package main

import (
	"claw/internal/config"
	crypto_rand "crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"claw/internal/captcha"

	"github.com/gin-gonic/gin"
)

func getAccountsBy(key string, value string, max int) ([]User, error) {
	var matches []User
	switch key {
	case "username":
		valueLower := Username(value).ToLower()
		idToUserMutex.RLock()
		uid, ok := usernameToId[valueLower]
		idToUserMutex.RUnlock()
		if ok {
			idToUserMutex.RLock()
			user := idToUser[uid]
			idToUserMutex.RUnlock()
			if user != nil {
				matches = append(matches, user)
				if max != -1 && len(matches) >= max {
					return matches, nil
				}
			}
		}
	case "key":
		idToUserMutex.RLock()
		id, ok := keyToId[value]
		idToUserMutex.RUnlock()
		if ok {
			user := getUserById(id)
			if user != nil && user.GetKey() == value {
				matches = append(matches, user)
				if max != -1 && len(matches) >= max {
					return matches, nil
				}
			}
		}
	default:
		usersMutex.RLock()
		for _, user := range users {
			if fmt.Sprintf("%v", user.Get(key)) == value {
				matches = append(matches, user)
				if max != -1 && len(matches) >= max {
					usersMutex.RUnlock()
					return matches, nil
				}
			}
		}
		usersMutex.RUnlock()
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("account not found for %s=%q", key, value)
	}
	return matches, nil
}

func getAccountByUserId[T UserId | string](id T) (User, error) {
	uid := UserId(id)
	idToUserMutex.RLock()
	user := idToUser[uid]
	idToUserMutex.RUnlock()
	if user != nil {
		return user, nil
	}
	return nil, fmt.Errorf("account not found for id=%q", id)
}

func resolveUserByName(c *gin.Context, username string) (User, bool) {
	userId := getIdByUsername(Username(username))
	if userId == "" {
		c.JSON(404, gin.H{"error": "user not found"})
		return nil, false
	}
	user := getUserById(userId)
	if len(user) == 0 {
		c.JSON(404, gin.H{"error": "user not found"})
		return nil, false
	}
	return user, true
}

func getAccountByUsername[T Username | string](username T) (User, error) {
	name := Username(username).ToLower()
	idToUserMutex.RLock()
	uid, ok := usernameToId[name]
	if !ok {
		idToUserMutex.RUnlock()
		return nil, fmt.Errorf("account not found for username=%q", username)
	}
	user := idToUser[uid]
	idToUserMutex.RUnlock()
	if user != nil {
		return user, nil
	}
	return nil, fmt.Errorf("account not found for username=%q", username)
}

func findAccountByLogin(username string, password string) (User, error) {
	name := Username(username).ToLower()
	idToUserMutex.RLock()
	uid, ok := usernameToId[name]
	idToUserMutex.RUnlock()
	if !ok {
		return nil, fmt.Errorf("account not found for login")
	}
	idToUserMutex.RLock()
	user := idToUser[uid]
	idToUserMutex.RUnlock()
	if user == nil {
		return nil, fmt.Errorf("account not found for login")
	}
	matched, needsUpgrade := VerifyPassword(user, password)
	if matched {
		if needsUpgrade {
			UpgradePasswordToV1(user, password)
			go saveUsers()
		}
		return user, nil
	}
	return nil, fmt.Errorf("account not found for login")
}

func getIdxOfAccountBy(key string, value string) int {
	if key == "username" {
		valueLower := Username(value).ToLower()
		idToUserMutex.RLock()
		uid, ok := usernameToId[valueLower]
		idToUserMutex.RUnlock()
		if !ok {
			return -1
		}
		usersMutex.RLock()
		for i := range users {
			if users[i].GetId() == uid {
				usersMutex.RUnlock()
				return i
			}
		}
		usersMutex.RUnlock()
		return -1
	}

	usersMutex.RLock()
	defer usersMutex.RUnlock()
	for i, user := range users {
		if user.Get(key) == value {
			return i
		}
	}
	return -1
}

// helper function to update user keys
func setAccountKey(username Username, key string, value any) error {
	user, err := getAccountByUsername(username)
	if err != nil {
		return fmt.Errorf("user not found: %s", username)
	}
	user.Set(key, value)
	return nil
}

func getUserBy(c *gin.Context) {
	if !authenticateAdmin(c) {
		return
	}

	key := c.Query("key")
	if !requireField(c, key, "Key is required") {
		return
	}

	value := c.Query("value")
	if value == "" {
		var body struct {
			Value string `json:"value"`
		}
		_ = c.ShouldBindJSON(&body)
		value = body.Value
	}
	if !requireField(c, value, "Value is required") {
		return
	}

	foundUsers, err := getAccountsBy(key, value, 1)
	if err != nil {
		c.JSON(404, gin.H{"error": "User not found"})
		return
	}

	userCopy := copyUser(foundUsers[0])
	delete(userCopy, "password")
	delete(userCopy, "sys.salt")

	c.JSON(200, userToNet(userCopy))
}

func getUser(c *gin.Context) {
	authKey := extractAuthKey(c)

	var foundUser User

	var subToken *SubToken

	if authKey != "" {
		if user, st := authenticateAnyKey(authKey); user != nil {
			foundUser = *user
			subToken = st
		}
	} else {
		if bodyUser := tryBodyLogin(c); bodyUser != nil {
			foundUser = *bodyUser
		}
	}

	username := c.Query("username")
	password := c.Query("password")

	if username != "" && password != "" && foundUser == nil {
		var err error = nil
		foundUser, err = findAccountByLogin(username, password)
		if err != nil || foundUser == nil {
			addLogin(c, foundUser, "Failed login")
			c.JSON(403, gin.H{"error": "Invalid authentication credentials"})
			return
		}
	}

	if foundUser != nil {

		if foundUser.IsBanned() {
			c.JSON(403, gin.H{
				"error":    "User is banned",
				"username": foundUser.GetUsername(),
			})
			return
		}

		if foundUser.Get("sys.email_verified") == false {
			c.JSON(403, gin.H{
				"error":              "Email address not verified",
				"username":           foundUser.GetUsername(),
				"token":              foundUser.GetKey(),
				"sys.email_verified": false,
			})
			return
		}

		if foundUser.Get("sys.tos_accepted") != true {
			c.JSON(403, gin.H{
				"error":            "Terms-Of-Service are not accepted or outdated",
				"username":         foundUser.GetUsername(),
				"token":            foundUser.GetKey(),
				"sys.tos_accepted": false,
			})
			return
		}

		ip := c.ClientIP()
		blocked_ips := foundUser.GetBlockedIps()
		if slices.Contains(blocked_ips, ip) {
			addLogin(c, foundUser, "Blocked ip attempted login")
			c.JSON(403, gin.H{"error": "Unable to login to this account"})
			return
		}

		now := time.Now().UnixMilli()
		foundUser.Set("sys.last_login", now)
		foundUser.Set("sys.total_logins", foundUser.GetInt("sys.total_logins")+1)
		foundUser.Set("sys.badges", calculateUserBadges(foundUser))

		header := c.GetHeader("CF-IPCountry")
		if header == "T1" {
			// block tor
			addLogin(c, foundUser, "Tor login attempted")
			c.JSON(403, gin.H{"error": "Tor is not allowed"})
			return
		}

		addLogin(c, foundUser, "Successful Login")

		go saveUsers()

		if subToken != nil {
			if !subToken.hasPermission(PermViewProfile) {
				c.JSON(200, userToProfileOnly(foundUser, authKey))
				return
			}
			c.JSON(200, userToNetWithSubToken(foundUser, authKey))
			return
		}

		c.JSON(200, userToNet(foundUser))

		return
	}

	c.JSON(403, gin.H{"error": "Invalid authentication credentials"})
}

func userToNet(user User) User {
	mu := getMutexForUser(user)
	mu.RLock()
	userCopy := make(User, len(user)+4)
	for k, v := range user {
		if k == "password" || k == "sys.salt" {
			continue
		}
		userCopy[k] = v
	}
	mu.RUnlock()

	userCopy["sys.friends"] = user.GetFriendUsers()
	userCopy["sys.requests"] = user.GetRequestedUsers()
	userCopy["sys.blocked"] = user.GetBlockedUsers()
	userCopy["sys.notes"] = user.GetNotesNet()
	userCopy["sys.moderator"] = user.IsModerator()
	userCopy["sys.admin"] = user.IsNetworkAdmin()
	transactions := user.GetTransactions()
	netTransactions := make([]TransactionNet, len(transactions))
	for i, transaction := range transactions {
		netTransactions[i] = transaction.ToNet()
	}
	userCopy["sys.transactions"] = netTransactions
	if sub, ok := userCopy["sys.subscription"].(map[string]any); ok {
		subCopy := make(map[string]any, len(sub))
		for k, v := range sub {
			subCopy[k] = v
		}
		subCopy["tier"] = user.GetSubscription().Tier
		userCopy["sys.subscription"] = subCopy
	}

	if userCopy["sys.banner"] != nil || userCopy["banner"] != nil {
		userCopy["banner"] = bannerURL(string(user.GetUsername()))
	}

	return userCopy
}

func userToNetWithSubToken(user User, subTokenValue string) User {
	netUser := userToNet(user)
	netUser["key"] = subTokenValue
	return netUser
}

func userToProfileOnly(user User, subTokenValue string) map[string]any {
	username := user.GetUsername()
	userId := user.GetId()
	followersMutex.RLock()
	followerCount := 0
	if data, exists := followersData[userId]; exists {
		followerCount = len(data.Followers)
	}
	followingCount := followingCountMap[userId]
	followersMutex.RUnlock()
	maxSizeStr := user.GetMaxSize()
	sub := user.GetSubscription().Tier
	calculatedBadges := calculateUserBadges(user)
	st := hub.getUserStatus(user.GetId())
	bio := user.GetString("bio")
	benefits := user.GetSubscriptionBenefits()
	if len(bio) > benefits.Bio_Length {
		bio = bio[:benefits.Bio_Length]
	}
	profileData := map[string]any{
		"key":          subTokenValue,
		"username":     username,
		"pfp":          avatarURL(string(username)),
		"sys.banned":   user.IsBanned(),
		"private":      user.IsPrivate(),
		"bio":          bio,
		"followers":    followerCount,
		"following":    followingCount,
		"pronouns":     user.GetString("pronouns"),
		"system":       user.GetSystem(),
		"created":      user.GetCreated(),
		"badges":       calculatedBadges,
		"subscription": sub,
		"max_size":     maxSizeStr,
		"currency":     user.GetCredits(),
		"index":        int(user.GetIndex()),
		"status":       st,
		"id":           userId,
	}
	if user.Get("sys.banner") != nil || user.Get("banner") != nil {
		profileData["banner"] = bannerURL(string(username))
	}
	if tag := groupTagForUser(user); tag != "" {
		profileData["group_tag"] = tag
	}
	return profileData
}

func checkAuth(c *gin.Context) {
	user := currentUser(c)
	tokenType := c.GetString("token_type")

	resp := gin.H{
		"auth":       true,
		"username":   user.GetUsername(),
		"token_type": tokenType,
	}
	if tokenType == "sub" {
		if st, ok := c.Get("sub_token"); ok {
			if subToken, ok := st.(*SubToken); ok {
				resp["permissions"] = subToken.Permissions
			}
		}
	}
	c.JSON(200, resp)
}

func addLogin(c *gin.Context, user User, message string) {
	if user == nil {
		return
	}
	logins := user.GetLogins()
	ip := c.ClientIP()
	hostname := c.GetHeader("Origin")
	userAgent := c.Request.UserAgent()
	device_type := "Unknown"
	if c.GetHeader("Sec-CH-UA-Mobile") == "?1" {
		device_type = "Mobile"
	} else {
		device_type = "Desktop"
	}

	logins = append(logins, Login{
		Origin:      hostname,
		UserAgent:   userAgent,
		IP_hmac:     hmacIp(ip),
		Country:     c.GetHeader("CF-IPCountry"),
		Timestamp:   time.Now().UnixMilli(),
		Device_type: device_type,
		Message:     message,
	})
	maxLogins := user.GetSubscriptionBenefits().Max_Login_History
	if n := len(logins); n > maxLogins {
		logins = logins[n-maxLogins:]
	}
	user.Set("sys.logins", logins)
}

func generateAccountToken() string {
	randomBytes := make([]byte, 64)
	_, err := crypto_rand.Read(randomBytes)
	if err != nil {
		log.Printf("CRITICAL: Failed to generate secure random token: %v", err)
		panic("failed to generate secure random token")
	}

	token := base64.URLEncoding.EncodeToString(randomBytes)

	return token
}

func refreshToken(c *gin.Context) {
	user := currentUser(c)

	newToken := generateAccountToken()

	user.Set("key", newToken)
	go saveUsers()

	c.JSON(200, gin.H{"token": newToken})
}

func registerUser(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Email    string `json:"email"`
		System   string `json:"system"`
		Captcha  string `json:"captcha"`
	}

	if !bindJSON(c, &req) {
		return
	}

	ip := c.ClientIP()
	from_url := c.GetHeader("referer")
	if from_url == "" {
		from_url = c.GetHeader("origin")
		if from_url == "" {
			from_url = "unknown"
		}
	}

	if isBannedIp(ip) {
		randomResponses := []string{
			"so sad, stay mad",
			"L bozo",
			"L",
			":3",
			"damn so close that time",
			"awwww",
			"ur gay :3 (and gay people are awesome)",
			"Take a shower",
			"even a toddler could do this better",
		}
		var idx int
		idxBytes := make([]byte, 1)
		crypto_rand.Read(idxBytes)
		idx = int(idxBytes[0]) % len(randomResponses)
		c.JSON(403, gin.H{"error": randomResponses[idx]})
		return
	}

	if !captcha.VerifyTurnstile(req.Captcha) {
		c.JSON(400, gin.H{"error": "CAPTCHA verification failed"})
		return
	}

	username := Username(req.Username)
	password := req.Password
	email := req.Email
	system := req.System

	if !requireFields(c, "Username and password are required", string(username), password) {
		return
	}

	if email == "" || !isValidEmail(email) {
		c.JSON(400, gin.H{"error": "A valid email address is required"})
		return
	}
	if isDisposableEmail(email) {
		c.JSON(400, gin.H{"error": "Disposable email addresses are not allowed"})
		return
	}

	if ok, msg := ValidateUsername(username); !ok {
		c.JSON(400, gin.H{"error": msg})
		return
	}

	if IsIpInBannedList(ip) {
		c.JSON(400, gin.H{"error": "IP address is banned"})
		return
	}

	if ok, msg := ValidatePassword(password); !ok {
		c.JSON(400, gin.H{"error": msg})
		return
	}

	// Validate username against systems with detailed feedback
	isValid, errorMessage, matchedSystem := validateSystem(system)
	if !isValid {
		c.JSON(400, gin.H{"error": errorMessage})
		return
	}

	newUser, err := createAccount(AccountCreateInput{
		Username:      username,
		Password:      password,
		Email:         email,
		System:        matchedSystem,
		Provider:      "rotur",
		RequestIP:     ip,
		RequestOrigin: from_url,
	})
	if err != nil {
		if strings.Contains(err.Error(), "username already") {
			c.JSON(400, gin.H{"error": "Username already in use"})
			return
		}
		if strings.Contains(err.Error(), "email already") {
			c.JSON(400, gin.H{"error": "Email already in use"})
			return
		}
		c.JSON(500, gin.H{"error": "Failed to create account"})
		return
	}
	verifyToken := generateVerifyToken()
	newUser.Set("sys.email_verify_token", verifyToken)
	newUser.Set("sys.email_verify_sent", time.Now().UnixMilli())
	go saveUsers()
	go sendVerifyEmail(email, string(username), verifyToken)

	userCopy := copyUser(newUser)
	delete(userCopy, "password")
	delete(userCopy, "sys.email_verify_token")
	delete(userCopy, "sys.salt")
	c.JSON(201, userCopy)
}

func findUserSize(username Username) int {
	user, err := getAccountByUsername(username)
	if err != nil {
		return 0
	}
	totalSize := 0
	mu := getMutexForUser(user)
	mu.RLock()
	for k, v := range user {
		if strings.HasPrefix(k, "sys.") {
			continue
		}
		switch v := v.(type) {
		case string:
			totalSize += len(v)
		case []string:
			for _, item := range v {
				totalSize += len(item)
			}
		case []any:
			for _, item := range v {
				if strItem, ok := item.(string); ok {
					totalSize += len(strItem)
				}
			}
		case map[string]any:
			for mk, mv := range v {
				if strMv, ok := mv.(string); ok {
					totalSize += len(strMv)
				}
				if strings.ToLower(mk) != "username" && strings.ToLower(mk) != "password" {
					totalSize += len(mk)
				}
			}
		default:
			totalSize += 100
		}
	}
	mu.RUnlock()
	return totalSize
}
func canUpdateUsernameUnsafe(username Username) (bool, string) {
	if username == "" {
		return false, "Invalid username"
	}
	ok, msg := ValidateUsername(username)
	if !ok {
		return false, msg
	}
	usernameLower := username.ToLower()
	idToUserMutex.RLock()
	_, taken := usernameToId[usernameLower]
	idToUserMutex.RUnlock()
	if taken {
		return false, "Username already in use"
	}

	return true, "Can update username"
}

func updateUsername(oldUsername, newUsername Username) {
	usernameLower := oldUsername.ToLower()
	newUsernameLower := newUsername.ToLower()

	if usernameLower == newUsernameLower {
		return
	}

	fs.RenameUserFileSystem(string(usernameLower), string(newUsernameLower))
	renameUserAvatar(oldUsername, newUsername)
}

func updateUser(c *gin.Context) {
	var req struct {
		Auth  string `json:"auth"`
		Key   string `json:"key"`
		Value any    `json:"value"`
	}
	if !bindJSON(c, &req) {
		return
	}
	authKey := req.Auth
	if authKey == "" {
		authKey = stripBearer(c.GetHeader("Authorization"))
	}
	key := req.Key
	if !requireField(c, key, "Key is required") {
		return
	}
	value := req.Value
	if value == nil {
		c.JSON(400, gin.H{"error": "Value is required"})
		return
	}
	stringValue := fmt.Sprintf("%v", value)

	if authKey == "" {
		c.JSON(403, gin.H{"error": "auth key is required"})
		return
	}

	user, subToken := authenticateAnyKey(authKey)
	if user == nil {
		c.JSON(403, gin.H{"error": "Invalid authentication key"})
		return
	}
	if subToken != nil && !subToken.hasPermission(PermManageProfile) {
		c.JSON(403, gin.H{"error": "Token lacks permission: " + string(PermManageProfile)})
		return
	}

	username := user.GetUsername()

	if key == "banner" {
		// Allow both data URIs and normal URLs
		var imageData string
		if strings.HasPrefix(stringValue, "data:") {
			imageData = stringValue
		} else {
			c.JSON(400, gin.H{"error": "Banner must be a valid data URI"})
			return
		}
		if getIdByUsername(username) == "" {
			c.JSON(403, gin.H{"error": "User not found"})
			return
		}
		benefits := user.GetSubscriptionBenefits()
		freeAndGifUploads := benefits.Has_Free_Banner_Uploads
		var currencyFloat float64 = user.GetCredits()
		if currencyFloat < 10 && !freeAndGifUploads {
			c.JSON(403, gin.H{"error": "Not enough credits to set banner (10 required)"})
			return
		}
		if err := saveBanner(imageData, user); err != nil {
			serverError(c, err)
			return
		}
		if !freeAndGifUploads {
			user.SetBalance(currencyFloat - 10)
		}
		user.Set("sys.banner", bannerURL(user.GetUsername()))
		go OnUserUpdate(user.GetId(), "sys.banner", bannerURL(user.GetUsername()))
		go saveUsers()
		c.JSON(200, gin.H{"message": "Banner uploaded successfully"})
		return
	}

	if key == "pfp" {
		// Allow both data URIs and normal URLs
		var imageData string
		if strings.HasPrefix(stringValue, "data:") {
			imageData = stringValue
		} else {
			c.JSON(400, gin.H{"error": "Profile picture must be a valid data URI"})
			return
		}
		if err := savePfp(imageData, user); err != nil {
			serverError(c, err)
			return
		}
		go broadcastUserUpdate(user.GetUsername(), "pfp", avatarURL(user.GetUsername()))
		go OnUserUpdate(user.GetId(), "pfp", avatarURL(user.GetUsername()))
		go saveUsers()
		c.JSON(200, gin.H{"message": "Profile picture uploaded successfully"})
		return
	}

	// Check for admin privileges - try Authorization header first, then query param
	var admin bool
	if c.GetHeader("Authorization") != "" {
		admin = isAdmin(c)
	} else {
		// Fall back to query param method
		envOnce.Do(loadEnvFile)
		ADMIN_TOKEN := os.Getenv("ADMIN_TOKEN")
		adminToken := c.Query("token")
		admin = adminToken != "" && ADMIN_TOKEN != "" && subtle.ConstantTimeCompare([]byte(adminToken), []byte(ADMIN_TOKEN)) == 1
	}

	totalSize := findUserSize(username)
	if totalSize+len(fmt.Sprintf("%v", value)) > 25000 {
		c.JSON(400, gin.H{"error": "Total account size exceeds 25000 bytes"})
		return
	}

	if key == "sys.id" {
		c.JSON(400, gin.H{"error": "Cannot update sys.id"})
		return
	}

	if key == "email" {
		emailLower := strings.ToLower(strings.TrimSpace(stringValue))
		idToUserMutex.RLock()
		conflictId, emailConflict := emailToId[emailLower]
		idToUserMutex.RUnlock()
		if emailConflict && conflictId != user.GetId() {
			c.JSON(400, gin.H{"error": "Email already in use"})
			return
		}

		user.Set("sys.email_verified", false)
		verifyToken := generateVerifyToken()
		user.Set("sys.email_verify_token", verifyToken)
		user.Set("sys.email_verify_sent", time.Now().UnixMilli())
		go sendVerifyEmail(stringValue, string(user.GetUsername()), verifyToken)
	}

	if key == "bio" {
		length := len(stringValue)
		bio_length := user.GetSubscriptionBenefits().Bio_Length
		if length > bio_length {
			c.JSON(400, gin.H{"error": "Bio length exceeds " + strconv.Itoa(bio_length) + " characters"})
			return
		}
	}

	if key == "system" {
		// switch your account's system
		systems := getAllSystems()
		for _, system := range systems {
			if system.Name == stringValue {
				user.Set("system", system.Name)
				c.JSON(200, gin.H{"message": "Successfully switched system to " + system.Name})
				return
			}
		}
		c.JSON(404, gin.H{"error": "System not found"})
		return
	}

	if strings.HasPrefix(key, "sys.") && !admin {
		c.JSON(400, gin.H{"error": "System keys cannot be modified directly"})
		return
	}
	if !requireMaxLen(c, stringValue, 1000, "Value length exceeds 1000 characters") {
		return
	}
	if !requireMaxLen(c, key, 20, "Key length exceeds 20 characters") {
		return
	}
	if key == "username" {
		usersMutex.RLock()
		ok, msg := canUpdateUsernameUnsafe(Username(getStringOrEmpty(value)))
		usersMutex.RUnlock()
		if !ok {
			c.JSON(400, gin.H{"error": msg})
			return
		}
		updateUsername(user.GetUsername(), Username(getStringOrEmpty(value)))
	}
	if slices.Contains(lockedKeys, key) {
		c.JSON(400, gin.H{"error": fmt.Sprintf("Key '%s' cannot be updated", key)})
		return
	}

	if err := setAccountKey(username, key, value); err != nil {
		c.JSON(404, gin.H{"error": err.Error()})
		return
	}

	go saveUsers()

	c.JSON(200, gin.H{
		"message":  "User key updated successfully",
		"username": username,
		"key":      key,
		"value":    value,
	})
}

func updateUserAdmin(c *gin.Context) {
	if !authenticateAdmin(c) {
		return
	}

	var userData map[string]any
	if !bindJSON(c, &userData) {
		return
	}

	operationType, hasType := userData["type"].(string)
	if hasType {
		username, ok := userData["username"].(string)
		if !ok || username == "" {
			c.JSON(400, gin.H{"error": "username is required"})
			return
		}

		switch operationType {
		case "update":
			key, hasKey := userData["key"].(string)
			value, hasValue := userData["value"]

			if slices.Contains(lockedKeys, key) {
				c.JSON(400, gin.H{"error": "Key '" + key + "' cannot be updated"})
				return
			}

			if !hasKey || !hasValue {
				c.JSON(400, gin.H{"error": "key and value are required for update operation"})
				return
			}

			user, err := getAccountByUsername(username)
			if err != nil {
				c.JSON(404, gin.H{"error": "User not found"})
				return
			}

			if !strings.HasPrefix(key, "sys.") {
				if len(key) > 50 {
					c.JSON(400, gin.H{"error": fmt.Sprintf("Key '%s' length exceeds 50 characters", key)})
					return
				}
			}

			switch key {
			case "username":
				username := Username(getStringOrEmpty(value))
				if ok, msg := canUpdateUsernameUnsafe(username); !ok {
					c.JSON(400, gin.H{"error": msg})
					return
				}
				oldUsername := user.GetUsername()
				updateUsername(oldUsername, username)
				user.Set("username", string(username))
			case "sys.currency":
				user.SetBalance(value)
			default:
				user.Set(key, value)
			}

			go saveUsers()

			c.JSON(200, gin.H{
				"message":  "User updated successfully",
				"username": username,
				"key":      key,
				"value":    value,
			})
			return

		case "remove":
			key, hasKey := userData["key"].(string)
			if !hasKey || key == "" {
				c.JSON(400, gin.H{"error": "key is required for remove operation"})
				return
			}

			user, err := getAccountByUsername(username)
			if err != nil {
				c.JSON(404, gin.H{"error": "User not found"})
				return
			}

			if strings.HasPrefix(key, "sys.") {
				c.JSON(400, gin.H{"error": "System keys cannot be deleted"})
				return
			}

			if slices.Contains(lockedKeys, key) {
				c.JSON(400, gin.H{"error": fmt.Sprintf("Key '%s' cannot be deleted", key)})
				return
			}

			user.DelKey(key)

			go saveUsers()

			go hub.broadcastToUserConns(user.GetId(), "key_delete", map[string]any{
				"key": key,
			})
			c.JSON(200, gin.H{
				"message":  "User key deleted successfully",
				"username": username,
				"key":      key,
			})
			return

		default:
			c.JSON(400, gin.H{"error": fmt.Sprintf("Invalid operation type '%s'. Must be 'update' or 'remove'", operationType)})
			return
		}
	}

	c.JSON(400, gin.H{"error": "type parameter is required. Must be 'update' or 'remove'"})
}

func gambleCredits(c *gin.Context) {
	c.JSON(400, gin.H{"error": "This endpoint is no longer available"})
}

func deleteUserKey(c *gin.Context) {
	var req struct {
		Auth string `json:"auth"`
		Key  string `json:"key"`
	}
	if !bindJSON(c, &req) {
		return
	}
	authKey := req.Auth
	key := req.Key
	if authKey == "" {
		c.JSON(403, gin.H{"error": "auth key is required"})
		return
	}

	user, subToken := authenticateAnyKey(authKey)
	if user == nil {
		c.JSON(403, gin.H{"error": "Invalid authentication key"})
		return
	}
	if subToken != nil && !subToken.hasPermission(PermDeleteAccount) {
		c.JSON(403, gin.H{"error": "Token lacks permission: " + string(PermDeleteAccount)})
		return
	}

	username := user.GetUsername()
	if username == "" {
		c.JSON(403, gin.H{"error": "User not found"})
		return
	}

	if !requireField(c, key, "Key is required") {
		return
	}

	if strings.HasPrefix(key, "sys.") {
		c.JSON(400, gin.H{"error": "System keys cannot be deleted"})
		return
	}

	if slices.Contains(lockedKeys, key) {
		c.JSON(400, gin.H{"error": fmt.Sprintf("Key '%s' cannot be deleted", key)})
		return
	}

	if key == "username" {
		c.JSON(400, gin.H{"error": "Cannot delete username key"})
		return
	}

	user.DelKey(key)

	go saveUsers()

	go hub.broadcastToUserConns(user.GetId(), "key_delete", map[string]any{
		"key": key,
	})

	c.JSON(200, gin.H{"message": "User key deleted successfully", "username": username, "key": key})
}

// PerformCreditTransfer performs a credit transfer between two users.
// Handles tax, transaction logging, and safety rules.
// Returns an error if the transfer cannot be completed.
func PerformCreditTransfer(fromUsername, toUsername Username, amount float64, note string) error {
	const taxRecipientShare = 0.25

	// normalize + validate amount
	nAmount, ok := normalizeEscrowAmount(amount)
	if !ok {
		return fmt.Errorf("minimum amount is 0.01")
	}

	fromUser, err := getAccountByUsername(fromUsername)
	if err != nil {
		return fmt.Errorf("sender user not found")
	}

	toUser, err := getAccountByUsername(toUsername)
	if err != nil {
		return fmt.Errorf("recipient user not found")
	}

	if fromUser.GetUsername().ToLower() == toUser.GetUsername().ToLower() {
		return fmt.Errorf("cannot send credits to yourself")
	}

	fromCurrency := roundVal(fromUser.GetCredits())
	if fromUsername != "rotur" {
		if fromCurrency < nAmount {
			return fmt.Errorf("insufficient funds (required: %.2f, available: %.2f)", nAmount, fromCurrency)
		}
	}

	toCurrency := roundVal(toUser.GetCredits())

	now := time.Now().UnixMilli()

	// Helper: clean note
	mkNote := func(base string) string {
		n := strings.TrimSpace(base)
		if n == "" {
			n = "transfer"
		}
		if len(n) > 50 {
			n = n[:50]
		}
		return n
	}
	note = mkNote(note)

	// Send credits when rotur is the sender
	if fromUsername == "rotur" {
		taxRecipient := Username("mist")
		fromSystem := toUser.GetSystem()
		systemsMutex.RLock()
		if sys, ok := systems[fromSystem]; ok {
			taxRecipient = sys.Owner.Name
		}
		systemsMutex.RUnlock()

		// Apply tax to taxRecipient if exists
		if taxUser, err := getAccountByUsername(taxRecipient); err == nil && taxRecipient != toUser.GetUsername() {
			newBalance := roundVal(taxUser.GetCredits() + taxRecipientShare)
			taxUser.applyTransaction(newBalance, Transaction{
				Note:      "Daily credit",
				User:      toUser.GetId(),
				Timestamp: now,
				Amount:    taxRecipientShare,
				Type:      "tax",
			})
		}
	}
	// Update balances
	if fromUser.GetUsername() != "rotur" {
		fromUser.SetBalance(roundVal(fromCurrency - nAmount))
	}
	if toUser.GetUsername() != "rotur" {
		toUser.SetBalance(roundVal(toCurrency + nAmount))
	}

	// Log transactions
	fromUser.addTransaction(Transaction{
		Note:     note,
		User:     toUser.GetId(),
		Amount:   nAmount,
		Type:     "out",
		NewTotal: fromCurrency - nAmount,
	})
	toUser.addTransaction(Transaction{
		Note:     note,
		User:     fromUser.GetId(),
		Amount:   nAmount,
		Type:     "in",
		NewTotal: toCurrency + nAmount,
	})

	go saveUsers()

	return nil
}

func transferCredits(c *gin.Context) {
	user := currentUser(c)

	var req struct {
		To     string `json:"to"`
		Amount any    `json:"amount"`
		Note   string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request payload"})
		return
	}
	amt := fmt.Sprintf("%v", req.Amount)

	if !requireField(c, amt, "Amount must be provided") {
		return
	}
	var nAmount float64
	var err error
	if after, ok := strings.CutPrefix(amt, "£"); ok {
		// convert to GBP
		nAmount, err = strconv.ParseFloat(after, 64)
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid amount"})
			return
		}
		creditsPerPound := creditsToPence(1) * 100
		nAmount = nAmount / creditsPerPound
	} else {
		nAmount, err = strconv.ParseFloat(amt, 64)
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid amount"})
			return
		}
	}
	nAmount = math.Round(nAmount*100) / 100 // round to 2 decimal places
	if nAmount < 0.01 {
		c.JSON(400, gin.H{"error": "Minimum amount is 0.01"})
		return
	}

	toUsername := Username(req.To).ToLower()
	if !requireField(c, toUsername, "Recipient username and amount must be provided") {
		return
	}
	if toUsername == user.GetUsername().ToLower() {
		c.JSON(400, gin.H{"error": "Cannot send credits to yourself"})
		return
	}

	err = PerformCreditTransfer(user.GetUsername(), toUsername, nAmount, req.Note)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, gin.H{"message": "Transfer successful", "from": user.GetUsername(), "to": toUsername, "amount": nAmount, "debited": nAmount})
}

func deleteUser(c *gin.Context) {
	user := currentUser(c)

	username := Username(c.Param("username"))
	if !requireField(c, username, "Username is required") {
		return
	}

	usernameLower := username.ToLower()
	if !isHardcodedAdmin(user.GetUsername()) && user.GetUsername().ToLower() != usernameLower {
		c.JSON(403, gin.H{"error": "Insufficient permissions to delete this user"})
		return
	}

	if err := performUserDeletion(username, false, false); err != nil {
		c.JSON(404, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, gin.H{"message": "User deleted successfully"})
}

func performAdminUserAction(c *gin.Context, ban bool, successMsg string) {
	if !authenticateAdmin(c) {
		return
	}

	var req struct {
		Username string `json:"username"`
	}
	if !bindJSON(c, &req) {
		return
	}

	username := Username(req.Username)
	if !requireField(c, username, "Username is required") {
		return
	}

	if err := performUserDeletion(username, true, ban); err != nil {
		c.JSON(404, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, gin.H{"message": successMsg})
}

func deleteUserAdmin(c *gin.Context) {
	performAdminUserAction(c, false, "User deleted successfully")
}

func banUserAdmin(c *gin.Context) {
	performAdminUserAction(c, true, "User banned successfully")
}

func transferCreditsAdmin(c *gin.Context) {
	if !authenticateAdmin(c) {
		return
	}

	toUsername := Username(c.Query("to"))
	amountStr := c.Query("amount")
	fromUsername := Username(c.Query("from"))
	note := c.Query("note")

	amountNum, err := strconv.ParseFloat(amountStr, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	err = PerformCreditTransfer(fromUsername, toUsername, amountNum, note)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "Transfer successful", "from": fromUsername, "to": toUsername, "amount": amountNum, "debited": amountNum})
}

func performUserDeletion(username Username, isAdmin bool, ban bool) error {
	usernameLower := username.ToLower()

	idx := getIdxOfAccountBy("username", string(usernameLower))
	if idx == -1 {
		return fmt.Errorf("user not found")
	}

	logPrefix := "Deleting user"
	if isAdmin {
		logPrefix = "Admin deleting user"
	}
	log.Printf("%s %s", logPrefix, usernameLower)

	usersMutex.RLock()
	uId := users[idx].GetId()
	usersMutex.RUnlock()

	if ban {
		usersMutex.Lock()
		oldUser := users[idx]
		oldId := oldUser.GetId()
		oldKey := oldUser.GetKey()
		// set as banned
		banned := User{
			"username":   username,
			"email":      oldUser.GetEmail(), // so that the same email cant be used by a banned user
			"private":    true,
			"sys.banned": true,
			"sys.id":     string(oldId),
			"sys.index":  oldUser.GetIndex(),
		}
		users[idx] = banned
		idToUserMutex.Lock()
		idToUser[oldId] = banned
		delete(keyToId, oldKey)
		idToUserMutex.Unlock()
		usersMutex.Unlock()
		dropMutexForUser(oldUser)
		saveUser(oldId)
	} else {
		deleteAccountAtIndexFast(idx)
	}

	go broadcastClawEvent("account_deleted", map[string]any{"id": uId})
	if !ban {
		ts := time.Now().UnixMilli()
		recordDeletedAccount(uId, ts)
		go notifyAccountDeleted(uId, ts)
	}

	usersMutex.RLock()
	for i := range users {
		target := &users[i]

		friends := target.GetFriends()
		for i, f := range friends {
			if f == uId {
				friends = append(friends[:i], friends[i+1:]...)
				target.SetFriends(friends)
				break
			}
		}

		requests := target.GetRequests()
		for i, r := range requests {
			if r == uId {
				requests = append(requests[:i], requests[i+1:]...)
				target.SetRequests(requests)
				break
			}
		}

		blocked := target.GetBlocked()
		for i, b := range blocked {
			if b == uId {
				blocked = append(blocked[:i], blocked[i+1:]...)
				target.SetBlocked(blocked)
				break
			}
		}

		target.RemoveNote(usernameLower)
	}
	usersMutex.RUnlock()

	go saveUsers()

	go func(target UserId, username Username) {
		scrubPost := func(p *Post) {
			if p.User == target {
				p.User = "Deleted User"
			}
			for j := range p.Replies {
				if p.Replies[j].User == target {
					p.Replies[j].User = "Deleted User"
				}
			}
			if len(p.Likes) > 0 {
				p.Likes = slices.DeleteFunc(p.Likes, func(u UserId) bool { return u == target })
			}
			if len(p.Viewers) > 0 {
				p.Viewers = slices.DeleteFunc(p.Viewers, func(u UserId) bool { return u == target })
			}
			if p.Poll != nil {
				delete(p.Poll.Votes, target)
			}
		}

		postsMutex.Lock()
		for i := range posts {
			scrubPost(&posts[i])
			if posts[i].OriginalPost != nil {
				scrubPost(posts[i].OriginalPost)
			}
		}
		postsMutex.Unlock()
		go savePosts()

		bookmarksMutex.Lock()
		delete(bookmarks, target)
		bookmarksMutex.Unlock()
		go saveBookmarks()

		scheduledMutex.Lock()
		scheduledPosts = slices.DeleteFunc(scheduledPosts, func(p Post) bool { return p.User == target })
		scheduledMutex.Unlock()
		go saveScheduledPosts()

		eventsHistoryMutex.Lock()
		delete(eventsHistory, target)
		eventsHistoryMutex.Unlock()
		go saveEventsHistory()

		// remove avatar and banner
		deleteUserAvatar(username)

		claimsData := loadDailyClaims()
		if _, ok := claimsData[username]; ok {
			delete(claimsData, username)
			saveDailyClaims(claimsData)
		}

		// Remove user storage
		if target != "" {
			userDir := string("rotur/user_storage/" + target)
			if err := os.RemoveAll(userDir); err != nil {
				log.Printf("Error removing user directory %s: %v", userDir, err)
			}
		}

		// remove file system
		if err := fs.DeleteUserFileSystem(string(username)); err != nil {
			log.Printf("Error deleting user file system: %v", err)
		}

		// Remove user from followers data
		followersMutex.Lock()
		delete(followersData, target)
		for userId, data := range followersData {
			newFollowers := make([]UserId, 0)
			for _, follower := range data.Followers {
				if follower != target {
					newFollowers = append(newFollowers, follower)
				}
			}
			followersData[userId] = FollowerData{
				Followers: newFollowers,
				Username:  data.Username,
				UserId:    data.UserId,
			}
		}
		followersMutex.Unlock()
		saveFollowers()
	}(uId, usernameLower)
	return nil
}

func deleteUserAvatar(username Username) {
	usernameLower := username.ToLower()

	fileTypes := []string{"", ".jpg", ".gif", ".png"}

	for _, fileType := range fileTypes {
		filePath := "rotur/avatars/" + string(usernameLower) + fileType
		if fileExists(filePath) {
			os.Remove(filePath)
		}

		bannerPath := "rotur/banners/" + string(usernameLower) + fileType
		if fileExists(bannerPath) {
			os.Remove(bannerPath)
		}
	}
}

func renameUserAvatar(oldUsername, newUsername Username) {
	usernameLower := string(oldUsername.ToLower())
	newUsernameLower := string(newUsername.ToLower())

	fileTypes := []string{"", ".jpg", ".gif", ".png"}

	for _, fileType := range fileTypes {
		filePath := "rotur/avatars/" + usernameLower + fileType
		if _, err := os.Stat(filePath); err == nil {
			newFilePath := "rotur/avatars/" + newUsernameLower + fileType
			os.Rename(filePath, newFilePath)
		}

		bannerPath := "rotur/banners/" + usernameLower + fileType
		if _, err := os.Stat(bannerPath); err == nil {
			newBannerPath := string("rotur/banners/" + newUsernameLower + fileType)
			os.Rename(bannerPath, newBannerPath)
		}
	}
}

var dailyClaimMutex sync.Mutex

func canClaimDaily(user *User) float64 {
	username := user.GetUsername().ToLower()

	claimsData := loadDailyClaims()

	nextClaimTime, ok := claimsData[username]
	if !ok || nextClaimTime == 0 {
		return 0
	}

	currentTime := float64(time.Now().Unix())

	elapsed := currentTime - nextClaimTime
	if elapsed < 86400 {
		return 86400 - elapsed
	}

	return 0
}

func timeUntilNextClaim(c *gin.Context) {
	user := currentUser(c)

	username := user.GetUsername().ToLower()

	claimsData := loadDailyClaims()

	nextClaimTime, ok := claimsData[username]
	if !ok {
		c.JSON(400, gin.H{"error": "No daily claim found"})
		return
	}

	currentTime := float64(time.Now().Unix())

	elapsed := currentTime - nextClaimTime
	if elapsed < 86400 {
		waitTime := 86400 - elapsed
		c.JSON(200, gin.H{"wait_time": waitTime})
		return
	}

	c.JSON(200, gin.H{"wait_time": 0})
}

func claimDaily(c *gin.Context) {
	user := currentUser(c)

	username := user.GetUsername().ToLower()

	waitTime := canClaimDaily(user)
	if waitTime > 0 {
		c.JSON(429, gin.H{
			"error":      "Daily claim already made",
			"wait_time":  waitTime,
			"wait_hours": strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", waitTime/3600), "0"), "."),
		})
		return
	}

	claimsData := loadDailyClaims()
	currentTime := float64(time.Now().Unix())
	claimsData[username] = currentTime
	saveDailyClaims(claimsData)

	benefits := user.GetSubscriptionBenefits()

	PerformCreditTransfer("rotur", username, float64(benefits.Daily_Credit_Multipler), "Daily claim")

	saveUsers()

	c.JSON(200, gin.H{"message": "Daily claim successful"})
}

// loadDailyClaims loads daily claims data from rotur_daily.json
func loadDailyClaims() map[Username]float64 {
	dailyClaimMutex.Lock()
	defer dailyClaimMutex.Unlock()
	return loadJSONOrDefault(config.DAILY_CLAIMS_FILE_PATH, map[Username]float64{})
}

// saveDailyClaims saves daily claims data to rotur_daily.json
func saveDailyClaims(claimsData map[Username]float64) {
	dailyClaimMutex.Lock()
	defer dailyClaimMutex.Unlock()
	data, err := json.MarshalIndent(claimsData, "", "  ")
	if err != nil {
		return
	}

	atomicWrite(config.DAILY_CLAIMS_FILE_PATH, data, 0644)
}

func acceptTos(c *gin.Context) {
	if c.GetHeader("Origin") != "https://accounts.bilup.org" {
		c.JSON(403, gin.H{"error": "This endpoint is only available on accounts.bilup.org"})
		return
	}

	user := currentUser(c)

	// Accept the TOS by setting a flag in the user data
	user.Set("sys.tos_accepted", true)
	user.Set("sys.tos_time", time.Now().Unix())

	go saveUsers()

	c.JSON(200, gin.H{"message": "Terms of Service accepted"})
}

func tosUpdate(c *gin.Context) {
	if !authenticateAdmin(c) {
		return
	}

	usersMutex.Lock()
	ids := make([]UserId, 0, len(users))
	for i := range users {
		users[i]["sys.tos_accepted"] = false
		ids = append(ids, users[i].GetId())
	}
	usersMutex.Unlock()

	saveUsersBulk(ids)

	c.JSON(200, gin.H{"message": "All users marked as not having accepted the updated Terms of Service"})
}

// Badge API handlers

func getBadges(c *gin.Context) {
	user := currentUser(c)

	usersMutex.RLock()
	defer usersMutex.RUnlock()

	// Find user in users slice to get updated data
	for _, u := range users {
		if u.GetUsername() == user.GetUsername() {
			badgeNames := calculateUserBadges(u)

			c.JSON(200, gin.H{
				"badge_names": badgeNames,
			})
			return
		}
	}

	c.JSON(404, gin.H{"error": "User not found"})
}
