package identity

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIAuthenticatorAcceptsBearerToken(t *testing.T) {
	verifier := seededVerifier(t)
	auth := APIAuthenticator{Verifier: verifier}
	req := httptest.NewRequest(http.MethodGet, "/nodes/dev_1/v1/models", nil)
	req.Header.Set("Authorization", "Bearer api_secret")

	got, err := auth.Authenticate(req)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if got.UserID != "user_1" || got.DeviceID != "dev_1" || got.AccountID != "alice" {
		t.Fatalf("principal = %+v", got)
	}
}

func TestAPIAuthenticatorAcceptsXAPIKey(t *testing.T) {
	verifier := seededVerifier(t)
	auth := APIAuthenticator{Verifier: verifier}
	req := httptest.NewRequest(http.MethodGet, "/nodes/dev_1/v1/models", nil)
	req.Header.Set("x-api-key", "api_secret")

	got, err := auth.Authenticate(req)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if got.UserID != "user_1" || got.DeviceID != "dev_1" || got.AccountID != "alice" {
		t.Fatalf("principal = %+v", got)
	}
}

func TestDeviceAuthenticatorAcceptsBearerAndDeviceTokenHeader(t *testing.T) {
	verifier := seededVerifier(t)
	auth := DeviceAuthenticator{Verifier: verifier}

	for _, header := range []struct {
		name  string
		key   string
		value string
	}{
		{name: "bearer", key: "Authorization", value: "Bearer dev_secret"},
		{name: "device header", key: "x-device-token", value: "dev_secret"},
	} {
		t.Run(header.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/daemon/connect", nil)
			req.Header.Set(header.key, header.value)
			got, err := auth.Authenticate(req)
			if err != nil {
				t.Fatalf("Authenticate() error = %v", err)
			}
			if got.UserID != "user_1" || got.DeviceID != "dev_1" || len(got.LocalAccountIDs) != 1 || got.LocalAccountIDs[0] != "alice" {
				t.Fatalf("principal = %+v", got)
			}
		})
	}
}

func TestAuthenticatorsRejectMissingVerifierOrToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	if _, err := (APIAuthenticator{}).Authenticate(req); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("API Authenticate() error = %v, want ErrUnauthorized", err)
	}
	if _, err := (DeviceAuthenticator{}).Authenticate(req); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Device Authenticate() error = %v, want ErrUnauthorized", err)
	}

	verifier := NewMemoryCredentialVerifier()
	if _, err := (APIAuthenticator{Verifier: verifier}).Authenticate(req); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("API missing token error = %v, want ErrUnauthorized", err)
	}
	if _, err := (DeviceAuthenticator{Verifier: verifier}).Authenticate(req); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Device missing token error = %v, want ErrUnauthorized", err)
	}

	req.Header.Set("Authorization", "Bearer wrong")
	if _, err := (APIAuthenticator{Verifier: verifier}).Authenticate(req); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("API wrong token error = %v, want ErrUnauthorized", err)
	}
	if _, err := (DeviceAuthenticator{Verifier: verifier}).Authenticate(req); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Device wrong token error = %v, want ErrUnauthorized", err)
	}
}

func seededVerifier(t *testing.T) *MemoryCredentialVerifier {
	t.Helper()
	verifier := NewMemoryCredentialVerifier()
	if err := verifier.AddAPIClient("api_secret", APIClientClaims{
		Subject:   "user_1",
		Audiences: []string{AudienceKittyAPI, AudienceKittySpace},
		Version:   CredentialVersion1,
		Scopes:    []Scope{ScopeChatRelay, ScopeModelsRead},
		UserID:    "user_1",
		DeviceID:  "dev_1",
		AccountID: "alice",
	}); err != nil {
		t.Fatalf("AddAPIClient() error = %v", err)
	}
	if err := verifier.AddDevice("dev_secret", DeviceClaims{
		Subject:         "device:dev_1",
		Audiences:       []string{AudienceKittySpace},
		Version:         CredentialVersion1,
		Scopes:          []Scope{ScopeDaemonConnect},
		UserID:          "user_1",
		DeviceID:        "dev_1",
		LocalAccountIDs: []string{"alice"},
	}); err != nil {
		t.Fatalf("AddDevice() error = %v", err)
	}
	return verifier
}
