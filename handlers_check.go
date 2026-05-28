package main

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func checkBanned(c *gin.Context) {
	body, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		return
	}

	banned := make([]string, 0)

	for username := range strings.SplitSeq(string(body), ",") {
		username = strings.TrimSpace(username)
		if username == "" {
			continue
		}

		user, err := getAccountByUsername(username)
		if err != nil {
			user = getUserById(UserId(username))
		}

		if user != nil && user.IsBanned() {
			banned = append(banned, username)
		}
	}

	c.JSON(http.StatusOK, gin.H{"banned": banned})
}
