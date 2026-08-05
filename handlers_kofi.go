package main

import (
	"time"

	"github.com/gin-gonic/gin"
)

// applySubscriptionToUser 写入 sys.subscription 并尝试同步 LITE_SUBSCRIPTION_KEY。
// admin 端点和爱发电 webhook 共用此函数。注意：setKeyNextBilling 在 LITE_SUBSCRIPTION_KEY
// 不在 keys 列表或用户未加入该 key 时返回 false，此时仅 sys.subscription 生效。
func applySubscriptionToUser(user User, tier string, nextBilling int64) bool {
	user.SetSubscription(subscription{
		Tier:         tier,
		Active:       true,
		Next_billing: nextBilling,
	})
	keyUpdated := setKeyNextBilling(user.GetId(), LITE_SUBSCRIPTION_KEY, nextBilling)
	go saveUsers()
	return keyUpdated
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
	keyUpdated := applySubscriptionToUser(user, tier, nextBilling)

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
