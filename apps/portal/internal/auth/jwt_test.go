package auth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/kittypaw-app/kittyportal/internal/auth"
	"github.com/kittypaw-app/kittyportal/internal/config"
)

func TestSignVerifyRoundtrip(t *testing.T) {
	cfg := config.LoadForTest()
	provider := auth.NewSingleKeyProvider(&cfg.JWTPrivateKey.PublicKey, cfg.JWTKID)

	token, err := auth.SignForAudiences("user-123", auth.DefaultAPIClientAudiences, auth.DefaultAPIClientScopes, cfg.JWTPrivateKey, cfg.JWTKID, 15*time.Minute)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	claims, err := auth.Verify(token, provider, auth.AudienceAPI)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}

	if claims.UserID != "user-123" {
		t.Fatalf("expected UserID=user-123, got %q", claims.UserID)
	}
}

func TestVerifyExpired(t *testing.T) {
	cfg := config.LoadForTest()
	provider := auth.NewSingleKeyProvider(&cfg.JWTPrivateKey.PublicKey, cfg.JWTKID)

	// -1*time.Hour blows past the 60s leeway window — must reject.
	token, err := auth.SignForAudiences("user-123", auth.DefaultAPIClientAudiences, auth.DefaultAPIClientScopes, cfg.JWTPrivateKey, cfg.JWTKID, -1*time.Hour)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	_, err = auth.Verify(token, provider, auth.AudienceAPI)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

// TestVerify_RejectsForeignKey: a token signed by a key whose kid is not
// in our JWKS provider must be rejected. Replaces the legacy
// TestVerifyWrongSecret (HS256 secret mismatch) — the modern equivalent
// is "kid not found in JWKS Lookup".
func TestVerify_RejectsForeignKey(t *testing.T) {
	cfg := config.LoadForTest()
	provider := auth.NewSingleKeyProvider(&cfg.JWTPrivateKey.PublicKey, cfg.JWTKID)

	// Mint a token with a foreign key + arbitrary kid not registered in
	// the provider.
	foreignKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate foreign key: %v", err)
	}
	token, err := auth.SignForAudiences("user-evil", auth.DefaultAPIClientAudiences, auth.DefaultAPIClientScopes, foreignKey, "foreign-kid", 15*time.Minute)
	if err != nil {
		t.Fatalf("sign with foreign key: %v", err)
	}

	if _, err := auth.Verify(token, provider, auth.AudienceAPI); err == nil {
		t.Fatal("expected Verify to reject token signed by foreign key (kid not in JWKS)")
	}
}

func TestVerifyMalformed(t *testing.T) {
	cfg := config.LoadForTest()
	provider := auth.NewSingleKeyProvider(&cfg.JWTPrivateKey.PublicKey, cfg.JWTKID)

	_, err := auth.Verify("not-a-jwt-token", provider, auth.AudienceAPI)
	if err == nil {
		t.Fatal("expected error for malformed token")
	}
}

// Plan 17 — kittychat credential foundation
// (docs/specs/kittychat-credential-foundation.md)

func TestSignForAudiences_RoundTrip(t *testing.T) {
	cfg := config.LoadForTest()
	provider := auth.NewSingleKeyProvider(&cfg.JWTPrivateKey.PublicKey, cfg.JWTKID)

	token, err := auth.SignForAudiences(
		"user-abc",
		[]string{"https://api.kittypaw.app", "https://chat.kittypaw.app", "https://space.kittypaw.app"},
		[]string{"chat:relay", "models:read"},
		cfg.JWTPrivateKey,
		cfg.JWTKID,
		15*time.Minute,
	)
	if err != nil {
		t.Fatalf("SignForAudiences: %v", err)
	}

	// Wire-format guard: header.alg=RS256, header.kid=cfg.JWTKID,
	// payload.v=ClaimsVersion (=2). These pin Plan 21 PR-B's RS256 cutover.
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT segments, got %d", len(parts))
	}
	hdrSeg, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var hdr map[string]any
	if err := json.Unmarshal(hdrSeg, &hdr); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	if hdr["alg"] != "RS256" {
		t.Fatalf("header.alg = %v, want RS256", hdr["alg"])
	}
	if hdr["kid"] != cfg.JWTKID {
		t.Fatalf("header.kid = %v, want %q", hdr["kid"], cfg.JWTKID)
	}
	payloadSeg, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(payloadSeg, &raw); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if v, _ := raw["v"].(float64); v != 2 {
		t.Fatalf("payload.v = %v, want 2", raw["v"])
	}

	claims, err := auth.Verify(token, provider, auth.AudienceAPI)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.UserID != "user-abc" {
		t.Fatalf("UserID = %q", claims.UserID)
	}
	if got := []string(claims.Audience); len(got) != 3 || got[0] != "https://api.kittypaw.app" || got[1] != "https://chat.kittypaw.app" || got[2] != "https://space.kittypaw.app" {
		t.Fatalf("Audience = %v, want [https://api.kittypaw.app https://chat.kittypaw.app https://space.kittypaw.app] (Space migration URL form)", got)
	}
	if len(claims.Scope) != 2 || claims.Scope[0] != "chat:relay" || claims.Scope[1] != "models:read" {
		t.Fatalf("Scope = %v, want [chat:relay models:read]", claims.Scope)
	}
	if claims.V != 2 {
		t.Fatalf("V = %d, want 2", claims.V)
	}
	if claims.Issuer != "https://portal.kittypaw.app/auth" {
		t.Fatalf("Issuer = %q, want https://portal.kittypaw.app/auth (RFC 7519 iss)", claims.Issuer)
	}
}

// TestClaimsJSONUsesSubField verifies the JSON wire shape uses RFC 7519
// "sub" (not legacy "uid"). Cross-service (kittychat) MUST be able to
// read the standard sub claim without any uid-fallback hack.
func TestClaimsJSONUsesSubField(t *testing.T) {
	cfg := config.LoadForTest()

	token, err := auth.SignForAudiences(
		"user-xyz",
		[]string{"https://api.kittypaw.app"},
		nil,
		cfg.JWTPrivateKey,
		cfg.JWTKID,
		15*time.Minute,
	)
	if err != nil {
		t.Fatalf("SignForAudiences: %v", err)
	}
	// Decode the middle (payload) segment without verification.
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT segments, got %d", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got, ok := raw["sub"].(string); !ok || got != "user-xyz" {
		t.Fatalf(`payload "sub" = %v, want "user-xyz"`, raw["sub"])
	}
	if _, ok := raw["uid"]; ok {
		t.Fatalf(`payload must not contain legacy "uid" key, got: %v`, raw)
	}
	if got, ok := raw["iss"].(string); !ok || got != "https://portal.kittypaw.app/auth" {
		t.Fatalf(`payload "iss" = %v, want "https://portal.kittypaw.app/auth"`, raw["iss"])
	}
}

// Plan 13 H1 — strict iss check.
// Same RSA key + wrong iss → Verify must reject (defense against same-key
// cross-service token confusion).
func TestVerify_RejectsWrongIssuer(t *testing.T) {
	cfg := config.LoadForTest()
	provider := auth.NewSingleKeyProvider(&cfg.JWTPrivateKey.PublicKey, cfg.JWTKID)

	claims := auth.Claims{
		UserID: "user-evil",
		V:      auth.ClaimsVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "evil-attacker",
			Audience:  jwt.ClaimStrings(auth.DefaultAPIClientAudiences),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = cfg.JWTKID
	signed, err := token.SignedString(cfg.JWTPrivateKey)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	_, err = auth.Verify(signed, provider, auth.AudienceAPI)
	if err == nil {
		t.Fatal("expected Verify to reject token with wrong issuer")
	}
	if !errors.Is(err, jwt.ErrTokenInvalidIssuer) {
		t.Fatalf("expected ErrTokenInvalidIssuer, got: %v", err)
	}
}

// Plan 13 H1 — strict aud check.
// Token with no audience (legacy bare-Sign shape) → Verify must reject.
func TestVerify_RejectsMissingAudience(t *testing.T) {
	cfg := config.LoadForTest()
	provider := auth.NewSingleKeyProvider(&cfg.JWTPrivateKey.PublicKey, cfg.JWTKID)

	claims := auth.Claims{
		UserID: "user-bare",
		V:      auth.ClaimsVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    auth.Issuer,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = cfg.JWTKID
	signed, err := token.SignedString(cfg.JWTPrivateKey)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	_, err = auth.Verify(signed, provider, auth.AudienceAPI)
	if err == nil {
		t.Fatal("expected Verify to reject token without audience")
	}
	if !errors.Is(err, jwt.ErrTokenRequiredClaimMissing) {
		t.Fatalf("expected ErrTokenRequiredClaimMissing, got: %v", err)
	}
}

// Plan 13 H1 — chat-only token must be rejected at api.kittypaw.app.
// Catches WithAudience(AudienceAPI) → WithAudience(AudienceChat) typo regression.
// Per spec D8: each resource server must enforce its own audience only.
func TestVerify_RejectsWrongAudience(t *testing.T) {
	cfg := config.LoadForTest()
	provider := auth.NewSingleKeyProvider(&cfg.JWTPrivateKey.PublicKey, cfg.JWTKID)

	claims := auth.Claims{
		UserID: "user-cross",
		V:      auth.ClaimsVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    auth.Issuer,
			Audience:  jwt.ClaimStrings{auth.AudienceChat}, // API aud 부재
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = cfg.JWTKID
	signed, err := token.SignedString(cfg.JWTPrivateKey)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	_, err = auth.Verify(signed, provider, auth.AudienceAPI)
	if err == nil {
		t.Fatal("expected Verify to reject chat-only audience token")
	}
	if !errors.Is(err, jwt.ErrTokenInvalidAudience) {
		t.Fatalf("expected ErrTokenInvalidAudience, got: %v", err)
	}
}

// Pin the contract: tokens minted with the legacy "uid" JSON tag (no "sub")
// MUST be rejected. There is no uid-fallback. The verifier reads the
// standard sub claim only.
func TestVerify_RejectsLegacyUIDOnlyToken(t *testing.T) {
	cfg := config.LoadForTest()
	provider := auth.NewSingleKeyProvider(&cfg.JWTPrivateKey.PublicKey, cfg.JWTKID)

	legacy := struct {
		LegacyUID string `json:"uid"`
		jwt.RegisteredClaims
	}{
		LegacyUID: "user-old",
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, legacy)
	token.Header["kid"] = cfg.JWTKID
	signed, err := token.SignedString(cfg.JWTPrivateKey)
	if err != nil {
		t.Fatalf("sign legacy: %v", err)
	}
	if _, err := auth.Verify(signed, provider, auth.AudienceAPI); err == nil {
		t.Fatal("expected Verify to reject token with only uid (no sub)")
	}
}

// Plan 21 PR-B step 16 — alg=HS256 downgrade attack guard.
// Even if an attacker forges an HS256 token with a kid known to the
// provider, the RS256-only WithValidMethods() must short-circuit before
// any signature check. Without this guard, a leaked public key + the
// HS256-with-public-key-as-secret trick would mint valid-looking tokens.
func TestVerify_RejectsHS256_Downgrade(t *testing.T) {
	cfg := config.LoadForTest()
	provider := auth.NewSingleKeyProvider(&cfg.JWTPrivateKey.PublicKey, cfg.JWTKID)

	claims := auth.Claims{
		UserID: "user-downgrade",
		V:      auth.ClaimsVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    auth.Issuer,
			Audience:  jwt.ClaimStrings(auth.DefaultAPIClientAudiences),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	token.Header["kid"] = cfg.JWTKID
	signed, err := token.SignedString([]byte("attacker-chosen-symmetric-secret")) //gitleaks:allow
	if err != nil {
		t.Fatalf("sign HS256: %v", err)
	}

	if _, err := auth.Verify(signed, provider, auth.AudienceAPI); err == nil {
		t.Fatal("expected Verify to reject HS256-signed token (downgrade attack)")
	}
}

// Plan 21 PR-B step 16 — leeway boundary: 30s past expiry must still pass.
// Verify uses jwt.WithLeeway(60*time.Second) for clock-skew tolerance
// agreed with kittychat. Tokens that expired ≤60s ago are accepted; this
// pins the in-window edge.
func TestVerify_LeewayBoundary_30sPass(t *testing.T) {
	cfg := config.LoadForTest()
	provider := auth.NewSingleKeyProvider(&cfg.JWTPrivateKey.PublicKey, cfg.JWTKID)

	// ttl=-30s → token expired 30s ago. Inside the 60s leeway window.
	token, err := auth.SignForAudiences("user-edge", auth.DefaultAPIClientAudiences, auth.DefaultAPIClientScopes, cfg.JWTPrivateKey, cfg.JWTKID, -30*time.Second)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	if _, err := auth.Verify(token, provider, auth.AudienceAPI); err != nil {
		t.Fatalf("expected Verify to accept token 30s past expiry (within 60s leeway), got: %v", err)
	}
}

// Plan 21 PR-B step 16 — leeway boundary: 90s past expiry must reject.
// Outside the 60s window → Verify rejects, even though the JWT is otherwise
// well-formed. Pinning the out-of-window edge guards against silent
// leeway extension drift.
func TestVerify_LeewayBoundary_90sFail(t *testing.T) {
	cfg := config.LoadForTest()
	provider := auth.NewSingleKeyProvider(&cfg.JWTPrivateKey.PublicKey, cfg.JWTKID)

	// ttl=-90s → token expired 90s ago. Outside the 60s leeway window.
	token, err := auth.SignForAudiences("user-edge", auth.DefaultAPIClientAudiences, auth.DefaultAPIClientScopes, cfg.JWTPrivateKey, cfg.JWTKID, -90*time.Second)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	if _, err := auth.Verify(token, provider, auth.AudienceAPI); err == nil {
		t.Fatal("expected Verify to reject token 90s past expiry (outside 60s leeway)")
	}
}

// Plan 21 PR-B step 16 — unknown kid must fail JWKS lookup.
// A token signed with the right alg + a foreign kid → Verify rejects at
// the keyfunc stage. This is distinct from TestVerify_RejectsForeignKey
// because here we use the SAME private key — only the kid header differs.
// (Real-world threat: a stale token referencing a kid we have rotated out.)
func TestVerify_RejectsUnknownKID(t *testing.T) {
	cfg := config.LoadForTest()
	provider := auth.NewSingleKeyProvider(&cfg.JWTPrivateKey.PublicKey, cfg.JWTKID)

	claims := auth.Claims{
		UserID: "user-stale",
		V:      auth.ClaimsVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    auth.Issuer,
			Audience:  jwt.ClaimStrings(auth.DefaultAPIClientAudiences),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "rotated-out-kid" // not in provider
	signed, err := token.SignedString(cfg.JWTPrivateKey)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	if _, err := auth.Verify(signed, provider, auth.AudienceAPI); err == nil {
		t.Fatal("expected Verify to reject token with unknown kid (rotated out / never registered)")
	}
}
