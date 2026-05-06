package identity

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestJWKSVerifierVerifiesDeviceTokenV2(t *testing.T) {
	key := newTestRSAKey(t)
	kid := "test-key-1"
	jwks := newTestJWKSet(t, kid, &key.PublicKey)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(jwks)
	}))
	defer srv.Close()

	verifier, err := NewJWTCredentialVerifier(JWTVerifierConfig{
		JWKSURL:  srv.URL,
		CacheTTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("NewJWTCredentialVerifier() error = %v", err)
	}

	token := signTestRS256DeviceJWT(t, key, kid, map[string]any{})
	got, err := verifier.VerifyDevice(context.Background(), token)
	if err != nil {
		t.Fatalf("VerifyDevice() error = %v", err)
	}

	if got.Subject != "device:dev_1" || got.UserID != "user_1" || got.DeviceID != "dev_1" {
		t.Fatalf("device claims = %+v, want user_1/dev_1", got)
	}
	if len(got.LocalAccountIDs) != 0 {
		t.Fatalf("LocalAccountIDs = %+v, want none from JWT", got.LocalAccountIDs)
	}
	assertScopes(t, got.Scopes, []Scope{ScopeDaemonConnect})
}

func TestJWKSVerifierRefetchesOnceForUnknownKID(t *testing.T) {
	oldKey := newTestRSAKey(t)
	newKey := newTestRSAKey(t)
	oldJWKS := newTestJWKSet(t, "old-key", &oldKey.PublicKey)
	newJWKS := newTestJWKSet(t, "new-key", &newKey.PublicKey)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			_, _ = w.Write(oldJWKS)
			return
		}
		_, _ = w.Write(newJWKS)
	}))
	defer srv.Close()

	verifier, err := NewJWTCredentialVerifier(JWTVerifierConfig{
		JWKSURL:  srv.URL,
		CacheTTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("NewJWTCredentialVerifier() error = %v", err)
	}

	token := signTestRS256DeviceJWT(t, newKey, "new-key", map[string]any{})
	if _, err := verifier.VerifyDevice(context.Background(), token); err != nil {
		t.Fatalf("VerifyDevice() error = %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("JWKS fetches = %d, want 2", got)
	}
}

func TestJWKSVerifierBacksOffRepeatedUnknownKIDRefetches(t *testing.T) {
	oldKey := newTestRSAKey(t)
	unknownKey := newTestRSAKey(t)
	jwks := newTestJWKSet(t, "old-key", &oldKey.PublicKey)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(jwks)
	}))
	defer srv.Close()

	verifier, err := NewJWTCredentialVerifier(JWTVerifierConfig{
		JWKSURL:  srv.URL,
		CacheTTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("NewJWTCredentialVerifier() error = %v", err)
	}

	token := signTestRS256DeviceJWT(t, unknownKey, "unknown-key", map[string]any{})
	for i := 0; i < 2; i++ {
		if _, err := verifier.VerifyDevice(context.Background(), token); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("VerifyDevice() attempt %d error = %v, want ErrUnauthorized", i+1, err)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("JWKS fetches = %d, want 2", got)
	}
}

func TestJWKSVerifierDoesNotBlockCachedKeyWhileRefetching(t *testing.T) {
	knownKey := newTestRSAKey(t)
	unknownKey := newTestRSAKey(t)
	started := make(chan struct{})
	release := make(chan struct{})
	transport := blockingRoundTripper{
		started: started,
		release: release,
		body:    newTestJWKSet(t, "unknown-key", &unknownKey.PublicKey),
	}
	verifier, err := NewJWTCredentialVerifier(JWTVerifierConfig{
		JWKSURL:      "http://example.test/.well-known/jwks.json",
		HTTPClient:   &http.Client{Transport: transport},
		CacheTTL:     time.Minute,
		FetchTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewJWTCredentialVerifier() error = %v", err)
	}
	verifier.keys["known-key"] = &knownKey.PublicKey
	verifier.cacheExpiresAt = time.Now().Add(time.Minute)

	errCh := make(chan error, 1)
	go func() {
		_, err := verifier.VerifyDevice(context.Background(), signTestRS256DeviceJWT(t, unknownKey, "unknown-key", map[string]any{}))
		errCh <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for JWKS refetch to start")
	}

	done := make(chan error, 1)
	go func() {
		_, err := verifier.VerifyDevice(context.Background(), signTestRS256DeviceJWT(t, knownKey, "known-key", map[string]any{}))
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cached VerifyDevice() error = %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("cached VerifyDevice() blocked behind JWKS refetch")
	}

	close(release)
	if err := <-errCh; err != nil {
		t.Fatalf("refetch VerifyDevice() error = %v", err)
	}
}

func TestJWKSVerifierTimesOutFetch(t *testing.T) {
	key := newTestRSAKey(t)
	started := make(chan struct{}, 1)
	verifier, err := NewJWTCredentialVerifier(JWTVerifierConfig{
		JWKSURL:      "http://example.test/.well-known/jwks.json",
		HTTPClient:   &http.Client{Transport: blockingRoundTripper{started: started, release: make(chan struct{})}},
		FetchTimeout: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewJWTCredentialVerifier() error = %v", err)
	}

	start := time.Now()
	_, err = verifier.VerifyDevice(context.Background(), signTestRS256DeviceJWT(t, key, "test-key", map[string]any{}))
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("VerifyDevice() error = %v, want ErrUnauthorized", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("VerifyDevice() elapsed = %v, want bounded timeout", elapsed)
	}
	select {
	case <-started:
	default:
		t.Fatal("JWKS fetch did not start")
	}
}

func TestJWKSVerifierKeepsPreviousCacheWhenRefreshHasNoUsableKeys(t *testing.T) {
	key := newTestRSAKey(t)
	validJWKS := newTestJWKSet(t, "known-key", &key.PublicKey)
	emptyJWKS := []byte(`{"keys":[]}`)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			_, _ = w.Write(validJWKS)
			return
		}
		_, _ = w.Write(emptyJWKS)
	}))
	defer srv.Close()

	verifier, err := NewJWTCredentialVerifier(JWTVerifierConfig{
		JWKSURL:  srv.URL,
		CacheTTL: time.Nanosecond,
	})
	if err != nil {
		t.Fatalf("NewJWTCredentialVerifier() error = %v", err)
	}
	token := signTestRS256DeviceJWT(t, key, "known-key", map[string]any{})
	if _, err := verifier.VerifyDevice(context.Background(), token); err != nil {
		t.Fatalf("first VerifyDevice() error = %v", err)
	}
	time.Sleep(time.Millisecond)

	if _, err := verifier.VerifyDevice(context.Background(), token); err != nil {
		t.Fatalf("VerifyDevice() after empty JWKS refresh error = %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("JWKS fetches = %d, want 2", got)
	}
}

func TestJWKSVerifierRejectsInvalidDeviceTokens(t *testing.T) {
	key := newTestRSAKey(t)
	kid := "test-key-1"
	jwks := newTestJWKSet(t, kid, &key.PublicKey)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(jwks)
	}))
	defer srv.Close()

	tests := []struct {
		name      string
		overrides map[string]any
		token     func() string
	}{
		{
			name:      "bad audience",
			overrides: map[string]any{"aud": []string{AudienceKittyAPI}},
		},
		{
			name:      "bad scope",
			overrides: map[string]any{"scope": []string{string(ScopeModelsRead)}},
		},
		{
			name:      "expired",
			overrides: map[string]any{"exp": time.Now().Add(-2 * time.Minute).Unix()},
		},
		{
			name:      "malformed subject",
			overrides: map[string]any{"sub": "dev_1"},
		},
		{
			name: "hs256 algorithm",
			token: func() string {
				return signTestJWT(t, map[string]any{
					"sub":     "device:dev_1",
					"iss":     IssuerKittyAPI,
					"aud":     []string{AudienceKittySpace},
					"scope":   []string{string(ScopeDaemonConnect)},
					"v":       CredentialVersion2,
					"user_id": "user_1",
					"iat":     time.Now().Unix(),
					"exp":     time.Now().Add(time.Hour).Unix(),
				})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verifier, err := NewJWTCredentialVerifier(JWTVerifierConfig{
				JWKSURL:  srv.URL,
				CacheTTL: time.Minute,
			})
			if err != nil {
				t.Fatalf("NewJWTCredentialVerifier() error = %v", err)
			}
			token := signTestRS256DeviceJWT(t, key, kid, tt.overrides)
			if tt.token != nil {
				token = tt.token()
			}

			_, err = verifier.VerifyDevice(context.Background(), token)
			if !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("VerifyDevice() error = %v, want ErrUnauthorized", err)
			}
		})
	}
}

func newTestRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return key
}

func newTestJWKSet(t *testing.T, kid string, key *rsa.PublicKey) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"keys": []map[string]string{{
			"kty": "RSA",
			"use": "sig",
			"alg": "RS256",
			"kid": kid,
			"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}},
	})
	if err != nil {
		t.Fatalf("marshal JWKS: %v", err)
	}
	return body
}

func signTestRS256DeviceJWT(t *testing.T, key *rsa.PrivateKey, kid string, overrides map[string]any) string {
	t.Helper()
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":     IssuerKittyAPI,
		"sub":     "device:dev_1",
		"aud":     []string{AudienceKittySpace},
		"scope":   []string{string(ScopeDaemonConnect)},
		"v":       CredentialVersion2,
		"user_id": "user_1",
		"iat":     now.Unix(),
		"exp":     now.Add(time.Hour).Unix(),
	}
	for name, value := range overrides {
		claims[name] = value
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("sign RS256 device jwt: %v", err)
	}
	return signed
}

type blockingRoundTripper struct {
	started chan<- struct{}
	release <-chan struct{}
	body    []byte
}

func (rt blockingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	select {
	case rt.started <- struct{}{}:
	default:
	}
	select {
	case <-rt.release:
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(rt.body)),
		Request:    req,
	}, nil
}
