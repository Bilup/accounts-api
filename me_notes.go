package main

import "github.com/gin-gonic/gin"

func getNotes(c *gin.Context) {
	user := currentUser(c)

	notes := user.GetNotesNet()

	c.JSON(200, gin.H{"notes": notes})
}

func noteUser(c *gin.Context) {
	username := Username(c.Param("username")).ToLower()
	if !requireField(c, username, "Username is required") {
		return
	}

	user := currentUser(c)

	var req struct {
		Note string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		req.Note = c.Query("note")
	}
	if !requireField(c, req.Note, "Note content is required") {
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
	username := Username(c.Param("username")).ToLower()
	if !requireField(c, username, "Username is required") {
		return
	}

	user := currentUser(c)
	user.RemoveNote(username)
	go saveUsers()
	c.JSON(200, gin.H{"success": true})
}
