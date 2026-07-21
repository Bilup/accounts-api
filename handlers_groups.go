package main

import (
	"bytes"
	"claw/internal/config"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"claw/internal/imageutil"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/nfnt/resize"
)

func requireGroupTag(c *gin.Context) (string, bool) {
	groupTag := c.Param("grouptag")
	if groupTag == "" {
		c.JSON(400, gin.H{"error": "Group tag is required"})
		return "", false
	}
	return groupTag, true
}

func getGroupOr404(c *gin.Context, groupTag string) (*Group, bool) {
	group, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return nil, false
	}
	return group, true
}

func requireGroupPermOrOwner(c *gin.Context, group *Group, userId UserId, groupTag, perm, errMsg string) bool {
	if group.OwnerUserId == userId || hasPermission(userId, groupTag, perm) {
		return true
	}
	c.JSON(403, gin.H{"error": errMsg})
	return false
}

func requireGroupEditAccess(c *gin.Context, group *Group, userId UserId, groupTag string) bool {
	if group.OwnerUserId == userId ||
		hasPermission(userId, groupTag, "groups.group.edit") ||
		hasPermission(userId, groupTag, "groups.manage") {
		return true
	}
	c.JSON(403, gin.H{"error": "You are not authorized to update this group"})
	return false
}

func notifyGroupEvent(targetUserId UserId, sender *User, groupTag, event string, data map[string]any, title, body string) {
	go func() {
		targetUser, err := getAccountByUserId(targetUserId)
		if err != nil {
			return
		}
		addUserEvent(targetUserId, event, data)
		notifyGroupMember(targetUser, sender, groupTag, title, body)
	}()
}

func notifyGroupMember(targetUser User, sender *User, groupTag, title, body string) {
	if isNotifyAllowed(targetUser, "group_"+groupTag, sender.GetId()) {
		sendPushNotificationToUser(targetUser, *sender, NotificationRequest{
			Source: "group_" + groupTag,
			Title:  title,
			Body:   body,
		})
	}
}

func findGroupMember(groupTag string, userId UserId) (GroupMember, bool) {
	for _, m := range getGroupMembers(groupTag) {
		if m.UserId == userId {
			return m, true
		}
	}
	return GroupMember{}, false
}

func isGroupMember(groupTag string, userId UserId) bool {
	_, ok := findGroupMember(groupTag, userId)
	return ok
}

func requireGroupPerm(c *gin.Context, userId UserId, groupTag, perm, errMsg string) bool {
	if hasPermission(userId, groupTag, perm) {
		return true
	}
	c.JSON(403, gin.H{"error": errMsg})
	return false
}

func requireGroupExists(c *gin.Context, groupTag string) bool {
	if _, ok := getGroupByTag(groupTag); !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return false
	}
	return true
}

func resolveGroup(c *gin.Context) (*Group, string, bool) {
	groupTag, ok := requireGroupTag(c)
	if !ok {
		return nil, "", false
	}
	group, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return nil, "", false
	}
	return group, groupTag, true
}

func getMyGroups(c *gin.Context) {
	user := currentUser(c)

	id := user.GetId()

	groupsDataMutex.RLock()
	defer groupsDataMutex.RUnlock()

	outGroups := make([]GroupPublic, 0)

	for _, data := range groupsData {
		for _, member := range data.Members {
			if member.UserId == id {
				publicGroup := data.Group.ToPublic()
				publicGroup.MemberCount = len(data.Members)
				outGroups = append(outGroups, publicGroup)
				break
			}
		}
	}

	c.JSON(200, outGroups)
}

func createGroup(c *gin.Context) {
	user := currentUser(c)

	tag := c.Query("tag")
	if !requireField(c, tag, "Tag is required") {
		return
	}
	if !requireMaxLen(c, tag, 10, "Tag must be 10 characters or less") {
		return
	}
	re := regexp.MustCompile(`^[a-zA-Z0-9]+$`)
	if !re.MatchString(tag) {
		c.JSON(400, gin.H{"error": "Tag must be alphanumeric only"})
		return
	}

	name := c.Query("name")
	if !requireField(c, name, "Name is required") {
		return
	}
	if !requireMaxLen(c, name, 50, "Name length exceeded") {
		return
	}
	description := c.Query("description")
	if !requireMaxLen(c, description, 500, "Description length exceeded") {
		return
	}

	readme := c.Query("readme")
	if !requireMaxLen(c, readme, 10000, "Readme length exceeded") {
		return
	}

	rules := c.Query("rules")
	if !requireMaxLen(c, rules, 5000, "Rules length exceeded") {
		return
	}

	entryFeeStr := c.DefaultQuery("entry_fee", "0")
	entryFee, err := strconv.ParseFloat(entryFeeStr, 64)
	if err != nil || entryFee < 0 || !isFiniteAmount(entryFee) {
		c.JSON(400, gin.H{"error": "Invalid entry fee"})
		return
	}

	public := c.DefaultQuery("public", "false") == "true"
	joinPolicyRaw := c.DefaultQuery("join_policy", "OPEN")
	joinPolicy := JoinPolicy(joinPolicyRaw)
	if joinPolicy != JoinPolicyOpen && joinPolicy != JoinPolicyRequest && joinPolicy != JoinPolicyInvite {
		c.JSON(400, gin.H{"error": "Invalid join policy"})
		return
	}

	ownerId := user.GetId()
	userCredits := user.GetCredits()
	if userCredits < 50 {
		c.JSON(400, gin.H{"error": "Insufficient funds to create a group (50 credits required)", "required": 50.0, "available": userCredits})
		return
	}

	groupId := GroupId(uuid.New().String())
	group := Group{
		Id:             groupId,
		Tag:            tag,
		Name:           name,
		Description:    description,
		Readme:         readme,
		Rules:          rules,
		IconUrl:        "",
		BannerUrl:      "",
		OwnerUserId:    user.GetId(),
		Public:         public,
		JoinPolicy:     joinPolicy,
		EntryFee:       entryFee,
		CreatedAt:      time.Now().Unix(),
		CreditsBalance: 0,
	}

	memberId := uuid.New().String()
	ownerRoleId := uuid.New().String()

	allPermissions := []string{
		"groups.manage",
		"groups.members.invite",
		"groups.members.remove",
		"groups.members.ban",
		"groups.members.view",
		"groups.roles.manage",
		"groups.roles.assign",
		"groups.announcements.send",
		"groups.events.manage",
		"groups.events.publish",
		"groups.tips.manage",
		"groups.tips.withdraw",
		"groups.tips.deposit",
		"groups.group.edit",
	}

	ownerRole := GroupRole{
		Id:             ownerRoleId,
		GroupTag:       tag,
		Name:           "Owner",
		Description:    "Group owner",
		AssignOnJoin:   false,
		SelfAssignable: false,
		Benefits:       []string{},
		Permissions:    allPermissions,
	}

	memberRole := GroupRole{
		Id:             memberId,
		GroupTag:       tag,
		Name:           "Member",
		Description:    "Regular group member",
		AssignOnJoin:   true,
		SelfAssignable: false,
		Benefits:       []string{},
		Permissions:    []string{},
	}

	roles := []GroupRole{ownerRole, memberRole}

	newGroupData := GroupData{
		Group: group,
		Members: []GroupMember{
			{
				Id:                 uuid.New().String(),
				GroupTag:           tag,
				UserId:             ownerId,
				RoleIds:            []string{ownerRoleId, memberId},
				JoinedAt:           time.Now().Unix(),
				MutedAnnouncements: false,
			},
		},
		Roles:           roles,
		Announcements:   []GroupAnnouncement{},
		Events:          map[string]GroupEvent{},
		Tips:            []GroupTip{},
		BenefitProducts: map[string]GroupBenefitProduct{},
		Invites:         []GroupInvite{},
		JoinRequests:    []GroupJoinRequest{},
		Bans:            []GroupBan{},
	}

	groupsDataMutex.Lock()
	if _, exists := groupsData[tag]; exists {
		groupsDataMutex.Unlock()
		c.JSON(400, gin.H{"error": "Group with this tag already exists"})
		return
	}
	for _, data := range groupsData {
		if data.Group.OwnerUserId == ownerId {
			groupsDataMutex.Unlock()
			c.JSON(400, gin.H{"error": "You already own a group"})
			return
		}
	}
	groupsData[tag] = &newGroupData
	groupsDataMutex.Unlock()

	user.applyTransaction(roundVal(userCredits-50), Transaction{
		Note:      "Group creation fee for " + tag,
		User:      UserId(""),
		Amount:    50,
		Type:      "group_create",
		Timestamp: time.Now().UnixMilli(),
	})
	go saveUsers()
	go saveGroupData(tag)

	log.Printf("Created group '%s' with tag '%s'", name, tag)
	log.Printf("Groups in memory: %d", len(groupsData))

	netGroup := group.ToNet()
	netGroup.MemberCount = 1

	c.JSON(201, netGroup)
}

func searchGroups(c *gin.Context) {
	query := strings.ToLower(strings.TrimSpace(c.Query("query")))

	groupsDataMutex.RLock()
	defer groupsDataMutex.RUnlock()

	var results []GroupNet
	for _, data := range groupsData {
		if !data.Group.Public {
			continue
		}
		if query == "" ||
			strings.Contains(strings.ToLower(data.Group.Tag), query) ||
			strings.Contains(strings.ToLower(data.Group.Name), query) ||
			strings.Contains(strings.ToLower(data.Group.Description), query) {
			netGroup := data.Group.ToNet()
			netGroup.MemberCount = len(data.Members)
			results = append(results, netGroup)
		}
	}

	c.JSON(200, results)
}

func joinGroup(c *gin.Context) {
	user := currentUser(c)

	group, groupTag, ok := resolveGroup(c)
	if !ok {
		return
	}

	if !group.Public {
		c.JSON(403, gin.H{"error": "Group is private"})
		return
	}

	members := getGroupMembers(groupTag)
	alreadyMember := false
	id := user.GetId()
	for _, member := range members {
		if member.UserId == id {
			alreadyMember = true
			break
		}
	}

	if alreadyMember {
		c.JSON(400, gin.H{"error": "You are already a member of this group"})
		return
	}

	// Check if user is banned
	groupsDataMutex.RLock()
	data, ok := groupsData[groupTag]
	groupsDataMutex.RUnlock()
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}
	for _, ban := range data.Bans {
		if ban.UserId == id {
			c.JSON(403, gin.H{"error": "You are banned from this group"})
			return
		}
	}

	if group.JoinPolicy == JoinPolicyInvite {
		groupsDataMutex.Lock()
		data = groupsData[groupTag]
		inviteFound := false
		for i, inv := range data.Invites {
			if inv.ToUserId == id && inv.Status == InvitePending {
				data.Invites[i].Status = InviteAccepted
				inviteFound = true
				break
			}
		}
		groupsData[groupTag] = data
		groupsDataMutex.Unlock()
		if !inviteFound {
			c.JSON(403, gin.H{"error": "This group is invite-only and you don't have a pending invite"})
			return
		}
		go saveGroupData(groupTag)
	}

	if group.JoinPolicy == JoinPolicyRequest {
		c.JSON(400, gin.H{"error": "This group requires a join request. Use the join request endpoint instead."})
		return
	}

	if group.EntryFee > 0 {
		userCredits := user.GetCredits()
		if userCredits < group.EntryFee {
			c.JSON(400, gin.H{"error": "Insufficient funds to join this group", "required": group.EntryFee, "available": userCredits})
			return
		}
		user.applyTransaction(roundVal(userCredits-group.EntryFee), Transaction{
			Note:      "Entry fee for group " + groupTag,
			User:      UserId(""),
			Amount:    group.EntryFee,
			Type:      "group_entry_fee",
			Timestamp: time.Now().UnixMilli(),
		})
		groupsDataMutex.Lock()
		data := groupsData[groupTag]
		data.Group.CreditsBalance = roundVal(data.Group.CreditsBalance + group.EntryFee)
		groupsData[groupTag] = data
		groupsDataMutex.Unlock()
		go saveGroupData(groupTag)
		go saveUsers()
	}

	roles := getGroupRoles(groupTag)
	roleIds := groupJoinRoleIds(roles)

	member := GroupMember{
		Id:                 uuid.New().String(),
		GroupTag:           groupTag,
		UserId:             user.GetId(),
		RoleIds:            roleIds,
		JoinedAt:           time.Now().Unix(),
		MutedAnnouncements: false,
	}

	members = append(members, member)
	updateGroupMembers(groupTag, members)
	netGroup := group.ToNet()
	netGroup.MemberCount = len(members)
	c.JSON(200, netGroup)
}

func leaveGroup(c *gin.Context) {
	user := currentUser(c)

	group, groupTag, ok := resolveGroup(c)
	if !ok {
		return
	}

	if user.GetId() == group.OwnerUserId {
		c.JSON(400, gin.H{"error": "You cannot leave the group you own"})
		return
	}

	members := getGroupMembers(groupTag)
	newMembers := make([]GroupMember, 0)
	hasMember := false

	for _, member := range members {
		if member.UserId == user.GetId() {
			hasMember = true
			continue
		}
		newMembers = append(newMembers, member)
	}

	if !hasMember {
		c.JSON(400, gin.H{"error": "You are not a member of this group"})
		return
	}

	updateGroupMembers(groupTag, newMembers)
	netGroup := group.ToNet()
	netGroup.MemberCount = len(newMembers)
	c.JSON(200, netGroup)
}

func getGroup(c *gin.Context) {
	_, groupTag, ok := resolveGroup(c)
	if !ok {
		return
	}

	groupData, ok := getGroupDataByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	publicData := groupData.Group.ToPublic()
	publicData.MemberCount = len(groupData.Members)

	c.JSON(200, publicData)
}

func updateGroup(c *gin.Context) {
	user := currentUser(c)

	groupTag, ok := requireGroupTag(c)
	if !ok {
		return
	}

	var jsonBody map[string]any
	if !bindJSON(c, &jsonBody) {
		return
	}

	group, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if !requireGroupEditAccess(c, group, user.GetId(), groupTag) {
		return
	}

	groupsDataMutex.Lock()
	defer groupsDataMutex.Unlock()

	data := groupsData[groupTag]

	if name, ok := jsonBody["name"].(string); ok {
		name = strings.TrimSpace(name)
		if !requireField(c, name, "Name cannot be empty") {
			return
		}
		if !requireMaxLen(c, name, 50, "Name length exceeded") {
			return
		}
		data.Group.Name = name
	}
	if description, ok := jsonBody["description"].(string); ok {
		data.Group.Description = description
	}
	if readme, ok := jsonBody["readme"].(string); ok {
		if !requireMaxLen(c, readme, 10000, "Readme length exceeded") {
			return
		}
		data.Group.Readme = readme
	}
	if rules, ok := jsonBody["rules"].(string); ok {
		if !requireMaxLen(c, rules, 5000, "Rules length exceeded") {
			return
		}
		data.Group.Rules = rules
	}
	if icon, ok := jsonBody["icon"].(string); ok {
		data.Group.IconUrl = icon
	}
	if icon, ok := jsonBody["icon_url"].(string); ok {
		data.Group.IconUrl = icon
	}
	if banner, ok := jsonBody["banner_url"].(string); ok {
		data.Group.BannerUrl = banner
	}

	if public, ok := jsonBody["public"].(bool); ok {
		data.Group.Public = public
	}
	if joinPolicy, ok := jsonBody["join_policy"].(string); ok {
		policy := JoinPolicy(joinPolicy)
		if policy != JoinPolicyOpen && policy != JoinPolicyRequest && policy != JoinPolicyInvite {
			c.JSON(400, gin.H{"error": "Invalid join policy"})
			return
		}
		data.Group.JoinPolicy = policy
	}
	if entryFee, ok := jsonBody["entry_fee"].(float64); ok {
		if entryFee < 0 {
			c.JSON(400, gin.H{"error": "Entry fee cannot be negative"})
			return
		}
		data.Group.EntryFee = entryFee
	}

	if newTag, ok := jsonBody["tag"].(string); ok {
		newTag = strings.TrimSpace(newTag)
		if !requireField(c, newTag, "Tag cannot be empty") {
			return
		}
		if !requireMaxLen(c, newTag, 10, "Tag must be 10 characters or less") {
			return
		}
		re := regexp.MustCompile(`^[a-zA-Z0-9]+$`)
		if !re.MatchString(newTag) {
			c.JSON(400, gin.H{"error": "Tag must be alphanumeric only"})
			return
		}
		if newTag != groupTag {
			if _, exists := groupsData[newTag]; exists {
				c.JSON(400, gin.H{"error": "Group with this tag already exists"})
				return
			}
			oldTag := groupTag
			data.Group.Tag = newTag
			for i := range data.Members {
				data.Members[i].GroupTag = newTag
			}
			for i := range data.Roles {
				data.Roles[i].GroupTag = newTag
			}
			for i := range data.Announcements {
				data.Announcements[i].GroupTag = newTag
			}
			for k, event := range data.Events {
				event.GroupTag = newTag
				data.Events[k] = event
			}
			for i := range data.Tips {
				data.Tips[i].GroupTag = newTag
			}
			for k, product := range data.BenefitProducts {
				product.GroupTag = newTag
				data.BenefitProducts[k] = product
			}
			for i := range data.Invites {
				data.Invites[i].GroupTag = newTag
			}
			for i := range data.JoinRequests {
				data.JoinRequests[i].GroupTag = newTag
			}
			for i := range data.Bans {
				data.Bans[i].GroupTag = newTag
			}
			delete(groupsData, oldTag)
			groupsData[newTag] = data
			groupTag = newTag
		}
	}
	groupsData[groupTag] = data
	go saveGroupData(groupTag)

	netGroup := data.Group.ToNet()
	netGroup.MemberCount = len(data.Members)
	c.JSON(200, netGroup)
}

func deleteGroup(c *gin.Context) {
	user := currentUser(c)

	group, groupTag, ok := resolveGroup(c)
	if !ok {
		return
	}

	groupsDataMutex.Lock()
	defer groupsDataMutex.Unlock()

	if group.OwnerUserId != user.GetId() {
		c.JSON(403, gin.H{"error": "You are not authorized to delete this group"})
		return
	}

	delete(groupsData, groupTag)
	go deleteGroupData(groupTag)

	c.JSON(200, gin.H{"message": "Group deleted successfully"})
}

func representGroup(c *gin.Context) {
	user := currentUser(c)

	_, groupTag, ok := resolveGroup(c)
	if !ok {
		return
	}

	if !isGroupMember(groupTag, user.GetId()) {
		c.JSON(400, gin.H{"error": "You are not a member of this group"})
		return
	}

	data, _ := getGroupDataByTag(groupTag)
	user.Set("sys.group", data.Group.Id)
	go saveUsers()
	InvalidateUserGroupTagCache(user.GetUsername())

	c.JSON(200, gin.H{"message": "You are now representing this group"})
}

func disrepresentGroup(c *gin.Context) {
	user := currentUser(c)

	user.DelKey("sys.group")
	go saveUsers()
	InvalidateUserGroupTagCache(user.GetUsername())

	c.JSON(200, gin.H{"message": "You are no longer representing any group"})
}

func reportGroup(c *gin.Context) {
	user := currentUser(c)

	group, groupTag, ok := resolveGroup(c)
	if !ok {
		return
	}

	reportStr := fmt.Sprintf("Reported by %s\nGroup: %s (%s)\nDescription: %s\n%s", user.GetUsername(), group.Name, groupTag, group.Description, JSONStringify(group))

	sendReportToDiscord(reportStr)

	c.JSON(200, gin.H{"message": "Report sent successfully"})
}

func hasPermission(userId UserId, groupTag string, permission string) bool {
	member, ok := findGroupMember(groupTag, userId)
	if !ok {
		return false
	}
	rolesMap := getGroupRolesMap(groupTag)
	for _, roleId := range member.RoleIds {
		role, roleExists := rolesMap[roleId]
		if !roleExists {
			continue
		}
		if role.Name == "Owner" {
			return true
		}
		for _, perm := range role.Permissions {
			if perm == permission {
				return true
			}
		}
	}
	return false
}

func getGroupMembersList(c *gin.Context) {
	user := currentUser(c)
	_, groupTag, ok := resolveGroup(c)
	if !ok {
		return
	}

	// Check authorization: owner or member with groups.members.view permission
	userId := user.GetId()
	callerMember, isMember := findGroupMember(groupTag, userId)
	if !isMember {
		c.JSON(403, gin.H{"error": "You don't have permission to view this group's members"})
		return
	}
	if !isOwnerRole(groupTag, callerMember.RoleIds) && !hasPermission(userId, groupTag, "groups.members.view") {
		c.JSON(403, gin.H{"error": "You don't have permission to view this group's members"})
		return
	}

	// Optional search filter: filter by username or user ID
	searchQuery := strings.ToLower(strings.TrimSpace(c.Query("search")))

	members := getGroupMembers(groupTag)
	filteredMembers := members
	if searchQuery != "" {
		filteredMembers = make([]GroupMember, 0)
		for _, m := range members {
			if strings.Contains(strings.ToLower(string(m.UserId)), searchQuery) {
				filteredMembers = append(filteredMembers, m)
				continue
			}
			username := strings.ToLower(string(getUserById(m.UserId).GetUsername()))
			if strings.Contains(username, searchQuery) {
				filteredMembers = append(filteredMembers, m)
			}
		}
	}

	pageStr := c.DefaultQuery("page", "1")
	perPageStr := c.DefaultQuery("per_page", "20")

	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}
	perPage, err := strconv.Atoi(perPageStr)
	if err != nil || perPage < 1 {
		perPage = 20
	}
	if perPage > 100 {
		perPage = 100
	}

	totalMembers := len(filteredMembers)
	start := (page - 1) * perPage
	end := start + perPage
	if start > totalMembers {
		start = totalMembers
	}
	if end > totalMembers {
		end = totalMembers
	}

	// Return members sorted by join date (newest first)
	sortedMembers := make([]GroupMember, totalMembers)
	copy(sortedMembers, filteredMembers)
	sort.Slice(sortedMembers, func(i, j int) bool {
		return sortedMembers[i].JoinedAt > sortedMembers[j].JoinedAt
	})

	pagedMembers := sortedMembers[start:end]
	results := make([]GroupMemberNet, 0, len(pagedMembers))
	for _, member := range pagedMembers {
		results = append(results, member.ToNet())
	}

	totalPages := (totalMembers + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}

	c.JSON(200, gin.H{
		"members":  results,
		"page":     page,
		"per_page": perPage,
		"total":    totalMembers,
		"pages":    totalPages,
	})
}

func getTopGroups(c *gin.Context) {
	groupsDataMutex.RLock()
	defer groupsDataMutex.RUnlock()
	type groupWithCount struct {
		data  *GroupData
		count int
	}
	var groups []groupWithCount
	for _, data := range groupsData {
		if data.Group.Public {
			groups = append(groups, groupWithCount{data: data, count: len(data.Members)})
		}
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].count > groups[j].count
	})
	limit := 10
	if len(groups) < limit {
		limit = len(groups)
	}
	results := make([]GroupPublic, 0, limit)
	for i := 0; i < limit; i++ {
		publicGroup := groups[i].data.Group.ToPublic()
		publicGroup.MemberCount = groups[i].count
		results = append(results, publicGroup)
	}
	c.JSON(200, results)
}

func uploadGroupIcon(c *gin.Context) {
	user := currentUser(c)
	group, groupTag, ok := resolveGroup(c)
	if !ok {
		return
	}
	if !requireGroupEditAccess(c, group, user.GetId(), groupTag) {
		return
	}
	file, header, err := c.Request.FormFile("icon")
	if err != nil {
		c.JSON(400, gin.H{"error": "Icon image file is required"})
		return
	}
	defer file.Close()
	if header.Size > 5*1024*1024 {
		c.JSON(400, gin.H{"error": "Image too large (max 5MB)"})
		return
	}
	imageData, err := io.ReadAll(file)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to read image"})
		return
	}
	if err := checkImageDimensions(imageData); err != nil {
		c.JSON(400, gin.H{"error": "Invalid image format"})
		return
	}
	dirPath := groupDirPath(groupTag)
	os.MkdirAll(dirPath, 0755)
	// Remove old icons
	for _, ext := range []string{".jpg", ".png", ".gif"} {
		os.Remove(filepath.Join(dirPath, "icon"+ext))
	}
	iconPath := filepath.Join(dirPath, "icon.jpg")
	if err := writeResizedJPEG(imageData, 256, 256, iconPath); err != nil {
		c.JSON(500, gin.H{"error": "Failed to encode icon"})
		return
	}
	// Update icon URL
	groupsDataMutex.Lock()
	data := groupsData[groupTag]
	data.Group.IconUrl = config.BASE_URL + "/groups/" + groupTag + "/icon.jpg"
	groupsData[groupTag] = data
	groupsDataMutex.Unlock()
	go saveGroupData(groupTag)
	c.JSON(200, gin.H{"message": "Icon uploaded", "icon_url": data.Group.IconUrl})
}

func getGroupIcon(c *gin.Context) {
	groupTag := c.Param("grouptag")
	iconPath := getGroupIconPath(groupTag)
	if iconPath == "" {
		c.JSON(404, gin.H{"error": "No icon found"})
		return
	}
	c.File(iconPath)
}

func uploadGroupBanner(c *gin.Context) {
	user := currentUser(c)
	group, groupTag, ok := resolveGroup(c)
	if !ok {
		return
	}
	if !requireGroupEditAccess(c, group, user.GetId(), groupTag) {
		return
	}

	file, header, err := c.Request.FormFile("banner")
	if err != nil {
		c.JSON(400, gin.H{"error": "Banner image file is required"})
		return
	}
	defer file.Close()

	if header.Size > 5*1024*1024 {
		c.JSON(400, gin.H{"error": "Image too large (max 5MB)"})
		return
	}

	imageData, err := io.ReadAll(file)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to read image"})
		return
	}

	if err := checkImageDimensions(imageData); err != nil {
		c.JSON(400, gin.H{"error": "Invalid image format"})
		return
	}

	contentType := header.Header.Get("Content-Type")
	var ext string
	switch {
	case strings.Contains(contentType, "image/gif"):
		ext = ".gif"
	case strings.Contains(contentType, "image/png"):
		ext = ".png"
	default:
		ext = ".jpg"
	}

	dirPath := groupDirPath(groupTag)
	os.MkdirAll(dirPath, 0755)

	for _, oldExt := range []string{".jpg", ".png", ".gif"} {
		os.Remove(filepath.Join(dirPath, "banner"+oldExt))
	}

	if ext == ".gif" {
		resizedData, err := imageutil.ResizeGIF(imageData, 900, 300)
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid GIF image"})
			return
		}
		bannerPath := filepath.Join(dirPath, "banner.gif")
		if err := os.WriteFile(bannerPath, resizedData, 0644); err != nil {
			c.JSON(500, gin.H{"error": "Failed to save banner"})
			return
		}
	} else {
		img, _, err := image.Decode(bytes.NewReader(imageData))
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid image format"})
			return
		}
		resized := resize.Resize(900, 300, img, resize.Lanczos3)
		bannerPath := filepath.Join(dirPath, "banner"+ext)
		out, err := os.Create(bannerPath)
		if err != nil {
			c.JSON(500, gin.H{"error": "Failed to save banner"})
			return
		}
		defer out.Close()
		if ext == ".png" {
			if err := png.Encode(out, resized); err != nil {
				c.JSON(500, gin.H{"error": "Failed to encode banner"})
				return
			}
		} else {
			if err := jpeg.Encode(out, resized, &jpeg.Options{Quality: 85}); err != nil {
				c.JSON(500, gin.H{"error": "Failed to encode banner"})
				return
			}
		}
	}

	groupsDataMutex.Lock()
	data := groupsData[groupTag]
	data.Group.BannerUrl = config.BASE_URL + "/groups/" + groupTag + "/banner"
	groupsData[groupTag] = data
	groupsDataMutex.Unlock()
	go saveGroupData(groupTag)

	c.JSON(200, gin.H{"message": "Banner uploaded", "banner_url": data.Group.BannerUrl})
}

func getGroupBanner(c *gin.Context) {
	groupTag := c.Param("grouptag")
	bannerPath := getGroupBannerPath(groupTag)
	if bannerPath == "" {
		c.JSON(404, gin.H{"error": "No banner found"})
		return
	}
	c.File(bannerPath)
}
