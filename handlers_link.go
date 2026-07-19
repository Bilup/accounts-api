package main

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const linkCodeTTL = 10 * time.Minute

type linkCodeEntry struct {
	token     string
	createdAt time.Time
}

var usedCodesMutex sync.Mutex
var usedCodes = make(map[string]linkCodeEntry)

func purgeExpiredLinkCodesLocked() {
	cutoff := time.Now().Add(-linkCodeTTL)
	for code, entry := range usedCodes {
		if entry.createdAt.Before(cutoff) {
			delete(usedCodes, code)
		}
	}
}

func generateUniqueLinkCode() string {
	for {
		b := make([]byte, 3)
		if _, err := rand.Read(b); err != nil {
			panic("failed to generate link code: " + err.Error())
		}
		code := strings.ToUpper(hex.EncodeToString(b))

		usedCodesMutex.Lock()
		purgeExpiredLinkCodesLocked()
		if _, exists := usedCodes[code]; !exists {
			usedCodes[code] = linkCodeEntry{createdAt: time.Now()}
			usedCodesMutex.Unlock()
			return code
		}
		usedCodesMutex.Unlock()
	}
}

func linkCodeValid(entry linkCodeEntry) bool {
	return time.Since(entry.createdAt) < linkCodeTTL
}

func getLinkCode(c *gin.Context) {
	code := generateUniqueLinkCode()
	c.JSON(200, gin.H{"code": code})
}

func linkCodeToAccount(c *gin.Context) {
	code := c.Query("code")

	usedCodesMutex.Lock()
	defer usedCodesMutex.Unlock()
	if entry, exists := usedCodes[code]; exists && linkCodeValid(entry) {
		user := currentUser(c)
		entry.token = user.GetKey()
		usedCodes[code] = entry
		c.JSON(200, "Linked Successfully")
		return
	}
	c.JSON(404, gin.H{"error": "No auth code found"})
}

func getLinkStatus(c *gin.Context) {
	code := c.Query("code")
	usedCodesMutex.Lock()
	defer usedCodesMutex.Unlock()
	if entry, exists := usedCodes[code]; exists && entry.token != "" && linkCodeValid(entry) {
		c.JSON(200, gin.H{"status": "linked"})
	} else {
		c.JSON(404, gin.H{"status": "not found"})
	}
}

func getLinkedUser(c *gin.Context) {
	code := c.Query("code")
	usedCodesMutex.Lock()
	defer usedCodesMutex.Unlock()
	if entry, exists := usedCodes[code]; exists && entry.token != "" && linkCodeValid(entry) {
		delete(usedCodes, code)
		c.JSON(200, gin.H{"linked": true, "token": entry.token})
	} else {
		c.JSON(404, gin.H{"linked": false, "token": ""})
	}
}
