package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
)

func stubFetchDiscovery(t *testing.T, resp *core.DiscoveryResponse, err error) {
	t.Helper()
	old := fetchDiscovery
	fetchDiscovery = func(string) (*core.DiscoveryResponse, error) {
		return resp, err
	}
	t.Cleanup(func() {
		fetchDiscovery = old
	})
}

func TestApplyDiscoveryStoresChatRelayURL(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/discovery" {
			t.Fatalf("path = %s, want /discovery", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"api_base_url":"https://api.kittypaw.app",
			"auth_base_url":"https://portal.kittypaw.app/auth",
			"connect_base_url":"https://connect.kittypaw.app",
			"home_base_url":"https://home.kittypaw.app",
			"chat_relay_url":"https://chat.kittypaw.app",
			"kakao_relay_url":"https://kakao.kittypaw.app",
			"skills_registry_url":"https://github.com/kittypaw-app/skills"
		}`)
	}))
	defer ts.Close()

	t.Setenv("KITTYPAW_CONFIG_DIR", t.TempDir())
	secrets, err := core.LoadAccountSecrets("alice")
	if err != nil {
		t.Fatalf("LoadAccountSecrets: %v", err)
	}
	mgr := core.NewAPITokenManager("", secrets)

	gotAPIBase := applyDiscovery(ts.URL, mgr)
	if gotAPIBase != "https://api.kittypaw.app" {
		t.Fatalf("applyDiscovery returned API base = %q", gotAPIBase)
	}
	gotChatRelay, ok := mgr.LoadChatRelayURL(ts.URL)
	if !ok || gotChatRelay != "https://chat.kittypaw.app" {
		t.Fatalf("LoadChatRelayURL = (%q, %v), want chat relay URL", gotChatRelay, ok)
	}
	gotAuthBase, ok := mgr.LoadAuthBaseURL(ts.URL)
	if !ok || gotAuthBase != "https://portal.kittypaw.app/auth" {
		t.Fatalf("LoadAuthBaseURL = (%q, %v), want auth base URL", gotAuthBase, ok)
	}
	gotConnectBase, ok := mgr.LoadConnectBaseURL(ts.URL)
	if !ok || gotConnectBase != "https://connect.kittypaw.app" {
		t.Fatalf("LoadConnectBaseURL = (%q, %v), want connect base URL", gotConnectBase, ok)
	}
	gotHomeBase, ok := mgr.LoadHomeBaseURL(ts.URL)
	if !ok || gotHomeBase != "https://home.kittypaw.app" {
		t.Fatalf("LoadHomeBaseURL = (%q, %v), want home base URL", gotHomeBase, ok)
	}
}

func TestApplyDiscoveryFallsBackOnDiscoveryError(t *testing.T) {
	stubFetchDiscovery(t, nil, errors.New("offline"))

	secrets := testSecretsStore(t)
	mgr := core.NewAPITokenManager("", secrets)
	apiURL := "https://portal.kittypaw.app"

	if got := applyDiscovery(apiURL, mgr); got != apiURL {
		t.Fatalf("applyDiscovery returned %q, want fallback %q", got, apiURL)
	}
}

func TestMaybePairChatRelayDevicePairsWhenRelayDiscovered(t *testing.T) {
	var gotAuth string
	var gotName string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/devices/pair" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		gotName, _ = body["name"].(string)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"device_id":"dev_123","device_access_token":"access-1","device_refresh_token":"refresh-1","expires_in":900}`)
	}))
	defer ts.Close()

	secrets := testSecretsStore(t)
	mgr := core.NewAPITokenManager("", secrets)
	apiURL := "https://portal.kittypaw.app"
	if err := mgr.SaveAuthBaseURL(apiURL, ts.URL+"/auth"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SaveChatRelayURL(apiURL, "https://chat.kittypaw.app"); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if paired := maybePairChatRelayDevice(apiURL, mgr, "user-access", &out); !paired {
		t.Fatal("maybePairChatRelayDevice paired = false, want true")
	}
	if gotAuth != "Bearer user-access" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotName == "" {
		t.Fatal("device name must not be empty")
	}
	tokens, ok := mgr.LoadChatRelayDeviceTokens(apiURL)
	if !ok || tokens.DeviceID != "dev_123" || tokens.AccessToken != "access-1" || tokens.RefreshToken != "refresh-1" {
		t.Fatalf("tokens = (%#v, %v), want stored pair response", tokens, ok)
	}
}

func TestMaybePairChatRelayDevicePairsWhenHomeDiscovered(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/devices/pair" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"device_id":"dev_home","device_access_token":"access-home","device_refresh_token":"refresh-home","expires_in":900}`)
	}))
	defer ts.Close()

	secrets := testSecretsStore(t)
	mgr := core.NewAPITokenManager("", secrets)
	apiURL := "https://portal.kittypaw.app"
	if err := mgr.SaveAuthBaseURL(apiURL, ts.URL+"/auth"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SaveHomeBaseURL(apiURL, "https://home.kittypaw.app"); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if paired := maybePairChatRelayDevice(apiURL, mgr, "user-access", &out); !paired {
		t.Fatal("maybePairChatRelayDevice paired = false, want true with Home discovery")
	}
	if gotAuth != "Bearer user-access" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	tokens, ok := mgr.LoadChatRelayDeviceTokens(apiURL)
	if !ok || tokens.DeviceID != "dev_home" || tokens.AccessToken != "access-home" || tokens.RefreshToken != "refresh-home" {
		t.Fatalf("tokens = (%#v, %v), want stored pair response", tokens, ok)
	}
}

func TestMaybePairChatRelayDeviceSkipsAlreadyPaired(t *testing.T) {
	secrets := testSecretsStore(t)
	mgr := core.NewAPITokenManager("", secrets)
	apiURL := "https://portal.kittypaw.app"
	if err := mgr.SaveChatRelayURL(apiURL, "https://chat.kittypaw.app"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SaveChatRelayDeviceTokens(apiURL, core.ChatRelayDeviceTokens{
		DeviceID:     "dev_123",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
	}); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if paired := maybePairChatRelayDevice(apiURL, mgr, "user-access", &out); paired {
		t.Fatal("maybePairChatRelayDevice paired = true, want false for already paired")
	}
	if out.String() != "" {
		t.Fatalf("output = %q, want empty skip", out.String())
	}
}

func TestMaybePairChatRelayDeviceSkipsWhenNoRelayURL(t *testing.T) {
	secrets := testSecretsStore(t)
	mgr := core.NewAPITokenManager("", secrets)

	var out strings.Builder
	if paired := maybePairChatRelayDevice("https://portal.kittypaw.app", mgr, "user-access", &out); paired {
		t.Fatal("maybePairChatRelayDevice paired = true, want false without relay URL")
	}
	if out.String() != "" {
		t.Fatalf("output = %q, want empty skip", out.String())
	}
}

func TestMaybePairChatRelayDeviceWarnsButDoesNotFail(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not ready", http.StatusNotFound)
	}))
	defer ts.Close()

	secrets := testSecretsStore(t)
	mgr := core.NewAPITokenManager("", secrets)
	apiURL := "https://portal.kittypaw.app"
	if err := mgr.SaveAuthBaseURL(apiURL, ts.URL); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SaveChatRelayURL(apiURL, "https://chat.kittypaw.app"); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if paired := maybePairChatRelayDevice(apiURL, mgr, "user-access", &out); paired {
		t.Fatal("maybePairChatRelayDevice paired = true, want false on pair failure")
	}
	if strings.Contains(out.String(), "chat-relay") {
		t.Fatalf("warning exposed internal chat-relay command: %q", out.String())
	}
	if !strings.Contains(out.String(), "Hosted chat setup skipped") {
		t.Fatalf("warning = %q, want hosted chat setup skip message", out.String())
	}
	if _, ok := mgr.LoadChatRelayDeviceTokens(apiURL); ok {
		t.Fatal("device tokens were stored after failed pair")
	}
}
