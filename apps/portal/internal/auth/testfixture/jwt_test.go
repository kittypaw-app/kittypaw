package testfixture_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/kittypaw-app/kittyportal/internal/auth/testfixture"
)

// TestIssueDeviceJWT_RoundTrip pins the wire format of device JWTs (Plan
// 20 spec): RS256 signing, kid header set, sub=device:<id>, user_id
// claim, aud, scope, v=2, exp/iat populated. The verifier recovers
// every field — drift in either direction (e.g. dropping user_id from
// the claim shape) trips this test, which is the same regression guard
// chat-side tests will rely on once they vendor the helper.
func TestIssueDeviceJWT_RoundTrip(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	now := time.Now()
	claims := testfixture.DeviceClaims{
		UserID:   "user-abc",
		DeviceID: "dev-xyz",
		Audience: []string{"https://chat.kittypaw.app", "https://home.kittypaw.app"},
		Scope:    []string{"daemon:connect"},
		Version:  2,
		IssuedAt: now,
		Expires:  now.Add(15 * time.Minute),
	}

	tokenStr, err := testfixture.IssueDeviceJWT(key, "test-kid", claims)
	if err != nil {
		t.Fatalf("IssueDeviceJWT: %v", err)
	}

	// Inspect the unverified header for alg + kid before validating —
	// the header is part of the contract, not an implementation detail.
	parts := strings.SplitN(tokenStr, ".", 3)
	if len(parts) != 3 {
		t.Fatalf("token did not have 3 segments: %q", tokenStr)
	}
	header, _, err := jwt.NewParser().ParseUnverified(tokenStr, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("ParseUnverified: %v", err)
	}
	if got := header.Header["alg"]; got != "RS256" {
		t.Fatalf("header.alg = %v, want RS256", got)
	}
	if got := header.Header["kid"]; got != "test-kid" {
		t.Fatalf("header.kid = %v, want test-kid", got)
	}

	// Now verify with the public key and inspect the payload.
	parsed, err := jwt.Parse(tokenStr, func(*jwt.Token) (any, error) {
		return &key.PublicKey, nil
	}, jwt.WithValidMethods([]string{"RS256"}))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}

	mc, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatalf("unexpected claims type: %T", parsed.Claims)
	}

	// Re-encode + decode through json so numeric/string types match
	// what real verifiers see on the wire.
	mcJSON, _ := json.Marshal(mc)
	var got struct {
		Sub    string   `json:"sub"`
		UserID string   `json:"user_id"`
		Aud    []string `json:"aud"`
		Scope  []string `json:"scope"`
		V      int      `json:"v"`
	}
	if err := json.Unmarshal(mcJSON, &got); err != nil {
		// aud may serialize as string when single-element — try both.
		var altGot struct {
			Sub    string   `json:"sub"`
			UserID string   `json:"user_id"`
			Aud    string   `json:"aud"`
			Scope  []string `json:"scope"`
			V      int      `json:"v"`
		}
		if err2 := json.Unmarshal(mcJSON, &altGot); err2 != nil {
			t.Fatalf("decode payload: %v / %v", err, err2)
		}
		got.Sub = altGot.Sub
		got.UserID = altGot.UserID
		got.Aud = []string{altGot.Aud}
		got.Scope = altGot.Scope
		got.V = altGot.V
	}

	if got.Sub != "device:dev-xyz" {
		t.Fatalf("sub = %q, want device:dev-xyz", got.Sub)
	}
	if got.UserID != "user-abc" {
		t.Fatalf("user_id = %q, want user-abc", got.UserID)
	}
	if len(got.Aud) != 2 || got.Aud[0] != "https://chat.kittypaw.app" || got.Aud[1] != "https://home.kittypaw.app" {
		t.Fatalf("aud = %v, want [https://chat.kittypaw.app https://home.kittypaw.app]", got.Aud)
	}
	if len(got.Scope) != 1 || got.Scope[0] != "daemon:connect" {
		t.Fatalf("scope = %v, want [daemon:connect]", got.Scope)
	}
	if got.V != 2 {
		t.Fatalf("v = %d, want 2", got.V)
	}
}
