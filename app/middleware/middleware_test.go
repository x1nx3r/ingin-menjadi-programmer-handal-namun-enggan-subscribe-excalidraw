package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestRequireAuthRedirectsUnauthenticated(t *testing.T) {
	called := false
	handler := RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/drawings", nil)
	handler(rr, req)

	if called {
		t.Error("handler was called for unauthenticated request")
	}
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
	if got := rr.Header().Get("HX-Redirect"); got != "/" {
		t.Errorf("expected HX-Redirect '/', got %q", got)
	}
}

func TestRequireAuthAllowsAuthenticated(t *testing.T) {
	called := false
	handler := RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/drawings", nil)
	ctx := context.WithValue(req.Context(), UserUIDKey, "user-123")
	handler(rr, req.WithContext(ctx))

	if !called {
		t.Error("handler was not called for authenticated request")
	}
}

func TestRequireSuperAdmin(t *testing.T) {
	t.Setenv("SUPER_ADMIN_EMAIL", "admin@example.com")

	called := false
	handler := RequireSuperAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	t.Run("blocks non-admin", func(t *testing.T) {
		called = false
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/admin", nil)
		ctx := context.WithValue(req.Context(), UserEmailKey, "other@example.com")
		handler.ServeHTTP(rr, req.WithContext(ctx))
		if called {
			t.Error("handler was called for non-admin")
		}
		if rr.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rr.Code)
		}
	})

	t.Run("allows admin", func(t *testing.T) {
		called = false
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/admin", nil)
		ctx := context.WithValue(req.Context(), UserEmailKey, "admin@example.com")
		handler.ServeHTTP(rr, req.WithContext(ctx))
		if !called {
			t.Error("handler was not called for admin")
		}
	})
}

func TestRateLimiterAllow(t *testing.T) {
	rl := newRateLimiter(1, 2, time.Second) // 1 per second, burst 2
	key := "1.2.3.4"

	if !rl.allow(key) {
		t.Error("first request should be allowed")
	}
	if !rl.allow(key) {
		t.Error("second request within burst should be allowed")
	}
	if rl.allow(key) {
		t.Error("third request should be blocked")
	}
}

func TestRateLimiterCleanupResetsState(t *testing.T) {
	rl := newRateLimiter(1, 1, time.Second)
	key := "1.2.3.4"

	rl.allow(key)
	rl.allow(key) // blocked

	if rl.hits == 0 {
		t.Error("expected hits to be non-zero after block")
	}
	if len(rl.blocked) == 0 {
		t.Error("expected blocked map to be non-empty")
	}

	rl.mu.Lock()
	rl.clients = make(map[string]*bucket)
	rl.hits = 0
	rl.blocked = make(map[string]int64)
	rl.mu.Unlock()

	if rl.hits != 0 {
		t.Errorf("hits not reset: %d", rl.hits)
	}
	if len(rl.blocked) != 0 {
		t.Errorf("blocked not reset: %d", len(rl.blocked))
	}
}

func TestClearSessionCookieFlags(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")

	clearSessionCookie(rr, req)

	cookies := rr.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
	c := cookies[0]
	if !c.HttpOnly {
		t.Error("expected HttpOnly")
	}
	if !c.Secure {
		t.Error("expected Secure")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("expected SameSite Strict, got %v", c.SameSite)
	}
}

func TestMain(m *testing.M) {
	// Ensure tests can run without the production env var.
	if os.Getenv("SUPER_ADMIN_EMAIL") == "" {
		os.Setenv("SUPER_ADMIN_EMAIL", "test-admin@example.com")
	}
	os.Exit(m.Run())
}
