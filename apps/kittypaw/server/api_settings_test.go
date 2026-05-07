package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
)

type telegramPairingTestChannel struct {
	chatID string
}

func (c *telegramPairingTestChannel) Name() string { return "telegram" }

func (c *telegramPairingTestChannel) Start(ctx context.Context, _ chan<- core.Event) error {
	<-ctx.Done()
	return ctx.Err()
}

func (c *telegramPairingTestChannel) SendResponse(context.Context, string, string, string) error {
	return nil
}

func (c *telegramPairingTestChannel) LastChatID() (string, bool) {
	return c.chatID, c.chatID != ""
}

func TestSettingsLLMUpdatesCompletedAccountConfig(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.LLM.Provider = "anthropic"
	cfg.LLM.APIKey = "old-key"
	cfg.LLM.Model = "old-model"
	srv := newServerWithLocalUserAndConfig(t, "alice", "pw", &cfg)
	cookie := loginSessionCookie(t, srv, "alice", "pw")

	req := httptest.NewRequest(http.MethodPost, "/api/settings/llm", strings.NewReader(`{
		"provider":"local",
		"local_url":"http://localhost:11434/v1",
		"local_model":"llama3.1"
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("settings llm code = %d body=%s", rr.Code, rr.Body.String())
	}

	cfgPath, err := core.ConfigPathForAccount("alice")
	if err != nil {
		t.Fatalf("config path: %v", err)
	}
	written, err := core.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	if written.LLM.Provider != "openai" || written.LLM.APIKey != "" || written.LLM.Model != "llama3.1" {
		t.Fatalf("written LLM = %#v, want local openai-compatible llama3.1 without API key", written.LLM)
	}
	if got := srv.accounts.Session("alice").Config.LLM.Model; got != "llama3.1" {
		t.Fatalf("runtime LLM model = %q, want llama3.1", got)
	}
}

func TestSettingsTelegramUpdatesCompletedAccountConfig(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.LLM.Provider = "anthropic"
	cfg.LLM.APIKey = "configured"
	cfg.LLM.Model = "claude-test"
	srv := newServerWithLocalUserAndConfig(t, "alice", "pw", &cfg)
	cookie := loginSessionCookie(t, srv, "alice", "pw")

	body, err := json.Marshal(map[string]string{
		"bot_token": "123456:ABCDEFGHIJKLMNOPQRSTUVWXYZabcd",
		"chat_id":   "4242",
	})
	if err != nil {
		t.Fatalf("marshal telegram body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/telegram", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("settings telegram code = %d body=%s", rr.Code, rr.Body.String())
	}

	written, err := core.LoadConfig(filepath.Join(srv.accountDepsForID("alice").Account.BaseDir, "config.toml"))
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	if len(written.Channels) != 1 || written.Channels[0].ChannelType != core.ChannelTelegram {
		t.Fatalf("channels = %#v, want one telegram channel", written.Channels)
	}
	if got := written.AllowedChatIDs; len(got) != 1 || got[0] != "4242" {
		t.Fatalf("admin chat IDs = %#v, want [4242]", got)
	}
}

func TestTelegramPairingChatIDUsesActiveChannelLastChatID(t *testing.T) {
	token := "123456:ABCDEFGHIJKLMNOPQRSTUVWXYZabcd"
	cfg := core.DefaultConfig()
	cfg.LLM.Provider = "anthropic"
	cfg.LLM.APIKey = "configured"
	cfg.LLM.Model = "claude-test"
	cfg.Server.APIKey = "api-key"
	cfg.Channels = []core.ChannelConfig{{
		ID:          "telegram",
		ChannelType: core.ChannelTelegram,
		Token:       token,
	}}
	srv := newServerWithLocalUserAndConfig(t, "alice", "pw", &cfg)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv.spawner = NewChannelSpawner(ctx, srv.eventCh)
	if err := srv.spawner.TrySpawn("alice", &telegramPairingTestChannel{chatID: "8172543364"}, cfg.Channels[0]); err != nil {
		t.Fatalf("spawn telegram channel: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/telegram/pairing/chat-id", strings.NewReader(`{
		"account_id":"alice",
		"token":"`+token+`"
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "api-key")
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("pairing chat-id code = %d body=%s", rr.Code, rr.Body.String())
	}

	var body struct {
		Status string `json:"status"`
		ChatID string `json:"chat_id"`
		Source string `json:"source"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "paired" || body.ChatID != "8172543364" || body.Source != "active_channel" {
		t.Fatalf("pairing result = %#v, want paired active_channel chat_id", body)
	}
}

func TestTelegramPairingChatIDAllowsLocalhostActiveChannelWithoutAuth(t *testing.T) {
	token := "123456:ABCDEFGHIJKLMNOPQRSTUVWXYZabcd"
	cfg := core.DefaultConfig()
	cfg.LLM.Provider = "anthropic"
	cfg.LLM.APIKey = "configured"
	cfg.LLM.Model = "claude-test"
	cfg.Server.APIKey = "api-key"
	cfg.Channels = []core.ChannelConfig{{
		ID:          "telegram",
		ChannelType: core.ChannelTelegram,
		Token:       token,
	}}
	srv := newServerWithLocalUserAndConfig(t, "alice", "pw", &cfg)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv.spawner = NewChannelSpawner(ctx, srv.eventCh)
	if err := srv.spawner.TrySpawn("alice", &telegramPairingTestChannel{chatID: "8172543364"}, cfg.Channels[0]); err != nil {
		t.Fatalf("spawn telegram channel: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/telegram/pairing/chat-id", strings.NewReader(`{
		"account_id":"new-account",
		"token":"`+token+`"
	}`))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("pairing chat-id code = %d body=%s", rr.Code, rr.Body.String())
	}

	var body struct {
		Status string `json:"status"`
		ChatID string `json:"chat_id"`
		Source string `json:"source"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "paired" || body.ChatID != "8172543364" || body.Source != "active_channel" {
		t.Fatalf("pairing result = %#v, want paired active_channel chat_id", body)
	}
}

func TestTelegramPairingChatIDFetchesWhenNoActiveChannel(t *testing.T) {
	token := "123456:ABCDEFGHIJKLMNOPQRSTUVWXYZabcd"
	oldFetch := fetchTelegramChatID
	fetchTelegramChatID = func(context.Context, string) (string, error) {
		return "424242", nil
	}
	t.Cleanup(func() { fetchTelegramChatID = oldFetch })

	cfg := core.DefaultConfig()
	cfg.LLM.Provider = "anthropic"
	cfg.LLM.APIKey = "configured"
	cfg.LLM.Model = "claude-test"
	cfg.Server.APIKey = "api-key"
	srv := newServerWithLocalUserAndConfig(t, "alice", "pw", &cfg)

	req := httptest.NewRequest(http.MethodPost, "/api/telegram/pairing/chat-id", strings.NewReader(`{
		"account_id":"alice",
		"token":"`+token+`"
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "api-key")
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("pairing chat-id code = %d body=%s", rr.Code, rr.Body.String())
	}

	var body struct {
		Status string `json:"status"`
		ChatID string `json:"chat_id"`
		Source string `json:"source"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "paired" || body.ChatID != "424242" || body.Source != "telegram_api" {
		t.Fatalf("pairing result = %#v, want paired telegram_api chat_id", body)
	}
}

func TestTelegramPairingChatIDAcceptsNonDefaultAccountAPIKey(t *testing.T) {
	token := "123456:ABCDEFGHIJKLMNOPQRSTUVWXYZabcd"
	aliceCfg := core.DefaultConfig()
	aliceCfg.LLM.Provider = "anthropic"
	aliceCfg.LLM.APIKey = "alice-llm"
	aliceCfg.LLM.Model = "claude-test"
	aliceCfg.Server.APIKey = "alice-api"
	bobCfg := core.DefaultConfig()
	bobCfg.LLM.Provider = "anthropic"
	bobCfg.LLM.APIKey = "bob-llm"
	bobCfg.LLM.Model = "claude-test"
	bobCfg.Server.APIKey = "bob-api"
	bobCfg.Channels = []core.ChannelConfig{{
		ID:          "telegram",
		ChannelType: core.ChannelTelegram,
		Token:       token,
	}}
	srv := newMultiAccountAuthTestServer(t, "alice", map[string]string{
		"alice": "alice-pw",
		"bob":   "bob-pw",
	}, map[string]*core.Config{
		"alice": &aliceCfg,
		"bob":   &bobCfg,
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv.spawner = NewChannelSpawner(ctx, srv.eventCh)
	if err := srv.spawner.TrySpawn("bob", &telegramPairingTestChannel{chatID: "999888"}, bobCfg.Channels[0]); err != nil {
		t.Fatalf("spawn bob telegram channel: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/telegram/pairing/chat-id", strings.NewReader(`{
		"account_id":"bob",
		"token":"`+token+`"
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "bob-api")
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("pairing chat-id code = %d body=%s", rr.Code, rr.Body.String())
	}

	var body struct {
		Status string `json:"status"`
		ChatID string `json:"chat_id"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "paired" || body.ChatID != "999888" {
		t.Fatalf("pairing result = %#v, want bob active chat_id", body)
	}
}

func TestSettingsRejectsBeforeCLISetup(t *testing.T) {
	root := t.TempDir()
	srv := newAuthTestServer(t, root, "alice", &core.Config{})

	req := httptest.NewRequest(http.MethodPost, "/api/settings/llm", strings.NewReader(`{"provider":"local","local_model":"llama3"}`))
	req.RemoteAddr = "127.0.0.1:1234"
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("settings before setup code = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
}

func TestSettingsWorkspacesUseLoggedInAccount(t *testing.T) {
	aliceCfg := core.DefaultConfig()
	aliceCfg.LLM.Provider = "anthropic"
	aliceCfg.LLM.APIKey = "alice-key"
	bobCfg := core.DefaultConfig()
	bobCfg.LLM.Provider = "anthropic"
	bobCfg.LLM.APIKey = "bob-key"
	srv := newMultiAccountAuthTestServer(t, "alice", map[string]string{
		"alice": "alice-pw",
		"bob":   "bob-pw",
	}, map[string]*core.Config{
		"alice": &aliceCfg,
		"bob":   &bobCfg,
	})

	workspaceDir := t.TempDir()
	body, err := json.Marshal(map[string]string{
		"name": "notes",
		"path": workspaceDir,
	})
	if err != nil {
		t.Fatalf("marshal workspace body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/settings/workspaces", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(loginSessionCookie(t, srv, "bob", "bob-pw"))
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("settings workspace create code = %d body=%s", rr.Code, rr.Body.String())
	}
	canonicalWorkspaceDir, err := filepath.EvalSymlinks(filepath.Clean(workspaceDir))
	if err != nil {
		t.Fatalf("canonicalize workspace dir: %v", err)
	}

	bobWorkspaces, err := srv.accountDepsForID("bob").Store.ListWorkspaces()
	if err != nil {
		t.Fatalf("list bob workspaces: %v", err)
	}
	if len(bobWorkspaces) != 1 || bobWorkspaces[0].Name != "notes" || bobWorkspaces[0].RootPath != canonicalWorkspaceDir {
		t.Fatalf("bob workspaces = %#v, want notes at %s", bobWorkspaces, canonicalWorkspaceDir)
	}
	aliceWorkspaces, err := srv.accountDepsForID("alice").Store.ListWorkspaces()
	if err != nil {
		t.Fatalf("list alice workspaces: %v", err)
	}
	if len(aliceWorkspaces) != 0 {
		t.Fatalf("alice workspaces = %#v, want none", aliceWorkspaces)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/settings/workspaces", nil)
	req.AddCookie(loginSessionCookie(t, srv, "bob", "bob-pw"))
	rr = httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("settings workspace list code = %d body=%s", rr.Code, rr.Body.String())
	}
	var listed []struct {
		Name     string `json:"name"`
		RootPath string `json:"root_path"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&listed); err != nil {
		t.Fatalf("decode workspace list: %v", err)
	}
	if len(listed) != 1 || listed[0].Name != "notes" || listed[0].RootPath != canonicalWorkspaceDir {
		t.Fatalf("listed workspaces = %#v, want bob notes workspace", listed)
	}
}

func TestSettingsDirectoriesBrowseListsDirectoriesForLoggedInAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Mkdir(filepath.Join(home, "project-a"), 0o755); err != nil {
		t.Fatalf("mkdir project-a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "notes.txt"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write notes file: %v", err)
	}
	cfg := core.DefaultConfig()
	cfg.LLM.Provider = "anthropic"
	cfg.LLM.APIKey = "alice-key"
	srv := newServerWithLocalUserAndConfig(t, "alice", "pw", &cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/directories?path="+url.QueryEscape(home), nil)
	req.AddCookie(loginSessionCookie(t, srv, "alice", "pw"))
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("settings directory browse code = %d body=%s", rr.Code, rr.Body.String())
	}

	var body struct {
		Path    string `json:"path"`
		Parent  string `json:"parent"`
		Entries []struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode directory browse: %v", err)
	}
	wantPath, err := filepath.EvalSymlinks(filepath.Clean(home))
	if err != nil {
		t.Fatalf("canonical home: %v", err)
	}
	if body.Path != wantPath {
		t.Fatalf("path = %q, want %q", body.Path, wantPath)
	}
	if body.Parent == "" {
		t.Fatal("parent should be set for the selected directory")
	}
	if len(body.Entries) != 1 || body.Entries[0].Name != "project-a" || body.Entries[0].Path != filepath.Join(wantPath, "project-a") {
		t.Fatalf("entries = %#v, want only project-a directory", body.Entries)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/settings/directories?path=relative", nil)
	req.AddCookie(loginSessionCookie(t, srv, "alice", "pw"))
	rr = httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("relative directory browse code = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}
