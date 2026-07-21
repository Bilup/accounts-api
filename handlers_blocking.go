package main

import (
	"slices"

	"github.com/gin-gonic/gin"
)

func getBlocking(c *gin.Context) {
	user := currentUser(c)

	c.JSON(200, user.GetBlockedUsers())
}

func blockUser(c *gin.Context) {
	user := currentUser(c)
	userId := Username(c.Param("username")).Id()

	if !requireField(c, userId, "Username is required") {
		return
	}

	if !accountExists(userId) {
		c.JSON(404, gin.H{"error": "User not found"})
		return
	}

	if user.GetId() == userId {
		c.JSON(400, gin.H{"error": "Cannot block yourself"})
		return
	}

	blocked := user.GetBlocked()
	if slices.Contains(blocked, userId) {
		c.JSON(400, gin.H{"error": "User already blocked"})
		return
	}

	user.AddBlocked(userId)

	blockerId := user.GetId()
	blockerFollowers, blockedFollowers, followsChanged := severFollowsForBlock(blockerId, userId)

	blockedUser := userId.User()
	blockerName := user.GetUsername().ToLower()
	blockedName := blockedUser.GetUsername().ToLower()
	user.RemoveFriend(blockedName)
	user.RemoveRequest(blockedName)
	blockedUser.RemoveFriend(blockerName)
	blockedUser.RemoveRequest(blockerName)

	go saveUsers()
	if followsChanged {
		go saveFollowers()
		go broadcastClawEvent("followers", map[string]any{"username": blockerName, "followers": blockerFollowers})
		go broadcastClawEvent("followers", map[string]any{"username": blockedName, "followers": blockedFollowers})
	}

	c.JSON(200, gin.H{"message": "User blocked"})
}

func severFollowsForBlock(blockerId, blockedId UserId) (blockerFollowers, blockedFollowers int, changed bool) {
	followersMutex.Lock()
	defer followersMutex.Unlock()

	remove := func(target, follower UserId) bool {
		data, ok := followersData[target]
		if !ok {
			return false
		}
		out := make([]UserId, 0, len(data.Followers))
		removed := false
		for _, f := range data.Followers {
			if f == follower {
				removed = true
				continue
			}
			out = append(out, f)
		}
		if removed {
			data.Followers = out
			followersData[target] = data
			if followingCountMap[follower] > 0 {
				followingCountMap[follower]--
			}
		}
		return removed
	}

	changed = remove(blockerId, blockedId)
	changed = remove(blockedId, blockerId) || changed
	blockerFollowers = len(followersData[blockerId].Followers)
	blockedFollowers = len(followersData[blockedId].Followers)
	return blockerFollowers, blockedFollowers, changed
}

func unblockUser(c *gin.Context) {
	user := currentUser(c)
	userId := Username(c.Param("username")).Id()

	if !requireField(c, userId, "Username is required") {
		return
	}

	blocked := user.GetBlocked()
	index := -1
	for i, b := range blocked {
		if b == userId {
			index = i
			break
		}
	}

	if index == -1 {
		c.JSON(404, gin.H{"error": "User not blocked"})
		return
	}

	user.RemoveBlocked(userId)

	go saveUsers()

	c.JSON(200, gin.H{"message": "User unblocked"})
}
