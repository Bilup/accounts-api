package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	GiftTaxPercent      = 0.01
	GiftMaxExpiryDays   = 90
	GiftMaxExpiryHours  = GiftMaxExpiryDays * 24
	GiftMaxExpiryMillis = int64(GiftMaxExpiryHours) * 60 * 60 * 1000
)

func createGift(c *gin.Context) {
	user := currentUser(c)

	var req struct {
		Amount       float64 `json:"amount"`
		Note         string  `json:"note"`
		ExpiresInHrs int     `json:"expires_in_hrs"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request payload"})
		return
	}

	nAmount, ok := normalizeEscrowAmount(req.Amount)
	if !ok {
		c.JSON(400, gin.H{"error": "Minimum amount is 0.01"})
		return
	}

	if nAmount <= 0 {
		c.JSON(400, gin.H{"error": "Amount must be greater than 0"})
		return
	}

	taxAmount := roundVal(nAmount * GiftTaxPercent)
	totalDeduction := roundVal(nAmount + taxAmount)

	userCredits := user.GetCredits()
	if userCredits < totalDeduction {
		c.JSON(400, gin.H{"error": "Insufficient funds", "required": totalDeduction, "available": userCredits})
		return
	}

	note := trimAndCapNote(req.Note, 50)

	var expiresAt int64 = 0
	if req.ExpiresInHrs > 0 {
		if req.ExpiresInHrs > GiftMaxExpiryHours {
			c.JSON(400, gin.H{"error": "Maximum expiration is 90 days", "max_hours": GiftMaxExpiryHours})
			return
		}
		expiresAt = time.Now().UnixMilli() + int64(req.ExpiresInHrs)*60*60*1000
	}

	giftId := generateToken()
	giftCode := generateGiftCode()

	now := time.Now().UnixMilli()

	gift := Gift{
		Id:        giftId,
		Code:      giftCode,
		Amount:    nAmount,
		Note:      note,
		CreatorId: user.GetId(),
		CreatedAt: now,
		ExpiresAt: expiresAt,
	}

	newBal := roundVal(userCredits - totalDeduction)
	user.applyTransaction(newBal, Transaction{
		Note:      note,
		User:      UserId(""),
		Amount:    totalDeduction,
		Type:      "gift_create",
		Timestamp: now,
		GiftId:    giftId,
		GiftCode:  giftCode,
	})

	giftsMutex.Lock()
	gifts = append(gifts, gift)
	giftsMutex.Unlock()

	go saveGifts()
	go saveUsers()

	c.JSON(200, gin.H{
		"message":    "Gift created successfully",
		"id":         giftId,
		"code":       giftCode,
		"amount":     nAmount,
		"tax":        taxAmount,
		"total_paid": totalDeduction,
		"expires_at": expiresAt,
		"claim_url":  "https://accounts.bilup.org/gift/" + giftCode,
	})
}

func getGift(c *gin.Context) {
	code := c.Param("code")
	if !requireField(c, code, "Gift code is required") {
		return
	}

	gift, exists := getGiftByCode(code)
	if !exists {
		c.JSON(404, gin.H{"error": "Gift not found"})
		return
	}

	if !gift.CanBeClaimed() {
		if gift.ClaimedAt != nil {
			c.JSON(410, gin.H{"error": "This gift has already been claimed"})
			return
		}
		if gift.CancelledAt != nil {
			c.JSON(410, gin.H{"error": "This gift has been cancelled"})
			return
		}
		if gift.IsExpired() {
			c.JSON(410, gin.H{"error": "This gift has expired"})
			return
		}
	}

	c.JSON(200, gin.H{"gift": gift.ToPublic()})
}

func claimGift(c *gin.Context) {
	user := currentUser(c)

	code := c.Param("code")
	if !requireField(c, code, "Gift code is required") {
		return
	}

	giftsMutex.RLock()
	giftIdx := -1
	for i := range gifts {
		if gifts[i].Code == code {
			giftIdx = i
			break
		}
	}
	if giftIdx == -1 {
		giftsMutex.RUnlock()
		c.JSON(404, gin.H{"error": "Gift not found"})
		return
	}

	gift := gifts[giftIdx]
	giftsMutex.RUnlock()

	userId := user.GetId()
	if gift.CreatorId == userId {
		c.JSON(400, gin.H{"error": "You cannot claim your own gift"})
		return
	}

	now := time.Now().UnixMilli()
	claimedBy := userId

	giftsMutex.Lock()
	g := &gifts[giftIdx]
	if !g.CanBeClaimed() {
		giftsMutex.Unlock()
		switch {
		case g.ClaimedAt != nil:
			c.JSON(400, gin.H{"error": "This gift has already been claimed"})
		case g.CancelledAt != nil:
			c.JSON(400, gin.H{"error": "This gift has been cancelled"})
		default:
			c.JSON(400, gin.H{"error": "This gift has expired"})
		}
		return
	}
	g.ClaimedAt = &now
	g.ClaimedBy = &claimedBy
	giftsMutex.Unlock()

	// 订阅类型礼包码：发放订阅而非积分
	if gift.IsSubscription() {
		durationMs := gift.DurationMs
		if durationMs <= 0 {
			durationMs = int64(31 * 24 * 60 * 60 * 1000) // 默认31天
		}
		nextBilling := time.Now().UnixMilli() + durationMs
		applySubscriptionToUser(*user, gift.Tier, nextBilling)

		go saveGifts()
		go saveUsers()

		c.JSON(200, gin.H{
			"message":      "Subscription claimed successfully",
			"tier":         gift.Tier,
			"duration_ms":  durationMs,
			"next_billing": nextBilling,
		})
		return
	}

	userCredits := user.GetCredits()
	newBal := roundVal(userCredits + gift.Amount)
	user.applyTransaction(newBal, Transaction{
		Note:      gift.Note,
		User:      gift.CreatorId,
		Amount:    gift.Amount,
		Type:      "gift_claim",
		Timestamp: now,
		GiftId:    gift.Id,
		GiftCode:  gift.Code,
	})

	creator := getUserById(gift.CreatorId)
	if len(creator) > 0 {
		creator.addTransaction(Transaction{
			Note:      "Gift claimed by " + string(user.GetUsername()),
			User:      userId,
			Amount:    gift.Amount,
			Type:      "gift_claimed",
			Timestamp: now,
			NewTotal:  creator.GetCredits(),
			GiftId:    gift.Id,
			GiftCode:  gift.Code,
		})
	}

	go saveGifts()
	go saveUsers()

	c.JSON(200, gin.H{
		"message":     "Gift claimed successfully",
		"amount":      gift.Amount,
		"new_balance": newBal,
	})
}
func cancelGift(c *gin.Context) {
	user := currentUser(c)

	giftId := c.Param("id")
	if !requireField(c, giftId, "Gift ID is required") {
		return
	}

	giftsMutex.RLock()
	giftIdx := -1
	for i := range gifts {
		if gifts[i].Id == giftId {
			giftIdx = i
			break
		}
	}
	if giftIdx == -1 {
		giftsMutex.RUnlock()
		c.JSON(404, gin.H{"error": "Gift not found"})
		return
	}

	gift := gifts[giftIdx]
	giftsMutex.RUnlock()

	userId := user.GetId()
	if gift.CreatorId != userId {
		c.JSON(403, gin.H{"error": "You can only cancel your own gifts"})
		return
	}

	now := time.Now().UnixMilli()

	giftsMutex.Lock()
	g := &gifts[giftIdx]
	if !g.CanBeCancelled() {
		giftsMutex.Unlock()
		switch {
		case g.ClaimedAt != nil:
			c.JSON(400, gin.H{"error": "This gift has already been claimed"})
		case g.CancelledAt != nil:
			c.JSON(400, gin.H{"error": "This gift has already been cancelled"})
		default:
			c.JSON(400, gin.H{"error": "This gift has expired"})
		}
		return
	}
	g.CancelledAt = &now
	giftsMutex.Unlock()

	userCredits := user.GetCredits()
	newBal := roundVal(userCredits + gift.Amount)
	user.applyTransaction(newBal, Transaction{
		Note:      "Gift cancelled: " + gift.Code,
		User:      UserId(""),
		Amount:    gift.Amount,
		Type:      "gift_refund",
		Timestamp: now,
		GiftId:    gift.Id,
		GiftCode:  gift.Code,
	})

	go saveGifts()
	go saveUsers()

	c.JSON(200, gin.H{
		"message":     "Gift cancelled successfully",
		"refunded":    gift.Amount,
		"new_balance": newBal,
	})
}
func getMyGifts(c *gin.Context) {
	user := currentUser(c)

	creatorGifts := getGiftsByCreator(user.GetId())

	netGifts := make([]GiftNet, 0, len(creatorGifts))
	for _, gift := range creatorGifts {
		netGifts = append(netGifts, gift.ToNet())
	}

	c.JSON(200, gin.H{
		"gifts": netGifts,
		"count": len(netGifts),
	})
}

func trimAndCapNote(note string, maxLen int) string {
	note = strings.TrimSpace(note)
	runes := []rune(note)
	if len(runes) > maxLen {
		runes = runes[:maxLen]
	}
	return string(runes)
}

// batchCreateGifts 管理员批量生成礼包码（不扣积分）。
// 支持积分类型和订阅类型，用于配合支付平台自动回复发码。
func batchCreateGifts(c *gin.Context) {
	if !authenticateAdmin(c) {
		return
	}

	var req struct {
		Type         string  `json:"type"`          // "credits" 或 "subscription"
		Amount       float64 `json:"amount"`        // 积分数（credits 类型必填）
		Tier         string  `json:"tier"`          // 订阅等级（subscription 类型必填，如 "Plus"、"Pro"）
		DurationDays int     `json:"duration_days"` // 订阅时长天数（subscription 类型必填）
		Count        int     `json:"count"`         // 生成数量（1-1000）
		Note         string  `json:"note"`          // 备注
		ExpiresInHrs int     `json:"expires_in_hrs"` // 礼包码过期时间（0=永不过期）
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request payload"})
		return
	}

	// 参数校验
	giftType := req.Type
	if giftType == "" {
		giftType = "credits"
	}
	if giftType != "credits" && giftType != "subscription" {
		c.JSON(400, gin.H{"error": "type must be 'credits' or 'subscription'"})
		return
	}
	if req.Count <= 0 {
		req.Count = 1
	}
	if req.Count > 1000 {
		c.JSON(400, gin.H{"error": "Maximum count is 1000"})
		return
	}
	if giftType == "credits" && req.Amount <= 0 {
		c.JSON(400, gin.H{"error": "amount must be greater than 0 for credits type"})
		return
	}
	if giftType == "subscription" {
		if req.Tier == "" {
			c.JSON(400, gin.H{"error": "tier is required for subscription type"})
			return
		}
		if req.DurationDays <= 0 {
			req.DurationDays = 31
		}
	}

	note := trimAndCapNote(req.Note, 50)

	var expiresAt int64 = 0
	if req.ExpiresInHrs > 0 {
		if req.ExpiresInHrs > GiftMaxExpiryHours {
			c.JSON(400, gin.H{"error": "Maximum expiration is 90 days", "max_hours": GiftMaxExpiryHours})
			return
		}
		expiresAt = time.Now().UnixMilli() + int64(req.ExpiresInHrs)*60*60*1000
	}

	now := time.Now().UnixMilli()
	codes := make([]string, 0, req.Count)
	durationMs := int64(req.DurationDays) * 24 * 60 * 60 * 1000

	giftsMutex.Lock()
	for i := 0; i < req.Count; i++ {
		gift := Gift{
			Id:        generateToken(),
			Code:      generateGiftCode(),
			Amount:    req.Amount,
			Note:      note,
			CreatedAt: now,
			ExpiresAt: expiresAt,
		}
		if giftType == "subscription" {
			gift.Type = "subscription"
			gift.Tier = req.Tier
			gift.DurationMs = durationMs
		}
		gifts = append(gifts, gift)
		codes = append(codes, gift.Code)
	}
	giftsMutex.Unlock()

	go saveGifts()

	c.JSON(200, gin.H{
		"message": fmt.Sprintf("Created %d gift codes", req.Count),
		"type":    giftType,
		"count":   len(codes),
		"codes":   codes,
	})
}
