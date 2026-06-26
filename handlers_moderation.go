package main

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const reportsFilePath = "./clawreports.json"

const (
	ReportTargetPost  = "post"
	ReportTargetReply = "reply"
	ReportTargetUser  = "user"
)

const (
	ReportStatusOpen      = "open"
	ReportStatusResolved  = "resolved"
	ReportStatusDismissed = "dismissed"
)

type Report struct {
	ID         string `json:"id"`
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	TargetUser UserId `json:"target_user,omitempty"`
	Reporter   UserId `json:"reporter"`
	Reason     string `json:"reason"`
	Snapshot   string `json:"snapshot,omitempty"`
	Status     string `json:"status"`
	Timestamp  int64  `json:"timestamp"`
	ResolvedBy UserId `json:"resolved_by,omitempty"`
	ResolvedAt int64  `json:"resolved_at,omitempty"`
	Note       string `json:"note,omitempty"`
}

type ReportNet struct {
	ID             string        `json:"id"`
	TargetType     string        `json:"target_type"`
	TargetID       string        `json:"target_id"`
	TargetUser     Username      `json:"target_user,omitempty"`
	Reporter       Username      `json:"reporter"`
	Reason         string        `json:"reason"`
	Snapshot       string        `json:"snapshot,omitempty"`
	Status         string        `json:"status"`
	Timestamp      int64         `json:"timestamp"`
	ResolvedBy     Username      `json:"resolved_by,omitempty"`
	ResolvedAt     int64         `json:"resolved_at,omitempty"`
	Note           string        `json:"note,omitempty"`
	TargetStanding StandingLevel `json:"target_standing,omitempty"`
}

var (
	reports      []Report
	reportsMutex sync.Mutex
)

func loadReports() {
	reportsMutex.Lock()
	defer reportsMutex.Unlock()
	if data, err := os.ReadFile(reportsFilePath); err == nil {
		_ = json.Unmarshal(data, &reports)
	}
	if reports == nil {
		reports = []Report{}
	}
}

func saveReports() {
	reportsMutex.Lock()
	data, err := json.Marshal(reports)
	reportsMutex.Unlock()
	if err == nil {
		_ = os.WriteFile(reportsFilePath, data, 0644)
	}
}

func findReply(replyID string) (content string, author UserId, found bool) {
	postsMutex.RLock()
	defer postsMutex.RUnlock()
	for i := range posts {
		for j := range posts[i].Replies {
			if posts[i].Replies[j].ID == replyID {
				return posts[i].Replies[j].Content, posts[i].Replies[j].User, true
			}
		}
	}
	return "", "", false
}

func reportToNet(r Report) ReportNet {
	net := ReportNet{
		ID:         r.ID,
		TargetType: r.TargetType,
		TargetID:   r.TargetID,
		Reporter:   resolveUser(r.Reporter),
		Reason:     r.Reason,
		Snapshot:   r.Snapshot,
		Status:     r.Status,
		Timestamp:  r.Timestamp,
		ResolvedAt: r.ResolvedAt,
		Note:       r.Note,
	}
	if r.TargetUser != "" {
		net.TargetUser = resolveUser(r.TargetUser)
		if u := getUserById(r.TargetUser); len(u) > 0 {
			net.TargetStanding = u.GetStanding()
		}
	}
	if r.ResolvedBy != "" {
		net.ResolvedBy = resolveUser(r.ResolvedBy)
	}
	return net
}

func submitReport(c *gin.Context) {
	user := c.MustGet("user").(*User)

	type Request struct {
		TargetType string `json:"target_type"`
		TargetID   string `json:"target_id"`
		Reason     string `json:"reason"`
	}
	var req Request
	if !bindJSON(c, &req) {
		return
	}

	req.TargetType = strings.ToLower(strings.TrimSpace(req.TargetType))
	req.TargetID = strings.TrimSpace(req.TargetID)
	req.Reason = strings.TrimSpace(req.Reason)

	if req.TargetID == "" {
		c.JSON(400, gin.H{"error": "target_id is required"})
		return
	}
	if req.Reason == "" {
		c.JSON(400, gin.H{"error": "reason is required"})
		return
	}
	if len(req.Reason) > 1000 {
		c.JSON(400, gin.H{"error": "reason is too long"})
		return
	}

	var snapshot string
	var targetUser UserId

	switch req.TargetType {
	case ReportTargetPost:
		post := getPostById(req.TargetID)
		if post == nil {
			c.JSON(404, gin.H{"error": "post not found"})
			return
		}
		snapshot = post.Content
		targetUser = post.User
	case ReportTargetReply:
		content, author, found := findReply(req.TargetID)
		if !found {
			c.JSON(404, gin.H{"error": "reply not found"})
			return
		}
		snapshot = content
		targetUser = author
	case ReportTargetUser:
		targetUser = UserId(req.TargetID)
		if len(getUserById(targetUser)) == 0 {
			if id := getIdByUsername(Username(req.TargetID)); id != "" {
				targetUser = id
			} else {
				c.JSON(404, gin.H{"error": "user not found"})
				return
			}
		}
	default:
		c.JSON(400, gin.H{"error": "target_type must be post, reply or user"})
		return
	}

	reporter := user.GetId()
	if targetUser == reporter {
		c.JSON(400, gin.H{"error": "you cannot report yourself"})
		return
	}

	reportsMutex.Lock()
	for i := range reports {
		if reports[i].Status == ReportStatusOpen &&
			reports[i].Reporter == reporter &&
			reports[i].TargetType == req.TargetType &&
			reports[i].TargetID == req.TargetID {
			reportsMutex.Unlock()
			c.JSON(409, gin.H{"error": "you have already reported this"})
			return
		}
	}
	report := Report{
		ID:         generateToken(),
		TargetType: req.TargetType,
		TargetID:   req.TargetID,
		TargetUser: targetUser,
		Reporter:   reporter,
		Reason:     req.Reason,
		Snapshot:   snapshot,
		Status:     ReportStatusOpen,
		Timestamp:  time.Now().Unix(),
	}
	reports = append(reports, report)
	reportsMutex.Unlock()

	go saveReports()

	c.JSON(201, gin.H{"success": true, "report_id": report.ID})
}

func modListReports(c *gin.Context) {
	status := strings.ToLower(strings.TrimSpace(c.DefaultQuery("status", ReportStatusOpen)))

	reportsMutex.Lock()
	out := make([]ReportNet, 0, len(reports))
	for i := range reports {
		if status != "all" && reports[i].Status != status {
			continue
		}
		out = append(out, reportToNet(reports[i]))
	}
	reportsMutex.Unlock()

	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp > out[j].Timestamp })

	c.JSON(200, gin.H{"reports": out})
}

func modResolveReport(c *gin.Context) {
	mod := c.MustGet("user").(*User)

	type Request struct {
		ID     string `json:"id"`
		Action string `json:"action"`
		Note   string `json:"note"`
	}
	var req Request
	if !bindJSON(c, &req) {
		return
	}

	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	var newStatus string
	switch req.Action {
	case "resolve", "resolved":
		newStatus = ReportStatusResolved
	case "dismiss", "dismissed":
		newStatus = ReportStatusDismissed
	default:
		c.JSON(400, gin.H{"error": "action must be resolve or dismiss"})
		return
	}

	reportsMutex.Lock()
	found := false
	for i := range reports {
		if reports[i].ID == req.ID {
			reports[i].Status = newStatus
			reports[i].ResolvedBy = mod.GetId()
			reports[i].ResolvedAt = time.Now().Unix()
			reports[i].Note = strings.TrimSpace(req.Note)
			found = true
			break
		}
	}
	reportsMutex.Unlock()

	if !found {
		c.JSON(404, gin.H{"error": "report not found"})
		return
	}

	go saveReports()
	c.JSON(200, gin.H{"success": true, "status": newStatus})
}

func resolveModTarget(c *gin.Context, mod *User, username string) (User, bool) {
	userId := getIdByUsername(Username(username))
	if userId == "" {
		c.JSON(404, gin.H{"error": "user not found"})
		return nil, false
	}
	usersMutex.RLock()
	target := getUserById(userId)
	usersMutex.RUnlock()
	if len(target) == 0 {
		c.JSON(404, gin.H{"error": "user not found"})
		return nil, false
	}
	if target.GetId() == mod.GetId() {
		c.JSON(400, gin.H{"error": "you cannot change your own standing"})
		return nil, false
	}
	if target.IsModerator() {
		c.JSON(403, gin.H{"error": "moderators cannot act on other moderators"})
		return nil, false
	}
	return target, true
}

func modSetStanding(c *gin.Context) {
	mod := c.MustGet("user").(*User)

	type Request struct {
		Username string        `json:"username"`
		Level    StandingLevel `json:"level"`
		Reason   string        `json:"reason"`
	}
	var req Request
	if !bindJSON(c, &req) {
		return
	}
	if req.Level == "" {
		c.JSON(400, gin.H{"error": "standing level is required"})
		return
	}
	switch req.Level {
	case StandingGood, StandingWarning, StandingSuspended, StandingBanned:
	default:
		c.JSON(400, gin.H{"error": "invalid standing level"})
		return
	}
	if req.Reason == "" {
		c.JSON(400, gin.H{"error": "reason is required"})
		return
	}

	target, ok := resolveModTarget(c, mod, req.Username)
	if !ok {
		return
	}

	target.SetStanding(req.Level, req.Reason, mod.GetId())
	go saveUsers()

	c.JSON(200, gin.H{
		"success":  true,
		"username": req.Username,
		"standing": target.GetStanding(),
	})
}

func modRecoverStanding(c *gin.Context) {
	mod := c.MustGet("user").(*User)

	type Request struct {
		Username string `json:"username"`
		Reason   string `json:"reason"`
	}
	var req Request
	if !bindJSON(c, &req) {
		return
	}
	if req.Reason == "" {
		c.JSON(400, gin.H{"error": "reason is required"})
		return
	}

	target, ok := resolveModTarget(c, mod, req.Username)
	if !ok {
		return
	}

	current := target.GetStanding()
	if current == StandingGood {
		c.JSON(400, gin.H{"error": "user is already in good standing"})
		return
	}

	var newLevel StandingLevel
	switch current {
	case StandingSuspended:
		newLevel = StandingWarning
	case StandingWarning:
		newLevel = StandingGood
	case StandingBanned:
		newLevel = StandingWarning
	default:
		newLevel = StandingGood
	}

	target.SetStanding(newLevel, req.Reason, mod.GetId())
	go saveUsers()

	c.JSON(200, gin.H{
		"success":           true,
		"username":          req.Username,
		"previous_standing": current,
		"new_standing":      newLevel,
	})
}

func modGetStandingHistory(c *gin.Context) {
	type Request struct {
		Username string `json:"username"`
	}
	var req Request
	if !bindJSON(c, &req) {
		return
	}
	if req.Username == "" {
		c.JSON(400, gin.H{"error": "username is required"})
		return
	}

	userId := getIdByUsername(Username(req.Username))
	if userId == "" {
		c.JSON(404, gin.H{"error": "user not found"})
		return
	}
	usersMutex.RLock()
	user := getUserById(userId)
	usersMutex.RUnlock()
	if len(user) == 0 {
		c.JSON(404, gin.H{"error": "user not found"})
		return
	}

	c.JSON(200, gin.H{
		"username":   req.Username,
		"standing":   user.GetStanding(),
		"recover_at": user.GetStandingRecoverAt(),
		"history":    user.GetStandingHistory(),
	})
}

func modSetModerator(c *gin.Context) {
	type Request struct {
		Username  string `json:"username"`
		Moderator bool   `json:"moderator"`
	}
	var req Request
	if !bindJSON(c, &req) {
		return
	}
	if req.Username == "" {
		c.JSON(400, gin.H{"error": "username is required"})
		return
	}

	userId := getIdByUsername(Username(req.Username))
	if userId == "" {
		c.JSON(404, gin.H{"error": "user not found"})
		return
	}
	usersMutex.RLock()
	user := getUserById(userId)
	usersMutex.RUnlock()
	if len(user) == 0 {
		c.JSON(404, gin.H{"error": "user not found"})
		return
	}
	if user.IsNetworkAdmin() {
		c.JSON(400, gin.H{"error": "user is a network admin and is always a moderator"})
		return
	}

	user.SetModerator(req.Moderator)
	go saveUsers()

	c.JSON(200, gin.H{
		"success":   true,
		"username":  req.Username,
		"moderator": req.Moderator,
	})
}

func modListModerators(c *gin.Context) {
	type Mod struct {
		Username Username `json:"username"`
		Admin    bool     `json:"admin"`
	}
	out := []Mod{}
	usersMutex.RLock()
	for i := range users {
		u := users[i]
		if u.IsModerator() {
			out = append(out, Mod{Username: u.GetUsername(), Admin: u.IsNetworkAdmin()})
		}
	}
	usersMutex.RUnlock()

	sort.Slice(out, func(i, j int) bool {
		if out[i].Admin != out[j].Admin {
			return out[i].Admin
		}
		return out[i].Username < out[j].Username
	})

	c.JSON(200, gin.H{"moderators": out})
}

func setModeratorAdmin(c *gin.Context) {
	if !authenticateAdmin(c) {
		return
	}
	type Request struct {
		Username  string `json:"username"`
		Moderator bool   `json:"moderator"`
	}
	var req Request
	if !bindJSON(c, &req) {
		return
	}
	if req.Username == "" {
		c.JSON(400, gin.H{"error": "username is required"})
		return
	}

	userId := getIdByUsername(Username(req.Username))
	if userId == "" {
		c.JSON(404, gin.H{"error": "user not found"})
		return
	}
	usersMutex.RLock()
	user := getUserById(userId)
	usersMutex.RUnlock()
	if len(user) == 0 {
		c.JSON(404, gin.H{"error": "user not found"})
		return
	}

	user.SetModerator(req.Moderator)
	go saveUsers()

	c.JSON(200, gin.H{
		"success":   true,
		"username":  req.Username,
		"moderator": req.Moderator,
	})
}
