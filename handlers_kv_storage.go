package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type kvStorageSetRequest struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

func storageMaxBytes(user *User) int64 {
	return int64(user.GetSubscriptionBenefits().FileSystem_Size)
}

func getStorageUsage(c *gin.Context) {
	user := currentUser(c)
	c.JSON(http.StatusOK, gin.H{
		"usage": kvStorageUsage(user.GetId()),
		"max":   storageMaxBytes(user),
	})
}

func listStorage(c *gin.Context) {
	user := currentUser(c)
	c.JSON(http.StatusOK, gin.H{
		"ids":   kvStorageList(user.GetId()),
		"usage": kvStorageUsage(user.GetId()),
		"max":   storageMaxBytes(user),
	})
}

func getStorage(c *gin.Context) {
	user := currentUser(c)
	id := c.Param("id")
	if !validStorageID(id) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid storage id"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "data": kvStorageGet(user.GetId(), id)})
}

func setStorage(c *gin.Context) {
	user := currentUser(c)
	id := c.Param("id")
	if !validStorageID(id) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid storage id"})
		return
	}

	var req kvStorageSetRequest
	if !bindJSON(c, &req) {
		return
	}
	if !requireField(c, req.Key, "storage key is required") {
		return
	}

	data, err := kvStorageSet(user.GetId(), id, req.Key, req.Value, storageMaxBytes(user))
	if err == errStorageQuota {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "storage quota exceeded"})
		return
	}
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "data": data})
}

func deleteStorage(c *gin.Context) {
	user := currentUser(c)
	id := c.Param("id")
	if !validStorageID(id) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid storage id"})
		return
	}

	if key := c.Query("key"); key != "" {
		data, err := kvStorageDelete(user.GetId(), id, key)
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": id, "data": data})
		return
	}

	cleared, err := kvStorageClear(user.GetId(), id)
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "cleared": cleared})
}

func clearStorage(c *gin.Context) {
	user := currentUser(c)
	cleared, err := kvStorageClear(user.GetId(), "")
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"cleared": cleared})
}
