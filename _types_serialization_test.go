package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestReply_UnmarshalJSON_FloatTimestamp(t *testing.T) {
	data := `{"id":"r1","content":"hello","user":"user1","timestamp":1700000000000}`
	var r Reply
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		t.Fatalf("Failed to unmarshal Reply: %v", err)
	}
	if r.ID != "r1" {
		t.Errorf("ID = %q, want %q", r.ID, "r1")
	}
	if r.Content != "hello" {
		t.Errorf("Content = %q, want %q", r.Content, "hello")
	}
	if r.User != "user1" {
		t.Errorf("User = %q, want %q", r.User, "user1")
	}
	if r.Timestamp != 1700000000000 {
		t.Errorf("Timestamp = %d, want %d", r.Timestamp, 1700000000000)
	}
}

func TestReply_UnmarshalJSON_StringTimestamp(t *testing.T) {
	data := `{"id":"r2","content":"world","user":"user2","timestamp":"1700000000000"}`
	var r Reply
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		t.Fatalf("Failed to unmarshal Reply with string timestamp: %v", err)
	}
	if r.Timestamp != 1700000000000 {
		t.Errorf("Timestamp = %d, want %d", r.Timestamp, 1700000000000)
	}
}

func TestPost_UnmarshalJSON_FloatTimestamp(t *testing.T) {
	ts := time.Now().UnixMilli()
	data := `{"id":"p1","content":"test post","user":"user1","timestamp":` + jsonInt(ts) + `}`
	var p Post
	if err := json.Unmarshal([]byte(data), &p); err != nil {
		t.Fatalf("Failed to unmarshal Post: %v", err)
	}
	if p.ID != "p1" {
		t.Errorf("ID = %q, want %q", p.ID, "p1")
	}
	if p.Timestamp != ts {
		t.Errorf("Timestamp = %d, want %d", p.Timestamp, ts)
	}
}

func TestPost_UnmarshalJSON_StringTimestamp(t *testing.T) {
	ts := time.Now().UnixMilli()
	data := `{"id":"p2","content":"test post","user":"user1","timestamp":"` + jsonIntStr(ts) + `"}`
	var p Post
	if err := json.Unmarshal([]byte(data), &p); err != nil {
		t.Fatalf("Failed to unmarshal Post with string timestamp: %v", err)
	}
	if p.Timestamp != ts {
		t.Errorf("Timestamp = %d, want %d", p.Timestamp, ts)
	}
}

func TestPost_ToNet(t *testing.T) {
	p := Post{
		ID:        "p1",
		Content:   "hello world",
		User:      UserId("user1"),
		Timestamp: 1700000000000,
		Pinned:    true,
		Likes:     []UserId{"user2", "user3"},
	}
	net := p.ToNet()
	if net.ID != "p1" {
		t.Errorf("NetPost.ID = %q, want %q", net.ID, "p1")
	}
	if net.Content != "hello world" {
		t.Errorf("NetPost.Content = %q, want %q", net.Content, "hello world")
	}
	if net.Pinned != true {
		t.Error("NetPost.Pinned should be true")
	}
}

func TestReply_ToNet(t *testing.T) {
	r := Reply{
		ID:        "r1",
		Content:   "reply content",
		User:      UserId("user1"),
		Timestamp: 1700000000000,
	}
	net := r.ToNet()
	if net.ID != "r1" {
		t.Errorf("NetReply.ID = %q, want %q", net.ID, "r1")
	}
	if net.Content != "reply content" {
		t.Errorf("NetReply.Content = %q, want %q", net.Content, "reply content")
	}
}

func TestTransaction_ToNet(t *testing.T) {
	tx := Transaction{
		Type:      "transfer",
		User:      UserId("user1"),
		Amount:    50.0,
		Note:      "test transfer",
		Timestamp: 1700000000000,
		NewTotal:  150.0,
		KeyName:   "mykey",
		KeyId:     "key1",
	}
	net := tx.ToNet()
	if net.Type != "transfer" {
		t.Errorf("TransactionNet.Type = %q, want %q", net.Type, "transfer")
	}
	if net.Amount != 50.0 {
		t.Errorf("TransactionNet.Amount = %v, want %v", net.Amount, 50.0)
	}
	if net.KeyName != "mykey" {
		t.Errorf("TransactionNet.KeyName = %q, want %q", net.KeyName, "mykey")
	}
}

func TestGift_ToNet(t *testing.T) {
	claimedAt := time.Now().UnixMilli()
	claimedBy := UserId("user1")
	g := Gift{
		Id:        "gift1",
		Code:      "abc123",
		Amount:    100.0,
		Note:      "test gift",
		CreatorId: UserId("creator1"),
		CreatedAt: 1700000000000,
		ExpiresAt: 1700100000000,
		ClaimedAt: &claimedAt,
		ClaimedBy: &claimedBy,
	}
	net := g.ToNet()
	if net.Id != "gift1" {
		t.Errorf("GiftNet.Id = %q, want %q", net.Id, "gift1")
	}
	if net.Amount != 100.0 {
		t.Errorf("GiftNet.Amount = %v, want %v", net.Amount, 100.0)
	}
}

func TestGift_ToPublic(t *testing.T) {
	g := Gift{
		Code:      "abc123",
		Amount:    100.0,
		Note:      "test gift",
		CreatorId: UserId("creator1"),
		ExpiresAt: 1700100000000,
	}
	pub := g.ToPublic()
	if pub.Code != "abc123" {
		t.Errorf("GiftPublic.Code = %q, want %q", pub.Code, "abc123")
	}
	if pub.Amount != 100.0 {
		t.Errorf("GiftPublic.Amount = %v, want %v", pub.Amount, 100.0)
	}
}

func TestKey_ToPublic(t *testing.T) {
	k := &Key{
		Key:   "testkey",
		Name:  "My Key",
		Price: 10,
		Type:  "one_time",
	}
	pub := k.ToPublic()
	if pub["key"] != "testkey" {
		t.Errorf("ToPublic()['key'] = %v, want %v", pub["key"], "testkey")
	}
	if pub["name"] != "My Key" {
		t.Errorf("ToPublic()['name'] = %v, want %v", pub["name"], "My Key")
	}
	if pub["price"] != 10 {
		t.Errorf("ToPublic()['price'] = %v, want %v", pub["price"], 10)
	}
}

func TestKey_setKey_Name(t *testing.T) {
	k := &Key{Name: "old"}
	k.setKey("name", "new")
	if k.Name != "new" {
		t.Errorf("setKey('name') = %q, want %q", k.Name, "new")
	}
}

func TestKey_setKey_Price(t *testing.T) {
	k := &Key{Price: 5}
	k.setKey("price", 10)
	if k.Price != 10 {
		t.Errorf("setKey('price', int) = %d, want %d", k.Price, 10)
	}
}

func TestKey_setKey_Price_Float64(t *testing.T) {
	k := &Key{Price: 5}
	k.setKey("price", float64(15))
	if k.Price != 15 {
		t.Errorf("setKey('price', float64) = %d, want %d", k.Price, 15)
	}
}

func TestKey_setKey_Type(t *testing.T) {
	k := &Key{Type: "one_time"}
	k.setKey("type", "subscription")
	if k.Type != "subscription" {
		t.Errorf("setKey('type') = %q, want %q", k.Type, "subscription")
	}
}

func TestKey_setKey_Webhook(t *testing.T) {
	k := &Key{}
	webhook := "https://example.com/webhook"
	k.setKey("webhook", webhook)
	if k.Webhook == nil || *k.Webhook != webhook {
		t.Errorf("setKey('webhook') = %v, want %v", k.Webhook, webhook)
	}
}

func TestKey_setKey_Data(t *testing.T) {
	k := &Key{}
	data := "some data"
	k.setKey("data", data)
	if k.Data == nil || *k.Data != data {
		t.Errorf("setKey('data') = %v, want %v", k.Data, data)
	}
}

func TestSystem_Set_Name(t *testing.T) {
	s := &System{Name: "old"}
	val, err := s.Set("name", "new")
	if err != nil {
		t.Fatalf("System.Set('name') returned error: %v", err)
	}
	if val != "new" {
		t.Errorf("System.Set('name') = %q, want %q", val, "new")
	}
	if s.Name != "new" {
		t.Errorf("System.Name = %q, want %q", s.Name, "new")
	}
}

func TestSystem_Set_Wallpaper(t *testing.T) {
	s := &System{Wallpaper: "old.jpg"}
	val, err := s.Set("wallpaper", "new.jpg")
	if err != nil {
		t.Fatalf("System.Set('wallpaper') returned error: %v", err)
	}
	if val != "new.jpg" {
		t.Errorf("System.Set('wallpaper') = %q, want %q", val, "new.jpg")
	}
}

func TestSystem_Set_Designation(t *testing.T) {
	s := &System{Designation: "old"}
	val, err := s.Set("designation", "new")
	if err != nil {
		t.Fatalf("System.Set('designation') returned error: %v", err)
	}
	if val != "new" {
		t.Errorf("System.Set('designation') = %q, want %q", val, "new")
	}
}

func TestSystem_Set_InvalidKey(t *testing.T) {
	s := &System{}
	_, err := s.Set("invalid_key", "value")
	if err == nil {
		t.Error("System.Set with invalid key should return error")
	}
}

func TestSystem_Set_InvalidValueType(t *testing.T) {
	s := &System{}
	_, err := s.Set("name", 123) // not a string
	if err == nil {
		t.Error("System.Set('name', int) should return error")
	}
}

func TestGroup_ToNet(t *testing.T) {
	g := Group{
		Id:             GroupId("g1"),
		Tag:            "testgroup",
		Name:           "Test Group",
		Description:    "A test group",
		OwnerUserId:    UserId("owner1"),
		Public:         true,
		JoinPolicy:     JoinPolicyOpen,
		CreatedAt:      1700000000000,
		CreditsBalance: 100.0,
	}
	net := g.ToNet()
	if net.Id != GroupId("g1") {
		t.Errorf("GroupNet.Id = %v, want g1", net.Id)
	}
	if net.Tag != "testgroup" {
		t.Errorf("GroupNet.Tag = %q, want %q", net.Tag, "testgroup")
	}
	if net.Public != true {
		t.Error("GroupNet.Public should be true")
	}
	if net.JoinPolicy != JoinPolicyOpen {
		t.Errorf("GroupNet.JoinPolicy = %q, want %q", net.JoinPolicy, JoinPolicyOpen)
	}
}

func TestGroup_ToPublic(t *testing.T) {
	g := &Group{
		Tag:        "tag1",
		Name:       "Group1",
		Public:     true,
		JoinPolicy: JoinPolicyRequest,
	}
	pub := g.ToPublic()
	if pub.Tag != "tag1" {
		t.Errorf("GroupPublic.Tag = %q, want %q", pub.Tag, "tag1")
	}
	if pub.JoinPolicy != JoinPolicyRequest {
		t.Errorf("GroupPublic.JoinPolicy = %q, want %q", pub.JoinPolicy, JoinPolicyRequest)
	}
}

func TestItem_ToNet(t *testing.T) {
	i := Item{
		Name:        "Sword",
		Description: "A sharp sword",
		Price:       100,
		Selling:     true,
		Author:      UserId("author1"),
		Owner:       UserId("owner1"),
		Created:     1700000000000,
		TotalIncome: 500,
	}
	net := i.ToNet()
	if net.Name != "Sword" {
		t.Errorf("NetItem.Name = %q, want %q", net.Name, "Sword")
	}
	if net.Price != 100 {
		t.Errorf("NetItem.Price = %d, want %d", net.Price, 100)
	}
	if net.Selling != true {
		t.Error("NetItem.Selling should be true")
	}
}

func jsonInt(v int64) string {
	return string(must(json.Marshal(v)))
}

func jsonIntStr(v int64) string {
	return string(must(json.Marshal(v)))
}

func must(data []byte, _ error) []byte {
	return data
}
