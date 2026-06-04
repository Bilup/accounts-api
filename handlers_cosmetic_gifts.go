package main

import (
	"slices"
	"time"

	"github.com/gin-gonic/gin"
)

func giftCosmetic(c *gin.Context) {
	user := c.MustGet("user").(*User)
	userId := user.GetId()

	var req struct {
		CosmeticId string `json:"cosmetic_id"`
		To         string `json:"to"`
		Note       string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request payload"})
		return
	}

	// Validate cosmetic id
	cosmeticId, valid := validateCosmeticId(req.CosmeticId)
	if !valid {
		c.JSON(400, gin.H{"error": "Invalid cosmetic id"})
		return
	}

	// Validate recipient
	if req.To == "" {
		c.JSON(400, gin.H{"error": "Recipient username is required"})
		return
	}
	toUser, err := getAccountByUsername(Username(req.To))
	if err != nil || len(toUser) == 0 {
		c.JSON(404, gin.H{"error": "Recipient user not found"})
		return
	}
	toUserId := toUser.GetId()
	if toUserId == userId {
		c.JSON(400, gin.H{"error": "You cannot gift a cosmetic to yourself"})
		return
	}

	// Look up cosmetic
	entry, exists := getCatalogEntryByIdForWrite(cosmeticId)
	if !exists {
		c.JSON(404, gin.H{"error": "Cosmetic not found"})
		return
	}

	if entry.PricingType == CosmeticFree || entry.Price == 0 {
		c.JSON(400, gin.H{"error": "Free cosmetics cannot be gifted - the recipient can claim them directly"})
		return
	}

	if entry.CreatorPct < 0 || entry.CreatorPct > 100 {
		c.JSON(500, gin.H{"error": "Invalid creator percentage in catalog"})
		return
	}

	taxAmount := roundVal(entry.Price * GiftTaxPercent)
	totalDeduction := roundVal(entry.Price + taxAmount)

	userCredits := user.GetCredits()
	if userCredits < totalDeduction {
		c.JSON(400, gin.H{
			"error":     "Insufficient credits",
			"required":  totalDeduction,
			"available": userCredits,
		})
		return
	}

	userCosmeticsMu.Lock()
	defer userCosmeticsMu.Unlock()

	recipientUc, err := loadUserCosmetics(toUserId)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to load recipient cosmetics data"})
		return
	}
	if slices.Contains(recipientUc.OwnedCosmetics, cosmeticId) {
		c.JSON(400, gin.H{"error": "Recipient already owns this cosmetic"})
		return
	}
	if len(recipientUc.OwnedCosmetics) >= maxOwnedCosmeticsPerUser {
		c.JSON(400, gin.H{"error": "Recipient has reached the maximum number of owned cosmetics"})
		return
	}

	newPurchaserBal := roundVal(userCredits - totalDeduction)
	if newPurchaserBal < 0 {
		c.JSON(400, gin.H{"error": "Insufficient credits"})
		return
	}
	user.SetBalance(newPurchaserBal)

	creatorShare := roundVal(entry.Price * (entry.CreatorPct / 100.0))
	platformShare := roundVal(entry.Price - creatorShare)

	now := time.Now().UnixMilli()

	note := trimAndCapNote(req.Note, 50)

	user.addTransaction(Transaction{
		Note:      "Cosmetic gift: " + entry.Name + " → " + string(toUser.GetUsername()),
		User:      toUserId,
		Amount:    totalDeduction,
		Type:      "cosmetic_gift",
		Timestamp: now,
		NewTotal:  newPurchaserBal,
	})

	creatorUser := getUserById(entry.CreatorId)
	if len(creatorUser) > 0 {
		creatorBal := creatorUser.GetCredits()
		newCreatorBal := roundVal(creatorBal + creatorShare)
		creatorUser.SetBalance(newCreatorBal)
		creatorUser.addTransaction(Transaction{
			Note:      "Cosmetic gift sale: " + entry.Name,
			User:      userId,
			Amount:    creatorShare,
			Type:      "cosmetic_sale",
			Timestamp: now,
			NewTotal:  newCreatorBal,
		})
	}

	mistUser, mistErr := getAccountByUsername(Username("mist"))
	if mistErr == nil && len(mistUser) > 0 {
		mistBal := mistUser.GetCredits()
		newMistBal := roundVal(mistBal + platformShare)
		mistUser.SetBalance(newMistBal)
		if platformShare > 0 {
			mistUser.addTransaction(Transaction{
				Note:      "Cosmetic gift platform cut: " + entry.Name,
				User:      userId,
				Amount:    platformShare,
				Type:      "cosmetic_platform",
				Timestamp: now,
				NewTotal:  newMistBal,
			})
		}
	}

	entry.Purchases++

	giftId := generateToken()
	cosmeticGift := CosmeticGift{
		Id:         giftId,
		CosmeticId: cosmeticId,
		FromUserId: userId,
		ToUserId:   toUserId,
		Note:       note,
		Amount:     entry.Price,
		CreatedAt:  now,
	}

	recipientUc.OwnedCosmetics = append(recipientUc.OwnedCosmetics, cosmeticId)
	if err := saveUserCosmetics(toUserId, recipientUc); err != nil {
		c.JSON(500, gin.H{"error": "Failed to save recipient cosmetics data"})
		return
	}

	claimedAt := now
	cosmeticGift.ClaimedAt = &claimedAt

	cosmeticGiftsMutex.Lock()
	cosmeticGifts = append(cosmeticGifts, cosmeticGift)
	cosmeticGiftsMutex.Unlock()

	addUserEvent(toUserId, "cosmetic_gift", map[string]any{
		"gift_id":       giftId,
		"cosmetic_id":   cosmeticId,
		"cosmetic_name": entry.Name,
		"from":          user.GetUsername(),
		"note":          note,
		"amount":        entry.Price,
	})

	go saveCosmeticGifts()
	go saveCosmeticsCatalog()
	go saveUsers()

	c.JSON(200, gin.H{
		"message":        "Cosmetic gifted successfully",
		"gift_id":        giftId,
		"cosmetic":       entry.ToPublic(),
		"to":             toUser.GetUsername(),
		"price":          entry.Price,
		"tax":            taxAmount,
		"total_paid":     totalDeduction,
		"creator_share":  creatorShare,
		"platform_share": platformShare,
		"new_balance":    newPurchaserBal,
	})
}

func getMyCosmeticGifts(c *gin.Context) {
	user := c.MustGet("user").(*User)
	userId := user.GetId()

	received := getCosmeticGiftsByRecipient(userId)
	sent := getCosmeticGiftsBySender(userId)

	receivedNet := make([]CosmeticGiftNet, 0, len(received))
	for _, g := range received {
		receivedNet = append(receivedNet, g.ToNet())
	}
	sentNet := make([]CosmeticGiftNet, 0, len(sent))
	for _, g := range sent {
		sentNet = append(sentNet, g.ToNet())
	}

	c.JSON(200, gin.H{
		"received": receivedNet,
		"sent":     sentNet,
	})
}
