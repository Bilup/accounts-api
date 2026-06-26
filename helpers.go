package main

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

func getStatus(c *gin.Context) {
	// Calculate uptime
	uptime := time.Since(startTime).Seconds()

	// Simple load average simulation (since we removed the dependency)
	loadAverage := []float64{0.0, 0.0, 0.0}

	// Count current data
	usersMutex.RLock()
	usersCount := len(users)
	usersMutex.RUnlock()

	postsMutex.RLock()
	postsCount := len(posts)
	postsMutex.RUnlock()

	itemsMutex.RLock()
	itemsCount := len(items)
	itemsMutex.RUnlock()

	keysMutex.RLock()
	keysCount := len(keys)
	keysMutex.RUnlock()

	statusData := gin.H{
		"items":        itemsCount,
		"keys":         keysCount,
		"load_average": loadAverage,
		"posts":        postsCount,
		"status":       "ok",
		"uptime":       uptime,
		"users":        usersCount,
		"version":      "1.0.0",
	}

	c.JSON(200, statusData)
}

func bindJSON(c *gin.Context, dst any) bool {
	if err := c.ShouldBindJSON(dst); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request body"})
		return false
	}
	return true
}

func currentUser(c *gin.Context) *User {
	return c.MustGet("user").(*User)
}

func serverError(c *gin.Context, err error) {
	c.JSON(500, gin.H{"error": err.Error()})
}

func toGenericJSON(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out any
	json.Unmarshal(b, &out)
	return out
}

func reJSON[T any](raw any, empty T) T {
	if raw == nil {
		return empty
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return empty
	}
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		return empty
	}
	return out
}

func requireField[T ~string](c *gin.Context, value T, errMsg string) bool {
	if value == "" {
		c.JSON(400, gin.H{"error": errMsg})
		return false
	}
	return true
}

func requireMaxLen[T ~string](c *gin.Context, value T, max int, errMsg string) bool {
	if len(value) > max {
		c.JSON(400, gin.H{"error": errMsg})
		return false
	}
	return true
}

func requireFields[T ~string](c *gin.Context, errMsg string, values ...T) bool {
	for _, v := range values {
		if v == "" {
			c.JSON(400, gin.H{"error": errMsg})
			return false
		}
	}
	return true
}

func queryLimit(c *gin.Context, def, max int) int {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", strconv.Itoa(def)))
	if err != nil || limit <= 0 {
		limit = def
	}
	return clamp(limit, 1, max)
}

func getStringSlice(u User, key string) []string {
	mu := getMutexForUser(u)
	mu.Lock()
	defer mu.Unlock()
	if v, ok := u[key]; ok {
		switch s := v.(type) {
		case []string:
			return s
		case []any:
			out := make([]string, 0, len(s))
			for _, val := range s {
				if str, ok := val.(string); ok {
					out = append(out, str)
				}
			}
			return out
		}
	}
	return []string{}
}
