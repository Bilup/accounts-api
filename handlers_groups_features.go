package main

import (
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func stringsFromJSONValue(value any) ([]string, bool) {
	switch v := value.(type) {
	case []string:
		return v, true
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	default:
		return nil, false
	}
}

func createAnnouncement(c *gin.Context) {
	user := c.MustGet("user").(*User)

	groupTag := c.Param("grouptag")
	if groupTag == "" {
		c.JSON(400, gin.H{"error": "Group tag is required"})
		return
	}

	title := c.Query("title")
	if title == "" {
		c.JSON(400, gin.H{"error": "Title is required"})
		return
	}
	if len(title) > 100 {
		c.JSON(400, gin.H{"error": "Title length exceeded"})
		return
	}

	body := c.Query("body")
	if len(body) > 2000 {
		c.JSON(400, gin.H{"error": "Body length exceeded"})
		return
	}

	_, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if !hasPermission(user.GetId(), groupTag, "groups.announcements.send") {
		c.JSON(403, gin.H{"error": "You don't have permission to send announcements"})
		return
	}

	pingMembers := c.DefaultQuery("ping_members", "false") == "true"

	announcement := GroupAnnouncement{
		Id:           uuid.New().String(),
		GroupTag:     groupTag,
		Title:        title,
		Body:         body,
		AuthorUserId: user.GetId(),
		CreatedAt:    time.Now().Unix(),
		PingMembers:  pingMembers,
	}

	addGroupAnnouncement(groupTag, announcement)
	// Send push notifications to group members
	go func() {
		members := getGroupMembers(groupTag)
		for _, member := range members {
			if member.UserId == user.GetId() {
				continue
			}
			if member.MutedAnnouncements {
				continue
			}
			targetUser := member.UserId.User()
			if isNotifyAllowed(targetUser, "group_"+groupTag, user.GetId()) {
				sendPushNotificationToUser(targetUser, *user, NotificationRequest{
					Source: "group_" + groupTag,
					Title:  "[" + groupTag + "] " + title,
					Body:   body,
				})
			}
		}
	}()

	c.JSON(201, announcement)
}

func getAnnouncements(c *gin.Context) {
	groupTag := c.Param("grouptag")
	if groupTag == "" {
		c.JSON(400, gin.H{"error": "Group tag is required"})
		return
	}

	_, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	limitStr := c.DefaultQuery("limit", "10")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 10
	}

	announcements := getGroupAnnouncements(groupTag)

	var results []GroupAnnouncementNet
	count := 0
	for i := len(announcements) - 1; i >= 0 && count < limit; i-- {
		if announcements[i].GroupTag == groupTag {
			results = append(results, announcements[i].ToNet())
			count++
		}
	}

	c.JSON(200, results)
}

func deleteAnnouncement(c *gin.Context) {
	user := c.MustGet("user").(*User)

	groupTag := c.Param("grouptag")
	announcementId := c.Param("announcementid")

	if groupTag == "" || announcementId == "" {
		c.JSON(400, gin.H{"error": "Group tag and announcement ID are required"})
		return
	}

	_, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if !hasPermission(user.GetId(), groupTag, "groups.announcements.send") {
		c.JSON(403, gin.H{"error": "You don't have permission to delete announcements"})
		return
	}

	announcements := getGroupAnnouncements(groupTag)
	newAnnouncements := make([]GroupAnnouncement, 0)
	found := false

	for _, ann := range announcements {
		if ann.Id == announcementId && ann.GroupTag == groupTag {
			found = true
			continue
		}
		newAnnouncements = append(newAnnouncements, ann)
	}

	if !found {
		c.JSON(404, gin.H{"error": "Announcement not found"})
		return
	}

	groupsDataMutex.Lock()
	data := groupsData[groupTag]
	data.Announcements = newAnnouncements
	groupsData[groupTag] = data
	groupsDataMutex.Unlock()

	go saveGroupData(groupTag)

	c.JSON(200, gin.H{"message": "Announcement deleted"})
}

func createEvent(c *gin.Context) {
	user := c.MustGet("user").(*User)

	groupTag := c.Param("grouptag")
	if groupTag == "" {
		c.JSON(400, gin.H{"error": "Group tag is required"})
		return
	}

	title := c.Query("title")
	if title == "" {
		c.JSON(400, gin.H{"error": "Title is required"})
		return
	}
	if len(title) > 100 {
		c.JSON(400, gin.H{"error": "Title length exceeded"})
		return
	}

	description := c.Query("description")
	if len(description) > 500 {
		c.JSON(400, gin.H{"error": "Description length exceeded"})
		return
	}

	location := c.Query("location")
	if len(location) > 200 {
		c.JSON(400, gin.H{"error": "Location length exceeded"})
		return
	}

	_, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if !hasPermission(user.GetId(), groupTag, "groups.events.manage") {
		c.JSON(403, gin.H{"error": "You don't have permission to manage events"})
		return
	}

	startTimeStr := c.Query("start_time")
	durationHoursStr := c.DefaultQuery("duration_hours", "1")

	startTime, err := strconv.ParseInt(startTimeStr, 10, 64)
	if err != nil || startTime <= time.Now().Unix() {
		c.JSON(400, gin.H{"error": "Invalid start time"})
		return
	}

	durationHours, err := strconv.Atoi(durationHoursStr)
	if err != nil || durationHours <= 0 || durationHours > 72 {
		c.JSON(400, gin.H{"error": "Invalid duration (must be 1-72 hours)"})
		return
	}

	endTime := startTime + (int64(durationHours) * 3600)

	visibilityRaw := c.DefaultQuery("visibility", "MEMBERS")
	visibility := EventVisibility(visibilityRaw)
	if visibility != EventVisibilityMembers && visibility != EventVisibilityPublic {
		c.JSON(400, gin.H{"error": "Invalid visibility"})
		return
	}

	published := c.DefaultQuery("published", "false") == "true"
	if published {
		if !hasPermission(user.GetId(), groupTag, "groups.events.publish") {
			c.JSON(403, gin.H{"error": "You don't have permission to publish events"})
			return
		}
	}

	eventId := uuid.New().String()
	event := GroupEvent{
		Id:          eventId,
		GroupTag:    groupTag,
		Title:       title,
		Description: description,
		StartTime:   startTime,
		EndTime:     endTime,
		Location:    location,
		Visibility:  visibility,
		CreatedBy:   user.GetId(),
		Published:   published,
	}

	addGroupEvent(groupTag, event)

	c.JSON(201, event)
}

func updateEvent(c *gin.Context) {
	user := c.MustGet("user").(*User)
	groupTag := c.Param("grouptag")
	eventId := c.Param("eventid")
	if groupTag == "" || eventId == "" {
		c.JSON(400, gin.H{"error": "Group tag and event ID are required"})
		return
	}

	_, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if !hasPermission(user.GetId(), groupTag, "groups.events.manage") {
		c.JSON(403, gin.H{"error": "You don't have permission to manage events"})
		return
	}

	var updateData map[string]any
	if !bindJSON(c, &updateData) {
		return
	}

	groupsDataMutex.Lock()
	defer groupsDataMutex.Unlock()

	data, ok := groupsData[groupTag]
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	event, ok := data.Events[eventId]
	if !ok || event.GroupTag != groupTag {
		c.JSON(404, gin.H{"error": "Event not found"})
		return
	}

	if title, ok := updateData["title"].(string); ok {
		if title == "" {
			c.JSON(400, gin.H{"error": "Title is required"})
			return
		}
		if len(title) > 100 {
			c.JSON(400, gin.H{"error": "Title length exceeded"})
			return
		}
		event.Title = title
	}
	if description, ok := updateData["description"].(string); ok {
		if len(description) > 500 {
			c.JSON(400, gin.H{"error": "Description length exceeded"})
			return
		}
		event.Description = description
	}
	if location, ok := updateData["location"].(string); ok {
		if len(location) > 200 {
			c.JSON(400, gin.H{"error": "Location length exceeded"})
			return
		}
		event.Location = location
	}
	if startTimeFloat, ok := updateData["start_time"].(float64); ok {
		startTime := int64(startTimeFloat)
		if startTime <= time.Now().Unix() {
			c.JSON(400, gin.H{"error": "Invalid start time"})
			return
		}
		duration := event.EndTime - event.StartTime
		if duration <= 0 {
			duration = 3600
		}
		event.StartTime = startTime
		event.EndTime = startTime + duration
	}
	if durationFloat, ok := updateData["duration_hours"].(float64); ok {
		durationHours := int64(durationFloat)
		if durationHours <= 0 || durationHours > 72 {
			c.JSON(400, gin.H{"error": "Invalid duration (must be 1-72 hours)"})
			return
		}
		event.EndTime = event.StartTime + (durationHours * 3600)
	}
	if visibilityRaw, ok := updateData["visibility"].(string); ok {
		visibility := EventVisibility(visibilityRaw)
		if visibility != EventVisibilityMembers && visibility != EventVisibilityPublic {
			c.JSON(400, gin.H{"error": "Invalid visibility"})
			return
		}
		event.Visibility = visibility
	}
	if published, ok := updateData["published"].(bool); ok {
		if published && !hasPermission(user.GetId(), groupTag, "groups.events.publish") {
			c.JSON(403, gin.H{"error": "You don't have permission to publish events"})
			return
		}
		event.Published = published
	}

	data.Events[eventId] = event
	groupsData[groupTag] = data
	go saveGroupData(groupTag)

	c.JSON(200, event.ToNet())
}

func deleteEvent(c *gin.Context) {
	user := c.MustGet("user").(*User)
	groupTag := c.Param("grouptag")
	eventId := c.Param("eventid")
	if groupTag == "" || eventId == "" {
		c.JSON(400, gin.H{"error": "Group tag and event ID are required"})
		return
	}

	_, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if !hasPermission(user.GetId(), groupTag, "groups.events.manage") {
		c.JSON(403, gin.H{"error": "You don't have permission to manage events"})
		return
	}

	groupsDataMutex.Lock()
	defer groupsDataMutex.Unlock()

	data, ok := groupsData[groupTag]
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	event, ok := data.Events[eventId]
	if !ok || event.GroupTag != groupTag {
		c.JSON(404, gin.H{"error": "Event not found"})
		return
	}

	delete(data.Events, eventId)
	groupsData[groupTag] = data
	go saveGroupData(groupTag)

	c.JSON(200, gin.H{"message": "Event deleted"})
}

func getEvents(c *gin.Context) {
	groupTag := c.Param("grouptag")
	if groupTag == "" {
		c.JSON(400, gin.H{"error": "Group tag is required"})
		return
	}

	_, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	user := c.MustGet("user").(*User)

	events := getGroupEvents(groupTag)
	members := getGroupMembers(groupTag)

	isMember := false
	for _, member := range members {
		if member.UserId == user.GetId() {
			isMember = true
			break
		}
	}

	var results []GroupEventNet
	for _, event := range events {
		if event.GroupTag == groupTag {
			switch event.Visibility {
			case EventVisibilityPublic:
				results = append(results, event.ToNet())
			case EventVisibilityMembers:
				if isMember {
					results = append(results, event.ToNet())
				}
			}
		}
	}

	c.JSON(200, results)
}

func sendTip(c *gin.Context) {
	user := c.MustGet("user").(*User)

	groupTag := c.Param("grouptag")
	if groupTag == "" {
		c.JSON(400, gin.H{"error": "Group tag is required"})
		return
	}

	amountStr := c.Query("amount")
	amount, err := strconv.ParseFloat(amountStr, 64)
	if err != nil || amount <= 0 || !isFiniteAmount(amount) {
		c.JSON(400, gin.H{"error": "Invalid amount"})
		return
	}
	note := c.Query("note")
	if len(note) > 200 {
		c.JSON(400, gin.H{"error": "Note length exceeded (max 200)"})
		return
	}

	group, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	members := getGroupMembers(groupTag)
	isMember := false
	for _, member := range members {
		if member.UserId == user.GetId() {
			isMember = true
			break
		}
	}

	if !isMember && !group.Public {
		c.JSON(403, gin.H{"error": "You can only tip groups you're a member of"})
		return
	}

	nAmount := roundVal(amount)
	userCredits := user.GetCredits()
	if userCredits < nAmount {
		c.JSON(400, gin.H{"error": "Insufficient funds", "required": nAmount, "available": userCredits})
		return
	}

	user.SetBalance(roundVal(userCredits - nAmount))
	user.addTransaction(Transaction{
		Note:      "Tip to group " + groupTag,
		User:      UserId(""),
		Amount:    nAmount,
		Type:      "group_tip",
		Timestamp: time.Now().UnixMilli(),
		NewTotal:  roundVal(userCredits - nAmount),
	})

	tip := GroupTip{
		Id:            uuid.New().String(),
		GroupTag:      groupTag,
		FromUserId:    user.GetId(),
		AmountCredits: nAmount,
		Note:          note,
		CreatedAt:     time.Now().Unix(),
	}
	addGroupTip(groupTag, tip)
	go saveUsers()

	c.JSON(201, tip)
}

func getGroupProducts(c *gin.Context) {
	groupTag := c.Param("grouptag")
	if groupTag == "" {
		c.JSON(400, gin.H{"error": "Group tag is required"})
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

	results := make([]GroupBenefitProductNet, 0, len(data.BenefitProducts))
	for _, product := range data.BenefitProducts {
		results = append(results, product.ToNet())
	}

	c.JSON(200, results)
}

func nextGroupBillingTime(frequency int, period string) int64 {
	if frequency <= 0 {
		frequency = 1
	}
	now := time.Now()
	switch strings.ToLower(period) {
	case "day":
		return now.AddDate(0, 0, frequency).UnixMilli()
	case "week":
		return now.AddDate(0, 0, frequency*7).UnixMilli()
	case "year":
		return now.AddDate(frequency, 0, 0).UnixMilli()
	default:
		return now.AddDate(0, frequency, 0).UnixMilli()
	}
}

func createGroupProduct(c *gin.Context) {
	user := c.MustGet("user").(*User)
	groupTag := c.Param("grouptag")
	if groupTag == "" {
		c.JSON(400, gin.H{"error": "Group tag is required"})
		return
	}

	_, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if !hasPermission(user.GetId(), groupTag, "groups.roles.manage") {
		c.JSON(403, gin.H{"error": "You don't have permission to manage role products"})
		return
	}

	name := c.Query("name")
	if name == "" {
		c.JSON(400, gin.H{"error": "Name is required"})
		return
	}
	if len(name) > 50 {
		c.JSON(400, gin.H{"error": "Name length exceeded"})
		return
	}

	description := c.Query("description")
	if len(description) > 200 {
		c.JSON(400, gin.H{"error": "Description length exceeded"})
		return
	}

	price, err := strconv.ParseFloat(c.Query("price_credits"), 64)
	if err != nil || price <= 0 || !isFiniteAmount(price) {
		c.JSON(400, gin.H{"error": "Invalid price"})
		return
	}
	subscription := c.DefaultQuery("subscription", "false") == "true"
	frequency := 0
	period := ""
	if subscription {
		frequency, err = strconv.Atoi(c.DefaultQuery("frequency", "1"))
		if err != nil || frequency <= 0 {
			c.JSON(400, gin.H{"error": "Invalid frequency"})
			return
		}
		period = strings.ToLower(c.DefaultQuery("period", "month"))
		if period != "day" && period != "week" && period != "month" && period != "year" {
			c.JSON(400, gin.H{"error": "Invalid period"})
			return
		}
	}

	roleId := c.Query("role_id")
	if roleId == "" {
		c.JSON(400, gin.H{"error": "Role ID is required"})
		return
	}

	rolesMap := getGroupRolesMap(groupTag)
	role, roleExists := rolesMap[roleId]
	if !roleExists || role.GroupTag != groupTag {
		c.JSON(404, gin.H{"error": "Role not found"})
		return
	}
	if role.Name == "Owner" {
		c.JSON(400, gin.H{"error": "Owner role cannot be sold"})
		return
	}

	product := GroupBenefitProduct{
		Id:            uuid.New().String(),
		GroupTag:      groupTag,
		Name:          name,
		Description:   description,
		PriceCredits:  roundVal(price),
		RoleGrantedId: roleId,
		Subscription:  subscription,
		Frequency:     frequency,
		Period:        period,
	}

	groupsDataMutex.Lock()
	data, ok := groupsData[groupTag]
	if !ok {
		groupsDataMutex.Unlock()
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}
	if data.BenefitProducts == nil {
		data.BenefitProducts = make(map[string]GroupBenefitProduct)
	}
	if data.ProductSubscriptions == nil {
		data.ProductSubscriptions = make(map[string]GroupProductSubscription)
	}
	data.BenefitProducts[product.Id] = product
	groupsData[groupTag] = data
	groupsDataMutex.Unlock()
	go saveGroupData(groupTag)

	c.JSON(201, product.ToNet())
}

func deleteGroupProduct(c *gin.Context) {
	user := c.MustGet("user").(*User)
	groupTag := c.Param("grouptag")
	productId := c.Param("productid")
	if groupTag == "" || productId == "" {
		c.JSON(400, gin.H{"error": "Group tag and product ID are required"})
		return
	}

	_, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if !hasPermission(user.GetId(), groupTag, "groups.roles.manage") {
		c.JSON(403, gin.H{"error": "You don't have permission to manage role products"})
		return
	}

	groupsDataMutex.Lock()
	data, ok := groupsData[groupTag]
	if !ok {
		groupsDataMutex.Unlock()
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}
	if _, ok := data.BenefitProducts[productId]; !ok {
		groupsDataMutex.Unlock()
		c.JSON(404, gin.H{"error": "Product not found"})
		return
	}
	delete(data.BenefitProducts, productId)
	groupsData[groupTag] = data
	groupsDataMutex.Unlock()
	go saveGroupData(groupTag)

	c.JSON(200, gin.H{"message": "Product deleted"})
}

func purchaseGroupProduct(c *gin.Context) {
	user := c.MustGet("user").(*User)
	groupTag := c.Param("grouptag")
	productId := c.Param("productid")
	if groupTag == "" || productId == "" {
		c.JSON(400, gin.H{"error": "Group tag and product ID are required"})
		return
	}

	group, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	userId := user.GetId()
	groupsDataMutex.Lock()
	data, ok := groupsData[groupTag]
	if !ok {
		groupsDataMutex.Unlock()
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	product, ok := data.BenefitProducts[productId]
	if !ok {
		groupsDataMutex.Unlock()
		c.JSON(404, gin.H{"error": "Product not found"})
		return
	}

	memberIdx := -1
	for i, member := range data.Members {
		if member.UserId == userId {
			memberIdx = i
			break
		}
	}
	if memberIdx == -1 {
		groupsDataMutex.Unlock()
		c.JSON(403, gin.H{"error": "You must be a member to purchase this role"})
		return
	}

	if product.RoleGrantedId == "" {
		groupsDataMutex.Unlock()
		c.JSON(400, gin.H{"error": "Product does not grant a role"})
		return
	}

	roleExists := false
	for _, role := range data.Roles {
		if role.Id == product.RoleGrantedId && role.GroupTag == groupTag {
			roleExists = true
			break
		}
	}
	if !roleExists {
		groupsDataMutex.Unlock()
		c.JSON(404, gin.H{"error": "Role no longer exists"})
		return
	}

	for _, roleId := range data.Members[memberIdx].RoleIds {
		if roleId == product.RoleGrantedId {
			groupsDataMutex.Unlock()
			c.JSON(400, gin.H{"error": "You already have this role"})
			return
		}
	}
	if product.Subscription {
		for _, sub := range data.ProductSubscriptions {
			if sub.UserId == userId && sub.ProductId == product.Id && sub.Active {
				groupsDataMutex.Unlock()
				c.JSON(400, gin.H{"error": "You already have this subscription"})
				return
			}
		}
	}

	userCredits := user.GetCredits()
	if userCredits < product.PriceCredits {
		groupsDataMutex.Unlock()
		c.JSON(400, gin.H{"error": "Insufficient funds", "required": product.PriceCredits, "available": userCredits})
		return
	}

	user.SetBalance(roundVal(userCredits - product.PriceCredits))
	user.addTransaction(Transaction{
		Note:      "Purchased role product " + product.Name + " from group " + groupTag,
		User:      UserId(""),
		Amount:    product.PriceCredits,
		Type:      "group_role_purchase",
		Timestamp: time.Now().UnixMilli(),
		NewTotal:  roundVal(userCredits - product.PriceCredits),
	})

	data.Members[memberIdx].RoleIds = append(data.Members[memberIdx].RoleIds, product.RoleGrantedId)
	var sub *GroupProductSubscription
	if product.Subscription {
		if data.ProductSubscriptions == nil {
			data.ProductSubscriptions = make(map[string]GroupProductSubscription)
		}
		newSub := GroupProductSubscription{
			Id:          uuid.New().String(),
			GroupTag:    groupTag,
			ProductId:   product.Id,
			UserId:      userId,
			RoleId:      product.RoleGrantedId,
			StartedAt:   time.Now().UnixMilli(),
			NextBilling: nextGroupBillingTime(product.Frequency, product.Period),
			Active:      true,
		}
		data.ProductSubscriptions[newSub.Id] = newSub
		sub = &newSub
	}
	data.Group.CreditsBalance = roundVal(data.Group.CreditsBalance + product.PriceCredits)
	groupsData[groupTag] = data
	groupsDataMutex.Unlock()

	go saveGroupData(groupTag)
	go saveUsers()

	netGroup := group.ToNet()
	netGroup.CreditsBalance = roundVal(group.CreditsBalance + product.PriceCredits)
	netGroup.MemberCount = len(data.Members)
	c.JSON(200, gin.H{
		"message": "Product purchased",
		"product": product.ToNet(),
		"subscription": func() any {
			if sub == nil {
				return nil
			}
			return sub.ToNet()
		}(),
		"group": netGroup,
	})
}

func checkGroupProductOwnership(c *gin.Context) {
	groupTag := c.Param("grouptag")
	productId := c.Param("productid")
	username := c.Param("username")
	if groupTag == "" || productId == "" || username == "" {
		c.JSON(400, gin.H{"error": "Group tag, product ID, and username are required"})
		return
	}
	userId := userIdFromParam(username)

	groupsDataMutex.RLock()
	data, ok := groupsData[groupTag]
	if !ok {
		groupsDataMutex.RUnlock()
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}
	product, ok := data.BenefitProducts[productId]
	if !ok {
		groupsDataMutex.RUnlock()
		c.JSON(404, gin.H{"error": "Product not found"})
		return
	}
	owned := false
	for _, member := range data.Members {
		if member.UserId != userId {
			continue
		}
		for _, roleId := range member.RoleIds {
			if roleId == product.RoleGrantedId {
				owned = true
				break
			}
		}
	}
	var sub *GroupProductSubscription
	for _, s := range data.ProductSubscriptions {
		if s.UserId == userId && s.ProductId == productId && s.Active {
			copySub := s
			sub = &copySub
			break
		}
	}
	groupsDataMutex.RUnlock()

	c.JSON(200, gin.H{
		"owned":     owned,
		"username":  username,
		"group_tag": groupTag,
		"product":   product.ToNet(),
		"subscription": func() any {
			if sub == nil {
				return nil
			}
			return sub.ToNet()
		}(),
	})
}

func getMyGroupProductSubscriptions(c *gin.Context) {
	user := c.MustGet("user").(*User)
	userId := user.GetId()
	groupsDataMutex.RLock()
	results := make([]GroupProductSubscriptionNet, 0)
	for _, data := range groupsData {
		for _, sub := range data.ProductSubscriptions {
			if sub.UserId == userId && sub.Active {
				results = append(results, sub.ToNet())
			}
		}
	}
	groupsDataMutex.RUnlock()
	c.JSON(200, results)
}

func cancelGroupProductSubscription(c *gin.Context) {
	user := c.MustGet("user").(*User)
	groupTag := c.Param("grouptag")
	productId := c.Param("productid")
	if groupTag == "" || productId == "" {
		c.JSON(400, gin.H{"error": "Group tag and product ID are required"})
		return
	}
	userId := user.GetId()
	groupsDataMutex.Lock()
	data, ok := groupsData[groupTag]
	if !ok {
		groupsDataMutex.Unlock()
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}
	for id, sub := range data.ProductSubscriptions {
		if sub.UserId == userId && sub.ProductId == productId && sub.Active {
			cancelAt := sub.NextBilling
			sub.CancelAt = &cancelAt
			data.ProductSubscriptions[id] = sub
			groupsData[groupTag] = data
			groupsDataMutex.Unlock()
			go saveGroupData(groupTag)
			c.JSON(200, gin.H{"status": "Cancellation scheduled", "cancel_at": cancelAt, "subscription": sub.ToNet()})
			return
		}
	}
	groupsDataMutex.Unlock()
	c.JSON(404, gin.H{"error": "Active subscription not found"})
}

func removeRoleId(roleIds []string, roleId string) []string {
	out := make([]string, 0, len(roleIds))
	for _, id := range roleIds {
		if id != roleId {
			out = append(out, id)
		}
	}
	return out
}

func processGroupProductSubscriptions() {
	now := time.Now().UnixMilli()
	usersDirty := false
	groupsDirty := make(map[string]bool)

	groupsDataMutex.Lock()
	for groupTag, data := range groupsData {
		if data.ProductSubscriptions == nil {
			continue
		}
		for subId, sub := range data.ProductSubscriptions {
			if !sub.Active || sub.NextBilling <= 0 || sub.NextBilling > now {
				continue
			}

			if sub.CancelAt != nil && *sub.CancelAt <= now {
				sub.Active = false
				data.ProductSubscriptions[subId] = sub
				for i := range data.Members {
					if data.Members[i].UserId == sub.UserId {
						data.Members[i].RoleIds = removeRoleId(data.Members[i].RoleIds, sub.RoleId)
						break
					}
				}
				groupsDirty[groupTag] = true
				continue
			}

			product, ok := data.BenefitProducts[sub.ProductId]
			if !ok || !product.Subscription {
				sub.Active = false
				data.ProductSubscriptions[subId] = sub
				groupsDirty[groupTag] = true
				continue
			}

			purchaser, err := getAccountByUserId(sub.UserId)
			if err != nil || purchaser.GetCredits() < product.PriceCredits {
				sub.Active = false
				data.ProductSubscriptions[subId] = sub
				for i := range data.Members {
					if data.Members[i].UserId == sub.UserId {
						data.Members[i].RoleIds = removeRoleId(data.Members[i].RoleIds, sub.RoleId)
						break
					}
				}
				groupsDirty[groupTag] = true
				continue
			}

			newBalance := roundVal(purchaser.GetCredits() - product.PriceCredits)
			purchaser.SetBalance(newBalance)
			purchaser.addTransaction(Transaction{
				Note:      "Group role subscription " + product.Name + " for " + groupTag,
				User:      UserId(""),
				Amount:    product.PriceCredits,
				Type:      "group_role_subscription",
				Timestamp: time.Now().UnixMilli(),
				NewTotal:  newBalance,
			})
			usersDirty = true
			data.Group.CreditsBalance = roundVal(data.Group.CreditsBalance + product.PriceCredits)
			sub.NextBilling = nextGroupBillingTime(product.Frequency, product.Period)
			data.ProductSubscriptions[subId] = sub
			groupsDirty[groupTag] = true
		}
		groupsData[groupTag] = data
	}
	groupsDataMutex.Unlock()

	for groupTag := range groupsDirty {
		go saveGroupData(groupTag)
	}
	if usersDirty {
		go saveUsers()
	}
}

func getTips(c *gin.Context) {
	groupTag := c.Param("grouptag")
	if groupTag == "" {
		c.JSON(400, gin.H{"error": "Group tag is required"})
		return
	}

	_, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	limitStr := c.DefaultQuery("limit", "20")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 20
	}

	tips := getGroupTips(groupTag)

	var results []GroupTipNet
	count := 0
	for i := len(tips) - 1; i >= 0 && count < limit; i-- {
		if tips[i].GroupTag == groupTag {
			results = append(results, tips[i].ToNet())
			count++
		}
	}

	c.JSON(200, results)
}

func withdrawTip(c *gin.Context) {
	user := c.MustGet("user").(*User)
	groupTag := c.Param("grouptag")
	if groupTag == "" {
		c.JSON(400, gin.H{"error": "Group tag is required"})
		return
	}

	amountStr := c.Query("amount")
	amount, err := strconv.ParseFloat(amountStr, 64)
	if err != nil || amount <= 0 || !isFiniteAmount(amount) {
		c.JSON(400, gin.H{"error": "Invalid amount"})
		return
	}

	group, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if !hasPermission(user.GetId(), groupTag, "groups.tips.withdraw") {
		c.JSON(403, gin.H{"error": "You don't have permission to withdraw from the group tip jar"})
		return
	}

	nAmount := roundVal(amount)

	if nAmount > group.CreditsBalance {
		c.JSON(400, gin.H{"error": "Insufficient funds in group tip jar", "required": nAmount, "available": group.CreditsBalance})
		return
	}

	withdrawal := GroupTipWithdrawal{
		Id:            uuid.New().String(),
		GroupTag:      groupTag,
		ToUserId:      user.GetId(),
		AmountCredits: nAmount,
		CreatedAt:     time.Now().Unix(),
	}

	if !addGroupWithdrawal(groupTag, withdrawal) {
		c.JSON(400, gin.H{"error": "Insufficient funds in group tip jar"})
		return
	}

	userCredits := user.GetCredits()
	user.SetBalance(roundVal(userCredits + nAmount))
	user.addTransaction(Transaction{
		Note:      "Withdrawal from group " + groupTag + " tip jar",
		User:      UserId(""),
		Amount:    nAmount,
		Type:      "group_tip_withdrawal",
		Timestamp: time.Now().UnixMilli(),
		NewTotal:  roundVal(userCredits + nAmount),
	})
	go saveUsers()

	c.JSON(201, withdrawal.ToNet())
}

func getWithdrawals(c *gin.Context) {
	groupTag := c.Param("grouptag")
	if groupTag == "" {
		c.JSON(400, gin.H{"error": "Group tag is required"})
		return
	}

	_, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if !hasPermission(c.MustGet("user").(*User).GetId(), groupTag, "groups.tips.withdraw") {
		c.JSON(403, gin.H{"error": "You don't have permission to view withdrawals"})
		return
	}

	limitStr := c.DefaultQuery("limit", "20")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 20
	}

	withdrawals := loadGroupWithdrawals(groupTag)
	var results []GroupTipWithdrawalNet
	count := 0
	for i := len(withdrawals) - 1; i >= 0 && count < limit; i-- {
		if withdrawals[i].GroupTag == groupTag {
			results = append(results, withdrawals[i].ToNet())
			count++
		}
	}

	c.JSON(200, results)
}

func createRole(c *gin.Context) {
	user := c.MustGet("user").(*User)

	groupTag := c.Param("grouptag")
	if groupTag == "" {
		c.JSON(400, gin.H{"error": "Group tag is required"})
		return
	}

	name := c.Query("name")
	if name == "" {
		c.JSON(400, gin.H{"error": "Name is required"})
		return
	}
	if len(name) > 50 {
		c.JSON(400, gin.H{"error": "Name length exceeded"})
		return
	}

	description := c.Query("description")
	if len(description) > 200 {
		c.JSON(400, gin.H{"error": "Description length exceeded"})
		return
	}

	assignOnJoin := c.DefaultQuery("assign_on_join", "false") == "true"
	selfAssignable := c.DefaultQuery("self_assignable", "false") == "true"

	_, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if !hasPermission(user.GetId(), groupTag, "groups.roles.manage") {
		c.JSON(403, gin.H{"error": "You don't have permission to manage roles"})
		return
	}

	roleId := uuid.New().String()
	role := GroupRole{
		Id:             roleId,
		GroupTag:       groupTag,
		Name:           name,
		Description:    description,
		AssignOnJoin:   assignOnJoin,
		SelfAssignable: selfAssignable,
		Benefits:       []string{},
		Permissions:    []string{},
	}

	roles := getGroupRoles(groupTag)
	roles = append(roles, role)
	updateGroupRoles(groupTag, roles)

	c.JSON(201, role)
}

func getRoles(c *gin.Context) {
	groupTag := c.Param("grouptag")
	if groupTag == "" {
		c.JSON(400, gin.H{"error": "Group tag is required"})
		return
	}

	_, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	roles := getGroupRoles(groupTag)

	var results []GroupRole
	for _, role := range roles {
		if role.GroupTag == groupTag {
			// Owner role always returns all permissions dynamically
			if role.Name == "Owner" {
				ownerRole := role
				ownerRole.Permissions = allGroupPermissions()
				results = append(results, ownerRole)
			} else {
				results = append(results, role)
			}
		}
	}

	c.JSON(200, results)
}

func updateRole(c *gin.Context) {
	user := c.MustGet("user").(*User)

	groupTag := c.Param("grouptag")
	roleId := c.Param("roleid")

	if groupTag == "" || roleId == "" {
		c.JSON(400, gin.H{"error": "Group tag and role ID are required"})
		return
	}

	_, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if !hasPermission(user.GetId(), groupTag, "groups.roles.manage") {
		c.JSON(403, gin.H{"error": "You don't have permission to manage roles"})
		return
	}

	var updateData map[string]any
	if !bindJSON(c, &updateData) {
		return
	}

	roles := getGroupRoles(groupTag)
	found := false

	for i, role := range roles {
		if role.Id == roleId && role.GroupTag == groupTag {
			found = true
			if name, ok := updateData["name"]; ok {
				roles[i].Name = name.(string)
			}
			if description, ok := updateData["description"]; ok {
				roles[i].Description = description.(string)
			}
			if assignOnJoin, ok := updateData["assign_on_join"]; ok {
				roles[i].AssignOnJoin = assignOnJoin.(bool)
			}
			if selfAssignable, ok := updateData["self_assignable"]; ok {
				roles[i].SelfAssignable = selfAssignable.(bool)
			}
			if permissions, ok := updateData["permissions"]; ok {
				parsed, valid := stringsFromJSONValue(permissions)
				if !valid {
					c.JSON(400, gin.H{"error": "Invalid permissions"})
					return
				}
				roles[i].Permissions = parsed
			}
			if benefits, ok := updateData["benefits"]; ok {
				parsed, valid := stringsFromJSONValue(benefits)
				if !valid {
					c.JSON(400, gin.H{"error": "Invalid benefits"})
					return
				}
				roles[i].Benefits = parsed
			}
			break
		}
	}

	if !found {
		c.JSON(404, gin.H{"error": "Role not found"})
		return
	}

	updateGroupRoles(groupTag, roles)

	c.JSON(200, gin.H{"message": "Role updated"})
}

func deleteRole(c *gin.Context) {
	user := c.MustGet("user").(*User)

	groupTag := c.Param("grouptag")
	roleId := c.Param("roleid")

	if groupTag == "" || roleId == "" {
		c.JSON(400, gin.H{"error": "Group tag and role ID are required"})
		return
	}

	_, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if !hasPermission(user.GetId(), groupTag, "groups.roles.manage") {
		c.JSON(403, gin.H{"error": "You don't have permission to manage roles"})
		return
	}

	roles := getGroupRoles(groupTag)
	found := false

	newRoles := make([]GroupRole, 0)
	for _, role := range roles {
		if role.Id == roleId && role.GroupTag == groupTag {
			found = true
			if role.Name == "Owner" || role.Name == "Everyone" {
				c.JSON(400, gin.H{"error": "Cannot delete default roles"})
				return
			}
			continue
		}
		newRoles = append(newRoles, role)
	}

	if !found {
		c.JSON(404, gin.H{"error": "Role not found"})
		return
	}

	updateGroupRoles(groupTag, newRoles)

	c.JSON(200, gin.H{"message": "Role deleted"})
}

// allGroupPermissions returns the canonical list of all group-level permissions.
// The Owner role is always granted every permission in this list dynamically.
func allGroupPermissions() []string {
	return []string{
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
}

// isOwnerRole checks whether any of the given role IDs correspond to the
// Owner role in the specified group.
func isOwnerRole(groupTag string, roleIds []string) bool {
	rolesMap := getGroupRolesMap(groupTag)
	for _, roleId := range roleIds {
		if role, ok := rolesMap[roleId]; ok && role.Name == "Owner" {
			return true
		}
	}
	return false
}

func getUserPermissions(c *gin.Context) {
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
	targetId := userIdFromParam(targetUserId)

	members := getGroupMembers(groupTag)
	var member GroupMember
	found := false

	for _, m := range members {
		if m.UserId == targetId {
			member = m
			found = true
			break
		}
	}

	if !found {
		c.JSON(404, gin.H{"error": "User is not a member of this group"})
		return
	}

	// Owner role always has all permissions dynamically
	if isOwnerRole(groupTag, member.RoleIds) {
		c.JSON(200, gin.H{"permissions": allGroupPermissions()})
		return
	}

	rolesMap := getGroupRolesMap(groupTag)

	permissionsMap := make(map[string]bool)
	for _, roleId := range member.RoleIds {
		role, roleExists := rolesMap[roleId]
		if roleExists {
			for _, perm := range role.Permissions {
				permissionsMap[perm] = true
			}
		}
	}

	permissions := make([]string, 0, len(permissionsMap))
	for perm := range permissionsMap {
		permissions = append(permissions, perm)
	}

	c.JSON(200, gin.H{"permissions": permissions})
}

func getUserRoles(c *gin.Context) {
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
	targetId := userIdFromParam(targetUserId)

	members := getGroupMembers(groupTag)
	var member GroupMember
	found := false

	for _, m := range members {
		if m.UserId == targetId {
			member = m
			found = true
			break
		}
	}

	if !found {
		c.JSON(404, gin.H{"error": "User is not a member of this group"})
		return
	}

	rolesMap := getGroupRolesMap(groupTag)

	var roles []GroupRole
	for _, roleId := range member.RoleIds {
		if role, roleExists := rolesMap[roleId]; roleExists {
			// Owner role always returns all permissions dynamically
			if role.Name == "Owner" {
				ownerRole := role
				ownerRole.Permissions = allGroupPermissions()
				roles = append(roles, ownerRole)
			} else {
				roles = append(roles, role)
			}
		}
	}

	c.JSON(200, gin.H{"roles": roles})
}

func getUserBenefits(c *gin.Context) {
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
	targetId := userIdFromParam(targetUserId)

	members := getGroupMembers(groupTag)
	var member GroupMember
	found := false

	for _, m := range members {
		if m.UserId == targetId {
			member = m
			found = true
			break
		}
	}

	if !found {
		c.JSON(404, gin.H{"error": "User is not a member of this group"})
		return
	}

	rolesMap := getGroupRolesMap(groupTag)

	benefitsMap := make(map[string]bool)
	for _, roleId := range member.RoleIds {
		role, roleExists := rolesMap[roleId]
		if roleExists {
			for _, benefit := range role.Benefits {
				benefitsMap[benefit] = true
			}
		}
	}

	benefits := make([]string, 0, len(benefitsMap))
	for benefit := range benefitsMap {
		benefits = append(benefits, benefit)
	}

	c.JSON(200, gin.H{"benefits": benefits})
}

func toggleAnnouncementMute(c *gin.Context) {
	user := c.MustGet("user").(*User)

	groupTag := c.Param("grouptag")
	if groupTag == "" {
		c.JSON(400, gin.H{"error": "Group tag is required"})
		return
	}

	_, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	members := getGroupMembers(groupTag)
	found := false

	for i, member := range members {
		if member.UserId == user.GetId() {
			found = true
			members[i].MutedAnnouncements = !members[i].MutedAnnouncements
			break
		}
	}

	if !found {
		c.JSON(400, gin.H{"error": "You are not a member of this group"})
		return
	}

	updateGroupMembers(groupTag, members)
	c.JSON(200, gin.H{"message": "Mute status updated"})
}

func assignRole(c *gin.Context) {
	user := c.MustGet("user").(*User)

	groupTag := c.Param("grouptag")
	targetUserId := c.Param("userid")
	roleId := c.Param("roleid")

	if groupTag == "" || targetUserId == "" || roleId == "" {
		c.JSON(400, gin.H{"error": "Group tag, user ID, and role ID are required"})
		return
	}

	_, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	rolesMap := getGroupRolesMap(groupTag)
	role, roleExists := rolesMap[roleId]
	targetId := userIdFromParam(targetUserId)

	if !roleExists || role.GroupTag != groupTag {
		c.JSON(404, gin.H{"error": "Role not found"})
		return
	}

	if role.SelfAssignable && user.GetId() == targetId {
	} else if !hasPermission(user.GetId(), groupTag, "groups.roles.assign") {
		c.JSON(403, gin.H{"error": "You don't have permission to assign roles"})
		return
	}

	members := getGroupMembers(groupTag)
	found := false

	for i, member := range members {
		if member.UserId == targetId {
			found = true
			for _, rId := range member.RoleIds {
				if rId == roleId {
					c.JSON(400, gin.H{"error": "User already has this role"})
					return
				}
			}
			members[i].RoleIds = append(members[i].RoleIds, roleId)
			break
		}
	}

	if !found {
		c.JSON(404, gin.H{"error": "User is not a member of this group"})
		return
	}

	updateGroupMembers(groupTag, members)

	c.JSON(200, gin.H{"message": "Role assigned"})
}

func removeRole(c *gin.Context) {
	user := c.MustGet("user").(*User)

	groupTag := c.Param("grouptag")
	targetUserId := c.Param("userid")
	roleId := c.Param("roleid")

	if groupTag == "" || targetUserId == "" || roleId == "" {
		c.JSON(400, gin.H{"error": "Group tag, user ID, and role ID are required"})
		return
	}

	_, ok := getGroupByTag(groupTag)
	if !ok {
		c.JSON(404, gin.H{"error": "Group not found"})
		return
	}

	if !hasPermission(user.GetId(), groupTag, "groups.roles.assign") {
		c.JSON(403, gin.H{"error": "You don't have permission to remove roles"})
		return
	}

	rolesMap := getGroupRolesMap(groupTag)
	role, roleExists := rolesMap[roleId]
	targetId := userIdFromParam(targetUserId)

	if !roleExists || role.GroupTag != groupTag {
		c.JSON(404, gin.H{"error": "Role not found"})
		return
	}

	if role.Name == "Owner" {
		c.JSON(400, gin.H{"error": "Cannot remove Owner role"})
		return
	}

	members := getGroupMembers(groupTag)
	found := false

	for i, member := range members {
		if member.UserId == targetId {
			found = true
			newRoleIds := make([]string, 0)
			for _, rId := range member.RoleIds {
				if rId != roleId {
					newRoleIds = append(newRoleIds, rId)
				}
			}
			if len(newRoleIds) == len(member.RoleIds) {
				c.JSON(400, gin.H{"error": "User doesn't have this role"})
				return
			}
			members[i].RoleIds = newRoleIds
			break
		}
	}

	if !found {
		c.JSON(404, gin.H{"error": "User is not a member of this group"})
		return
	}

	updateGroupMembers(groupTag, members)

	c.JSON(200, gin.H{"message": "Role removed"})
}
