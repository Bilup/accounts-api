package main

import (
	"claw/internal/config"
	"log"
	"sync"

	"github.com/gin-gonic/gin"
)

var (
	deletedAccounts      = map[UserId]int64{}
	deletedAccountsMutex sync.RWMutex
)

func loadDeletedAccounts() {
	deletedAccountsMutex.Lock()
	deletedAccounts = loadJSONOrDefault(config.DELETED_ACCOUNTS_PATH, map[UserId]int64{})
	count := len(deletedAccounts)
	deletedAccountsMutex.Unlock()
	log.Printf("Loaded %d deleted account records", count)
}

func saveDeletedAccounts() {
	deletedAccountsMutex.RLock()
	snapshot := make(map[UserId]int64, len(deletedAccounts))
	for k, v := range deletedAccounts {
		snapshot[k] = v
	}
	deletedAccountsMutex.RUnlock()
	saveJsonFile(config.DELETED_ACCOUNTS_PATH, snapshot)
}

func recordDeletedAccount(id UserId, ts int64) {
	if id == "" {
		return
	}
	deletedAccountsMutex.Lock()
	deletedAccounts[id] = ts
	deletedAccountsMutex.Unlock()
	go saveDeletedAccounts()
}

func isAccountDeleted(id UserId) bool {
	deletedAccountsMutex.RLock()
	defer deletedAccountsMutex.RUnlock()
	_, ok := deletedAccounts[id]
	return ok
}

func checkDeletedAccounts(c *gin.Context) {
	var req struct {
		Ids []UserId `json:"ids"`
	}
	if !bindJSON(c, &req) {
		return
	}
	if len(req.Ids) > 10000 {
		c.JSON(400, gin.H{"error": "too many ids"})
		return
	}

	deletedAccountsMutex.RLock()
	out := make([]UserId, 0)
	seen := make(map[UserId]struct{}, len(req.Ids))
	for _, id := range req.Ids {
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if _, ok := deletedAccounts[id]; ok {
			out = append(out, id)
		}
	}
	deletedAccountsMutex.RUnlock()

	c.JSON(200, gin.H{"deleted": out})
}
