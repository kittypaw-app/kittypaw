package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jinto/kittypaw/core"
)

// AC-RELOAD-VALIDATION: pin the symmetry contract with StartChannels and
// AddAccount — the reload path MUST run ValidateAccountChannels and
// ValidateTeamSpaceAccounts before any state mutation. These tests cover the
// two classes of invalid config that the validators catch:
//   (1) a new default-account bot_token that collides with a live peer,
//   (2) a default account flipping is_shared=true while still owning channels.
// Both must reject with 409, leave s.config untouched, and NOT call the
// spawner. The happy path round-trips: valid cfg → 200 + swap + Reconcile.

// writeReloadConfig stages a config.toml that core.LoadConfig can parse.
// Sets HOME so ConfigPath resolves into the test dir.
func writeReloadConfig(t *testing.T, cfg core.Config) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	accountCfgDir := filepath.Join(home, ".kittypaw", "accounts", core.DefaultAccountID)
	writeConfigForTest(t, accountCfgDir, &cfg)
}

// newReloadTestServer wires a minimal Server with a counting reloadReconcile
// hook so tests can assert the spawner was (not) called. Returns the server
// plus a pointer the caller can dereference to read the call count.
func newReloadTestServer(t *testing.T, live *core.Config, peers []*core.Account) (*Server, *int32) {
	t.Helper()
	configDir, err := core.ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir: %v", err)
	}
	accountsRoot := filepath.Join(configDir, "accounts")
	deps := make([]*AccountDeps, 0, len(peers)+1)
	defaultID := DefaultAccountID
	seenDefault := false
	for _, peer := range peers {
		if peer == nil {
			continue
		}
		cfg := peer.Config
		if cfg == nil {
			cfg = &core.Config{}
		}
		id := peer.ID
		if id == "" {
			id = DefaultAccountID
		}
		if id == DefaultAccountID {
			seenDefault = true
		}
		deps = append(deps, buildReloadAccountDeps(t, accountsRoot, id, cfg))
	}
	if len(deps) == 0 {
		deps = append(deps, buildReloadAccountDeps(t, accountsRoot, DefaultAccountID, live))
		seenDefault = true
	}
	if !seenDefault && len(deps) == 1 {
		defaultID = deps[0].Account.ID
	}

	var callN int32
	srv := NewWithServerConfig(deps, "test", core.TopLevelServerConfig{DefaultAccount: defaultID})
	srv.reloadReconcile = func(_ string, _ []core.ChannelConfig) error {
		atomic.AddInt32(&callN, 1)
		return nil
	}
	return srv, &callN
}

// TestHandleReload_DuplicateTelegramToken_Rejects locks in the stolen-token
// defense: a reload whose default-account [telegram] token matches a live
// peer's token must 409 before mutating state. Without this check, both
// long-pollers would race getUpdates and silently duplicate or drop messages.
func TestHandleReload_DuplicateTelegramToken_Rejects(t *testing.T) {
	// New cfg (on disk) introduces a duplicate bot_token with the live
	// "alice" account.
	newCfg := core.DefaultConfig()
	newCfg.LLM.Provider = "anthropic"
	newCfg.LLM.APIKey = "new-key"
	newCfg.Channels = []core.ChannelConfig{
		{ID: "telegram", ChannelType: core.ChannelTelegram, Token: "shared-token"},
	}
	writeReloadConfig(t, newCfg)

	// Live state: default has no channels; alice holds "shared-token".
	liveCfg := core.DefaultConfig()
	liveCfg.LLM.APIKey = "old-key"
	peers := []*core.Account{
		{ID: DefaultAccountID, Config: &liveCfg},
		{ID: "alice", Config: &core.Config{
			Channels: []core.ChannelConfig{
				{ChannelType: core.ChannelTelegram, Token: "shared-token"},
			},
		}},
	}
	srv, callN := newReloadTestServer(t, &liveCfg, peers)

	ts := httptest.NewServer(http.HandlerFunc(srv.handleReload))
	defer ts.Close()

	resp, err := http.Post(ts.URL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "channel validation") {
		t.Errorf("body = %q, want 'channel validation' prefix", body)
	}

	// Rollback contract: config pointer unchanged.
	if srv.config.LLM.APIKey != "old-key" {
		t.Errorf("config swapped despite rejection: LLM.APIKey=%q, want 'old-key'", srv.config.LLM.APIKey)
	}
	// Reconcile MUST NOT run — that would spawn the duplicate channel the
	// validator just rejected.
	if n := atomic.LoadInt32(callN); n != 0 {
		t.Errorf("Reconcile called %d times, want 0", n)
	}
}

// TestHandleReload_FamilyWithChannels_Rejects locks in the coordinator-only
// rule for team-space accounts: a reload that flips is_shared=true while still
// declaring [telegram]/[kakao] channels must 409. A team-space account owning
// a chat channel would silently intercept updates meant for the personal
// account that actually owns the real bot_token.
func TestHandleReload_FamilyWithChannels_Rejects(t *testing.T) {
	newCfg := core.DefaultConfig()
	newCfg.LLM.APIKey = "new-key"
	newCfg.IsShared = true
	newCfg.Channels = []core.ChannelConfig{
		{ID: "telegram", ChannelType: core.ChannelTelegram, Token: "family-token"},
	}
	writeReloadConfig(t, newCfg)

	liveCfg := core.DefaultConfig()
	liveCfg.LLM.APIKey = "old-key"
	peers := []*core.Account{
		{ID: DefaultAccountID, Config: &liveCfg},
	}
	srv, callN := newReloadTestServer(t, &liveCfg, peers)

	ts := httptest.NewServer(http.HandlerFunc(srv.handleReload))
	defer ts.Close()

	resp, err := http.Post(ts.URL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "team space validation") {
		t.Errorf("body = %q, want 'team space validation' prefix", body)
	}

	if srv.config.LLM.APIKey != "old-key" {
		t.Errorf("config swapped despite rejection: LLM.APIKey=%q", srv.config.LLM.APIKey)
	}
	if srv.config.IsSharedAccount() {
		t.Errorf("shared flag flipped to true despite rejection")
	}
	if n := atomic.LoadInt32(callN); n != 0 {
		t.Errorf("Reconcile called %d times, want 0", n)
	}
}

func TestHandleReload_UnknownTeamSpaceMember_Rejects(t *testing.T) {
	newCfg := core.DefaultConfig()
	newCfg.LLM.APIKey = "new-key"
	newCfg.IsShared = true
	newCfg.TeamSpace.Members = []string{"ghost"}
	writeReloadConfig(t, newCfg)

	liveCfg := core.DefaultConfig()
	liveCfg.LLM.APIKey = "old-key"
	peers := []*core.Account{
		{ID: DefaultAccountID, Config: &liveCfg},
	}
	srv, callN := newReloadTestServer(t, &liveCfg, peers)

	ts := httptest.NewServer(http.HandlerFunc(srv.handleReload))
	defer ts.Close()

	resp, err := http.Post(ts.URL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "team-space membership validation") {
		t.Errorf("body = %q, want 'team-space membership validation' prefix", body)
	}

	if srv.config.LLM.APIKey != "old-key" {
		t.Errorf("config swapped despite rejection: LLM.APIKey=%q", srv.config.LLM.APIKey)
	}
	if srv.config.IsSharedAccount() {
		t.Errorf("shared flag flipped to true despite rejection")
	}
	if len(srv.config.TeamSpace.Members) != 0 {
		t.Errorf("team-space members swapped despite rejection: %v", srv.config.TeamSpace.Members)
	}
	if peers[0].Config != &liveCfg {
		t.Error("accountList default config pointer changed despite rejection")
	}
	if peers[0].Config.LLM.APIKey != "old-key" {
		t.Errorf("accountList config mutated despite rejection: LLM.APIKey=%q", peers[0].Config.LLM.APIKey)
	}
	if n := atomic.LoadInt32(callN); n != 0 {
		t.Errorf("Reconcile called %d times, want 0", n)
	}
}

// TestHandleReload_SerializesWithAddAccount locks in the AC-RELOAD-VALIDATION
// serialization contract: the entire validate→swap→reconcile sequence runs
// under accountMu, not just the reconcile step. Without this lock the
// adversary sequence is: reload builds a snapshot that does NOT yet contain
// token X → releases accountMu → AddAccount(bob, token=X) acquires accountMu,
// snapshots a default-account channel list that also does not contain X yet,
// passes validation, spawns bob's bot → reload proceeds to swap *s.config
// (now default has X) and reconcile, spawning default's bot → two long-
// pollers race getUpdates on token X. The test proves accountMu cannot be
// acquired from another goroutine while Reconcile is in flight.
func TestHandleReload_SerializesWithAddAccount(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.LLM.APIKey = "ok"
	writeReloadConfig(t, cfg)

	barrier := make(chan struct{})
	started := make(chan struct{})
	srv, _ := newReloadTestServer(t, &cfg, []*core.Account{
		{ID: DefaultAccountID, Config: &cfg},
	})
	srv.reloadReconcile = func(_ string, _ []core.ChannelConfig) error {
		close(started)
		<-barrier
		return nil
	}
	ts := httptest.NewServer(http.HandlerFunc(srv.handleReload))
	defer ts.Close()

	done := make(chan struct{})
	go func() {
		resp, err := http.Post(ts.URL, "application/json", nil)
		if err == nil {
			resp.Body.Close()
		}
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("reconcile hook never ran")
	}

	if srv.accountMu.TryLock() {
		srv.accountMu.Unlock()
		close(barrier)
		<-done
		t.Fatal("accountMu was not held during Reconcile — TOCTOU window open")
	}

	close(barrier)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleReload did not return after barrier released")
	}

	if !srv.accountMu.TryLock() {
		t.Fatal("accountMu not released after handleReload returned")
	}
	srv.accountMu.Unlock()
}

// TestHandleReload_ValidConfig_SwapsAndReconciles is the happy-path baseline
// — validators pass, config swaps, Reconcile fires exactly once. Without
// this case a regression that rejects every reload would still pass the
// two adversarial tests above.
func TestHandleReload_ValidConfig_SwapsAndReconciles(t *testing.T) {
	newCfg := core.DefaultConfig()
	newCfg.LLM.Provider = "anthropic"
	newCfg.LLM.APIKey = "new-key"
	newCfg.LLM.Model = "claude-test"
	writeReloadConfig(t, newCfg)

	liveCfg := core.DefaultConfig()
	liveCfg.LLM.APIKey = "old-key"
	peers := []*core.Account{
		{ID: DefaultAccountID, Config: &liveCfg},
	}
	srv, callN := newReloadTestServer(t, &liveCfg, peers)

	ts := httptest.NewServer(http.HandlerFunc(srv.handleReload))
	defer ts.Close()

	resp, err := http.Post(ts.URL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %q, want 200", resp.StatusCode, body)
	}
	if srv.config.LLM.APIKey != "new-key" {
		t.Errorf("config not swapped: LLM.APIKey=%q, want 'new-key'", srv.config.LLM.APIKey)
	}
	if n := atomic.LoadInt32(callN); n != 1 {
		t.Errorf("Reconcile called %d times, want 1", n)
	}
}

func TestHandleReload_SingleNonDefaultAccount(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)

	newCfg := core.DefaultConfig()
	newCfg.LLM.Provider = "anthropic"
	newCfg.LLM.APIKey = "new-key"
	newCfg.LLM.Model = "claude-test"
	accountDir := filepath.Join(root, "accounts", "alice")
	writeConfigForTest(t, accountDir, &newCfg)

	liveCfg := core.DefaultConfig()
	liveCfg.LLM.APIKey = "old-key"
	var gotReconcileAccount string
	deps := buildReloadAccountDeps(t, filepath.Join(root, "accounts"), "alice", &liveCfg)
	srv := NewWithServerConfig([]*AccountDeps{deps}, "test", core.TopLevelServerConfig{DefaultAccount: "alice"})
	srv.reloadReconcile = func(accountID string, _ []core.ChannelConfig) error {
		gotReconcileAccount = accountID
		return nil
	}

	ts := httptest.NewServer(http.HandlerFunc(srv.handleReload))
	defer ts.Close()

	resp, err := http.Post(ts.URL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %q, want 200", resp.StatusCode, body)
	}
	if srv.config.LLM.APIKey != "new-key" {
		t.Fatalf("config not swapped: LLM.APIKey=%q, want new-key", srv.config.LLM.APIKey)
	}
	if gotReconcileAccount != "alice" {
		t.Fatalf("reconcile account = %q, want alice", gotReconcileAccount)
	}
}
