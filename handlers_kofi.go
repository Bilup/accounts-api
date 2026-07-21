package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/gin-gonic/gin"
)

// webhook that kofi sends to us when transactions are made.
func handleKofiTransaction(c *gin.Context) {
	data := c.PostForm("data")
	if !requireField(c, data, "No data found") {
		return
	}

	parsedData := make(map[string]any)
	err := json.Unmarshal([]byte(data), &parsedData)
	if err != nil {
		c.JSON(400, gin.H{"error": "Invalid data format"})
		return
	}
	verification := parsedData["verification_token"]
	kofiCode := os.Getenv("KOFI_CODE")
	if kofiCode == "" || subtle.ConstantTimeCompare([]byte(getStringOrEmpty(verification)), []byte(kofiCode)) != 1 {
		c.JSON(400, gin.H{"error": "Invalid verification code"})
		return
	}
	fmt.Println(JSONStringify(parsedData))

	email := getStringOrEmpty(parsedData["email"])

	foundBy := "None"
	accounts := []User{}
	var account User = nil

	discord_id := getStringOrEmpty(parsedData["discord_id"])
	if discord_id != "" {
		accounts, err = getAccountsBy("discord_id", discord_id, -1)
		if err == nil {
			foundBy = "Discord"
		}
	}
	if email != "" && len(accounts) == 0 {
		accounts, err = getAccountsBy("email", email, -1)
		if err == nil {
			foundBy = "Email"
		}
	}
	accountInfo := "No linked account found"
	if len(accounts) > 0 {
		account = accounts[0]
		accountInfo = fmt.Sprintf("**Username:** %s", account.GetUsername())
	}

	switch getStringOrEmpty(parsedData["type"]) {
	case "Donation":
		// TODO: handle donations
	case "Subscription":
		name := getStringOrEmpty(parsedData["tier_name"])
		if name == "" {
			// this exits if its a monthly donation, subscriptions have teir_name
			c.JSON(400, gin.H{"error": "No tier name found"})
			return
		}

		sendDiscordWebhook([]map[string]any{
			{
				"title": "New Subscription",
				"description": fmt.Sprintf(
					"**From:** %s\n**Amount:** %s %s\n**Message:** %s\n**Email:** %s\n\n[View on Ko-fi](%s)\n**Found By:** %s\n\n%s",
					parsedData["from_name"],
					parsedData["amount"],
					parsedData["currency"],
					parsedData["message"],
					parsedData["email"],
					parsedData["url"],
					foundBy,
					accountInfo,
				),
				"timestamp": time.Now().Format(time.RFC3339),
			},
		})

		if len(accounts) == 0 {
			c.JSON(400, gin.H{"error": fmt.Sprintf("No accounts found for %s", foundBy)})
			return
		}
		account := accounts[0]
		sub := account.GetSubscription()
		sub.Tier = name
		sub.Active = true
		sub.Next_billing = int64(time.Now().Add(time.Hour * 24 * 31).UnixMilli())
		account.SetSubscription(sub)
		go saveUsers()

		c.JSON(200, gin.H{"status": "success"})
	case "Shop Order":
		shopItems, ok := parsedData["shop_items"].([]any)
		if !ok {
			c.JSON(400, gin.H{"error": "Invalid shop_items format"})
			return
		}
		for _, shop_item := range shopItems {
			item, ok := shop_item.(map[string]any)
			if !ok {
				continue
			}
			sendDiscordWebhook([]map[string]any{
				{
					"title": "New Shop Order",
					"description": fmt.Sprintf("**User:** %s\n**Amount:** %s %s\n**Message:** %s\n**Email:** %s\n\n[View on Ko-fi](%s)\n**Found By:** %s\n\n%s",
						parsedData["from_name"],
						parsedData["amount"],
						parsedData["currency"],
						parsedData["message"],
						parsedData["email"],
						parsedData["url"],
						foundBy,
						accountInfo,
					),
					"timestamp": time.Now().Format(time.RFC3339),
				},
			})

			addCredits := func(credits int) {
				now := time.Now().UnixMilli()
				balance := float64(account.GetCredits()) + float64(credits)
				account.applyTransaction(balance, Transaction{
					Note:      fmt.Sprintf("%d credit purchase", credits),
					User:      getIdByUsername(Username("rotur")),
					Timestamp: now,
					Amount:    float64(credits),
					Type:      "transfer",
				})
				go saveUsers()
			}
			if account == nil {
				continue
			}
			code, _ := item["direct_link_code"].(string)
			switch code {
			case "eebeb7269f":
				addCredits(50)
			case "0638cbfd65":
				addCredits(250)
			case "e0ce188c8f":
				addCredits(500)
			}
		}
	}

	c.JSON(200, gin.H{"status": "success"})
}

func setSubscription(c *gin.Context) {
	if !authenticateAdmin(c) {
		return
	}

	var data map[string]any
	if !bindJSON(c, &data) {
		return
	}

	usernameStr, _ := data["username"].(string)
	tier, _ := data["tier"].(string)
	username := Username(usernameStr)

	if !requireFields(c, "Username and tier are required", usernameStr, tier) {
		return
	}

	days := 31
	if rawDays, ok := data["days"]; ok {
		if d, ok := rawDays.(float64); ok && d > 0 {
			days = int(d)
		}
	}

	user, err := getAccountByUsername(username)
	if err != nil {
		c.JSON(404, gin.H{"error": "User not found"})
		return
	}
	nextBilling := int64(time.Now().Add(time.Hour * 24 * time.Duration(days)).UnixMilli())
	user.SetSubscription(subscription{
		Tier:         tier,
		Active:       true,
		Next_billing: nextBilling,
	})
	go saveUsers()

	keyUpdated := setKeyNextBilling(user.GetId(), LITE_SUBSCRIPTION_KEY, nextBilling)

	c.JSON(200, gin.H{"message": "Subscription updated successfully", "days": days, "key_updated": keyUpdated})
}

func getUserSubscriptionBenefits(c *gin.Context) {
	user := currentUser(c)

	benefits := user.GetSubscriptionBenefits()
	sub := user.GetSubscription()

	c.JSON(200, gin.H{
		"benefits":     benefits,
		"subscription": sub,
	})
}
