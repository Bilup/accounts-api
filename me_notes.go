package main

import "github.com/gin-gonic/gin"

func noteUser(c *gin.Context) {
	username := Username(c.Param("username"))
	if username == "" {
		c.JSON(400, gin.H{"error": "Username is required"})
		return
	}

	user := c.MustGet("user").(*User)

	var req struct {
		Note string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		req.Note = c.Query("note")
	}
	if req.Note == "" {
		c.JSON(400, gin.H{"error": "Note content is required"})
		return
	}

	err := user.SetNote(username, req.Note)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	go saveUsers()
	c.JSON(200, gin.H{"success": true})
}

func deleteNote(c *gin.Context) {
	username := Username(c.Param("username"))
	if username == "" {
		c.JSON(400, gin.H{"error": "Username is required"})
		return
	}

	user := c.MustGet("user").(*User)
	user.RemoveNote(username)
	go saveUsers()
	c.JSON(200, gin.H{"success": true})
}
