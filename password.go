package main

import (
	"claw/internal/config"
	"crypto/hmac"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"

	"claw/internal/pwhash"
)

var (
	userSaltCache   = make(map[UserId]string)
	userSaltCacheMu sync.RWMutex
)

func getOrCreateSalt(user User) string {
	userId := user.GetId()

	if salt := user.GetString("sys.salt"); salt != "" {
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

	if user.GetPassVersion() == 1 && config.PBKDF2_SALT != "" {
		setUserSalt(user, config.PBKDF2_SALT)
		return config.PBKDF2_SALT
	}

	salt := pwhash.GenerateSalt()
	setUserSalt(user, salt)
	return salt
}

func setUserSalt(user User, salt string) {
	userId := user.GetId()
	user.Set("sys.salt", salt)

	userSaltCacheMu.Lock()
	userSaltCache[userId] = salt
	userSaltCacheMu.Unlock()

	dir := filepath.Join(config.USERDATA_PATH, string(userId))
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
		if pwhash.MD5Hex(rawPassword) == user.GetPassword() {
			return true, true
		}
		return false, false
	case 1:
		salt := getOrCreateSalt(user)
		candidate := pwhash.HashPBKDF2(rawPassword, salt, config.PBKDF2_ITERATIONS)
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
	stored := pwhash.HashPBKDF2(rawPassword, salt, config.PBKDF2_ITERATIONS)
	user.Set("password", stored)
	user.Set("sys.passv", 1)
}

func SetPasswordV1(user User, rawPassword string) {
	salt := getOrCreateSalt(user)
	stored := pwhash.HashPBKDF2(rawPassword, salt, config.PBKDF2_ITERATIONS)
	user.Set("password", stored)
	user.Set("sys.passv", 1)
}
