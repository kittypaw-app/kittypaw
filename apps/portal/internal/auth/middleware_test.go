package auth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kittypaw-app/kittyportal/internal/auth"
	"github.com/kittypaw-app/kittyportal/internal/auth/testfixture"
	"github.com/kittypaw-app/kittyportal/internal/config"
)

func TestMiddlewareValidJWT(t *testing.T) {
	cfg := config.LoadForTest()
	provider := auth.NewSingleKeyProvider(&cfg.JWTPrivateKey.PublicKey, cfg.JWTKID)

	userStore := newMockUserStore()
	_, _ = userStore.CreateOrUpdate(t.Context(), "google", "123", "t@t.com", "Test", "")

	token, _ := auth.SignForAudiences("user-google-123", auth.DefaultAPIClientAudiences, auth.DefaultAPIClientScopes, cfg.JWTPrivateKey, cfg.JWTKID, 15*time.Minute)

	handler := auth.Middleware(provider, auth.AudienceAPI, userStore)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		if user == nil {
			t.Fatal("expected user in context")
		}
		if user.ID != "user-google-123" {
			t.Fatalf("expected user-google-123, got %q", user.ID)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestMiddlewareAnonymous(t *testing.T) {
	cfg := config.LoadForTest()
	provider := auth.NewSingleKeyProvider(&cfg.JWTPrivateKey.PublicKey, cfg.JWTKID)

	userStore := newMockUserStore()

	handler := auth.Middleware(provider, auth.AudienceAPI, userStore)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		if user != nil {
			t.Fatal("expected nil user for anonymous")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestMiddlewareInvalidJWT(t *testing.T) {
	cfg := config.LoadForTest()
	provider := auth.NewSingleKeyProvider(&cfg.JWTPrivateKey.PublicKey, cfg.JWTKID)

	userStore := newMockUserStore()

	handler := auth.Middleware(provider, auth.AudienceAPI, userStore)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestMiddlewareMalformedHeader(t *testing.T) {
	cfg := config.LoadForTest()
	provider := auth.NewSingleKeyProvider(&cfg.JWTPrivateKey.PublicKey, cfg.JWTKID)

	userStore := newMockUserStore()

	handler := auth.Middleware(provider, auth.AudienceAPI, userStore)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "NotBearer something")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandleMeAuthenticated(t *testing.T) {
	cfg := config.LoadForTest()
	provider := auth.NewSingleKeyProvider(&cfg.JWTPrivateKey.PublicKey, cfg.JWTKID)

	userStore := newMockUserStore()
	_, _ = userStore.CreateOrUpdate(t.Context(), "google", "123", "t@t.com", "Test", "")

	token, _ := auth.SignForAudiences("user-google-123", auth.DefaultAPIClientAudiences, auth.DefaultAPIClientScopes, cfg.JWTPrivateKey, cfg.JWTKID, 15*time.Minute)

	handler := auth.Middleware(provider, auth.AudienceAPI, userStore)(http.HandlerFunc(auth.HandleMe))

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var user map[string]any
	if err := json.NewDecoder(w.Body).Decode(&user); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if user["email"] != "t@t.com" {
		t.Fatalf("expected email t@t.com, got %v", user["email"])
	}
}

func TestHandleMeAnonymous(t *testing.T) {
	cfg := config.LoadForTest()
	provider := auth.NewSingleKeyProvider(&cfg.JWTPrivateKey.PublicKey, cfg.JWTKID)

	userStore := newMockUserStore()

	handler := auth.Middleware(provider, auth.AudienceAPI, userStore)(http.HandlerFunc(auth.HandleMe))

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// TestMiddleware_RejectsCrossAudienceLeak pins the cross-audience leak
// guard. A device JWT (aud=AudienceChat+AudienceHome, scope=daemon:connect) MUST NOT
// authenticate against the user middleware (audience pinned to
// AudienceAPI). Per spec D8: each resource server enforces its own
// audience only — this catches typo regressions in main.go that wire the
// user MW with the wrong audience.
func TestMiddleware_RejectsCrossAudienceLeak(t *testing.T) {
	cfg := config.LoadForTest()
	provider := auth.NewSingleKeyProvider(&cfg.JWTPrivateKey.PublicKey, cfg.JWTKID)

	userStore := newMockUserStore()
	// Seed a user that the device JWT claims to belong to — any leak
	// would reach FindByID and we'd see a 200; the audience check must
	// fire BEFORE that lookup.
	_, _ = userStore.CreateOrUpdate(t.Context(), "google", "123", "t@t.com", "Test", "")

	now := time.Now()
	deviceToken, err := testfixture.IssueDeviceJWT(cfg.JWTPrivateKey, cfg.JWTKID, testfixture.DeviceClaims{
		DeviceID: "dev-1",
		UserID:   "user-google-123",
		Audience: []string{auth.AudienceChat, auth.AudienceHome},
		Scope:    []string{auth.ScopeDaemonConnect},
		Version:  2,
		IssuedAt: now,
		Expires:  now.Add(15 * time.Minute),
	})
	if err != nil {
		t.Fatalf("IssueDeviceJWT: %v", err)
	}

	handler := auth.Middleware(provider, auth.AudienceAPI, userStore)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+deviceToken)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 (device JWT must NOT auth against user MW pinned to AudienceAPI), got %d", w.Code)
	}
}
