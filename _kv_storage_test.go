package main

import (
	"claw/internal/config"
	"os"
	"testing"
)

func withTempUserdata(t *testing.T) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "kv_storage_test_*")
	if err != nil {
		t.Fatal(err)
	}
	origPath := config.USERDATA_PATH
	config.USERDATA_PATH = tmpDir
	t.Cleanup(func() {
		config.USERDATA_PATH = origPath
		os.RemoveAll(tmpDir)
	})
}

func TestKVStorageSetGetDelete(t *testing.T) {
	withTempUserdata(t)
	uid := UserId("id_kvuser")

	data, err := kvStorageSet(uid, "prefs", "theme", "dark", 0)
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if data["theme"] != "dark" {
		t.Fatalf("set returned %v, want theme=dark", data)
	}

	if _, err := kvStorageSet(uid, "prefs", "lang", "en", 0); err != nil {
		t.Fatalf("set 2: %v", err)
	}

	got := kvStorageGet(uid, "prefs")
	if got["theme"] != "dark" || got["lang"] != "en" {
		t.Fatalf("get returned %v", got)
	}

	got = kvStorageGet(uid, "missing")
	if len(got) != 0 {
		t.Fatalf("get missing returned %v, want empty", got)
	}

	after, err := kvStorageDelete(uid, "prefs", "theme")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := after["theme"]; ok {
		t.Fatalf("delete left theme in %v", after)
	}
	if after["lang"] != "en" {
		t.Fatalf("delete removed the wrong key: %v", after)
	}
}

func TestKVStorageStructuredValue(t *testing.T) {
	withTempUserdata(t)
	uid := UserId("id_kvstruct")

	nested := map[string]any{"a": float64(1), "b": []any{"x", "y"}}
	if _, err := kvStorageSet(uid, "doc", "obj", nested, 0); err != nil {
		t.Fatalf("set: %v", err)
	}

	got := kvStorageGet(uid, "doc")["obj"]
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("value round-tripped as %T, want map", got)
	}
	if m["a"] != float64(1) {
		t.Fatalf("nested value mismatch: %v", m)
	}
}

func TestKVStorageUsageAndList(t *testing.T) {
	withTempUserdata(t)
	uid := UserId("id_kvusage")

	if u := kvStorageUsage(uid); u != 0 {
		t.Fatalf("empty usage = %d, want 0", u)
	}
	if ids := kvStorageList(uid); len(ids) != 0 {
		t.Fatalf("empty list = %v", ids)
	}

	kvStorageSet(uid, "a", "k", "v", 0)
	kvStorageSet(uid, "b", "k", "v", 0)

	ids := kvStorageList(uid)
	if len(ids) != 2 {
		t.Fatalf("list = %v, want 2 ids", ids)
	}
	if kvStorageUsage(uid) <= 0 {
		t.Fatalf("usage should be > 0 after writes")
	}
}

func TestKVStorageQuota(t *testing.T) {
	withTempUserdata(t)
	uid := UserId("id_kvquota")

	if _, err := kvStorageSet(uid, "big", "k", "small", 10); err != errStorageQuota {
		t.Fatalf("tiny quota err = %v, want errStorageQuota", err)
	}

	if _, err := kvStorageSet(uid, "big", "k", "small", 10_000); err != nil {
		t.Fatalf("generous quota err = %v", err)
	}
}

func TestKVStorageClear(t *testing.T) {
	withTempUserdata(t)
	uid := UserId("id_kvclear")

	kvStorageSet(uid, "a", "k1", "v", 0)
	kvStorageSet(uid, "a", "k2", "v", 0)
	kvStorageSet(uid, "b", "k", "v", 0)

	cleared, err := kvStorageClear(uid, "a")
	if err != nil {
		t.Fatalf("clear id: %v", err)
	}
	if cleared != 1 {
		t.Fatalf("clear id returned %d, want 1", cleared)
	}
	if len(kvStorageGet(uid, "a")) != 0 {
		t.Fatalf("cleared id still has data")
	}
	if len(kvStorageGet(uid, "b")) == 0 {
		t.Fatalf("clear id wiped the wrong id")
	}

	cleared, err = kvStorageClear(uid, "")
	if err != nil {
		t.Fatalf("clear all: %v", err)
	}
	if cleared != 2 {
		t.Fatalf("clear all returned %d, want 2", cleared)
	}
}

func TestValidStorageID(t *testing.T) {
	cases := map[string]bool{
		"prefs":       true,
		"my.doc-1":    true,
		"":            false,
		"../escape":   false,
		"a/b":         false,
		`a\b`:         false,
	}
	for id, want := range cases {
		if got := validStorageID(id); got != want {
			t.Errorf("validStorageID(%q) = %v, want %v", id, got, want)
		}
	}
}
