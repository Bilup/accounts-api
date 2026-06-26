package main

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const bookmarksFilePath = "./clawbookmarks.json"
const scheduledFilePath = "./clawscheduled.json"

var (
	bookmarks      = map[UserId][]string{}
	bookmarksMutex sync.RWMutex

	scheduledPosts []Post
	scheduledMutex sync.Mutex
)

func loadFeatureStores() {
	if data, err := os.ReadFile(bookmarksFilePath); err == nil {
		_ = json.Unmarshal(data, &bookmarks)
	}
	if bookmarks == nil {
		bookmarks = map[UserId][]string{}
	}
	if data, err := os.ReadFile(scheduledFilePath); err == nil {
		_ = json.Unmarshal(data, &scheduledPosts)
	}
}

func saveBookmarks() {
	bookmarksMutex.RLock()
	data, err := json.Marshal(bookmarks)
	bookmarksMutex.RUnlock()
	if err == nil {
		_ = os.WriteFile(bookmarksFilePath, data, 0644)
	}
}

func saveScheduledPosts() {
	scheduledMutex.Lock()
	data, err := json.Marshal(scheduledPosts)
	scheduledMutex.Unlock()
	if err == nil {
		_ = os.WriteFile(scheduledFilePath, data, 0644)
	}
}

// ---- Views ----

func viewPost(c *gin.Context) {
	user := c.MustGet("user").(*User)
	uid := user.GetId()

	batch := c.Query("ids") != ""
	var ids []string
	if batch {
		for _, raw := range strings.Split(c.Query("ids"), ",") {
			if id := strings.TrimSpace(raw); id != "" {
				ids = append(ids, id)
			}
		}
	} else {
		id := c.Query("id")
		if id == "" {
			c.JSON(400, gin.H{"error": "Post ID is required"})
			return
		}
		ids = []string{id}
	}

	counts := make(map[string]int, len(ids))
	changed := false
	postsMutex.Lock()
	byId := make(map[string]*Post, len(posts))
	for i := range posts {
		byId[posts[i].ID] = &posts[i]
	}
	for _, id := range ids {
		if _, done := counts[id]; done {
			continue
		}
		target := byId[id]
		if target == nil {
			continue
		}
		already := false
		for _, v := range target.Viewers {
			if v == uid {
				already = true
				break
			}
		}
		if !already {
			target.Viewers = append(target.Viewers, uid)
			changed = true
		}
		counts[id] = len(target.Viewers)
	}
	postsMutex.Unlock()
	if changed {
		go savePosts()
	}

	if batch {
		c.JSON(200, gin.H{"views": counts})
		return
	}
	views, ok := counts[ids[0]]
	if !ok {
		c.JSON(404, gin.H{"error": "Post not found"})
		return
	}
	c.JSON(200, gin.H{"views": views})
}

// ---- Bookmarks (Plus+) ----

func getBookmarks(c *gin.Context) {
	user := c.MustGet("user").(*User)
	bookmarksMutex.RLock()
	ids := append([]string{}, bookmarks[user.GetId()]...)
	bookmarksMutex.RUnlock()

	out := make([]NetPost, 0, len(ids))
	postsMutex.RLock()
	byId := make(map[string]*Post, len(posts))
	for i := range posts {
		byId[posts[i].ID] = &posts[i]
	}
	for _, id := range ids {
		if p, ok := byId[id]; ok {
			out = append(out, p.ToNet())
		}
	}
	postsMutex.RUnlock()
	c.JSON(200, out)
}

func addBookmark(c *gin.Context) {
	user := c.MustGet("user").(*User)
	if !hasTierOrHigher(user.GetSubscription().Tier, "Plus") {
		c.JSON(403, gin.H{"error": "Saving posts requires a Plus subscription or higher"})
		return
	}
	id := c.Query("id")
	if id == "" {
		c.JSON(400, gin.H{"error": "Post ID is required"})
		return
	}
	if getPostById(id) == nil {
		c.JSON(404, gin.H{"error": "Post not found"})
		return
	}
	uid := user.GetId()
	bookmarksMutex.Lock()
	list := bookmarks[uid]
	for _, existing := range list {
		if existing == id {
			bookmarksMutex.Unlock()
			c.JSON(200, gin.H{"message": "Already saved"})
			return
		}
	}
	list = append([]string{id}, list...)
	if len(list) > 200 {
		list = list[:200]
	}
	bookmarks[uid] = list
	bookmarksMutex.Unlock()
	go saveBookmarks()
	c.JSON(200, gin.H{"message": "Saved"})
}

func removeBookmark(c *gin.Context) {
	user := c.MustGet("user").(*User)
	id := c.Query("id")
	uid := user.GetId()
	bookmarksMutex.Lock()
	list := bookmarks[uid]
	next := make([]string, 0, len(list))
	for _, existing := range list {
		if existing != id {
			next = append(next, existing)
		}
	}
	bookmarks[uid] = next
	bookmarksMutex.Unlock()
	go saveBookmarks()
	c.JSON(200, gin.H{"message": "Removed"})
}

// ---- Poll voting ----

func votePoll(c *gin.Context) {
	user := c.MustGet("user").(*User)
	id := c.Query("id")
	optionStr := c.Query("option")
	if id == "" || optionStr == "" {
		c.JSON(400, gin.H{"error": "Post ID and option are required"})
		return
	}
	option, err := strconv.Atoi(optionStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "Invalid option"})
		return
	}
	target := getPostById(id)
	if target == nil || target.Poll == nil {
		c.JSON(404, gin.H{"error": "Poll not found"})
		return
	}
	if option < 0 || option >= len(target.Poll.Options) {
		c.JSON(400, gin.H{"error": "Option out of range"})
		return
	}
	postsMutex.Lock()
	if target.Poll.Votes == nil {
		target.Poll.Votes = map[UserId]int{}
	}
	target.Poll.Votes[user.GetId()] = option
	netPoll := target.Poll.ToNet()
	profileOnly := target.ProfileOnly
	postsMutex.Unlock()
	go savePosts()

	if !profileOnly {
		go broadcastClawEvent("update_post", map[string]any{
			"id":   id,
			"key":  "poll",
			"data": netPoll,
		})
	}
	respPoll := *netPoll
	respPoll.Voted = &option
	c.JSON(200, gin.H{"poll": &respPoll})
}

// ---- Scheduled posts (Plus+) ----

func addScheduledPost(p Post) {
	scheduledMutex.Lock()
	scheduledPosts = append(scheduledPosts, p)
	scheduledMutex.Unlock()
	go saveScheduledPosts()
}

func getMyScheduled(c *gin.Context) {
	user := c.MustGet("user").(*User)
	uid := user.GetId()
	scheduledMutex.Lock()
	out := make([]NetPost, 0)
	for i := range scheduledPosts {
		if scheduledPosts[i].User == uid {
			out = append(out, scheduledPosts[i].ToNet())
		}
	}
	scheduledMutex.Unlock()
	c.JSON(200, out)
}

func scheduleLoop() {
	for {
		time.Sleep(15 * time.Second)
		now := time.Now().UnixMilli()
		scheduledMutex.Lock()
		if len(scheduledPosts) == 0 {
			scheduledMutex.Unlock()
			continue
		}
		due := make([]Post, 0)
		remaining := make([]Post, 0, len(scheduledPosts))
		for _, p := range scheduledPosts {
			if p.PublishAt <= now {
				due = append(due, p)
			} else {
				remaining = append(remaining, p)
			}
		}
		scheduledPosts = remaining
		scheduledMutex.Unlock()

		if len(due) == 0 {
			continue
		}

		for _, p := range due {
			p.Timestamp = time.Now().UnixMilli()
			p.PublishAt = 0
			postsMutex.Lock()
			posts = append(posts, p)
			postsMutex.Unlock()
			if !p.ProfileOnly {
				netPost := p.ToNet()
				broadcastClawEvent("new_post", netPost)
				sendPostToDiscord(netPost)
				notifyMentions(p.Content, p.User, p.ID)
			}
		}
		go savePosts()
		go saveScheduledPosts()
	}
}
