package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"
)

func v2CORSMiddleware() gin.HandlerFunc {
	const allowHeaders = "Content-Type, Authorization, X-Requested-With"
	const allowMethods = "GET, POST, PUT, PATCH, DELETE, OPTIONS"
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		h := c.Writer.Header()
		h.Add("Vary", "Origin")
		if origin != "" {
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
		} else {
			h.Set("Access-Control-Allow-Origin", "*")
		}
		h.Set("Access-Control-Allow-Methods", allowMethods)
		h.Set("Access-Control-Allow-Headers", allowHeaders)
		h.Set("Access-Control-Max-Age", "86400")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func v2BodyToQuery() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.ContentType() != "application/json" || c.Request.Body == nil {
			c.Next()
			return
		}
		raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 8<<20))
		c.Request.Body.Close()
		c.Request.Body = io.NopCloser(bytes.NewReader(raw))
		if err != nil {
			c.Next()
			return
		}
		if len(bytes.TrimSpace(raw)) > 0 {
			var obj map[string]any
			if json.Unmarshal(raw, &obj) == nil {
				q := c.Request.URL.Query()
				for k, v := range obj {
					if q.Has(k) {
						continue
					}
					if s, ok := v2Scalar(v); ok {
						q.Set(k, s)
					}
				}
				c.Request.URL.RawQuery = q.Encode()
			}
		}
		c.Next()
	}
}

func v2Scalar(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case bool:
		return strconv.FormatBool(t), true
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10), true
		}
		return strconv.FormatFloat(t, 'f', -1, 64), true
	case json.Number:
		return t.String(), true
	default:
		return "", false
	}
}

func v2ParamsToQuery() gin.HandlerFunc {
	return func(c *gin.Context) {
		if len(c.Params) == 0 {
			c.Next()
			return
		}
		q := c.Request.URL.Query()
		for _, p := range c.Params {
			q.Set(p.Key, p.Value)
		}
		c.Request.URL.RawQuery = q.Encode()
		c.Next()
	}
}

func v2SetQuery(values map[string]string) gin.HandlerFunc {
	return func(c *gin.Context) {
		q := c.Request.URL.Query()
		for k, v := range values {
			q.Set(k, v)
		}
		c.Request.URL.RawQuery = q.Encode()
		c.Next()
	}
}

func v2SessionKey(c *gin.Context) string {
	if h := c.GetHeader("Authorization"); h != "" {
		return stripBearer(h)
	}
	if t := c.Query("token"); t != "" {
		return t
	}
	if a := c.Query("auth"); a != "" {
		return a
	}
	if ck, err := c.Cookie(sessionCookieName); err == nil {
		return ck
	}
	return ""
}

func v2SetSessionCookie(c *gin.Context, value string) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		Domain:   os.Getenv("V2_COOKIE_DOMAIN"),
		MaxAge:   60 * 60 * 24 * 30,
		Secure:   os.Getenv("V2_COOKIE_INSECURE") == "",
		HttpOnly: true,
		SameSite: http.SameSiteNoneMode,
	})
}

func v2ClearSessionCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		Domain:   os.Getenv("V2_COOKIE_DOMAIN"),
		MaxAge:   -1,
		Secure:   os.Getenv("V2_COOKIE_INSECURE") == "",
		HttpOnly: true,
		SameSite: http.SameSiteNoneMode,
	})
}

func v2UserSummary(user *User) gin.H {
	return gin.H{
		"id":       user.GetId(),
		"username": user.GetUsername(),
	}
}

func createSessionV2(c *gin.Context) {
	key := v2SessionKey(c)
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "a token is required to start a session"})
		return
	}
	user, _ := authenticateAnyKey(key)
	if user == nil || user.IsBanned() {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return
	}
	v2SetSessionCookie(c, key)
	c.JSON(http.StatusCreated, gin.H{"user": v2UserSummary(user)})
}

func currentSessionV2(c *gin.Context) {
	key := v2SessionKey(c)
	if key == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	user, _ := authenticateAnyKey(key)
	if user == nil || user.IsBanned() {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": v2UserSummary(user)})
}

func deleteSessionV2(c *gin.Context) {
	v2ClearSessionCookie(c)
	c.Status(http.StatusNoContent)
}

func v2Like(rating string) gin.HandlerFunc {
	return v2SetQuery(map[string]string{"rating": rating})
}
