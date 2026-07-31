package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"os"
	"sync"
	"time"

	"claw/internal/config"

	"github.com/golang-jwt/jwt/v5"
)

// --- TTL 常量 ---

const (
	oidcAuthCodeTTL       = 5 * time.Minute
	oidcAccessTokenTTL    = 1 * time.Hour
	oidcPendingConsentTTL = 10 * time.Minute
)

// --- 客户端类型 ---

// OIDCClient 表示一个已注册的 OAuth/OIDC 客户端（如 Forgejo）。
type OIDCClient struct {
	ClientID         string   `json:"client_id"`
	ClientName       string   `json:"client_name"`
	ClientSecretHash string   `json:"client_secret_hash"` // sha256 hex
	RedirectURIs     []string `json:"redirect_uris"`
	CreatedAt        int64    `json:"created_at"`
}

// verifyClientSecret 常量时间比较提供的明文 secret 的 sha256 与存储的 hash。
func (c *OIDCClient) verifyClientSecret(provided string) bool {
	h := sha256.Sum256([]byte(provided))
	got := hex.EncodeToString(h[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(c.ClientSecretHash)) == 1
}

// hasRedirectURI 校验 redirect_uri 是否在白名单内。
func (c *OIDCClient) hasRedirectURI(uri string) bool {
	for _, u := range c.RedirectURIs {
		if u == uri {
			return true
		}
	}
	return false
}

// --- 内存状态：授权码 / 访问令牌 / 待确认授权 ---

type authCodeEntry struct {
	code                string
	clientID            string
	userID              UserId
	redirectURI         string
	scope               string
	nonce               string
	codeChallenge       string
	codeChallengeMethod string
	createdAt           time.Time
}

type accessTokenEntry struct {
	token     string
	userID    UserId
	clientID  string
	scope     string
	createdAt time.Time
	expiresAt time.Time
}

type pendingConsent struct {
	consentID string
	clientID  string
	clientName string
	userID    UserId
	redirectURI string
	scope      string
	state      string
	nonce      string
	codeChallenge       string
	codeChallengeMethod string
	createdAt time.Time
}

var (
	oidcStateMutex      sync.RWMutex
	oidcAuthCodes       = make(map[string]authCodeEntry)
	oidcAccessTokens    = make(map[string]accessTokenEntry)
	oidcPendingConsents = make(map[string]pendingConsent)
)

// --- RSA 签名密钥 ---

var (
	oidcSignKeyMutex sync.RWMutex
	oidcPrivateKey   *rsa.PrivateKey
	oidcPublicKey    *rsa.PublicKey
	oidcKeyID        string
)

// initOIDCSigningKey 加载或生成用于签发 id_token 的 RSA-2048 密钥对。
func initOIDCSigningKey() {
	oidcSignKeyMutex.Lock()
	defer oidcSignKeyMutex.Unlock()

	path := config.OIDC_SIGNING_KEY_FILE
	if data, err := os.ReadFile(path); err == nil {
		block, _ := pem.Decode(data)
		if block != nil {
			key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
			if err == nil {
				oidcPrivateKey = key
				oidcPublicKey = &key.PublicKey
				oidcKeyID = computeKeyID(oidcPublicKey)
				log.Printf("[oidc] Loaded signing key from %s (kid=%s)", path, oidcKeyID)
				return
			}
		}
		log.Printf("[oidc] Existing key file unreadable, regenerating: %v", err)
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatalf("[oidc] Failed to generate RSA key: %v", err)
	}
	oidcPrivateKey = key
	oidcPublicKey = &key.PublicKey
	oidcKeyID = computeKeyID(oidcPublicKey)

	keyBytes := x509.MarshalPKCS1PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: keyBytes})
	if err := os.WriteFile(path, pemBytes, 0600); err != nil {
		log.Printf("[oidc] Warning: Failed to persist signing key to %s: %v", path, err)
	} else {
		log.Printf("[oidc] Generated and saved new signing key to %s (kid=%s)", path, oidcKeyID)
	}
}

// computeKeyID 根据公钥派生稳定的 kid（公钥 DER 的 sha256 前 8 字节 hex）。
func computeKeyID(pub *rsa.PublicKey) string {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "default"
	}
	h := sha256.Sum256(der)
	return hex.EncodeToString(h[:8])
}

// signIDToken 使用 RS256 签发 id_token。
func signIDToken(claims jwt.MapClaims) (string, error) {
	oidcSignKeyMutex.RLock()
	defer oidcSignKeyMutex.RUnlock()

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = oidcKeyID
	return token.SignedString(oidcPrivateKey)
}

// publicJWKS 构造 JWKS（仅含当前公钥）。
func publicJWKS() map[string]any {
	oidcSignKeyMutex.RLock()
	defer oidcSignKeyMutex.RUnlock()

	return map[string]any{
		"keys": []map[string]any{
			{
				"kty": "RSA",
				"use": "sig",
				"alg": "RS256",
				"kid": oidcKeyID,
				"n":   base64urlBigInt(oidcPublicKey.N),
				"e":   base64urlBigInt(big.NewInt(int64(oidcPublicKey.E))),
			},
		},
	}
}

func base64urlBigInt(b *big.Int) string {
	return base64.RawURLEncoding.EncodeToString(b.Bytes())
}

// --- 内存状态辅助函数 ---

func storeAuthCode(entry authCodeEntry) {
	oidcStateMutex.Lock()
	oidcAuthCodes[entry.code] = entry
	oidcStateMutex.Unlock()
}

func consumeAuthCode(code string) (authCodeEntry, bool) {
	oidcStateMutex.Lock()
	defer oidcStateMutex.Unlock()
	entry, ok := oidcAuthCodes[code]
	if ok {
		delete(oidcAuthCodes, code)
	}
	return entry, ok
}

func storeAccessToken(entry accessTokenEntry) {
	oidcStateMutex.Lock()
	oidcAccessTokens[entry.token] = entry
	oidcStateMutex.Unlock()
}

func lookupAccessToken(token string) (accessTokenEntry, bool) {
	oidcStateMutex.RLock()
	defer oidcStateMutex.RUnlock()
	entry, ok := oidcAccessTokens[token]
	if !ok {
		return accessTokenEntry{}, false
	}
	if time.Now().After(entry.expiresAt) {
		return accessTokenEntry{}, false
	}
	return entry, true
}

func storePendingConsent(entry pendingConsent) {
	oidcStateMutex.Lock()
	oidcPendingConsents[entry.consentID] = entry
	oidcStateMutex.Unlock()
}

func consumePendingConsent(consentID string) (pendingConsent, bool) {
	oidcStateMutex.Lock()
	defer oidcStateMutex.Unlock()
	entry, ok := oidcPendingConsents[consentID]
	if ok {
		delete(oidcPendingConsents, consentID)
	}
	return entry, ok
}

// peekPendingConsent 非消费式查询待确认授权（供前端展示授权信息）。
func peekPendingConsent(consentID string) (pendingConsent, bool) {
	oidcStateMutex.RLock()
	defer oidcStateMutex.RUnlock()
	entry, ok := oidcPendingConsents[consentID]
	return entry, ok
}

// --- 随机 token 生成 ---

func randomToken(byteLen int) string {
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("failed to generate random bytes: %v", err))
	}
	return hex.EncodeToString(b)
}

// --- 清理 goroutine ---

// StartOIDCCleanup 定时清理过期的授权码、访问令牌与待确认授权。
func StartOIDCCleanup() {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now()
			oidcStateMutex.Lock()
			for k, v := range oidcAuthCodes {
				if now.Sub(v.createdAt) > oidcAuthCodeTTL {
					delete(oidcAuthCodes, k)
				}
			}
			for k, v := range oidcAccessTokens {
				if now.After(v.expiresAt) {
					delete(oidcAccessTokens, k)
				}
			}
			for k, v := range oidcPendingConsents {
				if now.Sub(v.createdAt) > oidcPendingConsentTTL {
					delete(oidcPendingConsents, k)
				}
			}
			oidcStateMutex.Unlock()
		}
	}()
}

// --- PKCE 校验 ---

// verifyPKCE 按 code_challenge_method 校验 code_verifier 与存储的 code_challenge。
func verifyPKCE(verifier, challenge, method string) bool {
	switch method {
	case "", "plain":
		return verifier == challenge
	case "S256":
		h := sha256.Sum256([]byte(verifier))
		return base64.RawURLEncoding.EncodeToString(h[:]) == challenge
	default:
		return false
	}
}
