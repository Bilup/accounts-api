package main

import (
	"encoding/json"
	"log"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func clawCanEdit(user *User) bool  { return hasTierOrHigher(user.GetSubscription().Tier, "Plus") }
func clawCanVideo(user *User) bool { return hasTierOrHigher(user.GetSubscription().Tier, "Pro") }

func clawPostLimit(user *User) int {
	t := user.GetSubscription().Tier
	switch {
	case hasTierOrHigher(t, "Max"):
		return 1000
	case hasTierOrHigher(t, "Pro"):
		return 800
	case hasTierOrHigher(t, "Plus"):
		return 600
	case hasTierOrHigher(t, "Lite"):
		return 400
	default:
		return 300
	}
}

func clawMaxPins(user *User) int {
	t := user.GetSubscription().Tier
	if hasTierOrHigher(t, "Pro") {
		return 5
	}
	if hasTierOrHigher(t, "Plus") {
		return 3
	}
	return 1
}

func clawMaxAttachments(user *User) int {
	t := user.GetSubscription().Tier
	if hasTierOrHigher(t, "Pro") {
		return 4
	}
	if hasTierOrHigher(t, "Plus") {
		return 2
	}
	return 1
}

func clawFeedLimit(user *User) int {
	if hasTierOrHigher(user.GetSubscription().Tier, "Plus") {
		return 200
	}
	return 100
}

var postLimits = map[string]int{
	"content_length":         300,
	"content_length_premium": 600,
	"attachment_length":      200,
}

var mentionRe = regexp.MustCompile(`@([a-zA-Z0-9_.-]{1,30})`)

func validateAttachmentURL(url string) string {
	if len(url) > postLimits["attachment_length"] {
		return "Attachment URL exceeds " + strconv.Itoa(postLimits["attachment_length"]) + " character limit"
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return "Attachment must be a valid URL"
	}
	if isFromBannedDomain(url) {
		return "Attachment from prohibited website"
	}
	allowed := []string{"image/png", "image/jpeg", "image/gif", "video/mp4", "video/webm"}
	if !isValidMimeType(url, allowed) {
		return "Attachment must be an image or video (PNG, JPEG, GIF, MP4, WEBM)"
	}
	return ""
}

func notifyMentions(content string, author UserId, postID string) {
	matches := mentionRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return
	}
	seen := map[UserId]bool{author: true}
	for _, m := range matches {
		uid := Username(m[1]).Id()
		if uid == "" || seen[uid] {
			continue
		}
		seen[uid] = true
		addUserEvent(uid, "mention", map[string]any{
			"user":    string(author),
			"post_id": postID,
			"content": content,
		})
	}
}

var lockedKeys = []string{"last_login", "max_size", "key", "created", "discord_id", "sys.id", "password"}

func getLimits(c *gin.Context) {
	c.JSON(200, postLimits)
}

func createPost(c *gin.Context) {
	rateLimitKey := getRateLimitKey(c)
	isAllowed, remaining, resetTime := applyRateLimit(rateLimitKey, "post")

	if !isAllowed {
		c.Header("X-RateLimit-Limit", strconv.Itoa(rateLimits["post"].Count))
		c.Header("X-RateLimit-Remaining", strconv.Itoa(remaining))
		c.Header("X-RateLimit-Reset", strconv.FormatFloat(resetTime, 'f', 0, 64))
		c.JSON(429, gin.H{"error": "Rate limit exceeded. Try again later."})
		return
	}

	user := currentUser(c)

	content := c.Query("content")

	osParam := c.Query("os")
	if osParam != "" {
		systems := getAllSystems()
		keys := make([]string, len(systems))
		i := 0
		for k := range systems {
			keys[i] = k
			i++
		}
		isValid := slices.Contains(keys, osParam)
		if !isValid {
			c.JSON(400, gin.H{"error": "OS is invalid"})
			return
		}
	}

	chars := clawPostLimit(user)

	// Check content length
	if len(content) > chars {
		c.JSON(400, gin.H{"error": "Content exceeds " + strconv.Itoa(chars) + " character limit"})
		return
	}

	// Get attachment if available
	var attachment *string
	if attachmentStr := c.Query("attachment"); attachmentStr != "" {
		if msg := validateAttachmentURL(attachmentStr); msg != "" {
			c.JSON(400, gin.H{"error": msg})
			return
		}
		attachment = &attachmentStr
	}

	var attachmentsList []string
	if multi := c.Query("attachments"); multi != "" {
		maxN := clawMaxAttachments(user)
		for _, raw := range strings.Split(multi, ",") {
			u := strings.TrimSpace(raw)
			if u == "" {
				continue
			}
			if len(attachmentsList) >= maxN {
				c.JSON(400, gin.H{"error": "Too many attachments (max " + strconv.Itoa(maxN) + " for your subscription tier)"})
				return
			}
			if msg := validateAttachmentURL(u); msg != "" {
				c.JSON(400, gin.H{"error": msg})
				return
			}
			attachmentsList = append(attachmentsList, u)
		}
		if len(attachmentsList) > 0 {
			first := attachmentsList[0]
			attachment = &first
		}
	}

	var poll *Poll
	if pollStr := c.Query("poll"); pollStr != "" {
		if !hasTierOrHigher(user.GetSubscription().Tier, "Plus") {
			c.JSON(403, gin.H{"error": "Polls require a Plus subscription or higher"})
			return
		}
		var opts []string
		if err := json.Unmarshal([]byte(pollStr), &opts); err != nil {
			c.JSON(400, gin.H{"error": "Invalid poll"})
			return
		}
		clean := make([]string, 0, len(opts))
		for _, o := range opts {
			o = strings.TrimSpace(o)
			if o != "" && len(o) <= 80 {
				clean = append(clean, o)
			}
		}
		if len(clean) < 2 || len(clean) > 6 {
			c.JSON(400, gin.H{"error": "A poll needs between 2 and 6 options"})
			return
		}
		poll = &Poll{Options: clean, Votes: map[UserId]int{}}
	}

	var publishAt int64
	if sched := c.Query("scheduled_for"); sched != "" {
		if !hasTierOrHigher(user.GetSubscription().Tier, "Plus") {
			c.JSON(403, gin.H{"error": "Scheduling posts requires a Plus subscription or higher"})
			return
		}
		ms, err := strconv.ParseInt(sched, 10, 64)
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid scheduled time"})
			return
		}
		if ms > time.Now().UnixMilli()+30000 {
			publishAt = ms
		}
	}

	if strings.TrimSpace(content) == "" && attachment == nil && len(attachmentsList) == 0 && poll == nil {
		c.JSON(400, gin.H{"error": "Post needs text, an attachment, or a poll"})
		return
	}

	// Check if post is profile-only
	profileOnly := c.Query("profile_only") == "1"

	// Create new post
	newPost := Post{
		ID:          generateToken(),
		Content:     content,
		User:        user.GetId(),
		Timestamp:   time.Now().UnixMilli(),
		Attachment:  attachment,
		Attachments: attachmentsList,
		ProfileOnly: profileOnly,
		Poll:        poll,
	}

	if osParam != "" {
		newPost.OS = &osParam
	}

	if publishAt > 0 {
		newPost.PublishAt = publishAt
		addScheduledPost(newPost)
		c.JSON(201, gin.H{"scheduled": true, "publish_at": publishAt, "id": newPost.ID})
		return
	}

	postsMutex.Lock()
	posts = append(posts, newPost)
	postsMutex.Unlock()

	go savePosts()

	// Send the new post to the WebSocket server only if it's not profile-only
	if !profileOnly {
		go func() {
			netPost := newPost.ToNet()
			broadcastClawEvent("new_post", netPost)
			sendPostToDiscord(netPost)
		}()
	}

	notifyMentions(content, user.GetId(), newPost.ID)

	c.JSON(201, newPost.ToNet())
}

func getPostById(id string) *Post {
	postsMutex.Lock()
	defer postsMutex.Unlock()

	var targetPost *Post = nil
	for i := range posts {
		if posts[i].ID == id {
			targetPost = &posts[i]
			break
		}
	}

	return targetPost
}

func mutatePostByID(id string, fn func(p *Post)) bool {
	postsMutex.Lock()
	defer postsMutex.Unlock()
	for i := range posts {
		if posts[i].ID == id {
			fn(&posts[i])
			return true
		}
	}
	return false
}

func lookupPostUnlocked(id string) *Post {
	for i := range posts {
		if posts[i].ID == id {
			return &posts[i]
		}
	}
	return nil
}

func getPost(c *gin.Context) {
	id := c.Query("id")
	if !requireField(c, id, "Post ID is required") {
		return
	}

	var viewer UserId
	if authKey := extractAuthKey(c); authKey != "" {
		if u := authenticateWithKey(authKey); u != nil {
			viewer = u.GetId()
		}
	}

	postsMutex.RLock()
	var net *NetPost
	for i := range posts {
		if posts[i].ID == id {
			n := posts[i].ToNet()
			if n.Poll != nil {
				n.Poll.Voted = posts[i].Poll.votedBy(viewer)
			}
			net = &n
			break
		}
	}
	postsMutex.RUnlock()

	if net == nil {
		c.JSON(404, gin.H{"error": "Post not found"})
		return
	}

	c.JSON(200, net)
}

func replyToPost(c *gin.Context) {
	// Rate limiting check

	user := currentUser(c)

	postID := c.Query("id")
	if !requireField(c, postID, "Post ID is required") {
		return
	}

	content := c.Query("content")
	if !requireField(c, content, "Content is required") {
		return
	}

	// Check content length
	postLimit := clawPostLimit(user)

	if len(content) > postLimit {
		c.JSON(400, gin.H{"error": "Content exceeds " + strconv.Itoa(postLimit) + " character limit"})
		return
	}

	targetPost := getPostById(postID)

	if targetPost == nil {
		c.JSON(404, gin.H{"error": "Post not found"})
		return
	}
	postOwner := targetPost.User

	foundUser, err := getAccountByUserId(postOwner)
	if err != nil || isUserBlockedBy(foundUser, user.GetId()) {
		c.JSON(400, gin.H{"error": "You cant reply to this post"})
		return
	}

	newReply := Reply{
		ID:        generateToken(),
		Content:   content,
		User:      user.GetId(),
		Timestamp: time.Now().UnixMilli(),
	}

	if !mutatePostByID(postID, func(p *Post) {
		p.Replies = append(p.Replies, newReply)
	}) {
		c.JSON(404, gin.H{"error": "Post not found"})
		return
	}

	go savePosts()

	addUserEvent(postOwner, "reply", map[string]any{
		"post_id":  postID,
		"reply_id": newReply.ID,
		"user":     user.GetId(),
		"content":  content,
	})

	notifyMentions(content, user.GetId(), postID)

	c.JSON(201, newReply.ToNet())
}

func deletePost(c *gin.Context) {
	user := currentUser(c)

	postID := c.Query("id")
	if !requireField(c, postID, "Post ID is required") {
		return
	}

	isMist := isHardcodedAdmin(user.GetUsername())
	userId := user.GetId()

	postsMutex.Lock()
	var postToDelete *Post
	newPosts := make([]Post, 0)

	for i := range posts { // use index to safely take address
		if posts[i].ID == postID {
			postToDelete = &posts[i]
			// Check if the user can delete this post
			if !isMist && userId != posts[i].User {
				postsMutex.Unlock()
				c.JSON(403, ErrorResponse{Error: "You cannot delete this post"})
				return
			}
		} else {
			newPosts = append(newPosts, posts[i])
		}
	}

	if postToDelete == nil {
		postsMutex.Unlock()
		c.JSON(404, ErrorResponse{Error: "Post not found"})
		return
	}

	wasPublic := !postToDelete.ProfileOnly
	posts = newPosts
	postsMutex.Unlock()

	go savePosts()

	// Broadcast deletion event for public posts
	if wasPublic {
		go broadcastClawEvent("delete_post", map[string]any{"id": postID})
	}

	c.JSON(200, gin.H{"message": "Post deleted successfully"})
}

func editPost(c *gin.Context) {
	user := currentUser(c)

	postID := c.Query("id")
	if !requireField(c, postID, "Post ID is required") {
		return
	}

	content := c.Query("content")
	if !requireField(c, content, "Content is required") {
		return
	}

	if !clawCanEdit(user) {
		c.JSON(403, gin.H{"error": "Editing posts requires a Plus subscription or higher"})
		return
	}

	chars := clawPostLimit(user)
	if len(content) > chars {
		c.JSON(400, gin.H{"error": "Content exceeds " + strconv.Itoa(chars) + " character limit"})
		return
	}

	targetPost := getPostById(postID)
	if targetPost == nil {
		c.JSON(404, gin.H{"error": "Post not found"})
		return
	}

	if targetPost.User != user.GetId() {
		c.JSON(403, gin.H{"error": "You can only edit your own posts"})
		return
	}

	if targetPost.IsRepost {
		c.JSON(400, gin.H{"error": "Reposts cannot be edited"})
		return
	}

	var editedAt int64
	var profileOnly bool
	if !mutatePostByID(postID, func(p *Post) {
		p.Content = content
		p.EditedAt = time.Now().UnixMilli()
		editedAt = p.EditedAt
		profileOnly = p.ProfileOnly
	}) {
		c.JSON(404, gin.H{"error": "Post not found"})
		return
	}

	go savePosts()

	if !profileOnly {
		go broadcastClawEvent("update_post", map[string]any{
			"id":   postID,
			"key":  "content",
			"data": map[string]any{"content": content, "edited_at": editedAt},
		})
	}

	c.JSON(200, gin.H{"message": "Post edited successfully", "edited_at": editedAt})
}

func ratePost(c *gin.Context) {
	user := currentUser(c)

	postID := c.Query("id")
	if !requireField(c, postID, "Post ID is required") {
		return
	}

	ratingStr := c.Query("rating")
	if !requireField(c, ratingStr, "Rating is required") {
		return
	}

	rating, err := strconv.Atoi(ratingStr)
	if err != nil || (rating != 0 && rating != 1) {
		c.JSON(400, gin.H{"error": "Rating must be 1 (like) or 0 (unlike)"})
		return
	}

	targetPost := getPostById(postID)

	if targetPost == nil {
		c.JSON(404, gin.H{"error": "Post not found"})
		return
	}

	if rating == 1 {
		foundUser, err := getAccountByUserId(targetPost.User)
		if err != nil || isUserBlockedBy(foundUser, user.GetId()) {
			c.JSON(400, gin.H{"error": "You cant like this post"})
			return
		}
	}

	userId := user.GetId()
	postOwner := targetPost.User

	var likesSnapshot []UserId
	var profileOnly bool
	liked := false
	if !mutatePostByID(postID, func(p *Post) {
		if rating == 1 {
			if !slices.Contains(p.Likes, userId) {
				p.Likes = append(p.Likes, userId)
				liked = true
			}
		} else {
			newLikes := make([]UserId, 0, len(p.Likes))
			for _, liker := range p.Likes {
				if liker != userId {
					newLikes = append(newLikes, liker)
				}
			}
			p.Likes = newLikes
		}
		likesSnapshot = append([]UserId(nil), p.Likes...)
		profileOnly = p.ProfileOnly
	}) {
		c.JSON(404, gin.H{"error": "Post not found"})
		return
	}

	go savePosts()

	// Broadcast rating update for public posts
	if !profileOnly {
		likes := make([]Username, 0, len(likesSnapshot))
		for _, liker := range likesSnapshot {
			likes = append(likes, liker.User().GetUsername())
		}
		go broadcastClawEvent("update_post", map[string]any{
			"id":   postID,
			"key":  "likes",
			"data": likes,
		})
		if liked {
			go broadcastClawEvent("new_like", map[string]any{
				"post_id": postID,
				"user":    user.GetUsername(),
				"likes":   likes,
			})
		}
	}

	if liked && postOwner != userId {
		addUserEvent(postOwner, "like", map[string]any{
			"post_id": postID,
			"user":    userId,
		})
	}

	c.JSON(200, gin.H{
		"message": "Post rated successfully",
		"likes":   likesSnapshot,
	})
}

func repost(c *gin.Context) {
	user := currentUser(c)

	postID := c.Query("id")
	if !requireField(c, postID, "Post ID is required") {
		return
	}

	// Find the original post by ID
	var originalPost *Post = getPostById(postID)

	if originalPost == nil {
		c.JSON(404, gin.H{"error": "Original post not found"})
		return
	}

	foundUser, err := getAccountByUserId(originalPost.User)
	if err != nil || isUserBlockedBy(foundUser, user.GetId()) {
		c.JSON(400, gin.H{"error": "You cant repost this post"})
		return
	}

	// Check if the original post is profile-only
	if originalPost.ProfileOnly {
		c.JSON(403, gin.H{"error": "Cannot repost a profile-only post"})
		return
	}

	if originalPost.IsRepost {
		c.JSON(403, gin.H{"error": "Cannot repost a repost"})
		return
	}

	content := c.Query("content")
	if content != "" {
		limit := clawPostLimit(user)
		if len(content) > limit {
			c.JSON(400, gin.H{"error": "Content exceeds " + strconv.Itoa(limit) + " character limit"})
			return
		}
	}

	quote := content != ""

	newRepost := Post{
		ID:             generateToken(),
		User:           user.GetId(),
		Content:        content,
		Timestamp:      time.Now().UnixMilli(),
		IsRepost:       true,
		OriginalPostID: originalPost.ID,
		ProfileOnly:    !quote,
	}

	postsMutex.Lock()
	posts = append(posts, newRepost)
	postsMutex.Unlock()

	go savePosts()

	if quote {
		go func() {
			postsMutex.RLock()
			netPost := newRepost.ToNet()
			postsMutex.RUnlock()
			broadcastClawEvent("new_post", netPost)
		}()
		notifyMentions(content, user.GetId(), newRepost.ID)
	}

	addUserEvent(originalPost.User, "repost", map[string]any{
		"repost_id":        newRepost.ID,
		"user":             user.GetId(),
		"original_post_id": originalPost.ID,
	})

	postsMutex.RLock()
	netRepost := newRepost.ToNet()
	postsMutex.RUnlock()
	c.JSON(201, netRepost)
}

func pinPost(c *gin.Context) {
	user := currentUser(c)

	postID := c.Query("id")
	if !requireField(c, postID, "Post ID is required") {
		return
	}

	var targetPost *Post = getPostById(postID)

	if targetPost == nil {
		c.JSON(404, gin.H{"error": "Post not found"})
		return
	}

	// Check if the user is the owner of the post
	if targetPost.User != user.GetId() {
		c.JSON(403, gin.H{"error": "You can only pin your own posts"})
		return
	}

	pinLimit := clawMaxPins(user)
	uid := user.GetId()
	pinnedCount := 0
	postsMutex.RLock()
	for i := range posts {
		if posts[i].User == uid && posts[i].Pinned && posts[i].ID != postID {
			pinnedCount++
		}
	}
	postsMutex.RUnlock()
	if pinnedCount >= pinLimit {
		c.JSON(403, gin.H{"error": "Pin limit reached (" + strconv.Itoa(pinLimit) + "). " + map[bool]string{true: "Unpin another post first.", false: "Claw Premium allows up to 5 pins."}[pinLimit == 5]})
		return
	}

	var profileOnly bool
	if !mutatePostByID(postID, func(p *Post) {
		p.Pinned = true
		profileOnly = p.ProfileOnly
	}) {
		c.JSON(404, gin.H{"error": "Post not found"})
		return
	}

	go savePosts()

	// Broadcast pin update for public posts
	if !profileOnly {
		go broadcastClawEvent("update_post", map[string]any{
			"id":   postID,
			"key":  "pinned",
			"data": true,
		})
	}

	c.JSON(200, gin.H{"message": "Post pinned successfully"})
}

func unpinPost(c *gin.Context) {
	user := currentUser(c)

	postID := c.Query("id")
	if !requireField(c, postID, "Post ID is required") {
		return
	}

	var targetPost *Post = getPostById(postID)

	if targetPost == nil {
		c.JSON(404, gin.H{"error": "Post not found"})
		return
	}

	// Check if the user is the owner of the post
	if targetPost.User != user.GetId() {
		c.JSON(403, gin.H{"error": "You can only unpin your own posts"})
		return
	}

	var profileOnly bool
	if !mutatePostByID(postID, func(p *Post) {
		p.Pinned = false
		profileOnly = p.ProfileOnly
	}) {
		c.JSON(404, gin.H{"error": "Post not found"})
		return
	}

	go savePosts()

	// Broadcast unpin update for public posts
	if !profileOnly {
		go broadcastClawEvent("update_post", map[string]any{
			"id":   postID,
			"key":  "pinned",
			"data": false,
		})
	}

	c.JSON(200, gin.H{"message": "Post unpinned successfully"})
}

func searchPosts(c *gin.Context) {
	query := c.Query("q")
	if !requireField(c, query, "Search query is required") {
		return
	}

	limit := queryLimit(c, 20, 20)

	queryLower := strings.ToLower(query)

	// Search posts by content
	searchResults := make([]NetPost, 0)
	postsMutex.RLock()
	for _, post := range posts {
		if strings.Contains(strings.ToLower(post.Content), queryLower) {
			searchResults = append(searchResults, post.ToNet())
			if len(searchResults) >= limit {
				break
			}
		}
	}
	postsMutex.RUnlock()

	// Reverse to show newest first
	for i := len(searchResults)/2 - 1; i >= 0; i-- {
		opp := len(searchResults) - 1 - i
		searchResults[i], searchResults[opp] = searchResults[opp], searchResults[i]
	}

	c.JSON(200, searchResults)
}

func getTopPosts(c *gin.Context) {
	limit := queryLimit(c, 50, 50)

	timePeriodStr := c.DefaultQuery("time_period", "24")
	timePeriod, err := strconv.Atoi(timePeriodStr)
	if err != nil || timePeriod <= 0 {
		timePeriod = 24
	}

	// Filter posts within the time period, excluding profile-only posts
	currentTime := time.Now().Unix()

	postsWithinPeriod := make([]NetPost, 0)
	postsMutex.RLock()
	for _, post := range posts {
		postTime := post.Timestamp / 1000 // Convert milliseconds to seconds
		if (currentTime-postTime) <= int64(timePeriod)*3600 && !post.ProfileOnly {
			postsWithinPeriod = append(postsWithinPeriod, post.ToNet())
		}
	}
	postsMutex.RUnlock()

	// Sort posts by likes count
	sort.Slice(postsWithinPeriod, func(i, j int) bool {
		return len(postsWithinPeriod[i].Likes) > len(postsWithinPeriod[j].Likes)
	})

	// Apply limit
	if len(postsWithinPeriod) > limit {
		postsWithinPeriod = postsWithinPeriod[:limit]
	}

	c.JSON(200, postsWithinPeriod)
}

var envOnce sync.Once

func loadEnvFile() {
	// Prefer workspace root .env (one directory up) then fall back to local .env.
	// Root file now holds authoritative configuration; local .env may override selectively if present.
	parent := "../.env"
	local := ".env"
	if _, err := os.Stat(parent); err == nil {
		if err := godotenv.Overload(parent); err != nil {
			log.Printf("[env] failed to load parent .env: %v", err)
		} else {
			log.Printf("[env] loaded parent .env (%s)", parent)
		}
	} else {
		log.Printf("[env] parent .env not found (%s): %v", parent, err)
	}
	if _, err := os.Stat(local); err == nil {
		if err := godotenv.Overload(local); err != nil {
			log.Printf("[env] failed to load local .env overrides: %v", err)
		} else {
			log.Printf("[env] loaded local .env overrides (%s)", local)
		}
	}
	// Reload config variables after populating environment
	loadConfigFromEnv()
}

func getFeed(c *gin.Context) {
	limit := queryLimit(c, 100, 100)

	offsetStr := c.DefaultQuery("offset", "0")
	offset, err := strconv.Atoi(offsetStr)
	if err != nil || offset < 0 {
		offset = 0
	}

	var viewer UserId
	if authKey := extractAuthKey(c); authKey != "" {
		if u := authenticateWithKey(authKey); u != nil {
			viewer = u.GetId()
		}
	}

	postsMutex.RLock()
	publicPosts := make([]NetPost, 0)
	for i := range posts {
		post := posts[i]
		if !post.ProfileOnly {
			np := post.ToNet()
			if np.Poll != nil {
				np.Poll.Voted = post.Poll.votedBy(viewer)
			}
			publicPosts = append(publicPosts, np)
		}
	}
	postsMutex.RUnlock()

	// Sort by timestamp (newest first)
	sort.Slice(publicPosts, func(i, j int) bool {
		return publicPosts[i].Timestamp > publicPosts[j].Timestamp
	})

	// Apply offset and limit to get the newest posts
	start := offset
	end := offset + limit
	if start > len(publicPosts) {
		start = len(publicPosts)
	}
	if end > len(publicPosts) {
		end = len(publicPosts)
	}
	result := publicPosts[start:end]

	c.JSON(200, result)
}

func getFollowingFeed(c *gin.Context) {
	user := currentUser(c)

	maxLimit := clawFeedLimit(user)
	limit := queryLimit(c, 100, maxLimit)

	userId := user.GetId()

	// Get the list of users the authenticated user is following
	following := make([]UserId, 0)
	followersMutex.RLock()
	for targetUser, data := range followersData {
		if slices.Contains(data.Followers, userId) {
			following = append(following, targetUser)
		}
	}
	followersMutex.RUnlock()

	// Get posts from followed users (excluding profile-only posts from other users)
	postsMutex.RLock()
	followingPosts := make([]NetPost, 0)
	for i := range posts {
		post := posts[i]
		isFollowed := slices.Contains(following, post.User)
		if isFollowed && !(post.ProfileOnly && post.User != userId) {
			np := post.ToNet()
			if np.Poll != nil {
				np.Poll.Voted = post.Poll.votedBy(userId)
			}
			followingPosts = append(followingPosts, np)
		}
	}
	postsMutex.RUnlock()

	// Sort by timestamp (newest first)
	sort.Slice(followingPosts, func(i, j int) bool {
		return followingPosts[i].Timestamp > followingPosts[j].Timestamp
	})

	// Apply limit - take the first (newest) entries after sorting
	var result = make([]NetPost, 0)
	if len(followingPosts) > limit {
		result = followingPosts[:limit]
	} else {
		result = followingPosts
	}

	c.JSON(200, result)
}
