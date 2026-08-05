package config

import (
	"log"
	"os"
	"strconv"
)

const DAILY_CLAIMS_FILE_PATH = "./rotur_daily.json"

const USERS_FILE_PATH = "./users.json"

const DELETED_ACCOUNTS_PATH = "./deleted_accounts.json"

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
	OIDC_ISSUER                   string
	OIDC_CLIENTS_FILE             string
	OIDC_SIGNING_KEY_FILE         string
	OIDC_FRONTEND_URL             string
	AFDIAN_USER_ID                string
	AFDIAN_PLANS_FILE             string
	AFDIAN_ORDERS_FILE            string
)

func MustEnv(key string, def string) string {
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

func Load() {
	LOCAL_POSTS_PATH = MustEnv("LOCAL_POSTS_PATH", "./posts.json")
	FOLLOWERS_FILE_PATH = MustEnv("FOLLOWERS_FILE_PATH", "./clawusers.json")
	ITEMS_FILE_PATH = MustEnv("ITEMS_FILE_PATH", "./items.json")
	GROUPS_FILE_PATH = MustEnv("GROUPS_FILE_PATH", "./rotur/groups")
	USERDATA_PATH = MustEnv("USERDATA_PATH", "./rotur/userdata")
	COSMETICS_FILE_PATH = MustEnv("COSMETICS_FILE_PATH", "./cosmetics_catalog.json")
	COSMETICS_ASSETS_PATH = MustEnv("COSMETICS_ASSETS_PATH", "./cosmetics")
	KEYS_FILE_PATH = MustEnv("KEYS_FILE_PATH", "./keys.json")
	EVENTS_HISTORY_PATH = MustEnv("EVENTS_HISTORY_PATH", "./events_history.json")
	SYSTEMS_FILE_PATH = MustEnv("SYSTEMS_FILE_PATH", "./systems.json")

	EVENT_SERVER_URL = MustEnv("EVENT_SERVER_URL", "")
	BANNED_WORDS_URL = MustEnv("BANNED_WORDS_URL", "")

	BASE_URL = MustEnv("BASE_URL", "https://api.accounts.bilup.org")
	SMTP_HOST = MustEnv("SMTP_HOST", "")
	SMTP_PORT = MustEnv("SMTP_PORT", "587")
	SMTP_USER = MustEnv("SMTP_USER", "")
	SMTP_PASS = MustEnv("SMTP_PASS", "")
	SMTP_FROM = MustEnv("SMTP_FROM", "")

	DISCORD_WEBHOOK_URL = MustEnv("DISCORD_WEBHOOK_URL", "")

	SUBSCRIPTION_CHECK_INTERVAL = intEnv("SUBSCRIPTION_CHECK_INTERVAL", 3600)
	INACTIVITY_TAX_CHECK_INTERVAL = intEnv("INACTIVITY_TAX_CHECK_INTERVAL", 3600)
	KEY_OWNERSHIP_CACHE_TTL = intEnv("KEY_OWNERSHIP_CACHE_TTL", 600)

	ADMIN_TOKEN = MustEnv("ADMIN_TOKEN", "")
	PBKDF2_SALT = MustEnv("PBKDF2_SALT", "")
	PBKDF2_ITERATIONS = intEnv("PBKDF2_ITERATIONS", 600000)

	// OIDC Provider 配置（OIDC_ISSUER 依赖 BASE_URL，须在其后）
	OIDC_ISSUER = MustEnv("OIDC_ISSUER", BASE_URL)
	OIDC_CLIENTS_FILE = MustEnv("OIDC_CLIENTS_FILE", "./oidc_clients.json")
	OIDC_SIGNING_KEY_FILE = MustEnv("OIDC_SIGNING_KEY_FILE", "./oidc_signing_key.pem")
	OIDC_FRONTEND_URL = MustEnv("OIDC_FRONTEND_URL", "https://accounts.bilup.org")

	// 爱发电（Afdian）配置
	AFDIAN_USER_ID = MustEnv("AFDIAN_USER_ID", "")
	AFDIAN_PLANS_FILE = MustEnv("AFDIAN_PLANS_FILE", "./afdian_plans.json")
	AFDIAN_ORDERS_FILE = MustEnv("AFDIAN_ORDERS_FILE", "./afdian_processed_orders.json")
}

func init() {
	Load()
}
