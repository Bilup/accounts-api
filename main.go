package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/gin-gonic/gin"
)

func main() {
	// Ensure environment variables are loaded before any handlers/config usage
	envOnce.Do(loadEnvFile)
	// (Re)load config in case env was changed externally
	loadConfigFromEnv()

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

	if err := loadJSONBadges(); err != nil {
		log.Printf("Warning: Failed to load badges.json: %v", err)
	}

	fmt.Println("Completed loading data")

	go cleanRateLimitStorage()
	go checkSubscriptions()
	go watchUsersFile()
	go watchBadgesFile()
	go cleanExpiredGifts()
	go cleanExpiredSubTokens()
	go cleanUnverifiedAccounts()
	go startStandingRecoveryChecker()
	StartValidatorCleanup()

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
		if strings.HasPrefix(c.Request.URL.Path, "/v2") {
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
