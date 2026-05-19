package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func generateVerifyToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return "ev_" + base64.URLEncoding.EncodeToString(b)
}

func isValidEmail(email string) bool {
	if email == "" {
		return false
	}
	addr, err := mail.ParseAddress(email)
	if err != nil {
		return false
	}
	return strings.EqualFold(addr.Address, email)
}

func isDisposableEmail(email string) bool {
	domain := strings.ToLower(strings.SplitN(email, "@", 2)[1])
	for _, d := range bannedDomains {
		if domain == d || strings.HasSuffix(domain, "."+d) {
			return true
		}
	}
	return false
}

func sendVerifyEmail(toEmail string, username string, token string) {
	if SMTP_HOST == "" || SMTP_FROM == "" || BASE_URL == "" {
		log.Printf("[email] SMTP not configured, skipping verification email for %s", toEmail)
		return
	}

	verifyURL := fmt.Sprintf("%s/verify_email?token=%s", strings.TrimRight(BASE_URL, "/"), token)

	subject := "Verify your Rotur email"
	body := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=\"utf-8\"\r\n\r\n"+
			"<div style=\"font-family:sans-serif;max-width:480px;margin:0 auto;padding:24px;\">"+
			"<h2 style=\"color:#57cdac;\">Welcome to Rotur, %s!</h2>"+
			"<p>Please verify your email address to activate your account.</p>"+
			"<a href=\"%s\" style=\"display:inline-block;padding:12px 24px;background:#57cdac;color:#fff;border-radius:6px;text-decoration:none;font-weight:bold;\">Verify Email</a>"+
			"<p>\"%s\"</p>"+
			"<p style=\"color:#777;font-size:13px;margin-top:16px;\">If you did not create this account, you can ignore this email.</p>"+
			"</div>",
		SMTP_FROM, toEmail, subject, username, verifyURL, verifyURL,
	)

	var auth smtp.Auth
	if SMTP_USER != "" && SMTP_PASS != "" {
		auth = smtp.PlainAuth("", SMTP_USER, SMTP_PASS, SMTP_HOST)
	}

	addr := SMTP_HOST + ":" + SMTP_PORT
	if err := smtp.SendMail(addr, auth, SMTP_FROM, []string{toEmail}, []byte(body)); err != nil {
		log.Printf("[email] failed to send verification to %s: %v", toEmail, err)
	}
}

func verifyEmailHandler(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		c.JSON(400, gin.H{"error": "token is required"})
		return
	}

	usersMutex.RLock()
	var foundUser User
	for i := range users {
		if users[i].GetString("sys.email_verify_token") == token {
			foundUser = users[i]
			break
		}
	}
	usersMutex.RUnlock()

	if foundUser == nil {
		c.JSON(400, gin.H{"error": "Invalid or expired verification token"})
		return
	}

	foundUser.Set("sys.email_verified", true)
	foundUser.Set("sys.email_verify_token", "")

	go saveUsers()

	c.JSON(200, gin.H{
		"message":  "Email verified successfully",
		"username": foundUser.GetUsername(),
	})
}

func resendVerificationHandler(c *gin.Context) {
	user := c.MustGet("user").(*User)

	if user.Get("sys.email_verified") == true {
		c.JSON(400, gin.H{"error": "Email is already verified"})
		return
	}

	email := user.GetEmail()
	if email == "" {
		c.JSON(400, gin.H{"error": "No email on file"})
		return
	}

	now := time.Now().UnixMilli()
	lastSent := int64(user.GetInt("sys.email_verify_sent"))
	if lastSent > 0 && (now-lastSent) < 60_000 {
		c.JSON(429, gin.H{"error": "Please wait before requesting another verification email"})
		return
	}

	token := generateVerifyToken()
	user.Set("sys.email_verify_token", token)
	user.Set("sys.email_verify_sent", now)
	go saveUsers()

	go sendVerifyEmail(email, string(user.GetUsername()), token)

	c.JSON(200, gin.H{"message": "Verification email sent"})
}
