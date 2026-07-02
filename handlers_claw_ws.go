package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const (
	clawRealtimeVersion     = "1.0.0"
	clawRealtimeHistorySize = 100
	clawRealtimeWriteWait   = 10 * time.Second
	clawRealtimePongWait    = 70 * time.Second
	clawRealtimePingEvery   = 30 * time.Second
)

type clawRealtimeConn struct {
	send     chan []byte
	userId   UserId
	username Username
}

type clawRealtimeHub struct {
	sync.RWMutex
	conns     map[*clawRealtimeConn]struct{}
	userConns map[UserId]map[*clawRealtimeConn]struct{}
}

var (
	clawHub = &clawRealtimeHub{
		conns:     make(map[*clawRealtimeConn]struct{}),
		userConns: make(map[UserId]map[*clawRealtimeConn]struct{}),
	}
	clawWSUpgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
)

func (h *clawRealtimeHub) register(c *clawRealtimeConn) {
	h.Lock()
	h.conns[c] = struct{}{}
	if c.userId != "" {
		h.addUserConnLocked(c)
	}
	count := len(h.conns)
	h.Unlock()
	log.Printf("[ClawWS] client connected; clients=%d", count)
}

func (h *clawRealtimeHub) unregister(c *clawRealtimeConn) {
	h.Lock()
	delete(h.conns, c)
	if c.userId != "" {
		h.removeUserConnLocked(c)
	}
	count := len(h.conns)
	h.Unlock()
	log.Printf("[ClawWS] client disconnected; clients=%d", count)
}

func (h *clawRealtimeHub) authenticateConn(c *clawRealtimeConn, user *User) {
	if user == nil {
		return
	}
	h.Lock()
	if c.userId != "" {
		h.removeUserConnLocked(c)
	}
	c.userId = user.GetId()
	c.username = user.GetUsername()
	h.addUserConnLocked(c)
	h.Unlock()
	c.sendJSON(map[string]any{
		"cmd":      "auth",
		"success":  true,
		"user_id":  string(c.userId),
		"username": string(c.username),
	})
}

func (h *clawRealtimeHub) addUserConnLocked(c *clawRealtimeConn) {
	if h.userConns[c.userId] == nil {
		h.userConns[c.userId] = make(map[*clawRealtimeConn]struct{})
	}
	h.userConns[c.userId][c] = struct{}{}
}

func (h *clawRealtimeHub) removeUserConnLocked(c *clawRealtimeConn) {
	conns := h.userConns[c.userId]
	if conns == nil {
		return
	}
	delete(conns, c)
	if len(conns) == 0 {
		delete(h.userConns, c.userId)
	}
}

func (h *clawRealtimeHub) clientCount() int {
	h.RLock()
	defer h.RUnlock()
	return len(h.conns)
}

func (h *clawRealtimeHub) broadcast(cmd string, val any) {
	msg, ok := clawRealtimeMessage(cmd, val)
	if !ok {
		return
	}
	h.RLock()
	conns := make([]*clawRealtimeConn, 0, len(h.conns))
	for c := range h.conns {
		conns = append(conns, c)
	}
	h.RUnlock()
	for _, c := range conns {
		c.enqueue(msg)
	}
}

func (h *clawRealtimeHub) sendToUser(uid UserId, cmd string, val any) {
	if uid == "" {
		return
	}
	msg, ok := clawRealtimeMessage(cmd, val)
	if !ok {
		return
	}
	h.RLock()
	conns := make([]*clawRealtimeConn, 0, len(h.userConns[uid]))
	for c := range h.userConns[uid] {
		conns = append(conns, c)
	}
	h.RUnlock()
	for _, c := range conns {
		c.enqueue(msg)
	}
}

func (c *clawRealtimeConn) enqueue(msg []byte) {
	select {
	case c.send <- msg:
	default:
	}
}

func (c *clawRealtimeConn) sendJSON(payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	c.enqueue(data)
}

func clawRealtimeMessage(cmd string, val any) ([]byte, bool) {
	data, err := json.Marshal(map[string]any{
		"cmd": cmd,
		"val": val,
	})
	return data, err == nil
}

func clawWSHandler(c *gin.Context) {
	ws, err := clawWSUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}

	conn := &clawRealtimeConn{send: make(chan []byte, 256)}
	if key := extractAuthKey(c); key != "" {
		if user := authenticateWithKey(key); user != nil {
			conn.userId = user.GetId()
			conn.username = user.GetUsername()
		} else if user, _, err := authenticateWithSubTokenFast(key); err == nil && user != nil {
			conn.userId = user.GetId()
			conn.username = user.GetUsername()
		}
	}

	clawHub.register(conn)
	go conn.writePump(ws)
	conn.sendInitialState()
	conn.readPump(ws)
}

func (c *clawRealtimeConn) sendInitialState() {
	c.sendJSON(map[string]any{
		"cmd": "handshake",
		"val": map[string]any{
			"server":  "claw-websocket",
			"version": clawRealtimeVersion,
		},
	})
	if c.userId != "" {
		c.sendJSON(map[string]any{
			"cmd":      "auth",
			"success":  true,
			"user_id":  string(c.userId),
			"username": string(c.username),
		})
	}
	c.sendJSON(map[string]any{
		"cmd": "posts",
		"val": latestPublicPosts(clawRealtimeHistorySize),
	})
}

func (c *clawRealtimeConn) readPump(ws *websocket.Conn) {
	defer func() {
		clawHub.unregister(c)
		ws.Close()
	}()
	ws.SetReadLimit(4096)
	ws.SetReadDeadline(time.Now().Add(clawRealtimePongWait))
	ws.SetPongHandler(func(string) error {
		ws.SetReadDeadline(time.Now().Add(clawRealtimePongWait))
		return nil
	})
	for {
		_, raw, err := ws.ReadMessage()
		if err != nil {
			return
		}
		var msg map[string]json.RawMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		var cmd string
		_ = json.Unmarshal(msg["cmd"], &cmd)
		switch cmd {
		case "auth":
			c.handleRealtimeAuth(msg)
		case "ping":
			c.sendJSON(map[string]any{"cmd": "pong"})
		}
	}
}

func (c *clawRealtimeConn) handleRealtimeAuth(msg map[string]json.RawMessage) {
	var key string
	_ = json.Unmarshal(msg["key"], &key)
	if key == "" {
		_ = json.Unmarshal(msg["auth"], &key)
	}
	if key == "" {
		c.sendJSON(map[string]any{"cmd": "auth", "success": false, "error": "key required"})
		return
	}
	if user := authenticateWithKey(key); user != nil {
		clawHub.authenticateConn(c, user)
		return
	}
	if user, _, err := authenticateWithSubTokenFast(key); err == nil && user != nil {
		clawHub.authenticateConn(c, user)
		return
	}
	c.sendJSON(map[string]any{"cmd": "auth", "success": false, "error": "invalid key"})
}

func (c *clawRealtimeConn) writePump(ws *websocket.Conn) {
	ticker := time.NewTicker(clawRealtimePingEvery)
	defer func() {
		ticker.Stop()
		ws.Close()
	}()
	for {
		select {
		case data, ok := <-c.send:
			ws.SetWriteDeadline(time.Now().Add(clawRealtimeWriteWait))
			if !ok {
				_ = ws.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := ws.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}
		case <-ticker.C:
			ws.SetWriteDeadline(time.Now().Add(clawRealtimeWriteWait))
			if err := ws.WriteJSON(map[string]any{"cmd": "ping"}); err != nil {
				return
			}
		}
	}
}

func latestPublicPosts(limit int) []NetPost {
	if limit <= 0 {
		return []NetPost{}
	}
	postsMutex.RLock()
	defer postsMutex.RUnlock()

	out := make([]NetPost, 0, limit)
	for i := len(posts) - 1; i >= 0 && len(out) < limit; i-- {
		if posts[i].ProfileOnly {
			continue
		}
		out = append(out, posts[i].ToNet())
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func clawRealtimeReceiveEvent(eventType string, data any) {
	if eventType == "" {
		return
	}
	clawHub.broadcast(eventType, data)
}

func clawRealtimeSendUserEvent(uid UserId, event Event) {
	payload := map[string]any{}
	for k, v := range event.Data {
		payload[k] = v
	}
	payload["type"] = event.Type
	payload["id"] = event.ID
	payload["timestamp"] = event.Timestamp

	clawHub.sendToUser(uid, "event", payload)
	clawHub.sendToUser(uid, event.Type, payload)
}

func startClawRealtimeServers() {
	ws := gin.New()
	ws.Use(gin.Recovery(), corsMiddleware())
	ws.GET("/", clawWSHandler)
	ws.GET("/ws", clawWSHandler)

	go func() {
		log.Println("[ClawWS] WebSocket server starting on port 5610...")
		if err := ws.Run("127.0.0.1:5610"); err != nil {
			log.Fatalf("[ClawWS] failed to start WebSocket server: %v", err)
		}
	}()
}
