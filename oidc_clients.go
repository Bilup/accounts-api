package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"sync"
	"time"

	"claw/internal/config"
)

var (
	oidcClientsMutex sync.RWMutex
	oidcClients      = make(map[string]OIDCClient) // client_id -> client
)

// loadOIDCClients 从 oidc_clients.json 加载已注册的客户端。
func loadOIDCClients() {
	data, err := os.ReadFile(config.OIDC_CLIENTS_FILE)
	if err != nil {
		log.Printf("[oidc] No clients file to load (%v), starting empty", err)
		return
	}
	var list []OIDCClient
	if err := json.Unmarshal(data, &list); err != nil {
		log.Printf("[oidc] Warning: failed to parse %s: %v", config.OIDC_CLIENTS_FILE, err)
		return
	}
	oidcClientsMutex.Lock()
	oidcClients = make(map[string]OIDCClient, len(list))
	for _, c := range list {
		oidcClients[c.ClientID] = c
	}
	oidcClientsMutex.Unlock()
	log.Printf("[oidc] Loaded %d OIDC clients", len(list))
}

// saveOIDCClients 将客户端列表持久化到 oidc_clients.json。
func saveOIDCClients() {
	oidcClientsMutex.RLock()
	list := make([]OIDCClient, 0, len(oidcClients))
	for _, c := range oidcClients {
		list = append(list, c)
	}
	oidcClientsMutex.RUnlock()

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		log.Printf("[oidc] Failed to marshal clients: %v", err)
		return
	}
	if err := os.WriteFile(config.OIDC_CLIENTS_FILE, data, 0644); err != nil {
		log.Printf("[oidc] Failed to write clients file: %v", err)
	}
}

// getOIDCClient 按 client_id 取客户端。
func getOIDCClient(clientID string) (OIDCClient, bool) {
	oidcClientsMutex.RLock()
	defer oidcClientsMutex.RUnlock()
	c, ok := oidcClients[clientID]
	return c, ok
}

// listOIDCClients 返回所有客户端（不含 secret hash）。
func listOIDCClients() []OIDCClient {
	oidcClientsMutex.RLock()
	defer oidcClientsMutex.RUnlock()
	out := make([]OIDCClient, 0, len(oidcClients))
	for _, c := range oidcClients {
		// 剥离 secret hash 后返回
		safe := c
		safe.ClientSecretHash = ""
		out = append(out, safe)
	}
	return out
}

// registerOIDCClient 创建新客户端，返回客户端与明文 secret（仅此一次）。
func registerOIDCClient(name string, redirectURIs []string) (OIDCClient, string) {
	clientID := randomToken(8)           // 16 hex 字符
	plaintextSecret := randomToken(16)   // 32 hex 字符
	h := sha256.Sum256([]byte(plaintextSecret))

	client := OIDCClient{
		ClientID:         clientID,
		ClientName:       name,
		ClientSecretHash: hex.EncodeToString(h[:]),
		RedirectURIs:     redirectURIs,
		CreatedAt:        time.Now().Unix(),
	}

	oidcClientsMutex.Lock()
	oidcClients[clientID] = client
	oidcClientsMutex.Unlock()
	go saveOIDCClients()

	return client, plaintextSecret
}

// deleteOIDCClient 删除客户端，返回是否删除成功。
func deleteOIDCClient(clientID string) bool {
	oidcClientsMutex.Lock()
	defer oidcClientsMutex.Unlock()
	if _, ok := oidcClients[clientID]; !ok {
		return false
	}
	delete(oidcClients, clientID)
	go saveOIDCClients()
	return true
}
