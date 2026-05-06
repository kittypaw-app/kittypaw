package config

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/kittypaw-app/kittyportal/internal/auth"
)

// MinJWTKeyBits is the lower bound for RSA signing keys. Below this is
// not considered safe for production token signing in the present era.
const MinJWTKeyBits = 2048

type Config struct {
	Port                      string
	UnixSocket                string
	DatabaseURL               string
	JWTPrivateKey             *rsa.PrivateKey // RS256 signing key (Plan 21 PR-B cutover; replaces HS256 secret)
	JWTKID                    string          // RFC 7638 thumbprint of JWTPrivateKey's public half
	GoogleClientID            string
	GoogleClientSecret        string
	GoogleAuthURL             string
	GoogleTokenURL            string
	GoogleUserInfoURL         string
	ConnectBaseURL            string
	ConnectGoogleClientID     string
	ConnectGoogleClientSecret string
	ConnectGoogleAuthURL      string
	ConnectGoogleTokenURL     string
	ConnectGoogleUserInfoURL  string
	ConnectXClientID          string
	ConnectXClientSecret      string
	ConnectXAuthURL           string
	ConnectXTokenURL          string
	ConnectXUserInfoURL       string
	SpaceBaseURL              string
	GitHubClientID            string
	GitHubClientSecret        string
	BaseURL                   string
	AllowedOrigins            []string
	KakaoRelayURL             string
	ChatRelayURL              string
	APIBaseURL                string
	SkillsRegistryURL         string
	// WebRedirectURIAllowlist — exact-match list of redirect_uri values
	// accepted by the web OAuth flow (Plan 25). CSV from env. Empty list
	// disables the web flow entirely (HandleWebGoogleLogin will reject
	// every request) — chosen over a permissive default to fail-safe in
	// dev/CI without explicit allowlist config.
	WebRedirectURIAllowlist []string
}

func Load() (*Config, error) {
	c := &Config{
		Port:                      env("PORT", "8080"),
		UnixSocket:                os.Getenv("UNIX_SOCKET"),
		DatabaseURL:               os.Getenv("DATABASE_URL"),
		GoogleClientID:            os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret:        os.Getenv("GOOGLE_CLIENT_SECRET"),
		GoogleAuthURL:             os.Getenv("GOOGLE_AUTH_URL"),
		GoogleTokenURL:            os.Getenv("GOOGLE_TOKEN_URL"),
		GoogleUserInfoURL:         os.Getenv("GOOGLE_USERINFO_URL"),
		ConnectBaseURL:            strings.TrimRight(env("CONNECT_BASE_URL", ""), "/"),
		ConnectGoogleClientID:     os.Getenv("CONNECT_GOOGLE_CLIENT_ID"),
		ConnectGoogleClientSecret: os.Getenv("CONNECT_GOOGLE_CLIENT_SECRET"),
		ConnectGoogleAuthURL:      os.Getenv("CONNECT_GOOGLE_AUTH_URL"),
		ConnectGoogleTokenURL:     os.Getenv("CONNECT_GOOGLE_TOKEN_URL"),
		ConnectGoogleUserInfoURL:  os.Getenv("CONNECT_GOOGLE_USERINFO_URL"),
		ConnectXClientID:          os.Getenv("CONNECT_X_CLIENT_ID"),
		ConnectXClientSecret:      os.Getenv("CONNECT_X_CLIENT_SECRET"),
		ConnectXAuthURL:           os.Getenv("CONNECT_X_AUTH_URL"),
		ConnectXTokenURL:          os.Getenv("CONNECT_X_TOKEN_URL"),
		ConnectXUserInfoURL:       os.Getenv("CONNECT_X_USERINFO_URL"),
		SpaceBaseURL:              strings.TrimRight(env("SPACE_BASE_URL", ""), "/"),
		GitHubClientID:            os.Getenv("GITHUB_CLIENT_ID"),
		GitHubClientSecret:        os.Getenv("GITHUB_CLIENT_SECRET"),
		BaseURL:                   env("BASE_URL", "http://localhost:8080"),
		KakaoRelayURL:             os.Getenv("KAKAO_RELAY_URL"),
		ChatRelayURL:              os.Getenv("CHAT_RELAY_URL"),
		APIBaseURL:                os.Getenv("API_BASE_URL"),
		SkillsRegistryURL:         env("SKILLS_REGISTRY_URL", "https://github.com/kittypaw-app/skills"),
	}

	required := map[string]string{
		"DATABASE_URL": c.DatabaseURL,
	}
	for name, val := range required {
		if val == "" {
			return nil, fmt.Errorf("%s is required", name)
		}
	}

	// Plan 21 PR-B: RS256 cutover complete — HS256 JWT_SECRET removed.
	// Token signing/verification flows entirely through JWT_PRIVATE_KEY_PEM_B64
	// (PR-A) + the JWKS endpoint. The RSA key is required at startup so an
	// undecodable env can't survive into request time.
	//
	// Encoding contract: standard base64 (RFC 4648 §4 — `+/` alphabet
	// with padding). NOT URL-safe (`-_`). Mismatch at deploy time
	// surfaces here as "illegal base64 data" — see deploy/env docs.
	keyB64 := os.Getenv("JWT_PRIVATE_KEY_PEM_B64")
	if keyB64 == "" {
		return nil, fmt.Errorf("JWT_PRIVATE_KEY_PEM_B64 is required")
	}
	pemBytes, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return nil, fmt.Errorf("decode JWT_PRIVATE_KEY_PEM_B64 (base64): %w", err)
	}
	priv, kid, err := auth.LoadPrivateKeyPEM(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("load RSA private key: %w", err)
	}
	if priv.N.BitLen() < MinJWTKeyBits {
		return nil, fmt.Errorf("RSA key must be at least %d bits, got %d", MinJWTKeyBits, priv.N.BitLen())
	}
	c.JWTPrivateKey = priv
	c.JWTKID = kid

	if origins := os.Getenv("CORS_ORIGINS"); origins != "" {
		c.AllowedOrigins = splitCSV(origins)
	} else {
		c.AllowedOrigins = []string{c.BaseURL}
	}
	if c.APIBaseURL == "" {
		c.APIBaseURL = c.BaseURL
	}

	if allow := os.Getenv("WEB_REDIRECT_URI_ALLOWLIST"); allow != "" {
		// Trim whitespace per entry — operators sometimes wrap CSVs across
		// lines in .env and the parser must not silently fail to match a
		// "https://chat.kittypaw.app/auth/callback " entry.
		raw := strings.Split(allow, ",")
		c.WebRedirectURIAllowlist = make([]string, 0, len(raw))
		for _, e := range raw {
			if t := strings.TrimSpace(e); t != "" {
				c.WebRedirectURIAllowlist = append(c.WebRedirectURIAllowlist, t)
			}
		}
	}

	return c, nil
}

// LoadForTest returns a config suitable for testing (no required fields).
// The shared fixture key is cached via sync.Once below so handlers that
// publish/use JWKS work without each test wiring its own key. Cost of
// 2048-bit generation is ~50ms; reusing across the suite keeps tests
// snappy and lets thumbprint pin the kid in assertions.
//
// The cache lives in this production file rather than a *_test.go
// because LoadForTest itself is a public API consumed by tests in
// other packages — Go does not link _test.go symbols across packages.
func LoadForTest() *Config {
	priv, kid := loadForTestKey()
	return &Config{
		Port:                      env("PORT", "8080"),
		BaseURL:                   env("BASE_URL", "http://localhost:8080"),
		ConnectBaseURL:            "https://connect.kittypaw.app",
		ConnectGoogleClientID:     "connect-client-id",
		ConnectGoogleClientSecret: "connect-secret",
		ConnectXClientID:          "x-connect-client-id",
		ConnectXClientSecret:      "x-connect-secret",
		SpaceBaseURL:              "https://space.kittypaw.app",
		APIBaseURL:                env("BASE_URL", "http://localhost:8080"),
		AllowedOrigins:            []string{"http://localhost:8080"},
		JWTPrivateKey:             priv,
		JWTKID:                    kid,
	}
}

var (
	loadForTestKeyOnce sync.Once
	loadForTestKeyPriv *rsa.PrivateKey
	loadForTestKeyKID  string
)

func loadForTestKey() (*rsa.PrivateKey, string) {
	loadForTestKeyOnce.Do(func() {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(fmt.Sprintf("LoadForTest: rsa.GenerateKey: %v", err))
		}
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			panic(fmt.Sprintf("LoadForTest: marshal PKCS8: %v", err))
		}
		pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
		_, kid, err := auth.LoadPrivateKeyPEM(pemBytes)
		if err != nil {
			panic(fmt.Sprintf("LoadForTest: load PEM: %v", err))
		}
		loadForTestKeyPriv = key
		loadForTestKeyKID = kid
	})
	return loadForTestKeyPriv, loadForTestKeyKID
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
