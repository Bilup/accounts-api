package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"net/mail"
	"net/smtp"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func generateVerifyToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("failed to generate verify token: " + err.Error())
	}
	return "ev_" + base64.RawURLEncoding.EncodeToString(b)
}

func generateResetToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("failed to generate reset token: " + err.Error())
	}
	return "pr_" + base64.RawURLEncoding.EncodeToString(b)
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

func sendResetEmail(toEmail string, username Username, token string) {
	if SMTP_HOST == "" || SMTP_FROM == "" || BASE_URL == "" {
		log.Printf("[email] SMTP not configured, skipping reset email for %s", toEmail)
		return
	}
	resetURL := fmt.Sprintf("%s/reset_password?token=%s", "https://rotur.dev", url.QueryEscape(token))
	subject := "Reset your Rotur password"
	body := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=\"utf-8\"\r\n\r\n"+
			"<div style=\"font-family:sans-serif;max-width:480px;margin:0 auto;padding:24px;\">"+
			"<h2 style=\"color:#57cdac;\">Password Reset, %s</h2>"+
			"<p>We received a request to reset your password.</p>"+
			"<a href=\"%s\" style=\"display:inline-block;padding:12px 24px;background:#57cdac;color:#fff;border-radius:6px;text-decoration:none;font-weight:bold;\">Reset Password</a>"+
			"<p>\"%s\"</p>"+
			"<p style=\"color:#777;font-size:13px;margin-top:16px;\">This link expires in 1 hour. If you did not request a reset, ignore this email.</p>"+
			"</div>",
		SMTP_FROM, toEmail, subject, username, resetURL, resetURL,
	)
	var auth smtp.Auth
	if SMTP_USER != "" && SMTP_PASS != "" {
		auth = smtp.PlainAuth("", SMTP_USER, SMTP_PASS, SMTP_HOST)
	}
	addr := SMTP_HOST + ":" + SMTP_PORT
	if err := smtp.SendMail(addr, auth, SMTP_FROM, []string{toEmail}, []byte(body)); err != nil {
		log.Printf("[email] failed to send reset to %s: %v", toEmail, err)
	}
}

func verifyEmailHandler(c *gin.Context) {
	token := c.Query("token")
	if !requireField(c, token, "token is required") {
		return
	}
	usersMutex.RLock()
	var foundUser *User
	for i := range users {
		if users[i].GetString("sys.email_verify_token") == token {
			foundUser = &users[i]
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
	user := currentUser(c)
	if user.Get("sys.email_verified") == true {
		c.JSON(400, gin.H{"error": "Email is already verified"})
		return
	}
	email := user.GetEmail()
	if !requireField(c, email, "No email on file") {
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

func changePasswordHandler(c *gin.Context) {
	user := currentUser(c)
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !bindJSON(c, &req) {
		return
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		c.JSON(400, gin.H{"error": "Current password and new password are required"})
		return
	}
	matched, _ := VerifyPassword(*user, req.CurrentPassword)
	if !matched {
		c.JSON(403, gin.H{"error": "Current password is incorrect"})
		return
	}
	if ok, msg := ValidatePassword(req.NewPassword); !ok {
		c.JSON(400, gin.H{"error": msg})
		return
	}
	SetPasswordV1(*user, req.NewPassword)
	go saveUsers()
	c.JSON(200, gin.H{"message": "Password changed successfully"})
}

func requestPasswordResetHandler(c *gin.Context) {
	var req struct {
		Email string `json:"email"`
	}
	if !bindJSON(c, &req) {
		return
	}
	email := strings.TrimSpace(req.Email)
	if email == "" || !isValidEmail(email) {
		c.JSON(400, gin.H{"error": "A valid email address is required"})
		return
	}
	var foundUser *User
	usersMutex.RLock()
	for i := range users {
		if strings.EqualFold(strings.TrimSpace(users[i].GetEmail()), email) {
			foundUser = &users[i]
			break
		}
	}
	usersMutex.RUnlock()
	if foundUser == nil {
		c.JSON(200, gin.H{"message": "If an account with that email exists, a reset link has been sent"})
		return
	}
	now := time.Now().UnixMilli()
	lastSent := int64(foundUser.GetInt("sys.reset_sent"))
	if lastSent > 0 && (now-lastSent) < 60_000 {
		c.JSON(429, gin.H{"error": "Please wait before requesting another reset email"})
		return
	}
	token := generateResetToken()
	foundUser.Set("sys.reset_token", token)
	foundUser.Set("sys.reset_sent", now)
	foundUser.Set("sys.reset_expires", now+3_600_000)
	go saveUsers()
	go sendResetEmail(foundUser.GetEmail(), foundUser.GetUsername(), token)
	c.JSON(200, gin.H{"message": "If an account with that email exists, a reset link has been sent"})
}

func resetPasswordHandler(c *gin.Context) {
	var req struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}
	if !bindJSON(c, &req) {
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" || req.NewPassword == "" {
		c.JSON(400, gin.H{"error": "Token and new password are required"})
		return
	}
	if ok, msg := ValidatePassword(req.NewPassword); !ok {
		c.JSON(400, gin.H{"error": msg})
		return
	}
	usersMutex.RLock()
	var foundUser *User
	for i := range users {
		if users[i].GetString("sys.reset_token") == token {
			foundUser = &users[i]
			break
		}
	}
	usersMutex.RUnlock()
	if foundUser == nil {
		c.JSON(400, gin.H{"error": "Invalid or expired reset token"})
		return
	}
	expires := int64(foundUser.GetInt("sys.reset_expires"))
	if time.Now().UnixMilli() > expires {
		foundUser.Set("sys.reset_token", "")
		foundUser.Set("sys.reset_expires", 0)
		go saveUsers()
		c.JSON(400, gin.H{"error": "Reset token has expired"})
		return
	}
	SetPasswordV1(*foundUser, req.NewPassword)
	foundUser.Set("sys.reset_token", "")
	foundUser.Set("sys.reset_sent", 0)
	foundUser.Set("sys.reset_expires", 0)
	go saveUsers()
	c.JSON(200, gin.H{"message": "Password reset successfully"})
}
