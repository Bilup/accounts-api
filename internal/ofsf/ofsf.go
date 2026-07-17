// Package ofsf implements the Origin File System Format engine: a per-user
// on-disk file store with a path index, used by the /files endpoints.
package ofsf

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

func getStringOrEmpty(val any) string {
	if s, ok := val.(string); ok {
		return s
	}
	return ""
}

func generateToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("failed to generate token: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func jsonString(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(data)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func copyAndReplace(src, dst, old, new string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, []byte(strings.ReplaceAll(string(data), old, new)), 0644)
}

type FileMetadata struct {
	Entry FileEntry `json:"entry"`
	Index int       `json:"index"`
}

type FileEntry []any

type FileStat struct {
	UUID    string    `json:"uuid"`
	Size    int64     `json:"size,omitempty"`
	ModTime time.Time `json:"mtime,omitempty"`
	Ok      bool      `json:"ok"`
}

func ofsfErrorf(format string, a ...any) {
	fmt.Printf("\033[91m[-] OFSF Error\033[0m | "+format+"\n", a...)
}

func ofsfOkf(format string, a ...any) {
	fmt.Printf("\033[92m[+] OFSF\033[0m | "+format+"\n", a...)
}

func ofsfWarnf(format string, a ...any) {
	fmt.Printf("\033[93m[~] OFSF\033[0m | "+format+"\n", a...)
}

const (
	fileEntrySize = 14
	fileDir       = "./rotur/files"

	defaultOFSF = "./rotur/base.ofsf"
)

type UpdateChange struct {
	Command string `json:"command"`
	UUID    string `json:"uuid"`
	Dta     any    `json:"dta"`
	Idx     any    `json:"idx"`
}

type UpdateResult struct {
	Payload       string `json:"payload"`
	UsedSize      int    `json:"used_size,omitempty"`
	AvailableSize int    `json:"available_size,omitempty"`
}

type FileSystem struct {
	mu sync.RWMutex
}

func NewFileSystem() *FileSystem {
	return &FileSystem{}
}

func formatOFSPath(username string, dir string) string {
	basePath := "origin/(c) users/" + string(username) + "/"
	formatted := strings.Trim(dir, "/")
	return strings.TrimSuffix(basePath+formatted, "/")
}

func (fs *FileSystem) ensureFoldersUnsafe(username string, dir string) error {
	dir = strings.TrimSuffix(strings.TrimSuffix(dir, "/"), " ")
	if dir == "" || dir == "/" {
		return nil
	}

	parts := strings.Split(strings.TrimPrefix(dir, "/"), "/")
	index, _ := fs.loadPathIndexUnsafe(username)

	for i := 1; i <= len(parts); i++ {
		folderPath := formatOFSPath(username, strings.Join(parts[:i], "/"))
		if _, exists := index[strings.ToLower(folderPath)+".folder"]; exists {
			continue
		}

		now := time.Now().UnixMilli()
		uuid := generateToken()
		location := formatOFSPath(username, strings.Join(parts[:i-1], "/"))

		entry := []any{
			".folder",  // [0] type
			parts[i-1], // [1] name
			location,   // [2] location
			[]any{},    // [3] data
			nil,        // [4] data_secondary
			int64(0),   // [5] x
			int64(0),   // [6] y
			now,        // [7] id
			now,        // [8] created
			now,        // [9] edited
			"",         // [10] icon
			0,          // [11] size
			[]string{}, // [12] permissions
			uuid,       // [13] uuid
		}

		if err := fs.handleAddUnsafe(username, UpdateChange{
			Command: "UUIDa",
			UUID:    uuid,
			Dta:     entry,
		}); err != nil {
			return fmt.Errorf("failed to create folder %q: %w", folderPath, err)
		}

		index, _ = fs.loadPathIndexUnsafe(username)
	}
	return nil
}

func (fs *FileSystem) WriteUserFileUnsafe(username string, fullPath string, content string) error {
	now := time.Now().UnixMilli()
	lowerPath := strings.ToLower(fullPath)
	index, _ := fs.loadPathIndexUnsafe(username)

	prefix := strings.ToLower("origin/(c) users/" + string(username) + "/")
	relativePath := strings.TrimPrefix(lowerPath, prefix)
	lastSlash := strings.LastIndex(relativePath, "/")
	var dir string
	if lastSlash > 0 {
		dir = relativePath[:lastSlash]
	}

	if err := fs.ensureFoldersUnsafe(username, dir); err != nil {
		return err
	}

	index, _ = fs.loadPathIndexUnsafe(username)

	fileUUID, exists := index[lowerPath]
	if exists {
		entry, err := fs.getFileByUUIDUnsafe(username, fileUUID)
		if err != nil {
			return fmt.Errorf("failed to read existing file: %w", err)
		}
		if len(entry) < 14 {
			return fmt.Errorf("existing file entry is malformed")
		}
		entry[3] = content
		entry[8] = now // edited time
		entry[11] = len(content)
		if err := fs.setFileByUUIDUnsafe(username, fileUUID, entry); err != nil {
			return fmt.Errorf("failed to update file: %w", err)
		}
	} else {
		fileUUID = generateToken()

		fileName := relativePath
		if lastSlash >= 0 {
			fileName = relativePath[lastSlash+1:]
		}
		ext := ""
		name := fileName
		if dotIdx := strings.LastIndex(fileName, "."); dotIdx > 0 {
			ext = fileName[dotIdx:]
			name = fileName[:dotIdx]
		}

		location := formatOFSPath(username, dir)
		entry := []any{
			ext,          // [0] type
			name,         // [1] name
			location,     // [2] location
			content,      // [3] data
			nil,          // [4] data_secondary
			int64(0),     // [5] x
			int64(0),     // [6] y
			now,          // [7] id
			now,          // [8] created
			now,          // [9] edited
			"",           // [10] icon
			len(content), // [11] size
			[]string{},   // [12] permissions
			fileUUID,     // [13] uuid
		}

		if err := fs.handleAddUnsafe(username, UpdateChange{
			Command: "UUIDa",
			UUID:    fileUUID,
			Dta:     entry,
		}); err != nil {
			return fmt.Errorf("failed to create file: %w", err)
		}
	}
	return nil
}

func (fs *FileSystem) WriteUserFile(username string, fullPath string, content string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.WriteUserFileUnsafe(username, fullPath, content)
}

func (fs *FileSystem) ReadUserFile(username string, fullPath string) string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.ReadUserFileUnsafe(username, fullPath)
}

func (fs *FileSystem) ReadUserFileUnsafe(username string, fullPath string) string {
	index, _ := fs.loadPathIndexUnsafe(username)
	fileUUID, ok := index[strings.ToLower(fullPath)]
	if !ok {
		return ""
	}
	entry, err := fs.getFileByUUIDUnsafe(username, fileUUID)
	if err != nil || len(entry) < 4 {
		return ""
	}
	dataStr, ok := entry[3].(string)
	if !ok {
		return ""
	}
	return dataStr
}

func (fs *FileSystem) HandleOFSFUpdate(username string, updates []UpdateChange, maxSize int) UpdateResult {

	ofsfOkf("%s processing %d file updates", username, len(updates))

	fs.MigrateOrLog(username)

	// Process all updates while holding the lock
	fs.mu.Lock()
	for _, change := range updates {
		switch change.Command {
		case "UUIDa":
			if err := fs.handleAddUnsafe(username, change); err != nil {
				log.Printf("[OFSF] Error handling UUIDa for %s: %v", username, err)
			}
		case "UUIDr":
			fs.handleReplaceUnsafe(username, change)
		case "UUIDd":
			fs.handleDeleteUnsafe(username, change)
		}
	}
	fs.mu.Unlock()

	usedSize, err := fs.calculateTotalSize(username)
	if err != nil {
		return UpdateResult{Payload: "Error calculating size"}
	}

	availableSize := maxSize - usedSize

	if usedSize > maxSize {
		ofsfErrorf("User %s exceeded upload storage limit (used: %d, available: %d)",
			username, usedSize, availableSize)
		return UpdateResult{
			Payload:       "Max Upload Size Exceeded",
			UsedSize:      usedSize,
			AvailableSize: availableSize,
		}
	}

	ofsfOkf("Updated %s files (used: %d, available: %d)",
		username, usedSize, availableSize)

	return UpdateResult{
		Payload:       "Successfully Updated Origin Files",
		UsedSize:      usedSize,
		AvailableSize: availableSize,
	}
}

func extractIndex(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x) - 1
	case int:
		return x - 1
	case string:
		var i int
		fmt.Sscanf(x, "%d", &i)
		return i - 1
	default:
		return 0
	}
}

func isValidFileUUID(uuid string) bool {
	if len(uuid) != 32 {
		return false
	}
	for _, c := range uuid {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// handleAddUnsafe assumes the lock is already held
func (fs *FileSystem) handleAddUnsafe(username string, change UpdateChange) error {
	if !isValidFileUUID(change.UUID) {
		return fmt.Errorf("invalid UUID")
	}
	userDir := filepath.Join(fileDir, string(username))
	if err := os.MkdirAll(userDir, 0755); err != nil {
		log.Printf("Error creating user directory %s: %v", userDir, err)
		return fmt.Errorf("failed to create user directory: %w", err)
	}
	path := filepath.Join(userDir, change.UUID+".json")

	if _, err := os.Stat(path); err == nil {
		return nil
	}

	dta, ok := change.Dta.([]any)
	if !ok || len(dta) != fileEntrySize {
		return fmt.Errorf("invalid entry data")
	}

	dta[7] = time.Now().UnixMilli()
	dta[8] = dta[7]

	meta := FileMetadata{
		Entry: dta,
		Index: 0,
	}

	data, err := json.Marshal(meta)
	if err != nil {
		log.Printf("Error marshaling metadata: %v", err)
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		log.Printf("Error writing file %s: %v", path, err)
		return fmt.Errorf("failed to write file: %w", err)
	}

	// Load and update path index (unsafe version - no locking)
	idx, _ := fs.loadPathIndexUnsafe(username)
	entryPath := entryToPath(dta, username)
	replacedUUID := ""
	if oldUUID, ok := idx[entryPath]; ok && oldUUID != change.UUID && isValidFileUUID(oldUUID) {
		replacedUUID = oldUUID
	}
	idx[entryPath] = change.UUID
	if err := fs.savePathIndexUnsafe(username, idx); err != nil {
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			log.Printf("Error rolling back file %s after index update failure: %v", path, removeErr)
		}
		return fmt.Errorf("failed to update path index: %w", err)
	}
	if replacedUUID != "" {
		if err := os.Remove(filepath.Join(userDir, replacedUUID+".json")); err != nil && !os.IsNotExist(err) {
			log.Printf("Error removing replaced file %s for %s: %v", replacedUUID, username, err)
		}
	}
	fs.attachToParentUnsafe(username, dta, change.UUID, replacedUUID, idx)
	return nil
}

func folderChildren(entry FileEntry) ([]any, bool) {
	if getStringOrEmpty(entry[0]) != ".folder" {
		return nil, false
	}
	switch data := entry[3].(type) {
	case []any:
		return data, true
	case string:
		var arr []any
		if json.Unmarshal([]byte(data), &arr) == nil {
			return arr, true
		}
	case nil:
		return []any{}, true
	}
	return nil, false
}

// attachToParentUnsafe assumes the lock is already held
func (fs *FileSystem) attachToParentUnsafe(username string, entry FileEntry, uuid string, replacedUUID string, idx PathIndex) {
	parentUUID, ok := idx[entryToLocation(entry, username)+".folder"]
	if !ok || parentUUID == uuid {
		return
	}
	parent, err := fs.getFileByUUIDUnsafe(username, parentUUID)
	if err != nil || len(parent) != fileEntrySize {
		return
	}
	children, ok := folderChildren(parent)
	if !ok {
		return
	}
	changed := false
	found := false
	result := make([]any, 0, len(children)+1)
	for _, child := range children {
		s, _ := child.(string)
		if replacedUUID != "" && s == replacedUUID {
			changed = true
			continue
		}
		if s == uuid {
			found = true
		}
		result = append(result, child)
	}
	if !found {
		result = append(result, uuid)
		changed = true
	}
	if changed {
		parent[3] = result
		fs.setFileByUUIDUnsafe(username, parentUUID, parent)
	}
}

// handleReplaceUnsafe assumes the lock is already held
func (fs *FileSystem) handleReplaceUnsafe(username string, change UpdateChange) error {
	entry, err := fs.getFileByUUIDUnsafe(username, change.UUID)
	if err != nil {
		return err
	}

	oldPath := entryToPath(entry, username)

	idx := extractIndex(change.Idx)
	entry[8] = time.Now().UnixMilli()
	if idx >= 0 && idx < len(entry) {
		entry[idx] = change.Dta
	}

	newPath := entryToPath(entry, username)

	if err := fs.setFileByUUIDUnsafe(username, change.UUID, entry); err != nil {
		return err
	}

	if oldPath != newPath {
		index, _ := fs.loadPathIndexUnsafe(username)
		delete(index, oldPath)
		index[newPath] = change.UUID
		fs.savePathIndexUnsafe(username, index)
	}
	return nil
}

// handleDeleteUnsafe assumes the lock is already held
func (fs *FileSystem) handleDeleteUnsafe(username string, change UpdateChange) error {
	if !isValidFileUUID(change.UUID) {
		return fmt.Errorf("invalid UUID")
	}
	filePath := filepath.Join(fileDir, string(username), change.UUID+".json")
	os.Remove(filePath)

	idx, _ := fs.loadPathIndexUnsafe(username)
	for path, uuid := range idx {
		if uuid == change.UUID {
			delete(idx, path)
			break
		}
	}

	fs.savePathIndexUnsafe(username, idx)
	return nil
}

func userIndexPath(username string) string {
	return filepath.Join(fileDir, string(username), ".index.json")
}

func (fs *FileSystem) RenameUserFileSystem(oldUsername string, newUsername string) {
	fs.MigrateOrLog(oldUsername)

	fs.mu.Lock()
	defer fs.mu.Unlock()

	oldUserDir := filepath.Join(fileDir, string(oldUsername))
	newUserDir := filepath.Join(fileDir, string(newUsername))
	oldIndex, err := fs.loadPathIndexUnsafe(oldUsername)
	if err != nil {
		ofsfErrorf("Failed to load path index for %s: %v", oldUsername, err)
		return
	}
	preferred := make(PathIndex, len(oldIndex))
	for _, uuid := range oldIndex {
		entry, err := fs.getFileByUUIDUnsafe(oldUsername, uuid)
		if err != nil || len(entry) != fileEntrySize {
			continue
		}
		preferred[entryToPath(entry, newUsername)] = uuid
	}
	if err := os.Rename(oldUserDir, newUserDir); err != nil {
		if !os.IsNotExist(err) {
			ofsfErrorf("Failed to rename user directory: %v", err)
		}
		return
	}

	index, err := fs.rebuildPathIndexWithPreferredUnsafe(newUsername, preferred)
	if err != nil {
		ofsfErrorf("Failed to rebuild path index for %s: %v", newUsername, err)
		return
	}

	rootKey := strings.ToLower("origin/(c) users/" + string(oldUsername) + ".folder")
	rootUUID, ok := index[rootKey]
	if !ok {
		return
	}
	root, err := fs.getFileByUUIDUnsafe(newUsername, rootUUID)
	if err != nil || len(root) != fileEntrySize {
		return
	}
	root[1] = string(newUsername)
	if err := fs.setFileByUUIDUnsafe(newUsername, rootUUID, root); err != nil {
		ofsfErrorf("Failed to update root folder name for %s: %v", newUsername, err)
		return
	}
	delete(index, rootKey)
	index[strings.ToLower("origin/(c) users/"+string(newUsername)+".folder")] = rootUUID
	fs.savePathIndexUnsafe(newUsername, index)
}

type PathIndex map[string]string

// rebuildPathIndexUnsafe assumes the lock is already held
func (fs *FileSystem) rebuildPathIndexUnsafe(username string) (PathIndex, error) {
	return fs.rebuildPathIndexWithPreferredUnsafe(username, nil)
}

// rebuildPathIndexWithPreferredUnsafe assumes the lock is already held. When
// duplicate files resolve to one path, preferred records the authoritative UUID.
func (fs *FileSystem) rebuildPathIndexWithPreferredUnsafe(username string, preferred PathIndex) (PathIndex, error) {
	userDir := filepath.Join(fileDir, string(username))

	idx := make(PathIndex)

	entries, err := os.ReadDir(userDir)
	if err != nil {
		if os.IsNotExist(err) {
			if err := fs.savePathIndexUnsafe(username, idx); err != nil {
				return nil, err
			}
			return idx, nil
		}
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		if entry.Name() == ".index.json" {
			continue
		}

		filePath := filepath.Join(userDir, entry.Name())

		fileEntry, err := readFileEntry(filePath)
		if err != nil {
			continue
		}

		path := entryToPath(fileEntry, username)
		uuid := strings.TrimSuffix(entry.Name(), ".json")

		current, exists := idx[path]
		preferredUUID, hasPreferred := preferred[path]
		if !exists || (hasPreferred && uuid == preferredUUID && current != preferredUUID) {
			idx[path] = uuid
		}
	}

	if err := fs.savePathIndexUnsafe(username, idx); err != nil {
		return nil, err
	}

	ofsfWarnf("Rebuilt path index for %s (%d entries)",
		username, len(idx))

	return idx, nil
}

// loadPathIndexUnsafe assumes the lock is already held
func (fs *FileSystem) loadPathIndexUnsafe(username string) (PathIndex, error) {
	path := userIndexPath(username)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fs.rebuildPathIndexUnsafe(username)
		}
		return nil, err
	}

	var idx PathIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return fs.rebuildPathIndexUnsafe(username)
	}

	return idx, nil
}

// loadPathIndex is the public version that acquires the lock
func (fs *FileSystem) LoadPathIndex(username string) (PathIndex, error) {
	fs.MigrateOrLog(username)

	path := userIndexPath(username)

	// First check if index exists with read lock
	fs.mu.RLock()
	_, err := os.Stat(path)
	fs.mu.RUnlock()

	if err == nil {
		fs.mu.RLock()
		data, readErr := os.ReadFile(path)
		fs.mu.RUnlock()

		if readErr == nil {
			fmt.Println("Loading path index for", username)
			var idx PathIndex
			if unmarshalErr := json.Unmarshal(data, &idx); unmarshalErr == nil {
				return idx, nil
			}
		}
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	fmt.Println("Rebuilding path index for", username)

	return fs.rebuildPathIndexUnsafe(username)
}

// savePathIndexUnsafe assumes the lock is already held
func (fs *FileSystem) savePathIndexUnsafe(username string, idx PathIndex) error {
	path := userIndexPath(username)

	tmp := path + ".tmp"
	data, err := json.Marshal(idx)
	if err != nil {
		return err
	}

	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}

	return os.Rename(tmp, path) // atomic on POSIX
}

func entryToLocation(entry FileEntry, username string) string {
	location := strings.ToLower(getStringOrEmpty(entry[2]))
	if strings.HasPrefix(location, "origin/(c) users/") {
		parts := strings.Split(location, "/")
		if len(parts) >= 3 {
			rest := parts[3:]
			location = "origin/(c) users/" + strings.ToLower(username)
			if len(rest) > 0 {
				location += "/" + strings.Join(rest, "/")
			}
		}
	}
	return location
}

func joinNoClean(a, b string) string {
	a = strings.TrimRight(a, "/")
	b = strings.TrimLeft(b, "/")

	if a == "" {
		return b
	}
	if b == "" {
		return a
	}

	return a + "/" + b
}

func entryToPath(entry FileEntry, username string) string {
	name := getStringOrEmpty(entry[1]) + getStringOrEmpty(entry[0])

	return strings.ToLower(
		joinNoClean(
			entryToLocation(entry, username),
			name,
		),
	)
}

func (fs *FileSystem) GetFileStats(username string, uuids []string) ([]FileStat, error) {
	if err := fs.migrateFromLegacy(username); err != nil {
		return nil, err
	}

	fs.mu.RLock()
	defer fs.mu.RUnlock()

	userDir := filepath.Join(fileDir, string(username))
	stats := make([]FileStat, 0, len(uuids))

	for _, uuid := range uuids {
		if !isValidFileUUID(uuid) {
			stats = append(stats, FileStat{
				UUID: uuid,
				Ok:   false,
			})
			continue
		}
		path := filepath.Join(userDir, uuid+".json")

		info, err := os.Stat(path)
		if err != nil {
			stats = append(stats, FileStat{
				UUID: uuid,
				Ok:   false,
			})
			continue
		}

		stats = append(stats, FileStat{
			UUID:    uuid,
			Size:    info.Size(),
			ModTime: info.ModTime().UTC(),
			Ok:      true,
		})
	}

	return stats, nil
}

func (fs *FileSystem) GetUserPath(username string) string {
	return filepath.Join(fileDir, string(username))
}

func (fs *FileSystem) MigrateOrLog(username string) {
	if err := fs.migrateFromLegacy(username); err != nil {
		ofsfErrorf("Migration failed: %v", err)
	}
}

func (fs *FileSystem) migrateFromLegacy(username string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	legacyPath := filepath.Join(fileDir, string(username)+".ofsf")

	newPath := filepath.Join(fileDir, string(username))
	if info, err := os.Stat(newPath); err == nil && info.IsDir() {
		return nil
	}
	if !fileExists(legacyPath) {
		copyAndReplace(defaultOFSF, legacyPath, "${USERNAME}", string(username))
	}

	ofsfWarnf("Migrating %s from legacy format", username)

	data, err := os.ReadFile(legacyPath)
	if err != nil {
		return err
	}

	if len(data) == 0 {
		os.Remove(legacyPath)
		return nil
	}

	var filesList []any
	if err := json.Unmarshal(data, &filesList); err != nil {
		return err
	}

	userDir := fs.GetUserPath(username)
	if err := os.MkdirAll(userDir, 0755); err != nil {
		return err
	}

	pathIndex := PathIndex{}

	remap := map[string]string{}
	for i := 0; i+fileEntrySize <= len(filesList); i += fileEntrySize {
		if uuid, ok := filesList[i+13].(string); ok && uuid != "" && !isValidFileUUID(uuid) {
			if _, exists := remap[uuid]; !exists {
				remap[uuid] = generateToken()
			}
		}
	}

	index := 0
	for i := 0; i+fileEntrySize <= len(filesList); i += fileEntrySize {
		entry := filesList[i : i+fileEntrySize]
		uuid, ok := entry[13].(string)
		if !ok {
			continue
		}
		if newUUID, exists := remap[uuid]; exists {
			uuid = newUUID
			entry[13] = newUUID
		}
		if !isValidFileUUID(uuid) {
			continue
		}
		if len(remap) > 0 {
			if children, hasChildren := folderChildren(entry); hasChildren {
				for j, child := range children {
					if s, isStr := child.(string); isStr {
						if newUUID, exists := remap[s]; exists {
							children[j] = newUUID
						}
					}
				}
				entry[3] = children
			}
		}
		metadata := FileMetadata{
			Entry: entry,
			Index: index,
		}
		internalPath := entryToPath(entry, username)
		pathIndex[internalPath] = uuid
		entryData, err := json.Marshal(metadata)
		if err != nil {
			log.Printf("Error marshaling entry data: %v", err)
			continue
		}
		filePath := filepath.Join(userDir, uuid+".json")
		if err := os.WriteFile(filePath, entryData, 0644); err != nil {
			log.Printf("Error writing file %s: %v", filePath, err)
			continue
		}
		index++
	}

	filePath := filepath.Join(userDir, ".index.json")
	data, err = json.Marshal(pathIndex)
	if err == nil {
		if writeErr := os.WriteFile(filePath, data, 0644); writeErr != nil {
			log.Printf("Error writing index file %s: %v", filePath, writeErr)
		}
	} else {
		log.Printf("Error marshaling path index: %v", err)
	}

	os.Remove(legacyPath)
	ofsfOkf("Migration complete for %s", username)

	return nil
}

func readFileEntry(path string) (FileEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var metadata FileMetadata
	if err := json.Unmarshal(data, &metadata); err != nil || metadata.Entry == nil {
		return nil, fmt.Errorf("invalid file entry")
	}
	return metadata.Entry, nil
}

// getFileByUUIDUnsafe assumes the lock is already held
func (fs *FileSystem) getFileByUUIDUnsafe(username string, uuid string) (FileEntry, error) {
	if !isValidFileUUID(uuid) {
		return nil, fmt.Errorf("invalid UUID")
	}
	userDir := fs.GetUserPath(username)

	entry, err := readFileEntry(filepath.Join(userDir, uuid+".json"))
	if err != nil {
		return nil, fmt.Errorf("file not found with the provided UUID")
	}

	if entry[0] != ".folder" {
		switch entry[3].(type) {
		case map[string]any, []any:
			entry[3] = jsonString(entry[3])
		}
	}
	return entry, nil
}

// GetFileByUUID is the public version that acquires the lock
func (fs *FileSystem) GetFileByUUID(username string, uuid string) (FileEntry, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	return fs.getFileByUUIDUnsafe(username, uuid)
}

// setFileByUUIDUnsafe assumes the lock is already held
func (fs *FileSystem) setFileByUUIDUnsafe(username string, uuid string, file FileEntry) error {
	if !isValidFileUUID(uuid) {
		return fmt.Errorf("invalid UUID")
	}
	userDir := fs.GetUserPath(username)
	if err := os.MkdirAll(userDir, 0755); err != nil {
		return fmt.Errorf("failed to create user directory: %w", err)
	}

	filePath := filepath.Join(userDir, uuid+".json")

	if file[0] != ".folder" {
		switch file[3].(type) {
		case map[string]any, []any:
			file[3] = jsonString(file[3])
		}
	}

	data, err := json.Marshal(FileMetadata{
		Entry: file,
		Index: 0,
	})

	if err != nil {
		return err
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return err
	}

	return nil
}

func (fs *FileSystem) calculateTotalSize(username string) (int, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	userDir := fs.GetUserPath(username)
	totalSize := 0

	entries, err := os.ReadDir(userDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		totalSize += int(info.Size())
	}

	return totalSize, nil
}

func (fs *FileSystem) GetFilesByUUIDs(username string, uuids []string) (map[string]FileEntry, error) {
	fs.MigrateOrLog(username)
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	result := make(map[string]FileEntry)
	userDir := fs.GetUserPath(username)

	for _, uuid := range uuids {
		if !isValidFileUUID(uuid) {
			continue
		}
		filePath := filepath.Join(userDir, uuid+".json")

		if entry, err := readFileEntry(filePath); err == nil {
			result[uuid] = entry
		}
	}

	return result, nil
}

func (fs *FileSystem) DeleteUserFileSystem(username string) error {
	fs.MigrateOrLog(username)
	fs.mu.Lock()
	defer fs.mu.Unlock()

	userDir := fs.GetUserPath(username)

	if err := os.RemoveAll(userDir); err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("No files found for user %s to delete\n", username)
			return nil
		}
		return err
	}

	legacyPath := filepath.Join(fileDir, string(username)+".ofsf")
	os.Remove(legacyPath)

	fmt.Printf("Successfully deleted files for user %s\n", username)
	return nil
}

func (fs *FileSystem) GetUserFileSize(username string) (string, error) {
	fs.MigrateOrLog(username)
	size, err := fs.calculateTotalSize(username)
	if err != nil {
		return "", err
	}

	switch {
	case size >= 1<<30:
		return fmt.Sprintf("%.4f GB", float64(size)/(1<<30)), nil
	case size >= 1<<20:
		return fmt.Sprintf("%.2f MB", float64(size)/(1<<20)), nil
	case size >= 1<<10:
		return fmt.Sprintf("%.2f KB", float64(size)/(1<<10)), nil
	default:
		return fmt.Sprintf("%d bytes", size), nil
	}
}

func (fs *FileSystem) GetFilesIndexWithThreshold(username string, sizeThreshold int) ([]any, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	userDir := fs.GetUserPath(username)
	entries, err := os.ReadDir(userDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var allEntries []FileMetadata

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		filePath := filepath.Join(userDir, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		var metadata FileMetadata
		if err := json.Unmarshal(data, &metadata); err == nil && metadata.Entry != nil {
			entryCopy := make(FileEntry, len(metadata.Entry))
			copy(entryCopy, metadata.Entry)

			if sizeThreshold > 0 && len(entryCopy) == 14 {
				if entryCopy[0] == ".folder" {
					arr, ok := entryCopy[3].([]any)
					if ok {
						entryCopy[3] = jsonString(arr)
						entryCopy[11] = len(arr)
					}
				} else {
					dataStr := ""
					switch entryCopy[3].(type) {
					case string:
						dataStr = entryCopy[3].(string)
					case []any, map[string]any:
						dataStr = jsonString(entryCopy[3])
						entryCopy[3] = dataStr
					}
					entryCopy[11] = len(dataStr)
					if entryCopy[11].(int) > sizeThreshold {
						entryCopy[3] = false
					}
				}
			}

			entryCopy[2] = entryToLocation(entryCopy, username)
			metadata.Entry = entryCopy
			allEntries = append(allEntries, metadata)
		}
	}

	sort.Slice(allEntries, func(i, j int) bool {
		return len(allEntries[i].Entry[2].(string)) < len(allEntries[j].Entry[2].(string))
	})

	result := make([]any, 0)
	for _, meta := range allEntries {
		result = append(result, meta.Entry...)
	}

	return result, nil
}
