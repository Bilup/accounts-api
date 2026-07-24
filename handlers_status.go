package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func statusWSHandler(c *gin.Context) {
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	conn := &Conn{
		send:     make(chan []byte, 64),
		rooms:    make(map[string]struct{}),
		presence: PresenceOnline,
	}
	go conn.writePump(ws)
	conn.readPump(ws)
}

func (c *Conn) readPump(ws *websocket.Conn) {
	defer func() {
		hub.unregister(c)
		ws.Close()
	}()
	ws.SetReadLimit(maxWSMessageBytes)
	ws.SetReadDeadline(time.Now().Add(120 * time.Second))
	ws.SetPongHandler(func(string) error {
		ws.SetReadDeadline(time.Now().Add(120 * time.Second))
		return nil
	})
	for {
		_, raw, err := ws.ReadMessage()
		if err != nil {
			return
		}
		var msg map[string]json.RawMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.sendError("invalid json")
			continue
		}
		cmdBytes, ok := msg["cmd"]
		if !ok {
			c.sendError("missing cmd")
			continue
		}
		var cmd string
		if err := json.Unmarshal(cmdBytes, &cmd); err != nil {
			c.sendError("invalid cmd")
			continue
		}
		if c.userId == "" && cmd != "auth" {
			c.sendError("not authenticated")
			continue
		}
		switch cmd {
		case "auth":
			c.handleAuth(msg)
		case "join":
			c.handleJoin(msg)
		case "leave":
			c.handleLeave(msg)
		case "rooms":
			c.handleRooms()
		case "set_status":
			c.handleSetStatus(msg)
		case "add_activity":
			c.handleAddActivity(msg)
		case "remove_activity":
			c.handleRemoveActivity(msg)
		case "room_state":
			c.handleRoomState(msg)
		case "gmsg":
			c.handleGmsg(msg)
		case "pmsg":
			c.handlePmsg(msg)
		case "ping":
		default:
			c.sendError("unknown command")
		}
	}
}

func (c *Conn) writePump(ws *websocket.Conn) {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		ws.Close()
	}()
	for {
		select {
		case data, ok := <-c.send:
			ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				ws.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := ws.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}
		case <-ticker.C:
			ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *Conn) sendMsg(payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	select {
	case c.send <- data:
	default:
	}
}

func (c *Conn) sendError(message string) {
	c.sendMsg(map[string]any{"cmd": "error", "message": message})
}

func (c *Conn) requireStatusUpdatePermission() bool {
	if c.canUpdateStatus {
		return true
	}
	c.sendError("Token lacks permission: " + string(PermManageProfile))
	return false
}

func (c *Conn) handleAuth(msg map[string]json.RawMessage) {
	if c.userId != "" {
		c.sendError("already authenticated")
		return
	}
	var key string
	if err := json.Unmarshal(msg["key"], &key); err != nil || key == "" {
		c.sendError("key required")
		return
	}
	user, subToken := authenticateAnyKey(key)
	if user == nil {
		c.sendError("invalid key")
		return
	}
	c.canUpdateStatus = subToken == nil || subToken.hasPermission(PermManageProfile)
	c.userId = user.GetId()
	c.username = user.GetUsername()
	hub.register(c)

	hub.Lock()
	us := hub.userStatus[c.userId]
	if raw := user.Get("sys.status"); raw != nil {
		if m, ok := raw.(map[string]any); ok {
			us.Presence = Presence(strings.ToLower(getStringOrDefault(m["presence"], "online")))
			us.Status = getStringOrDefault(m["status"], "")
		}
	}
	c.presence = us.Presence
	c.lastPresenceSet = time.Now()
	hub.Unlock()

	userObj := userToNet(*user)
	userObj["sys.status"] = map[string]any{
		"presence": string(us.Presence),
		"status":   us.Status,
	}

	c.sendMsg(map[string]any{
		"cmd":      "ready",
		"user_id":  string(c.userId),
		"username": string(c.username),
		"user":     userObj,
	})
}

func (c *Conn) handleJoin(msg map[string]json.RawMessage) {
	var rooms []string
	if err := json.Unmarshal(msg["rooms"], &rooms); err != nil {
		var single string
		if err := json.Unmarshal(msg["rooms"], &single); err != nil {
			c.sendError("rooms required")
			return
		}
		rooms = []string{single}
	}
	if len(rooms) == 0 {
		c.sendError("rooms required")
		return
	}
	for _, name := range rooms {
		if len(name) > maxRoomNameLen || !roomNameRe.MatchString(name) {
			c.sendError("invalid room name: " + name)
			return
		}
	}
	hub.Lock()
	if len(c.rooms)+len(rooms) > maxRoomsPerConn {
		hub.Unlock()
		c.sendError("room limit reached")
		return
	}
	for _, name := range rooms {
		if _, exists := c.rooms[name]; exists {
			hub.Unlock()
			c.sendError("already in room: " + name)
			return
		}
	}
	for _, name := range rooms {
		c.rooms[name] = struct{}{}
		r := hub.getOrMakeRoom(name)
		r.members[c.userId] = struct{}{}
	}
	state := hub.mergedStateLocked(c.userId)
	var snapshots map[string][]RoomMember
	snapshots = make(map[string][]RoomMember, len(rooms))
	for _, name := range rooms {
		snapshots[name] = roomSnapshotLocked(hub, name)
	}
	hub.Unlock()
	for _, name := range rooms {
		c.sendMsg(map[string]any{"cmd": "join_ok", "room": name})
		c.sendMsg(map[string]any{
			"cmd":     "room_state",
			"room":    name,
			"members": snapshots[name],
		})
		if state.Presence.visible() {
			hub.broadcast(name, "member_join", map[string]any{
				"room":       name,
				"user_id":    string(c.userId),
				"username":   string(c.username),
				"status":     state.Status,
				"presence":   string(state.Presence),
				"activities": state.Activities,
			}, c.userId)
		}
	}
}

func (c *Conn) handleLeave(msg map[string]json.RawMessage) {
	var rooms []string
	if err := json.Unmarshal(msg["rooms"], &rooms); err != nil {
		var single string
		if err := json.Unmarshal(msg["rooms"], &single); err != nil {
			c.sendError("rooms required")
			return
		}
		rooms = []string{single}
	}
	if len(rooms) == 0 {
		c.sendError("rooms required")
		return
	}
	hub.Lock()
	for _, name := range rooms {
		if _, exists := c.rooms[name]; !exists {
			hub.Unlock()
			c.sendError("not in room: " + name)
			return
		}
	}
	for _, name := range rooms {
		delete(c.rooms, name)
	}
	for _, name := range rooms {
		r, ok := hub.rooms[name]
		if !ok {
			continue
		}
		stillInRoom := false
		for _, cc := range hub.userConns[c.userId] {
			if cc == c {
				continue
			}
			if _, inRoom := cc.rooms[name]; inRoom {
				stillInRoom = true
				break
			}
		}
		if stillInRoom {
			state := hub.mergedStateLocked(c.userId)
			hub.broadcastLocked(name, "status_update", map[string]any{
				"room":     name,
				"user_id":  string(c.userId),
				"username": string(c.username),
				"status":   state.Status,
				"presence": string(state.Presence),
			}, c.userId)
		} else {
			delete(r.members, c.userId)
			hub.broadcastLocked(name, "member_leave", map[string]any{
				"room":    name,
				"user_id": string(c.userId),
			}, "")
			if len(r.members) == 0 {
				delete(hub.rooms, name)
			}
		}
	}
	hub.Unlock()
	for _, name := range rooms {
		c.sendMsg(map[string]any{"cmd": "leave_ok", "room": name})
	}
}

func (c *Conn) handleRooms() {
	hub.Lock()
	rooms := make([]string, 0, len(c.rooms))
	for name := range c.rooms {
		rooms = append(rooms, name)
	}
	hub.Unlock()
	c.sendMsg(map[string]any{"cmd": "rooms", "rooms": rooms})
}

func (c *Conn) handleSetStatus(msg map[string]json.RawMessage) {
	if !c.requireStatusUpdatePermission() {
		return
	}

	var status string
	var statusSet bool
	if b, ok := msg["status"]; ok {
		statusSet = true
		if err := json.Unmarshal(b, &status); err != nil {
			c.sendError("invalid status")
			return
		}
		if len(status) > maxStatusLen {
			c.sendError("status too long")
			return
		}
	}

	var p string
	var presenceSet bool
	if b, ok := msg["presence"]; ok {
		presenceSet = true
		if err := json.Unmarshal(b, &p); err != nil {
			c.sendError("invalid presence")
			return
		}
		p = strings.ToLower(p)
		switch Presence(p) {
		case PresenceOnline, PresenceIdle, PresenceDND, PresenceInvisible:
		default:
			c.sendError("invalid presence value")
			return
		}
	}

	if !statusSet && !presenceSet {
		c.sendError("status or presence required")
		return
	}

	hub.Lock()
	us := hub.userStatus[c.userId]
	if us == nil {
		us = &UserStatus{Presence: PresenceOnline, Activities: make(map[string]Activity)}
		hub.userStatus[c.userId] = us
	}

	if presenceSet && c.presence == Presence(p) && statusSet && us.Status == status {
		hub.Unlock()
		return
	}
	if presenceSet && !statusSet && c.presence == Presence(p) {
		hub.Unlock()
		return
	}
	if statusSet && !presenceSet && us.Status == status {
		hub.Unlock()
		return
	}

	oldMerged := hub.mergedPresenceLocked(c.userId)

	if presenceSet {
		c.presence = Presence(p)
		c.lastPresenceSet = time.Now()
		us.Presence = Presence(p)
	}
	if statusSet {
		us.Status = status
	}

	newMerged := hub.mergedPresenceLocked(c.userId)
	roomNames := hub.allRoomsForUserLocked(c.userId)

	hub.persistStatusLocked(c.userId, us)

	hub.Unlock()

	wasVisible := oldMerged.visible()
	nowVisible := newMerged.visible()

	if wasVisible && !nowVisible {
		for _, name := range roomNames {
			hub.broadcast(name, "member_leave", map[string]any{
				"room":    name,
				"user_id": string(c.userId),
			}, "")
		}
	} else if !wasVisible && nowVisible {
		hub.Lock()
		state := hub.mergedStateLocked(c.userId)
		hub.Unlock()
		for _, name := range roomNames {
			hub.broadcast(name, "member_join", map[string]any{
				"room":       name,
				"user_id":    string(c.userId),
				"username":   string(c.username),
				"status":     state.Status,
				"presence":   string(state.Presence),
				"activities": state.Activities,
			}, "")
		}
	} else {
		hub.broadcastStatusToAllRooms(c.userId, roomNames)
	}
}

func isIdenticalActivity(a, b Activity) bool {
	if a.ID != b.ID ||
		a.Title != b.Title ||
		a.Image != b.Image ||
		a.URL != b.URL ||
		a.Status != b.Status ||
		a.StartTime != b.StartTime {
		return false
	}

	if (a.Application == nil) != (b.Application == nil) {
		return false
	}
	if a.Application != nil &&
		(a.Application.Name != b.Application.Name ||
			a.Application.URL != b.Application.URL) {
		return false
	}

	if (a.Media == nil) != (b.Media == nil) {
		return false
	}
	if a.Media != nil &&
		(a.Media.Title != b.Media.Title ||
			a.Media.Artist != b.Media.Artist ||
			a.Media.Album != b.Media.Album ||
			a.Media.Start != b.Media.Start ||
			a.Media.End != b.Media.End) {
		return false
	}

	return true
}

func (c *Conn) handleAddActivity(msg map[string]json.RawMessage) {
	if !c.requireStatusUpdatePermission() {
		return
	}

	var act Activity
	raw, ok := msg["id"]
	if !ok {
		c.sendError("id required")
		return
	}
	if err := json.Unmarshal(raw, &act.ID); err != nil || act.ID == "" {
		c.sendError("invalid id")
		return
	}
	if b, ok := msg["title"]; ok {
		json.Unmarshal(b, &act.Title)
	}
	if b, ok := msg["application"]; ok {
		var app ActivityApplication
		json.Unmarshal(b, &app)
		act.Application = &app
	}
	if b, ok := msg["image"]; ok {
		json.Unmarshal(b, &act.Image)
	}
	if b, ok := msg["url"]; ok {
		json.Unmarshal(b, &act.URL)
	}
	if b, ok := msg["status"]; ok {
		json.Unmarshal(b, &act.Status)
	}
	if b, ok := msg["start_time"]; ok {
		json.Unmarshal(b, &act.StartTime)
	}
	if b, ok := msg["media"]; ok {
		var media ActivityMedia
		json.Unmarshal(b, &media)
		act.Media = &media
	}

	hub.Lock()
	us := hub.userStatus[c.userId]
	if us == nil {
		us = &UserStatus{Presence: PresenceOnline, Activities: make(map[string]Activity)}
		hub.userStatus[c.userId] = us
	}

	if c.activities == nil {
		c.activities = make(map[string]struct{})
	}

	if len(c.activities) >= maxActivitiesPerConn {
		hub.Unlock()
		c.sendError("activity limit reached")
		return
	}
	if isIdenticalActivity(act, us.Activities[act.ID]) {
		hub.Unlock()
		return
	}
	us.Activities[act.ID] = act
	c.activities[act.ID] = struct{}{}

	roomNames := hub.allRoomsForUserLocked(c.userId)
	hub.Unlock()
	hub.broadcastStatusToAllRooms(c.userId, roomNames)
}

func (c *Conn) handleRemoveActivity(msg map[string]json.RawMessage) {
	if !c.requireStatusUpdatePermission() {
		return
	}

	var id string
	if err := json.Unmarshal(msg["id"], &id); err != nil || id == "" {
		c.sendError("id required")
		return
	}
	hub.Lock()
	us := hub.userStatus[c.userId]
	if us == nil {
		hub.Unlock()
		c.sendError("activity not found")
		return
	}
	if _, exists := us.Activities[id]; !exists {
		hub.Unlock()
		c.sendError("activity not found")
		return
	}
	delete(us.Activities, id)

	if c.activities != nil {
		delete(c.activities, id)
	}

	roomNames := hub.allRoomsForUserLocked(c.userId)
	hub.Unlock()
	hub.broadcastStatusToAllRooms(c.userId, roomNames)
}

func (c *Conn) handleRoomState(msg map[string]json.RawMessage) {
	var room string
	if err := json.Unmarshal(msg["room"], &room); err != nil || room == "" {
		c.sendError("room required")
		return
	}
	hub.Lock()
	if _, inRoom := c.rooms[room]; !inRoom {
		hub.Unlock()
		c.sendError("not in room: " + room)
		return
	}
	snapshot := roomSnapshotLocked(hub, room)
	hub.Unlock()
	c.sendMsg(map[string]any{
		"cmd":     "room_state",
		"room":    room,
		"members": snapshot,
	})
}

func statusSetHTTP(c *gin.Context) {
	user := currentUser(c)
	uid := user.GetId()

	var body struct {
		Status   *string `json:"status"`
		Presence *string `json:"presence"`
	}
	if !bindJSON(c, &body) {
		return
	}

	if body.Status == nil && body.Presence == nil {
		c.JSON(400, gin.H{"error": "status or presence required"})
		return
	}

	if body.Status != nil && len(*body.Status) > maxStatusLen {
		c.JSON(400, gin.H{"error": "status too long"})
		return
	}

	var presence Presence
	if body.Presence != nil {
		presence = Presence(strings.ToLower(*body.Presence))
		switch presence {
		case PresenceOnline, PresenceIdle, PresenceDND, PresenceInvisible:
		default:
			c.JSON(400, gin.H{"error": "invalid presence value"})
			return
		}
	}

	hub.Lock()
	us := hub.userStatus[uid]
	if us == nil {
		us = &UserStatus{Presence: PresenceOnline, Activities: make(map[string]Activity)}
		hub.userStatus[uid] = us
	}

	oldMerged := hub.mergedPresenceLocked(uid)

	if body.Presence != nil {
		us.Presence = presence
	}
	if body.Status != nil {
		us.Status = *body.Status
	}

	newMerged := hub.mergedPresenceLocked(uid)
	roomNames := hub.allRoomsForUserLocked(uid)
	hub.persistStatusLocked(uid, us)
	hub.Unlock()

	wasVisible := oldMerged.visible()
	nowVisible := newMerged.visible()

	if wasVisible && !nowVisible {
		for _, name := range roomNames {
			hub.broadcast(name, "member_leave", map[string]any{
				"room":    name,
				"user_id": string(uid),
			}, "")
		}
	} else if !wasVisible && nowVisible {
		hub.Lock()
		state := hub.mergedStateLocked(uid)
		username := ""
		if conns := hub.userConns[uid]; len(conns) > 0 {
			username = string(conns[0].username)
		}
		hub.Unlock()
		for _, name := range roomNames {
			hub.broadcast(name, "member_join", map[string]any{
				"room":       name,
				"user_id":    string(uid),
				"username":   username,
				"status":     state.Status,
				"presence":   string(state.Presence),
				"activities": state.Activities,
			}, "")
		}
	} else {
		hub.broadcastStatusToAllRooms(uid, roomNames)
	}

	c.JSON(200, gin.H{"ok": true})
}

func statusGetHTTP(c *gin.Context) {
	name := c.Query("name")
	if !requireField(c, name, "name parameter missing") {
		return
	}
	uid := getIdByUsername(Username(name).ToLower())
	if uid == "" {
		c.JSON(404, gin.H{"error": "user not found"})
		return
	}
	state := hub.getUserStatus(uid)
	if state == nil {
		c.JSON(404, gin.H{"error": "no status"})
		return
	}
	c.JSON(200, gin.H{
		"username":   name,
		"status":     state.Status,
		"presence":   string(state.Presence),
		"activities": state.Activities,
	})
}
