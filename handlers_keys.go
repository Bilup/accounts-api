package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type ErrorResponse struct {
	Error string `json:"error"`
}

type keyCreationResp struct {
	Status       string        `json:"status,omitempty"`
	Key          string        `json:"key,omitempty"`
	Type         string        `json:"type,omitempty"`
	Price        int           `json:"price,omitempty"`
	Subscription *Subscription `json:"subscription,omitempty"`
}

func createKey(c *gin.Context) {
	user := currentUser(c)

	name := c.Query("name")
	if name == "" {
		c.JSON(400, ErrorResponse{Error: "Key name is required"})
		return
	}

	description := c.Query("description")
	priceStr := c.Query("price")
	subscriptionFlag := strings.ToLower(c.Query("subscription")) // "true"/"1" to enable
	frequencyStr := c.Query("frequency")
	period := c.Query("period")

	// Defaults
	price := 0
	if priceStr != "" {
		if p, err := strconv.Atoi(priceStr); err == nil && p >= 0 {
			price = p
		}
	}
	frequency := 1
	if frequencyStr != "" {
		if f, err := strconv.Atoi(frequencyStr); err == nil && f > 0 {
			frequency = f
		}
	}
	if period == "" {
		period = "month"
	}

	max_keys := user.GetSubscriptionBenefits().Max_Keys

	keysMutex.Lock()
	defer keysMutex.Unlock()

	// Check if key name already exists
	userId := user.GetUsername().Id()
	total_keys := 0
	for _, key := range keys {
		if key.Creator == userId {
			total_keys++
		}
	}
	if total_keys >= max_keys {
		c.JSON(400, ErrorResponse{Error: fmt.Sprintf("You can only have up to %d free keys", max_keys)})
		return
	}

	newKey := Key{
		Key:         generateToken(),
		Creator:     userId,
		Users:       make(map[UserId]KeyUserData),
		Name:        name,
		Price:       price,
		Type:        "standard",
		TotalIncome: 0,
	}

	if description != "" {
		newKey.Data = &description
	}

	// Handle subscription creation
	if subscriptionFlag == "true" || subscriptionFlag == "1" {
		sub := &Subscription{
			Active:    true,
			Frequency: frequency,
			Period:    period,
		}
		sub.NextBilling = computeNextBilling(sub).Unix() // store as unix seconds for consistency
		newKey.Type = "subscription"
		newKey.Subscription = sub
	}

	// Add creator to key users
	newKey.Users[user.GetId()] = KeyUserData{
		Time: time.Now().Unix(),
	}

	keys = append(keys, newKey)
	if keyStringToIdx == nil {
		keyStringToIdx = make(map[string]int)
	}
	keyStringToIdx[newKey.Key] = len(keys) - 1

	go saveKeys()

	c.JSON(200, keyCreationResp{
		Status:       "Key created successfully",
		Key:          newKey.Key,
		Type:         newKey.Type,
		Price:        newKey.Price,
		Subscription: newKey.Subscription,
	})
}

func getMyKeys(c *gin.Context) {
	user := currentUser(c)

	userId := user.GetId()

	keysMutex.RLock()
	userKeys := make([]NetKey, 0)
	for _, key := range keys {
		if _, hasAccess := key.Users[userId]; hasAccess {
			if key.Creator != userId {
				key.Data = nil
				key.TotalIncome = 0
			}
			userKeys = append(userKeys, key.ToNet())
		}
	}
	keysMutex.RUnlock()

	c.JSON(200, userKeys)
}

func checkKey(c *gin.Context) {
	username := Username(c.Param("username"))
	userId := username.Id()
	keyToCheck := c.Query("key")

	if !requireField(c, keyToCheck, "Key is required") {
		return
	}

	hasKey := doesUserOwnKey(userId, keyToCheck)

	c.JSON(200, gin.H{
		"owned":    hasKey,
		"username": username,
		"key":      keyToCheck,
	})
}

func revokeKey(c *gin.Context) {
	id := c.Param("id")
	user := currentUser(c)

	targetId := Username(c.Query("user")).Id()
	if !accountExists(targetId) {
		c.JSON(400, gin.H{"error": "Target user not found"})
		return
	}

	keysMutex.Lock()
	defer keysMutex.Unlock()

	for i := range keys {
		if keys[i].Key == id {
			if keys[i].Creator != user.GetId() {
				c.JSON(403, gin.H{"error": "You can only revoke access to keys you created"})
				return
			}
			if targetId == keys[i].Creator {
				c.JSON(400, gin.H{"error": "You cannot revoke access from the key creator"})
				return
			}

			delete(keys[i].Users, targetId)

			go saveKeys()

			c.JSON(200, gin.H{"status": "Key access revoked successfully"})
			return
		}
	}

	c.JSON(404, gin.H{"error": "Key not found"})
}

func deleteKey(c *gin.Context) {
	id := c.Param("id")
	user := currentUser(c)

	keysMutex.Lock()
	defer keysMutex.Unlock()

	for i, key := range keys {
		if key.Key == id {
			if key.Creator != user.GetId() {
				c.JSON(403, ErrorResponse{Error: "You can only delete keys you created"})
				return
			}

			// Remove the key
			keys = append(keys[:i], keys[i+1:]...)
			rebuiltIdx := make(map[string]int, len(keys))
			for j := range keys {
				rebuiltIdx[keys[j].Key] = j
			}
			keyStringToIdx = rebuiltIdx

			go saveKeys()

			c.JSON(200, gin.H{"status": "Key deleted successfully"})
			return
		}
	}

	c.JSON(404, ErrorResponse{Error: "Key not found"})
}

func updateKey(c *gin.Context) {
	id := c.Param("id")
	key := c.Query("key")
	data := c.Query("data")
	user := currentUser(c)
	if key == "" {
		c.JSON(403, ErrorResponse{Error: "update key and data are required"})
		return
	}

	var parsedData any
	if json.Valid([]byte(data)) {
		json.Unmarshal([]byte(data), &parsedData)
	} else {
		parsedData = data
	}

	keysMutex.Lock()
	defer keysMutex.Unlock()

	for i := range keys {
		if keys[i].Key == id {
			if keys[i].Creator != user.GetId() {
				c.JSON(403, ErrorResponse{Error: "You can only update keys you created"})
				return
			}

			keys[i].setKey(key, parsedData)

			go saveKeys()

			c.JSON(200, gin.H{"status": "Key updated successfully"})
			return
		}
	}

	c.JSON(404, ErrorResponse{Error: "Key not found"})
}

func setKeyName(c *gin.Context) {
	id := c.Param("id")
	user := currentUser(c)

	name := c.Query("name")
	if !requireField(c, name, "Name is required") {
		return
	}

	keysMutex.Lock()
	defer keysMutex.Unlock()

	for i := range keys {
		if keys[i].Key == id {
			if keys[i].Creator != user.GetId() {
				c.JSON(403, ErrorResponse{Error: "You can only rename keys you created"})
				return
			}

			keys[i].Name = name

			go saveKeys()

			c.JSON(200, gin.H{"status": "Key name updated successfully"})
			return
		}
	}

	c.JSON(404, ErrorResponse{Error: "Key not found"})
}

func getKey(c *gin.Context) {
	id := c.Param("id")

	keysMutex.RLock()
	defer keysMutex.RUnlock()

	for _, key := range keys {
		if key.Key == id {
			c.JSON(200, key.ToPublic())
			return
		}
	}

	c.JSON(404, ErrorResponse{Error: "Key not found"})
}

func adminAddUserToKey(c *gin.Context) {
	id := c.Param("id")
	user := currentUser(c)

	targetUser := Username(c.Query("user"))
	if targetUser == "" {
		targetUser = Username(c.Query("username"))
	}
	if targetUser == "" {
		c.JSON(400, ErrorResponse{Error: "Target user is required"})
		return
	}
	targetId := targetUser.Id()
	if !accountExists(targetId) {
		c.JSON(400, ErrorResponse{Error: "Target user not found"})
		return
	}

	keysMutex.Lock()
	defer keysMutex.Unlock()

	for i := range keys {
		if keys[i].Key == id {
			if keys[i].Creator != user.GetId() {
				c.JSON(403, ErrorResponse{Error: "You can only add users to your own keys"})
				return
			}
			keys[i].Users[targetId] = KeyUserData{
				Time: time.Now().Unix(),
			}

			go saveKeys()

			c.JSON(200, gin.H{"status": "User added to key successfully"})
			return
		}
	}

	c.JSON(404, ErrorResponse{Error: "Key not found"})
}

func adminRemoveUserFromKey(c *gin.Context) {
	id := c.Param("id")
	user := currentUser(c)

	targetUser := Username(c.Query("user"))
	if targetUser == "" {
		targetUser = Username(c.Query("username"))
	}
	if targetUser == "" {
		c.JSON(400, ErrorResponse{Error: "Target user is required"})
		return
	}
	targetId := targetUser.Id()
	if !accountExists(targetId) {
		c.JSON(400, ErrorResponse{Error: "Target user not found"})
		return
	}

	keysMutex.Lock()
	defer keysMutex.Unlock()

	for i := range keys {
		if keys[i].Key == id {
			if keys[i].Creator != user.GetId() {
				c.JSON(403, ErrorResponse{Error: "You can only remove users from your own keys"})
				return
			}
			delete(keys[i].Users, targetId)

			go saveKeys()

			c.JSON(200, gin.H{"status": "User removed from key successfully"})
			return
		}
	}

	c.JSON(404, ErrorResponse{Error: "Key not found"})
}

func buyKey(c *gin.Context) {
	id := c.Param("id")
	user := currentUser(c)

	userId := user.GetId()
	balance := user.GetCredits()

	// Mutate the key under keysMutex, but do all credit/transaction work after
	// releasing it: applyTransaction -> GetSubscription re-enters keysMutex.
	var (
		boughtKey Key
		found     bool
		errMsg    string
	)
	keysMutex.Lock()
	for i := range keys {
		if keys[i].Key != id {
			continue
		}
		found = true
		switch {
		case keys[i].Price < 0:
			errMsg = "Key is not for sale"
		case hasKeyAccess(keys[i], userId):
			errMsg = "You already have access to this key"
		case balance < float64(keys[i].Price):
			errMsg = "Insufficient balance to buy this key"
		default:
			userData := KeyUserData{
				Time:  time.Now().Unix(),
				Price: keys[i].Price,
			}
			if keys[i].Subscription != nil {
				userData.NextBilling = computeNextBilling(keys[i].Subscription).UnixMilli()
			}
			keys[i].Users[userId] = userData
			keys[i].TotalIncome += keys[i].Price
			boughtKey = keys[i]
		}
		break
	}
	keysMutex.Unlock()

	if !found {
		c.JSON(404, ErrorResponse{Error: "Key not found"})
		return
	}
	if errMsg != "" {
		c.JSON(400, ErrorResponse{Error: errMsg})
		return
	}

	go saveKeys()

	now := time.Now().UnixMilli()
	price := float64(boughtKey.Price)

	user.applyTransaction(user.GetCredits()-price, Transaction{
		Note:      "key purchase",
		User:      userId,
		Amount:    price,
		Type:      "key_buy",
		Timestamp: now,
		KeyName:   boughtKey.Name,
		KeyId:     boughtKey.Key,
	})

	if owner, err := getAccountByUserId(boughtKey.Creator); err == nil && owner.GetId() != userId {
		// 10% tax on purchase
		value := price * 0.9
		owner.applyTransaction(owner.GetCredits()+value, Transaction{
			Note:      "key purchase",
			User:      userId,
			Amount:    price,
			Type:      "key_sale",
			Timestamp: now,
			KeyName:   boughtKey.Name,
			KeyId:     boughtKey.Key,
		})
	}

	if boughtKey.Webhook != nil && len(*boughtKey.Webhook) > 0 {
		username := user.GetUsername()
		_ = sendWebhook(*boughtKey.Webhook, map[string]any{
			"username":  username,      // purchaser
			"key":       boughtKey.Key, // id
			"price":     boughtKey.Price,
			"content":   string(username) + " purchased key " + boughtKey.Key + " for " + strconv.Itoa(boughtKey.Price) + " credits",
			"timestamp": time.Now().Unix(),
		})
	}

	go saveUsers()

	c.JSON(200, gin.H{"message": "Key purchased successfully"})
}

func hasKeyAccess(k Key, userId UserId) bool {
	_, ok := k.Users[userId]
	return ok
}

func cancelKey(c *gin.Context) {
	id := c.Param("id")
	user := currentUser(c)

	userId := user.GetId()

	keysMutex.Lock()
	defer keysMutex.Unlock()

	for i := range keys {
		if keys[i].Key != id {
			continue
		}

		userData, ok := keys[i].Users[userId]
		if !ok {
			c.JSON(404, ErrorResponse{Error: "You don't have this key"})
			return
		}

		// Subscription keys: keep access until next billing date, then remove.
		if keys[i].Subscription != nil {
			if userData.NextBilling == nil {
				c.JSON(400, ErrorResponse{Error: "This subscription has no next_billing date"})
				return
			}

			nextBilling := getInt64OrDefault(userData.NextBilling, 0)

			// We always store cancel_at as unix ms to match other per-user billing fields.
			if nextBilling < 10_000_000_000 {
				// looks like seconds; convert to ms
				nextBilling = nextBilling * 1000
			}

			userData.CancelAt = nextBilling
			keys[i].Users[userId] = userData
			go saveKeys()

			c.JSON(200, gin.H{
				"status":    "Cancellation scheduled",
				"cancel_at": nextBilling,
			})
			return
		}

		// Non-subscription keys: cancel immediately.
		delete(keys[i].Users, userId)
		go saveKeys()
		c.JSON(200, gin.H{"status": "Cancelled"})
		return
	}

	c.JSON(404, gin.H{"error": "Key not found"})
}

func debugSubscriptionsEndpoint(c *gin.Context) {
	user := currentUser(c)

	// Only allow admin users to access debug info
	if !isHardcodedAdmin(user.GetUsername()) {
		c.JSON(403, gin.H{"error": "Admin access required"})
		return
	}

	debugSubscriptions()
	c.JSON(200, gin.H{"message": "Subscription debug info logged to console"})
}

func computeNextBilling(subscription *Subscription) time.Time {
	frequency := subscription.Frequency
	if frequency == 0 {
		frequency = 1
	}
	period := subscription.Period
	if period == "" {
		period = "month"
	}

	currentTime := time.Now()
	var nextBillingTime time.Time
	switch strings.ToLower(period) {
	case "day":
		nextBillingTime = currentTime.AddDate(0, 0, frequency)
	case "week":
		nextBillingTime = currentTime.AddDate(0, 0, frequency*7)
	case "month":
		nextBillingTime = currentTime.AddDate(0, frequency, 0)
	case "year":
		nextBillingTime = currentTime.AddDate(frequency, 0, 0)
	default:
		nextBillingTime = currentTime.AddDate(0, frequency, 0)
	}
	return nextBillingTime
}

func checkSubscriptions() {
	ticker := time.NewTicker(time.Duration(SUBSCRIPTION_CHECK_INTERVAL) * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		log.Println("Checking subscriptions...")

		type keySnapshot struct {
			Key       *Key
			KeyStr    string
			Owner     User
			OwnerUser User
			UsersData map[UserId]KeyUserData
		}
		keysMutex.RLock()
		var snapshots []keySnapshot
		for keyIndex := range keys {
			key := &keys[keyIndex]
			if key.Subscription == nil {
				continue
			}
			owner, err := getAccountByUserId(key.Creator)
			if err != nil {
				continue
			}
			if len(key.Users) == 0 {
				continue
			}
			usersCopy := make(map[UserId]KeyUserData, len(key.Users))
			for uid, ud := range key.Users {
				usersCopy[uid] = ud
			}
			snapshots = append(snapshots, keySnapshot{
				Key:       key,
				KeyStr:    key.Key,
				Owner:     owner,
				UsersData: usersCopy,
			})
		}
		keysMutex.RUnlock()

		subscriptionsProcessed := len(snapshots)
		chargesProcessed := 0
		usersDirty := false

		for _, snap := range snapshots {
			usersToRemove := make([]UserId, 0)
			ownerId := snap.Owner.GetId()

			for userId, userData := range snap.UsersData {
				if userData.NextBilling == nil {
					continue
				}

				// If user scheduled cancellation and we've reached that time, remove them now.
				if userData.CancelAt != nil {
					cancelAt := getInt64OrDefault(userData.CancelAt, 0)
					if cancelAt > 0 {
						if cancelAt < 10_000_000_000 {
							cancelAt = cancelAt * 1000
						}
						if time.Now().UnixMilli() >= cancelAt {
							usersToRemove = append(usersToRemove, userId)
							continue
						}
					}
				}

				username := userId.User().GetUsername()

				if userData.NextBilling == nil {
					log.Printf("Warning: Invalid NextBilling type for user %s in key %s", username, snap.Key.Key)
					continue
				}
				nextBilling := getInt64OrDefault(userData.NextBilling, 0)

				currentTimeMs := time.Now().UnixMilli()
				nextBillingTime := time.Unix(nextBilling/1000, 0)

				log.Printf("User %s in key %s: Next billing %s",
					username, snap.Key.Key,
					nextBillingTime.Format("2006-01-02 15:04:05"))

				if currentTimeMs >= nextBilling {
					if userData.Price != 0 {
						log.Printf("Processing subscription payment for %s for key %s (amount: %.2f)", username, snap.Key.Key, float64(userData.Price))

						purchaser, err := getAccountByUsername(username)
						if err != nil {
							log.Printf("User %s not found for key %s", username, snap.Key.Key)
							usersToRemove = append(usersToRemove, userId)
							continue
						}

						if purchaser.GetId() == ownerId {
							nextBillingTime := computeNextBilling(snap.Key.Subscription)
							userData.NextBilling = nextBillingTime.UnixMilli()
							keysMutex.Lock()
							if idx, ok := keyStringToIdx[snap.KeyStr]; ok && idx < len(keys) && keys[idx].Key == snap.KeyStr {
								keys[idx].Users[userId] = userData
							}
							keysMutex.Unlock()
							continue
						}

						var currencyFloat float64 = purchaser.GetCredits()
						price := float64(userData.Price)
						if currencyFloat < price {
							log.Printf("User %s does not have enough currency for key %s (needed: %.2f, available: %.2f)",
								username, snap.Key.Key, price, currencyFloat)

							// send an event
							go notify("sys.key_lost", map[string]any{
								"username": username,
								"key":      snap.Key.Key,
								"key_name": snap.Key.Name,
							})
							usersToRemove = append(usersToRemove, userId)
							continue
						}
						currencyFloat -= price
						purchaser.applyTransaction(currencyFloat, Transaction{
							Note:      "key purchase",
							User:      snap.Key.Creator,
							Amount:    price,
							Type:      "key_buy",
							Timestamp: time.Now().UnixMilli(),
							KeyName:   snap.Key.Name,
							KeyId:     snap.Key.Key,
						})

						// 10% tax on purchase
						value := price * 0.9
						newBal := snap.Owner.GetCredits() + value
						snap.Owner.applyTransaction(newBal, Transaction{
							Note:      "key purchase",
							User:      username.Id(),
							Amount:    value,
							Type:      "key_sale",
							Timestamp: time.Now().UnixMilli(),
							KeyName:   snap.Key.Name,
							KeyId:     snap.Key.Key,
						})
						usersDirty = true

						nextBillingTime := computeNextBilling(snap.Key.Subscription)
						newNextBilling := nextBillingTime.UnixMilli()
						userData.NextBilling = newNextBilling
						keysMutex.Lock()
						if idx, ok := keyStringToIdx[snap.KeyStr]; ok && idx < len(keys) && keys[idx].Key == snap.KeyStr {
							keys[idx].TotalIncome += userData.Price
							keys[idx].Users[userId] = userData
						}
						keysMutex.Unlock()

						if snap.Key.Webhook != nil && len(*snap.Key.Webhook) > 0 {
							_ = sendWebhook(*snap.Key.Webhook, map[string]any{
								"username":  username,     // purchaser
								"key":       snap.Key.Key, // id
								"price":     snap.Key.Price,
								"content":   string(username) + " was charged by key: " + snap.Key.Key + " for " + strconv.Itoa(snap.Key.Price) + " credits",
								"timestamp": time.Now().Unix(),
							})
						}

						log.Printf("Successfully billed user %s for key %s. Next billing: %s",
							username, snap.Key.Key, nextBillingTime.Format("2006-01-02 15:04:05"))
						chargesProcessed++
					}
				}
			}

			if len(usersToRemove) > 0 {
				keysMutex.Lock()
				if idx, ok := keyStringToIdx[snap.KeyStr]; ok && idx < len(keys) && keys[idx].Key == snap.KeyStr {
					for _, username := range usersToRemove {
						delete(keys[idx].Users, username)
						log.Printf("Removed user %s from key %s due to payment failure", username, snap.Key.Key)
					}
				}
				keysMutex.Unlock()
			}
		}

		if usersDirty {
			go saveUsers()
		}
		processGroupProductSubscriptions()

		log.Printf("Subscription check completed: %d keys with subscriptions checked, %d charges processed", subscriptionsProcessed, chargesProcessed)

		saveKeys()
	}
}

func debugSubscriptions() {
	keysMutex.RLock()
	defer keysMutex.RUnlock()

	log.Println("=== SUBSCRIPTION DEBUG INFO ===")
	subscriptionCount := 0

	for _, key := range keys {
		if key.Subscription == nil {
			continue
		}

		subscriptionCount++
		log.Printf("Key: %s (Creator: %s)", key.Key, key.Creator)
		log.Printf("  Subscription Period: %s, Frequency: %d", key.Subscription.Period, key.Subscription.Frequency)

		for username, userData := range key.Users {
			if userData.NextBilling != nil {
				nextBilling := getInt64OrDefault(userData.NextBilling, 0)

				nextBillingTime := time.Unix(nextBilling/1000, 0)
				timeUntilBilling := time.Until(nextBillingTime)

				log.Printf("  User %s: Price %.2f, Next billing %s (in %s)",
					username, float64(userData.Price),
					nextBillingTime.Format("2006-01-02 15:04:05"),
					timeUntilBilling.String())
			} else {
				log.Printf("  User %s: No NextBilling set", username)
			}
		}
	}

	log.Printf("Total subscriptions: %d", subscriptionCount)
	log.Println("=== END SUBSCRIPTION DEBUG ===")
}
