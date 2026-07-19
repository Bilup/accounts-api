package main

import (
	"crypto/subtle"
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
		var user User
		if idx >= 0 && idx < len(users) {
			user = users[idx]
		}
		usersMutex.RUnlock()
		if user != nil && user.GetKey() == key {
			return &user
		}
	}

	usersMutex.RLock()
	defer usersMutex.RUnlock()
	for i := range users {
		if users[i].GetKey() == key {
			// Copy the map header out of the slice: slice slots can be
			// swapped by concurrent deletes after the lock is released.
			user := users[i]
			return &user
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
	return getInt64OrDefault(userData.NextBilling, 0)
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
	return stripBearer(authHeader)
}

func isAdmin(c *gin.Context) bool {
	envOnce.Do(loadEnvFile)
	ADMIN_TOKEN := os.Getenv("ADMIN_TOKEN")
	if ADMIN_TOKEN == "" {
		return false
	}
	provided := extractAdminToken(c.GetHeader("Authorization"))
	return subtle.ConstantTimeCompare([]byte(provided), []byte(ADMIN_TOKEN)) == 1
}

func authenticateAdmin(c *gin.Context) bool {
	envOnce.Do(loadEnvFile)
	ADMIN_TOKEN := os.Getenv("ADMIN_TOKEN")
	if ADMIN_TOKEN == "" {
		c.JSON(500, gin.H{"error": "ADMIN_TOKEN environment variable not set"})
		return false
	}
	provided := extractAdminToken(c.GetHeader("Authorization"))
	if subtle.ConstantTimeCompare([]byte(provided), []byte(ADMIN_TOKEN)) != 1 {
		c.JSON(403, gin.H{"error": "Invalid admin authentication"})
		return false
	}
	return true
}
