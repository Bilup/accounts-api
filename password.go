package main

import (
	"crypto/hmac"
	"crypto/md5"
	crypto_rand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/pbkdf2"
)

func md5Hex(input string) string {
	h := md5.Sum([]byte(input))
	return hex.EncodeToString(h[:])
}

func isMD5Hex(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func HashPBKDF2(rawPassword string, salt string, iterations int) string {
	derived := pbkdf2.Key([]byte(rawPassword), []byte(salt), iterations, 32, sha256.New)
	return hex.EncodeToString(derived)
}

func generateSalt() string {
	b := make([]byte, 32)
	if _, err := crypto_rand.Read(b); err != nil {
		panic("failed to generate salt: " + err.Error())
	}
	return hex.EncodeToString(b)
}

var (
	userSaltCache   = make(map[UserId]string)
	userSaltCacheMu sync.RWMutex
)

func getOrCreateSalt(user User) string {
	userId := user.GetId()

	if salt := getStringOrEmpty(user.Get("sys.salt")); salt != "" {
		return salt
	}

	userSaltCacheMu.RLock()
	cached, ok := userSaltCache[userId]
	userSaltCacheMu.RUnlock()
	if ok && cached != "" {
		user.Set("sys.salt", cached)
		return cached
	}

	loaded, err := LoadUserJSON[string](userId, "salt.json")
	if err == nil && loaded != "" {
		userSaltCacheMu.Lock()
		userSaltCache[userId] = loaded
		userSaltCacheMu.Unlock()
		user.Set("sys.salt", loaded)
		return loaded
	}

	if user.GetPassVersion() == 1 && PBKDF2_SALT != "" {
		setUserSalt(user, PBKDF2_SALT)
		return PBKDF2_SALT
	}

	salt := generateSalt()
	setUserSalt(user, salt)
	return salt
}

func setUserSalt(user User, salt string) {
	userId := user.GetId()
	user.Set("sys.salt", salt)

	userSaltCacheMu.Lock()
	userSaltCache[userId] = salt
	userSaltCacheMu.Unlock()

	dir := filepath.Join(USERDATA_PATH, string(userId))
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("[salt] error creating userdata dir for %s: %v", userId, err)
		return
	}
	path := filepath.Join(dir, "salt.json")
	data, _ := json.Marshal(salt)
	if err := atomicWrite(path, data, 0600); err != nil {
		log.Printf("[salt] error writing salt file for %s: %v", userId, err)
	}
}

func VerifyPassword(user User, rawPassword string) (bool, bool) {
	switch user.GetPassVersion() {
	case 0:
		if md5Hex(rawPassword) == user.GetPassword() {
			return true, true
		}
		return false, false
	case 1:
		salt := getOrCreateSalt(user)
		candidate := HashPBKDF2(rawPassword, salt, PBKDF2_ITERATIONS)
		if hmac.Equal([]byte(candidate), []byte(user.GetPassword())) {
			return true, false
		}
		return false, false
	default:
		return false, false
	}
}

func UpgradePasswordToV1(user User, rawPassword string) {
	salt := getOrCreateSalt(user)
	stored := HashPBKDF2(rawPassword, salt, PBKDF2_ITERATIONS)
	user.Set("password", stored)
	user.Set("sys.passv", 1)
}

func SetPasswordV1(user User, rawPassword string) {
	salt := getOrCreateSalt(user)
	stored := HashPBKDF2(rawPassword, salt, PBKDF2_ITERATIONS)
	user.Set("password", stored)
	user.Set("sys.passv", 1)
}
