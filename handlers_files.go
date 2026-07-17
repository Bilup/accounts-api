package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"claw/internal/ofsf"

	"github.com/gin-gonic/gin"
)

type UpdateFileRequest struct {
	Updates []ofsf.UpdateChange `json:"updates" binding:"required"`
}

type GetFilesRequest struct {
	Username string   `json:"username" binding:"required"`
	UUIDs    []string `json:"uuids" binding:"required"`
}

type GetFileSizesRequest struct {
	UUIDs []string `json:"uuids" binding:"required"`
}

var fs = ofsf.NewFileSystem()

func updateFiles(c *gin.Context) {
	user := currentUser(c)

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read request body"})
		return
	}

	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	var req UpdateFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fmt.Println("Raw body:", string(bodyBytes))

		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	maxSize := user.GetSubscriptionBenefits().FileSystem_Size

	result := fs.HandleOFSFUpdate(string(user.GetUsername()), req.Updates, maxSize)

	statusCode := http.StatusOK
	if result.Payload == "Max Upload Size Exceeded" {
		statusCode = http.StatusRequestEntityTooLarge
	} else if result.Payload != "Successfully Updated Origin Files" {
		statusCode = http.StatusBadRequest
	}

	c.JSON(statusCode, result)
}

func sendOctetStream(c *gin.Context, data []byte) {
	c.Header("Content-Length", fmt.Sprintf("%d", len(data)))
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Cache-Control", "no-cache")
	c.Data(http.StatusOK, "application/octet-stream", data)
}

func getFilesByUUIDs(c *gin.Context) {
	user := currentUser(c)

	var req GetFilesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	files, err := fs.GetFilesByUUIDs(string(user.GetUsername()), req.UUIDs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"files": files})
}

func getUserFileSize(c *gin.Context) {
	user := currentUser(c)

	username := string(user.GetUsername())

	size, err := fs.GetUserFileSize(username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"username": username, "size": size})
}

func deleteAllUserFiles(c *gin.Context) {
	user := currentUser(c)

	username := string(user.GetUsername())

	if err := fs.DeleteUserFileSystem(username); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "deleted", "username": username})
}

func getFilesIndex(c *gin.Context) {
	user := currentUser(c)

	username := string(user.GetUsername())
	fs.MigrateOrLog(username)
	index, err := fs.GetFilesIndexWithThreshold(username, 50*1024)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	jsonData, err := json.Marshal(index)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to serialize OFSF data"})
		return
	}

	sendOctetStream(c, jsonData)
}

func getFilesAll(c *gin.Context) {
	user := currentUser(c)

	username := string(user.GetUsername())
	fs.MigrateOrLog(username)
	index, err := fs.GetFilesIndexWithThreshold(username, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	jsonData, err := json.Marshal(index)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to serialize OFSF data"})
		return
	}

	sendOctetStream(c, jsonData)
}

func getFileSizes(c *gin.Context) {
	user := currentUser(c)
	username := string(user.GetUsername())

	var req GetFileSizesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	stats, err := fs.GetFileStats(username, req.UUIDs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"stats": stats})
}

func getFileByUUID(c *gin.Context) {
	user := currentUser(c)

	uuid := c.Query("uuid")
	if uuid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "UUID is required"})
		return
	}

	username := string(user.GetUsername())
	fs.MigrateOrLog(username)

	file, err := fs.GetFileByUUID(username, uuid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	jsonData, err := json.Marshal(file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to serialize OFSF data"})
		return
	}

	sendOctetStream(c, jsonData)
}

func getFileByPath(c *gin.Context) {
	user := currentUser(c)
	username := string(user.GetUsername())

	path := c.Param("path")
	if path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Path is required"})
		return
	}

	path = strings.ToLower(strings.TrimPrefix(path, "/"))

	index, err := fs.LoadPathIndex(username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load path index"})
		return
	}

	uuid, ok := index[path]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "File not found"})
		return
	}

	entry, err := fs.GetFileByUUID(username, uuid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	data, err := json.Marshal(entry)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to serialize file"})
		return
	}

	sendOctetStream(c, data)
}

func getPathIndex(c *gin.Context) {
	user := currentUser(c)
	username := string(user.GetUsername())

	index, err := fs.LoadPathIndex(username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load path index"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"index": index, "username": username})
}
