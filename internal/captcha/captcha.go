package captcha

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
)

// VerifyTurnstile verifies a Cloudflare Turnstile token against the
// siteverify API. The secret key is read from the TURNSTILE_SECRET
// environment variable.
func VerifyTurnstile(token string) bool {
	secret := os.Getenv("TURNSTILE_SECRET")
	if secret == "" {
		log.Println("⚠️ TURNSTILE_SECRET not set in environment")
		return false
	}

	form := url.Values{}
	form.Add("secret", secret)
	form.Add("response", token)

	resp, err := http.PostForm("https://challenges.cloudflare.com/turnstile/v0/siteverify", form)
	if err != nil {
		log.Println("Turnstile request failed:", err)
		return false
	}
	defer resp.Body.Close()

	var result struct {
		Success    bool     `json:"success"`
		ErrorCodes []string `json:"error-codes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Println("Turnstile decode error:", err)
		return false
	}

	if !result.Success {
		log.Println("Turnstile failed:", result.ErrorCodes)
	}
	return result.Success
}
