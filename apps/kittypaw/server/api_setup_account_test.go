package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
)

func TestSetupStatusUsesLoggedInAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	aliceCfg := core.DefaultConfig()
	bobCfg := core.DefaultConfig()
	bobCfg.LLM.Models = []core.ModelConfig{{
		ID:       "main",
		Provider: "openai",
		Model:    "llama3",
		BaseURL:  "http://localhost:11434/v1/chat/completions",
	}}
	bobCfg.Channels = []core.ChannelConfig{{ChannelType: core.ChannelTelegram, Token: "123456:ABCDEF"}}
	srv := newMultiAccountAuthTestServer(t, "alice", map[string]string{
		"alice": "alice-pw",
		"bob":   "bob-pw",
	}, map[string]*core.Config{
		"alice": &aliceCfg,
		"bob":   &bobCfg,
	})
	if err := srv.accountDepsForID("bob").Store.SetUserContext("setup:telegram_chat_id", "998877", "setup"); err != nil {
		t.Fatalf("set bob pending telegram chat id: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	req.AddCookie(loginSessionCookie(t, srv, "bob", "bob-pw"))
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status code = %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		ExistingProvider     *string `json:"existing_provider"`
		HasTelegram          bool    `json:"has_telegram"`
		TelegramChatID       *string `json:"telegram_chat_id"`
		DefaultWorkspacePath string  `json:"default_workspace_path"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if body.ExistingProvider == nil || *body.ExistingProvider != "local" {
		t.Fatalf("existing_provider = %v, want local", body.ExistingProvider)
	}
	if !body.HasTelegram {
		t.Fatal("has_telegram = false, want true from bob config")
	}
	if body.TelegramChatID == nil || *body.TelegramChatID != "***8877" {
		t.Fatalf("telegram_chat_id = %v, want masked bob pending id", body.TelegramChatID)
	}
	wantWorkspace := filepath.Join(home, "Documents", "kittypaw", "bob")
	if body.DefaultWorkspacePath != wantWorkspace {
		t.Fatalf("default_workspace_path = %q, want %q", body.DefaultWorkspacePath, wantWorkspace)
	}
}

func TestSetupStatusReportsOpenRouterProvider(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.LLM.Models = []core.ModelConfig{{
		ID:         "main",
		Provider:   "openai",
		Model:      core.OpenRouterDefaultModel,
		Credential: "openrouter",
		BaseURL:    core.OpenRouterBaseURL,
	}}
	srv := newServerWithLocalUserAndConfig(t, "alice", "pw", &cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	req.AddCookie(loginSessionCookie(t, srv, "alice", "pw"))
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status code = %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		ExistingProvider *string `json:"existing_provider"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if body.ExistingProvider == nil || *body.ExistingProvider != "openrouter" {
		t.Fatalf("existing_provider = %v, want openrouter", body.ExistingProvider)
	}
}

func TestSetupWorkspaceCreatesDefaultAccountFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	srv := newMultiAccountAuthTestServer(t, "alice", map[string]string{
		"alice": "alice-pw",
		"bob":   "bob-pw",
	}, map[string]*core.Config{
		"alice": {},
		"bob":   {},
	})

	path := filepath.Join(home, "Documents", "kittypaw", "bob")
	reqBody, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/setup/workspace", bytes.NewReader(reqBody))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(loginSessionCookie(t, srv, "bob", "bob-pw"))
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("workspace code = %d body=%s", rr.Code, rr.Body.String())
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("default workspace dir stat = (%v, %v), want existing directory", info, err)
	}
	canonicalPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("canonicalize path: %v", err)
	}
	if got, ok, err := srv.accountDepsForID("bob").Store.GetUserContext("setup:workspace_path"); err != nil || !ok || got != canonicalPath {
		t.Fatalf("stored workspace path = (%q, %v, %v), want %q true nil", got, ok, err, canonicalPath)
	}
}

func TestSetupRejectsUnauthenticatedWhenLocalUsersExist(t *testing.T) {
	srv := newServerWithLocalUser(t, "alice", "pw")

	req := httptest.NewRequest(http.MethodPost, "/api/setup/llm", strings.NewReader(`{"provider":"local","local_url":"http://localhost:11434/v1","local_model":"llama3"}`))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("setup llm unauthenticated code = %d, want 401; body=%s", rr.Code, rr.Body.String())
	}
}

func TestSetupLlmAcceptsOpenAIAndGemini(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		provider string
		model    string
		baseURL  string
		apiKey   string
	}{
		{
			name:     "openai",
			body:     `{"provider":"openai","api_key":"sk-openai"}`,
			provider: "openai",
			model:    core.OpenAIDefaultModel,
			apiKey:   "sk-openai",
		},
		{
			name:     "gemini",
			body:     `{"provider":"gemini","api_key":"sk-gemini"}`,
			provider: "gemini",
			model:    core.GeminiDefaultModel,
			apiKey:   "sk-gemini",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServerWithLocalUser(t, "alice", "pw")
			req := httptest.NewRequest(http.MethodPost, "/api/setup/llm", strings.NewReader(tc.body))
			req.RemoteAddr = "127.0.0.1:1234"
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(loginSessionCookie(t, srv, "alice", "pw"))
			rr := httptest.NewRecorder()
			srv.setupRoutes().ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("setup llm code = %d body=%s", rr.Code, rr.Body.String())
			}
			st := srv.accountDepsForID("alice").Store
			for key, want := range map[string]string{
				"setup:llm_provider": tc.provider,
				"setup:llm_model":    tc.model,
				"setup:llm_base_url": tc.baseURL,
				"setup:llm_api_key":  tc.apiKey,
			} {
				got, ok, err := st.GetUserContext(key)
				if err != nil || !ok || got != want {
					t.Fatalf("%s = (%q, %v, %v), want %q true nil", key, got, ok, err, want)
				}
			}
		})
	}
}

func TestBootstrapAllowsLoggedInNonDefaultAccount(t *testing.T) {
	srv := newMultiAccountAuthTestServer(t, "alice", map[string]string{
		"alice": "alice-pw",
		"bob":   "bob-pw",
	}, map[string]*core.Config{
		"alice": {},
		"bob":   {},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Host = "127.0.0.1:1234"
	req.AddCookie(loginSessionCookie(t, srv, "bob", "bob-pw"))
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("bootstrap code = %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		APIKey string `json:"api_key"`
		WSURL  string `json:"ws_url"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode bootstrap: %v", err)
	}
	if body.APIKey != "" {
		t.Fatalf("non-default bootstrap api_key = %q, want empty", body.APIKey)
	}
	if body.WSURL == "" {
		t.Fatal("non-default bootstrap ws_url is empty")
	}
}

func TestSetupKakaoRegisterUsesLoggedInAccountSecrets(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	srv := newMultiAccountAuthTestServerWithRoot(t, root, "alice", map[string]string{
		"alice": "alice-pw",
		"bob":   "bob-pw",
	}, map[string]*core.Config{
		"alice": {},
		"bob":   {},
	})
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/register" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"relay-token","pair_code":"123456","channel_url":"https://example.com/channel"}`))
	}))
	t.Cleanup(relay.Close)

	bobSecrets, err := core.LoadAccountSecrets("bob")
	if err != nil {
		t.Fatalf("load bob secrets: %v", err)
	}
	bobMgr := core.NewAPITokenManager("", bobSecrets)
	if err := bobMgr.SaveKakaoRelayBaseURL(core.DefaultAPIServerURL, relay.URL); err != nil {
		t.Fatalf("save bob relay URL: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/setup/kakao/register", nil)
	req.AddCookie(loginSessionCookie(t, srv, "bob", "bob-pw"))
	req.RemoteAddr = "127.0.0.1:1234"
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("kakao register code = %d body=%s", rr.Code, rr.Body.String())
	}

	bobFresh, err := core.LoadAccountSecrets("bob")
	if err != nil {
		t.Fatalf("reload bob secrets: %v", err)
	}
	if wsURL, ok := core.NewAPITokenManager("", bobFresh).LoadKakaoRelayWSURL(core.DefaultAPIServerURL); !ok || wsURL == "" {
		t.Fatalf("bob Kakao relay URL = (%q, %v), want saved", wsURL, ok)
	}
	aliceFresh, err := core.LoadAccountSecrets("alice")
	if err != nil {
		t.Fatalf("reload alice secrets: %v", err)
	}
	if wsURL, ok := core.NewAPITokenManager("", aliceFresh).LoadKakaoRelayWSURL(core.DefaultAPIServerURL); ok || wsURL != "" {
		t.Fatalf("alice Kakao relay URL = (%q, %v), want empty", wsURL, ok)
	}
}

func TestSetupCompleteWritesLoggedInAccountConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	aliceCfg := core.DefaultConfig()
	aliceCfg.LLM.Provider = "anthropic"
	aliceCfg.LLM.APIKey = "alice-existing"
	bobCfg := core.DefaultConfig()
	srv := newMultiAccountAuthTestServerWithRoot(t, root, "alice", map[string]string{
		"alice": "alice-pw",
		"bob":   "bob-pw",
	}, map[string]*core.Config{
		"alice": &aliceCfg,
		"bob":   &bobCfg,
	})
	for accountID, cfg := range map[string]*core.Config{"alice": &aliceCfg, "bob": &bobCfg} {
		writeConfigForTest(t, filepath.Join(root, "accounts", accountID), cfg)
	}

	cookie := loginSessionCookie(t, srv, "bob", "bob-pw")
	postSetupJSON(t, srv, cookie, "/api/setup/llm", `{"provider":"local","local_url":"http://localhost:11434/v1","local_model":"llama3"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/setup/complete", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("complete code = %d body=%s", rr.Code, rr.Body.String())
	}
	bobWritten, err := core.LoadConfig(filepath.Join(root, "accounts", "bob", "config.toml"))
	if err != nil {
		t.Fatalf("load bob config: %v", err)
	}
	if bobWritten.LLM.Provider != "openai" || bobWritten.LLM.BaseURL != "http://localhost:11434/v1/chat/completions" {
		t.Fatalf("bob LLM = provider %q base %q, want local/openai config", bobWritten.LLM.Provider, bobWritten.LLM.BaseURL)
	}
	aliceWritten, err := core.LoadConfig(filepath.Join(root, "accounts", "alice", "config.toml"))
	if err != nil {
		t.Fatalf("load alice config: %v", err)
	}
	if aliceWritten.LLM.APIKey != "" {
		t.Fatalf("alice config API key = %q, want empty", aliceWritten.LLM.APIKey)
	}
}

func TestSetupTelegramRejectsDuplicateLiveCredentialAcrossAccounts(t *testing.T) {
	sharedToken := "123456:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	aliceCfg := core.DefaultConfig()
	aliceCfg.Channels = []core.ChannelConfig{{ChannelType: core.ChannelTelegram, Token: sharedToken}}
	bobCfg := core.DefaultConfig()
	srv := newMultiAccountAuthTestServer(t, "alice", map[string]string{
		"alice": "alice-pw",
		"bob":   "bob-pw",
	}, map[string]*core.Config{
		"alice": &aliceCfg,
		"bob":   &bobCfg,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/setup/telegram", strings.NewReader(`{"bot_token":"`+sharedToken+`","chat_id":"999"}`))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(loginSessionCookie(t, srv, "bob", "bob-pw"))
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("telegram setup code = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	if _, ok, _ := srv.accountDepsForID("bob").Store.GetUserContext("setup:telegram_bot_token"); ok {
		t.Fatal("duplicate telegram token was persisted in bob setup store")
	}
}

func TestSetupCompleteRejectsDuplicateStoredTelegramCredential(t *testing.T) {
	sharedToken := "123456:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	aliceCfg := core.DefaultConfig()
	aliceCfg.Channels = []core.ChannelConfig{{ChannelType: core.ChannelTelegram, Token: sharedToken}}
	bobCfg := core.DefaultConfig()
	srv := newMultiAccountAuthTestServerWithRoot(t, root, "alice", map[string]string{
		"alice": "alice-pw",
		"bob":   "bob-pw",
	}, map[string]*core.Config{
		"alice": &aliceCfg,
		"bob":   &bobCfg,
	})
	for accountID, cfg := range map[string]*core.Config{"alice": &aliceCfg, "bob": &bobCfg} {
		writeConfigForTest(t, filepath.Join(root, "accounts", accountID), cfg)
	}
	bobStore := srv.accountDepsForID("bob").Store
	if err := bobStore.SetUserContext("setup:llm_provider", "openai", "setup"); err != nil {
		t.Fatalf("set bob llm provider: %v", err)
	}
	if err := bobStore.SetUserContext("setup:llm_model", "llama3", "setup"); err != nil {
		t.Fatalf("set bob llm model: %v", err)
	}
	if err := bobStore.SetUserContext("setup:llm_base_url", "http://localhost:11434/v1/chat/completions", "setup"); err != nil {
		t.Fatalf("set bob llm base url: %v", err)
	}
	if err := bobStore.SetUserContext("setup:telegram_bot_token", sharedToken, "setup"); err != nil {
		t.Fatalf("set bob telegram token: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/setup/complete", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.AddCookie(loginSessionCookie(t, srv, "bob", "bob-pw"))
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("complete code = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	bobWritten, err := core.LoadConfig(filepath.Join(root, "accounts", "bob", "config.toml"))
	if err != nil {
		t.Fatalf("load bob config: %v", err)
	}
	if len(bobWritten.Channels) != 0 {
		t.Fatalf("bob channels = %#v, want unchanged empty config after rejected setup", bobWritten.Channels)
	}
}

func TestSetupCompleteRejectsDuplicateStoredKakaoRelayURL(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	aliceCfg := core.DefaultConfig()
	aliceCfg.Channels = []core.ChannelConfig{{ChannelType: core.ChannelKakaoTalk}}
	bobCfg := core.DefaultConfig()
	srv := newMultiAccountAuthTestServerWithRoot(t, root, "alice", map[string]string{
		"alice": "alice-pw",
		"bob":   "bob-pw",
	}, map[string]*core.Config{
		"alice": &aliceCfg,
		"bob":   &bobCfg,
	})
	for accountID, cfg := range map[string]*core.Config{"alice": &aliceCfg, "bob": &bobCfg} {
		writeConfigForTest(t, filepath.Join(root, "accounts", accountID), cfg)
		secrets, err := core.LoadAccountSecrets(accountID)
		if err != nil {
			t.Fatalf("load %s secrets: %v", accountID, err)
		}
		mgr := core.NewAPITokenManager("", secrets)
		if err := mgr.SaveKakaoRelayWSURL(core.DefaultAPIServerURL, "wss://relay.example/ws/shared"); err != nil {
			t.Fatalf("save %s kakao relay URL: %v", accountID, err)
		}
	}
	bobStore := srv.accountDepsForID("bob").Store
	if err := bobStore.SetUserContext("setup:llm_provider", "openai", "setup"); err != nil {
		t.Fatalf("set bob llm provider: %v", err)
	}
	if err := bobStore.SetUserContext("setup:llm_model", "llama3", "setup"); err != nil {
		t.Fatalf("set bob llm model: %v", err)
	}
	if err := bobStore.SetUserContext("setup:llm_base_url", "http://localhost:11434/v1/chat/completions", "setup"); err != nil {
		t.Fatalf("set bob llm base url: %v", err)
	}
	if err := bobStore.SetUserContext("setup:kakao_relay_token", "paired", "setup"); err != nil {
		t.Fatalf("set bob kakao relay token: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/setup/complete", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.AddCookie(loginSessionCookie(t, srv, "bob", "bob-pw"))
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("complete code = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
}

func TestSetupCompleteRejectsDuplicateStoredKakaoRelayURLWithCustomAPIServer(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	apiURL := "https://portal.alt.example"
	sharedRelay := "wss://relay.example/ws/custom-shared"
	aliceCfg := core.DefaultConfig()
	aliceCfg.Channels = []core.ChannelConfig{{ChannelType: core.ChannelKakaoTalk}}
	bobCfg := core.DefaultConfig()
	srv := newMultiAccountAuthTestServerWithRoot(t, root, "alice", map[string]string{
		"alice": "alice-pw",
		"bob":   "bob-pw",
	}, map[string]*core.Config{
		"alice": &aliceCfg,
		"bob":   &bobCfg,
	})
	for accountID, cfg := range map[string]*core.Config{"alice": &aliceCfg, "bob": &bobCfg} {
		writeConfigForTest(t, filepath.Join(root, "accounts", accountID), cfg)
		secrets, err := core.LoadAccountSecrets(accountID)
		if err != nil {
			t.Fatalf("load %s secrets: %v", accountID, err)
		}
		mgr := core.NewAPITokenManager("", secrets)
		if err := mgr.SaveKakaoRelayWSURL(apiURL, sharedRelay); err != nil {
			t.Fatalf("save %s kakao relay URL: %v", accountID, err)
		}
		if accountID == "alice" {
			if err := secrets.Set("kittypaw-api", "api_url", apiURL); err != nil {
				t.Fatalf("save alice api url: %v", err)
			}
		}
	}
	bobStore := srv.accountDepsForID("bob").Store
	if err := bobStore.SetUserContext("setup:llm_provider", "openai", "setup"); err != nil {
		t.Fatalf("set bob llm provider: %v", err)
	}
	if err := bobStore.SetUserContext("setup:llm_model", "llama3", "setup"); err != nil {
		t.Fatalf("set bob llm model: %v", err)
	}
	if err := bobStore.SetUserContext("setup:llm_base_url", "http://localhost:11434/v1/chat/completions", "setup"); err != nil {
		t.Fatalf("set bob llm base url: %v", err)
	}
	if err := bobStore.SetUserContext("setup:api_server_url", apiURL, "setup"); err != nil {
		t.Fatalf("set bob api server URL: %v", err)
	}
	if err := bobStore.SetUserContext("setup:kakao_relay_token", "paired", "setup"); err != nil {
		t.Fatalf("set bob kakao relay token: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/setup/complete", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.AddCookie(loginSessionCookie(t, srv, "bob", "bob-pw"))
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("complete code = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
}

func TestSetupCompleteRejectsFamilyAccountChannels(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	aliceCfg := core.DefaultConfig()
	familyCfg := core.DefaultConfig()
	familyCfg.IsShared = true
	srv := newMultiAccountAuthTestServerWithRoot(t, root, "alice", map[string]string{
		"alice":  "alice-pw",
		"family": "family-pw",
	}, map[string]*core.Config{
		"alice":  &aliceCfg,
		"family": &familyCfg,
	})
	for accountID, cfg := range map[string]*core.Config{"alice": &aliceCfg, "family": &familyCfg} {
		writeConfigForTest(t, filepath.Join(root, "accounts", accountID), cfg)
	}
	familyStore := srv.accountDepsForID("family").Store
	if err := familyStore.SetUserContext("setup:llm_provider", "openai", "setup"); err != nil {
		t.Fatalf("set family llm provider: %v", err)
	}
	if err := familyStore.SetUserContext("setup:llm_model", "llama3", "setup"); err != nil {
		t.Fatalf("set family llm model: %v", err)
	}
	if err := familyStore.SetUserContext("setup:llm_base_url", "http://localhost:11434/v1/chat/completions", "setup"); err != nil {
		t.Fatalf("set family llm base url: %v", err)
	}
	if err := familyStore.SetUserContext("setup:telegram_bot_token", "123456:FFFFFFFFFFFFFFFFFFFFFFFFFFFFFF", "setup"); err != nil {
		t.Fatalf("set family telegram token: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/setup/complete", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.AddCookie(loginSessionCookie(t, srv, "family", "family-pw"))
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("complete code = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
}

func TestSetupCompleteRefreshesLoggedInAccountRuntime(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	aliceCfg := core.DefaultConfig()
	bobCfg := core.DefaultConfig()
	srv := newMultiAccountAuthTestServerWithRoot(t, root, "alice", map[string]string{
		"alice": "alice-pw",
		"bob":   "bob-pw",
	}, map[string]*core.Config{
		"alice": &aliceCfg,
		"bob":   &bobCfg,
	})
	for accountID, cfg := range map[string]*core.Config{"alice": &aliceCfg, "bob": &bobCfg} {
		writeConfigForTest(t, filepath.Join(root, "accounts", accountID), cfg)
	}

	originalBobRuntime := srv.accounts.Runtime("bob")
	if originalBobRuntime == nil {
		t.Fatal("bob runtime missing before setup complete")
	}
	originalBobProvider := originalBobRuntime.Provider

	cookie := loginSessionCookie(t, srv, "bob", "bob-pw")
	postSetupJSON(t, srv, cookie, "/api/setup/llm", `{"provider":"local","local_url":"http://localhost:11434/v1","local_model":"llama3"}`)
	postSetupJSON(t, srv, cookie, "/api/setup/telegram", `{"bot_token":"123456:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBB","chat_id":"42"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/setup/complete", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("complete code = %d body=%s", rr.Code, rr.Body.String())
	}

	bobRuntime := srv.accounts.Runtime("bob")
	if bobRuntime == nil {
		t.Fatal("bob runtime missing after setup complete")
	}
	if bobRuntime == originalBobRuntime {
		t.Fatalf("bob runtime pointer was preserved after setup complete: %p", bobRuntime)
	}
	if bobRuntime.Config.LLM.BaseURL != "http://localhost:11434/v1/chat/completions" {
		t.Fatalf("bob runtime config base URL = %q, want generated local URL", bobRuntime.Config.LLM.BaseURL)
	}
	if bobRuntime.Provider == nil {
		t.Fatal("bob runtime provider is nil after setup complete")
	}
	if bobRuntime.Provider == originalBobProvider {
		t.Fatal("bob runtime provider was not refreshed after setup complete")
	}
	if !srv.schedulers.Has("bob") {
		t.Fatal("bob scheduler missing after setup complete")
	}
	if len(bobRuntime.Config.Channels) != 1 || bobRuntime.Config.Channels[0].Token != "123456:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBB" {
		t.Fatalf("bob runtime channels = %#v, want generated telegram channel", bobRuntime.Config.Channels)
	}
	if got := srv.accountDepsForID("bob").Account.Config.LLM.BaseURL; got != "http://localhost:11434/v1/chat/completions" {
		t.Fatalf("bob deps config base URL = %q, want generated local URL", got)
	}
	if got := srv.accountRegistry.Get("bob").Config.LLM.BaseURL; got != "http://localhost:11434/v1/chat/completions" {
		t.Fatalf("bob registry config base URL = %q, want generated local URL", got)
	}

	aliceReload := core.DefaultConfig()
	aliceReload.LLM.Provider = "anthropic"
	aliceReload.LLM.APIKey = "alice-key"
	aliceReload.Channels = []core.ChannelConfig{{ChannelType: core.ChannelTelegram, Token: "123456:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"}}
	writeConfigForTest(t, filepath.Join(root, "accounts", "alice"), &aliceReload)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/reload", nil)
	rr = httptest.NewRecorder()
	srv.handleReload(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("reload code = %d, want 409 after bob runtime snapshot update; body=%s", rr.Code, rr.Body.String())
	}
}

func TestSetupCompleteForDefaultPreservesReloadConfigPointers(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	aliceCfg := core.DefaultConfig()
	srv := newMultiAccountAuthTestServerWithRoot(t, root, "alice", map[string]string{
		"alice": "alice-pw",
	}, map[string]*core.Config{
		"alice": &aliceCfg,
	})
	writeConfigForTest(t, filepath.Join(root, "accounts", "alice"), &aliceCfg)

	cookie := loginSessionCookie(t, srv, "alice", "alice-pw")
	postSetupJSON(t, srv, cookie, "/api/setup/llm", `{"provider":"local","local_url":"http://localhost:11434/v1","local_model":"llama3"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/setup/complete", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("complete code = %d body=%s", rr.Code, rr.Body.String())
	}
	if srv.config != srv.runtime.Config {
		t.Fatal("default server config and session config should share the same pointer after setup")
	}

	reloadCfg := core.DefaultConfig()
	reloadCfg.LLM.Provider = "anthropic"
	reloadCfg.LLM.APIKey = "reloaded-key"
	reloadCfg.LLM.Model = "claude-test"
	writeConfigForTest(t, filepath.Join(root, "accounts", "alice"), &reloadCfg)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/reload", nil)
	rr = httptest.NewRecorder()
	srv.handleReload(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("reload code = %d body=%s", rr.Code, rr.Body.String())
	}
	if srv.runtime.Config.LLM.APIKey != "reloaded-key" {
		t.Fatalf("default runtime config API key = %q, want reloaded-key", srv.runtime.Config.LLM.APIKey)
	}
	if got := srv.accountDepsForID("alice").Account.Config.LLM.APIKey; got != "reloaded-key" {
		t.Fatalf("default deps config API key = %q, want reloaded-key", got)
	}
	if got := srv.accountRegistry.Get("alice").Config.LLM.APIKey; got != "reloaded-key" {
		t.Fatalf("default registry config API key = %q, want reloaded-key", got)
	}
}

func newMultiAccountAuthTestServerWithRoot(t *testing.T, root, defaultAccount string, users map[string]string, cfgs map[string]*core.Config) *Server {
	t.Helper()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	auth := core.NewLocalAuthStore(filepath.Join(root, "accounts"))
	for accountID, password := range users {
		if err := auth.CreateUser(accountID, password); err != nil {
			t.Fatalf("create local auth user %s: %v", accountID, err)
		}
	}
	ids := make([]string, 0, len(users))
	for accountID := range users {
		ids = append(ids, accountID)
	}
	sortStrings(ids)
	deps := make([]*AccountDeps, 0, len(ids))
	for _, accountID := range ids {
		cfg := cfgs[accountID]
		if cfg == nil {
			cfg = &core.Config{}
		}
		deps = append(deps, buildAccountDeps(t, filepath.Join(root, "accounts"), accountID, cfg))
	}
	return NewWithServerConfig(deps, "test", core.TopLevelServerConfig{DefaultAccount: defaultAccount})
}

func sortStrings(values []string) {
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
}

func postSetupJSON(t *testing.T, srv *Server, cookie *http.Cookie, path, body string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("%s code = %d body=%s", path, rr.Code, rr.Body.String())
	}
}
