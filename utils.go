package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// Helper functions
func generateToken() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

func generateShortToken() string {
	bytes := make([]byte, 8)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

func roundVal(val float64) float64 {
	return math.Round(val*100) / 100
}

func getStringOrEmpty(val any) string {
	return getStringOrDefault(val, "")
}

func getStringOrDefault(val any, defaultVal string) string {
	if val == nil {
		return defaultVal
	}
	if s, ok := val.(string); ok {
		return s
	}
	return defaultVal
}

func getIntOrDefault(val any, defaultVal int) int {
	if val == nil {
		return defaultVal
	}

	switch v := val.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}

	return defaultVal
}

func getFloatOrDefault(val any, defaultVal float64) float64 {
	if val == nil {
		return defaultVal
	}
	switch val := val.(type) {
	case float64:
		return val
	case float32:
		return float64(val)
	case int:
		return float64(val)
	case int64:
		return float64(val)
	case json.Number:
		f, _ := val.Float64()
		return f
	default:
		return defaultVal
	}
}

func requireTier(tier string) gin.HandlerFunc {
	return func(c *gin.Context) {
		user := c.MustGet("user").(*User)
		user_tier := user.GetSubscription().Tier
		if hasTierOrHigher(user_tier, tier) {
			c.Next()
			return
		}
		c.JSON(403, gin.H{"error": "You need a higher subscription tier to access this endpoint"})
		c.Abort()
	}
}

func tierRank(tier string) int {
	switch strings.ToLower(tier) {
	case "lite":
		return 1
	case "plus":
		return 2
	case "drive", "pro":
		return 3
	case "max":
		return 4
	default:
		return 0
	}
}

func hasTierOrHigher(tier, required string) bool {
	return tierRank(tier) >= tierRank(required)
}

func requireStanding(minLevel StandingLevel) gin.HandlerFunc {
	return func(c *gin.Context) {
		user := c.MustGet("user").(*User)
		if user.HasStandingOrHigher(minLevel) {
			c.Next()
			return
		}
		current := user.GetStanding()
		c.JSON(403, gin.H{"error": fmt.Sprintf("Your account standing does not allow this action. Current: %s", current)})
		c.Abort()
	}
}

func normalizeEscrowAmount(raw float64) (float64, bool) {
	amt := roundVal(raw)
	if amt < 0.01 {
		return 0, false
	}
	return amt, true
}

func loadBannedWords() {
	words, err := loadBannedWordsLocal()
	if err != nil {
		log.Printf("Error loading banned words: %v", err)
	} else {
		log.Printf("Loaded %d banned words", len(words))
	}
}

var bannedWordPatterns []*regexp.Regexp
var bannedWordPatternsOnce sync.Once

func compileBannedWordPatterns() {
	bannedWordPatternsOnce.Do(func() {
		words, err := loadBannedWordsLocal()
		if err != nil {
			return
		}
		bannedWordPatterns = make([]*regexp.Regexp, 0, len(words))
		for _, term := range words {
			pattern := `\b` + regexp.QuoteMeta(strings.ToLower(term)) + `\b`
			if re, err := regexp.Compile(pattern); err == nil {
				bannedWordPatterns = append(bannedWordPatterns, re)
			}
		}
	})
}

func containsDerogatory(text string) bool {
	if text == "" {
		return false
	}
	compileBannedWordPatterns()
	textLower := strings.ToLower(text)
	for _, re := range bannedWordPatterns {
		if re.MatchString(textLower) {
			return true
		}
	}
	return false
}

func accountExists(userId UserId) bool {
	idToUserMutex.RLock()
	defer idToUserMutex.RUnlock()

	_, ok := idToUser[userId]
	return ok
}

func isUserBlockedBy(user User, userId UserId) bool {
	blocked := user.GetBlocked()
	for _, blockedId := range blocked {
		if blockedId == userId {
			return true
		}
	}
	return false
}

func isFromBannedDomain(url string) bool {
	if url == "" {
		return false
	}

	urlLower := strings.ToLower(url)
	for _, domain := range bannedDomains {
		if strings.Contains(urlLower, domain) {
			return true
		}
	}
	return false
}

func isValidMimeType(url string, allowedTypes []string) bool {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Head(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	contentType := resp.Header.Get("Content-Type")
	return slices.Contains(allowedTypes, contentType)
}

// Rate limiting functions
func applyRateLimit(key string, limitType string) (bool, int, float64) {
	rateLimitMutex.Lock()
	defer rateLimitMutex.Unlock()

	currentTime := time.Now().Unix()
	limits, exists := rateLimits[limitType]
	if !exists {
		limits = rateLimits["default"]
	}

	compositeKey := limitType + ":" + key

	rateLimit, exists := rateLimitStorage[compositeKey]
	if !exists || currentTime > rateLimit.ResetAt {
		rateLimitStorage[compositeKey] = &RateLimit{
			Count:   0,
			ResetAt: currentTime + int64(limits.Period),
		}
		rateLimit = rateLimitStorage[compositeKey]
	}

	rateLimit.Count++

	isAllowed := rateLimit.Count <= limits.Count
	remaining := max(limits.Count-rateLimit.Count, 0)

	// If rate limit exceeded, add 10 seconds penalty
	// Fuck scrapers and bots ngl
	if !isAllowed {
		rateLimit.ResetAt += 10
	}

	resetTime := float64(rateLimit.ResetAt)

	return isAllowed, remaining, resetTime
}

func getRateLimitKey(c *gin.Context) string {
	authKey := c.Query("auth")
	if authKey != "" {
		return authKey
	}

	clientIP := c.ClientIP()
	if clientIP != "" {
		return clientIP
	}

	return "unknown_client"
}

func cleanRateLimitStorage() {
	for {
		time.Sleep(5 * time.Minute)
		currentTime := time.Now().Unix()

		rateLimitMutex.Lock()
		keysToRemove := make([]string, 0)

		for key, data := range rateLimitStorage {
			if currentTime > data.ResetAt {
				keysToRemove = append(keysToRemove, key)
			}
		}

		for _, key := range keysToRemove {
			delete(rateLimitStorage, key)
		}

		rateLimitMutex.Unlock()
	}
}

func getUserByIdx(idx int) (*User, error) {
	usersMutex.RLock()
	defer usersMutex.RUnlock()
	if idx < 0 || len(users) <= idx {
		return nil, fmt.Errorf("index out of bounds")
	}
	user := &users[idx]
	return user, nil
}

func rateLimit(limitType string) gin.HandlerFunc {
	return func(c *gin.Context) {
		rateLimitKey := getRateLimitKey(c)
		isAllowed, remaining, resetTime := applyRateLimit(rateLimitKey, limitType)

		if !isAllowed {
			c.Header("X-RateLimit-Limit", strconv.Itoa(rateLimits[limitType].Count))
			c.Header("X-RateLimit-Remaining", strconv.Itoa(remaining))
			c.Header("X-RateLimit-Reset", strconv.FormatFloat(resetTime, 'f', 0, 64))
			c.JSON(429, gin.H{"error": "Rate limit exceeded. Rate limit extended by 10 seconds due to violation.", "reset_time": resetTime, "remaining": remaining})
			c.Abort()
			return
		}

		c.Next()
	}
}

func requiresAuth(c *gin.Context) {
	authKey := c.Query("auth")
	if authKey == "" {
		authHeader := c.GetHeader("Authorization")
		if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
			authKey = authHeader[7:]
		} else if authHeader != "" {
			authKey = authHeader
		}
	}
	if authKey == "" {
		c.JSON(403, gin.H{"error": "auth key is required"})
		c.Abort()
		return
	}

	user := authenticateWithKey(authKey)
	if user != nil {
		if user.IsBanned() {
			c.JSON(403, gin.H{"error": "User is banned"})
			c.Abort()
			return
		}
		user.GetSubscription()
		c.Set("user", user)
		c.Set("token_type", "main")
		c.Next()
		return
	}

	subUser, subToken, err := authenticateWithSubTokenFast(authKey)
	if err != nil || subUser == nil {
		c.JSON(403, gin.H{"error": "Invalid authentication key"})
		c.Abort()
		return
	}
	if subUser.IsBanned() {
		c.JSON(403, gin.H{"error": "User is banned"})
		c.Abort()
		return
	}
	subUser.GetSubscription()
	c.Set("user", subUser)
	c.Set("token_type", "sub")
	c.Set("sub_token", subToken)
	c.Next()
}

func requirePermission(perm TokenPermission) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenType, exists := c.Get("token_type")
		if !exists {
			c.JSON(403, gin.H{"error": "Authentication required"})
			c.Abort()
			return
		}

		if tokenType == "main" {
			c.Next()
			return
		}

		subTokenVal, exists := c.Get("sub_token")
		if !exists {
			c.JSON(403, gin.H{"error": "Invalid token context"})
			c.Abort()
			return
		}

		subToken, ok := subTokenVal.(*SubToken)
		if !ok {
			c.JSON(403, gin.H{"error": "Invalid token context"})
			c.Abort()
			return
		}

		if !subToken.hasPermission(perm) {
			c.JSON(403, gin.H{"error": fmt.Sprintf("Token lacks permission: %s", string(perm))})
			c.Abort()
			return
		}

		c.Next()
	}
}

func requireMainToken() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenType, exists := c.Get("token_type")
		if !exists || tokenType != "main" {
			c.JSON(403, gin.H{"error": "This action requires the main account token"})
			c.Abort()
			return
		}
		c.Next()
	}
}

var bannedIPsCache struct {
	sync.RWMutex
	ips   []string
	mtime time.Time
}

const bannedIPsPath = "./rotur/banned.json"

func getBannedIPs() []string {
	now := time.Now()
	bannedIPsCache.RLock()
	if now.Sub(bannedIPsCache.mtime) < 5*time.Minute {
		ips := bannedIPsCache.ips
		bannedIPsCache.RUnlock()
		return ips
	}
	bannedIPsCache.RUnlock()

	file, err := os.Open(bannedIPsPath)
	if err != nil {
		return []string{}
	}
	defer file.Close()

	var data struct {
		IPs []string `json:"ips"`
	}

	if err := json.NewDecoder(file).Decode(&data); err != nil {
		return []string{}
	}

	bannedIPsCache.Lock()
	bannedIPsCache.ips = data.IPs
	bannedIPsCache.mtime = now
	bannedIPsCache.Unlock()

	return data.IPs
}

func isBannedIp(ip string) bool {
	bannedIPs := getBannedIPs()
	for _, bannedIP := range bannedIPs {
		if bannedIP == ip {
			return true
		}
		if len(bannedIP) > 4 && bannedIP[4] == ':' && strings.HasPrefix(ip, bannedIP) {
			return true
		}
	}
	return false
}

func corsMiddleware() gin.HandlerFunc {
	config := cors.DefaultConfig()
	config.AllowAllOrigins = true
	config.AllowMethods = []string{"GET", "POST", "PATCH", "DELETE", "PUT", "OPTIONS"}
	config.AllowHeaders = []string{"Content-Type", "Authorization"}
	return cors.New(config)
}

func JSONStringify(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf(`%v`, v)
	}
	return string(data)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return !os.IsNotExist(err) && info.Mode().IsRegular()
}

func copyAndReplace(src, dst, old, new string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	updated := strings.ReplaceAll(string(data), old, new)

	return os.WriteFile(dst, []byte(updated), 0644)
}

func removeUserPath(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // already gone, not an error
		}
		return err
	}

	if info.IsDir() {
		return os.RemoveAll(path)
	}

	return os.Remove(path)
}

func sendWebhook(url string, data map[string]any) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 204 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status code: %d (%s)", resp.StatusCode, string(body))
	}
	return nil
}

func hmacIp(ip string) string {
	mac := hmac.New(sha256.New, []byte(os.Getenv("HMAC_KEY")))
	mac.Write([]byte(ip))
	return hex.EncodeToString(mac.Sum(nil))
}

func sendDiscordWebhook(data []map[string]any) {
	webhook := os.Getenv("ACCOUNT_CREATION_WEBHOOK")

	if webhook == "" {
		log.Println("No webhook configured, not sending Discord webhook")
		return
	}
	body := map[string]any{
		"embeds": data,
	}

	go func() {
		if err := sendWebhook(webhook, body); err != nil {
			log.Println("Failed to send account creation webhook:", err)
		}
	}()
}

func clamp(num int, low int, high int) int {
	if num > high {
		return high
	}
	if num < low {
		return low
	}
	return num
}

func deleteAccountAtIndexFast(idx int) error {
	usersMutex.Lock()
	defer usersMutex.Unlock()

	if idx < 0 || idx >= len(users) {
		return fmt.Errorf("index out of range")
	}

	removedUser := users[idx]
	removedUserId := removedUser.GetId()
	removedUsername := removedUser.GetUsername().ToLower()
	removedKey := removedUser.GetKey()

	users[idx] = users[len(users)-1]
	users = users[:len(users)-1]

	idToUserMutex.Lock()
	delete(idToUser, removedUserId)
	delete(usernameToId, removedUsername)
	delete(keyToUserIdx, removedKey)
	idToUserMutex.Unlock()

	go saveUsers()
	return nil
}

func loadGifts() {
	file, err := os.Open("gifts.json")
	if err != nil {
		if os.IsNotExist(err) {
			gifts = []Gift{}
			return
		}
		log.Printf("Error opening gifts.json: %v", err)
		gifts = []Gift{}
		return
	}
	defer file.Close()

	var loaded []Gift
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&loaded); err != nil {
		log.Printf("Error decoding gifts.json: %v", err)
		gifts = []Gift{}
		return
	}

	gifts = loaded
	log.Printf("Loaded %d gifts", len(gifts))
}

func saveGifts() {
	giftsMutex.RLock()
	defer giftsMutex.RUnlock()
	saveJsonFile("gifts.json", gifts)
}

func cleanExpiredGifts() {
	for {
		time.Sleep(1 * time.Hour)
		giftsMutex.RLock()
		var expiredGifts []Gift
		for i := range gifts {
			gift := &gifts[i]
			if gift.IsActive() && gift.IsExpired() {
				expiredGifts = append(expiredGifts, *gift)
			}
		}
		giftsMutex.RUnlock()

		if len(expiredGifts) == 0 {
			continue
		}

		changed := false
		now := time.Now().UnixMilli()
		for _, gift := range expiredGifts {
			creator := getUserById(gift.CreatorId)
			if len(creator) > 0 {
				newBal := roundVal(creator.GetCredits() + gift.Amount)
				creator.SetBalance(newBal)
				creator.addTransaction(Transaction{
					Note:      "Gift expired: " + gift.Code,
					User:      UserId(""),
					Amount:    gift.Amount,
					Type:      "gift_refund",
					Timestamp: now,
					NewTotal:  newBal,
					GiftId:    gift.Id,
					GiftCode:  gift.Code,
				})
			}
			changed = true
		}

		if changed {
			giftsMutex.Lock()
			for i := range gifts {
				gift := &gifts[i]
				if gift.IsActive() && gift.IsExpired() {
					cancelledAt := now
					gift.CancelledAt = &cancelledAt
				}
			}
			giftsMutex.Unlock()
			go saveGifts()
			go saveUsers()
		}
	}
}
func generateGiftCode() string {
	return generateShortToken() + generateShortToken()
}

func getGiftByCode(code string) (*Gift, bool) {
	giftsMutex.RLock()
	defer giftsMutex.RUnlock()

	for i := range gifts {
		if gifts[i].Code == code {
			return &gifts[i], true
		}
	}
	return nil, false
}

func getGiftById(id string) (*Gift, bool) {
	giftsMutex.RLock()
	defer giftsMutex.RUnlock()

	for i := range gifts {
		if gifts[i].Id == id {
			return &gifts[i], true
		}
	}
	return nil, false
}

func getGiftsByCreator(creatorId UserId) []Gift {
	giftsMutex.RLock()
	defer giftsMutex.RUnlock()

	result := make([]Gift, 0)
	for _, gift := range gifts {
		if gift.CreatorId == creatorId {
			result = append(result, gift)
		}
	}
	return result
}

func loadCosmeticGifts() {
	if _, err := os.Stat("cosmetic_gifts.json"); os.IsNotExist(err) {
		cosmeticGifts = []CosmeticGift{}
		return
	}
	data, err := os.ReadFile("cosmetic_gifts.json")
	if err != nil {
		log.Printf("Error reading cosmetic_gifts.json: %v", err)
		cosmeticGifts = []CosmeticGift{}
		return
	}
	if err := json.Unmarshal(data, &cosmeticGifts); err != nil {
		log.Printf("Error unmarshaling cosmetic_gifts.json: %v", err)
		cosmeticGifts = []CosmeticGift{}
		return
	}
	log.Printf("Loaded %d cosmetic gifts", len(cosmeticGifts))
}

func saveCosmeticGifts() {
	cosmeticGiftsMutex.RLock()
	defer cosmeticGiftsMutex.RUnlock()
	saveJsonFile("cosmetic_gifts.json", cosmeticGifts)
}

func getCosmeticGiftsByRecipient(userId UserId) []CosmeticGift {
	cosmeticGiftsMutex.RLock()
	defer cosmeticGiftsMutex.RUnlock()
	result := make([]CosmeticGift, 0)
	for _, g := range cosmeticGifts {
		if g.ToUserId == userId {
			result = append(result, g)
		}
	}
	return result
}

func getCosmeticGiftsBySender(userId UserId) []CosmeticGift {
	cosmeticGiftsMutex.RLock()
	defer cosmeticGiftsMutex.RUnlock()
	result := make([]CosmeticGift, 0)
	for _, g := range cosmeticGifts {
		if g.FromUserId == userId {
			result = append(result, g)
		}
	}
	return result
}
