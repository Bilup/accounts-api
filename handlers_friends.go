package main

import (
	"github.com/gin-gonic/gin"
)

func resolveFriendTarget(c *gin.Context, username Username) (User, bool) {
	target, err := getAccountByUsername(username)
	if err != nil {
		c.JSON(404, gin.H{"error": "Account Does Not Exist"})
		return nil, false
	}
	return target, true
}

func sendFriendRequest(c *gin.Context) {
	sender := currentUser(c)
	targetUsername := Username(c.Param("username"))
	if !requireField(c, targetUsername, "Username cannot be empty") {
		return
	}
	senderName := sender.GetUsername().ToLower()
	senderId := sender.GetId()
	targetLower := targetUsername.ToLower()
	if senderName == targetLower {
		c.JSON(400, gin.H{"error": "You need other friends"})
		return
	}
	target, ok := resolveFriendTarget(c, targetLower)
	if !ok {
		return
	}
	if isUserBlockedBy(target, senderId) {
		c.JSON(400, gin.H{"error": "You cant send friend requests to this user"})
		return
	}
	if sender.IsFriend(targetLower) {
		c.JSON(400, gin.H{"error": "Already Friends"})
		return
	}
	if sender.HasRequest(targetLower) {
		sender.AddFriend(targetLower)
		target.AddFriend(senderName)
		sender.RemoveRequest(targetLower)
		target.RemoveRequest(senderName)
		go saveUsers()
		c.JSON(200, gin.H{"message": "Friend request accepted automatically"})
		return
	}
	if target.HasRequest(senderName) {
		c.JSON(400, gin.H{"error": "Already Requested"})
		return
	}
	target.AddRequest(senderName)
	go saveUsers()
	c.JSON(200, gin.H{"message": "Friend request sent successfully"})
}

func acceptFriendRequest(c *gin.Context) {
	current := currentUser(c)
	requesterName := Username(c.Param("username")).ToLower()
	if !requireField(c, requesterName, "Username cannot be empty") {
		return
	}
	currentName := current.GetUsername().ToLower()
	if currentName == requesterName {
		c.JSON(400, gin.H{"error": "Invalid Operation"})
		return
	}
	requester, ok := resolveFriendTarget(c, requesterName)
	if !ok {
		return
	}
	found := current.RemoveRequest(requesterName)
	if !found {
		c.JSON(400, gin.H{"error": "No Pending Request"})
		return
	}
	if current.IsFriend(requesterName) {
		go saveUsers()
		c.JSON(200, gin.H{"message": "Friend request accepted"})
		return
	}
	current.AddFriend(requesterName)
	requester.AddFriend(currentName)
	go saveUsers()
	c.JSON(200, gin.H{"message": "Friend request accepted"})
}

func rejectFriendRequest(c *gin.Context) {
	current := currentUser(c)
	requesterName := Username(c.Param("username")).ToLower()
	if !requireField(c, requesterName, "Username cannot be empty") {
		return
	}
	found := current.RemoveRequest(requesterName)
	if !found {
		c.JSON(400, gin.H{"error": "No Pending Request"})
		return
	}
	go saveUsers()
	c.JSON(200, gin.H{"message": "Friend request rejected"})
}

func cancelFriendRequest(c *gin.Context) {
	sender := currentUser(c)
	targetName := Username(c.Param("username")).ToLower()
	if !requireField(c, targetName, "Username cannot be empty") {
		return
	}
	senderName := sender.GetUsername().ToLower()
	if senderName == targetName {
		c.JSON(400, gin.H{"error": "Invalid Operation"})
		return
	}
	target, ok := resolveFriendTarget(c, targetName)
	if !ok {
		return
	}
	found := target.RemoveRequest(senderName)
	if !found {
		c.JSON(400, gin.H{"error": "No Pending Request"})
		return
	}
	go saveUsers()
	c.JSON(200, gin.H{"message": "Friend request cancelled"})
}

func removeFriend(c *gin.Context) {
	current := currentUser(c)
	otherName := Username(c.Param("username")).ToLower()
	if !requireField(c, otherName, "Username cannot be empty") {
		return
	}
	currentName := current.GetUsername().ToLower()
	if currentName == otherName {
		c.JSON(400, gin.H{"error": "Cannot Remove Yourself"})
		return
	}
	other, ok := resolveFriendTarget(c, otherName)
	if !ok {
		return
	}
	removedCurrent := current.RemoveFriend(otherName)
	current.RemoveRequest(otherName)
	other.RemoveFriend(currentName)
	other.RemoveRequest(currentName)
	if !removedCurrent {
		c.JSON(400, gin.H{"error": "Not Friends"})
		return
	}
	go saveUsers()
	c.JSON(200, gin.H{"message": "Friend removed"})
}

func getMeFriends(c *gin.Context) {
	user := currentUser(c)
	c.JSON(200, gin.H{"friends": user.GetFriendUsers()})
}

func getMeRequests(c *gin.Context) {
	user := currentUser(c)
	c.JSON(200, gin.H{"requests": user.GetRequestedUsers()})
}

func getMeRequestsOut(c *gin.Context) {
	user := currentUser(c)
	c.JSON(200, gin.H{"requests_out": user.GetOutgoingRequests()})
}
