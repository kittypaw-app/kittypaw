package identity

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const testJWTSecret = "test-jwt-secret-with-at-least-32-bytes"

func TestJWTCredentialVerifierVerifiesAPIClientToken(t *testing.T) {
	verifier, err := NewJWTCredentialVerifier(JWTVerifierConfig{Secret: testJWTSecret})
	if err != nil {
		t.Fatalf("NewJWTCredentialVerifier() error = %v", err)
	}
	token := signTestJWT(t, map[string]any{
		"sub":   "user_1",
		"iss":   IssuerKittyAPI,
		"aud":   []string{AudienceKittyAPI, AudienceKittyHome},
		"scope": []string{string(ScopeChatRelay), string(ScopeModelsRead)},
		"v":     CredentialVersion1,
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(time.Hour).Unix(),
	})

	got, err := verifier.VerifyAPIClient(context.Background(), token)
	if err != nil {
		t.Fatalf("VerifyAPIClient() error = %v", err)
	}
	if got.Subject != "user_1" || got.UserID != "user_1" {
		t.Fatalf("user claims = %+v, want user_1 subject/user_id", got)
	}
	if got.DeviceID != "" || got.AccountID != "" {
		t.Fatalf("routing claims = %+v, want user-scoped token without device/account defaults", got)
	}
	assertStringSlice(t, got.Audiences, []string{AudienceKittyAPI, AudienceKittyHome})
	assertScopes(t, got.Scopes, []Scope{ScopeChatRelay, ScopeModelsRead})
}

func TestJWTCredentialVerifierIgnoresUnknownScopes(t *testing.T) {
	verifier, err := NewJWTCredentialVerifier(JWTVerifierConfig{Secret: testJWTSecret})
	if err != nil {
		t.Fatalf("NewJWTCredentialVerifier() error = %v", err)
	}
	token := signTestJWT(t, map[string]any{
		"sub":   "user_1",
		"iss":   IssuerKittyAPI,
		"aud":   []string{AudienceKittyAPI, AudienceKittyHome},
		"scope": []string{string(ScopeModelsRead), "future:scope"},
		"v":     CredentialVersion1,
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(time.Hour).Unix(),
	})

	got, err := verifier.VerifyAPIClient(context.Background(), token)
	if err != nil {
		t.Fatalf("VerifyAPIClient() error = %v", err)
	}
	assertScopes(t, got.Scopes, []Scope{ScopeModelsRead})
}

func TestJWTCredentialVerifierRejectsLegacyIssuerAudience(t *testing.T) {
	verifier, err := NewJWTCredentialVerifier(JWTVerifierConfig{Secret: testJWTSecret})
	if err != nil {
		t.Fatalf("NewJWTCredentialVerifier() error = %v", err)
	}
	tests := []struct {
		name string
		iss  string
		aud  []string
	}{
		{
			name: "legacy issuer and audience",
			iss:  "kittyapi",
			aud:  []string{"kittyapi", "kittyhome"},
		},
		{
			name: "legacy issuer",
			iss:  "kittyapi",
			aud:  []string{AudienceKittyAPI, AudienceKittyHome},
		},
		{
			name: "legacy audience",
			iss:  IssuerKittyAPI,
			aud:  []string{AudienceKittyAPI, "kittyhome"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := signTestJWT(t, map[string]any{
				"sub":   "user_1",
				"iss":   tt.iss,
				"aud":   tt.aud,
				"scope": []string{string(ScopeChatRelay), string(ScopeModelsRead)},
				"v":     CredentialVersion1,
				"iat":   time.Now().Unix(),
				"exp":   time.Now().Add(time.Hour).Unix(),
			})

			_, err := verifier.VerifyAPIClient(context.Background(), token)
			if !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("VerifyAPIClient() error = %v, want ErrUnauthorized", err)
			}
		})
	}
}

func TestJWTCredentialVerifierRejectsLegacyUIDOnlyToken(t *testing.T) {
	verifier, err := NewJWTCredentialVerifier(JWTVerifierConfig{Secret: testJWTSecret})
	if err != nil {
		t.Fatalf("NewJWTCredentialVerifier() error = %v", err)
	}
	token := signTestJWT(t, map[string]any{
		"uid":   "user_1",
		"iss":   IssuerKittyAPI,
		"aud":   []string{AudienceKittyAPI, AudienceKittyHome},
		"scope": []string{string(ScopeChatRelay), string(ScopeModelsRead)},
		"v":     CredentialVersion1,
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(time.Hour).Unix(),
	})

	_, err = verifier.VerifyAPIClient(context.Background(), token)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("VerifyAPIClient() error = %v, want ErrUnauthorized", err)
	}
}

func TestJWTCredentialVerifierRejectsInvalidAPIClientClaims(t *testing.T) {
	tests := []struct {
		name   string
		claims map[string]any
	}{
		{
			name: "missing issuer",
			claims: map[string]any{
				"sub": "user_1", "aud": []string{AudienceKittyAPI, AudienceKittyHome},
				"scope": []string{string(ScopeChatRelay), string(ScopeModelsRead)}, "v": CredentialVersion1,
			},
		},
		{
			name: "missing kittyhome audience",
			claims: map[string]any{
				"sub": "user_1", "iss": IssuerKittyAPI, "aud": []string{AudienceKittyAPI},
				"scope": []string{string(ScopeChatRelay), string(ScopeModelsRead)}, "v": CredentialVersion1,
			},
		},
		{
			name: "missing api scope",
			claims: map[string]any{
				"sub": "user_1", "iss": IssuerKittyAPI, "aud": []string{AudienceKittyAPI, AudienceKittyHome},
				"scope": []string{string(ScopeDaemonConnect)}, "v": CredentialVersion1,
			},
		},
		{
			name: "expired",
			claims: map[string]any{
				"sub": "user_1", "iss": IssuerKittyAPI, "aud": []string{AudienceKittyAPI, AudienceKittyHome},
				"scope": []string{string(ScopeChatRelay), string(ScopeModelsRead)}, "v": CredentialVersion1,
				"exp": time.Now().Add(-time.Minute).Unix(),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.claims["iat"] = time.Now().Unix()
			if _, ok := tt.claims["exp"]; !ok {
				tt.claims["exp"] = time.Now().Add(time.Hour).Unix()
			}
			verifier, err := NewJWTCredentialVerifier(JWTVerifierConfig{Secret: testJWTSecret})
			if err != nil {
				t.Fatalf("NewJWTCredentialVerifier() error = %v", err)
			}

			_, err = verifier.VerifyAPIClient(context.Background(), signTestJWT(t, tt.claims))
			if !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("VerifyAPIClient() error = %v, want ErrUnauthorized", err)
			}
		})
	}
}

func TestJWTCredentialVerifierVerifiesDeviceToken(t *testing.T) {
	verifier, err := NewJWTCredentialVerifier(JWTVerifierConfig{Secret: testJWTSecret})
	if err != nil {
		t.Fatalf("NewJWTCredentialVerifier() error = %v", err)
	}
	token := signTestJWT(t, map[string]any{
		"sub":            "device:dev_1",
		"iss":            IssuerKittyAPI,
		"aud":            []string{AudienceKittyHome},
		"scope":          []string{string(ScopeDaemonConnect)},
		"v":              CredentialVersion1,
		"user_id":        "user_1",
		"device_id":      "dev_1",
		"local_accounts": []string{"alice", "bob"},
		"iat":            time.Now().Unix(),
		"exp":            time.Now().Add(time.Hour).Unix(),
	})

	got, err := verifier.VerifyDevice(context.Background(), token)
	if err != nil {
		t.Fatalf("VerifyDevice() error = %v", err)
	}
	if got.Subject != "device:dev_1" || got.UserID != "user_1" || got.DeviceID != "dev_1" {
		t.Fatalf("device claims = %+v", got)
	}
	assertStringSlice(t, got.LocalAccountIDs, []string{"alice", "bob"})
	assertScopes(t, got.Scopes, []Scope{ScopeDaemonConnect})
}

func TestJWTCredentialVerifierRejectsDeviceSubjectMismatch(t *testing.T) {
	tests := []struct {
		name    string
		subject string
	}{
		{name: "user subject", subject: "user_1"},
		{name: "other device subject", subject: "device:dev_2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verifier, err := NewJWTCredentialVerifier(JWTVerifierConfig{Secret: testJWTSecret})
			if err != nil {
				t.Fatalf("NewJWTCredentialVerifier() error = %v", err)
			}
			token := signTestJWT(t, map[string]any{
				"sub":            tt.subject,
				"iss":            IssuerKittyAPI,
				"aud":            []string{AudienceKittyHome},
				"scope":          []string{string(ScopeDaemonConnect)},
				"v":              CredentialVersion1,
				"user_id":        "user_1",
				"device_id":      "dev_1",
				"local_accounts": []string{"alice"},
				"iat":            time.Now().Unix(),
				"exp":            time.Now().Add(time.Hour).Unix(),
			})

			_, err = verifier.VerifyDevice(context.Background(), token)
			if !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("VerifyDevice() error = %v, want ErrUnauthorized", err)
			}
		})
	}
}

func signTestJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "HS256", "typ": "JWT"}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	segments := []string{
		base64.RawURLEncoding.EncodeToString(headerJSON),
		base64.RawURLEncoding.EncodeToString(claimsJSON),
	}
	mac := hmac.New(sha256.New, []byte(testJWTSecret))
	_, _ = mac.Write([]byte(strings.Join(segments, ".")))
	segments = append(segments, base64.RawURLEncoding.EncodeToString(mac.Sum(nil)))
	return strings.Join(segments, ".")
}
