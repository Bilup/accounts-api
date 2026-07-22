package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"claw/internal/model"
)

type subscription struct {
	Active       bool   `json:"active"`
	Tier         string `json:"tier"`
	Next_billing int64  `json:"next_billing"`
}

type sub_benefits struct {
	Max_Keys                int  `json:"max_keys"`
	Max_Login_History       int  `json:"max_login_history"`
	Max_Transaction_History int  `json:"max_transaction_history"`
	Max_Rmails              int  `json:"max_rmails"`
	FileSystem_Size         int  `json:"file_system_size"`
	Bio_Length              int  `json:"bio_length"`
	Has_Animated_Pfp        bool `json:"animated_pfp"`
	Has_Animated_Banner     bool `json:"animated_banner"`
	Has_Free_Banner_Uploads bool `json:"free_banner_uploads"`
	Has_Bio_templating      bool `json:"bio_templating"`
	Has_Profile_notes       bool `json:"profile_notes"`
	Daily_Credit_Multipler  int  `json:"daily_credit_multiplier"`
}

type Username = model.Username

type UserId = model.UserId

type Timestamp = model.Timestamp

func userIdFromParam(param string) UserId {
	if user, err := getAccountByUsername(param); err == nil {
		return user.GetId()
	}
	return UserId(param)
}

type GroupId string

type JoinPolicy string

const (
	JoinPolicyOpen    JoinPolicy = "OPEN"
	JoinPolicyRequest JoinPolicy = "REQUEST"
	JoinPolicyInvite  JoinPolicy = "INVITE"
)

type InviteStatus string

const (
	InvitePending  InviteStatus = "PENDING"
	InviteAccepted InviteStatus = "ACCEPTED"
	InviteDeclined InviteStatus = "DECLINED"
	InviteRevoked  InviteStatus = "REVOKED"
)

type RequestStatus string

const (
	RequestPending  RequestStatus = "PENDING"
	RequestAccepted RequestStatus = "ACCEPTED"
	RequestDeclined RequestStatus = "DECLINED"
)

type Group struct {
	Id             GroupId    `json:"id"`
	Tag            string     `json:"tag"`
	Name           string     `json:"name"`
	Description    string     `json:"description"`
	Readme         string     `json:"readme"`
	Rules          string     `json:"rules"`
	IconUrl        string     `json:"icon_url"`
	BannerUrl      string     `json:"banner_url"`
	OwnerUserId    UserId     `json:"owner_user_id"`
	Public         bool       `json:"public"`
	JoinPolicy     JoinPolicy `json:"join_policy"`
	EntryFee       float64    `json:"entry_fee"`
	CreatedAt      int64      `json:"created_at"`
	CreditsBalance float64    `json:"credits_balance"`
}

func getGroupDataByTag(tag string) (*GroupData, bool) {
	groupsDataMutex.RLock()
	data, ok := groupsData[tag]
	groupsDataMutex.RUnlock()
	if !ok {
		return nil, false
	}
	return data, true
}

func getGroupByTag(tag string) (*Group, bool) {
	groupFile, exists := getGroupDataByTag(tag)
	if !exists {
		return &Group{}, false
	}
	return &groupFile.Group, true
}

func getGroupMembers(groupTag string) []GroupMember {
	data, exists := getGroupDataByTag(groupTag)
	if !exists {
		return []GroupMember{}
	}
	return data.Members
}

func updateGroupMembers(groupTag string, members []GroupMember) {
	mutateGroupData(groupTag, func(data *GroupData) {
		data.Members = members
	})
}

// mutateGroupData runs fn on the group's data under the write lock and
// schedules a save. It is a no-op if the group does not exist.
func mutateGroupData(groupTag string, fn func(*GroupData)) bool {
	groupsDataMutex.Lock()
	data, ok := groupsData[groupTag]
	if !ok {
		groupsDataMutex.Unlock()
		return false
	}
	fn(data)
	groupsDataMutex.Unlock()
	go saveGroupData(groupTag)
	return true
}

func getGroupRoles(groupTag string) []GroupRole {
	data, exists := getGroupDataByTag(groupTag)
	if !exists {
		return []GroupRole{}
	}
	return data.Roles
}

func groupJoinRoleIds(roles []GroupRole) []string {
	roleIds := make([]string, 0)
	seen := make(map[string]bool)
	for _, role := range roles {
		if role.AssignOnJoin && role.Id != "" && !seen[role.Id] {
			roleIds = append(roleIds, role.Id)
			seen[role.Id] = true
		}
	}
	if len(roleIds) > 0 {
		return roleIds
	}
	for _, role := range roles {
		if strings.EqualFold(role.Name, "Member") && role.Id != "" {
			return []string{role.Id}
		}
	}
	return []string{}
}

func updateGroupRoles(groupTag string, roles []GroupRole) {
	mutateGroupData(groupTag, func(data *GroupData) {
		data.Roles = roles
	})
}

func getGroupAnnouncements(groupTag string) []GroupAnnouncement {
	data, exists := getGroupDataByTag(groupTag)
	if !exists {
		return []GroupAnnouncement{}
	}
	return data.Announcements
}

func addGroupAnnouncement(groupTag string, announcement GroupAnnouncement) {
	mutateGroupData(groupTag, func(data *GroupData) {
		data.Announcements = append(data.Announcements, announcement)
	})
}

func getGroupEvents(groupTag string) []GroupEvent {
	groupsDataMutex.RLock()
	defer groupsDataMutex.RUnlock()

	data, exists := groupsData[groupTag]
	if !exists {
		return nil
	}

	events := make([]GroupEvent, 0, len(data.Events))
	for _, event := range data.Events {
		events = append(events, event)
	}
	return events
}

func addGroupEvent(groupTag string, event GroupEvent) {
	mutateGroupData(groupTag, func(data *GroupData) {
		if data.Events == nil {
			data.Events = make(map[string]GroupEvent)
		}
		data.Events[event.Id] = event
	})
}

func getGroupTips(groupTag string) []GroupTip {
	return loadGroupTips(groupTag)
}

func addGroupTip(groupTag string, tip GroupTip) {
	tips := loadGroupTips(groupTag)
	tips = append(tips, tip)
	mutateGroupData(groupTag, func(data *GroupData) {
		data.Group.CreditsBalance += tip.AmountCredits
	})
	go saveGroupTips(groupTag, tips)
}

func getGroupRolesMap(groupTag string) map[string]GroupRole {
	groupsDataMutex.RLock()
	defer groupsDataMutex.RUnlock()

	data, exists := groupsData[groupTag]
	if !exists {
		return nil
	}

	rolesMap := make(map[string]GroupRole)
	for _, role := range data.Roles {
		rolesMap[role.Id] = role
	}
	return rolesMap
}

type GroupPublic = GroupNet

type GroupNet struct {
	Id             GroupId    `json:"id"`
	Tag            string     `json:"tag"`
	Name           string     `json:"name"`
	Description    string     `json:"description"`
	Readme         string     `json:"readme"`
	Rules          string     `json:"rules"`
	IconUrl        string     `json:"icon_url"`
	BannerUrl      string     `json:"banner_url"`
	OwnerUserId    Username   `json:"owner_user_id"`
	Public         bool       `json:"public"`
	JoinPolicy     JoinPolicy `json:"join_policy"`
	EntryFee       float64    `json:"entry_fee"`
	CreatedAt      int64      `json:"created_at"`
	CreditsBalance float64    `json:"credits_balance"`
	MemberCount    int        `json:"member_count"`
}

func (g Group) ToNet() GroupNet {
	return GroupNet{
		Id:             g.Id,
		Tag:            g.Tag,
		Name:           g.Name,
		Description:    g.Description,
		Readme:         g.Readme,
		Rules:          g.Rules,
		IconUrl:        g.IconUrl,
		BannerUrl:      g.BannerUrl,
		OwnerUserId:    getUserById(g.OwnerUserId).GetUsername(),
		Public:         g.Public,
		JoinPolicy:     g.JoinPolicy,
		EntryFee:       g.EntryFee,
		CreatedAt:      g.CreatedAt,
		CreditsBalance: g.CreditsBalance,
		MemberCount:    0,
	}
}

func (g *Group) ToPublic() GroupPublic {
	return g.ToNet()
}

type GroupMember struct {
	Id                 string   `json:"id"`
	GroupTag           string   `json:"group_tag"`
	UserId             UserId   `json:"user_id"`
	RoleIds            []string `json:"role_ids"`
	JoinedAt           int64    `json:"joined_at"`
	MutedAnnouncements bool     `json:"muted_announcements"`
}

type GroupRole struct {
	Id             string   `json:"id"`
	GroupTag       string   `json:"group_tag"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	AssignOnJoin   bool     `json:"assign_on_join"`
	SelfAssignable bool     `json:"self_assignable"`
	Benefits       []string `json:"benefits"`
	Permissions    []string `json:"permissions"`
}

type GroupAnnouncement struct {
	Id           string `json:"id"`
	GroupTag     string `json:"group_tag"`
	Title        string `json:"title"`
	Body         string `json:"body"`
	AuthorUserId UserId `json:"author_user_id"`
	CreatedAt    int64  `json:"created_at"`
	PingMembers  bool   `json:"ping_members"`
}

type EventVisibility string

const (
	EventVisibilityMembers EventVisibility = "MEMBERS"
	EventVisibilityPublic  EventVisibility = "PUBLIC"
)

type GroupEvent struct {
	Id          string          `json:"id"`
	GroupTag    string          `json:"group_tag"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	StartTime   int64           `json:"start_time"`
	EndTime     int64           `json:"end_time"`
	Location    string          `json:"location"`
	Visibility  EventVisibility `json:"visibility"`
	CreatedBy   UserId          `json:"created_by"`
	Published   bool            `json:"published"`
}

type GroupTip struct {
	Id            string  `json:"id"`
	GroupTag      string  `json:"group_tag"`
	FromUserId    UserId  `json:"from_user_id"`
	AmountCredits float64 `json:"amount_credits"`
	Note          string  `json:"note"`
	CreatedAt     int64   `json:"created_at"`
}

type GroupTipWithdrawal struct {
	Id            string  `json:"id"`
	GroupTag      string  `json:"group_tag"`
	ToUserId      UserId  `json:"to_user_id"`
	AmountCredits float64 `json:"amount_credits"`
	CreatedAt     int64   `json:"created_at"`
}

type GroupBenefitProduct struct {
	Id             string  `json:"id"`
	GroupTag       string  `json:"group_tag"`
	Name           string  `json:"name"`
	Description    string  `json:"description"`
	PriceCredits   float64 `json:"price_credits"`
	RoleGrantedId  string  `json:"role_granted_id,omitempty"`
	BenefitGranted string  `json:"benefit_granted,omitempty"`
	Subscription   bool    `json:"subscription"`
	Frequency      int     `json:"frequency,omitempty"`
	Period         string  `json:"period,omitempty"`
}

type GroupProductSubscription struct {
	Id          string `json:"id"`
	GroupTag    string `json:"group_tag"`
	ProductId   string `json:"product_id"`
	UserId      UserId `json:"user_id"`
	RoleId      string `json:"role_id"`
	StartedAt   int64  `json:"started_at"`
	NextBilling int64  `json:"next_billing"`
	CancelAt    *int64 `json:"cancel_at,omitempty"`
	Active      bool   `json:"active"`
}

type GroupInvite struct {
	Id         string       `json:"id"`
	GroupTag   string       `json:"group_tag"`
	FromUserId UserId       `json:"from_user_id"`
	ToUserId   UserId       `json:"to_user_id"`
	Status     InviteStatus `json:"status"`
	CreatedAt  int64        `json:"created_at"`
}

type GroupJoinRequest struct {
	Id        string        `json:"id"`
	GroupTag  string        `json:"group_tag"`
	UserId    UserId        `json:"user_id"`
	Message   string        `json:"message"`
	Status    RequestStatus `json:"status"`
	CreatedAt int64         `json:"created_at"`
}

type GroupBan struct {
	Id        string `json:"id"`
	GroupTag  string `json:"group_tag"`
	UserId    UserId `json:"user_id"`
	BannedBy  UserId `json:"banned_by"`
	Reason    string `json:"reason"`
	CreatedAt int64  `json:"created_at"`
}

type GroupData struct {
	Group                Group                               `json:"group"`
	Members              []GroupMember                       `json:"members"`
	Roles                []GroupRole                         `json:"roles"`
	Announcements        []GroupAnnouncement                 `json:"announcements"`
	Events               map[string]GroupEvent               `json:"events"`
	Tips                 []GroupTip                          `json:"tips"`
	BenefitProducts      map[string]GroupBenefitProduct      `json:"benefit_products"`
	ProductSubscriptions map[string]GroupProductSubscription `json:"product_subscriptions"`
	Invites              []GroupInvite                       `json:"invites"`
	JoinRequests         []GroupJoinRequest                  `json:"join_requests"`
	Bans                 []GroupBan                          `json:"bans"`
}

var userMutexesLock sync.Mutex
var userPtrMutexes = map[uintptr]*sync.RWMutex{}

func getMutexForUser(u User) *sync.RWMutex {
	ptr := reflect.ValueOf(u).Pointer()
	userMutexesLock.Lock()
	defer userMutexesLock.Unlock()
	mu, ok := userPtrMutexes[ptr]
	if !ok {
		mu = &sync.RWMutex{}
		userPtrMutexes[ptr] = mu
	}
	return mu
}

func dropMutexForUser(u User) {
	ptr := reflect.ValueOf(u).Pointer()
	userMutexesLock.Lock()
	delete(userPtrMutexes, ptr)
	userMutexesLock.Unlock()
}

var subs_benefits = map[string]sub_benefits{
	"free":  tierFree(),
	"lite":  tierLite(),
	"plus":  tierPlus(),
	"drive": tierPro(), // drive is just an alias for pro now
	"pro":   tierPro(),
	"max":   tierMax(),
}

func normalizeSubscriptionTier(tier string) string {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "lite":
		return "Lite"
	case "plus":
		return "Plus"
	case "drive", "pro":
		return "Pro"
	case "max":
		return "Max"
	default:
		return "Free"
	}
}

func tierFree() sub_benefits {
	benefits := sub_benefits{
		Max_Keys:                5,
		Max_Login_History:       10,
		Max_Rmails:              100,
		FileSystem_Size:         5_000_000,
		Bio_Length:              200,
		Max_Transaction_History: 20,
		Daily_Credit_Multipler:  1,
	}
	return benefits
}

func tierLite() sub_benefits {
	b := tierFree()
	b.FileSystem_Size = 10_000_000
	b.Has_Bio_templating = true
	return b
}

func tierPlus() sub_benefits {
	b := tierLite()
	b.FileSystem_Size = 15_000_000
	b.Has_Profile_notes = true
	b.Max_Keys = 20
	b.Max_Login_History = 100
	b.Max_Rmails = 1000
	b.Bio_Length = 500
	b.Has_Animated_Pfp = true
	b.Max_Transaction_History = 100
	b.Daily_Credit_Multipler = 2
	return b
}

func tierPro() sub_benefits {
	b := tierPlus()
	b.Max_Keys = 50
	b.Max_Rmails = 100_000
	b.FileSystem_Size = 1_000_000_000
	b.Bio_Length = 1000
	b.Has_Animated_Banner = true
	b.Has_Free_Banner_Uploads = true
	b.Max_Transaction_History = 500
	b.Daily_Credit_Multipler = 3
	return b
}

func tierMax() sub_benefits {
	b := tierPro()
	b.Max_Keys = 500
	b.FileSystem_Size = 10_000_000_000
	return b
}

// User represents a user with dynamic fields
type User map[string]any

// Helper methods for User
func (u User) GetUsername() Username {
	mu := getMutexForUser(u)
	mu.RLock()
	defer mu.RUnlock()
	if v, ok := u["username"]; ok {
		switch s := v.(type) {
		case string:
			return Username(s)
		case Username:
			return s
		}
	}
	return ""
}

func (u User) GetTheme() map[string]any {
	mu := getMutexForUser(u)
	mu.RLock()
	defer mu.RUnlock()
	if theme, ok := u["theme"]; ok {
		if m, ok := theme.(map[string]any); ok {
			return m
		}
	}
	return map[string]any{}
}

func (u User) GetId() UserId {
	mu := getMutexForUser(u)
	mu.RLock()
	defer mu.RUnlock()
	if id, ok := u["sys.id"]; ok {
		if str, ok := id.(string); ok {
			return UserId(str)
		}
		return ""
	}
	return ""
}

func (u User) GetKey() string {
	mu := getMutexForUser(u)
	mu.RLock()
	defer mu.RUnlock()
	if key, ok := u["key"]; ok {
		if str, ok := key.(string); ok {
			return str
		}
	}
	return ""
}

func (u User) GetPassword() string {
	mu := getMutexForUser(u)
	mu.RLock()
	defer mu.RUnlock()
	if password, ok := u["password"]; ok {
		if str, ok := password.(string); ok {
			return str
		}
	}
	return ""
}

func (u User) GetPassVersion() int {
	return getIntOrDefault(u.Get("sys.passv"), 0)
}

func parseJSONTimestamp(v any) (int64, error) {
	switch t := v.(type) {
	case string:
		return strconv.ParseInt(t, 10, 64)
	case float64:
		return int64(t), nil
	case int64:
		return int64(t), nil
	}
	return 0, nil
}

func (u User) GetSystem() string {
	return getStringOrDefault(u.Get("system"), "rotur")
}

func (u User) GetEmail() string {
	return u.GetString("email")
}

func (u User) SetBlocked(blocked []UserId) {
	strs := make([]string, len(blocked))
	for i, b := range blocked {
		strs[i] = string(b)
	}
	u.Set("sys.blocked", strs)
}

func (u User) GetBlocked() []UserId {
	blocked := getStringSlice(u, "sys.blocked")
	out := make([]UserId, len(blocked))
	for i, b := range blocked {
		out[i] = UserId(b)
	}
	return out
}

func resolveUsernames(ids []UserId) []Username {
	out := make([]Username, 0, len(ids))
	for _, id := range ids {
		if user := getUserById(id); user != nil {
			if username := user.GetUsername(); username != "" {
				out = append(out, username)
			}
		}
	}
	return out
}

func (u User) GetBlockedUsers() []Username {
	return resolveUsernames(u.GetBlocked())
}

func (u User) AddBlocked(userId UserId) {
	if u.HasBlocked(userId) {
		return
	}
	blocked := u.GetBlocked()
	blocked = append(blocked, userId)
	u.SetBlocked(blocked)
}

func (u User) RemoveBlocked(userId UserId) {
	if !u.HasBlocked(userId) {
		return
	}
	blocked := u.GetBlocked()
	newBlocked := make([]UserId, 0, len(blocked)-1)
	for _, b := range blocked {
		if b != userId {
			newBlocked = append(newBlocked, b)
		}
	}
	u.SetBlocked(newBlocked)
}

func (u User) HasBlocked(userId UserId) bool {
	blocked := u.GetBlocked()
	for _, b := range blocked {
		if b == userId {
			return true
		}
	}
	return false
}

func (u User) IsBanned() bool {
	banned := u.Get("sys.banned")
	return banned == true || banned == "true"
}

func (u User) IsNetworkAdmin() bool {
	return isHardcodedAdmin(u.GetUsername())
}

func (u User) IsModerator() bool {
	if u.IsNetworkAdmin() {
		return true
	}
	mod := u.Get("sys.moderator")
	return mod == true || mod == "true"
}

func (u User) SetModerator(isMod bool) {
	u.Set("sys.moderator", isMod)
}

func (u User) IsPrivate() bool {
	private := u.Get("private")
	return private == true
}

func (u User) SetFriends(friends []UserId) {
	strs := make([]string, len(friends))
	for i, f := range friends {
		strs[i] = string(f)
	}
	u.Set("sys.friends", strs)
}

func (u User) SetRequests(requests []UserId) {
	strs := make([]string, len(requests))
	for i, r := range requests {
		strs[i] = string(r)
	}
	u.Set("sys.requests", strs)
}

func (u User) AddRequest(username Username) bool {
	if u.HasRequest(username) {
		return false
	}
	requests := u.GetRequests()
	userId := getIdByUsername(username)
	requests = append(requests, userId)
	u.SetRequests(requests)
	return true
}

func (u User) RemoveRequest(username Username) bool {
	if !u.HasRequest(username) {
		return false
	}
	requests := u.GetRequests()
	userId := getIdByUsername(username)
	requestIds := make([]UserId, 0, len(requests)-1)
	for _, r := range requests {
		if r != userId {
			requestIds = append(requestIds, r)
		}
	}
	u.SetRequests(requestIds)
	return true
}

func (u User) HasRequest(username Username) bool {
	userId := getIdByUsername(username)
	if userId == "" {
		return false
	}
	for _, r := range u.GetRequests() {
		if r == userId {
			return true
		}
	}
	return false
}

func (u User) AddFriend(username Username) bool {
	friends := u.GetFriends()
	if u.IsFriend(username) {
		return false
	}
	userId := getIdByUsername(username)
	friends = append(friends, userId)
	u.SetFriends(friends)

	return true
}

func (u User) RemoveFriend(username Username) bool {
	friends := u.GetFriends()
	if !u.IsFriend(username) {
		return false
	}
	userId := getIdByUsername(username)
	newFriends := make([]UserId, 0, len(friends)-1)
	for _, f := range friends {
		if f != userId {
			newFriends = append(newFriends, f)
		}
	}
	u.SetFriends(newFriends)

	return true
}

func (u User) IsFriend(username Username) bool {
	userId := getIdByUsername(username)
	if userId == "" {
		return false
	}
	for _, f := range u.GetFriends() {
		if f == userId {
			return true
		}
	}
	return false
}

func getIdByUsername(username Username) UserId {
	idToUserMutex.RLock()
	defer idToUserMutex.RUnlock()
	val, ok := usernameToId[username.ToLower()]
	if ok {
		return val
	}
	return UserId("")
}

func getUserById(id UserId) User {
	idToUserMutex.RLock()
	defer idToUserMutex.RUnlock()
	return idToUser[id]
}

func (u User) GetFriends() []UserId {
	friends := getStringSlice(u, "sys.friends")
	out := make([]UserId, len(friends))
	for i, f := range friends {
		out[i] = UserId(f)
	}
	return out
}

func (u User) GetFriendUsers() []Username {
	return resolveUsernames(u.GetFriends())
}

func (u User) GetRequests() []UserId {
	requests := getStringSlice(u, "sys.requests")
	out := make([]UserId, len(requests))
	for i, r := range requests {
		out[i] = UserId(r)
	}
	return out
}

func (u User) GetOutgoingRequests() []Username {
	myId := u.GetId()
	myName := u.GetUsername().ToLower()
	usersMutex.RLock()
	defer usersMutex.RUnlock()
	out := make([]Username, 0)
	for _, other := range users {
		if other.GetId() == myId {
			continue
		}
		if other.HasRequest(myName) {
			out = append(out, other.GetUsername())
		}
	}
	return out
}

func (u User) GetRequestedUsers() []Username {
	return resolveUsernames(u.GetRequests())
}

func (u User) GetCreated() int64 {
	return getInt64OrDefault(u.Get("created"), 0)
}

func (u User) GetIndex() int64 {
	return getInt64OrDefault(u.Get("sys.index"), 0)
}

func (u User) GetNotes() map[UserId]string {
	notes := u.Get("sys.notes")
	out := make(map[UserId]string)
	switch m := notes.(type) {
	case map[UserId]string:
		maps.Copy(out, m)
	case map[UserId]any:
		for k, v := range m {
			if s, ok := v.(string); ok {
				out[k] = s
			}
		}
	case map[string]string:
		for k, v := range m {
			out[UserId(k)] = v
		}
	case map[string]any:
		for k, v := range m {
			if s, ok := v.(string); ok {
				out[UserId(k)] = s
			}
		}
	}
	return out
}

func (u User) SetNote(username Username, note string) error {
	if len(note) > 300 {
		return fmt.Errorf("note content is too long")
	}
	notes := u.GetNotes()
	userId := getIdByUsername(username)
	notes[userId] = note
	u.Set("sys.notes", notes)
	return nil
}

func (u User) RemoveNote(username Username) {
	notes := u.GetNotes()
	userId := getIdByUsername(username)
	delete(notes, userId)
	u.Set("sys.notes", notes)
}

func (u User) GetNotesNet() map[Username]string {
	notes := u.GetNotes()
	out := make(map[Username]string, len(notes))
	for id, note := range notes {
		user := getUserById(id)
		if user == nil {
			continue
		}
		if username := user.GetUsername(); username != "" {
			out[username] = note
		}
	}
	return out
}

func (u User) GetCredits() float64 {
	mu := getMutexForUser(u)
	mu.RLock()
	defer mu.RUnlock()
	if credits, ok := u["sys.currency"]; ok {
		switch v := credits.(type) {
		case float64:
			return v
		case int64:
			return float64(v)
		case int:
			return float64(v)
		case string:
			if floatValue, err := strconv.ParseFloat(v, 64); err == nil {
				return floatValue
			}
		}
	}
	return 0
}

func (u User) SetBalance(balance any) {
	var fval float64
	switch v := balance.(type) {
	case float64:
		fval = v
	case float32:
		fval = float64(v)
	case int:
		fval = float64(v)
	case int64:
		fval = float64(v)
	case string:
		if parsed, err := strconv.ParseFloat(v, 64); err == nil {
			fval = parsed
		} else {
			return
		}
	default:
		return
	}
	u.Set("sys.currency", roundVal(fval))
}

func (u User) GetLogins() []Login {
	raw := u.Get("sys.logins")
	if raw == nil {
		return nil
	}

	switch v := raw.(type) {
	case []Login:
		return v
	case []any:
		out := make([]Login, 0, len(v))
		for _, item := range v {
			switch l := item.(type) {
			case Login:
				out = append(out, l)
			case map[string]any:
				var login Login
				if b, err := json.Marshal(l); err == nil {
					_ = json.Unmarshal(b, &login)
					out = append(out, login)
				}
			}
		}
		return out
	default:
		return nil
	}
}

func (u User) GetSubscription() subscription {
	username := u.GetUsername()
	if strings.EqualFold(string(username), "mist") {
		// keep me as the sigma
		return subscription{
			Active:       true,
			Tier:         "Max",
			Next_billing: time.Now().UnixMilli() + (24 * 60 * 60 * 1000),
		}
	}
	usub := u.Get("sys.subscription")
	val := subscription{
		Active:       false,
		Tier:         "Free",
		Next_billing: 0,
	}
	checkExternalBilling := func() (ok bool) {
		next := getKeyNextBilling(u.GetId(), LITE_SUBSCRIPTION_KEY)
		if next == 0 {
			return false
		}
		val.Active = true
		val.Tier = "Lite"
		val.Next_billing = next
		return true
	}
	if usub == nil {
		_ = checkExternalBilling()
		return val
	}
	sub, ok := usub.(map[string]any)
	if !ok {
		_ = checkExternalBilling()
		return val
	}
	val.Active = sub["active"] == true
	val.Tier = normalizeSubscriptionTier(getStringOrDefault(sub["tier"], "Free"))
	val.Next_billing = int64(getIntOrDefault(sub["next_billing"], 0))

	if val.Next_billing == 0 {
		if checkExternalBilling() {
			return val
		}
		val.Active = false
		val.Tier = "Free"
		return val
	}

	if val.Next_billing < time.Now().UnixMilli() && val.Active {
		if checkExternalBilling() {
			return val
		}
		go sendDiscordWebhook([]map[string]any{
			{
				"title": "Lost Subscription",
				"description": fmt.Sprintf("**User:** %s\n**Tier:** %s\n**Next Billing:** %s",
					username, val.Tier, time.Unix(val.Next_billing/1000, 0).Format(time.RFC3339)),
				"color":     0x57cdac,
				"timestamp": time.Now().Format(time.RFC3339),
			},
		})
		val.Active = false
		val.Next_billing = 0
		val.Tier = "Free"
		u.SetSubscription(val)
		go saveUsers()
	}
	return val
}

func (u User) GetSubscriptionBenefits() sub_benefits {
	tier := strings.ToLower(normalizeSubscriptionTier(u.GetSubscription().Tier))
	benefits, ok := subs_benefits[tier]
	if !ok {
		return tierFree()
	}
	return benefits
}

func (u User) GetBlockedIps() []string {
	return getStringSlice(u, "blocked_ips")
}

// social links to display on the user's profile (max 3)
func (u User) GetSocialLinks() []string {
	return getStringSlice(u, "sys.social_links")
}

func (u User) SetSocialLinks(links []string) {
	u.Set("sys.social_links", links)
}

func (u User) SetSubscription(sub subscription) {
	sub.Tier = normalizeSubscriptionTier(sub.Tier)
	u.Set("sys.subscription", map[string]any{
		"active":       sub.Active,
		"tier":         sub.Tier,
		"next_billing": sub.Next_billing,
	})
	InvalidateUserTierCache(u.GetUsername())
	if u.Get("max_size") != u.GetMaxSize() {
		u.Set("max_size", u.GetMaxSize())
	}
}

func (u User) GetMaxSize() string {
	amt := strconv.Itoa(u.GetSubscriptionBenefits().FileSystem_Size)
	if u.Get("max_size") != amt {
		u.Set("max_size", amt)
	}
	return amt
}

func (u User) GetTransactions() []Transaction {
	raw := u.Get("sys.transactions")
	if txs, ok := raw.([]Transaction); ok {
		return txs
	}
	return reJSON(raw, []Transaction(nil))
}

func (u User) addTransaction(tx Transaction) {
	txs := u.GetTransactions()
	benefits := u.GetSubscriptionBenefits()

	tx.Timestamp = time.Now().UnixMilli()

	noteStr := strings.TrimSpace(tx.Note)
	if len(noteStr) > 50 {
		noteStr = noteStr[:50]
	}
	tx.Note = noteStr

	txs = append([]Transaction{tx}, txs...)
	if len(txs) > benefits.Max_Transaction_History {
		txs = txs[:benefits.Max_Transaction_History]
	}
	u.Set("sys.transactions", txs)
}

func (u User) applyTransaction(newBalance float64, tx Transaction) {
	u.SetBalance(newBalance)
	tx.NewTotal = newBalance
	u.addTransaction(tx)
}

func (u User) SetLogins(logins []Login) {
	u.Set("sys.logins", logins)
}

func (u User) Has(key string) bool {
	mu := getMutexForUser(u)
	mu.RLock()
	defer mu.RUnlock()
	_, ok := u[key]
	return ok
}

func (u User) Get(key string) any {
	mu := getMutexForUser(u)
	mu.RLock()
	defer mu.RUnlock()
	value, ok := u[key]
	if ok {
		return value
	}
	return nil
}

func (u User) GetString(key string) string {
	mu := getMutexForUser(u)
	mu.RLock()
	defer mu.RUnlock()
	value, ok := u[key]
	if ok {
		switch v := value.(type) {
		case string:
			return v
		case int:
			return strconv.Itoa(v)
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		}
	}
	return ""
}

func (u User) GetInt(key string) int {
	mu := getMutexForUser(u)
	mu.RLock()
	defer mu.RUnlock()
	value, ok := u[key]
	if ok {
		switch v := value.(type) {
		case int:
			return v
		case float64:
			return int(v)
		case int64:
			return int(v)
		case string:
			if intValue, err := strconv.Atoi(v); err == nil {
				return intValue
			}
		}
	}
	return 0
}

func (u User) DelKey(key string) error {
	mu := getMutexForUser(u)
	mu.Lock()
	var username Username
	if v, ok := u["username"]; ok {
		if str, ok := v.(string); ok {
			username = Username(str)
		}
	}
	delete(u, key)
	mu.Unlock()
	if id := u.GetId(); id != "" {
		saveUser(id)
	}
	go notify("sys.delete", map[string]any{
		"username": username,
		"key":      key,
	})
	return nil
}

func (u User) Set(key string, value any) {
	mu := getMutexForUser(u)
	mu.Lock()
	oldValue := u[key]
	u[key] = value

	var uid UserId
	if v, ok := u["sys.id"]; ok && v != nil {
		if str, ok := v.(string); ok && str != "" {
			uid = UserId(str)
		}
	}

	mu.Unlock()

	maintainUserIndexOnSet(uid, key, oldValue, value)

	if key != "key" && key != "password" {
		if uid != "" {
			go OnUserUpdate(uid, key, value)
		}
	}

	if uid != "" {
		saveUser(uid)
	}
}

func (u User) SetTransient(key string, value any) {
	mu := getMutexForUser(u)
	mu.Lock()
	u[key] = value

	var uid UserId
	if v, ok := u["sys.id"]; ok && v != nil {
		if str, ok := v.(string); ok && str != "" {
			uid = UserId(str)
		}
	}
	mu.Unlock()

	if uid != "" {
		go OnUserUpdate(uid, key, value)
	}
}

// FollowerData represents follower information
type FollowerData struct {
	Followers []UserId `json:"followers"`
	Username  Username `json:"username"`
	UserId    UserId   `json:"user_id"`
}

// Post represents a social media post
type Post struct {
	ID             string   `json:"id"`
	Content        string   `json:"content"`
	User           UserId   `json:"user"`
	Timestamp      int64    `json:"timestamp"`
	Attachment     *string  `json:"attachment,omitempty"`
	Attachments    []string `json:"attachments,omitempty"`
	ProfileOnly    bool     `json:"profile_only,omitempty"`
	OS             *string  `json:"os,omitempty"`
	Replies        []Reply  `json:"replies,omitempty"`
	Likes          []UserId `json:"likes,omitempty"`
	Pinned         bool     `json:"pinned,omitempty"`
	IsRepost       bool     `json:"is_repost,omitempty"`
	OriginalPostID string   `json:"original_post_id,omitempty"`
	OriginalPost   *Post    `json:"original_post,omitempty"`
	EditedAt       int64    `json:"edited_at,omitempty"`
	Viewers        []UserId `json:"viewers,omitempty"`
	Poll           *Poll    `json:"poll,omitempty"`
	PublishAt      int64    `json:"publish_at,omitempty"`
}

type Poll struct {
	Options []string       `json:"options"`
	Votes   map[UserId]int `json:"votes,omitempty"`
}

type NetPollOption struct {
	Text  string `json:"text"`
	Count int    `json:"count"`
}

type NetPoll struct {
	Options []NetPollOption `json:"options"`
	Total   int             `json:"total"`
	Voted   *int            `json:"voted,omitempty"`
}

func (p *Poll) votedBy(viewer UserId) *int {
	if p == nil || viewer == "" {
		return nil
	}
	if opt, ok := p.Votes[viewer]; ok {
		v := opt
		return &v
	}
	return nil
}

func (p *Poll) ToNet() *NetPoll {
	if p == nil {
		return nil
	}
	counts := make([]int, len(p.Options))
	total := 0
	for _, opt := range p.Votes {
		if opt >= 0 && opt < len(counts) {
			counts[opt]++
			total++
		}
	}
	out := &NetPoll{Options: make([]NetPollOption, len(p.Options)), Total: total}
	for i, text := range p.Options {
		out.Options[i] = NetPollOption{Text: text, Count: counts[i]}
	}
	return out
}

type NetPost struct {
	ID           string     `json:"id"`
	Content      string     `json:"content"`
	User         Username   `json:"user"`
	Timestamp    int64      `json:"timestamp"`
	Attachment   *string    `json:"attachment,omitempty"`
	Attachments  []string   `json:"attachments,omitempty"`
	ProfileOnly  bool       `json:"profile_only,omitempty"`
	OS           *string    `json:"os,omitempty"`
	Replies      []NetReply `json:"replies,omitempty"`
	Likes        []Username `json:"likes,omitempty"`
	Pinned       bool       `json:"pinned,omitempty"`
	IsRepost     bool       `json:"is_repost,omitempty"`
	OriginalPost *NetPost   `json:"original_post,omitempty"`
	EditedAt     int64      `json:"edited_at,omitempty"`
	Premium      bool       `json:"premium,omitempty"`
	Tier         string     `json:"tier,omitempty"`
	GroupTag     string     `json:"group_tag,omitempty"`
	Views        int        `json:"views,omitempty"`
	Poll         *NetPoll   `json:"poll,omitempty"`
}

func resolveUser(u UserId) Username {
	if name := getUserById(u).GetUsername(); name != "" {
		return name
	}
	return Username(u)
}

func (p Post) ToNet() NetPost {
	replies := make([]NetReply, 0)
	likes := make([]Username, 0)
	for _, reply := range p.Replies {
		replies = append(replies, reply.ToNet())
	}
	for _, like := range p.Likes {
		likes = append(likes, resolveUser(like))
	}
	var original *NetPost
	if p.OriginalPostID != "" {
		if op := lookupPostUnlocked(p.OriginalPostID); op != nil {
			o := op.ToNet()
			original = &o
		}
	}
	if original == nil && p.OriginalPost != nil {
		o := p.OriginalPost.ToNet()
		original = &o
	}
	username := resolveUser(p.User)
	premium := false
	tierStr := ""
	if tier, ok := getUserTierCached(username); ok && tierRank(tier) >= 1 {
		tierStr = tier
		premium = hasTierOrHigher(tier, "Plus")
	}
	return NetPost{
		ID:           p.ID,
		Content:      p.Content,
		User:         username,
		Premium:      premium,
		Tier:         tierStr,
		GroupTag:     getUserGroupTagCached(username),
		Attachment:   p.Attachment,
		Attachments:  p.Attachments,
		ProfileOnly:  p.ProfileOnly,
		OS:           p.OS,
		Replies:      replies,
		Likes:        likes,
		Pinned:       p.Pinned,
		IsRepost:     p.IsRepost,
		OriginalPost: original,
		Timestamp:    p.Timestamp,
		EditedAt:     p.EditedAt,
		Views:        len(p.Viewers),
		Poll:         p.Poll.ToNet(),
	}
}

// Reply represents a reply to a post
type Reply struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	User      UserId `json:"user"`
	Timestamp int64  `json:"timestamp"`
}

type NetReply struct {
	ID        string   `json:"id"`
	Content   string   `json:"content"`
	User      Username `json:"user"`
	Timestamp int64    `json:"timestamp"`
}

func (r Reply) ToNet() NetReply {
	return NetReply{
		ID:        r.ID,
		Content:   r.Content,
		User:      resolveUser(r.User),
		Timestamp: r.Timestamp,
	}
}

type Badge struct {
	Name        string `json:"name"`
	Icon        string `json:"icon"`
	Description string `json:"description"`
}

// System represents a system definition
type System struct {
	Name        string      `json:"name"`
	Owner       SystemOwner `json:"owner"`
	Wallpaper   string      `json:"wallpaper"`
	Designation string      `json:"designation"`
	Icon        string      `json:"icon"`
}

func (s *System) Set(key string, value any) (string, error) {
	switch key {
	case "name":
		if v, ok := value.(string); ok {
			renameSystem(s.Name, v)
			s.Name = v
			return v, nil
		} else {
			return "", fmt.Errorf("invalid name value: %v", value)
		}
	case "owner":
		if v, ok := value.(SystemOwner); ok {
			s.Owner = v
			return string(v.Name), nil
		} else {
			return "", fmt.Errorf("invalid owner value: %v", value)
		}
	case "wallpaper":
		if v, ok := value.(string); ok {
			s.Wallpaper = v
			return v, nil
		} else {
			return "", fmt.Errorf("invalid wallpaper value: %v", value)
		}
	case "designation":
		if v, ok := value.(string); ok {
			s.Designation = v
			return v, nil
		} else {
			return "", fmt.Errorf("invalid designation value: %v", value)
		}
	}
	return "", fmt.Errorf("invalid system key: %s", key)
}

// SystemOwner represents the owner of a system
type SystemOwner struct {
	Name      Username `json:"name"`
	DiscordID string   `json:"discord_id"`
}

type Transaction struct {
	Type       string  `json:"type"`
	User       UserId  `json:"user"`
	To         string  `json:"to"`
	Amount     float64 `json:"amount"`
	Note       string  `json:"note"`
	Timestamp  int64   `json:"time"`
	NewTotal   float64 `json:"new_total"`
	PetitionId string  `json:"petition_id,omitempty"`
	KeyName    string  `json:"key_name,omitempty"`
	KeyId      string  `json:"key_id,omitempty"`
	GiftId     string  `json:"gift_id,omitempty"`
	GiftCode   string  `json:"gift_code,omitempty"`
}

type Gift struct {
	Id          string  `json:"id"`
	Code        string  `json:"code"`
	Amount      float64 `json:"amount"`
	Note        string  `json:"note"`
	CreatorId   UserId  `json:"creator_id"`
	CreatedAt   int64   `json:"created_at"`
	ExpiresAt   int64   `json:"expires_at"`
	ClaimedAt   *int64  `json:"claimed_at,omitempty"`
	ClaimedBy   *UserId `json:"claimed_by,omitempty"`
	CancelledAt *int64  `json:"cancelled_at,omitempty"`
}

func (g Gift) ToNet() GiftNet {
	var claimedAt *int64
	var claimedBy *Username
	if g.ClaimedAt != nil {
		claimedAt = g.ClaimedAt
	}
	if g.ClaimedBy != nil {
		username := getUserById(*g.ClaimedBy).GetUsername()
		claimedBy = &username
	}
	return GiftNet{
		Id:        g.Id,
		Code:      g.Code,
		Amount:    g.Amount,
		Note:      g.Note,
		CreatorId: getUserById(g.CreatorId).GetUsername(),
		CreatedAt: g.CreatedAt,
		ExpiresAt: g.ExpiresAt,
		ClaimedAt: claimedAt,
		ClaimedBy: claimedBy,
	}
}

func (g Gift) ToPublic() GiftPublic {
	return GiftPublic{
		Code:      g.Code,
		Amount:    g.Amount,
		Note:      g.Note,
		CreatorId: getUserById(g.CreatorId).GetUsername(),
		ExpiresAt: g.ExpiresAt,
	}
}

func (g Gift) IsActive() bool {
	return g.ClaimedAt == nil && g.CancelledAt == nil
}

func (g Gift) IsExpired() bool {
	if g.ExpiresAt == 0 {
		return false
	}
	return time.Now().UnixMilli() > g.ExpiresAt
}

func (g Gift) CanBeClaimed() bool {
	return g.IsActive() && !g.IsExpired()
}

func (g Gift) CanBeCancelled() bool {
	return g.IsActive() && !g.IsExpired()
}

type GiftNet struct {
	Id        string    `json:"id"`
	Code      string    `json:"code"`
	Amount    float64   `json:"amount"`
	Note      string    `json:"note"`
	CreatorId Username  `json:"creator_id"`
	CreatedAt int64     `json:"created_at"`
	ExpiresAt int64     `json:"expires_at"`
	ClaimedAt *int64    `json:"claimed_at,omitempty"`
	ClaimedBy *Username `json:"claimed_by,omitempty"`
}

type GiftPublic struct {
	Code      string   `json:"code"`
	Amount    float64  `json:"amount"`
	Note      string   `json:"note"`
	CreatorId Username `json:"creator_id"`
	ExpiresAt int64    `json:"expires_at"`
}

type CosmeticGift struct {
	Id         string  `json:"id"`
	CosmeticId string  `json:"cosmetic_id"`
	FromUserId UserId  `json:"from_user_id"`
	ToUserId   UserId  `json:"to_user_id"`
	Note       string  `json:"note"`
	Amount     float64 `json:"amount"` // price paid by sender
	CreatedAt  int64   `json:"created_at"`
	ClaimedAt  *int64  `json:"claimed_at,omitempty"`
}

type CosmeticGiftNet struct {
	Id           string   `json:"id"`
	CosmeticId   string   `json:"cosmetic_id"`
	CosmeticName string   `json:"cosmetic_name"`
	From         Username `json:"from"`
	To           Username `json:"to"`
	Note         string   `json:"note"`
	Amount       float64  `json:"amount"`
	CreatedAt    int64    `json:"created_at"`
	ClaimedAt    *int64   `json:"claimed_at,omitempty"`
}

func (g CosmeticGift) ToNet() CosmeticGiftNet {
	var cosmeticName string
	if entry, ok := getCatalogEntryById(g.CosmeticId); ok {
		cosmeticName = entry.Name
	} else {
		cosmeticName = g.CosmeticId
	}
	return CosmeticGiftNet{
		Id:           g.Id,
		CosmeticId:   g.CosmeticId,
		CosmeticName: cosmeticName,
		From:         getUserById(g.FromUserId).GetUsername(),
		To:           getUserById(g.ToUserId).GetUsername(),
		Note:         g.Note,
		Amount:       g.Amount,
		CreatedAt:    g.CreatedAt,
		ClaimedAt:    g.ClaimedAt,
	}
}

func (t Transaction) ToNet() TransactionNet {
	name := getUserById(t.User).GetUsername()
	if name == "" {
		name = Username(t.User)
	}
	return TransactionNet{
		Type:       t.Type,
		User:       name,
		Amount:     t.Amount,
		Note:       t.Note,
		Timestamp:  t.Timestamp,
		NewTotal:   t.NewTotal,
		PetitionId: t.PetitionId,
		KeyName:    t.KeyName,
		KeyId:      t.KeyId,
	}
}

type TransactionNet struct {
	Type       string   `json:"type"`
	User       Username `json:"user"`
	Amount     float64  `json:"amount"`
	Note       string   `json:"note"`
	Timestamp  int64    `json:"time"`
	NewTotal   float64  `json:"new_total"`
	PetitionId string   `json:"petition_id,omitempty"`
	KeyName    string   `json:"key_name,omitempty"`
	KeyId      string   `json:"key_id,omitempty"`
}

// UnmarshalJSON handles the timestamp field arriving as a string or a number.
func (r *Reply) UnmarshalJSON(data []byte) error {
	type replyAlias Reply
	var temp struct {
		replyAlias
		Timestamp any `json:"timestamp"`
	}
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}
	ts, err := parseJSONTimestamp(temp.Timestamp)
	if err != nil {
		return err
	}
	*r = Reply(temp.replyAlias)
	r.Timestamp = ts
	return nil
}

// Item represents a marketplace item
type Item struct {
	Name            string            `json:"name"`
	Description     string            `json:"description"`
	Price           int               `json:"price"`
	Selling         bool              `json:"selling"`
	Author          UserId            `json:"author"`
	Owner           UserId            `json:"owner"`
	PrivateData     any               `json:"private_data,omitempty"`
	Created         int64             `json:"created"`
	TransferHistory []TransferHistory `json:"transfer_history"`
	TotalIncome     int               `json:"total_income"`
}

func (i Item) ToNet() NetItem {
	transferHistory := make([]NetTransferHistory, 0, len(i.TransferHistory))
	for _, history := range i.TransferHistory {
		transferHistory = append(transferHistory, history.ToNet())
	}
	return NetItem{
		Name:            i.Name,
		Description:     i.Description,
		Price:           i.Price,
		Selling:         i.Selling,
		Author:          getUserById(i.Author).GetUsername(),
		Owner:           getUserById(i.Owner).GetUsername(),
		PrivateData:     i.PrivateData,
		Created:         i.Created,
		TransferHistory: transferHistory,
		TotalIncome:     i.TotalIncome,
	}
}

type NetItem struct {
	Name            string               `json:"name"`
	Description     string               `json:"description"`
	Price           int                  `json:"price"`
	Selling         bool                 `json:"selling"`
	Author          Username             `json:"author"`
	Owner           Username             `json:"owner"`
	PrivateData     any                  `json:"private_data,omitempty"`
	Created         int64                `json:"created"`
	TransferHistory []NetTransferHistory `json:"transfer_history"`
	TotalIncome     int                  `json:"total_income"`
}

type Login struct {
	Origin      string `json:"origin"`
	UserAgent   string `json:"userAgent"`
	IP_hmac     string `json:"ip_hmac"`
	Country     string `json:"country"`
	Timestamp   int64  `json:"timestamp"`
	Device_type string `json:"device_type"`
	Message     string `json:"message"`
}

// TransferHistory represents item transfer history
type TransferHistory struct {
	From      *UserId `json:"from"`
	To        UserId  `json:"to"`
	Timestamp int64   `json:"timestamp"`
	Type      string  `json:"type"`
	Price     *int    `json:"price,omitempty"`
}

func (t TransferHistory) ToNet() NetTransferHistory {
	var from *Username

	if t.From == nil {
		return NetTransferHistory{}
	}

	username := getUserById(*t.From).GetUsername()
	if username != "" {
		from = &username
	}
	return NetTransferHistory{
		From:      from,
		To:        getUserById(t.To).GetUsername(),
		Timestamp: t.Timestamp,
		Type:      t.Type,
		Price:     t.Price,
	}
}

type NetTransferHistory struct {
	From      *Username `json:"from"`
	To        Username  `json:"to"`
	Timestamp int64     `json:"timestamp"`
	Type      string    `json:"type"`
	Price     *int      `json:"price,omitempty"`
}

// Key represents an access key
type Key struct {
	Key          string                 `json:"key"`
	Creator      UserId                 `json:"creator"`
	Users        map[UserId]KeyUserData `json:"users"`
	Name         string                 `json:"name"`
	Price        int                    `json:"price"`
	Data         *string                `json:"data"`
	Subscription *Subscription          `json:"subscription,omitempty"`
	Type         string                 `json:"type"`
	TotalIncome  int                    `json:"total_income,omitempty"`
	Webhook      *string                `json:"webhook,omitempty"`
}

func (key *Key) ToNet() NetKey {
	users := make(map[Username]KeyUserData)
	for uid, v := range key.Users {
		users[getUserById(uid).GetUsername()] = v
	}
	return NetKey{
		Key:          key.Key,
		Name:         key.Name,
		Price:        key.Price,
		Type:         key.Type,
		TotalIncome:  key.TotalIncome,
		Webhook:      key.Webhook,
		Subscription: key.Subscription,
		Users:        users,
		Creator:      getUserById(key.Creator).GetUsername(),
	}
}

type NetKey struct {
	Key          string                   `json:"key"`
	Name         string                   `json:"name"`
	Price        int                      `json:"price"`
	Type         string                   `json:"type"`
	TotalIncome  int                      `json:"total_income,omitempty"`
	Webhook      *string                  `json:"webhook,omitempty"`
	Subscription *Subscription            `json:"subscription,omitempty"`
	Users        map[Username]KeyUserData `json:"users"`
	Creator      Username                 `json:"creator"`
	Data         *string                  `json:"data,omitempty"`
}

func (k *Key) setKey(key string, value any) {
	switch key {
	case "name":
		if v, ok := value.(string); ok {
			k.Name = v
		}
	case "price":
		if v, ok := value.(int); ok {
			k.Price = v
		} else if v, ok := value.(float64); ok {
			k.Price = int(v)
		}
	case "data":
		if v, ok := value.(string); ok {
			k.Data = &v
		}
	case "subscription":
		if v, ok := value.(Subscription); ok {
			k.Subscription = &v
		}
	case "type":
		if v, ok := value.(string); ok {
			k.Type = v
		}
	case "webhook":
		if v, ok := value.(string); ok {
			k.Webhook = &v
		}
	}
}

func (k *Key) ToPublic() map[string]any {
	return map[string]any{
		"key":   k.Key,
		"name":  k.Name,
		"price": k.Price,
		"type":  k.Type,
	}
}

// KeyUserData represents user data for a key
type KeyUserData struct {
	Time        int64 `json:"time"`
	Price       int   `json:"price,omitempty"`
	NextBilling any   `json:"next_billing,omitempty"`
	CancelAt    any   `json:"cancel_at,omitempty"` // unix ms; when reached, user should be removed from key
}

// Subscription represents subscription information
type Subscription struct {
	Active      bool   `json:"active"`
	Frequency   int    `json:"frequency"`
	Period      string `json:"period"`
	NextBilling any    `json:"next_billing"`
}

// Event represents a user event/notification
type Event struct {
	Type      string         `json:"type"`
	Data      map[string]any `json:"data"`
	Timestamp int64          `json:"timestamp"`
	ID        string         `json:"id"`
}

// RateLimit represents rate limiting data
type RateLimit struct {
	Count   int
	ResetAt int64
}

// RateLimitConfig represents rate limiting configuration
type RateLimitConfig struct {
	Count  int
	Period int
}

type StandingLevel string

const (
	StandingGood      StandingLevel = "good"
	StandingWarning   StandingLevel = "warning"
	StandingSuspended StandingLevel = "suspended"
	StandingBanned    StandingLevel = "banned"
)

type StandingHistoryEntry struct {
	Level     StandingLevel `json:"level"`
	Previous  StandingLevel `json:"previous"`
	Reason    string        `json:"reason"`
	AdminId   UserId        `json:"admin_id"`
	Timestamp int64         `json:"timestamp"`
}

func (u User) GetStanding() StandingLevel {
	standing := u.Get("sys.standing")
	if standing != nil {
		if str, ok := standing.(string); ok {
			return StandingLevel(str)
		}
	}
	return StandingGood
}

func (u User) SetStanding(level StandingLevel, reason string, adminId UserId) {
	current := u.GetStanding()
	if current == level {
		return
	}

	u.Set("sys.standing", string(level))

	history := u.GetStandingHistory()
	newEntry := StandingHistoryEntry{
		Level:     level,
		Previous:  current,
		Reason:    reason,
		AdminId:   adminId,
		Timestamp: time.Now().Unix(),
	}
	history = append(history, newEntry)
	u.Set("sys.standing_history", history)

	switch level {
	case StandingGood:
		u.Set("sys.standing_recover_at", nil)
	case StandingWarning:
		u.Set("sys.standing_recover_at", time.Now().Add(7*24*time.Hour).Unix())
	case StandingSuspended:
		u.Set("sys.standing_recover_at", time.Now().Add(30*24*time.Hour).Unix())
	case StandingBanned:
		u.Set("sys.standing_recover_at", nil)
		u.Set("sys.banned", true)
	}
}

func (u User) GetStandingHistory() []StandingHistoryEntry {
	history := u.Get("sys.standing_history")
	if history == nil {
		return []StandingHistoryEntry{}
	}
	entries, ok := history.([]any)
	if !ok {
		return []StandingHistoryEntry{}
	}
	out := make([]StandingHistoryEntry, 0, len(entries))
	for _, e := range entries {
		if entryMap, ok := e.(map[string]any); ok {
			entry := StandingHistoryEntry{
				Level:     StandingLevel(getStringOrDefault(entryMap["level"], "good")),
				Previous:  StandingLevel(getStringOrDefault(entryMap["previous"], "good")),
				Reason:    getStringOrEmpty(entryMap["reason"]),
				AdminId:   UserId(getStringOrEmpty(entryMap["admin_id"])),
				Timestamp: int64(getIntOrDefault(entryMap["timestamp"], 0)),
			}
			out = append(out, entry)
		}
	}
	return out
}

func (u User) GetStandingRecoverAt() int64 {
	return getInt64OrDefault(u.Get("sys.standing_recover_at"), 0)
}

func standingRank(level StandingLevel) int {
	switch level {
	case StandingGood:
		return 0
	case StandingWarning:
		return 1
	case StandingSuspended:
		return 2
	case StandingBanned:
		return 3
	default:
		return 0
	}
}

func (u User) canStanding(minAllowed StandingLevel) bool {
	return standingRank(u.GetStanding()) <= standingRank(minAllowed)
}

func (u User) CanCreatePost() bool     { return u.canStanding(StandingGood) }
func (u User) CanCreateReply() bool    { return u.canStanding(StandingGood) }
func (u User) CanRepost() bool         { return u.canStanding(StandingGood) }
func (u User) CanTradeBuy() bool       { return u.canStanding(StandingWarning) }
func (u User) CanTradeSell() bool      { return u.canStanding(StandingGood) }
func (u User) CanTradeTransfer() bool  { return u.canStanding(StandingGood) }
func (u User) CanInteractFriend() bool { return u.canStanding(StandingGood) }
func (u User) CanFollow() bool         { return u.canStanding(StandingWarning) }

func (u User) HasStandingOrHigher(required StandingLevel) bool {
	return standingRank(u.GetStanding()) <= standingRank(required)
}

// Global variables
var (
	startTime = time.Now()

	users           []User
	usernameToId    map[Username]UserId
	idToUser        map[UserId]User
	keyToId         map[string]UserId
	emailToId       map[string]UserId
	resetTokenToId  map[string]UserId
	verifyTokenToId map[string]UserId
	idToUserMutex   sync.RWMutex
	usersMutex      sync.RWMutex

	groupsData      map[string]*GroupData
	groupsDataMutex sync.RWMutex

	followersData     map[UserId]FollowerData
	followingCountMap map[UserId]int
	followersMutex    sync.RWMutex

	posts      []Post
	postsMutex sync.RWMutex

	items      []Item
	itemsMutex sync.RWMutex

	keys           []Key
	keyStringToIdx map[string]int
	keysMutex      sync.RWMutex

	systems      map[string]System
	systemsMutex sync.RWMutex

	eventsHistory      map[UserId][]Event
	eventsHistoryMutex sync.RWMutex

	rateLimitStorage = make(map[string]*RateLimit)
	rateLimitMutex   sync.RWMutex

	keyOwnershipCacheMutex sync.RWMutex

	gifts      []Gift
	giftsMutex sync.RWMutex

	cosmeticGifts      []CosmeticGift
	cosmeticGiftsMutex sync.RWMutex
)

// UnmarshalJSON handles the timestamp field arriving as a string or a number.
func (p *Post) UnmarshalJSON(data []byte) error {
	type postAlias Post
	var temp struct {
		postAlias
		Timestamp any `json:"timestamp"`
	}
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}
	ts, err := parseJSONTimestamp(temp.Timestamp)
	if err != nil {
		return err
	}
	*p = Post(temp.postAlias)
	p.Timestamp = ts
	return nil
}
