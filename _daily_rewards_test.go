package main

import (
	"claw/internal/config"
	"path/filepath"
	"testing"
)

// resetUserStoreForTest rebuilds the in-memory user store with only the
// given usernames, each on the default "rotur" system. Every other index is
// cleared so tests don't leak state across cases.
func resetUserStoreForTest(t *testing.T, names ...string) {
	t.Helper()

	usersMutex.Lock()
	users = nil
	usersMutex.Unlock()

	idToUserMutex.Lock()
	usernameToId = make(map[Username]UserId)
	idToUser = make(map[UserId]User)
	keyToId = make(map[string]UserId)
	emailToId = make(map[string]UserId)
	idToUserMutex.Unlock()

	dirtyUsersMu.Lock()
	dirtyUsers = make(map[UserId]struct{})
	dirtyUsersMu.Unlock()

	for _, n := range names {
		u := User{
			"username":         n,
			"email":            n + "@test.local",
			"password":         "",
			"key":              "key_" + n,
			"system":           "rotur",
			"sys.id":           "id_" + n,
			"sys.currency":     float64(0),
			"sys.transactions": []any{},
			"sys.tos_accepted": true,
			"sys.index":        int64(len(users) + 1),
		}

		usersMutex.Lock()
		users = append(users, u)
		usersMutex.Unlock()

		idToUserMutex.Lock()
		usernameToId[Username(n)] = UserId("id_" + n)
		idToUser[UserId("id_"+n)] = u
		keyToId["key_"+n] = UserId("id_" + n)
		emailToId[n+"@test.local"] = UserId("id_" + n)
		idToUserMutex.Unlock()
	}
}

func TestDailyClaimWithoutSystemAccount(t *testing.T) {
	origPath := config.USERDATA_PATH
	config.USERDATA_PATH = filepath.Join(t.TempDir(), "userdata")
	defer func() { config.USERDATA_PATH = origPath }()

	// Only a regular user exists - the built-in "rotur" system account is
	// missing, which is what claimDaily relies on to mint credits.
	resetUserStoreForTest(t, "alice")

	err := PerformCreditTransfer("rotur", "alice", 2, "每日签到 +2")
	if err == nil {
		t.Fatal("expected PerformCreditTransfer to fail when the 'rotur' system account is missing")
	}
}

func TestDailyClaimWithSystemAccount(t *testing.T) {
	origPath := config.USERDATA_PATH
	config.USERDATA_PATH = filepath.Join(t.TempDir(), "userdata")
	defer func() { config.USERDATA_PATH = origPath }()

	resetUserStoreForTest(t, "alice", "rotur")

	err := PerformCreditTransfer("rotur", "alice", 2, "每日签到 +2")
	if err != nil {
		t.Fatalf("PerformCreditTransfer failed with 'rotur' present: %v", err)
	}

	alice, err := getAccountByUsername("alice")
	if err != nil {
		t.Fatalf("failed to look up alice: %v", err)
	}
	if got := alice.GetCredits(); got != 2 {
		t.Errorf("alice credits = %v, want 2", got)
	}
}

func TestEnsureSystemAccountsCreatesMissingMint(t *testing.T) {
	origPath := config.USERDATA_PATH
	config.USERDATA_PATH = filepath.Join(t.TempDir(), "userdata")
	defer func() { config.USERDATA_PATH = origPath }()

	// Start from a store with no system accounts at all.
	resetUserStoreForTest(t, "alice")

	ensureSystemAccounts()

	for _, name := range []Username{"rotur", "mist"} {
		if _, err := getAccountByUsername(name); err != nil {
			t.Fatalf("%q system account should have been created: %v", name, err)
		}
	}

	// Sign-in rewards should now succeed end-to-end.
	err := PerformCreditTransfer("rotur", "alice", 2, "每日签到 +2")
	if err != nil {
		t.Fatalf("PerformCreditTransfer should succeed after ensureSystemAccounts: %v", err)
	}
	alice, err := getAccountByUsername("alice")
	if err != nil {
		t.Fatalf("failed to look up alice: %v", err)
	}
	if got := alice.GetCredits(); got != 2 {
		t.Errorf("alice credits = %v, want 2", got)
	}
}
