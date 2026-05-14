package server

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/browser"
	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/engine"
	"github.com/jinto/kittypaw/sandbox"
	"github.com/jinto/kittypaw/store"
)

// TestServer_New_WiresAccountFieldsPerAccount is the TDD lead for PR-1:
// server.New must build one engine.AccountRuntime per account with AccountID,
// AccountRegistry (shared pointer), and Fanout (team-space coordinator only) wired.
// Until this test passes, Plan B's cross-account Share.read + Fanout
// paths are dead code — see Plan C items C9/C11 in TASKS.md.
func TestServer_New_WiresAccountFieldsPerAccount(t *testing.T) {
	root := t.TempDir()

	familyDeps := buildAccountDeps(t, root, "family", &core.Config{IsFamily: true})
	aliceDeps := buildAccountDeps(t, root, "alice", &core.Config{})

	srv := New([]*AccountDeps{familyDeps, aliceDeps}, "test")

	famSess := srv.accounts.Runtime("family")
	if famSess == nil {
		t.Fatal("team-space runtime not registered on AccountRouter")
	}
	aliceSess := srv.accounts.Runtime("alice")
	if aliceSess == nil {
		t.Fatal("alice runtime not registered on AccountRouter")
	}

	// --- AccountID set on each session.
	if famSess.AccountID != "family" {
		t.Errorf("family.AccountID = %q, want %q", famSess.AccountID, "family")
	}
	if aliceSess.AccountID != "alice" {
		t.Errorf("alice.AccountID = %q, want %q", aliceSess.AccountID, "alice")
	}

	// --- Same *core.AccountRegistry pointer on every session.
	if famSess.AccountRegistry == nil {
		t.Fatal("family.AccountRegistry is nil")
	}
	if famSess.AccountRegistry != aliceSess.AccountRegistry {
		t.Errorf("accounts must share one AccountRegistry; got %p vs %p",
			famSess.AccountRegistry, aliceSess.AccountRegistry)
	}

	// --- Fanout: team-space gets it; personal does NOT.
	if famSess.Fanout == nil {
		t.Error("team-space Fanout must be non-nil (Fanout.send/broadcast capability)")
	}
	if aliceSess.Fanout != nil {
		t.Error("personal account.Fanout must be nil (I5 — personal cannot reach personal)")
	}

	// --- Defense in depth: registry.Get resolves both accounts.
	if famSess.AccountRegistry.Get("family") == nil {
		t.Error("registry missing family entry")
	}
	if famSess.AccountRegistry.Get("alice") == nil {
		t.Error("registry missing alice entry")
	}
}

// TestServer_New_LegacySingleAccount_NoFanout enforces backward
// compatibility: a single "default" account (legacy install) boots with
// Fanout=nil and AccountRegistry non-nil. We intentionally do NOT gate
// AccountRegistry on multi-account — personal→family Share.read works
// the same whether there are 1 or N accounts.
func TestServer_New_LegacySingleAccount_NoFanout(t *testing.T) {
	root := t.TempDir()
	defaultDeps := buildAccountDeps(t, root, DefaultAccountID, &core.Config{})

	srv := New([]*AccountDeps{defaultDeps}, "test")

	sess := srv.accounts.Runtime(DefaultAccountID)
	if sess == nil {
		t.Fatal("default runtime not registered")
	}
	if sess.AccountID != DefaultAccountID {
		t.Errorf("AccountID = %q, want %q", sess.AccountID, DefaultAccountID)
	}
	if sess.Fanout != nil {
		t.Error("legacy single-account must have Fanout=nil")
	}
	if sess.AccountRegistry == nil {
		t.Error("AccountRegistry must be non-nil even in single-account mode")
	}
	if ids := srv.accounts.AccountIDs(); len(ids) != 1 || ids[0] != DefaultAccountID {
		t.Errorf("AccountIDs() = %v, want [%s]", ids, DefaultAccountID)
	}
}

func TestApplyAccountConfigForDefaultReplacesAccountRuntimeDeps(t *testing.T) {
	root := t.TempDir()
	cfg := core.DefaultConfig()
	cfg.LLM.APIKey = "old-key"
	cfg.Browser.Enabled = false
	deps := buildAccountDeps(t, root, "alice", &cfg)
	deps.ServiceTokenMgr = core.NewServiceTokenManager(deps.Secrets)
	srv := New([]*AccountDeps{deps}, "test", "alice")
	oldRuntime := srv.runtime
	if oldRuntime == nil {
		t.Fatal("default runtime is nil")
	}
	if oldRuntime.BrowserController == nil {
		t.Fatal("initial BrowserController is nil")
	}
	if oldRuntime.ProjectJobRuntime == nil {
		t.Fatal("initial ProjectJobRuntime is nil")
	}

	freshSecrets, err := core.LoadSecretsFrom(deps.Account.SecretsPath())
	if err != nil {
		t.Fatalf("load fresh secrets: %v", err)
	}
	if err := freshSecrets.Set("llm/anthropic", "api_key", "fresh-key"); err != nil {
		t.Fatalf("save fresh llm secret: %v", err)
	}

	reloadCfg := core.DefaultConfig()
	reloadCfg.Browser.Enabled = true
	srv.accountMu.Lock()
	_, err = srv.applyAccountConfigLocked("alice", &reloadCfg)
	srv.accountMu.Unlock()
	if err != nil {
		t.Fatalf("applyAccountConfigLocked() error = %v", err)
	}

	if srv.runtime == oldRuntime {
		t.Fatal("default account reload should replace the existing runtime pointer")
	}
	if srv.runtime.BrowserController == nil {
		t.Fatal("default runtime BrowserController was not refreshed")
	}
	status, err := srv.runtime.BrowserController.Execute(context.Background(), core.SkillCall{
		SkillName: "Browser",
		Method:    "status",
	})
	if err != nil {
		t.Fatalf("browser status: %v", err)
	}
	if !strings.Contains(status, `"enabled":true`) {
		t.Fatalf("browser status = %s, want enabled true after config update", status)
	}
	if srv.runtime.ServiceTokenMgr == nil {
		t.Fatal("default runtime ServiceTokenMgr was not refreshed")
	}
	if srv.runtime.ProjectJobRuntime == nil {
		t.Fatal("default runtime ProjectJobRuntime was not refreshed")
	}
	if srv.runtime.ProjectJobRuntime != deps.JobRuntime {
		t.Fatal("default runtime ProjectJobRuntime does not match account deps runtime")
	}
	if srv.runtime.Config.LLM.APIKey != "fresh-key" {
		t.Fatalf("default runtime LLM API key = %q, want fresh-key from disk secrets", srv.runtime.Config.LLM.APIKey)
	}
	if srv.runtime.Provider == nil {
		t.Fatal("default runtime Provider was not refreshed")
	}
	if srv.runtime.Sandbox == oldRuntime.Sandbox {
		t.Fatal("default runtime Sandbox was not refreshed")
	}
}

func TestHandleReloadAppliesDefaultRuntimeDeps(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)

	initial := core.DefaultConfig()
	initial.Browser.Enabled = false
	deps := buildAccountDeps(t, filepath.Join(root, "accounts"), "alice", &initial)
	srv := NewWithServerConfig([]*AccountDeps{deps}, "test", core.TopLevelServerConfig{
		DefaultAccount: "alice",
	})
	oldRuntime := srv.runtime
	oldSandbox := oldRuntime.Sandbox

	reloadCfg := core.DefaultConfig()
	reloadCfg.LLM.APIKey = "reload-key"
	reloadCfg.Sandbox.TimeoutSecs = 77
	reloadCfg.Browser.Enabled = true
	writeConfigForTest(t, filepath.Join(root, "accounts", "alice"), &reloadCfg)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/reload", nil)
	rr := httptest.NewRecorder()
	srv.handleReload(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("reload code = %d body=%s", rr.Code, rr.Body.String())
	}
	if srv.runtime == oldRuntime {
		t.Fatal("reload should replace the default runtime")
	}
	if srv.runtime.Provider == nil {
		t.Fatal("reload did not refresh Provider")
	}
	if srv.runtime.Sandbox == oldSandbox {
		t.Fatal("reload did not refresh Sandbox")
	}
	status, err := srv.runtime.BrowserController.Execute(context.Background(), core.SkillCall{
		SkillName: "Browser",
		Method:    "status",
	})
	if err != nil {
		t.Fatalf("browser status: %v", err)
	}
	if !strings.Contains(status, `"enabled":true`) {
		t.Fatalf("browser status = %s, want enabled true after reload", status)
	}
	if srv.config != srv.runtime.Config {
		t.Fatal("server config and default runtime config should share the replaced config pointer")
	}
	if got := srv.accountDepsForID("alice").Account.Config.LLM.APIKey; got != "reload-key" {
		t.Fatalf("deps config API key = %q, want reload-key", got)
	}
	if got := srv.accountRegistry.Get("alice").Config.LLM.APIKey; got != "reload-key" {
		t.Fatalf("registry config API key = %q, want reload-key", got)
	}
}

func TestHandleReloadAppliesNamedAccountRuntimeDeps(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)

	aliceInitial := core.DefaultConfig()
	aliceInitial.LLM.APIKey = "alice-old-key"
	bobInitial := core.DefaultConfig()
	bobInitial.LLM.APIKey = "bob-old-key"
	aliceDeps := buildAccountDeps(t, filepath.Join(root, "accounts"), "alice", &aliceInitial)
	bobDeps := buildAccountDeps(t, filepath.Join(root, "accounts"), "bob", &bobInitial)
	srv := NewWithServerConfig([]*AccountDeps{aliceDeps, bobDeps}, "test", core.TopLevelServerConfig{
		DefaultAccount: "alice",
	})
	oldAliceRuntime := srv.runtime
	oldBobRuntime := srv.accounts.Runtime("bob")
	writeConfigForTest(t, filepath.Join(root, "accounts", "alice"), &aliceInitial)

	bobReload := core.DefaultConfig()
	bobReload.LLM.APIKey = "bob-reloaded-key"
	bobReload.Sandbox.TimeoutSecs = 88
	bobReload.Browser.Enabled = true
	writeConfigForTest(t, filepath.Join(root, "accounts", "bob"), &bobReload)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/reload", bytes.NewBufferString(`{"account_id":"bob"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.handleReload(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("reload code = %d body=%s", rr.Code, rr.Body.String())
	}

	bobRuntime := srv.accounts.Runtime("bob")
	if bobRuntime == nil {
		t.Fatal("bob runtime missing after reload")
	}
	if bobRuntime == oldBobRuntime {
		t.Fatal("named reload should replace bob runtime")
	}
	if srv.runtime != oldAliceRuntime {
		t.Fatal("named reload for bob should not replace default alice runtime")
	}
	if got := bobRuntime.Config.LLM.APIKey; got != "bob-reloaded-key" {
		t.Fatalf("bob runtime API key = %q, want bob-reloaded-key", got)
	}
	if got := srv.accountDepsForID("bob").Account.Config.LLM.APIKey; got != "bob-reloaded-key" {
		t.Fatalf("bob deps config API key = %q, want bob-reloaded-key", got)
	}
	if got := srv.accountRegistry.Get("bob").Config.LLM.APIKey; got != "bob-reloaded-key" {
		t.Fatalf("bob registry config API key = %q, want bob-reloaded-key", got)
	}
}

func TestServerNewConfiguredDefaultAccount(t *testing.T) {
	root := t.TempDir()
	aliceDeps := buildAccountDeps(t, root, "alice", &core.Config{})
	bobDeps := buildAccountDeps(t, root, "bob", &core.Config{})

	srv := NewWithServerConfig([]*AccountDeps{aliceDeps, bobDeps}, "test", core.TopLevelServerConfig{
		DefaultAccount: "bob",
	})

	if got := srv.accounts.Runtime("bob"); got == nil || got != srv.runtime {
		t.Fatalf("default runtime = %p, want bob runtime %p", srv.runtime, got)
	}
	if srv.accountRegistry.DefaultID() != "bob" {
		t.Fatalf("registry default = %q, want bob", srv.accountRegistry.DefaultID())
	}
}

func TestServerNewUsesMasterAPIKey(t *testing.T) {
	root := t.TempDir()
	cfg := core.DefaultConfig()
	cfg.Server.APIKey = "account-key"
	aliceDeps := buildAccountDeps(t, root, "alice", &cfg)

	srv := NewWithServerConfig([]*AccountDeps{aliceDeps}, "test", core.TopLevelServerConfig{
		MasterAPIKey: "master-key",
	})
	srv.localAuth = core.NewLocalAuthStore(filepath.Join(root, "accounts"))
	if got := srv.effectiveAPIKey(); got != "master-key" {
		t.Fatalf("effectiveAPIKey = %q, want master-key", got)
	}

	protected := srv.requireAPIKey(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("x-api-key", "master-key")
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("master key auth status = %d, body = %q", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rec = httptest.NewRecorder()
	srv.handleBootstrap(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d, body = %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"api_key":"master-key"`) {
		t.Fatalf("bootstrap body = %q, want master api key", rec.Body.String())
	}
}

func TestHandleChatAdmissionBusyReturns429(t *testing.T) {
	root := t.TempDir()
	cfg := core.DefaultConfig()
	cfg.Workspace.LiveIndex = false
	cfg.Runtime.MaxConcurrentTurnsPerAccount = 1
	cfg.Runtime.MaxQueuedTurnsPerAccount = 0
	deps := buildAccountDeps(t, root, "alice", &cfg)
	srv := New([]*AccountDeps{deps}, "test", "alice")

	lease, err := srv.defaultRuntime().Admission.Acquire(context.Background(), engine.RuntimeAdmissionRequest{
		AccountID: "alice",
		ScopeKey:  "held",
	})
	if err != nil {
		t.Fatalf("hold admission: %v", err)
	}
	defer lease.Release()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", strings.NewReader(`{"text":"hello","session_id":"s1"}`))
	rec := httptest.NewRecorder()
	srv.handleChat(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, body = %q; want 429", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "runtime busy") {
		t.Fatalf("body = %q, want runtime busy", rec.Body.String())
	}
	if _, err := srv.defaultRuntime().Admission.Acquire(context.Background(), engine.RuntimeAdmissionRequest{AccountID: "alice", ScopeKey: "verify"}); !errors.Is(err, engine.ErrRuntimeAdmissionBusy) {
		t.Fatalf("admission should still be held during assertion, got %v", err)
	}
}

func TestStartChannelsRejectsUnknownTeamSpaceMember(t *testing.T) {
	root := t.TempDir()
	teamDeps := buildAccountDeps(t, root, "team", &core.Config{
		IsShared:  true,
		TeamSpace: core.TeamSpaceConfig{Members: []string{"ghost"}},
	})
	srv := New([]*AccountDeps{teamDeps}, "test")

	err := srv.StartChannels(context.Background())
	if err == nil {
		t.Fatal("expected membership validation error")
	}
	if !strings.Contains(err.Error(), "team-space membership validation") {
		t.Fatalf("error = %v, want membership validation", err)
	}
}

func TestServerNewSingleNonDefaultAccountIsDefault(t *testing.T) {
	root := t.TempDir()
	aliceDeps := buildAccountDeps(t, root, "alice", &core.Config{})

	srv := New([]*AccountDeps{aliceDeps}, "test")

	if got := srv.accounts.Runtime("alice"); got == nil || got != srv.runtime {
		t.Fatalf("default runtime = %p, want alice session %p", srv.runtime, got)
	}
	if srv.accountRegistry.DefaultID() != "alice" {
		t.Fatalf("registry default = %q, want alice", srv.accountRegistry.DefaultID())
	}
}

// buildAccountDeps constructs the minimum set of per-account dependencies
// needed to drive server.New through its wiring path. Store is a real
// tempdir SQLite (server.New calls Store methods during startup);
// Provider is nil because no Run() is executed in these tests.
func buildAccountDeps(t *testing.T, root, id string, cfg *core.Config) *AccountDeps {
	t.Helper()

	baseDir := filepath.Join(root, id)
	account := &core.Account{ID: id, BaseDir: baseDir, Config: cfg}
	if err := account.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs %s: %v", id, err)
	}

	dbPath := filepath.Join(account.DataDir(), "kittypaw.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store %s: %v", dbPath, err)
	}
	t.Cleanup(func() { _ = st.Close() })

	sbox := sandbox.New(core.SandboxConfig{TimeoutSecs: 5})

	secrets, _ := core.LoadSecretsFrom(filepath.Join(baseDir, "secrets.json"))
	saveRuntimeSecretsForTest(t, secrets, cfg)
	core.HydrateRuntimeSecrets(cfg, secrets)
	pkgMgr := core.NewPackageManagerFrom(baseDir, secrets)
	apiTokenMgr := core.NewAPITokenManager(baseDir, secrets)

	return &AccountDeps{
		Account: account,
		Store:   st,
		Sandbox: sbox,
		BrowserController: browser.NewController(browser.ControllerOptions{
			Config:  cfg.Browser,
			BaseDir: baseDir,
		}),
		PkgMgr:      pkgMgr,
		APITokenMgr: apiTokenMgr,
		Secrets:     secrets,
	}
}

func buildReloadAccountDeps(t *testing.T, root, id string, cfg *core.Config) *AccountDeps {
	t.Helper()

	baseDir := filepath.Join(root, id)
	account := &core.Account{ID: id, BaseDir: baseDir, Config: cfg}
	if err := account.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs %s: %v", id, err)
	}

	dbPath := filepath.Join(account.DataDir(), "kittypaw.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store %s: %v", dbPath, err)
	}
	t.Cleanup(func() { _ = st.Close() })

	secrets, err := core.LoadSecretsFrom(account.SecretsPath())
	if err != nil {
		t.Fatalf("load secrets %s: %v", account.SecretsPath(), err)
	}
	return &AccountDeps{
		Account: account,
		Store:   st,
		Sandbox: sandbox.New(cfg.Sandbox),
		BrowserController: browser.NewController(browser.ControllerOptions{
			Config:  cfg.Browser,
			BaseDir: baseDir,
		}),
		PkgMgr:          core.NewPackageManagerFrom(baseDir, secrets),
		APITokenMgr:     core.NewAPITokenManager(baseDir, secrets),
		ServiceTokenMgr: core.NewServiceTokenManager(secrets),
		Secrets:         secrets,
	}
}

func writeConfigForTest(t *testing.T, accountDir string, cfg *core.Config) {
	t.Helper()
	if cfg.IsFamily {
		cfg.IsShared = true
	}
	if err := os.MkdirAll(accountDir, 0o755); err != nil {
		t.Fatalf("mkdir account dir: %v", err)
	}
	secrets, err := core.LoadSecretsFrom(filepath.Join(accountDir, "secrets.json"))
	if err != nil {
		t.Fatalf("load secrets: %v", err)
	}
	saveRuntimeSecretsForTest(t, secrets, cfg)
	if err := core.WriteConfigAtomic(cfg, filepath.Join(accountDir, "config.toml")); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func saveRuntimeSecretsForTest(t *testing.T, secrets *core.SecretsStore, cfg *core.Config) {
	t.Helper()
	if secrets == nil || cfg == nil {
		return
	}
	if cfg.LLM.APIKey != "" {
		credential := cfg.LLM.Provider
		if credential == "" {
			if m := cfg.DefaultModel(); m != nil {
				credential = m.SecretID()
			}
		}
		if credential != "" {
			if err := secrets.Set("llm/"+credential, "api_key", cfg.LLM.APIKey); err != nil {
				t.Fatalf("save llm secret: %v", err)
			}
		}
	}
	if cfg.Server.APIKey != "" {
		if err := secrets.Set("local-server", "api_key", cfg.Server.APIKey); err != nil {
			t.Fatalf("save server api key secret: %v", err)
		}
	}
	for _, ch := range cfg.Channels {
		id := ch.SecretID()
		if id == "" {
			continue
		}
		switch ch.ChannelType {
		case core.ChannelTelegram:
			if ch.Token != "" {
				if err := secrets.Set("channel/"+id, "bot_token", ch.Token); err != nil {
					t.Fatalf("save telegram secret: %v", err)
				}
			}
		case core.ChannelKakaoTalk:
			if ch.KakaoWSURL != "" {
				if err := secrets.Set("channel/"+id, "ws_url", ch.KakaoWSURL); err != nil {
					t.Fatalf("save kakao secret: %v", err)
				}
			}
		}
	}
}
