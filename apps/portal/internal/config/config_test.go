package config_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"strings"
	"testing"

	"github.com/kittypaw-app/kittyportal/internal/config"
)

// generatePEM returns a fresh PEM-encoded RSA private key of the given
// bit size. Inline so tests don't depend on the static testdata/jwks/
// fixture (built in T5) — keeps T3 self-contained.
func generatePEM(t *testing.T, bits int) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

// loadWithEnv injects required env vars then calls Load(). Bare-minimum
// fields are always set so test failures isolate to the JWT key path.
func loadWithEnv(t *testing.T, envs map[string]string) (*config.Config, error) {
	t.Helper()
	base := map[string]string{
		"DATABASE_URL": "postgres://localhost/x",
	}
	for k, v := range envs {
		base[k] = v
	}
	for k, v := range base {
		t.Setenv(k, v)
	}
	return config.Load()
}

func TestConfig_LoadJWTKey_Valid(t *testing.T) {
	pemStr := generatePEM(t, 2048)
	b64 := base64.StdEncoding.EncodeToString([]byte(pemStr))

	cfg, err := loadWithEnv(t, map[string]string{"JWT_PRIVATE_KEY_PEM_B64": b64})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.JWTPrivateKey == nil {
		t.Fatal("JWTPrivateKey is nil")
	}
	if cfg.JWTKID == "" {
		t.Fatal("JWTKID is empty")
	}
	if cfg.JWTPrivateKey.N.BitLen() < 2048 {
		t.Fatalf("loaded key bits = %d, want ≥2048", cfg.JWTPrivateKey.N.BitLen())
	}
}

func TestConfig_LoadGoogleOAuthEndpointOverrides(t *testing.T) {
	pemStr := generatePEM(t, 2048)
	b64 := base64.StdEncoding.EncodeToString([]byte(pemStr))

	cfg, err := loadWithEnv(t, map[string]string{
		"JWT_PRIVATE_KEY_PEM_B64": b64,
		"GOOGLE_AUTH_URL":         "http://oauth.local/auth",
		"GOOGLE_TOKEN_URL":        "http://oauth.local/token",
		"GOOGLE_USERINFO_URL":     "http://oauth.local/userinfo",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.GoogleAuthURL != "http://oauth.local/auth" {
		t.Fatalf("GoogleAuthURL = %q", cfg.GoogleAuthURL)
	}
	if cfg.GoogleTokenURL != "http://oauth.local/token" {
		t.Fatalf("GoogleTokenURL = %q", cfg.GoogleTokenURL)
	}
	if cfg.GoogleUserInfoURL != "http://oauth.local/userinfo" {
		t.Fatalf("GoogleUserInfoURL = %q", cfg.GoogleUserInfoURL)
	}
}

func TestConfig_LoadConnectSettings(t *testing.T) {
	pemStr := generatePEM(t, 2048)
	b64 := base64.StdEncoding.EncodeToString([]byte(pemStr))

	cfg, err := loadWithEnv(t, map[string]string{
		"JWT_PRIVATE_KEY_PEM_B64":      b64,
		"BASE_URL":                     "https://portal.kittypaw.app",
		"CONNECT_BASE_URL":             "https://connect.kittypaw.app/",
		"HOME_BASE_URL":                "https://home.kittypaw.app/",
		"CONNECT_GOOGLE_CLIENT_ID":     "connect-client-id",
		"CONNECT_GOOGLE_CLIENT_SECRET": "connect-secret",
		"CONNECT_GOOGLE_AUTH_URL":      "http://connect-oauth.local/auth",
		"CONNECT_GOOGLE_TOKEN_URL":     "http://connect-oauth.local/token",
		"CONNECT_GOOGLE_USERINFO_URL":  "http://connect-oauth.local/userinfo",
		"CONNECT_X_CLIENT_ID":          "x-client-id",
		"CONNECT_X_CLIENT_SECRET":      "x-secret",
		"CONNECT_X_AUTH_URL":           "http://x-oauth.local/auth",
		"CONNECT_X_TOKEN_URL":          "http://x-oauth.local/token",
		"CONNECT_X_USERINFO_URL":       "http://x-oauth.local/users/me",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ConnectBaseURL != "https://connect.kittypaw.app" {
		t.Fatalf("ConnectBaseURL = %q", cfg.ConnectBaseURL)
	}
	if cfg.HomeBaseURL != "https://home.kittypaw.app" {
		t.Fatalf("HomeBaseURL = %q", cfg.HomeBaseURL)
	}
	if cfg.ConnectGoogleClientID != "connect-client-id" {
		t.Fatalf("ConnectGoogleClientID = %q", cfg.ConnectGoogleClientID)
	}
	if cfg.ConnectGoogleClientSecret != "connect-secret" {
		t.Fatalf("ConnectGoogleClientSecret = %q", cfg.ConnectGoogleClientSecret)
	}
	if cfg.ConnectGoogleAuthURL != "http://connect-oauth.local/auth" {
		t.Fatalf("ConnectGoogleAuthURL = %q", cfg.ConnectGoogleAuthURL)
	}
	if cfg.ConnectGoogleTokenURL != "http://connect-oauth.local/token" {
		t.Fatalf("ConnectGoogleTokenURL = %q", cfg.ConnectGoogleTokenURL)
	}
	if cfg.ConnectGoogleUserInfoURL != "http://connect-oauth.local/userinfo" {
		t.Fatalf("ConnectGoogleUserInfoURL = %q", cfg.ConnectGoogleUserInfoURL)
	}
	if cfg.ConnectXClientID != "x-client-id" {
		t.Fatalf("ConnectXClientID = %q", cfg.ConnectXClientID)
	}
	if cfg.ConnectXClientSecret != "x-secret" {
		t.Fatalf("ConnectXClientSecret = %q", cfg.ConnectXClientSecret)
	}
	if cfg.ConnectXAuthURL != "http://x-oauth.local/auth" {
		t.Fatalf("ConnectXAuthURL = %q", cfg.ConnectXAuthURL)
	}
	if cfg.ConnectXTokenURL != "http://x-oauth.local/token" {
		t.Fatalf("ConnectXTokenURL = %q", cfg.ConnectXTokenURL)
	}
	if cfg.ConnectXUserInfoURL != "http://x-oauth.local/users/me" {
		t.Fatalf("ConnectXUserInfoURL = %q", cfg.ConnectXUserInfoURL)
	}
}

func TestConfig_LoadForTestConnectBaseURL(t *testing.T) {
	cfg := config.LoadForTest()
	if cfg.ConnectBaseURL == "" {
		t.Fatal("ConnectBaseURL should default in tests")
	}
	if cfg.HomeBaseURL == "" {
		t.Fatal("HomeBaseURL should default in tests")
	}
	if cfg.ConnectGoogleClientID == "" {
		t.Fatal("ConnectGoogleClientID should default in tests")
	}
	if cfg.ConnectGoogleClientSecret == "" {
		t.Fatal("ConnectGoogleClientSecret should default in tests")
	}
	if cfg.ConnectXClientID == "" {
		t.Fatal("ConnectXClientID should default in tests")
	}
	if cfg.ConnectXClientSecret == "" {
		t.Fatal("ConnectXClientSecret should default in tests")
	}
}

func TestConfig_LoadUnixSocket(t *testing.T) {
	pemStr := generatePEM(t, 2048)
	b64 := base64.StdEncoding.EncodeToString([]byte(pemStr))

	cfg, err := loadWithEnv(t, map[string]string{
		"JWT_PRIVATE_KEY_PEM_B64": b64,
		"UNIX_SOCKET":             "/tmp/kittyportal.sock",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.UnixSocket != "/tmp/kittyportal.sock" {
		t.Fatalf("UnixSocket = %q, want /tmp/kittyportal.sock", cfg.UnixSocket)
	}
}

// TestConfig_LoadJWTKey_BadBase64 ensures we fail-fast at startup
// rather than letting an undecodable env survive into request-time.
func TestConfig_LoadJWTKey_BadBase64(t *testing.T) {
	_, err := loadWithEnv(t, map[string]string{"JWT_PRIVATE_KEY_PEM_B64": "!!!not-base64"})
	if err == nil {
		t.Fatal("expected error for malformed base64")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "base64") &&
		!strings.Contains(strings.ToLower(err.Error()), "decode") {
		t.Fatalf("error doesn't mention base64/decode: %v", err)
	}
}

// TestConfig_LoadJWTKey_BadPEM covers the case where base64 decodes
// successfully but the bytes aren't valid PEM (typo, truncated key, etc).
func TestConfig_LoadJWTKey_BadPEM(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte("not a pem"))
	_, err := loadWithEnv(t, map[string]string{"JWT_PRIVATE_KEY_PEM_B64": b64})
	if err == nil {
		t.Fatal("expected error for non-PEM bytes")
	}
}

// TestConfig_LoadJWTKey_TooSmall enforces the 2048-bit floor — RSA
// keys below this are not considered safe for production signing today.
func TestConfig_LoadJWTKey_TooSmall(t *testing.T) {
	pemStr := generatePEM(t, 1024)
	b64 := base64.StdEncoding.EncodeToString([]byte(pemStr))

	_, err := loadWithEnv(t, map[string]string{"JWT_PRIVATE_KEY_PEM_B64": b64})
	if err == nil {
		t.Fatal("expected error for 1024-bit key")
	}
	if !strings.Contains(err.Error(), "2048") {
		t.Fatalf("error doesn't mention 2048-bit floor: %v", err)
	}
}
