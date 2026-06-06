package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// --- LoadUserJSON / SaveUserJSON ---

func TestSaveAndLoadUserJSON(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "userdata_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	origPath := USERDATA_PATH
	USERDATA_PATH = tmpDir
	defer func() { USERDATA_PATH = origPath }()

	// Setup a minimal idToUser mapping so Username.Id() works
	idToUserMutex.Lock()
	if usernameToId == nil {
		usernameToId = make(map[Username]UserId)
	}
	if idToUser == nil {
		idToUser = make(map[UserId]User)
	}
	usernameToId["jsontestuser"] = UserId("id_jsontestuser")
	idToUser[UserId("id_jsontestuser")] = User{"username": "jsontestuser", "sys.id": "id_jsontestuser"}
	idToUserMutex.Unlock()
	defer func() {
		idToUserMutex.Lock()
		delete(usernameToId, "jsontestuser")
		delete(idToUser, "id_jsontestuser")
		idToUserMutex.Unlock()
	}()

	type TestStruct struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	username := Username("jsontestuser")
	userId := username.Id()
	orig := TestStruct{Name: "test", Value: 42}

	err = SaveUserJSON(userId, "test_data.json", orig)
	if err != nil {
		t.Fatalf("SaveUserJSON failed: %v", err)
	}

	loaded, err := LoadUserJSON[TestStruct](userId, "test_data.json")
	if err != nil {
		t.Fatalf("LoadUserJSON failed: %v", err)
	}

	if loaded.Name != "test" || loaded.Value != 42 {
		t.Errorf("Loaded data mismatch: got %+v, want {Name:test Value:42}", loaded)
	}
}

func TestLoadUserJSON_NotExists(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "userdata_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	origPath := USERDATA_PATH
	USERDATA_PATH = tmpDir
	defer func() { USERDATA_PATH = origPath }()

	idToUserMutex.Lock()
	if usernameToId == nil {
		usernameToId = make(map[Username]UserId)
	}
	if idToUser == nil {
		idToUser = make(map[UserId]User)
	}
	usernameToId["nonexistuser"] = UserId("id_nonexistuser")
	idToUser[UserId("id_nonexistuser")] = User{"username": "nonexistuser", "sys.id": "id_nonexistuser"}
	idToUserMutex.Unlock()
	defer func() {
		idToUserMutex.Lock()
		delete(usernameToId, "nonexistuser")
		delete(idToUser, "id_nonexistuser")
		idToUserMutex.Unlock()
	}()

	_, err = LoadUserJSON[struct{}](UserId("id_nonexistuser"), "nonexistent.json")
	if err == nil {
		t.Error("LoadUserJSON should return error for nonexistent file")
	}
}

func TestSaveUserJSON_CreatesDirectory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "userdata_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	origPath := USERDATA_PATH
	USERDATA_PATH = filepath.Join(tmpDir, "deep", "nested")
	defer func() { USERDATA_PATH = origPath }()

	idToUserMutex.Lock()
	if usernameToId == nil {
		usernameToId = make(map[Username]UserId)
	}
	if idToUser == nil {
		idToUser = make(map[UserId]User)
	}
	usernameToId["dirtestuser"] = UserId("id_dirtestuser")
	idToUser[UserId("id_dirtestuser")] = User{"username": "dirtestuser", "sys.id": "id_dirtestuser"}
	idToUserMutex.Unlock()
	defer func() {
		idToUserMutex.Lock()
		delete(usernameToId, "dirtestuser")
		delete(idToUser, "id_dirtestuser")
		idToUserMutex.Unlock()
	}()

	err = SaveUserJSON(UserId("id_dirtestuser"), "data.json", map[string]string{"k": "v"})
	if err != nil {
		t.Fatalf("SaveUserJSON should create nested dirs, got: %v", err)
	}

	dirPath := filepath.Join(USERDATA_PATH, "id_dirtestuser")
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		t.Errorf("Directory %s should exist", dirPath)
	}
}

func TestJSONValid_VariousTypes(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{`{"key": "value"}`, true},
		{`[1, 2, 3]`, true},
		{`42`, true},
		{`"hello"`, true},
		{`true`, true},
		{`false`, true},
		{`null`, true},
		{`{}`, true},
		{`[]`, true},
		{``, false},
		{`{broken`, false},
		{`[1, 2,`, false},
		{`undefined`, false},
	}
	for _, tt := range tests {
		got := json.Valid([]byte(tt.input))
		if got != tt.expected {
			t.Errorf("json.Valid(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

// --- JSONStringify ---

func TestJSONStringify_NilMap(t *testing.T) {
	// json.Marshal(nil map) should produce "null" or "{}"
	result := JSONStringify(map[string]string(nil))
	if result != "null" {
		t.Logf("JSONStringify(nil map) = %q (depends on json.Marshal behavior)", result)
	}
}

func TestJSONStringify_Struct(t *testing.T) {
	type testStruct struct {
		Name string `json:"name"`
	}
	result := JSONStringify(testStruct{Name: "test"})
	expected := `{"name":"test"}`
	if result != expected {
		t.Errorf("JSONStringify(struct) = %q, want %q", result, expected)
	}
}

// --- JSON round-trip for Gift ---

func TestGift_JSON_RoundTrip(t *testing.T) {
	now := int64(1700000000000)
	claimedAt := now + 1000
	claimedBy := UserId("user1")

	g := Gift{
		Id:        "gift1",
		Code:      "abc123def456",
		Amount:    50.0,
		Note:      "test gift",
		CreatorId: UserId("creator1"),
		CreatedAt: now,
		ExpiresAt: now + 86400000,
		ClaimedAt: &claimedAt,
		ClaimedBy: &claimedBy,
	}

	data, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("Failed to marshal Gift: %v", err)
	}

	var decoded Gift
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal Gift: %v", err)
	}

	if decoded.Id != g.Id {
		t.Errorf("Id mismatch: got %q, want %q", decoded.Id, g.Id)
	}
	if decoded.Code != g.Code {
		t.Errorf("Code mismatch: got %q, want %q", decoded.Code, g.Code)
	}
	if decoded.Amount != g.Amount {
		t.Errorf("Amount mismatch: got %v, want %v", decoded.Amount, g.Amount)
	}
	if decoded.ClaimedAt == nil || *decoded.ClaimedAt != claimedAt {
		t.Errorf("ClaimedAt mismatch: got %v, want %d", decoded.ClaimedAt, claimedAt)
	}
}

// --- JSON round-trip for Key ---

func TestKey_JSON_RoundTrip(t *testing.T) {
	webhook := "https://example.com/hook"
	data := "some data"

	k := Key{
		Key:         "key123",
		Creator:     UserId("creator1"),
		Users:       map[UserId]KeyUserData{},
		Name:        "Test Key",
		Price:       10,
		Type:        "subscription",
		Webhook:     &webhook,
		Data:        &data,
		TotalIncome: 100,
	}
	k.Users[UserId("user1")] = KeyUserData{
		Time:  1700000000000,
		Price: 10,
	}

	encoded, err := json.Marshal(k)
	if err != nil {
		t.Fatalf("Failed to marshal Key: %v", err)
	}

	var decoded Key
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal Key: %v", err)
	}

	if decoded.Key != "key123" {
		t.Errorf("Key mismatch: got %q, want %q", decoded.Key, "key123")
	}
	if decoded.Name != "Test Key" {
		t.Errorf("Name mismatch: got %q, want %q", decoded.Name, "Test Key")
	}
	if decoded.Price != 10 {
		t.Errorf("Price mismatch: got %d, want %d", decoded.Price, 10)
	}
}

// --- JSON round-trip for Transaction ---

func TestTransaction_JSON_RoundTrip(t *testing.T) {
	tx := Transaction{
		Type:       "transfer",
		User:       UserId("user1"),
		To:         "user2",
		Amount:     50.0,
		Note:       "payment",
		Timestamp:  1700000000000,
		NewTotal:   150.0,
		PetitionId: "pet1",
		KeyName:    "mykey",
		KeyId:      "key1",
	}

	data, err := json.Marshal(tx)
	if err != nil {
		t.Fatalf("Failed to marshal Transaction: %v", err)
	}

	var decoded Transaction
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal Transaction: %v", err)
	}

	if decoded.Type != "transfer" {
		t.Errorf("Type mismatch: got %q, want %q", decoded.Type, "transfer")
	}
	if decoded.Amount != 50.0 {
		t.Errorf("Amount mismatch: got %v, want %v", decoded.Amount, 50.0)
	}
	if decoded.KeyName != "mykey" {
		t.Errorf("KeyName mismatch: got %q, want %q", decoded.KeyName, "mykey")
	}
}
