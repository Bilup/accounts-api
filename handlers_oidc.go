package main

import (
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

// loginGateError 返回登录门禁错误码（空字符串表示通过）。
// 返回的是稳定错误 ID，前端据此做国际化展示。复用现有登录门禁检查（封禁/邮箱验证/TOS）。
func loginGateError(user *User) string {
	if user.IsBanned() {
		return "account_banned"
	}
	if user.Get("sys.email_verified") != true {
		return "email_not_verified"
	}
	if user.Get("sys.tos_accepted") != true {
		return "tos_not_accepted"
	}
	return ""
}

// buildCallbackURL 构造带 query 的客户端回调 URL（用于授权确认后返回给前端跳转）。
func buildCallbackURL(redirectURI string, params url.Values) string {
	return redirectURI + "?" + params.Encode()
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

// oidcAuthorize 处理授权请求：未登录则 302 重定向到前端登录页（accounts.bilup.org/auth），
// 已登录则 302 重定向到前端授权确认页（accounts.bilup.org/consent）。
// 前端登录页登录成功后会带 ?token=<登录token> 跳回本端点，据此识别登录态；
// 授权确认页通过 /oauth/consent_info 与 /oauth/consent 完成交互。
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

	// 构造完整的 authorize URL，供登录后回跳
	returnTo := config.OIDC_ISSUER + c.Request.URL.Path
	if c.Request.URL.RawQuery != "" {
		returnTo += "?" + c.Request.URL.RawQuery
	}

	// 检测当前登录态：优先读 token（前端 /auth 登录页跳回时携带 ?token=），
	// 其次复用 extractAuthKey（auth query / Authorization header / session cookie）。
	var currentUser *User
	authKey := c.Query("token")
	if authKey == "" {
		authKey = extractAuthKey(c)
	}
	if authKey != "" {
		user, _ := authenticateAnyKey(authKey)
		currentUser = user
	}

	// 未登录 → 重定向到前端登录页 accounts.bilup.org/auth?return_to=...
	// 前端登录成功后会跳回 return_to 并附上 ?token=<登录token>
	if currentUser == nil {
		q := url.Values{}
		q.Set("return_to", returnTo)
		c.Redirect(http.StatusFound, config.OIDC_FRONTEND_URL+"/auth?"+q.Encode())
		return
	}

	// 已登录但未通过门禁（封禁/邮箱未验证/TOS 未接受）→ 重定向到前端登录页并附带错误码
	// error 为稳定错误 ID，前端据此做国际化展示
	if errCode := loginGateError(currentUser); errCode != "" {
		q := url.Values{}
		q.Set("return_to", returnTo)
		q.Set("error", errCode)
		c.Redirect(http.StatusFound, config.OIDC_FRONTEND_URL+"/auth?"+q.Encode())
		return
	}

	// 已登录且通过门禁 → 生成待确认授权，重定向到前端授权确认页
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
	q := url.Values{}
	q.Set("consent_id", consentID)
	c.Redirect(http.StatusFound, config.OIDC_FRONTEND_URL+"/consent?"+q.Encode())
}

// --- 4. 授权确认信息（GET，供前端展示） ---

// oidcConsentInfo 返回待确认授权的详情（应用名、scope 等），不消费该授权。
func oidcConsentInfo(c *gin.Context) {
	consentID := c.Query("consent_id")
	if consentID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "consent_id is required"})
		return
	}
	pending, ok := peekPendingConsent(consentID)
	if !ok || time.Since(pending.createdAt) > oidcPendingConsentTTL {
		c.JSON(http.StatusNotFound, gin.H{"error": "consent session expired or invalid"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"consent_id":        pending.consentID,
		"client_name":       pending.clientName,
		"scope":             pending.scope,
		"scope_description": scopeDescription(pending.scope),
	})
}

// --- 5. 授权确认处理（POST JSON） ---

// oidcConsent 处理前端的授权确认请求，返回最终应跳转的回调 URL。
// 前端拿到 redirect 后执行 window.location.href = redirect 完成回跳。
func oidcConsent(c *gin.Context) {
	var req struct {
		ConsentID string `json:"consent_id"`
		Action    string `json:"action"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if req.ConsentID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "consent_id is required"})
		return
	}

	pending, ok := consumePendingConsent(req.ConsentID)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_grant", "error_description": "consent session expired or invalid"})
		return
	}

	// 过期或拒绝 → 返回带 error 的回调 URL
	if time.Since(pending.createdAt) > oidcPendingConsentTTL || req.Action != "approve" {
		params := url.Values{}
		params.Set("error", "access_denied")
		if pending.state != "" {
			params.Set("state", pending.state)
		}
		c.JSON(http.StatusOK, gin.H{"redirect": buildCallbackURL(pending.redirectURI, params)})
		return
	}

	// 批准 → 生成授权码，返回带 code 的回调 URL
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

	params := url.Values{}
	params.Set("code", code)
	if pending.state != "" {
		params.Set("state", pending.state)
	}
	c.JSON(http.StatusOK, gin.H{"redirect": buildCallbackURL(pending.redirectURI, params)})
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

// --- 8. 用户信息端点 ---

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
