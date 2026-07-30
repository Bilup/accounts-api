# OIDC Provider 实现计划

## Context（背景）

当前 accounts-api 是一个自建的账号系统（Go/Gin），但只支持自有的 token 认证机制，无法作为身份提供者给第三方应用（如 Forgejo）使用。为了让 Forgejo 等支持 OIDC 的应用能通过"点击登录 → 授权 → 获取用户信息（头像、邮箱、简介） → 自动注册/登录"全流程接入，需要将本系统升格为 **OIDC Provider**。

**核心约束**（用户明确要求）：OIDC 作为现有系统的**补充**，一切基于现有机制——复用现有用户存储（users.json）、现有认证（session cookie / `authenticateAnyKey`）、现有管理员机制（`authenticateAdmin` / ADMIN_TOKEN），不引入新的数据库或重型框架。依赖 `golang-jwt/jwt/v5` 和 `go-jose/go-jose/v3` 已在 go.mod 中。

**已确认的设计决策**：
- 授权流程：用户已登录时**显示授权确认页**（HTML，带 Approve/Deny）
- 客户端管理：**JSON 文件 + 管理员 API** 双重方式

---

## 实现方案：手写但符合规范的 OIDC Provider

采用手写实现（与项目现有 JSON 文件存储 + 简单 handler 风格一致），复用已有依赖 `golang-jwt/jwt/v5`（签发 id_token）和 `go-jose/go-jose/v3`（JWKS）。授权码 / 访问令牌使用内存 map + TTL（沿用现有 `validatorInfos` / `usedCodes` 模式）。

### 新增文件

#### 1. `oidc.go` — OIDC 核心类型与状态管理
- **客户端存储类型**：
  ```go
  type OIDCClient struct {
      ClientID         string   `json:"client_id"`
      ClientName       string   `json:"client_name"`
      ClientSecretHash string   `json:"client_secret_hash"` // sha256 hex
      RedirectURIs     []string `json:"redirect_uris"`
      CreatedAt        int64    `json:"created_at"`
  }
  ```
- **内存状态**（带 `sync.RWMutex`，沿用 `handlers_validators.go` / `handlers_link.go` 模式）：
  - `authCodes map[string]authCodeEntry` — 授权码，TTL 5 分钟
  - `accessTokens map[string]accessTokenEntry` — 访问令牌，TTL 1 小时
  - `pendingConsents map[string]pendingConsent` — 待确认授权请求（consent_id → 参数+userId），TTL 10 分钟
- **RSA 签名密钥**：启动时从 `OIDC_SIGNING_KEY_FILE`（默认 `./oidc_signing_key.pem`）加载；不存在则生成 RSA-2048 并写入。`kid` = 公钥 SHA-256 前 8 字节 hex。
- **JWT 签发函数** `signIDToken(claims) (string, error)` — RS256，用 `golang-jwt/jwt/v5`。
- **JWKS 构造函数** `publicJWKS() map[string]any` — 用公钥构造 JWK，含 `kid`。
- **清理 goroutine** `StartOIDCCleanup()` — 定时清理过期 auth code / access token / pending consent（沿用 `StartValidatorCleanup` 模式）。

#### 2. `handlers_oidc.go` — OIDC HTTP handlers
所有端点均为标准 OIDC 路径（**根级别**，不在 /v1 或 /v2 下，确保 Forgejo 兼容）。

| 端点 | 方法 | 说明 |
|------|------|------|
| `/.well-known/openid-configuration` | GET | 发现文档（issuer/各端点 URL/支持的 scope、response_type、signing alg） |
| `/.well-known/jwks.json` | GET | 公钥 JWKS |
| `/oauth/authorize` | GET | 授权端点（见下方流程） |
| `/oauth/consent` | POST | 处理授权确认（Approve/Deny） |
| `/oauth/login` | POST | OIDC 专用登录（用户名密码 → 设 session cookie → 回跳 authorize） |
| `/oauth/token` | POST | 令牌端点（code → access_token + id_token） |
| `/oauth/userinfo` | GET | 用户信息端点（access_token → 用户属性） |

**授权端点 `/oauth/authorize` 流程**：
1. 解析参数：`client_id, redirect_uri, response_type, scope, state, nonce, code_challenge, code_challenge_method`
2. 校验 `response_type=code`；校验 client 存在且 `redirect_uri` 在白名单
3. 通过 `extractAuthKey(c)` + `authenticateAnyKey` 检测 session cookie 是否已登录
4. **未登录** → 渲染最小化登录 HTML（表单 POST 到 `/oauth/login`，含 `return_to` = 完整 authorize URL）
5. **已登录** → 复用现有登录门禁检查（`IsBanned` / `sys.email_verified` / `sys.tos_accepted`，与 [handlers_users.go](file:///e:/Bilup/accounts-api/handlers_users.go) `getUser` 一致）→ 生成 `consent_id`，存入 `pendingConsents`，渲染授权确认 HTML（显示 client_name、请求 scope、Approve/Deny 按钮）

**`/oauth/consent` 流程**：根据 `consent_id` + `action`
- approve → 生成随机 auth code，存入 `authCodes`（含 userId/clientId/redirectUri/scope/nonce/codeChallenge）→ 302 重定向 `redirect_uri?code=...&state=...`
- deny → 302 重定向 `redirect_uri?error=access_denied&state=...`

**`/oauth/login` 流程**：解析 username/password/return_to → `findAccountByLogin` 认证 → 通过则 `v2SetSessionCookie(c, user.GetKey())` 设 session cookie（复用 [v2_support.go](file:///e:/Bilup/accounts-api/v2_support.go)）→ 302 回 `return_to`

**`/oauth/token` 流程**：
1. 解析 `grant_type=authorization_code, code, redirect_uri, client_id, client_secret, code_verifier`
2. `authenticateAdmin` 不适用——这里用 client 凭证：sha256(provided_secret) 与 `ClientSecretHash` 常量时间比较
3. 查 auth code：校验匹配 client_id + redirect_uri + 未过期 → 删除（一次性）
4. 若有 PKCE `code_verifier`：按 `code_challenge_method`(S256) 校验
5. 生成随机 access_token（32 字节 hex）存入 `accessTokens`
6. 签发 id_token（JWT RS256），claims：`iss, sub(user.GetId), aud(client_id), exp, iat, nonce, preferred_username, email, email_verified`
7. 返回 `{access_token, token_type:"Bearer", expires_in:3600, id_token}`

**`/oauth/userinfo` 流程**：
1. `stripBearer(Authorization)` 取 access_token
2. 查 `accessTokens`，校验未过期 → 取 userId → `getUserById`
3. 返回标准 claims：`sub, name, preferred_username, email, email_verified, picture(avatarURL(username)), profile, bio`
   - `picture` 复用 [avatars.go](file:///e:/Bilup/accounts-api/avatars.go) 的 `avatarURL(username)`
   - `bio` 复用 `user.GetString("bio")`

#### 3. `oidc_clients.go` — 客户端存储管理
- `loadOIDCClients()` / `saveOIDCClients()` — 读写 `oidc_clients.json`（沿用 `loadKeys`/`saveKeys` 模式，带 mutex）
- `getOIDCClient(clientID) (*OIDCClient, bool)`
- `registerOIDCClient(name, redirectURIs) (OIDCClient, plaintextSecret)` — 生成 client_id（`crypto/rand` hex 16 字节）+ client_secret（32 字节），存 sha256 hash，返回明文 secret 仅一次
- `deleteOIDCClient(clientID) bool`
- `listOIDCClients() []OIDCClient` — 不返回 secret

### 修改文件

#### 4. [internal/config/config.go](file:///e:/Bilup/accounts-api/internal/config/config.go)
在 `Load()` 中新增：
```go
OIDC_ISSUER         = MustEnv("OIDC_ISSUER", BASE_URL)          // 默认用 BASE_URL
OIDC_CLIENTS_FILE   = MustEnv("OIDC_CLIENTS_FILE", "./oidc_clients.json")
OIDC_SIGNING_KEY_FILE = MustEnv("OIDC_SIGNING_KEY_FILE", "./oidc_signing_key.pem")
```
注意：`OIDC_ISSUER` 依赖 `BASE_URL`，需在 `BASE_URL` 赋值之后读取。

#### 5. [main.go](file:///e:/Bilup/accounts-api/main.go)
- `initDataFiles()` 的 `files` map 新增：`config.OIDC_CLIENTS_FILE: "[]"`
- `main()` 中 `loadKeys()` 后新增 `loadOIDCClients()` 和 `initOIDCSigningKey()`（加载/生成 RSA 密钥）
- `StartValidatorCleanup()` 后新增 `StartOIDCCleanup()`

#### 6. [routes_v1.go](file:///e:/Bilup/accounts-api/routes_v1.go)
在 `setupV1Routes(r)` 中新增路由组（根级别，不挂 v1CORS，使用 v2CORSMiddleware 以支持凭证）：
```go
// OIDC discovery & JWKS
r.GET("/.well-known/openid-configuration", oidcDiscovery)
r.GET("/.well-known/jwks.json", oidcJWKS)
// OAuth/OIDC endpoints
r.GET("/oauth/authorize", oidcAuthorize)
r.POST("/oauth/consent", oidcConsent)
r.POST("/oauth/login", oidcLogin)
r.POST("/oauth/token", oidcToken)
r.GET("/oauth/userinfo", oidcUserinfo)
// 管理员客户端管理（复用 authenticateAdmin）
admin := r.Group("/admin/oidc")
admin.Use(func(c *gin.Context) { if !authenticateAdmin(c) { c.Abort() }; c.Next() })
admin.POST("/clients", createOIDCClient)
admin.GET("/clients", listOIDCClients)
admin.DELETE("/clients/:id", deleteOIDCClient)
```

---

## 复用的现有函数（关键）

| 现有函数 | 文件位置 | 用途 |
|----------|----------|------|
| `extractAuthKey(c)` | [utils.go:294](file:///e:/Bilup/accounts-api/utils.go#L294) | 检测 session cookie 登录态 |
| `authenticateAnyKey(key)` | [utils.go:350](file:///e:/Bilup/accounts-api/utils.go#L350) | 解析 session key 到用户 |
| `findAccountByLogin(user, pass)` | [handlers_users.go](file:///e:/Bilup/accounts-api/handlers_users.go) | OIDC 登录页密码认证 |
| `v2SetSessionCookie(c, key)` | [v2_support.go](file:///e:/Bilup/accounts-api/v2_support.go) | 设置 session cookie |
| `authenticateAdmin(c)` | [auth.go:97](file:///e:/Bilup/accounts-api/auth.go#L97) | 管理员 API 鉴权 |
| `stripBearer(header)` | [utils.go](file:///e:/Bilup/accounts-api/utils.go) | 解析 Bearer token |
| `avatarURL(username)` | [avatars.go](file:///e:/Bilup/accounts-api/avatars.go) | userinfo picture 字段 |
| `getUserById(id)` | [types.go](file:///e:/Bilup/accounts-api/types.go) | 按 ID 取用户 |
| `user.GetId/GetUsername/GetEmail/GetString("bio")/Get("sys.email_verified")` | [types.go](file:///e:/Bilup/accounts-api/types.go) | id_token 与 userinfo claims |

---

## 新增环境变量（.env）

```env
# OIDC Provider 配置
OIDC_ISSUER=https://api.accounts.bilup.org
OIDC_CLIENTS_FILE=./oidc_clients.json
OIDC_SIGNING_KEY_FILE=./oidc_signing_key.pem
```
`oidc_clients.json` 和 `oidc_signing_key.pem` 已加入 .gitignore（*.json 已忽略；需追加 `oidc_signing_key.pem`）。

---

## 验证方案

### 1. 编译验证
```bash
go build .
```

### 2. 启动服务
```bash
go run .
```
确认日志显示 `Loaded N OIDC clients` 和 OIDC 密钥加载成功。

### 3. 发现端点测试
```bash
curl https://api.accounts.bilup.org/.well-known/openid-configuration
# 应返回含 issuer / authorization_endpoint / token_endpoint / userinfo_endpoint / jwks_uri 的 JSON
curl https://api.accounts.bilup.org/.well-known/jwks.json
# 应返回含 RSA 公钥的 JWKS
```

### 4. 注册客户端（管理员）
```bash
curl -X POST -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"client_name":"Forgejo","redirect_uris":["https://forgejo.example.com/user/oauth2/bilup/callback"]}' \
  https://api.accounts.bilup.org/admin/oidc/clients
# 返回 client_id + client_secret（仅此一次）
```

### 5. Forgejo 端配置
在 Forgejo 管理后台 → Authentication Sources → Add OAuth2 Source：
- Provider: OpenID Connect
- Client ID / Client Secret: 上一步获得
- OpenID Connect Auto Discovery URL: `https://api.accounts.bilup.org/.well-known/openid-configuration`

### 6. 端到端流程
在 Forgejo 登录页点击 OIDC 登录 → 跳转到 `/oauth/authorize` → 若未登录显示登录页 → 登录后显示授权确认页 → Approve → 回跳 Forgejo → 自动创建/登录账号，头像/邮箱/简介同步。

### 7. 单元测试（可选）
对 token 端点 PKCE 校验、id_token 签名验证、userinfo 权限校验编写 `handlers_oidc_test.go`。
