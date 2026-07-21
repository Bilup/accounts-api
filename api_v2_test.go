package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func newV2Engine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v2CORS := v2CORSMiddleware()
	r.Use(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/v2") {
			v2CORS(c)
			return
		}
		c.Next()
	})
	setupV2Routes(r)
	return r
}

func TestV2RegisterMountsCleanly(t *testing.T) {
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("setupV2Routes panicked: %v", rec)
		}
	}()
	newV2Engine(t)
}

func TestV1AndAvatarRoutesMountCleanly(t *testing.T) {
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("route setup panicked: %v", rec)
		}
	}()
	gin.SetMode(gin.TestMode)
	setupV1Routes(gin.New())
	setupAvatarRoutes(gin.New())
}

func TestV2PreflightIsCredentialed(t *testing.T) {
	r := newV2Engine(t)
	req := httptest.NewRequest(http.MethodOptions, "/v2/posts", nil)
	req.Header.Set("Origin", "https://app.example.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("Allow-Origin = %q, want reflected origin", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("Allow-Credentials = %q, want true", got)
	}
}

func TestV2SessionRequiresAuth(t *testing.T) {
	r := newV2Engine(t)
	req := httptest.NewRequest(http.MethodGet, "/v2/sessions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauth /v2/sessions = %d, want 401", w.Code)
	}
}

func TestV2SessionRejectsBadToken(t *testing.T) {
	r := newV2Engine(t)
	req := httptest.NewRequest(http.MethodPost, "/v2/sessions", strings.NewReader(`{"token":"definitely-not-valid"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bad-token login = %d, want 401", w.Code)
	}
}

func TestV2PublicEndpointReachesHandler(t *testing.T) {
	r := newV2Engine(t)
	req := httptest.NewRequest(http.MethodGet, "/v2/limits", nil)
	req.Header.Set("Origin", "https://app.example.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v2/limits = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("Allow-Origin = %q, want reflected origin", got)
	}
}

func TestV2ProtectedEndpointRejectsAnonymous(t *testing.T) {
	r := newV2Engine(t)
	req := httptest.NewRequest(http.MethodPost, "/v2/posts", strings.NewReader(`{"content":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("anonymous POST /v2/posts = %d, want 403", w.Code)
	}
}
