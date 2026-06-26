package main

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

func nextRecoveryLevel(current StandingLevel) StandingLevel {
	switch current {
	case StandingSuspended:
		return StandingWarning
	case StandingWarning:
		return StandingGood
	case StandingBanned:
		return StandingWarning
	default:
		return StandingGood
	}
}

func setStandingAdmin(c *gin.Context) {
	if !authenticateAdmin(c) {
		return
	}
	type Request struct {
		Username string        `json:"username"`
		Level    StandingLevel `json:"level"`
		Reason   string        `json:"reason"`
	}

	var req Request
	if !bindJSON(c, &req) {
		return
	}

	if !requireField(c, req.Level, "standing level is required") {
		return
	}

	if !requireField(c, req.Reason, "reason is required") {
		return
	}

	user, ok := resolveUserByName(c, req.Username)
	if !ok {
		return
	}

	adminId := c.GetHeader("X-Admin-ID")
	if adminId == "" {
		adminId = "unknown"
	}

	user.SetStanding(req.Level, req.Reason, UserId(adminId))

	go saveUsers()

	c.JSON(200, gin.H{
		"success":  true,
		"username": req.Username,
		"standing": user.GetStanding(),
	})
}

func getStandingHistoryAdmin(c *gin.Context) {
	if !authenticateAdmin(c) {
		return
	}
	type Request struct {
		Username string `json:"username"`
	}

	var req Request
	if !bindJSON(c, &req) {
		return
	}

	if !requireField(c, req.Username, "username is required") {
		return
	}

	user, ok := resolveUserByName(c, req.Username)
	if !ok {
		return
	}

	history := user.GetStandingHistory()

	c.JSON(200, gin.H{
		"username": req.Username,
		"standing": user.GetStanding(),
		"history":  history,
	})
}

func recoverStandingAdmin(c *gin.Context) {
	if !authenticateAdmin(c) {
		return
	}
	type Request struct {
		Username string `json:"username"`
		Reason   string `json:"reason"`
	}

	var req Request
	if !bindJSON(c, &req) {
		return
	}

	if !requireField(c, req.Username, "username is required") {
		return
	}

	if !requireField(c, req.Reason, "reason is required") {
		return
	}

	user, ok := resolveUserByName(c, req.Username)
	if !ok {
		return
	}

	current := user.GetStanding()
	if current == StandingGood {
		c.JSON(400, gin.H{"error": "user is already in good standing"})
		return
	}

	adminId := c.GetHeader("X-Admin-ID")
	if adminId == "" {
		adminId = "unknown"
	}

	newLevel := nextRecoveryLevel(current)
	user.SetStanding(newLevel, req.Reason, UserId(adminId))

	go saveUsers()

	c.JSON(200, gin.H{
		"success":           true,
		"username":          req.Username,
		"previous_standing": current,
		"new_standing":      newLevel,
	})
}

func startStandingRecoveryChecker() {
	go func() {
		for {
			time.Sleep(5 * time.Minute)

			type recoveryEntry struct {
				Id       UserId
				NewLevel StandingLevel
			}
			var pending []recoveryEntry
			now := time.Now().Unix()

			usersMutex.RLock()
			for i := range users {
				user := &users[i]
				recoverAt := user.GetStandingRecoverAt()
				if recoverAt > 0 && recoverAt < now {
					standing := user.GetStanding()
					var newLevel StandingLevel
					switch standing {
					case StandingWarning:
						newLevel = StandingGood
					case StandingSuspended:
						newLevel = StandingWarning
					default:
						continue
					}
					pending = append(pending, recoveryEntry{Id: user.GetId(), NewLevel: newLevel})
				}
			}
			usersMutex.RUnlock()

			if len(pending) > 0 {
				for _, entry := range pending {
					if u := getUserById(entry.Id); len(u) > 0 {
						u.SetStanding(entry.NewLevel, "Automatic recovery", UserId("system"))
					}
				}
				go saveUsers()
			}
		}
	}()
}

func GetUserStanding(c *gin.Context) {
	username := c.Query("username")
	if !requireField(c, username, "username is required") {
		return
	}

	user, ok := resolveUserByName(c, username)
	if !ok {
		return
	}

	recoverAt := user.GetStandingRecoverAt()

	c.JSON(http.StatusOK, gin.H{
		"username":   username,
		"standing":   user.GetStanding(),
		"recover_at": recoverAt,
		"history":    user.GetStandingHistory(),
	})
}
