package main

import (
	"claw/internal/config"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/gin-gonic/gin"
)

func init() {
	initDataFiles()
}

func initDataFiles() {
	dirs := []string{
		config.GROUPS_FILE_PATH,
		config.USERDATA_PATH,
		config.COSMETICS_ASSETS_PATH,
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Printf("[init] Warning: Failed to create directory %s: %v", dir, err)
		} else {
			log.Printf("[init] Created directory %s", dir)
		}
	}

	files := map[string]string{
		config.DAILY_CLAIMS_FILE_PATH:  "[]",
		config.USERS_FILE_PATH:         "[]",
		config.DELETED_ACCOUNTS_PATH:   "{}",
		config.LOCAL_POSTS_PATH:        "[]",
		config.FOLLOWERS_FILE_PATH:     "{}",
		config.ITEMS_FILE_PATH:         "[]",
		config.KEYS_FILE_PATH:          "[]",
		config.EVENTS_HISTORY_PATH:     "{}",
		config.SYSTEMS_FILE_PATH:       "{}",
		config.COSMETICS_FILE_PATH:     "[]",
		filepath.Join("./rotur", "badges.json"): "[]",
		config.OIDC_CLIENTS_FILE:       "[]",
		config.AFDIAN_PLANS_FILE:       `{"plans":{},"skus":{},"days_per_month":31}`,
		config.AFDIAN_ORDERS_FILE:      "{}",
	}

	for path, content := range files {
		if info, err := os.Stat(path); os.IsNotExist(err) {
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				log.Printf("[init] Warning: Failed to create file %s: %v", path, err)
			} else {
				log.Printf("[init] Created file %s", path)
			}
		} else if info.Size() == 0 {
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				log.Printf("[init] Warning: Failed to fix empty file %s: %v", path, err)
			} else {
				log.Printf("[init] Fixed empty file %s", path)
			}
		}
	}
}

func main() {
	// Ensure environment variables are loaded before any handlers/config usage
	envOnce.Do(loadEnvFile)
	// (Re)load config in case env was changed externally
	config.Load()

	// Load initial data
	loadBannedWords()
	loadUsers()
	loadGroupData()
	loadFollowers()
	loadPosts()
	loadItems()
	loadKeys()
	loadSystems()
	loadEventsHistory()
	loadGifts()
	loadCosmeticsCatalog()
	loadFeatureStores()
	loadReports()
	loadDeletedAccounts()
	go scheduleLoop()
	loadCosmeticGifts()
	buildSubTokenIndex()
	loadOIDCClients()
	initOIDCSigningKey()
	loadAfdianPlans()
	loadAfdianOrders()
	initAfdianPublicKey()

	if err := loadJSONBadges(); err != nil {
		log.Printf("Warning: Failed to load badges.json: %v", err)
	}

	fmt.Println("Completed loading data")

	go cleanRateLimitStorage()
	go checkSubscriptions()
	go watchUserIndexes()
	go watchBadgesFile()
	go cleanExpiredGifts()
	go cleanExpiredSubTokens()
	go cleanUnverifiedAccounts()
	go startStandingRecoveryChecker()
	StartValidatorCleanup()
	StartOIDCCleanup()
	go watchAfdianPlansFile()

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		log.Printf("Received %s, flushing state to disk...", sig)
		flushAll()
		log.Println("Flush complete, exiting")
		os.Exit(0)
	}()

	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	v1CORS := corsMiddleware()
	v2CORS := v2CORSMiddleware()
	r.Use(func(c *gin.Context) {
		path := c.Request.URL.Path
		// /v2 与 /oauth 路径需要支持凭证（cookie）的跨域请求，使用 v2CORS
		if strings.HasPrefix(path, "/v2") || strings.HasPrefix(path, "/oauth") {
			v2CORS(c)
			return
		}
		v1CORS(c)
	})

	startClawRealtimeServers()

	setupV1Routes(r)
	setupV2Routes(r)
	startAvatarServer()

	log.Println("Claw server starting on port 5602...")
	if err := r.Run("0.0.0.0:5602"); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
