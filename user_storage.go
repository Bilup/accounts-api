package main

import (
	"claw/internal/config"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const userFileName = "user.json"

func userFilePath(id UserId) string {
	return getUserDataFile(id, userFileName)
}

func writeUserFile(id UserId, u User) error {
	path := userFilePath(id)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.Marshal(u)
	if err != nil {
		return err
	}
	return atomicWrite(path, data, 0644)
}

func deleteUserFile(id UserId) {
	if id == "" {
		return
	}
	if err := os.Remove(userFilePath(id)); err != nil && !os.IsNotExist(err) {
		log.Printf("Error removing user file for %s: %v", id, err)
	}
}

func readAllUserFiles() []User {
	entries, err := os.ReadDir(config.USERDATA_PATH)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("Error reading user data dir: %v", err)
		}
		return nil
	}
	loaded := make([]User, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(config.USERDATA_PATH, e.Name(), userFileName)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var u User
		if err := json.Unmarshal(data, &u); err != nil {
			log.Printf("Error unmarshaling %s: %v", path, err)
			continue
		}
		if len(u) == 0 {
			continue
		}
		loaded = append(loaded, u)
	}
	return loaded
}

func migrateLegacyUsersFile() ([]User, bool) {
	if _, err := os.Stat(config.USERS_FILE_PATH); os.IsNotExist(err) {
		return nil, false
	}
	data, err := os.ReadFile(config.USERS_FILE_PATH)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	var legacy []User
	if err := json.Unmarshal(data, &legacy); err != nil {
		log.Printf("Error unmarshaling legacy users file (migration aborted): %v", err)
		return nil, false
	}
	written := 0
	for i := range legacy {
		id := legacy[i].GetId()
		if id == "" {
			continue
		}
		if err := writeUserFile(id, legacy[i]); err != nil {
			log.Printf("Error migrating user %s to per-user file: %v", id, err)
			continue
		}
		written++
	}
	if err := os.Rename(config.USERS_FILE_PATH, config.USERS_FILE_PATH+".migrated"); err != nil {
		log.Printf("Error archiving legacy users file after migration: %v", err)
	}
	log.Printf("Migrated %d users from %s to per-user files", written, config.USERS_FILE_PATH)
	return legacy, true
}

// rebuildUserIndexesLocked rebuilds every secondary index from the users slice.
// Callers must hold usersMutex (read or write).
func rebuildUserIndexesLocked() {
	n := len(users)
	usernameToIdInner := make(map[Username]UserId, n)
	idToUserInner := make(map[UserId]User, n)
	keyToIdInner := make(map[string]UserId, n)
	emailToIdInner := make(map[string]UserId, n)
	resetTokenToIdInner := make(map[string]UserId, n)
	verifyTokenToIdInner := make(map[string]UserId, n)
	tierCache := make(map[Username]string, n)

	for i := range users {
		u := users[i]
		id := u.GetId()
		nameLower := u.GetUsername().ToLower()
		usernameToIdInner[nameLower] = id
		idToUserInner[id] = u
		if key := u.GetKey(); key != "" {
			keyToIdInner[key] = id
		}
		if em := strings.ToLower(strings.TrimSpace(u.GetEmail())); em != "" {
			emailToIdInner[em] = id
		}
		if t := u.GetString("sys.reset_token"); t != "" {
			resetTokenToIdInner[t] = id
		}
		if t := u.GetString("sys.email_verify_token"); t != "" {
			verifyTokenToIdInner[t] = id
		}

		tier := "Free"
		if sub := u.Get("sys.subscription"); sub != nil {
			if m, ok := sub.(map[string]any); ok {
				if t, ok := m["tier"]; ok {
					if s, ok := t.(string); ok && s != "" {
						tier = normalizeSubscriptionTier(s)
					}
				}
			}
		}
		tierCache[nameLower] = tier
	}

	idToUserMutex.Lock()
	usernameToId = usernameToIdInner
	idToUser = idToUserInner
	keyToId = keyToIdInner
	emailToId = emailToIdInner
	resetTokenToId = resetTokenToIdInner
	verifyTokenToId = verifyTokenToIdInner
	idToUserMutex.Unlock()

	userTierCacheMu.Lock()
	userTierCache = tierCache
	userTierCacheMu.Unlock()
}

func rebuildUserIndexes() {
	usersMutex.RLock()
	defer usersMutex.RUnlock()
	rebuildUserIndexesLocked()
}

func anyToUsername(v any) Username {
	switch t := v.(type) {
	case Username:
		return t
	case string:
		return Username(t)
	}
	return ""
}

func normEmail(v any) string {
	s, _ := v.(string)
	return strings.ToLower(strings.TrimSpace(s))
}

func swapIndexEntry(m map[string]UserId, oldKey, newKey string, id UserId) {
	idToUserMutex.Lock()
	if oldKey != "" {
		delete(m, oldKey)
	}
	if newKey != "" && id != "" {
		m[newKey] = id
	}
	idToUserMutex.Unlock()
}

func maintainUserIndexOnSet(uid UserId, key string, oldValue, value any) {
	if uid == "" {
		return
	}
	switch key {
	case "key":
		o, _ := oldValue.(string)
		n, _ := value.(string)
		if o != n {
			swapIndexEntry(keyToId, o, n, uid)
		}
	case "username":
		o := string(anyToUsername(oldValue).ToLower())
		n := string(anyToUsername(value).ToLower())
		if o != n {
			idToUserMutex.Lock()
			if o != "" {
				delete(usernameToId, Username(o))
			}
			if n != "" {
				usernameToId[Username(n)] = uid
			}
			idToUserMutex.Unlock()
		}
	case "email":
		o := normEmail(oldValue)
		n := normEmail(value)
		if o != n {
			swapIndexEntry(emailToId, o, n, uid)
		}
	case "sys.reset_token":
		o, _ := oldValue.(string)
		n, _ := value.(string)
		if o != n {
			swapIndexEntry(resetTokenToId, o, n, uid)
		}
	case "sys.email_verify_token":
		o, _ := oldValue.(string)
		n, _ := value.(string)
		if o != n {
			swapIndexEntry(verifyTokenToId, o, n, uid)
		}
	}
}

func watchUserIndexes() {
	for {
		time.Sleep(2 * time.Second)
		rebuildUserIndexes()
	}
}

var (
	dirtyUsersMu    sync.Mutex
	dirtyUsers      = map[UserId]struct{}{}
	userFlusherOnce sync.Once
)

func saveUser(id UserId) {
	if id == "" {
		return
	}
	dirtyUsersMu.Lock()
	dirtyUsers[id] = struct{}{}
	dirtyUsersMu.Unlock()
	userFlusherOnce.Do(func() { go userFlushLoop() })
}

func saveUsersBulk(ids []UserId) {
	for _, id := range ids {
		saveUser(id)
	}
}

func userFlushLoop() {
	for {
		time.Sleep(100 * time.Millisecond)

		dirtyUsersMu.Lock()
		if len(dirtyUsers) == 0 {
			dirtyUsersMu.Unlock()
			continue
		}
		batch := make([]UserId, 0, len(dirtyUsers))
		for id := range dirtyUsers {
			batch = append(batch, id)
		}
		dirtyUsers = make(map[UserId]struct{})
		dirtyUsersMu.Unlock()

		for _, id := range batch {
			flushUser(id)
		}
	}
}

func flushUser(id UserId) {
	u := getUserById(id)
	if u == nil {
		return
	}
	snapshot := copyUser(u)
	if err := writeUserFile(id, snapshot); err != nil {
		log.Printf("Error saving user %s: %v", id, err)
	}
}
