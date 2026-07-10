package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStatusUpdatePermissionMatchesProfilePermission(t *testing.T) {
	tests := []struct {
		name  string
		token SubToken
		want  bool
	}{
		{
			name:  "edit profile permission",
			token: SubToken{Permissions: []TokenPermission{PermManageProfile}},
			want:  true,
		},
		{
			name:  "read-only permissions",
			token: SubToken{Permissions: []TokenPermission{PermViewProfile, PermViewPosts}},
			want:  false,
		},
		{
			name:  "no permissions",
			token: SubToken{},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.token.hasPermission(PermManageProfile); got != tt.want {
				t.Fatalf("status update permission = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReadOnlyStatusConnectionCannotMutateState(t *testing.T) {
	oldHub := hub
	hub = &Hub{
		conns:      make(map[*Conn]struct{}),
		rooms:      make(map[string]*Room),
		userConns:  make(map[UserId][]*Conn),
		userStatus: make(map[UserId]*UserStatus),
	}
	t.Cleanup(func() { hub = oldHub })

	uid := UserId("read-only-user")
	conn := &Conn{
		send:     make(chan []byte, 3),
		userId:   uid,
		rooms:    make(map[string]struct{}),
		presence: PresenceOnline,
	}
	hub.userStatus[uid] = &UserStatus{
		Status:     "unchanged",
		Presence:   PresenceOnline,
		Activities: map[string]Activity{"existing": {ID: "existing", Title: "Existing"}},
	}

	conn.handleSetStatus(rawStatusMessage(map[string]any{"status": "changed"}))
	conn.handleAddActivity(rawStatusMessage(map[string]any{"id": "new", "title": "New"}))
	conn.handleRemoveActivity(rawStatusMessage(map[string]any{"id": "existing"}))

	state := hub.userStatus[uid]
	if state.Status != "unchanged" {
		t.Fatalf("status was mutated to %q", state.Status)
	}
	if _, ok := state.Activities["new"]; ok {
		t.Fatal("activity was added by a read-only connection")
	}
	if _, ok := state.Activities["existing"]; !ok {
		t.Fatal("activity was removed by a read-only connection")
	}

	for range 3 {
		var response struct {
			Cmd     string `json:"cmd"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(<-conn.send, &response); err != nil {
			t.Fatal(err)
		}
		if response.Cmd != "error" || !strings.Contains(response.Message, string(PermManageProfile)) {
			t.Fatalf("unexpected permission response: %+v", response)
		}
	}
}

func TestWritableStatusConnectionCanMutateState(t *testing.T) {
	oldHub := hub
	hub = &Hub{
		conns:      make(map[*Conn]struct{}),
		rooms:      make(map[string]*Room),
		userConns:  make(map[UserId][]*Conn),
		userStatus: make(map[UserId]*UserStatus),
	}
	t.Cleanup(func() { hub = oldHub })

	uid := UserId("writable-user")
	conn := &Conn{
		send:            make(chan []byte, 1),
		userId:          uid,
		canUpdateStatus: true,
		rooms:           make(map[string]struct{}),
		presence:        PresenceOnline,
	}
	hub.userConns[uid] = []*Conn{conn}
	hub.userStatus[uid] = &UserStatus{
		Presence:   PresenceOnline,
		Activities: make(map[string]Activity),
	}

	conn.handleSetStatus(rawStatusMessage(map[string]any{"status": "available"}))
	conn.handleAddActivity(rawStatusMessage(map[string]any{"id": "coding", "title": "Coding"}))

	state := hub.userStatus[uid]
	if state.Status != "available" {
		t.Fatalf("status = %q, want available", state.Status)
	}
	if _, ok := state.Activities["coding"]; !ok {
		t.Fatal("activity was not added by writable connection")
	}

	conn.handleRemoveActivity(rawStatusMessage(map[string]any{"id": "coding"}))
	if _, ok := state.Activities["coding"]; ok {
		t.Fatal("activity was not removed by writable connection")
	}
}

func rawStatusMessage(values map[string]any) map[string]json.RawMessage {
	message := make(map[string]json.RawMessage, len(values))
	for key, value := range values {
		encoded, _ := json.Marshal(value)
		message[key] = encoded
	}
	return message
}
