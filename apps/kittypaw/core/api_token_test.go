package core

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNamespaceForURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"http://localhost:8080", "kittypaw-api/localhost:8080"},
		{"https://api.kittypaw.com", "kittypaw-api/api.kittypaw.com"},
		{"http://10.0.0.1:3000", "kittypaw-api/10.0.0.1:3000"},
		{"https://api.kittypaw.com:443", "kittypaw-api/api.kittypaw.com:443"},
	}
	for _, tt := range tests {
		got := NamespaceForURL(tt.url)
		if got != tt.want {
			t.Errorf("NamespaceForURL(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func makeJWT(expUnix int64) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256"}`))
	claims, _ := json.Marshal(map[string]any{"uid": "user-1", "exp": expUnix})
	payload := base64.RawURLEncoding.EncodeToString(claims)
	sig := base64.RawURLEncoding.EncodeToString([]byte("fake-signature"))
	return header + "." + payload + "." + sig
}

func TestIsJWTExpired(t *testing.T) {
	tests := []struct {
		name string
		exp  int64
		want bool
	}{
		{"future 10min", time.Now().Add(10 * time.Minute).Unix(), false},
		{"past 1min", time.Now().Add(-1 * time.Minute).Unix(), true},
		{"within grace 15s", time.Now().Add(15 * time.Second).Unix(), true},
		{"exactly at grace 30s", time.Now().Add(30 * time.Second).Unix(), true},
		{"just outside grace 31s", time.Now().Add(31 * time.Second).Unix(), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := makeJWT(tt.exp)
			got := isJWTExpired(token)
			if got != tt.want {
				t.Errorf("isJWTExpired(exp=%d) = %v, want %v", tt.exp, got, tt.want)
			}
		})
	}
}

func TestIsJWTExpired_InvalidTokens(t *testing.T) {
	tests := []string{
		"",
		"not-a-jwt",
		"a.b", // only 2 parts
		"a." + base64.RawURLEncoding.EncodeToString([]byte("{}")) + ".c", // no exp
	}
	for _, token := range tests {
		if !isJWTExpired(token) {
			t.Errorf("isJWTExpired(%q) should be true for invalid token", token)
		}
	}
}

func TestAPITokenManager_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	secrets := &SecretsStore{
		path: dir + "/secrets.json",
		data: make(map[string]map[string]string),
	}

	mgr := NewAPITokenManager("", secrets)
	apiURL := "http://localhost:8080"

	validToken := makeJWT(time.Now().Add(10 * time.Minute).Unix())
	if err := mgr.SaveTokens(apiURL, validToken, "refresh-abc"); err != nil {
		t.Fatal(err)
	}

	got, err := mgr.LoadAccessToken(apiURL)
	if err != nil {
		t.Fatal(err)
	}
	if got != validToken {
		t.Errorf("LoadAccessToken = %q, want %q", got, validToken)
	}
	if got, ok := secrets.Get("kittypaw-api", "api_url"); !ok || got != apiURL {
		t.Fatalf("default api_url = (%q, %v), want %q true", got, ok, apiURL)
	}
}

func TestAPITokenManager_NotLoggedIn(t *testing.T) {
	secrets := &SecretsStore{
		path: "/dev/null",
		data: make(map[string]map[string]string),
	}
	mgr := NewAPITokenManager("", secrets)

	got, err := mgr.LoadAccessToken("http://localhost:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty token for not-logged-in, got %q", got)
	}
}

func TestAPITokenManager_AutoRefresh(t *testing.T) {
	newToken := makeJWT(time.Now().Add(15 * time.Minute).Unix())

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":%q,"refresh_token":"new-refresh","token_type":"Bearer","expires_in":900}`, newToken)
	}))
	defer ts.Close()

	secrets := &SecretsStore{
		path: t.TempDir() + "/secrets.json",
		data: make(map[string]map[string]string),
	}
	mgr := NewAPITokenManager("", secrets)
	mgr.client = ts.Client()

	// Save an expired token.
	expiredToken := makeJWT(time.Now().Add(-1 * time.Minute).Unix())
	mgr.SaveTokens(ts.URL, expiredToken, "old-refresh")

	got, err := mgr.LoadAccessToken(ts.URL)
	if err != nil {
		t.Fatalf("LoadAccessToken: %v", err)
	}
	if got != newToken {
		t.Errorf("expected refreshed token, got %q", got)
	}

	// Verify the new refresh token was saved.
	ns := NamespaceForURL(ts.URL)
	if rt, _ := secrets.Get(ns, "refresh_token"); rt != "new-refresh" {
		t.Errorf("expected new refresh token saved, got %q", rt)
	}
}

func TestAPITokenManager_SaveAndLoadAPIBaseURL(t *testing.T) {
	secrets := &SecretsStore{
		path: t.TempDir() + "/secrets.json",
		data: make(map[string]map[string]string),
	}
	mgr := NewAPITokenManager("", secrets)
	apiURL := "http://localhost:8080"

	if err := mgr.SaveAPIBaseURL(apiURL, "https://api.kittypaw.app"); err != nil {
		t.Fatal(err)
	}
	got, ok := mgr.LoadAPIBaseURL(apiURL)
	if !ok || got != "https://api.kittypaw.app" {
		t.Errorf("LoadAPIBaseURL = (%q, %v), want (https://api.kittypaw.app, true)", got, ok)
	}

	// Empty value deletes the key.
	if err := mgr.SaveAPIBaseURL(apiURL, ""); err != nil {
		t.Fatal(err)
	}
	got, ok = mgr.LoadAPIBaseURL(apiURL)
	if ok || got != "" {
		t.Errorf("after empty save, Load = (%q, %v), want (\"\", false)", got, ok)
	}
}

func TestAPITokenManager_SaveAndLoadSkillsRegistryURL(t *testing.T) {
	secrets := &SecretsStore{
		path: t.TempDir() + "/secrets.json",
		data: make(map[string]map[string]string),
	}
	mgr := NewAPITokenManager("", secrets)
	apiURL := "http://localhost:8080"

	if err := mgr.SaveSkillsRegistryURL(apiURL, "https://github.com/kittypaw-app/skills"); err != nil {
		t.Fatal(err)
	}
	got, ok := mgr.LoadSkillsRegistryURL(apiURL)
	if !ok || got != "https://github.com/kittypaw-app/skills" {
		t.Errorf("LoadSkillsRegistryURL = (%q, %v)", got, ok)
	}

	if err := mgr.SaveSkillsRegistryURL(apiURL, ""); err != nil {
		t.Fatal(err)
	}
	_, ok = mgr.LoadSkillsRegistryURL(apiURL)
	if ok {
		t.Errorf("expected key deleted after empty save")
	}
}

func TestAPITokenManager_SaveKakaoRelayBaseURL_EmptyDeletes(t *testing.T) {
	secrets := &SecretsStore{
		path: t.TempDir() + "/secrets.json",
		data: make(map[string]map[string]string),
	}
	mgr := NewAPITokenManager("", secrets)
	apiURL := "http://localhost:8080"
	ns := NamespaceForURL(apiURL)

	// First store a real value.
	if err := mgr.SaveKakaoRelayBaseURL(apiURL, "https://kakao.kittypaw.app"); err != nil {
		t.Fatal(err)
	}
	got, ok := mgr.LoadKakaoRelayBaseURL(apiURL)
	if !ok || got != "https://kakao.kittypaw.app" {
		t.Fatalf("setup: LoadKakaoRelayBaseURL = (%q, %v)", got, ok)
	}
	if stored, ok := secrets.Get(ns, "kakao_relay_url"); !ok || stored != "https://kakao.kittypaw.app" {
		t.Fatalf("SaveKakaoRelayBaseURL stored key = (%q, %v), want kakao_relay_url", stored, ok)
	}
	if stale, ok := secrets.Get(ns, "relay_url"); ok || stale != "" {
		t.Fatalf("SaveKakaoRelayBaseURL must not write legacy relay_url, got (%q, %v)", stale, ok)
	}

	// Empty save must delete the key so stale URLs don't persist across relay migrations.
	if err := mgr.SaveKakaoRelayBaseURL(apiURL, ""); err != nil {
		t.Fatal(err)
	}
	got, ok = mgr.LoadKakaoRelayBaseURL(apiURL)
	if ok || got != "" {
		t.Errorf("after empty save, Load = (%q, %v), want (\"\", false)", got, ok)
	}
}

func TestAPITokenManager_SaveChatRelayURL_EmptyDeletes(t *testing.T) {
	secrets := &SecretsStore{
		path: t.TempDir() + "/secrets.json",
		data: make(map[string]map[string]string),
	}
	mgr := NewAPITokenManager("", secrets)
	apiURL := "http://localhost:8080"
	ns := NamespaceForURL(apiURL)

	if err := mgr.SaveChatRelayURL(apiURL, "https://chat.kittypaw.app"); err != nil {
		t.Fatal(err)
	}
	got, ok := mgr.LoadChatRelayURL(apiURL)
	if !ok || got != "https://chat.kittypaw.app" {
		t.Fatalf("LoadChatRelayURL = (%q, %v), want chat relay URL", got, ok)
	}
	if stored, ok := secrets.Get(ns, "chat_relay_url"); !ok || stored != "https://chat.kittypaw.app" {
		t.Fatalf("SaveChatRelayURL stored key = (%q, %v), want chat_relay_url", stored, ok)
	}

	if err := mgr.SaveChatRelayURL(apiURL, ""); err != nil {
		t.Fatal(err)
	}
	got, ok = mgr.LoadChatRelayURL(apiURL)
	if ok || got != "" {
		t.Errorf("after empty save, LoadChatRelayURL = (%q, %v), want (\"\", false)", got, ok)
	}
}

func TestAPITokenManager_SaveAndLoadChatRelayDeviceID(t *testing.T) {
	secrets := &SecretsStore{
		path: t.TempDir() + "/secrets.json",
		data: make(map[string]map[string]string),
	}
	mgr := NewAPITokenManager("", secrets)
	apiURL := "https://portal.kittypaw.app"
	ns := NamespaceForURL(apiURL)

	if err := mgr.SaveChatRelayDeviceID(apiURL, "dev_123"); err != nil {
		t.Fatal(err)
	}
	got, ok := mgr.LoadChatRelayDeviceID(apiURL)
	if !ok || got != "dev_123" {
		t.Fatalf("LoadChatRelayDeviceID = (%q, %v), want device id", got, ok)
	}
	if stored, ok := secrets.Get(ns, "chat_relay_device_id"); !ok || stored != "dev_123" {
		t.Fatalf("stored device id = (%q, %v), want chat_relay_device_id", stored, ok)
	}

	if err := mgr.SaveChatRelayDeviceID(apiURL, ""); err != nil {
		t.Fatal(err)
	}
	got, ok = mgr.LoadChatRelayDeviceID(apiURL)
	if ok || got != "" {
		t.Fatalf("after empty save, LoadChatRelayDeviceID = (%q, %v), want empty false", got, ok)
	}
}

func TestAPITokenManager_SaveAuthBaseURL_EmptyDeletes(t *testing.T) {
	secrets := &SecretsStore{
		path: t.TempDir() + "/secrets.json",
		data: make(map[string]map[string]string),
	}
	mgr := NewAPITokenManager("", secrets)
	apiURL := "https://portal.kittypaw.app"
	ns := NamespaceForURL(apiURL)

	if err := mgr.SaveAuthBaseURL(apiURL, "https://portal.kittypaw.app/auth"); err != nil {
		t.Fatal(err)
	}
	got, ok := mgr.LoadAuthBaseURL(apiURL)
	if !ok || got != "https://portal.kittypaw.app/auth" {
		t.Fatalf("LoadAuthBaseURL = (%q, %v), want auth base URL", got, ok)
	}
	if stored, ok := secrets.Get(ns, "auth_base_url"); !ok || stored != "https://portal.kittypaw.app/auth" {
		t.Fatalf("stored auth base URL = (%q, %v), want auth_base_url", stored, ok)
	}

	if err := mgr.SaveAuthBaseURL(apiURL, ""); err != nil {
		t.Fatal(err)
	}
	got, ok = mgr.LoadAuthBaseURL(apiURL)
	if ok || got != "" {
		t.Fatalf("after empty save, LoadAuthBaseURL = (%q, %v), want empty false", got, ok)
	}
}

func TestAPITokenManager_SaveLoadAndResolveConnectBaseURL(t *testing.T) {
	secrets := &SecretsStore{
		path: t.TempDir() + "/secrets.json",
		data: make(map[string]map[string]string),
	}
	mgr := NewAPITokenManager("", secrets)
	apiURL := "https://portal.kittypaw.app"
	ns := NamespaceForURL(apiURL)

	if err := mgr.SaveConnectBaseURL(apiURL, "https://connect.kittypaw.app/"); err != nil {
		t.Fatal(err)
	}
	got, ok := mgr.LoadConnectBaseURL(apiURL)
	if !ok || got != "https://connect.kittypaw.app" {
		t.Fatalf("LoadConnectBaseURL = (%q, %v), want connect base URL", got, ok)
	}
	if stored, ok := secrets.Get(ns, "connect_base_url"); !ok || stored != "https://connect.kittypaw.app" {
		t.Fatalf("stored connect base URL = (%q, %v), want connect_base_url", stored, ok)
	}
	if resolved := mgr.ResolveConnectBaseURL(apiURL); resolved != "https://connect.kittypaw.app" {
		t.Fatalf("ResolveConnectBaseURL = %q", resolved)
	}

	if err := mgr.SaveConnectBaseURL(apiURL, ""); err != nil {
		t.Fatal(err)
	}
	got, ok = mgr.LoadConnectBaseURL(apiURL)
	if ok || got != "" {
		t.Fatalf("after empty save, LoadConnectBaseURL = (%q, %v), want empty false", got, ok)
	}
}

func TestAPITokenManager_ResolveAPIURL(t *testing.T) {
	secrets := &SecretsStore{
		path: t.TempDir() + "/secrets.json",
		data: make(map[string]map[string]string),
	}
	mgr := NewAPITokenManager("", secrets)

	if got := mgr.ResolveAPIURL(); got != DefaultAPIServerURL {
		t.Fatalf("default ResolveAPIURL = %q", got)
	}
	if err := mgr.SaveTokens("http://localhost:9714/", makeJWT(time.Now().Add(10*time.Minute).Unix()), "refresh"); err != nil {
		t.Fatal(err)
	}
	if got := mgr.ResolveAPIURL(); got != "http://localhost:9714" {
		t.Fatalf("ResolveAPIURL = %q", got)
	}
}

func TestAPITokenManager_ResolveConnectBaseURLFallbacks(t *testing.T) {
	secrets := &SecretsStore{
		path: t.TempDir() + "/secrets.json",
		data: make(map[string]map[string]string),
	}
	mgr := NewAPITokenManager("", secrets)

	if got := mgr.ResolveConnectBaseURL("https://portal.kittypaw.app"); got != "https://connect.kittypaw.app" {
		t.Fatalf("portal fallback = %q", got)
	}
	if got := mgr.ResolveConnectBaseURL("http://localhost:8080"); got != "http://localhost:8080" {
		t.Fatalf("localhost fallback = %q", got)
	}
}

func TestAPITokenManager_SaveLoadAndResolveSpaceBaseURL(t *testing.T) {
	secrets := &SecretsStore{
		path: t.TempDir() + "/secrets.json",
		data: make(map[string]map[string]string),
	}
	mgr := NewAPITokenManager("", secrets)
	apiURL := "https://portal.kittypaw.app"
	ns := NamespaceForURL(apiURL)

	if err := mgr.SaveSpaceBaseURL(apiURL, "https://space.kittypaw.app/"); err != nil {
		t.Fatalf("SaveSpaceBaseURL: %v", err)
	}
	got, ok := mgr.LoadSpaceBaseURL(apiURL)
	if !ok || got != "https://space.kittypaw.app" {
		t.Fatalf("LoadSpaceBaseURL = (%q, %v), want space base URL", got, ok)
	}
	if stored, ok := secrets.Get(ns, "space_base_url"); !ok || stored != "https://space.kittypaw.app" {
		t.Fatalf("stored space_base_url = (%q, %v)", stored, ok)
	}
	if resolved := mgr.ResolveSpaceBaseURL(apiURL); resolved != "https://space.kittypaw.app" {
		t.Fatalf("ResolveSpaceBaseURL = %q", resolved)
	}
	if err := mgr.SaveSpaceBaseURL(apiURL, ""); err != nil {
		t.Fatalf("SaveSpaceBaseURL empty: %v", err)
	}
	got, ok = mgr.LoadSpaceBaseURL(apiURL)
	if ok || got != "" {
		t.Fatalf("after empty save, LoadSpaceBaseURL = (%q, %v), want empty false", got, ok)
	}
}

func TestAPITokenManager_ResolveSpaceBaseURLFallbacks(t *testing.T) {
	secrets := &SecretsStore{
		path: t.TempDir() + "/secrets.json",
		data: make(map[string]map[string]string),
	}
	mgr := NewAPITokenManager("", secrets)

	if got := mgr.ResolveSpaceBaseURL("https://portal.kittypaw.app"); got != "https://space.kittypaw.app" {
		t.Fatalf("ResolveSpaceBaseURL portal fallback = %q", got)
	}
	if got := mgr.ResolveSpaceBaseURL("http://localhost:8080"); got != "http://localhost:8080" {
		t.Fatalf("ResolveSpaceBaseURL localhost fallback = %q", got)
	}
}

func TestAPITokenManager_SaveLoadAndClearChatRelayDeviceTokens(t *testing.T) {
	secrets := &SecretsStore{
		path: t.TempDir() + "/secrets.json",
		data: make(map[string]map[string]string),
	}
	mgr := NewAPITokenManager("", secrets)
	apiURL := "https://portal.kittypaw.app"

	tokens := ChatRelayDeviceTokens{
		DeviceID:     "dev_123",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
	}
	if err := mgr.SaveChatRelayDeviceTokens(apiURL, tokens); err != nil {
		t.Fatal(err)
	}
	got, ok := mgr.LoadChatRelayDeviceTokens(apiURL)
	if !ok {
		t.Fatal("LoadChatRelayDeviceTokens ok = false, want true")
	}
	if got != tokens {
		t.Fatalf("LoadChatRelayDeviceTokens = %#v, want %#v", got, tokens)
	}

	if err := mgr.ClearChatRelayDeviceTokens(apiURL); err != nil {
		t.Fatal(err)
	}
	got, ok = mgr.LoadChatRelayDeviceTokens(apiURL)
	if ok || got != (ChatRelayDeviceTokens{}) {
		t.Fatalf("after clear = (%#v, %v), want empty false", got, ok)
	}
}

func TestAPITokenManager_PairChatRelayDeviceStoresResponse(t *testing.T) {
	var gotAuth string
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode pair body: %v", err)
		}
		if body["name"] != "m3-enuma" {
			t.Fatalf("pair body name = %#v", body["name"])
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"device_id":"dev_123","device_access_token":"access-1","device_refresh_token":"refresh-1","expires_in":900}`)
	}))
	defer ts.Close()

	secrets := &SecretsStore{path: t.TempDir() + "/secrets.json", data: make(map[string]map[string]string)}
	mgr := NewAPITokenManager("", secrets)
	apiURL := "https://portal.kittypaw.app"
	got, err := mgr.PairChatRelayDevice(ts.URL, apiURL, "user-access", ChatRelayDevicePairRequest{Name: "m3-enuma"})
	if err != nil {
		t.Fatalf("PairChatRelayDevice: %v", err)
	}
	if gotPath != "/devices/pair" {
		t.Fatalf("path = %q, want /devices/pair", gotPath)
	}
	if gotAuth != "Bearer user-access" {
		t.Fatalf("Authorization = %q, want Bearer user-access", gotAuth)
	}
	want := ChatRelayDeviceTokens{DeviceID: "dev_123", AccessToken: "access-1", RefreshToken: "refresh-1"}
	if got != want {
		t.Fatalf("tokens = %#v, want %#v", got, want)
	}
	stored, ok := mgr.LoadChatRelayDeviceTokens(apiURL)
	if !ok || stored != want {
		t.Fatalf("stored tokens = (%#v, %v), want %#v true", stored, ok, want)
	}
}

func TestAPITokenManager_RefreshChatRelayDeviceTokenRotatesStoredTokens(t *testing.T) {
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
		fmt.Fprint(w, `{"device_access_token":"access-2","device_refresh_token":"refresh-2","expires_in":900}`)
	}))
	defer ts.Close()

	secrets := &SecretsStore{path: t.TempDir() + "/secrets.json", data: make(map[string]map[string]string)}
	mgr := NewAPITokenManager("", secrets)
	apiURL := "https://portal.kittypaw.app"
	if err := mgr.SaveChatRelayDeviceTokens(apiURL, ChatRelayDeviceTokens{
		DeviceID:     "dev_123",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := mgr.RefreshChatRelayDeviceToken(ts.URL, apiURL)
	if err != nil {
		t.Fatalf("RefreshChatRelayDeviceToken: %v", err)
	}
	if gotPath != "/devices/refresh" {
		t.Fatalf("path = %q, want /devices/refresh", gotPath)
	}
	if gotRefresh != "refresh-1" {
		t.Fatalf("refresh_token = %q, want refresh-1", gotRefresh)
	}
	want := ChatRelayDeviceTokens{DeviceID: "dev_123", AccessToken: "access-2", RefreshToken: "refresh-2"}
	if got != want {
		t.Fatalf("tokens = %#v, want %#v", got, want)
	}
	stored, ok := mgr.LoadChatRelayDeviceTokens(apiURL)
	if !ok || stored != want {
		t.Fatalf("stored tokens = (%#v, %v), want %#v true", stored, ok, want)
	}
}

func TestAPITokenManager_EnsureChatRelayDeviceAccessTokenRefreshesExpiredAccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/devices/refresh" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"device_access_token":%q,"device_refresh_token":"refresh-2","expires_in":900}`, makeJWT(time.Now().Add(10*time.Minute).Unix()))
	}))
	defer ts.Close()

	secrets := &SecretsStore{path: t.TempDir() + "/secrets.json", data: make(map[string]map[string]string)}
	mgr := NewAPITokenManager("", secrets)
	apiURL := "https://portal.kittypaw.app"
	if err := mgr.SaveChatRelayDeviceTokens(apiURL, ChatRelayDeviceTokens{
		DeviceID:     "dev_123",
		AccessToken:  makeJWT(time.Now().Add(-1 * time.Minute).Unix()),
		RefreshToken: "refresh-1",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := mgr.EnsureChatRelayDeviceAccessToken(ts.URL, apiURL)
	if err != nil {
		t.Fatalf("EnsureChatRelayDeviceAccessToken: %v", err)
	}
	if got.DeviceID != "dev_123" || got.RefreshToken != "refresh-2" {
		t.Fatalf("tokens = %#v, want retained device id and rotated refresh", got)
	}
}

func TestAPITokenManager_ChatRelayDeviceAccessTokenExpired(t *testing.T) {
	secrets := &SecretsStore{path: t.TempDir() + "/secrets.json", data: make(map[string]map[string]string)}
	mgr := NewAPITokenManager("", secrets)
	apiURL := "https://portal.kittypaw.app"
	if _, ok := mgr.ChatRelayDeviceAccessTokenExpired(apiURL); ok {
		t.Fatal("status ok = true without stored tokens")
	}
	if err := mgr.SaveChatRelayDeviceTokens(apiURL, ChatRelayDeviceTokens{
		DeviceID:     "dev_123",
		AccessToken:  makeJWT(time.Now().Add(-1 * time.Minute).Unix()),
		RefreshToken: "refresh-1",
	}); err != nil {
		t.Fatal(err)
	}
	expired, ok := mgr.ChatRelayDeviceAccessTokenExpired(apiURL)
	if !ok || !expired {
		t.Fatalf("expired status = (%v, %v), want true true", expired, ok)
	}
}

func TestAPITokenManager_LoadKakaoRelayBaseURL_ReadsKakaoRelayURLSecret(t *testing.T) {
	secrets := &SecretsStore{
		path: t.TempDir() + "/secrets.json",
		data: make(map[string]map[string]string),
	}
	mgr := NewAPITokenManager("", secrets)
	apiURL := "http://localhost:8080"
	ns := NamespaceForURL(apiURL)

	if err := secrets.Set(ns, "kakao_relay_url", "https://kakao.kittypaw.app"); err != nil {
		t.Fatal(err)
	}

	got, ok := mgr.LoadKakaoRelayBaseURL(apiURL)
	if !ok || got != "https://kakao.kittypaw.app" {
		t.Fatalf("LoadKakaoRelayBaseURL = (%q, %v), want kakao_relay_url value", got, ok)
	}
}

func TestAPITokenManager_ClearTokens(t *testing.T) {
	secrets := &SecretsStore{
		path: t.TempDir() + "/secrets.json",
		data: make(map[string]map[string]string),
	}
	mgr := NewAPITokenManager("", secrets)

	validToken := makeJWT(time.Now().Add(10 * time.Minute).Unix())
	mgr.SaveTokens("http://localhost:8080", validToken, "refresh")
	mgr.ClearTokens("http://localhost:8080")

	got, err := mgr.LoadAccessToken("http://localhost:8080")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("expected empty after clear, got %q", got)
	}
}

// TestAPITokenManager_PerAccount_LoginToDaemonRoundTrip pins the contract
// that a login flow's write (per-account secrets) is visible to a
// separately constructed APITokenManager reading from the same
// per-account store — the inverse of the bug where login wrote globally
// and the server read per-account. The asymmetry guard asserts no global
// secrets file is produced.
func TestAPITokenManager_PerAccount_LoginToDaemonRoundTrip(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)

	apiURL := "http://localhost:8080"
	validToken := makeJWT(time.Now().Add(10 * time.Minute).Unix())

	// Login simulation — writer side.
	writerSecrets, err := LoadAccountSecrets("default")
	if err != nil {
		t.Fatalf("login-side LoadAccountSecrets: %v", err)
	}
	writerMgr := NewAPITokenManager("", writerSecrets)
	if err := writerMgr.SaveTokens(apiURL, validToken, "REFRESH"); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	// Server simulation — independently open the same per-account store.
	readerSecrets, err := LoadAccountSecrets("default")
	if err != nil {
		t.Fatalf("server-side LoadAccountSecrets: %v", err)
	}
	readerMgr := NewAPITokenManager("", readerSecrets)

	got, err := readerMgr.LoadAccessToken(apiURL)
	if err != nil {
		t.Fatalf("LoadAccessToken: %v", err)
	}
	if got != validToken {
		t.Errorf("server-side token = %q, want %q", got, validToken)
	}

	// Asymmetry guard: a global secrets.json must NOT have been created.
	globalPath := filepath.Join(root, "secrets.json")
	if _, err := os.Stat(globalPath); err == nil {
		t.Fatal("global secrets.json must not exist — write should be per-account only")
	}
}
