package main

import (
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

var hardcodedAdmins = map[string]bool{
	"mist": true,
}

func isHardcodedAdmin(username Username) bool {
	return hardcodedAdmins[strings.ToLower(string(username))]
}

func authenticateWithKey(key string) *User {
	idToUserMutex.RLock()
	idx, ok := keyToUserIdx[key]
	idToUserMutex.RUnlock()
	if ok {
		usersMutex.RLock()
		user := users[idx]
		usersMutex.RUnlock()
		if user.GetKey() == key {
			return &user
		}
	}

	usersMutex.RLock()
	defer usersMutex.RUnlock()
	for i := range users {
		if users[i].GetKey() == key {
			return &users[i]
		}
	}
	return nil
}

func doesUserOwnKey(userId UserId, key string) bool {
	keysMutex.RLock()
	idx, ok := keyStringToIdx[key]
	if !ok {
		keysMutex.RUnlock()
		return false
	}
	userKey := keys[idx]
	keysMutex.RUnlock()

	_, exists := userKey.Users[userId]
	return exists
}

func getKeyNextBilling(userId UserId, key string) int64 {
	keysMutex.RLock()
	defer keysMutex.RUnlock()
	idx, ok := keyStringToIdx[key]
	if !ok {
		return 0
	}
	userData, exists := keys[idx].Users[userId]
	if !exists {
		return 0
	}
	nextBilling := userData.NextBilling
	if nextBilling == nil {
		return 0
	}
	switch v := nextBilling.(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	default:
		return 0
	}
}

func setKeyNextBilling(userId UserId, key string, nextBilling int64) bool {
	keysMutex.Lock()
	idx, ok := keyStringToIdx[key]
	if !ok {
		keysMutex.Unlock()
		return false
	}
	userData, exists := keys[idx].Users[userId]
	if !exists {
		keysMutex.Unlock()
		return false
	}
	userData.NextBilling = nextBilling
	keys[idx].Users[userId] = userData
	keysMutex.Unlock()
	go saveKeys()
	return true
}

func extractAdminToken(authHeader string) string {
	if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		return authHeader[len("bearer "):]
	}
	if authHeader != "" {
		return authHeader
	}
	return ""
}

func isAdmin(c *gin.Context) bool {
	envOnce.Do(loadEnvFile)
	ADMIN_TOKEN := os.Getenv("ADMIN_TOKEN")
	if ADMIN_TOKEN == "" {
		return false
	}
	return extractAdminToken(c.GetHeader("Authorization")) == ADMIN_TOKEN
}

func authenticateAdmin(c *gin.Context) bool {
	envOnce.Do(loadEnvFile)
	ADMIN_TOKEN := os.Getenv("ADMIN_TOKEN")
	if ADMIN_TOKEN == "" {
		c.JSON(500, gin.H{"error": "ADMIN_TOKEN environment variable not set"})
		return false
	}
	if extractAdminToken(c.GetHeader("Authorization")) != ADMIN_TOKEN {
		c.JSON(403, gin.H{"error": "Invalid admin authentication"})
		return false
	}
	return true
}
