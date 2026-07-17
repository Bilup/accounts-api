package ofsf

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestEntry(t *testing.T, username string, uuid string, entry []any) {
	t.Helper()
	userDir := filepath.Join(fileDir, username)
	if err := os.MkdirAll(userDir, 0755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(FileMetadata{Entry: entry, Index: 0})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDir, uuid+".json"), data, 0644); err != nil {
		t.Fatal(err)
	}
}

func readTestEntry(t *testing.T, username string, uuid string) FileEntry {
	t.Helper()
	entry, err := readFileEntry(filepath.Join(fileDir, username, uuid+".json"))
	if err != nil {
		t.Fatalf("failed to read entry %s: %v", uuid, err)
	}
	return entry
}

func childUUIDs(t *testing.T, entry FileEntry) []string {
	t.Helper()
	children, ok := folderChildren(entry)
	if !ok {
		t.Fatalf("entry has no readable children: %v", entry)
	}
	out := make([]string, 0, len(children))
	for _, c := range children {
		s, isStr := c.(string)
		if !isStr {
			t.Fatalf("non-string child %v", c)
		}
		out = append(out, s)
	}
	return out
}

var fs = NewFileSystem()

const (
	rootUUID = "505e0c088eb1808f39768badca1e5ddd"
	binUUID  = "1771d2de24d410263bbc511b032d9054"
)

func TestRenameUserFileSystem(t *testing.T) {
	t.Chdir(t.TempDir())

	writeTestEntry(t, "allucat1000", rootUUID, []any{
		".folder", "Allucat1000", "origin/(c) users",
		[]any{binUUID},
		"", 0, 0, "", 1746023271378, 1746023271378, "", 0, []any{}, rootUUID,
	})
	writeTestEntry(t, "allucat1000", binUUID, []any{
		".folder", "Bin", "origin/(c) users/allucat1000",
		[]any{},
		"", 0, 0, "", 1746023271378, 1746023271378, "", 0, []any{}, binUUID,
	})

	fs.RenameUserFileSystem("allucat1000", "huopa")

	if fileExists(filepath.Join(fileDir, "allucat1000", rootUUID+".json")) {
		t.Error("old user directory should be gone")
	}

	root := readTestEntry(t, "huopa", rootUUID)
	if root[1] != "huopa" {
		t.Errorf("root name = %v, want huopa", root[1])
	}
	if root[2] != "origin/(c) users" {
		t.Errorf("root location = %v, want origin/(c) users", root[2])
	}

	bin := readTestEntry(t, "huopa", binUUID)
	if loc := entryToLocation(bin, "huopa"); loc != "origin/(c) users/huopa" {
		t.Errorf("bin resolved location = %v, want origin/(c) users/huopa", loc)
	}

	fs.mu.Lock()
	idx, err := fs.loadPathIndexUnsafe("huopa")
	fs.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if idx["origin/(c) users/huopa.folder"] != rootUUID {
		t.Errorf("index missing renamed root path: %v", idx)
	}
	if idx["origin/(c) users/huopa/bin.folder"] != binUUID {
		t.Errorf("index missing renamed bin path: %v", idx)
	}
	for path := range idx {
		if strings.Contains(path, "allucat1000") {
			t.Errorf("index still contains old username path: %s", path)
		}
	}
}

func TestWriteUserFileNoDuplicateFolders(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(filepath.Join(fileDir, "alice"), 0755); err != nil {
		t.Fatal(err)
	}

	path := "origin/(c) users/alice/application data/notify@rotur/endpoints.json"
	if err := fs.WriteUserFile("alice", path, `{"endpoints":[]}`); err != nil {
		t.Fatal(err)
	}
	if err := fs.WriteUserFile("alice", path, `{"endpoints":[{"a":1}]}`); err != nil {
		t.Fatal(err)
	}

	counts := map[string]int{}
	dirEntries, err := os.ReadDir(filepath.Join(fileDir, "alice"))
	if err != nil {
		t.Fatal(err)
	}
	var appDataEntry, notifyEntry FileEntry
	for _, de := range dirEntries {
		if de.Name() == ".index.json" {
			continue
		}
		entry, err := readFileEntry(filepath.Join(fileDir, "alice", de.Name()))
		if err != nil {
			continue
		}
		name := getStringOrEmpty(entry[1])
		counts[name]++
		switch name {
		case "application data":
			appDataEntry = entry
		case "notify@rotur":
			notifyEntry = entry
		}
	}
	for _, name := range []string{"application data", "notify@rotur", "endpoints"} {
		if counts[name] != 1 {
			t.Errorf("expected exactly one %q entry, got %d", name, counts[name])
		}
	}

	if got := fs.ReadUserFile("alice", path); got != `{"endpoints":[{"a":1}]}` {
		t.Errorf("file content = %s", got)
	}

	notifyUUID := getStringOrEmpty(notifyEntry[13])
	found := false
	for _, c := range childUUIDs(t, appDataEntry) {
		if c == notifyUUID {
			found = true
		}
	}
	if !found {
		t.Errorf("notify@rotur %s not attached to application data children", notifyUUID)
	}
}

func TestHandleAddPathCollision(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(filepath.Join(fileDir, "bob"), 0755); err != nil {
		t.Fatal(err)
	}

	first := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	second := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	makeEntry := func(uuid string) []any {
		return []any{
			".txt", "notes", "origin/(c) users/bob",
			"hello", nil, int64(0), int64(0), int64(1), int64(1), int64(1), "", 5, []any{}, uuid,
		}
	}

	fs.mu.Lock()
	err1 := fs.handleAddUnsafe("bob", UpdateChange{Command: "UUIDa", UUID: first, Dta: makeEntry(first)})
	err2 := fs.handleAddUnsafe("bob", UpdateChange{Command: "UUIDa", UUID: second, Dta: makeEntry(second)})
	idx, idxErr := fs.loadPathIndexUnsafe("bob")
	fs.mu.Unlock()
	if err1 != nil || err2 != nil || idxErr != nil {
		t.Fatal(err1, err2, idxErr)
	}

	if fileExists(filepath.Join(fileDir, "bob", first+".json")) {
		t.Error("stale duplicate entry should be removed on path collision")
	}
	if idx["origin/(c) users/bob/notes.txt"] != second {
		t.Errorf("index should point at newest uuid: %v", idx)
	}
}

func TestHandleAddPathCollisionPreservesOldFileWhenIndexSaveFails(t *testing.T) {
	t.Chdir(t.TempDir())
	userDir := filepath.Join(fileDir, "bob")
	if err := os.MkdirAll(userDir, 0755); err != nil {
		t.Fatal(err)
	}

	first := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	second := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	makeEntry := func(uuid string) []any {
		return []any{
			".txt", "notes", "origin/(c) users/bob",
			"hello", nil, int64(0), int64(0), int64(1), int64(1), int64(1), "", 5, []any{}, uuid,
		}
	}

	fs.mu.Lock()
	if err := fs.handleAddUnsafe("bob", UpdateChange{Command: "UUIDa", UUID: first, Dta: makeEntry(first)}); err != nil {
		fs.mu.Unlock()
		t.Fatal(err)
	}
	if err := os.Mkdir(userIndexPath("bob")+".tmp", 0755); err != nil {
		fs.mu.Unlock()
		t.Fatal(err)
	}
	err := fs.handleAddUnsafe("bob", UpdateChange{Command: "UUIDa", UUID: second, Dta: makeEntry(second)})
	idx, idxErr := fs.loadPathIndexUnsafe("bob")
	fs.mu.Unlock()

	if err == nil {
		t.Fatal("expected collision to fail when the index cannot be saved")
	}
	if idxErr != nil {
		t.Fatal(idxErr)
	}
	if idx["origin/(c) users/bob/notes.txt"] != first {
		t.Fatalf("index no longer points to original file: %v", idx)
	}
	if !fileExists(filepath.Join(userDir, first+".json")) {
		t.Fatal("original file was removed before the index update committed")
	}
	if fileExists(filepath.Join(userDir, second+".json")) {
		t.Fatal("new file was not rolled back after the index update failed")
	}
}

func TestRenameUserFileSystemPreservesIndexedDuplicate(t *testing.T) {
	t.Chdir(t.TempDir())

	authoritative := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	stale := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	writeTestEntry(t, "alice", rootUUID, []any{
		".folder", "alice", "origin/(c) users", []any{authoritative},
		"", 0, 0, "", 1, 1, "", 0, []any{}, rootUUID,
	})
	makeEntry := func(uuid string, content string) []any {
		return []any{
			".txt", "notes", "origin/(c) users/alice",
			content, nil, int64(0), int64(0), int64(1), int64(1), int64(1), "", len(content), []any{}, uuid,
		}
	}
	writeTestEntry(t, "alice", authoritative, makeEntry(authoritative, "current"))
	writeTestEntry(t, "alice", stale, makeEntry(stale, "stale"))

	fs.mu.Lock()
	err := fs.savePathIndexUnsafe("alice", PathIndex{
		"origin/(c) users/alice.folder":    rootUUID,
		"origin/(c) users/alice/notes.txt": authoritative,
	})
	fs.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	fs.RenameUserFileSystem("alice", "renamed")

	fs.mu.Lock()
	idx, err := fs.loadPathIndexUnsafe("renamed")
	fs.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if got := idx["origin/(c) users/renamed/notes.txt"]; got != authoritative {
		t.Fatalf("renamed index selected duplicate %q, want authoritative %q", got, authoritative)
	}
	if got := fs.ReadUserFile("renamed", "origin/(c) users/renamed/notes.txt"); got != "current" {
		t.Fatalf("renamed path content = %q, want current", got)
	}
}
