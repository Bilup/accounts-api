package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const kvStorageDir = "storage"

var errStorageQuota = errors.New("storage quota exceeded")

var (
	kvStorageLocksMu sync.Mutex
	kvStorageLocks   = map[string]*sync.Mutex{}
)

func kvStorageLockFor(userId UserId) *sync.Mutex {
	kvStorageLocksMu.Lock()
	defer kvStorageLocksMu.Unlock()
	m, ok := kvStorageLocks[string(userId)]
	if !ok {
		m = &sync.Mutex{}
		kvStorageLocks[string(userId)] = m
	}
	return m
}

func validStorageID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	if strings.Contains(id, "..") || strings.ContainsAny(id, `/\`) {
		return false
	}
	return true
}

func kvStorageUserDir(userId UserId) string {
	return filepath.Join(getUserDataDir(userId), kvStorageDir)
}

func kvStorageFilePath(userId UserId, id string) string {
	return filepath.Join(kvStorageUserDir(userId), id+".json")
}

func readKVStorage(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil || out == nil {
		return map[string]any{}
	}
	return out
}

func writeKVStorage(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return atomicWrite(path, b, 0644)
}

func kvStorageFileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func kvStorageUsageLocked(userId UserId) int64 {
	entries, err := os.ReadDir(kvStorageUserDir(userId))
	if err != nil {
		return 0
	}
	var total int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if info, err := e.Info(); err == nil {
			total += info.Size()
		}
	}
	return total
}

func kvStorageListLocked(userId UserId) []string {
	entries, err := os.ReadDir(kvStorageUserDir(userId))
	if err != nil {
		return []string{}
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(e.Name(), ".json"))
	}
	return ids
}

func kvStorageSet(userId UserId, id, key string, value any, maxBytes int64) (map[string]any, error) {
	lock := kvStorageLockFor(userId)
	lock.Lock()
	defer lock.Unlock()

	path := kvStorageFilePath(userId, id)
	data := readKVStorage(path)
	data[key] = value

	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, err
	}

	if maxBytes > 0 {
		projected := kvStorageUsageLocked(userId) - kvStorageFileSize(path) + int64(len(b))
		if projected > maxBytes {
			return nil, errStorageQuota
		}
	}

	if err := writeKVStorage(path, b); err != nil {
		return nil, err
	}
	return data, nil
}

func kvStorageGet(userId UserId, id string) map[string]any {
	lock := kvStorageLockFor(userId)
	lock.Lock()
	defer lock.Unlock()
	return readKVStorage(kvStorageFilePath(userId, id))
}

func kvStorageDelete(userId UserId, id, key string) (map[string]any, error) {
	lock := kvStorageLockFor(userId)
	lock.Lock()
	defer lock.Unlock()

	path := kvStorageFilePath(userId, id)
	data := readKVStorage(path)
	delete(data, key)

	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := writeKVStorage(path, b); err != nil {
		return nil, err
	}
	return data, nil
}

func kvStorageUsage(userId UserId) int64 {
	lock := kvStorageLockFor(userId)
	lock.Lock()
	defer lock.Unlock()
	return kvStorageUsageLocked(userId)
}

func kvStorageList(userId UserId) []string {
	lock := kvStorageLockFor(userId)
	lock.Lock()
	defer lock.Unlock()
	return kvStorageListLocked(userId)
}

func kvStorageClear(userId UserId, id string) (int, error) {
	lock := kvStorageLockFor(userId)
	lock.Lock()
	defer lock.Unlock()

	empty := map[string]any{}
	b, _ := json.MarshalIndent(empty, "", "  ")

	if id != "" {
		path := kvStorageFilePath(userId, id)
		if _, err := os.Stat(path); err != nil {
			return 0, nil
		}
		if err := writeKVStorage(path, b); err != nil {
			return 0, err
		}
		return 1, nil
	}

	dir := kvStorageUserDir(userId)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, nil
	}
	cleared := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if err := writeKVStorage(filepath.Join(dir, e.Name()), b); err == nil {
			cleared++
		}
	}
	return cleared, nil
}
