package main

import (
	"encoding/json"
	"maps"
	"time"
)

func roomFromMsg(c *Conn, msg map[string]json.RawMessage) (string, bool) {
	var room string
	if b, ok := msg["room"]; ok {
		if err := json.Unmarshal(b, &room); err != nil {
			c.sendError("invalid room")
			return "", false
		}
	}
	if room == "" {
		c.sendError("room required")
		return "", false
	}
	hub.Lock()
	_, inRoom := c.rooms[room]
	hub.Unlock()
	if !inRoom {
		c.sendError("not in room: " + room)
		return "", false
	}
	return room, true
}

func (c *Conn) sendAck(cmd, room string, msg map[string]json.RawMessage) {
	ack := map[string]any{"cmd": cmd, "room": room}
	if b, ok := msg["listener"]; ok {
		var listener any
		if json.Unmarshal(b, &listener) == nil {
			ack["listener"] = listener
		}
	}
	c.sendMsg(ack)
}

func (c *Conn) handleGmsg(msg map[string]json.RawMessage) {
	room, ok := roomFromMsg(c, msg)
	if !ok {
		return
	}
	val, ok := msg["val"]
	if !ok {
		c.sendError("val required")
		return
	}
	payload := map[string]any{
		"room":      room,
		"val":       json.RawMessage(val),
		"origin":    map[string]any{"user_id": string(c.userId), "username": string(c.username)},
		"timestamp": time.Now().UnixMilli(),
	}
	hub.broadcast(room, "gmsg", payload, c.userId)
	c.sendAck("gmsg_ok", room, msg)
}

func (c *Conn) handlePmsg(msg map[string]json.RawMessage) {
	room, ok := roomFromMsg(c, msg)
	if !ok {
		return
	}
	val, ok := msg["val"]
	if !ok {
		c.sendError("val required")
		return
	}
	var to string
	if b, ok := msg["to"]; !ok || json.Unmarshal(b, &to) != nil || to == "" {
		if b, ok := msg["id"]; !ok || json.Unmarshal(b, &to) != nil || to == "" {
			c.sendError("to required")
			return
		}
	}
	targetId := getIdByUsername(Username(to).ToLower())
	if targetId == "" {
		targetId = UserId(to)
	}
	payload := map[string]any{
		"room":      room,
		"val":       json.RawMessage(val),
		"origin":    map[string]any{"user_id": string(c.userId), "username": string(c.username)},
		"timestamp": time.Now().UnixMilli(),
	}
	if !hub.sendToUserInRoom(targetId, room, "pmsg", payload) {
		c.sendError("user not in room")
		return
	}
	c.sendAck("pmsg_ok", room, msg)
}

func (h *Hub) sendToUserInRoom(uid UserId, roomName, cmd string, payload map[string]any) bool {
	wrapper := map[string]any{"cmd": cmd}
	maps.Copy(wrapper, payload)
	data, err := json.Marshal(wrapper)
	if err != nil {
		return false
	}
	h.Lock()
	defer h.Unlock()
	r, ok := h.rooms[roomName]
	if !ok {
		return false
	}
	if _, member := r.members[uid]; !member {
		return false
	}
	for _, cc := range h.userConns[uid] {
		if _, inRoom := cc.rooms[roomName]; !inRoom {
			continue
		}
		select {
		case cc.send <- data:
		default:
		}
	}
	return true
}
