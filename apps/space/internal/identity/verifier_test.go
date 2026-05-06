package identity

import (
	"context"
	"errors"
	"testing"
)

func TestMemoryCredentialVerifierVerifiesSeededAPIClient(t *testing.T) {
	verifier := NewMemoryCredentialVerifier()
	want := APIClientClaims{
		Subject:   "user_1",
		Audiences: []string{AudienceKittySpace},
		Version:   CredentialVersion1,
		Scopes:    []Scope{ScopeChatRelay, ScopeModelsRead},
		UserID:    "user_1",
		DeviceID:  "dev_1",
		AccountID: "alice",
	}
	if err := verifier.AddAPIClient("api_secret", want); err != nil {
		t.Fatalf("AddAPIClient() error = %v", err)
	}

	got, err := verifier.VerifyAPIClient(context.Background(), "api_secret")
	if err != nil {
		t.Fatalf("VerifyAPIClient() error = %v", err)
	}
	if got.Subject != want.Subject || !sameStrings(got.Audiences, want.Audiences) || got.Version != want.Version {
		t.Fatalf("claims = %+v, want %+v", got, want)
	}
	if got.UserID != want.UserID || got.DeviceID != want.DeviceID || got.AccountID != want.AccountID {
		t.Fatalf("identity claims = %+v, want %+v", got, want)
	}
	assertScopes(t, got.Scopes, want.Scopes)
}

func TestMemoryCredentialVerifierVerifiesSeededDevice(t *testing.T) {
	verifier := NewMemoryCredentialVerifier()
	want := DeviceClaims{
		Subject:         "device:dev_1",
		Audiences:       []string{AudienceKittySpace},
		Version:         CredentialVersion1,
		Scopes:          []Scope{ScopeDaemonConnect},
		UserID:          "user_1",
		DeviceID:        "dev_1",
		LocalAccountIDs: []string{"alice", "bob"},
	}
	if err := verifier.AddDevice("dev_secret", want); err != nil {
		t.Fatalf("AddDevice() error = %v", err)
	}

	got, err := verifier.VerifyDevice(context.Background(), "dev_secret")
	if err != nil {
		t.Fatalf("VerifyDevice() error = %v", err)
	}
	if got.Subject != want.Subject || !sameStrings(got.Audiences, want.Audiences) || got.Version != want.Version {
		t.Fatalf("claims = %+v, want %+v", got, want)
	}
	if got.UserID != want.UserID || got.DeviceID != want.DeviceID {
		t.Fatalf("identity claims = %+v, want %+v", got, want)
	}
	assertScopes(t, got.Scopes, want.Scopes)
	assertStringSlice(t, got.LocalAccountIDs, want.LocalAccountIDs)
}

func TestMemoryCredentialVerifierRejectsUnknownTokens(t *testing.T) {
	verifier := NewMemoryCredentialVerifier()

	if _, err := verifier.VerifyAPIClient(context.Background(), "missing"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("VerifyAPIClient() error = %v, want ErrUnauthorized", err)
	}
	if _, err := verifier.VerifyDevice(context.Background(), "missing"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("VerifyDevice() error = %v, want ErrUnauthorized", err)
	}
	if _, err := verifier.VerifyAPIClient(context.Background(), ""); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("VerifyAPIClient(empty) error = %v, want ErrUnauthorized", err)
	}
	if _, err := verifier.VerifyDevice(context.Background(), ""); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("VerifyDevice(empty) error = %v, want ErrUnauthorized", err)
	}
}

func TestMemoryCredentialVerifierValidatesAPIClientSeeds(t *testing.T) {
	valid := APIClientClaims{
		Subject:   "user_1",
		Audiences: []string{AudienceKittySpace},
		Version:   CredentialVersion1,
		Scopes:    []Scope{ScopeChatRelay, ScopeModelsRead},
		UserID:    "user_1",
		DeviceID:  "dev_1",
		AccountID: "alice",
	}
	tests := []struct {
		name   string
		token  string
		claims APIClientClaims
	}{
		{name: "empty token", token: "", claims: valid},
		{name: "missing kittyspace audience", token: "api_secret", claims: withAPIAudiences(valid, []string{AudienceKittyAPI})},
		{name: "legacy kittyspace audience", token: "api_secret", claims: withAPIAudiences(valid, []string{"kittyspace"})},
		{name: "wrong version", token: "api_secret", claims: withAPIVersion(valid, CredentialVersion2+1)},
		{name: "missing scope", token: "api_secret", claims: withAPIScopes(valid, nil)},
		{name: "unknown scope", token: "api_secret", claims: withAPIScopes(valid, []Scope{"unknown"})},
		{name: "missing account", token: "api_secret", claims: withAPIAccount(valid, "")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verifier := NewMemoryCredentialVerifier()
			if err := verifier.AddAPIClient(tt.token, tt.claims); err == nil {
				t.Fatal("AddAPIClient() error = nil, want validation error")
			}
		})
	}
}

func TestMemoryCredentialVerifierValidatesDeviceSeeds(t *testing.T) {
	valid := DeviceClaims{
		Subject:         "device:dev_1",
		Audiences:       []string{AudienceKittySpace},
		Version:         CredentialVersion1,
		Scopes:          []Scope{ScopeDaemonConnect},
		UserID:          "user_1",
		DeviceID:        "dev_1",
		LocalAccountIDs: []string{"alice"},
	}
	tests := []struct {
		name   string
		token  string
		claims DeviceClaims
	}{
		{name: "empty token", token: "", claims: valid},
		{name: "missing kittyspace audience", token: "dev_secret", claims: withDeviceAudiences(valid, []string{AudienceKittyAPI})},
		{name: "legacy kittyspace audience", token: "dev_secret", claims: withDeviceAudiences(valid, []string{"kittyspace"})},
		{name: "wrong version", token: "dev_secret", claims: withDeviceVersion(valid, CredentialVersion2+1)},
		{name: "missing scope", token: "dev_secret", claims: withDeviceScopes(valid, nil)},
		{name: "unknown scope", token: "dev_secret", claims: withDeviceScopes(valid, []Scope{"unknown"})},
		{name: "missing account", token: "dev_secret", claims: withDeviceAccounts(valid, nil)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verifier := NewMemoryCredentialVerifier()
			if err := verifier.AddDevice(tt.token, tt.claims); err == nil {
				t.Fatal("AddDevice() error = nil, want validation error")
			}
		})
	}
}

func TestMemoryCredentialVerifierReturnsDefensiveCopies(t *testing.T) {
	verifier := NewMemoryCredentialVerifier()
	apiClaims := APIClientClaims{
		Subject:   "user_1",
		Audiences: []string{AudienceKittySpace},
		Version:   CredentialVersion1,
		Scopes:    []Scope{ScopeChatRelay, ScopeModelsRead},
		UserID:    "user_1",
		DeviceID:  "dev_1",
		AccountID: "alice",
	}
	if err := verifier.AddAPIClient("api_secret", apiClaims); err != nil {
		t.Fatalf("AddAPIClient() error = %v", err)
	}
	deviceClaims := DeviceClaims{
		Subject:         "device:dev_1",
		Audiences:       []string{AudienceKittySpace},
		Version:         CredentialVersion1,
		Scopes:          []Scope{ScopeDaemonConnect},
		UserID:          "user_1",
		DeviceID:        "dev_1",
		LocalAccountIDs: []string{"alice"},
	}
	if err := verifier.AddDevice("dev_secret", deviceClaims); err != nil {
		t.Fatalf("AddDevice() error = %v", err)
	}

	apiGot, err := verifier.VerifyAPIClient(context.Background(), "api_secret")
	if err != nil {
		t.Fatalf("VerifyAPIClient() error = %v", err)
	}
	apiGot.Scopes[0] = "mutated"
	apiGot.Audiences[0] = "mutated"
	apiAgain, err := verifier.VerifyAPIClient(context.Background(), "api_secret")
	if err != nil {
		t.Fatalf("VerifyAPIClient() again error = %v", err)
	}
	assertScopes(t, apiAgain.Scopes, apiClaims.Scopes)
	assertStringSlice(t, apiAgain.Audiences, apiClaims.Audiences)

	deviceGot, err := verifier.VerifyDevice(context.Background(), "dev_secret")
	if err != nil {
		t.Fatalf("VerifyDevice() error = %v", err)
	}
	deviceGot.Scopes[0] = "mutated"
	deviceGot.Audiences[0] = "mutated"
	deviceGot.LocalAccountIDs[0] = "mutated"
	deviceAgain, err := verifier.VerifyDevice(context.Background(), "dev_secret")
	if err != nil {
		t.Fatalf("VerifyDevice() again error = %v", err)
	}
	assertScopes(t, deviceAgain.Scopes, deviceClaims.Scopes)
	assertStringSlice(t, deviceAgain.Audiences, deviceClaims.Audiences)
	assertStringSlice(t, deviceAgain.LocalAccountIDs, deviceClaims.LocalAccountIDs)
}

func TestMemoryCredentialVerifierAcceptsMultiAudienceClaims(t *testing.T) {
	verifier := NewMemoryCredentialVerifier()
	claims := APIClientClaims{
		Subject:   "user_1",
		Audiences: []string{AudienceKittyAPI, AudienceKittySpace},
		Version:   CredentialVersion1,
		Scopes:    []Scope{ScopeChatRelay, ScopeModelsRead},
		UserID:    "user_1",
		DeviceID:  "dev_1",
		AccountID: "alice",
	}
	if err := verifier.AddAPIClient("api_secret", claims); err != nil {
		t.Fatalf("AddAPIClient() error = %v", err)
	}
	got, err := verifier.VerifyAPIClient(context.Background(), "api_secret")
	if err != nil {
		t.Fatalf("VerifyAPIClient() error = %v", err)
	}
	assertStringSlice(t, got.Audiences, claims.Audiences)
}

func assertScopes(t *testing.T, got, want []Scope) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("scopes = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("scopes = %+v, want %+v", got, want)
		}
	}
}

func assertStringSlice(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("slice = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("slice = %+v, want %+v", got, want)
		}
	}
}

func withAPIAudiences(claims APIClientClaims, audiences []string) APIClientClaims {
	claims.Audiences = audiences
	return claims
}

func withAPIVersion(claims APIClientClaims, version int) APIClientClaims {
	claims.Version = version
	return claims
}

func withAPIScopes(claims APIClientClaims, scopes []Scope) APIClientClaims {
	claims.Scopes = scopes
	return claims
}

func withAPIAccount(claims APIClientClaims, accountID string) APIClientClaims {
	claims.AccountID = accountID
	return claims
}

func withDeviceAudiences(claims DeviceClaims, audiences []string) DeviceClaims {
	claims.Audiences = audiences
	return claims
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func withDeviceVersion(claims DeviceClaims, version int) DeviceClaims {
	claims.Version = version
	return claims
}

func withDeviceScopes(claims DeviceClaims, scopes []Scope) DeviceClaims {
	claims.Scopes = scopes
	return claims
}

func withDeviceAccounts(claims DeviceClaims, accountIDs []string) DeviceClaims {
	claims.LocalAccountIDs = accountIDs
	return claims
}
