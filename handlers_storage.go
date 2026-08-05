package main

import (
	"claw/internal/config"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// File operations
func loadUsers() {
	usersMutex.Lock()
	defer usersMutex.Unlock()

	loaded := readAllUserFiles()

	if legacy, didMigrate := migrateLegacyUsersFile(); didMigrate && len(loaded) == 0 {
		loaded = legacy
	}
	if loaded == nil {
		loaded = make([]User, 0)
	}

	migratedCount := 0
	for i := range loaded {
		if loaded[i].IsBanned() && loaded[i].Get("sys.standing") == nil {
			loaded[i].Set("sys.standing", string(StandingBanned))
			migratedCount++
		}
	}
	if migratedCount > 0 {
		log.Printf("Migrated %d users to standing system", migratedCount)
	}

	users = loaded

	assignMissingUserIndexesLocked()

	fmt.Println("Loaded", len(loaded), "users")

	rebuildUserIndexesLocked()
	clearOverlayCosmeticsCache()

	userMutexesLock.Lock()
	userPtrMutexes = make(map[uintptr]*sync.RWMutex, len(loaded))
	userMutexesLock.Unlock()
}

var (
	userIndexMu  sync.Mutex
	userIndexMax int64
)

// assignMissingUserIndexesLocked stamps a persistent, monotonic ordinal on any
// user that lacks one, ordered by creation time so newer accounts get higher
// indexes. Existing indexes are never reassigned, so a user's index is stable
// across restarts and unaffected by deletions. Callers must hold usersMutex.
func assignMissingUserIndexesLocked() {
	var maxIdx int64
	var missing []int
	for i := range users {
		if idx := users[i].GetIndex(); idx > 0 {
			if idx > maxIdx {
				maxIdx = idx
			}
		} else {
			missing = append(missing, i)
		}
	}

	sort.SliceStable(missing, func(a, b int) bool {
		ua, ub := users[missing[a]], users[missing[b]]
		if ca, cb := ua.GetCreated(), ub.GetCreated(); ca != cb {
			return ca < cb
		}
		return ua.GetId() < ub.GetId()
	})
	for _, i := range missing {
		maxIdx++
		users[i]["sys.index"] = maxIdx
		saveUser(users[i].GetId())
	}

	userIndexMu.Lock()
	if maxIdx > userIndexMax {
		userIndexMax = maxIdx
	}
	userIndexMu.Unlock()
}

func nextUserIndex() int64 {
	userIndexMu.Lock()
	defer userIndexMu.Unlock()
	userIndexMax++
	return userIndexMax
}

func loadGroupData() {
	groupsDataMutex.Lock()
	defer groupsDataMutex.Unlock()
	if _, err := os.Stat(config.GROUPS_FILE_PATH); os.IsNotExist(err) {
		err = os.MkdirAll(config.GROUPS_FILE_PATH, 0755)
		if err != nil {
			log.Printf("Error creating groups directory: %v", err)
			groupsData = make(map[string]*GroupData)
			return
		}
		groupsData = make(map[string]*GroupData)
		return
	}
	entries, err := os.ReadDir(config.GROUPS_FILE_PATH)
	if err != nil {
		log.Printf("Error reading groups directory: %v", err)
		groupsData = make(map[string]*GroupData)
		return
	}
	groupsData = make(map[string]*GroupData)
	loadedCount := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		groupJsonPath := filepath.Join(config.GROUPS_FILE_PATH, entry.Name(), "group.json")
		data, err := os.ReadFile(groupJsonPath)
		if err != nil {
			log.Printf("Error reading group file %s: %v", groupJsonPath, err)
			continue
		}
		var groupData GroupData
		if err := json.Unmarshal(data, &groupData); err != nil {
			log.Printf("Error unmarshaling group data from %s: %v", groupJsonPath, err)
			continue
		}
		// Load tips from separate file and compute credits balance
		tipsPath := filepath.Join(config.GROUPS_FILE_PATH, entry.Name(), "tips.json")
		tipsData, err := os.ReadFile(tipsPath)
		if err == nil {
			var tips []GroupTip
			if json.Unmarshal(tipsData, &tips) == nil {
				balance := 0.0
				for _, tip := range tips {
					balance += tip.AmountCredits
				}
				withdrawalsPath := filepath.Join(config.GROUPS_FILE_PATH, entry.Name(), "withdrawals.json")
				if withdrawalsData, wErr := os.ReadFile(withdrawalsPath); wErr == nil {
					var withdrawals []GroupTipWithdrawal
					if json.Unmarshal(withdrawalsData, &withdrawals) == nil {
						for _, w := range withdrawals {
							balance -= w.AmountCredits
						}
					}
				}
				if balance < 0 {
					balance = 0
				}
				groupData.Group.CreditsBalance = balance
			}
		}
		tag := groupData.Group.Tag
		if groupData.Invites == nil {
			groupData.Invites = []GroupInvite{}
		}
		if groupData.JoinRequests == nil {
			groupData.JoinRequests = []GroupJoinRequest{}
		}
		if groupData.Bans == nil {
			groupData.Bans = []GroupBan{}
		}
		groupsData[tag] = &groupData
		loadedCount++
	}
	log.Printf("Loaded %d groups", loadedCount)
}

func saveGroupFile(groupTag string, groupData *GroupData) {
	dirPath := groupDirPath(groupTag)
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		log.Printf("Error creating group directory for %s: %v", groupTag, err)
		return
	}
	mainPath := filepath.Join(dirPath, "group.json")
	data, err := json.MarshalIndent(groupData, "", " ")
	if err != nil {
		log.Printf("Error marshaling group data for %s: %v", groupTag, err)
		return
	}
	if err := atomicWrite(mainPath, data, 0644); err != nil {
		log.Printf("Error saving group data for %s: %v", groupTag, err)
	}
}

func saveGroupData(groupTag string) {
	groupsDataMutex.RLock()
	groupData, ok := groupsData[groupTag]
	groupsDataMutex.RUnlock()
	if ok {
		saveGroupFile(groupTag, groupData)
	}
}

func deleteGroupData(groupTag string) {
	dirPath := groupDirPath(groupTag)
	os.RemoveAll(dirPath)
}

func atomicWrite(path string, data []byte, perm os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err = f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err = f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err = os.Chmod(tmp, perm); err != nil {
		os.Remove(tmp)
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func saveUsers() {
	flushDirtyUsers()
}

func flushDirtyUsers() {
	dirtyUsersMu.Lock()
	if len(dirtyUsers) == 0 {
		dirtyUsersMu.Unlock()
		return
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

func copyUser(u User) User {
	if u == nil {
		return nil
	}

	mu := getMutexForUser(u)

	mu.RLock()
	defer mu.RUnlock()

	return deepCopyUser(u)
}

func deepCopyUser(u User) User {
	out := make(User, len(u))

	for k, v := range u {
		out[k] = deepCopyValue(v)
	}

	return out
}

func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, vv := range t {
			m[k] = deepCopyValue(vv)
		}
		return m
	case []map[string]any:
		s := make([]map[string]any, len(t))
		for i := range t {
			// deep copy each map element
			mm := make(map[string]any, len(t[i]))
			for k, vv := range t[i] {
				mm[k] = deepCopyValue(vv)
			}
			s[i] = mm
		}
		return s
	case []any:
		s := make([]any, len(t))
		for i := range t {
			s[i] = deepCopyValue(t[i])
		}
		return s
	case []string:
		s := make([]string, len(t))
		copy(s, t)
		return s
	case []int:
		s := make([]int, len(t))
		copy(s, t)
		return s
	case []float64:
		s := make([]float64, len(t))
		copy(s, t)
		return s
	case []bool:
		s := make([]bool, len(t))
		copy(s, t)
		return s
	default:
		return v
	}
}

func loadFollowers() {
	tempData := loadJSONOrDefault(config.FOLLOWERS_FILE_PATH, map[UserId]FollowerData{})

	validFollowersData := make(map[UserId]FollowerData)
	for k, v := range tempData {
		validFollowers := make([]UserId, 0)
		for _, follower := range v.Followers {
			if accountExists(follower) {
				validFollowers = append(validFollowers, follower)
			}
		}
		if accountExists(k) && len(validFollowers) > 0 {
			validFollowersData[k] = FollowerData{
				Followers: validFollowers,
				Username:  getUserById(k).GetUsername(),
				UserId:    k,
			}
		}
	}

	followersMutex.Lock()
	followersData = validFollowersData
	fcMap := make(map[UserId]int, len(validFollowersData))
	for _, v := range validFollowersData {
		for _, follower := range v.Followers {
			fcMap[follower]++
		}
	}
	followingCountMap = fcMap

	followersMutex.Unlock()

	log.Printf("Loaded %d followers", len(followersData))
}

func saveFollowers() {
	followersMutex.RLock()
	defer followersMutex.RUnlock()
	saveJsonFile(config.FOLLOWERS_FILE_PATH, followersData)
}

func loadJSONOrDefault[T any](path string, def T) T {
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("Error reading %s: %v", path, err)
		}
		return def
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		log.Printf("Error unmarshaling %s: %v", path, err)
		return def
	}
	return v
}

func loadPosts() {
	postsMutex.Lock()
	defer postsMutex.Unlock()
	posts = loadJSONOrDefault(config.LOCAL_POSTS_PATH, []Post{})
	log.Printf("Loaded %d posts", len(posts))
}

func savePosts() {
	postsMutex.RLock()
	defer postsMutex.RUnlock()
	saveJsonFile(config.LOCAL_POSTS_PATH, posts)
}

func loadItems() {
	itemsMutex.Lock()
	defer itemsMutex.Unlock()
	items = loadJSONOrDefault(config.ITEMS_FILE_PATH, []Item{})
	log.Printf("Loaded %d items", len(items))
}

func saveItems() {
	itemsMutex.RLock()
	defer itemsMutex.RUnlock()
	saveJsonFile(config.ITEMS_FILE_PATH, items)
}

func loadKeys() {
	keysMutex.Lock()
	defer keysMutex.Unlock()

	keys = loadJSONOrDefault(config.KEYS_FILE_PATH, []Key{})

	keyStringToIdxInner := make(map[string]int, len(keys))
	for i, k := range keys {
		keyStringToIdxInner[k.Key] = i
	}
	keyStringToIdx = keyStringToIdxInner

	log.Printf("Loaded %d keys", len(keys))
}

func saveKeys() {
	keysMutex.RLock()
	defer keysMutex.RUnlock()
	saveJsonFile(config.KEYS_FILE_PATH, keys)
}

func loadSystems() {
	systemsMutex.Lock()
	defer systemsMutex.Unlock()
	systems = loadJSONOrDefault(config.SYSTEMS_FILE_PATH, map[string]System{})
	log.Printf("Loaded %d systems", len(systems))
}

func saveSystems() {
	systemsMutex.RLock()
	defer systemsMutex.RUnlock()
	saveJsonFile(config.SYSTEMS_FILE_PATH, systems)
}

func loadEventsHistory() {
	eventsHistoryMutex.Lock()
	defer eventsHistoryMutex.Unlock()
	eventsHistory = loadJSONOrDefault(config.EVENTS_HISTORY_PATH, map[UserId][]Event{})
	log.Printf("Loaded %d events history", len(eventsHistory))
}

func saveEventsHistory() {
	eventsHistoryMutex.RLock()
	defer eventsHistoryMutex.RUnlock()
	saveJsonFile(config.EVENTS_HISTORY_PATH, eventsHistory)
}

var (
	saveLocksMu sync.Mutex
	saveLocks   = map[string]*sync.Mutex{}
)

func saveLockFor(path string) *sync.Mutex {
	saveLocksMu.Lock()
	defer saveLocksMu.Unlock()
	m, ok := saveLocks[path]
	if !ok {
		m = &sync.Mutex{}
		saveLocks[path] = m
	}
	return m
}

func saveJsonFile(path string, v any) bool {
	lock := saveLockFor(path)
	lock.Lock()
	defer lock.Unlock()

	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		log.Printf("Error marshaling JSON: %v", err)
		return false
	}

	if err := atomicWrite(path, data, 0644); err != nil {
		log.Printf("Error saving JSON file: %v", err)
		return false
	}
	return true
}

func flushAll() {
	flushDirtyUsers()
	savePosts()
	saveScheduledPosts()
	saveBookmarks()
	saveItems()
	saveKeys()
	saveFollowers()
	saveGifts()
	saveReports()
	saveEventsHistory()
	saveCosmeticGifts()
	saveAfdianOrders()
}

func watchFile(path string, reload func()) {
	var lastMtime time.Time
	if stat, err := os.Stat(path); err == nil {
		lastMtime = stat.ModTime()
	}

	for {
		time.Sleep(500 * time.Millisecond)
		if stat, err := os.Stat(path); err == nil {
			if stat.ModTime().After(lastMtime) {
				time.Sleep(500 * time.Millisecond)
				reload()
				lastMtime = stat.ModTime()
			}
		}
	}
}
