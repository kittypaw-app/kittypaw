package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
)

func TestRunChatRelayPairStoresDeviceTokens(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))

	var gotAuth string
	var gotName string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/discovery":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"api_base_url":%q,"auth_base_url":%q,"chat_relay_url":"https://chat.kittypaw.app"}`, "http://"+r.Host, "http://"+r.Host+"/auth")
		case "/auth/devices/pair":
			gotAuth = r.Header.Get("Authorization")
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode pair body: %v", err)
			}
			gotName, _ = body["name"].(string)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"device_id":"dev_123","device_access_token":"access-secret","device_refresh_token":"refresh-secret","expires_in":900}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	secrets, err := core.LoadAccountSecrets("alice")
	if err != nil {
		t.Fatal(err)
	}
	mgr := core.NewAPITokenManager("", secrets)
	userAccess := chatRelayTestAccessToken("user-access")
	if err := mgr.SaveTokens(ts.URL, userAccess, "user-refresh"); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		err = runChatRelayPair(&chatRelayFlags{account: "alice", apiURL: ts.URL, name: "m3-enuma"})
	})
	if err != nil {
		t.Fatalf("runChatRelayPair: %v", err)
	}
	if gotAuth != "Bearer "+userAccess {
		t.Fatalf("Authorization = %q, want user access bearer", gotAuth)
	}
	if gotName != "m3-enuma" {
		t.Fatalf("pair name = %q", gotName)
	}
	if !strings.Contains(out, "Hosted chat ready") {
		t.Fatalf("stdout = %q, want hosted chat ready message", out)
	}
	if strings.Contains(out, "dev_123") || strings.Contains(out, "device_id") {
		t.Fatalf("stdout exposed device internals: %q", out)
	}
	secretsAfterPair, err := core.LoadAccountSecrets("alice")
	if err != nil {
		t.Fatal(err)
	}
	tokens, ok := core.NewAPITokenManager("", secretsAfterPair).LoadChatRelayDeviceTokens(ts.URL)
	if !ok || tokens.DeviceID != "dev_123" || tokens.AccessToken != "access-secret" || tokens.RefreshToken != "refresh-secret" {
		t.Fatalf("stored tokens = (%#v, %v), want pair response", tokens, ok)
	}
}

func TestRunChatRelayStatusDoesNotPrintDeviceInternals(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))

	secrets, err := core.LoadAccountSecrets("alice")
	if err != nil {
		t.Fatal(err)
	}
	mgr := core.NewAPITokenManager("", secrets)
	if err := mgr.SaveAuthBaseURL(core.DefaultAPIServerURL, "https://portal.kittypaw.app/auth"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SaveChatRelayURL(core.DefaultAPIServerURL, "https://chat.kittypaw.app"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SaveChatRelayDeviceTokens(core.DefaultAPIServerURL, core.ChatRelayDeviceTokens{
		DeviceID:     "dev_123",
		AccessToken:  chatRelayTestAccessToken("access-secret"),
		RefreshToken: "refresh-secret",
	}); err != nil {
		t.Fatal(err)
	}

	var runErr error
	out := captureStdout(t, func() {
		runErr = runChatRelayStatus(&chatRelayFlags{account: "alice", apiURL: core.DefaultAPIServerURL})
	})
	if runErr != nil {
		t.Fatalf("runChatRelayStatus: %v", runErr)
	}
	for _, want := range []string{"account: alice", "hosted_chat: ready"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout = %q, missing %q", out, want)
		}
	}
	for _, leaked := range []string{"dev_123", "device_id", "access_token", "refresh_token", "refresh-secret", "access-secret"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("status exposed %q in %q", leaked, out)
		}
	}
}

func TestRunChatRelayStatusRecognizesSpaceDiscovery(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))

	secrets, err := core.LoadAccountSecrets("alice")
	if err != nil {
		t.Fatal(err)
	}
	mgr := core.NewAPITokenManager("", secrets)
	if err := mgr.SaveSpaceBaseURL(core.DefaultAPIServerURL, "https://space.kittypaw.app"); err != nil {
		t.Fatal(err)
	}

	var runErr error
	out := captureStdout(t, func() {
		runErr = runChatRelayStatus(&chatRelayFlags{account: "alice", apiURL: core.DefaultAPIServerURL})
	})
	if runErr != nil {
		t.Fatalf("runChatRelayStatus: %v", runErr)
	}
	if !strings.Contains(out, "hosted_chat: login needed") {
		t.Fatalf("stdout = %q, want login needed for Space discovery", out)
	}
}

func TestNewChatRelayCmdIsHiddenAndDoesNotExposeDisconnect(t *testing.T) {
	cmd := newChatRelayCmd()
	if !cmd.Hidden {
		t.Fatal("chat-relay command should be hidden from normal help")
	}
	for _, child := range cmd.Commands() {
		if child.Name() == "disconnect" {
			t.Fatal("chat-relay disconnect should not be exposed")
		}
	}
}
