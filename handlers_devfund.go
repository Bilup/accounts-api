package main

import (
	"crypto/subtle"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// escrowTransfer - Transfer credits to escrow (no tax for internal transfers)
func escrowTransfer(c *gin.Context) {
	user := c.MustGet("user").(*User)

	var req struct {
		Amount     float64 `json:"amount"`
		PetitionID string  `json:"petition_id"`
		Note       string  `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request payload"})
		return
	}

	// Normalize & validate amount
	nAmount, ok := normalizeEscrowAmount(req.Amount)
	if !ok {
		c.JSON(400, gin.H{"error": "Minimum amount is 0.01"})
		return
	}

	if req.PetitionID == "" {
		c.JSON(400, gin.H{"error": "Petition ID is required"})
		return
	}

	// Check sender balance
	fromCurrency := user.GetCredits()
	if fromCurrency == 0 {
		c.JSON(400, gin.H{"error": "Sender user has no currency"})
		return
	}
	fromCurrency = roundVal(fromCurrency)

	if fromCurrency < nAmount {
		c.JSON(400, gin.H{"error": "Insufficient funds", "required": nAmount, "available": fromCurrency})
		return
	}

	// Deduct from sender (no tax for escrow)
	newBal := roundVal(fromCurrency - nAmount)
	if newBal < 0 { // guard against tiny floating error
		newBal = 0
	}

	user.SetBalance(newBal)

	// Add escrow transaction to sender
	now := time.Now().UnixMilli()
	note := strings.TrimSpace(req.Note)
	if note == "" {
		note = "devfund escrow"
	}
	if len(note) > 50 {
		note = note[:50]
	}

	user.addTransaction(Transaction{
		Note:       note,
		User:       Username("rotur").Id(),
		Timestamp:  now,
		Amount:     nAmount,
		Type:       "escrow_out",
		PetitionId: req.PetitionID,
		NewTotal:   newBal,
	})

	go saveUsers()

	c.JSON(200, gin.H{
		"message":     "Escrow transfer successful",
		"from":        user.GetUsername(),
		"amount":      nAmount,
		"petition_id": req.PetitionID,
		"new_balance": newBal,
	})
}

type escrowReleaseRequest struct {
	Amount     float64 `json:"amount"`
	ToUsername string  `json:"to_username"`
	PetitionID string  `json:"petition_id"`
	Note       string  `json:"note"`
}

// performEscrowRelease credits the developer and records the transaction. It is
// shared by the admin (mist) and the service-key (devfund vote) release paths.
func performEscrowRelease(c *gin.Context, req escrowReleaseRequest) {
	nAmount, ok := normalizeEscrowAmount(req.Amount)
	if !ok {
		c.JSON(400, gin.H{"error": "Minimum amount is 0.01"})
		return
	}
	if req.ToUsername == "" {
		c.JSON(400, gin.H{"error": "Recipient username is required"})
		return
	}
	if req.PetitionID == "" {
		c.JSON(400, gin.H{"error": "Petition ID is required"})
		return
	}

	toUser, err := getAccountByUsername(Username(req.ToUsername))
	if err != nil {
		c.JSON(404, gin.H{"error": "Recipient user not found"})
		return
	}

	toCurrency := toUser.GetCredits()
	if toCurrency == 0 {
		toCurrency = float64(0)
	}
	newBal := roundVal(toCurrency + nAmount)
	toUser.SetBalance(newBal)

	now := time.Now().UnixMilli()
	note := strings.TrimSpace(req.Note)
	if note == "" {
		note = "devfund escrow release"
	}
	if len(note) > 50 {
		note = note[:50]
	}

	toUser.addTransaction(Transaction{
		Note:       note,
		User:       Username("rotur").Id(),
		Timestamp:  now,
		Amount:     nAmount,
		Type:       "escrow_in",
		PetitionId: req.PetitionID,
		NewTotal:   newBal,
	})

	go saveUsers()

	c.JSON(200, gin.H{
		"message":     "Escrow release successful",
		"to":          toUser.GetUsername(),
		"amount":      nAmount,
		"petition_id": req.PetitionID,
		"new_balance": newBal,
	})
}

// escrowRelease - Release escrow credits to developer (admin only)
func escrowRelease(c *gin.Context) {
	user := c.MustGet("user").(*User)

	// Only allow mist (admin) to release escrow
	if user.GetUsername().ToLower() != "mist" {
		c.JSON(403, gin.H{"error": "Admin access required"})
		return
	}

	var req escrowReleaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request payload"})
		return
	}
	performEscrowRelease(c, req)
}

// escrowReleaseService - Release escrow credits to a developer on behalf of the
// devFund service. Authenticated by a shared service key rather than a user
// token, so backer-vote completion can pay out without an admin in the loop.
func escrowReleaseService(c *gin.Context) {
	key := c.GetHeader("X-Devfund-Key")
	expected := os.Getenv("DEVFUND_SERVICE_KEY")
	if expected == "" || subtle.ConstantTimeCompare([]byte(key), []byte(expected)) != 1 {
		c.JSON(403, gin.H{"error": "Invalid service key"})
		return
	}

	var req escrowReleaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request payload"})
		return
	}
	performEscrowRelease(c, req)
}
