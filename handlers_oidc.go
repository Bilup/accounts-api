package main

import (
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	"claw/internal/config"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// --- 辅助 ---

func oidcEndpoint(path string) string {
	return config.OIDC_ISSUER + path
}

// scopeContains 检查空格分隔的 scope 字符串是否包含某 scope。
func scopeContains(scope, target string) bool {
	for _, s := range strings.Fields(scope) {
		if s == target {
			return true
		}
	}
	return false
}

// scopeDescription 把 scope 列表转成人类可读描述。
func scopeDescription(scope string) string {
	var parts []string
	for _, s := range strings.Fields(scope) {
		switch s {
		case "openid":
			parts = append(parts, "验证你的身份 (openid)")
		case "profile":
			parts = append(parts, "读取你的用户名、头像与简介 (profile)")
		case "email":
			parts = append(parts, "读取你的邮箱地址 (email)")
		default:
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return "验证你的身份 (openid)"
	}
	return strings.Join(parts, "、")
}

// oidcRedirectError 向客户端回调 URL 返回 OAuth 错误。
func oidcRedirectError(c *gin.Context, redirectURI, errCode, state string) {
	q := url.Values{}
	q.Set("error", errCode)
	if state != "" {
		q.Set("state", state)
	}
	c.Redirect(http.StatusFound, redirectURI+"?"+q.Encode())
}

// userPassesLoginGate 复用现有登录门禁检查（封禁/邮箱验证/TOS）。
// 返回 true 表示通过，false 表示已在响应中拒绝。
func userPassesLoginGate(c *gin.Context, user *User) bool {
	if user.IsBanned() {
		c.JSON(http.StatusForbidden, gin.H{"error": "User is banned"})
		return false
	}
	if user.Get("sys.email_verified") != true {
		c.JSON(http.StatusForbidden, gin.H{"error": "Email address not verified"})
		return false
	}
	if user.Get("sys.tos_accepted") != true {
		c.JSON(http.StatusForbidden, gin.H{"error": "Terms of Service not accepted"})
		return false
	}
	return true
}

// --- 1. 发现端点 ---

func oidcDiscovery(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"issuer":                                config.OIDC_ISSUER,
		"authorization_endpoint":                oidcEndpoint("/oauth/authorize"),
		"token_endpoint":                        oidcEndpoint("/oauth/token"),
		"userinfo_endpoint":                     oidcEndpoint("/oauth/userinfo"),
		"jwks_uri":                              oidcEndpoint("/.well-known/jwks.json"),
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"scopes_supported":                      []string{"openid", "profile", "email"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_post", "client_secret_basic"},
		"code_challenge_methods_supported":      []string{"plain", "S256"},
		"claims_supported": []string{
			"sub", "iss", "aud", "exp", "iat", "nonce",
			"preferred_username", "name", "email", "email_verified",
			"picture", "profile", "bio",
		},
	})
}

// --- 2. JWKS 端点 ---

func oidcJWKS(c *gin.Context) {
	c.JSON(http.StatusOK, publicJWKS())
}

// --- 3. 授权端点 ---

func oidcAuthorize(c *gin.Context) {
	clientID := c.Query("client_id")
	redirectURI := c.Query("redirect_uri")
	responseType := c.Query("response_type")
	scope := c.Query("scope")
	state := c.Query("state")
	nonce := c.Query("nonce")
	codeChallenge := c.Query("code_challenge")
	codeChallengeMethod := c.Query("code_challenge_method")

	// 校验必填参数
	if clientID == "" || redirectURI == "" || responseType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "error_description": "client_id, redirect_uri and response_type are required"})
		return
	}
	if responseType != "code" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported_response_type", "error_description": "only 'code' is supported"})
		return
	}

	// 校验客户端与 redirect_uri
	client, ok := getOIDCClient(clientID)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_client", "error_description": "unknown client_id"})
		return
	}
	if !client.hasRedirectURI(redirectURI) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "error_description": "redirect_uri not registered"})
		return
	}

	// 检测当前登录态（复用 session cookie / auth key 机制）
	var currentUser *User
	if key := extractAuthKey(c); key != "" {
		user, _ := authenticateAnyKey(key)
		currentUser = user
	}

	// 未登录 → 渲染登录页
	if currentUser == nil {
		renderOIDCLogin(c, c.Request.URL.String())
		return
	}

	// 已登录但未通过门禁 → 提示
	if !userPassesLoginGate(c, currentUser) {
		return
	}

	// 已登录且通过门禁 → 生成待确认授权，渲染授权确认页
	consentID := randomToken(16)
	storePendingConsent(pendingConsent{
		consentID:           consentID,
		clientID:            clientID,
		clientName:          client.ClientName,
		userID:              currentUser.GetId(),
		redirectURI:         redirectURI,
		scope:               scope,
		state:               state,
		nonce:               nonce,
		codeChallenge:       codeChallenge,
		codeChallengeMethod: codeChallengeMethod,
		createdAt:           time.Now(),
	})
	renderOIDCConsent(c, client.ClientName, scope, consentID)
}

// --- 4. 授权确认处理 ---

func oidcConsent(c *gin.Context) {
	consentID := c.PostForm("consent_id")
	action := c.PostForm("action")

	pending, ok := consumePendingConsent(consentID)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_grant", "error_description": "consent session expired or invalid"})
		return
	}

	if time.Since(pending.createdAt) > oidcPendingConsentTTL {
		oidcRedirectError(c, pending.redirectURI, "access_denied", pending.state)
		return
	}

	if action != "approve" {
		oidcRedirectError(c, pending.redirectURI, "access_denied", pending.state)
		return
	}

	// 生成授权码
	code := randomToken(16)
	storeAuthCode(authCodeEntry{
		code:                code,
		clientID:            pending.clientID,
		userID:              pending.userID,
		redirectURI:         pending.redirectURI,
		scope:               pending.scope,
		nonce:               pending.nonce,
		codeChallenge:       pending.codeChallenge,
		codeChallengeMethod: pending.codeChallengeMethod,
		createdAt:           time.Now(),
	})

	q := url.Values{}
	q.Set("code", code)
	if pending.state != "" {
		q.Set("state", pending.state)
	}
	c.Redirect(http.StatusFound, pending.redirectURI+"?"+q.Encode())
}

// --- 5. OIDC 登录 ---

func oidcLogin(c *gin.Context) {
	username := c.PostForm("username")
	password := c.PostForm("password")
	returnTo := c.PostForm("return_to")

	if username == "" || password == "" {
		renderOIDCLogin(c, returnTo)
		return
	}

	user, err := findAccountByLogin(username, password)
	if err != nil || len(user) == 0 {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusOK, oidcLoginPageHTML(returnTo, "用户名或密码错误"))
		return
	}
	if user.IsBanned() {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusOK, oidcLoginPageHTML(returnTo, "账号已被封禁"))
		return
	}
	if user.Get("sys.email_verified") != true {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusOK, oidcLoginPageHTML(returnTo, "邮箱未验证，请先验证邮箱"))
		return
	}
	if user.Get("sys.tos_accepted") != true {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusOK, oidcLoginPageHTML(returnTo, "请先接受服务条款"))
		return
	}

	// 设置 session cookie（复用 v2 机制）
	v2SetSessionCookie(c, user.GetKey())

	if returnTo == "" {
		returnTo = config.OIDC_ISSUER
	}
	c.Redirect(http.StatusFound, returnTo)
}

// --- 6. 令牌端点 ---

func oidcToken(c *gin.Context) {
	grantType := c.PostForm("grant_type")
	if grantType != "authorization_code" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported_grant_type"})
		return
	}

	code := c.PostForm("code")
	redirectURI := c.PostForm("redirect_uri")
	clientID := c.PostForm("client_id")
	clientSecret := c.PostForm("client_secret")
	codeVerifier := c.PostForm("code_verifier")

	// 支持 HTTP Basic 认证（client_secret_basic）
	if clientID == "" || clientSecret == "" {
		if bid, bsecret, ok := c.Request.BasicAuth(); ok {
			clientID = bid
			clientSecret = bsecret
		}
	}

	if clientID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_client", "error_description": "client_id is required"})
		return
	}

	client, ok := getOIDCClient(clientID)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_client"})
		return
	}
	if !client.verifyClientSecret(clientSecret) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_client", "error_description": "client secret mismatch"})
		return
	}

	// 校验授权码
	entry, ok := consumeAuthCode(code)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_grant", "error_description": "authorization code invalid or expired"})
		return
	}
	if time.Since(entry.createdAt) > oidcAuthCodeTTL {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_grant", "error_description": "authorization code expired"})
		return
	}
	if entry.clientID != clientID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_grant", "error_description": "code was issued to a different client"})
		return
	}
	if entry.redirectURI != redirectURI {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_grant", "error_description": "redirect_uri mismatch"})
		return
	}

	// PKCE 校验
	if entry.codeChallenge != "" {
		if !verifyPKCE(codeVerifier, entry.codeChallenge, entry.codeChallengeMethod) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_grant", "error_description": "PKCE verification failed"})
			return
		}
	}

	// 取用户
	user := getUserById(entry.userID)
	if user == nil || len(user) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_grant", "error_description": "user no longer exists"})
		return
	}

	// 生成 access_token
	accessToken := randomToken(32)
	now := time.Now()
	expiresIn := int(oidcAccessTokenTTL.Seconds())
	storeAccessToken(accessTokenEntry{
		token:     accessToken,
		userID:    entry.userID,
		clientID:  clientID,
		scope:     entry.scope,
		createdAt: now,
		expiresAt: now.Add(oidcAccessTokenTTL),
	})

	// 签发 id_token
	claims := jwt.MapClaims{
		"iss": config.OIDC_ISSUER,
		"sub": string(user.GetId()),
		"aud": clientID,
		"exp": now.Add(oidcAccessTokenTTL).Unix(),
		"iat": now.Unix(),
	}
	if entry.nonce != "" {
		claims["nonce"] = entry.nonce
	}
	if scopeContains(entry.scope, "profile") {
		claims["preferred_username"] = string(user.GetUsername())
		claims["name"] = string(user.GetUsername())
		claims["picture"] = avatarURL(user.GetUsername())
	}
	if scopeContains(entry.scope, "email") {
		claims["email"] = user.GetEmail()
		claims["email_verified"] = user.Get("sys.email_verified") == true
	}

	idToken, err := signIDToken(claims)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server_error", "error_description": "failed to sign id_token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"access_token": accessToken,
		"token_type":   "Bearer",
		"expires_in":   expiresIn,
		"id_token":     idToken,
		"scope":        entry.scope,
	})
}

// --- 7. 用户信息端点 ---

func oidcUserinfo(c *gin.Context) {
	accessToken := stripBearer(c.GetHeader("Authorization"))
	if accessToken == "" {
		accessToken = c.Query("access_token")
	}
	if accessToken == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token", "error_description": "access token is required"})
		return
	}

	entry, ok := lookupAccessToken(accessToken)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token", "error_description": "token invalid or expired"})
		return
	}

	user := getUserById(entry.userID)
	if user == nil || len(user) == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token", "error_description": "user not found"})
		return
	}

	resp := gin.H{
		"sub": string(user.GetId()),
	}
	if scopeContains(entry.scope, "profile") {
		resp["preferred_username"] = string(user.GetUsername())
		resp["name"] = string(user.GetUsername())
		resp["picture"] = avatarURL(user.GetUsername())
		resp["profile"] = oidcEndpoint("/get_user_new?username=" + url.QueryEscape(string(user.GetUsername())))
		resp["bio"] = user.GetString("bio")
	}
	if scopeContains(entry.scope, "email") {
		resp["email"] = user.GetEmail()
		resp["email_verified"] = user.Get("sys.email_verified") == true
	}
	c.JSON(http.StatusOK, resp)
}

// --- 管理员：客户端管理 ---

func createOIDCClient(c *gin.Context) {
	var req struct {
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if req.ClientName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "client_name is required"})
		return
	}
	if len(req.RedirectURIs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "at least one redirect_uri is required"})
		return
	}

	client, plaintextSecret := registerOIDCClient(req.ClientName, req.RedirectURIs)
	c.JSON(http.StatusCreated, gin.H{
		"client_id":     client.ClientID,
		"client_name":   client.ClientName,
		"client_secret": plaintextSecret,
		"redirect_uris": client.RedirectURIs,
		"created_at":    client.CreatedAt,
		"note":          "请妥善保存 client_secret，此为唯一一次明文返回",
	})
}

func listOIDCClientsHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"clients": listOIDCClients()})
}

func deleteOIDCClientHandler(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "client id is required"})
		return
	}
	if !deleteOIDCClient(id) {
		c.JSON(http.StatusNotFound, gin.H{"error": "client not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "client deleted"})
}

// --- HTML 页面 ---

func renderOIDCLogin(c *gin.Context, returnTo string) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, oidcLoginPageHTML(returnTo, ""))
}

func renderOIDCConsent(c *gin.Context, clientName, scope, consentID string) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, oidcConsentPageHTML(clientName, scope, consentID))
}

func oidcLoginPageHTML(returnTo, errMsg string) string {
	escReturn := html.EscapeString(returnTo)
	escErr := html.EscapeString(errMsg)
	errBlock := ""
	if escErr != "" {
		errBlock = `<p style="color:#e53935;font-size:14px;margin:8px 0">` + escErr + `</p>`
	}
	return `<!DOCTYPE html><html lang="zh"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>登录 - Bilup Accounts</title>
<style>body{font-family:system-ui,sans-serif;background:#0d1117;color:#e6edf3;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0}
.card{background:#161b22;border:1px solid #30363d;border-radius:8px;padding:32px;width:320px}
h1{font-size:20px;margin:0 0 8px}
label{display:block;font-size:13px;margin:12px 0 4px;color:#8b949e}
input{width:100%;box-sizing:border-box;padding:8px;border-radius:6px;border:1px solid #30363d;background:#0d1117;color:#e6edf3;font-size:14px}
button{width:100%;margin-top:16px;padding:10px;border:none;border-radius:6px;background:#238636;color:#fff;font-size:14px;cursor:pointer}
button:hover{background:#2ea043}
</style></head><body><form class="card" method="POST" action="/oauth/login">
<h1>登录 Bilup Accounts</h1>
<p style="font-size:13px;color:#8b949e;margin:0">请登录以继续授权流程</p>
` + errBlock + `
<input type="hidden" name="return_to" value="` + escReturn + `">
<label>用户名</label><input name="username" autocomplete="username" required autofocus>
<label>密码</label><input name="password" type="password" autocomplete="current-password" required>
<button type="submit">登录</button>
</form></body></html>`
}

func oidcConsentPageHTML(clientName, scope, consentID string) string {
	escName := html.EscapeString(clientName)
	escScope := html.EscapeString(scope)
	desc := html.EscapeString(scopeDescription(scope))
	return `<!DOCTYPE html><html lang="zh"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>授权 - Bilup Accounts</title>
<style>body{font-family:system-ui,sans-serif;background:#0d1117;color:#e6edf3;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0}
.card{background:#161b22;border:1px solid #30363d;border-radius:8px;padding:32px;width:380px;text-align:center}
h1{font-size:20px;margin:0 0 8px}
p{font-size:14px;color:#8b949e;line-height:1.6}
.scopes{text-align:left;background:#0d1117;border:1px solid #30363d;border-radius:6px;padding:12px;margin:16px 0;font-size:13px}
.app{color:#58a6ff;font-weight:600}
button{width:48%;padding:10px;border:none;border-radius:6px;font-size:14px;cursor:pointer;margin:4px}
.approve{background:#238636;color:#fff}.approve:hover{background:#2ea043}
.deny{background:#21262d;color:#e6edf3;border:1px solid #30363d}.deny:hover{background:#30363d}
.row{display:flex;justify-content:space-between;margin-top:16px}
</style></head><body><form class="card" method="POST" action="/oauth/consent">
<input type="hidden" name="consent_id" value="` + html.EscapeString(consentID) + `">
<input type="hidden" name="scope" value="` + escScope + `">
<h1>授权请求</h1>
<p>应用 <span class="app">` + escName + `</span> 请求访问你的 Bilup 账号信息：</p>
<div class="scopes">` + desc + `</div>
<p style="font-size:12px">授权后该应用将能够读取上述信息。你可随时撤销授权。</p>
<div class="row">
<button class="deny" type="submit" name="action" value="deny">拒绝</button>
<button class="approve" type="submit" name="action" value="approve">授权</button>
</div>
</form></body></html>`
}
