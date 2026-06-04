package main

import (
	"fmt"
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)


func sendGroupInvite(c *gin.Context) {
	user := c.MustGet("user").(*User)
	groupTag := c.Param("grouptag")

	if groupTag == "" {
		c.JSON(400, gin.H{"error": "Group tag is required"})
		return
	}

	group, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if !hasPermission(user.GetId(), groupTag, "groups.members.invite") && group.OwnerUserId != user.GetId() {
		c.JSON(403, gin.H{"error": "You don't have permission to invite members"})
		return
	}

	targetUsername := c.Query("username")
	if targetUsername == "" {
		c.JSON(400, gin.H{"error": "Username is required"})
		return
	}

	targetUser, err := getAccountByUsername(targetUsername)
	if err != nil {
		c.JSON(404, gin.H{"error": "User not found"})
		return
	}

	targetUserId := targetUser.GetId()

	if targetUserId == user.GetId() {
		c.JSON(400, gin.H{"error": "You cannot invite yourself"})
		return
	}

	members := getGroupMembers(groupTag)
	for _, member := range members {
		if member.UserId == targetUserId {
			c.JSON(400, gin.H{"error": "User is already a member of this group"})
			return
		}
	}

	groupsDataMutex.RLock()
	data, ok := groupsData[groupTag]
	groupsDataMutex.RUnlock()
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	for _, inv := range data.Invites {
		if inv.ToUserId == targetUserId && inv.GroupTag == groupTag && inv.Status == InvitePending {
			c.JSON(400, gin.H{"error": "User already has a pending invite"})
			return
		}
	}

	for _, ban := range data.Bans {
		if ban.UserId == targetUserId {
			c.JSON(400, gin.H{"error": "User is banned from this group"})
			return
		}
	}

	invite := GroupInvite{
		Id:        uuid.New().String(),
		GroupTag:  groupTag,
		FromUserId: user.GetId(),
		ToUserId:  targetUserId,
		Status:    InvitePending,
		CreatedAt: time.Now().Unix(),
	}

	groupsDataMutex.Lock()
	data = groupsData[groupTag]
	data.Invites = append(data.Invites, invite)
	groupsData[groupTag] = data
	groupsDataMutex.Unlock()
	go saveGroupData(groupTag)

	go func() {
		if isNotifyAllowed(targetUser, "group_invite", user.GetId()) {
			sendPushNotificationToUser(targetUser, *user, NotificationRequest{
				Source: "group_invite",
				Title:  "Group Invite",
				Body:   fmt.Sprintf("%s invited you to join %s", user.GetUsername(), group.Name),
			})
		}
	}()

	addUserEvent(targetUserId, "group_invite", map[string]any{
		"group_tag":   groupTag,
		"group_name":  group.Name,
		"from":        string(user.GetUsername()),
		"invite_id":   invite.Id,
	})

	c.JSON(201, invite.ToNet())
}

func acceptGroupInvite(c *gin.Context) {
	user := c.MustGet("user").(*User)
	groupTag := c.Param("grouptag")
	inviteId := c.Param("inviteid")

	if groupTag == "" || inviteId == "" {
		c.JSON(400, gin.H{"error": "Group tag and invite ID are required"})
		return
	}

	group, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	groupsDataMutex.Lock()
	defer groupsDataMutex.Unlock()

	data, ok := groupsData[groupTag]
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	inviteIdx := -1
	for i, inv := range data.Invites {
		if inv.Id == inviteId && inv.GroupTag == groupTag && inv.ToUserId == user.GetId() && inv.Status == InvitePending {
			inviteIdx = i
			break
		}
	}
	if inviteIdx == -1 {
		c.JSON(404, gin.H{"error": "Invite not found or already handled"})
		return
	}

	for _, ban := range data.Bans {
		if ban.UserId == user.GetId() {
			c.JSON(403, gin.H{"error": "You are banned from this group"})
			return
		}
	}

	for _, member := range data.Members {
		if member.UserId == user.GetId() {
			data.Invites[inviteIdx].Status = InviteAccepted
			groupsData[groupTag] = data
			go saveGroupData(groupTag)
			c.JSON(400, gin.H{"error": "You are already a member of this group"})
			return
		}
	}

	if group.EntryFee > 0 {
		userCredits := user.GetCredits()
		if userCredits < group.EntryFee {
			c.JSON(400, gin.H{"error": "Insufficient funds to join this group", "required": group.EntryFee, "available": userCredits})
			return
		}
		user.SetBalance(roundVal(userCredits - group.EntryFee))
		user.addTransaction(Transaction{
			Note:      "Entry fee for group " + groupTag,
			User:      UserId(""),
			Amount:    group.EntryFee,
			Type:      "group_entry_fee",
			Timestamp: time.Now().UnixMilli(),
			NewTotal:  roundVal(userCredits - group.EntryFee),
		})
		data.Group.CreditsBalance = roundVal(data.Group.CreditsBalance + group.EntryFee)
		go saveUsers()
	}

	memberRoleId := ""
	for _, role := range data.Roles {
		if role.AssignOnJoin {
			memberRoleId = role.Id
			break
		}
	}
	if memberRoleId == "" {
		for _, role := range data.Roles {
			if role.Name == "Member" {
				memberRoleId = role.Id
				break
			}
		}
	}
	if memberRoleId == "" {
		c.JSON(500, gin.H{"error": "Default member role not found"})
		return
	}

	member := GroupMember{
		Id:        uuid.New().String(),
		GroupTag:  groupTag,
		UserId:    user.GetId(),
		RoleIds:   []string{memberRoleId},
		JoinedAt:  time.Now().Unix(),
		MutedAnnouncements: false,
	}
	data.Members = append(data.Members, member)

	data.Invites[inviteIdx].Status = InviteAccepted
	groupsData[groupTag] = data
	go saveGroupData(groupTag)

	netGroup := data.Group.ToNet()
	netGroup.MemberCount = len(data.Members)
	c.JSON(200, gin.H{
		"message": "Invite accepted",
		"group":   netGroup,
	})
}

func declineGroupInvite(c *gin.Context) {
	user := c.MustGet("user").(*User)
	groupTag := c.Param("grouptag")
	inviteId := c.Param("inviteid")

	if groupTag == "" || inviteId == "" {
		c.JSON(400, gin.H{"error": "Group tag and invite ID are required"})
		return
	}

	_, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	groupsDataMutex.Lock()
	defer groupsDataMutex.Unlock()

	data, ok := groupsData[groupTag]
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	found := false
	for i, inv := range data.Invites {
		if inv.Id == inviteId && inv.GroupTag == groupTag && inv.ToUserId == user.GetId() && inv.Status == InvitePending {
			data.Invites[i].Status = InviteDeclined
			found = true
			break
		}
	}
	if !found {
		c.JSON(404, gin.H{"error": "Invite not found or already handled"})
		return
	}

	groupsData[groupTag] = data
	go saveGroupData(groupTag)

	c.JSON(200, gin.H{"message": "Invite declined"})
}

func revokeGroupInvite(c *gin.Context) {
	user := c.MustGet("user").(*User)
	groupTag := c.Param("grouptag")
	inviteId := c.Param("inviteid")

	if groupTag == "" || inviteId == "" {
		c.JSON(400, gin.H{"error": "Group tag and invite ID are required"})
		return
	}

	group, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if !hasPermission(user.GetId(), groupTag, "groups.members.invite") && group.OwnerUserId != user.GetId() {
		c.JSON(403, gin.H{"error": "You don't have permission to revoke invites"})
		return
	}

	groupsDataMutex.Lock()
	defer groupsDataMutex.Unlock()

	data, ok := groupsData[groupTag]
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	found := false
	newInvites := make([]GroupInvite, 0, len(data.Invites))
	for _, inv := range data.Invites {
		if inv.Id == inviteId && inv.GroupTag == groupTag && inv.Status == InvitePending {
			found = true
			continue // remove the invite
		}
		newInvites = append(newInvites, inv)
	}
	if !found {
		c.JSON(404, gin.H{"error": "Invite not found or already handled"})
		return
	}

	data.Invites = newInvites
	groupsData[groupTag] = data
	go saveGroupData(groupTag)

	c.JSON(200, gin.H{"message": "Invite revoked"})
}

func getGroupInvites(c *gin.Context) {
	user := c.MustGet("user").(*User)
	groupTag := c.Param("grouptag")

	if groupTag == "" {
		c.JSON(400, gin.H{"error": "Group tag is required"})
		return
	}

	group, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if !hasPermission(user.GetId(), groupTag, "groups.members.invite") && group.OwnerUserId != user.GetId() {
		c.JSON(403, gin.H{"error": "You don't have permission to view invites"})
		return
	}

	groupsDataMutex.RLock()
	data, ok := groupsData[groupTag]
	groupsDataMutex.RUnlock()
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	results := make([]GroupInviteNet, 0)
	for _, inv := range data.Invites {
		if inv.Status == InvitePending {
			results = append(results, inv.ToNet())
		}
	}

	c.JSON(200, results)
}

func getMyGroupInvites(c *gin.Context) {
	user := c.MustGet("user").(*User)
	userId := user.GetId()

	groupsDataMutex.RLock()
	defer groupsDataMutex.RUnlock()

	results := make([]GroupInviteNet, 0)
	for _, data := range groupsData {
		for _, inv := range data.Invites {
			if inv.ToUserId == userId && inv.Status == InvitePending {
				results = append(results, inv.ToNet())
			}
		}
	}

	c.JSON(200, results)
}


func kickGroupMember(c *gin.Context) {
	user := c.MustGet("user").(*User)
	groupTag := c.Param("grouptag")
	targetUserId := c.Param("userid")

	if groupTag == "" || targetUserId == "" {
		c.JSON(400, gin.H{"error": "Group tag and user ID are required"})
		return
	}

	group, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if !hasPermission(user.GetId(), groupTag, "groups.members.remove") && group.OwnerUserId != user.GetId() {
		c.JSON(403, gin.H{"error": "You don't have permission to kick members"})
		return
	}

	groupsDataMutex.Lock()
	defer groupsDataMutex.Unlock()

	data, ok := groupsData[groupTag]
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if UserId(targetUserId) == data.Group.OwnerUserId {
		c.JSON(400, gin.H{"error": "Cannot kick the group owner"})
		return
	}

	if user.GetId() != data.Group.OwnerUserId {
		for _, member := range data.Members {
			if member.UserId == UserId(targetUserId) {
				if isOwnerRole(groupTag, member.RoleIds) {
					c.JSON(403, gin.H{"error": "Cannot kick a member with the Owner role"})
					return
				}
				break
			}
		}
	}

	found := false
	newMembers := make([]GroupMember, 0, len(data.Members))
	for _, member := range data.Members {
		if member.UserId == UserId(targetUserId) {
			found = true
			continue
		}
		newMembers = append(newMembers, member)
	}
	if !found {
		c.JSON(404, gin.H{"error": "User is not a member of this group"})
		return
	}

	data.Members = newMembers
	groupsData[groupTag] = data
	go saveGroupData(groupTag)

	go func() {
		targetUser, err := getAccountByUserId(UserId(targetUserId))
		if err == nil {
			addUserEvent(UserId(targetUserId), "group_kicked", map[string]any{
				"group_tag":  groupTag,
				"group_name": group.Name,
				"kicked_by":  string(user.GetUsername()),
			})
			if isNotifyAllowed(targetUser, "group_"+groupTag, user.GetId()) {
				sendPushNotificationToUser(targetUser, *user, NotificationRequest{
					Source: "group_" + groupTag,
					Title:  "Removed from Group",
					Body:   fmt.Sprintf("You have been removed from %s", group.Name),
				})
			}
		}
	}()

	log.Printf("User %s kicked %s from group %s", user.GetUsername(), targetUserId, groupTag)

	netGroup := data.Group.ToNet()
	netGroup.MemberCount = len(data.Members)
	c.JSON(200, gin.H{
		"message": "Member kicked",
		"group":   netGroup,
	})
}


func banGroupMember(c *gin.Context) {
	user := c.MustGet("user").(*User)
	groupTag := c.Param("grouptag")
	targetUserId := c.Param("userid")

	if groupTag == "" || targetUserId == "" {
		c.JSON(400, gin.H{"error": "Group tag and user ID are required"})
		return
	}

	group, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if !hasPermission(user.GetId(), groupTag, "groups.members.ban") && group.OwnerUserId != user.GetId() {
		c.JSON(403, gin.H{"error": "You don't have permission to ban members"})
		return
	}

	reason := c.Query("reason")
	if len(reason) > 200 {
		c.JSON(400, gin.H{"error": "Reason length exceeded (max 200)"})
		return
	}

	groupsDataMutex.Lock()
	defer groupsDataMutex.Unlock()

	data, ok := groupsData[groupTag]
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if UserId(targetUserId) == data.Group.OwnerUserId {
		c.JSON(400, gin.H{"error": "Cannot ban the group owner"})
		return
	}

	for _, ban := range data.Bans {
		if ban.UserId == UserId(targetUserId) {
			c.JSON(400, gin.H{"error": "User is already banned from this group"})
			return
		}
	}

	ban := GroupBan{
		Id:        uuid.New().String(),
		GroupTag:  groupTag,
		UserId:    UserId(targetUserId),
		BannedBy:  user.GetId(),
		Reason:    reason,
		CreatedAt: time.Now().Unix(),
	}

	data.Bans = append(data.Bans, ban)

	newMembers := make([]GroupMember, 0, len(data.Members))
	wasMember := false
	for _, member := range data.Members {
		if member.UserId == UserId(targetUserId) {
			wasMember = true
			continue
		}
		newMembers = append(newMembers, member)
	}
	data.Members = newMembers

	newInvites := make([]GroupInvite, 0, len(data.Invites))
	for _, inv := range data.Invites {
		if inv.ToUserId == UserId(targetUserId) && inv.Status == InvitePending {
			continue
		}
		newInvites = append(newInvites, inv)
	}
	data.Invites = newInvites

	newRequests := make([]GroupJoinRequest, 0, len(data.JoinRequests))
	for _, req := range data.JoinRequests {
		if req.UserId == UserId(targetUserId) && req.Status == RequestPending {
			continue
		}
		newRequests = append(newRequests, req)
	}
	data.JoinRequests = newRequests

	groupsData[groupTag] = data
	go saveGroupData(groupTag)

	go func() {
		targetUser, err := getAccountByUserId(UserId(targetUserId))
		if err == nil {
			addUserEvent(UserId(targetUserId), "group_banned", map[string]any{
				"group_tag":  groupTag,
				"group_name": group.Name,
				"banned_by":  string(user.GetUsername()),
				"reason":     reason,
			})
			if isNotifyAllowed(targetUser, "group_"+groupTag, user.GetId()) {
				sendPushNotificationToUser(targetUser, *user, NotificationRequest{
					Source: "group_" + groupTag,
					Title:  "Banned from Group",
					Body:   fmt.Sprintf("You have been banned from %s", group.Name),
				})
			}
		}
	}()

	log.Printf("User %s banned %s from group %s", user.GetUsername(), targetUserId, groupTag)

	status := "banned"
	if wasMember {
		status = "banned and removed from group"
	}

	c.JSON(200, gin.H{
		"message": status,
		"ban":     ban.ToNet(),
	})
}

func unbanGroupMember(c *gin.Context) {
	user := c.MustGet("user").(*User)
	groupTag := c.Param("grouptag")
	targetUserId := c.Param("userid")

	if groupTag == "" || targetUserId == "" {
		c.JSON(400, gin.H{"error": "Group tag and user ID are required"})
		return
	}

	group, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if !hasPermission(user.GetId(), groupTag, "groups.members.ban") && group.OwnerUserId != user.GetId() {
		c.JSON(403, gin.H{"error": "You don't have permission to unban members"})
		return
	}

	groupsDataMutex.Lock()
	defer groupsDataMutex.Unlock()

	data, ok := groupsData[groupTag]
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	found := false
	newBans := make([]GroupBan, 0, len(data.Bans))
	for _, ban := range data.Bans {
		if ban.UserId == UserId(targetUserId) && ban.GroupTag == groupTag {
			found = true
			continue
		}
		newBans = append(newBans, ban)
	}
	if !found {
		c.JSON(404, gin.H{"error": "User is not banned from this group"})
		return
	}

	data.Bans = newBans
	groupsData[groupTag] = data
	go saveGroupData(groupTag)

	c.JSON(200, gin.H{"message": "User unbanned"})
}

func getGroupBans(c *gin.Context) {
	user := c.MustGet("user").(*User)
	groupTag := c.Param("grouptag")

	if groupTag == "" {
		c.JSON(400, gin.H{"error": "Group tag is required"})
		return
	}

	group, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if !hasPermission(user.GetId(), groupTag, "groups.members.ban") && group.OwnerUserId != user.GetId() {
		c.JSON(403, gin.H{"error": "You don't have permission to view bans"})
		return
	}

	groupsDataMutex.RLock()
	data, ok := groupsData[groupTag]
	groupsDataMutex.RUnlock()
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	results := make([]GroupBanNet, 0, len(data.Bans))
	for _, ban := range data.Bans {
		results = append(results, ban.ToNet())
	}

	c.JSON(200, results)
}

func checkGroupBan(c *gin.Context) {
	groupTag := c.Param("grouptag")
	targetUserId := c.Param("userid")

	if groupTag == "" || targetUserId == "" {
		c.JSON(400, gin.H{"error": "Group tag and user ID are required"})
		return
	}

	_, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	groupsDataMutex.RLock()
	data, ok := groupsData[groupTag]
	groupsDataMutex.RUnlock()
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	for _, ban := range data.Bans {
		if ban.UserId == UserId(targetUserId) {
			c.JSON(200, gin.H{"banned": true, "ban": ban.ToNet()})
			return
		}
	}

	c.JSON(200, gin.H{"banned": false})
}


func requestToJoinGroup(c *gin.Context) {
	user := c.MustGet("user").(*User)
	groupTag := c.Param("grouptag")

	if groupTag == "" {
		c.JSON(400, gin.H{"error": "Group tag is required"})
		return
	}

	group, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if group.JoinPolicy != JoinPolicyRequest {
		c.JSON(400, gin.H{"error": "This group does not require join requests"})
		return
	}

	if !group.Public {
		c.JSON(403, gin.H{"error": "Group is private"})
		return
	}

	userId := user.GetId()

	members := getGroupMembers(groupTag)
	for _, member := range members {
		if member.UserId == userId {
			c.JSON(400, gin.H{"error": "You are already a member of this group"})
			return
		}
	}

	groupsDataMutex.Lock()
	defer groupsDataMutex.Unlock()

	data, ok := groupsData[groupTag]
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	for _, ban := range data.Bans {
		if ban.UserId == userId {
			c.JSON(403, gin.H{"error": "You are banned from this group"})
			return
		}
	}

	for _, req := range data.JoinRequests {
		if req.UserId == userId && req.Status == RequestPending {
			c.JSON(400, gin.H{"error": "You already have a pending join request"})
			return
		}
	}

	for _, inv := range data.Invites {
		if inv.ToUserId == userId && inv.Status == InvitePending {
			c.JSON(400, gin.H{"error": "You already have a pending invite to this group"})
			return
		}
	}

	message := c.Query("message")
	if len(message) > 200 {
		c.JSON(400, gin.H{"error": "Message length exceeded (max 200)"})
		return
	}

	joinRequest := GroupJoinRequest{
		Id:        uuid.New().String(),
		GroupTag:  groupTag,
		UserId:    userId,
		Message:   message,
		Status:    RequestPending,
		CreatedAt: time.Now().Unix(),
	}

	data.JoinRequests = append(data.JoinRequests, joinRequest)
	groupsData[groupTag] = data
	go saveGroupData(groupTag)

	go func() {
		members := getGroupMembers(groupTag)
		for _, member := range members {
			if member.UserId == userId {
				continue
			}
			if hasPermission(member.UserId, groupTag, "groups.members.invite") || member.UserId == group.OwnerUserId {
				targetUser := member.UserId.User()
				if isNotifyAllowed(targetUser, "group_"+groupTag, user.GetId()) {
					sendPushNotificationToUser(targetUser, *user, NotificationRequest{
						Source: "group_" + groupTag,
						Title:  "Join Request",
						Body:   fmt.Sprintf("%s requested to join %s", user.GetUsername(), group.Name),
					})
				}
			}
		}
	}()

	c.JSON(201, joinRequest.ToNet())
}

func getGroupJoinRequests(c *gin.Context) {
	user := c.MustGet("user").(*User)
	groupTag := c.Param("grouptag")

	if groupTag == "" {
		c.JSON(400, gin.H{"error": "Group tag is required"})
		return
	}

	group, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if !hasPermission(user.GetId(), groupTag, "groups.members.invite") && group.OwnerUserId != user.GetId() {
		c.JSON(403, gin.H{"error": "You don't have permission to view join requests"})
		return
	}

	groupsDataMutex.RLock()
	data, ok := groupsData[groupTag]
	groupsDataMutex.RUnlock()
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	results := make([]GroupJoinRequestNet, 0)
	for _, req := range data.JoinRequests {
		if req.Status == RequestPending {
			results = append(results, req.ToNet())
		}
	}

	c.JSON(200, results)
}

func acceptGroupJoinRequest(c *gin.Context) {
	user := c.MustGet("user").(*User)
	groupTag := c.Param("grouptag")
	requestId := c.Param("requestid")

	if groupTag == "" || requestId == "" {
		c.JSON(400, gin.H{"error": "Group tag and request ID are required"})
		return
	}

	group, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if !hasPermission(user.GetId(), groupTag, "groups.members.invite") && group.OwnerUserId != user.GetId() {
		c.JSON(403, gin.H{"error": "You don't have permission to accept join requests"})
		return
	}

	groupsDataMutex.Lock()
	defer groupsDataMutex.Unlock()

	data, ok := groupsData[groupTag]
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	reqIdx := -1
	for i, req := range data.JoinRequests {
		if req.Id == requestId && req.GroupTag == groupTag && req.Status == RequestPending {
			reqIdx = i
			break
		}
	}
	if reqIdx == -1 {
		c.JSON(404, gin.H{"error": "Join request not found or already handled"})
		return
	}

	targetUserId := data.JoinRequests[reqIdx].UserId

	for _, ban := range data.Bans {
		if ban.UserId == targetUserId {
			c.JSON(403, gin.H{"error": "User is banned from this group"})
			return
		}
	}

	for _, member := range data.Members {
		if member.UserId == targetUserId {
			data.JoinRequests[reqIdx].Status = RequestAccepted
			groupsData[groupTag] = data
			go saveGroupData(groupTag)
			c.JSON(400, gin.H{"error": "User is already a member of this group"})
			return
		}
	}

	if group.EntryFee > 0 {
		targetUser, err := getAccountByUserId(targetUserId)
		if err != nil {
			c.JSON(404, gin.H{"error": "Target user not found"})
			return
		}
		userCredits := targetUser.GetCredits()
		if userCredits < group.EntryFee {
			c.JSON(400, gin.H{"error": "User has insufficient funds to join this group", "required": group.EntryFee, "available": userCredits})
			return
		}
		targetUser.SetBalance(roundVal(userCredits - group.EntryFee))
		targetUser.addTransaction(Transaction{
			Note:      "Entry fee for group " + groupTag,
			User:      UserId(""),
			Amount:    group.EntryFee,
			Type:      "group_entry_fee",
			Timestamp: time.Now().UnixMilli(),
			NewTotal:  roundVal(userCredits - group.EntryFee),
		})
		data.Group.CreditsBalance = roundVal(data.Group.CreditsBalance + group.EntryFee)
		go saveUsers()
	}

	memberRoleId := ""
	for _, role := range data.Roles {
		if role.AssignOnJoin {
			memberRoleId = role.Id
			break
		}
	}
	if memberRoleId == "" {
		for _, role := range data.Roles {
			if role.Name == "Member" {
				memberRoleId = role.Id
				break
			}
		}
	}
	if memberRoleId == "" {
		c.JSON(500, gin.H{"error": "Default member role not found"})
		return
	}

	member := GroupMember{
		Id:        uuid.New().String(),
		GroupTag:  groupTag,
		UserId:    targetUserId,
		RoleIds:   []string{memberRoleId},
		JoinedAt:  time.Now().Unix(),
		MutedAnnouncements: false,
	}
	data.Members = append(data.Members, member)

	data.JoinRequests[reqIdx].Status = RequestAccepted
	groupsData[groupTag] = data
	go saveGroupData(groupTag)

	go func() {
		targetUser, err := getAccountByUserId(targetUserId)
		if err == nil {
			addUserEvent(targetUserId, "group_request_accepted", map[string]any{
				"group_tag":  groupTag,
				"group_name": group.Name,
			})
			if isNotifyAllowed(targetUser, "group_"+groupTag, user.GetId()) {
				sendPushNotificationToUser(targetUser, *user, NotificationRequest{
					Source: "group_" + groupTag,
					Title:  "Join Request Accepted",
					Body:   fmt.Sprintf("Your request to join %s has been accepted", group.Name),
				})
			}
		}
	}()

	netGroup := data.Group.ToNet()
	netGroup.MemberCount = len(data.Members)
	c.JSON(200, gin.H{
		"message": "Join request accepted",
		"group":   netGroup,
	})
}

func declineGroupJoinRequest(c *gin.Context) {
	user := c.MustGet("user").(*User)
	groupTag := c.Param("grouptag")
	requestId := c.Param("requestid")

	if groupTag == "" || requestId == "" {
		c.JSON(400, gin.H{"error": "Group tag and request ID are required"})
		return
	}

	group, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if !hasPermission(user.GetId(), groupTag, "groups.members.invite") && group.OwnerUserId != user.GetId() {
		c.JSON(403, gin.H{"error": "You don't have permission to decline join requests"})
		return
	}

	groupsDataMutex.Lock()
	defer groupsDataMutex.Unlock()

	data, ok := groupsData[groupTag]
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	found := false
	for i, req := range data.JoinRequests {
		if req.Id == requestId && req.GroupTag == groupTag && req.Status == RequestPending {
			data.JoinRequests[i].Status = RequestDeclined
			found = true
			break
		}
	}
	if !found {
		c.JSON(404, gin.H{"error": "Join request not found or already handled"})
		return
	}

	groupsData[groupTag] = data
	go saveGroupData(groupTag)

	go func() {
		targetUserId := data.JoinRequests[0].UserId // find the one we just declined
		for _, req := range data.JoinRequests {
			if req.Id == requestId {
				targetUserId = req.UserId
				break
			}
		}
		targetUser, err := getAccountByUserId(targetUserId)
		if err == nil {
			addUserEvent(targetUserId, "group_request_declined", map[string]any{
				"group_tag":  groupTag,
				"group_name": group.Name,
			})
			if isNotifyAllowed(targetUser, "group_"+groupTag, user.GetId()) {
				sendPushNotificationToUser(targetUser, *user, NotificationRequest{
					Source: "group_" + groupTag,
					Title:  "Join Request Declined",
					Body:   fmt.Sprintf("Your request to join %s has been declined", group.Name),
				})
			}
		}
	}()

	c.JSON(200, gin.H{"message": "Join request declined"})
}


func transferGroupOwnership(c *gin.Context) {
	user := c.MustGet("user").(*User)
	groupTag := c.Param("grouptag")
	targetUserId := c.Param("userid")

	if groupTag == "" || targetUserId == "" {
		c.JSON(400, gin.H{"error": "Group tag and user ID are required"})
		return
	}

	group, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if group.OwnerUserId != user.GetId() {
		c.JSON(403, gin.H{"error": "Only the group owner can transfer ownership"})
		return
	}

	if UserId(targetUserId) == user.GetId() {
		c.JSON(400, gin.H{"error": "You are already the owner"})
		return
	}

	groupsDataMutex.Lock()
	defer groupsDataMutex.Unlock()

	data, ok := groupsData[groupTag]
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	targetIdx := -1
	ownerIdx := -1
	ownerRoleId := ""
	memberRoleId := ""

	for _, role := range data.Roles {
		if role.Name == "Owner" {
			ownerRoleId = role.Id
		}
		if role.Name == "Member" && role.AssignOnJoin {
			memberRoleId = role.Id
		}
	}

	for i, member := range data.Members {
		if member.UserId == UserId(targetUserId) {
			targetIdx = i
		}
		if member.UserId == user.GetId() {
			ownerIdx = i
		}
	}

	if targetIdx == -1 {
		c.JSON(404, gin.H{"error": "Target user is not a member of this group"})
		return
	}

	if ownerRoleId != "" {
		data.Members[targetIdx].RoleIds = append(data.Members[targetIdx].RoleIds, ownerRoleId)
	}

	if ownerIdx != -1 && ownerRoleId != "" {
		newRoleIds := make([]string, 0)
		for _, rid := range data.Members[ownerIdx].RoleIds {
			if rid != ownerRoleId {
				newRoleIds = append(newRoleIds, rid)
			}
		}
		if len(newRoleIds) == 0 && memberRoleId != "" {
			newRoleIds = append(newRoleIds, memberRoleId)
		}
		data.Members[ownerIdx].RoleIds = newRoleIds
	}

	data.Group.OwnerUserId = UserId(targetUserId)
	groupsData[groupTag] = data
	go saveGroupData(groupTag)

	go func() {
		targetUser, err := getAccountByUserId(UserId(targetUserId))
		if err == nil {
			addUserEvent(UserId(targetUserId), "group_ownership_transferred", map[string]any{
				"group_tag":  groupTag,
				"group_name": group.Name,
				"from":       string(user.GetUsername()),
			})
			if isNotifyAllowed(targetUser, "group_"+groupTag, user.GetId()) {
				sendPushNotificationToUser(targetUser, *user, NotificationRequest{
					Source: "group_" + groupTag,
					Title:  "Group Ownership Transferred",
					Body:   fmt.Sprintf("You are now the owner of %s", group.Name),
				})
			}
		}
	}()

	log.Printf("Group %s ownership transferred from %s to %s", groupTag, user.GetUsername(), targetUserId)

	netGroup := data.Group.ToNet()
	netGroup.MemberCount = len(data.Members)
	c.JSON(200, gin.H{
		"message": "Ownership transferred",
		"group":   netGroup,
	})
}


func getGroupMemberInfo(c *gin.Context) {
	groupTag := c.Param("grouptag")
	targetUserId := c.Param("userid")

	if groupTag == "" || targetUserId == "" {
		c.JSON(400, gin.H{"error": "Group tag and user ID are required"})
		return
	}

	_, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	members := getGroupMembers(groupTag)
	for _, member := range members {
		if member.UserId == UserId(targetUserId) {
			rolesMap := getGroupRolesMap(groupTag)
			roles := make([]GroupRole, 0)
			for _, roleId := range member.RoleIds {
				if role, ok := rolesMap[roleId]; ok {
					if role.Name == "Owner" {
						ownerRole := role
						ownerRole.Permissions = allGroupPermissions()
						roles = append(roles, ownerRole)
					} else {
						roles = append(roles, role)
					}
				}
			}

			permissionsMap := make(map[string]bool)
			for _, roleId := range member.RoleIds {
				if role, ok := rolesMap[roleId]; ok {
					if role.Name == "Owner" {
						for _, perm := range allGroupPermissions() {
							permissionsMap[perm] = true
						}
					} else {
						for _, perm := range role.Permissions {
							permissionsMap[perm] = true
						}
					}
				}
			}
			permissions := make([]string, 0, len(permissionsMap))
			for perm := range permissionsMap {
				permissions = append(permissions, perm)
			}

			benefitsMap := make(map[string]bool)
			for _, roleId := range member.RoleIds {
				if role, ok := rolesMap[roleId]; ok {
					for _, benefit := range role.Benefits {
						benefitsMap[benefit] = true
					}
				}
			}
			benefits := make([]string, 0, len(benefitsMap))
			for benefit := range benefitsMap {
				benefits = append(benefits, benefit)
			}

			c.JSON(200, gin.H{
				"member":      member.ToNet(),
				"roles":       roles,
				"permissions": permissions,
				"benefits":    benefits,
			})
			return
		}
	}

	c.JSON(404, gin.H{"error": "User is not a member of this group"})
}
