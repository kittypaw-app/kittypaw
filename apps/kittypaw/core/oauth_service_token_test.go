package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestServiceTokenManagerSaveAndLoadCurrentToken(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	secrets := &SecretsStore{path: t.TempDir() + "/secrets.json", data: make(map[string]map[string]string)}
	mgr := NewServiceTokenManager(secrets)
	mgr.now = func() time.Time { return now }

	tokens := ServiceTokenSet{
		Provider:       "gmail",
		AccessToken:    "access-1",
		RefreshToken:   "refresh-1",
		TokenType:      "Bearer",
		ExpiresIn:      3600,
		Scope:          "gmail.readonly",
		Email:          "alice@example.com",
		Username:       "alice",
		ConnectBaseURL: "https://connect.kittypaw.app",
	}
	if err := mgr.Save("gmail", tokens); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := mgr.LoadAccessToken("gmail")
	if err != nil {
		t.Fatalf("LoadAccessToken: %v", err)
	}
	if got != "access-1" {
		t.Fatalf("LoadAccessToken = %q", got)
	}
	ns := ServiceTokenNamespace("gmail")
	for key, want := range map[string]string{
		"access_token":     "access-1",
		"refresh_token":    "refresh-1",
		"token_type":       "Bearer",
		"scope":            "gmail.readonly",
		"email":            "alice@example.com",
		"username":         "alice",
		"connect_base_url": "https://connect.kittypaw.app",
	} {
		if got, ok := secrets.Get(ns, key); !ok || got != want {
			t.Fatalf("%s = (%q, %v), want %q", key, got, ok, want)
		}
	}
	if got, ok := secrets.Get(ns, "expires_at"); !ok || got == "" {
		t.Fatalf("expires_at = (%q, %v), want stored timestamp", got, ok)
	}
}

func TestServiceTokenManagerRefreshesExpiredToken(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	var gotPath string
	var gotRefresh string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode refresh body: %v", err)
		}
		gotRefresh = body["refresh_token"]
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"provider":"gmail","access_token":"access-2","token_type":"Bearer","expires_in":3600,"scope":"gmail.readonly"}`)
	}))
	defer ts.Close()

	secrets := &SecretsStore{path: t.TempDir() + "/secrets.json", data: make(map[string]map[string]string)}
	mgr := NewServiceTokenManager(secrets)
	mgr.client = ts.Client()
	mgr.now = func() time.Time { return now }
	if err := mgr.Save("gmail", ServiceTokenSet{
		Provider:       "gmail",
		AccessToken:    "access-1",
		RefreshToken:   "refresh-1",
		ExpiresAt:      now.Add(-time.Minute),
		ConnectBaseURL: ts.URL,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := mgr.LoadAccessToken("gmail")
	if err != nil {
		t.Fatalf("LoadAccessToken: %v", err)
	}
	if got != "access-2" {
		t.Fatalf("LoadAccessToken = %q", got)
	}
	if gotPath != "/connect/gmail/refresh" {
		t.Fatalf("refresh path = %q", gotPath)
	}
	if gotRefresh != "refresh-1" {
		t.Fatalf("refresh token = %q", gotRefresh)
	}
	if storedRefresh, _ := secrets.Get(ServiceTokenNamespace("gmail"), "refresh_token"); storedRefresh != "refresh-1" {
		t.Fatalf("refresh token should be preserved, got %q", storedRefresh)
	}
}

func TestServiceTokenManagerRefreshesProviderSpecificEndpoint(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode refresh body: %v", err)
		}
		if body["refresh_token"] != "x-refresh-1" {
			t.Fatalf("refresh token = %q", body["refresh_token"])
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"provider":"x","access_token":"x-access-2","refresh_token":"x-refresh-2","token_type":"bearer","expires_in":7200,"scope":"tweet.read users.read offline.access","username":"jaypark"}`)
	}))
	defer ts.Close()

	secrets := &SecretsStore{path: t.TempDir() + "/secrets.json", data: make(map[string]map[string]string)}
	mgr := NewServiceTokenManager(secrets)
	mgr.client = ts.Client()
	mgr.now = func() time.Time { return now }
	if err := mgr.Save("x", ServiceTokenSet{
		Provider:       "x",
		AccessToken:    "x-access-1",
		RefreshToken:   "x-refresh-1",
		ExpiresAt:      now.Add(-time.Minute),
		ConnectBaseURL: ts.URL,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := mgr.LoadAccessToken("x")
	if err != nil {
		t.Fatalf("LoadAccessToken: %v", err)
	}
	if got != "x-access-2" {
		t.Fatalf("LoadAccessToken = %q", got)
	}
	if gotPath != "/connect/x/refresh" {
		t.Fatalf("refresh path = %q", gotPath)
	}
	if storedUsername, _ := secrets.Get(ServiceTokenNamespace("x"), "username"); storedUsername != "jaypark" {
		t.Fatalf("username = %q", storedUsername)
	}
	if storedRefresh, _ := secrets.Get(ServiceTokenNamespace("x"), "refresh_token"); storedRefresh != "x-refresh-2" {
		t.Fatalf("refresh token = %q", storedRefresh)
	}
}

func TestServiceTokenManagerExpiredWithoutRefreshTokenIsActionable(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	secrets := &SecretsStore{path: t.TempDir() + "/secrets.json", data: make(map[string]map[string]string)}
	mgr := NewServiceTokenManager(secrets)
	mgr.now = func() time.Time { return now }
	if err := mgr.Save("gmail", ServiceTokenSet{
		Provider:    "gmail",
		AccessToken: "access-1",
		ExpiresAt:   now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	_, err := mgr.LoadAccessToken("gmail")
	if err == nil {
		t.Fatal("LoadAccessToken succeeded, want reconnect error")
	}
	if !strings.Contains(err.Error(), "kittypaw connect gmail") {
		t.Fatalf("error = %v, want reconnect guidance", err)
	}
}

func TestServiceTokenManagerMissingToken(t *testing.T) {
	secrets := &SecretsStore{path: t.TempDir() + "/secrets.json", data: make(map[string]map[string]string)}
	mgr := NewServiceTokenManager(secrets)

	got, err := mgr.LoadAccessToken("gmail")
	if err != nil {
		t.Fatalf("LoadAccessToken: %v", err)
	}
	if got != "" {
		t.Fatalf("LoadAccessToken = %q, want empty for missing connection", got)
	}
}
