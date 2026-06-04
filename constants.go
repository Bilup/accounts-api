package main

import (
	"log"
	"os"
	"strconv"
)

var DAILY_CLAIMS_FILE_PATH = "./rotur_daily.json"

const USERS_FILE_PATH = "./users.json"

var (
	LOCAL_POSTS_PATH              string
	FOLLOWERS_FILE_PATH           string
	ITEMS_FILE_PATH               string
	KEYS_FILE_PATH                string
	EVENTS_HISTORY_PATH           string
	SYSTEMS_FILE_PATH             string
	GROUPS_FILE_PATH              string
	USERDATA_PATH                 string
	COSMETICS_FILE_PATH           string
	COSMETICS_ASSETS_PATH         string
	WEBSOCKET_SERVER_URL          string
	EVENT_SERVER_URL              string
	SUBSCRIPTION_CHECK_INTERVAL   int
	INACTIVITY_TAX_CHECK_INTERVAL int
	BANNED_WORDS_URL              string
	BASE_URL                      string
	SMTP_HOST                     string
	SMTP_PORT                     string
	SMTP_USER                     string
	SMTP_PASS                     string
	SMTP_FROM                     string
	DISCORD_WEBHOOK_URL           string
	KEY_OWNERSHIP_CACHE_TTL       int
	ADMIN_TOKEN                   string
	PBKDF2_SALT                   string
	PBKDF2_ITERATIONS             int

	bannedDomains = []string{
		"pornhub.com", "xvideos.com", "xnxx.com", "redtube.com", "youporn.com",
		"xhamster.com", "tube8.com", "spankbang.com", "brazzers.com", "onlyfans.com",
		"chaturbate.com", "livejasmine.com", "cam4.com",
	}

	rateLimits = map[string]RateLimitConfig{
		"default":  {Count: 100, Period: 60},
		"post":     {Count: 5, Period: 60},
		"reply":    {Count: 10, Period: 60},
		"follow":   {Count: 20, Period: 60},
		"profile":  {Count: 30, Period: 60},
		"search":   {Count: 20, Period: 60},
		"ai":       {Count: 5, Period: 10},
		"register": {Count: 5, Period: 10},
		"global":   {Count: 10, Period: 10}, // Global rate limit: 10 requests per 10 seconds
	}
)

func mustEnv(key string, def string) string {
	val := os.Getenv(key)
	if val == "" {
		if def != "" {
			return def
		}
		log.Printf("[config] WARNING: %s not set", key)
	}
	return val
}

func intEnv(key string, def int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		log.Printf("[config] invalid int for %s=%s (using default %d)", key, raw, def)
		return def
	}
	return v
}

func loadConfigFromEnv() {
	LOCAL_POSTS_PATH = mustEnv("LOCAL_POSTS_PATH", "./posts.json")
	FOLLOWERS_FILE_PATH = mustEnv("FOLLOWERS_FILE_PATH", "./clawusers.json")
	ITEMS_FILE_PATH = mustEnv("ITEMS_FILE_PATH", "./items.json")
	GROUPS_FILE_PATH = mustEnv("GROUPS_FILE_PATH", "./rotur/groups")
	USERDATA_PATH = mustEnv("USERDATA_PATH", "./rotur/userdata")
	COSMETICS_FILE_PATH = mustEnv("COSMETICS_FILE_PATH", "./cosmetics_catalog.json")
	COSMETICS_ASSETS_PATH = mustEnv("COSMETICS_ASSETS_PATH", "./cosmetics")
	KEYS_FILE_PATH = mustEnv("KEYS_FILE_PATH", "./keys.json")
	EVENTS_HISTORY_PATH = mustEnv("EVENTS_HISTORY_PATH", "./events_history.json")
	SYSTEMS_FILE_PATH = mustEnv("SYSTEMS_FILE_PATH", "./systems.json")

	// External services
	WEBSOCKET_SERVER_URL = mustEnv("WEBSOCKET_SERVER_URL", "")
	EVENT_SERVER_URL = mustEnv("EVENT_SERVER_URL", "")
	BANNED_WORDS_URL = mustEnv("BANNED_WORDS_URL", "")

	BASE_URL = mustEnv("BASE_URL", "https://api.rotur.dev")
	SMTP_HOST = mustEnv("SMTP_HOST", "")
	SMTP_PORT = mustEnv("SMTP_PORT", "587")
	SMTP_USER = mustEnv("SMTP_USER", "")
	SMTP_PASS = mustEnv("SMTP_PASS", "")
	SMTP_FROM = mustEnv("SMTP_FROM", "")

	DISCORD_WEBHOOK_URL = mustEnv("DISCORD_WEBHOOK_URL", "")

	SUBSCRIPTION_CHECK_INTERVAL = intEnv("SUBSCRIPTION_CHECK_INTERVAL", 3600)
	INACTIVITY_TAX_CHECK_INTERVAL = intEnv("INACTIVITY_TAX_CHECK_INTERVAL", 3600)
	KEY_OWNERSHIP_CACHE_TTL = intEnv("KEY_OWNERSHIP_CACHE_TTL", 600)

	ADMIN_TOKEN = mustEnv("ADMIN_TOKEN", "")
	PBKDF2_SALT = mustEnv("PBKDF2_SALT", "")
	PBKDF2_ITERATIONS = intEnv("PBKDF2_ITERATIONS", 600000)
}

func init() {
	loadConfigFromEnv()
	groupsData = make(map[string]*GroupData)
}
