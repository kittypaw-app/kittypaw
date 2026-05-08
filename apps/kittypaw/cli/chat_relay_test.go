package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/remote/chatrelay"
	"github.com/jinto/kittypaw/server"
)

func TestChatRelayConnectorConfigsRequiresCompleteAccountSecrets(t *testing.T) {
	secrets := testSecretsStore(t)
	mgr := core.NewAPITokenManager("", secrets)
	apiURL := core.DefaultAPIServerURL
	if err := mgr.SaveChatRelayURL(apiURL, "https://chat.kittypaw.app"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SaveChatRelayDeviceID(apiURL, "dev_123"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SaveChatRelayDeviceTokens(apiURL, core.ChatRelayDeviceTokens{
		DeviceID:     "dev_123",
		AccessToken:  chatRelayTestAccessToken("device-token-1"),
		RefreshToken: "refresh-token-1",
	}); err != nil {
		t.Fatal(err)
	}

	got := chatRelayConnectorConfigs([]*server.AccountDeps{
		{
			Account:     &core.Account{ID: "alice"},
			Secrets:     secrets,
			APITokenMgr: mgr,
		},
	}, "0.1.5", false)

	if len(got) != 1 {
		t.Fatalf("connector configs = %d, want 1", len(got))
	}
	cfg := got[0]
	if cfg.RelayURL != "https://chat.kittypaw.app" {
		t.Fatalf("RelayURL = %q", cfg.RelayURL)
	}
	if cfg.DeviceID != "dev_123" {
		t.Fatalf("DeviceID = %q", cfg.DeviceID)
	}
	if cfg.Credential != chatRelayTestAccessToken("device-token-1") {
		t.Fatalf("Credential = %q", cfg.Credential)
	}
	if len(cfg.LocalAccounts) != 1 || cfg.LocalAccounts[0] != "alice" {
		t.Fatalf("LocalAccounts = %#v", cfg.LocalAccounts)
	}
	if cfg.DaemonVersion != "0.1.5" {
		t.Fatalf("DaemonVersion = %q", cfg.DaemonVersion)
	}
	if len(cfg.Capabilities) != 0 {
		t.Fatalf("Capabilities = %#v, want none until operation dispatch is wired", cfg.Capabilities)
	}
}

func TestChatRelayConnectorConfigsPrefersSpaceBaseURL(t *testing.T) {
	secrets := testSecretsStore(t)
	mgr := core.NewAPITokenManager("", secrets)
	apiURL := core.DefaultAPIServerURL
	if err := mgr.SaveChatRelayURL(apiURL, "https://chat.kittypaw.app"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SaveSpaceBaseURL(apiURL, "https://space.kittypaw.app"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SaveChatRelayDeviceTokens(apiURL, core.ChatRelayDeviceTokens{
		DeviceID:     "dev_123",
		AccessToken:  chatRelayTestAccessToken("device-token-1"),
		RefreshToken: "refresh-token-1",
	}); err != nil {
		t.Fatal(err)
	}

	got := chatRelayConnectorConfigs([]*server.AccountDeps{
		{
			Account:     &core.Account{ID: "alice"},
			Secrets:     secrets,
			APITokenMgr: mgr,
		},
	}, "0.1.5", false)

	if len(got) != 1 {
		t.Fatalf("connector configs = %d, want 1", len(got))
	}
	if got[0].RelayURL != "https://space.kittypaw.app" {
		t.Fatalf("RelayURL = %q, want Space base URL", got[0].RelayURL)
	}
}

func TestChatRelayConnectorConfigsReloadFreshSecretsFromDisk(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	account, err := core.InitAccount(filepath.Join(root, "accounts"), "alice", core.AccountOpts{})
	if err != nil {
		t.Fatalf("InitAccount: %v", err)
	}
	staleSecrets, err := core.LoadSecretsFrom(account.SecretsPath())
	if err != nil {
		t.Fatalf("load stale secrets: %v", err)
	}
	staleMgr := core.NewAPITokenManager("", staleSecrets)

	freshSecrets, err := core.LoadSecretsFrom(account.SecretsPath())
	if err != nil {
		t.Fatalf("load fresh secrets: %v", err)
	}
	freshMgr := core.NewAPITokenManager("", freshSecrets)
	if err := freshMgr.SaveSpaceBaseURL(core.DefaultAPIServerURL, "https://space.kittypaw.app"); err != nil {
		t.Fatal(err)
	}
	if err := freshMgr.SaveChatRelayDeviceTokens(core.DefaultAPIServerURL, core.ChatRelayDeviceTokens{
		DeviceID:     "dev_fresh",
		AccessToken:  chatRelayTestAccessToken("fresh-token"),
		RefreshToken: "refresh-fresh",
	}); err != nil {
		t.Fatal(err)
	}

	got := chatRelayConnectorConfigs([]*server.AccountDeps{
		{
			Account:     account,
			Secrets:     staleSecrets,
			APITokenMgr: staleMgr,
		},
	}, "0.5.6", true)

	if len(got) != 1 {
		t.Fatalf("connector configs = %d, want fresh disk config", len(got))
	}
	if got[0].DeviceID != "dev_fresh" || got[0].Credential != chatRelayTestAccessToken("fresh-token") {
		t.Fatalf("connector = %#v, want fresh disk credential", got[0])
	}
}

func TestChatRelayConnectorConfigsAdvertisesDefaultCapabilitiesWhenDispatchIsReady(t *testing.T) {
	secrets := testSecretsStore(t)
	mgr := core.NewAPITokenManager("", secrets)
	apiURL := core.DefaultAPIServerURL
	if err := mgr.SaveChatRelayURL(apiURL, "https://chat.kittypaw.app"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SaveChatRelayDeviceID(apiURL, "dev_123"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SaveChatRelayDeviceTokens(apiURL, core.ChatRelayDeviceTokens{
		DeviceID:     "dev_123",
		AccessToken:  chatRelayTestAccessToken("device-token-1"),
		RefreshToken: "refresh-token-1",
	}); err != nil {
		t.Fatal(err)
	}

	got := chatRelayConnectorConfigs([]*server.AccountDeps{
		{
			Account:     &core.Account{ID: "alice"},
			Secrets:     secrets,
			APITokenMgr: mgr,
		},
	}, "0.1.5", true)

	if len(got) != 1 {
		t.Fatalf("connector configs = %d, want 1", len(got))
	}
	if len(got[0].Capabilities) != 0 {
		t.Fatalf("Capabilities = %#v, want nil/default capabilities", got[0].Capabilities)
	}
	if got[0].Capabilities != nil {
		t.Fatalf("Capabilities = %#v, want nil so protocol defaults are advertised", got[0].Capabilities)
	}
}

func TestChatRelayConnectorConfigsGroupsAccountsForSameDeviceCredential(t *testing.T) {
	aliceSecrets := testSecretsStore(t)
	aliceMgr := core.NewAPITokenManager("", aliceSecrets)
	bobSecrets := testSecretsStore(t)
	bobMgr := core.NewAPITokenManager("", bobSecrets)
	for _, mgr := range []*core.APITokenManager{aliceMgr, bobMgr} {
		if err := mgr.SaveChatRelayURL(core.DefaultAPIServerURL, "https://chat.kittypaw.app"); err != nil {
			t.Fatal(err)
		}
		if err := mgr.SaveChatRelayDeviceID(core.DefaultAPIServerURL, "dev_123"); err != nil {
			t.Fatal(err)
		}
		if err := mgr.SaveChatRelayDeviceTokens(core.DefaultAPIServerURL, core.ChatRelayDeviceTokens{
			DeviceID:     "dev_123",
			AccessToken:  chatRelayTestAccessToken("device-token-1"),
			RefreshToken: "refresh-token-1",
		}); err != nil {
			t.Fatal(err)
		}
	}

	got := chatRelayConnectorConfigs([]*server.AccountDeps{
		{Account: &core.Account{ID: "alice"}, Secrets: aliceSecrets, APITokenMgr: aliceMgr},
		{Account: &core.Account{ID: "bob"}, Secrets: bobSecrets, APITokenMgr: bobMgr},
	}, "0.1.5", false)

	if len(got) != 1 {
		t.Fatalf("connector configs = %d, want one grouped device connector", len(got))
	}
	if want := []string{"alice", "bob"}; !equalStringSlices(got[0].LocalAccounts, want) {
		t.Fatalf("LocalAccounts = %#v, want %#v", got[0].LocalAccounts, want)
	}
}

func TestChatRelayConnectorConfigsSeparatesSameDeviceWithDifferentCredential(t *testing.T) {
	aliceSecrets := testSecretsStore(t)
	aliceMgr := core.NewAPITokenManager("", aliceSecrets)
	bobSecrets := testSecretsStore(t)
	bobMgr := core.NewAPITokenManager("", bobSecrets)
	for _, setup := range []struct {
		mgr        *core.APITokenManager
		credential string
	}{
		{aliceMgr, "device-token-user-1"},
		{bobMgr, "device-token-user-2"},
	} {
		if err := setup.mgr.SaveChatRelayURL(core.DefaultAPIServerURL, "https://chat.kittypaw.app"); err != nil {
			t.Fatal(err)
		}
		if err := setup.mgr.SaveChatRelayDeviceID(core.DefaultAPIServerURL, "dev_123"); err != nil {
			t.Fatal(err)
		}
		if err := setup.mgr.SaveChatRelayDeviceTokens(core.DefaultAPIServerURL, core.ChatRelayDeviceTokens{
			DeviceID:     "dev_123",
			AccessToken:  chatRelayTestAccessToken(setup.credential),
			RefreshToken: "refresh-" + setup.credential,
		}); err != nil {
			t.Fatal(err)
		}
	}

	got := chatRelayConnectorConfigs([]*server.AccountDeps{
		{Account: &core.Account{ID: "alice"}, Secrets: aliceSecrets, APITokenMgr: aliceMgr},
		{Account: &core.Account{ID: "bob"}, Secrets: bobSecrets, APITokenMgr: bobMgr},
	}, "0.1.5", false)

	if len(got) != 2 {
		t.Fatalf("connector configs = %d, want two credentials kept separate", len(got))
	}
}

func TestChatRelayConnectorRuntimeRefreshRotatesGroupedAccounts(t *testing.T) {
	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/devices/refresh" {
			http.NotFound(w, r)
			return
		}
		calls++
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode refresh body: %v", err)
		}
		if body["refresh_token"] != "refresh-1" {
			t.Fatalf("refresh token = %q, want refresh-1", body["refresh_token"])
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"device_access_token":"access-2","device_refresh_token":"refresh-2","expires_in":900}`)
	}))
	defer ts.Close()

	aliceSecrets := testSecretsStore(t)
	aliceMgr := core.NewAPITokenManager("", aliceSecrets)
	bobSecrets := testSecretsStore(t)
	bobMgr := core.NewAPITokenManager("", bobSecrets)
	for _, mgr := range []*core.APITokenManager{aliceMgr, bobMgr} {
		if err := mgr.SaveAuthBaseURL(core.DefaultAPIServerURL, ts.URL); err != nil {
			t.Fatal(err)
		}
		if err := mgr.SaveChatRelayURL(core.DefaultAPIServerURL, "https://chat.kittypaw.app"); err != nil {
			t.Fatal(err)
		}
		if err := mgr.SaveChatRelayDeviceTokens(core.DefaultAPIServerURL, core.ChatRelayDeviceTokens{
			DeviceID:     "dev_123",
			AccessToken:  chatRelayTestAccessToken("access-1"),
			RefreshToken: "refresh-1",
		}); err != nil {
			t.Fatal(err)
		}
	}

	got := chatRelayConnectorRuntimeConfigs([]*server.AccountDeps{
		{Account: &core.Account{ID: "alice"}, Secrets: aliceSecrets, APITokenMgr: aliceMgr},
		{Account: &core.Account{ID: "bob"}, Secrets: bobSecrets, APITokenMgr: bobMgr},
	}, "0.1.5", false)
	if len(got) != 1 {
		t.Fatalf("runtime configs = %d, want one grouped connector", len(got))
	}
	refreshed, err := got[0].RefreshCredential(context.Background())
	if err != nil {
		t.Fatalf("RefreshCredential: %v", err)
	}
	if refreshed != "access-2" {
		t.Fatalf("refreshed credential = %q, want access-2", refreshed)
	}
	if calls != 1 {
		t.Fatalf("refresh calls = %d, want 1", calls)
	}
	for name, mgr := range map[string]*core.APITokenManager{"alice": aliceMgr, "bob": bobMgr} {
		tokens, ok := mgr.LoadChatRelayDeviceTokens(core.DefaultAPIServerURL)
		if !ok || tokens.AccessToken != "access-2" || tokens.RefreshToken != "refresh-2" || tokens.DeviceID != "dev_123" {
			t.Fatalf("%s tokens = (%#v, %v), want rotated tokens", name, tokens, ok)
		}
	}
}

func TestChatRelayConnectorRuntimeConfigsRefreshExpiredCredentialOnceForGroup(t *testing.T) {
	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/devices/refresh" {
			http.NotFound(w, r)
			return
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"device_access_token":%q,"device_refresh_token":"refresh-2","expires_in":900}`, chatRelayTestAccessToken("access-2"))
	}))
	defer ts.Close()

	aliceSecrets := testSecretsStore(t)
	aliceMgr := core.NewAPITokenManager("", aliceSecrets)
	bobSecrets := testSecretsStore(t)
	bobMgr := core.NewAPITokenManager("", bobSecrets)
	for _, mgr := range []*core.APITokenManager{aliceMgr, bobMgr} {
		if err := mgr.SaveAuthBaseURL(core.DefaultAPIServerURL, ts.URL); err != nil {
			t.Fatal(err)
		}
		if err := mgr.SaveChatRelayURL(core.DefaultAPIServerURL, "https://chat.kittypaw.app"); err != nil {
			t.Fatal(err)
		}
		if err := mgr.SaveChatRelayDeviceTokens(core.DefaultAPIServerURL, core.ChatRelayDeviceTokens{
			DeviceID:     "dev_123",
			AccessToken:  chatRelayTestAccessTokenWithExp("access-1", 1),
			RefreshToken: "refresh-1",
		}); err != nil {
			t.Fatal(err)
		}
	}

	got := chatRelayConnectorRuntimeConfigs([]*server.AccountDeps{
		{Account: &core.Account{ID: "alice"}, Secrets: aliceSecrets, APITokenMgr: aliceMgr},
		{Account: &core.Account{ID: "bob"}, Secrets: bobSecrets, APITokenMgr: bobMgr},
	}, "0.1.5", false)
	if len(got) != 1 {
		t.Fatalf("runtime configs = %d, want one grouped connector", len(got))
	}
	if got[0].Config.Credential != chatRelayTestAccessToken("access-2") {
		t.Fatalf("credential was not refreshed before connect")
	}
	if calls != 1 {
		t.Fatalf("refresh calls = %d, want one group-level refresh", calls)
	}
	for name, mgr := range map[string]*core.APITokenManager{"alice": aliceMgr, "bob": bobMgr} {
		tokens, ok := mgr.LoadChatRelayDeviceTokens(core.DefaultAPIServerURL)
		if !ok || tokens.AccessToken != chatRelayTestAccessToken("access-2") || tokens.RefreshToken != "refresh-2" {
			t.Fatalf("%s tokens = (%#v, %v), want group-rotated tokens", name, tokens, ok)
		}
	}
}

func TestChatRelayConnectorRuntimeConfigsDropsInvalidDeviceCredential(t *testing.T) {
	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/devices/refresh" {
			http.NotFound(w, r)
			return
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"invalid refresh token"}`, http.StatusUnauthorized)
	}))
	defer ts.Close()

	secrets := testSecretsStore(t)
	mgr := core.NewAPITokenManager("", secrets)
	apiURL := core.DefaultAPIServerURL
	if err := mgr.SaveAuthBaseURL(apiURL, ts.URL); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SaveSpaceBaseURL(apiURL, "https://space.kittypaw.app"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SaveChatRelayDeviceTokens(apiURL, core.ChatRelayDeviceTokens{
		DeviceID:     "dev_123",
		AccessToken:  chatRelayTestAccessTokenWithExp("expired", 1),
		RefreshToken: "refresh-1",
	}); err != nil {
		t.Fatal(err)
	}

	got := chatRelayConnectorRuntimeConfigs([]*server.AccountDeps{
		{Account: &core.Account{ID: "alice"}, Secrets: secrets, APITokenMgr: mgr},
	}, "0.1.5", false)
	if len(got) != 0 {
		t.Fatalf("runtime configs = %#v, want none after terminal invalid credential", got)
	}
	if calls != 1 {
		t.Fatalf("refresh calls = %d, want 1", calls)
	}
	if tokens, ok := mgr.LoadChatRelayDeviceTokens(apiURL); ok || tokens != (core.ChatRelayDeviceTokens{}) {
		t.Fatalf("stored device tokens = (%#v, %v), want cleared", tokens, ok)
	}
}

func TestChatRelayConnectorRuntimeRefreshMapsInvalidRefreshToTerminalCredential(t *testing.T) {
	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/devices/refresh" {
			http.NotFound(w, r)
			return
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"invalid refresh token"}`, http.StatusUnauthorized)
	}))
	defer ts.Close()

	secrets := testSecretsStore(t)
	mgr := core.NewAPITokenManager("", secrets)
	apiURL := core.DefaultAPIServerURL
	if err := mgr.SaveAuthBaseURL(apiURL, ts.URL); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SaveSpaceBaseURL(apiURL, "https://space.kittypaw.app"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SaveChatRelayDeviceTokens(apiURL, core.ChatRelayDeviceTokens{
		DeviceID:     "dev_123",
		AccessToken:  chatRelayTestAccessToken("access-1"),
		RefreshToken: "refresh-1",
	}); err != nil {
		t.Fatal(err)
	}

	got := chatRelayConnectorRuntimeConfigs([]*server.AccountDeps{
		{Account: &core.Account{ID: "alice"}, Secrets: secrets, APITokenMgr: mgr},
	}, "0.1.5", false)
	if len(got) != 1 {
		t.Fatalf("runtime configs = %d, want connector before relay rejects access token", len(got))
	}
	_, err := got[0].RefreshCredential(context.Background())
	if !errors.Is(err, chatrelay.ErrCredentialInvalid) {
		t.Fatalf("RefreshCredential error = %v, want ErrCredentialInvalid", err)
	}
	if calls != 1 {
		t.Fatalf("refresh calls = %d, want 1", calls)
	}
	if tokens, ok := mgr.LoadChatRelayDeviceTokens(apiURL); ok || tokens != (core.ChatRelayDeviceTokens{}) {
		t.Fatalf("stored device tokens = (%#v, %v), want cleared", tokens, ok)
	}
}

func chatRelayTestAccessToken(subject string) string {
	return chatRelayTestAccessTokenWithExp(subject, 4102444800)
}

func chatRelayTestAccessTokenWithExp(subject string, exp int64) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"sub":%q,"exp":%d}`, subject, exp)))
	return header + "." + payload + ".sig"
}

func TestChatRelayConnectorConfigsSkipsPartialSecrets(t *testing.T) {
	secrets := testSecretsStore(t)
	mgr := core.NewAPITokenManager("", secrets)
	if err := mgr.SaveChatRelayURL(core.DefaultAPIServerURL, "https://chat.kittypaw.app"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SaveChatRelayDeviceID(core.DefaultAPIServerURL, "dev_123"); err != nil {
		t.Fatal(err)
	}

	got := chatRelayConnectorConfigs([]*server.AccountDeps{
		{
			Account:     &core.Account{ID: "alice"},
			Secrets:     secrets,
			APITokenMgr: mgr,
		},
	}, "0.1.5", false)

	if len(got) != 0 {
		t.Fatalf("connector configs = %#v, want none without credential", got)
	}
}

func testSecretsStore(t *testing.T) *core.SecretsStore {
	t.Helper()
	secrets, err := core.LoadSecretsFrom(t.TempDir() + "/secrets.json")
	if err != nil {
		t.Fatal(err)
	}
	return secrets
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
