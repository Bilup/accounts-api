package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

var (
	usernameAllowedRe = regexp.MustCompile("^[a-z0-9_][a-z0-9_.-]*[a-z0-9_.]$")
	bannedWordsOnce   sync.Once
	bannedWords       []string
	bannedWordsErr    error
)

func loadBannedWordsLocal() ([]string, error) {
	bannedWordsOnce.Do(func() {
		file, err := os.Open("./banned_words.json")
		if err == nil {
			defer file.Close()
			var words []string
			if err := json.NewDecoder(file).Decode(&words); err == nil {
				bannedWords = words
			}
		}

		if BANNED_WORDS_URL != "" {
			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Get(BANNED_WORDS_URL)
			if err == nil {
				defer resp.Body.Close()
				if resp.StatusCode == 200 {
					body, err := io.ReadAll(resp.Body)
					if err == nil {
						for _, word := range strings.Split(string(body), "\n") {
							word = strings.TrimSpace(word)
							if word != "" && !slices.Contains(bannedWords, word) {
								bannedWords = append(bannedWords, word)
							}
						}
					}
				}
			}
		}

		if len(bannedWords) == 0 && bannedWordsErr == nil {
			bannedWordsErr = fmt.Errorf("no banned words loaded")
		}
	})
	return bannedWords, bannedWordsErr
}

func ValidateUsername(username Username) (bool, string) {
	usernameLower := string(username.ToLower())
	if usernameLower == "" {
		return false, "Username is required"
	}
	if len(usernameLower) < 3 || len(usernameLower) > 20 {
		return false, "Username must be between 3 and 20 characters"
	}
	if !usernameAllowedRe.MatchString(usernameLower) {
		return false, "Username contains invalid characters"
	}
	words, err := loadBannedWordsLocal()
	if err == nil {
		for _, banned := range words {
			u := strings.ReplaceAll(usernameLower, "1", "l")
			u = strings.ReplaceAll(u, "3", "e")
			u = strings.ReplaceAll(u, "5", "s")
			u = strings.ReplaceAll(u, "7", "t")
			u = strings.ReplaceAll(u, "9", "i")
			u = strings.ReplaceAll(u, "0", "o")
			u = strings.ReplaceAll(u, "8", "b")
			if strings.Contains(u, strings.ToLower(banned)) {
				return false, "Username contains a banned word"
			}
		}
	}
	return true, ""
}

func ValidatePassword(password string) (bool, string) {
	if password == "" {
		return false, "Password is required"
	}
	if len(password) < 8 {
		return false, "Password must be at least 8 characters"
	}
	if len(password) > 128 {
		return false, "Password must be at most 128 characters"
	}
	if isMD5Hex(password) {
		return false, "Password looks like an MD5 hash - please use a real password"
	}
	return true, ""
}

func IsIpInBannedList(ip string) bool {
	ips := strings.SplitSeq(os.Getenv("BANNED_IPS"), ",")
	for ipAddr := range ips {
		ipAddr = strings.TrimSpace(ipAddr)
		if ipAddr == "" {
			continue
		}
		if strings.EqualFold(ipAddr, ip) {
			return true
		}
	}
	return false
}
