package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jinto/kittypaw/core"
)

// newServerForAdminTest builds a one-account Server ("default") wired end-to-end
// so setupRoutes() and AddAccount paths are both exercisable. The default
// account intentionally has no channels so peers can be added without
// colliding on bot tokens unless the test explicitly sets one.
func newServerForAdminTest(t *testing.T, accountsRoot string, defaultCh []core.ChannelConfig) *Server {
	t.Helper()
	cfg := &core.Config{}
	cfg.Channels = defaultCh
	deps := buildAccountDeps(t, accountsRoot, DefaultAccountID, cfg)
	srv := New([]*AccountDeps{deps}, "test-admin")
	srv.localAuth = core.NewLocalAuthStore(accountsRoot)
	return srv
}

// stageAccountOnDisk writes a minimum-viable config.toml under accountsRoot/id
// without InitAccount (which also enforces allow-list rules we'd fight in
// tests). AddAccount → OpenAccountDeps reads this config unchanged.
func stageAccountOnDisk(t *testing.T, accountsRoot, id string, isFamily bool, channels []core.ChannelConfig) {
	t.Helper()
	dir := filepath.Join(accountsRoot, id)
	tt := &core.Account{ID: id, BaseDir: dir}
	if err := tt.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs %s: %v", id, err)
	}
	cfg := core.DefaultConfig()
	cfg.LLM.Provider = "anthropic"
	cfg.LLM.APIKey = "test-dummy"
	cfg.LLM.Model = "claude-test"
	cfg.IsFamily = isFamily
	cfg.Channels = channels
	if err := core.WriteConfigAtomic(&cfg, filepath.Join(dir, "config.toml")); err != nil {
		t.Fatalf("write config %s: %v", id, err)
	}
}

// accountForDirectAdd constructs a *core.Account whose Config is already
// populated — lets unit tests call AddAccount without a round-trip through
// config.toml on disk. EnsureDirs is deferred to OpenAccountDeps.
func accountForDirectAdd(accountsRoot, id string, isFamily bool, channels []core.ChannelConfig) *core.Account {
	cfg := core.DefaultConfig()
	cfg.LLM.Provider = "anthropic"
	cfg.LLM.APIKey = "test-dummy"
	cfg.LLM.Model = "claude-test"
	cfg.IsFamily = isFamily
	cfg.Channels = channels
	return &core.Account{
		ID:      id,
		BaseDir: filepath.Join(accountsRoot, id),
		Config:  &cfg,
	}
}

// --- Server.AddAccount (unit) ---

func TestAddAccount_RegistersOnAllThreeStores(t *testing.T) {
	root := t.TempDir()
	srv := newServerForAdminTest(t, root, nil)

	alice := accountForDirectAdd(root, "alice", true, nil)
	if err := srv.AddAccount(alice); err != nil {
		t.Fatalf("AddAccount: %v", err)
	}

	if srv.accounts.Runtime("alice") == nil {
		t.Error("alice missing from AccountRouter after AddAccount")
	}
	if srv.accountRegistry.Get("alice") == nil {
		t.Error("alice missing from AccountRegistry after AddAccount")
	}
	found := false
	for _, peer := range srv.accountList {
		if peer != nil && peer.ID == "alice" {
			found = true
		}
	}
	if !found {
		t.Error("alice missing from accountList after AddAccount")
	}
}

func TestAddAccount_DuplicateReturnsSentinel(t *testing.T) {
	root := t.TempDir()
	srv := newServerForAdminTest(t, root, nil)

	first := accountForDirectAdd(root, "alice", true, nil)
	if err := srv.AddAccount(first); err != nil {
		t.Fatalf("first AddAccount: %v", err)
	}
	second := accountForDirectAdd(root, "alice", true, nil)
	err := srv.AddAccount(second)
	if !errors.Is(err, ErrAccountAlreadyActive) {
		t.Fatalf("want ErrAccountAlreadyActive, got %v", err)
	}
}

// TestAddAccount_StoresDeps guards the close-target wiring: AddAccount must
// retain the *AccountDeps so RemoveAccount can close the SQLite store and
// shut down the MCP registry symmetrically. Without this, every hot-added
// account would leak its deps on removal.
func TestAddAccount_StoresDeps(t *testing.T) {
	root := t.TempDir()
	srv := newServerForAdminTest(t, root, nil)

	alice := accountForDirectAdd(root, "alice", true, nil)
	if err := srv.AddAccount(alice); err != nil {
		t.Fatalf("AddAccount: %v", err)
	}

	srv.accountMu.Lock()
	td, ok := srv.accountDeps["alice"]
	srv.accountMu.Unlock()
	if !ok || td == nil {
		t.Fatalf("accountDeps[alice] missing after AddAccount: ok=%v td=%v", ok, td)
	}
	if td.Account == nil || td.Account.ID != "alice" {
		t.Errorf("stored td points at wrong account: %+v", td.Account)
	}
	if td.Store == nil {
		t.Error("stored td has nil Store — Close would no-op")
	}
}

// TestAddAccount_RollbackRemovesDeps: if AddAccount fails after storing deps,
// the map entry must be cleaned up. Use a team-space-with-channels failure which
// rejects AFTER ValidateAccountID but BEFORE accountDeps insertion — we just
// need to ensure the map never ends up with a dangling entry.
func TestAddAccount_RollbackPreservesDepsMap(t *testing.T) {
	root := t.TempDir()
	srv := newServerForAdminTest(t, root, nil)

	// team-space with channels is rejected by ValidateTeamSpaceAccounts BEFORE OpenAccountDeps
	fam := accountForDirectAdd(root, "family", true, []core.ChannelConfig{
		{ChannelType: core.ChannelTelegram, Token: "f"},
	})
	_ = srv.AddAccount(fam)

	srv.accountMu.Lock()
	_, ok := srv.accountDeps["family"]
	srv.accountMu.Unlock()
	if ok {
		t.Error("accountDeps[family] leaked after rejected AddAccount")
	}
}

func TestAddAccount_NilInputs(t *testing.T) {
	root := t.TempDir()
	srv := newServerForAdminTest(t, root, nil)

	if err := srv.AddAccount(nil); err == nil {
		t.Error("AddAccount(nil): want error, got nil")
	}
	if err := srv.AddAccount(&core.Account{ID: "alice"}); err == nil {
		t.Error("AddAccount with nil Config: want error, got nil")
	}
}

func TestAddAccount_InvalidIDRejected(t *testing.T) {
	root := t.TempDir()
	srv := newServerForAdminTest(t, root, nil)

	bad := accountForDirectAdd(root, "../escape", true, nil)
	if err := srv.AddAccount(bad); err == nil {
		t.Error("AddAccount(../escape): want error, got nil")
	}
}

// TestAddAccount_ChannelCollision exercises the pre-spawn validation path:
// if the would-be account declares a bot token already claimed by a live
// peer, AddAccount must reject and leave every registry untouched.
func TestAddAccount_ChannelCollision(t *testing.T) {
	root := t.TempDir()
	srv := newServerForAdminTest(t, root, []core.ChannelConfig{
		{ChannelType: core.ChannelTelegram, Token: "shared-tok"},
	})

	// "bob" is not family, declares the same telegram token → must fail.
	bob := accountForDirectAdd(root, "bob", false, []core.ChannelConfig{
		{ChannelType: core.ChannelTelegram, Token: "shared-tok"},
	})
	if err := srv.AddAccount(bob); err == nil {
		t.Fatal("expected channel-collision rejection, got nil")
	}

	// Side-effect assertion — no leak across registries.
	if srv.accounts.Runtime("bob") != nil {
		t.Error("bob leaked into AccountRouter after rejected AddAccount")
	}
	if srv.accountRegistry.Get("bob") != nil {
		t.Error("bob leaked into AccountRegistry after rejected AddAccount")
	}
	for _, peer := range srv.accountList {
		if peer != nil && peer.ID == "bob" {
			t.Fatal("bob leaked into accountList after rejected AddAccount")
		}
	}
}

// TestAddAccount_FamilyWithChannelsRejected guards the team-space invariant:
// a hot-added team-space account must never declare channels.
func TestAddAccount_FamilyWithChannelsRejected(t *testing.T) {
	root := t.TempDir()
	srv := newServerForAdminTest(t, root, nil)

	fam := accountForDirectAdd(root, "family", true, []core.ChannelConfig{
		{ChannelType: core.ChannelTelegram, Token: "f"},
	})
	if err := srv.AddAccount(fam); err == nil {
		t.Error("team-space account with channels: want error, got nil")
	}
	if srv.accountRegistry.Get("family") != nil {
		t.Error("family leaked into registry despite validation failure")
	}
}

func TestAddAccount_UnknownTeamSpaceMemberRejectedStateUnchanged(t *testing.T) {
	root := t.TempDir()
	srv := newServerForAdminTest(t, root, nil)
	srv.config.Server.APIKey = "default-key"

	team := accountForDirectAdd(root, "team", true, nil)
	team.Config.TeamSpace.Members = []string{"ghost"}

	err := srv.AddAccount(team)
	if err == nil {
		t.Fatal("expected membership validation error, got nil")
	}
	if !strings.Contains(err.Error(), "team-space membership validation") {
		t.Fatalf("AddAccount error = %v, want team-space membership validation", err)
	}

	if srv.accounts.Runtime("team") != nil {
		t.Error("team leaked into AccountRouter after rejected AddAccount")
	}
	if srv.accountRegistry.Get("team") != nil {
		t.Error("team leaked into AccountRegistry after rejected AddAccount")
	}
	for _, peer := range srv.accountList {
		if peer != nil && peer.ID == "team" {
			t.Fatal("team leaked into accountList after rejected AddAccount")
		}
	}
	srv.accountMu.Lock()
	_, ok := srv.accountDeps["team"]
	srv.accountMu.Unlock()
	if ok {
		t.Error("accountDeps[team] leaked after rejected AddAccount")
	}
	if srv.config.Server.APIKey != "default-key" {
		t.Fatalf("server config mutated after rejected AddAccount: api_key=%q", srv.config.Server.APIKey)
	}
	if _, err := os.Stat(filepath.Join(root, "team", "data")); !os.IsNotExist(err) {
		t.Fatalf("account deps opened before validation; data dir stat err = %v", err)
	}
}

// --- Server.RemoveAccount (unit) ---

// TestRemoveAccount_HappyPath mirrors AddAccount end-to-end: after remove, none
// of the five registries (router, list, registry, deps map, session cache)
// retain the account. Closes AC-RM1 a/b at the server layer (channel-less).
func TestRemoveAccount_HappyPath(t *testing.T) {
	root := t.TempDir()
	srv := newServerForAdminTest(t, root, nil)

	alice := accountForDirectAdd(root, "alice", true, nil)
	if err := srv.AddAccount(alice); err != nil {
		t.Fatalf("AddAccount: %v", err)
	}

	if err := srv.RemoveAccount("alice"); err != nil {
		t.Fatalf("RemoveAccount: %v", err)
	}

	if sess := srv.accounts.Runtime("alice"); sess != nil {
		t.Error("alice still in AccountRouter after RemoveAccount")
	}
	if srv.accountRegistry.Get("alice") != nil {
		t.Error("alice still in AccountRegistry after RemoveAccount")
	}
	for _, peer := range srv.accountList {
		if peer != nil && peer.ID == "alice" {
			t.Fatal("alice still in accountList after RemoveAccount")
		}
	}
	srv.accountMu.Lock()
	_, ok := srv.accountDeps["alice"]
	srv.accountMu.Unlock()
	if ok {
		t.Error("alice still in accountDeps after RemoveAccount")
	}
}

// TestRemoveAccount_NotActive returns a distinct sentinel so the HTTP layer
// can map it to 404 (not 500). AC-RM3 server-side piece — CLI layer handles
// the user-facing message.
func TestRemoveAccount_NotActive(t *testing.T) {
	root := t.TempDir()
	srv := newServerForAdminTest(t, root, nil)

	err := srv.RemoveAccount("zzz")
	if !errors.Is(err, ErrAccountNotActive) {
		t.Fatalf("want ErrAccountNotActive, got %v", err)
	}
}

// TestRemoveAccount_InvalidIDRejectedStateUnchanged guards AC-RM5's spirit:
// any pre-reconcile rejection (here: malformed ID) must leave every registry
// untouched so a retry picks up clean state. Using ValidateAccountID as the
// failure lever because Reconcile's channel-stop path aggregates errors
// internally (slog) rather than propagating them — the "abort before mutate"
// invariant is what matters for AC-RM5.
func TestRemoveAccount_InvalidIDRejectedStateUnchanged(t *testing.T) {
	root := t.TempDir()
	srv := newServerForAdminTest(t, root, nil)

	alice := accountForDirectAdd(root, "alice", true, nil)
	if err := srv.AddAccount(alice); err != nil {
		t.Fatalf("AddAccount: %v", err)
	}

	// Malformed ID rejected at entry — alice untouched.
	if err := srv.RemoveAccount("../escape"); err == nil {
		t.Error("want validation error, got nil")
	}

	if srv.accounts.Runtime("alice") == nil {
		t.Error("alice dropped from router after rejected RemoveAccount")
	}
	if srv.accountRegistry.Get("alice") == nil {
		t.Error("alice dropped from registry after rejected RemoveAccount")
	}
	srv.accountMu.Lock()
	_, ok := srv.accountDeps["alice"]
	srv.accountMu.Unlock()
	if !ok {
		t.Error("alice dropped from accountDeps after rejected RemoveAccount")
	}
}

// --- HTTP handler ---

func postAdminAccount(t *testing.T, srv *Server, accountID, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"account_id": accountID})
	req := httptest.NewRequest("POST", "/api/v1/admin/accounts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remoteAddr
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	return w
}

// deleteAdminAccount posts to POST /api/v1/admin/accounts/{id}/delete so we
// can assert the full Chi route chain (localhost gate + handler + RemoveAccount).
func deleteAdminAccount(t *testing.T, srv *Server, accountID, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/v1/admin/accounts/"+accountID+"/delete", nil)
	req.RemoteAddr = remoteAddr
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)
	return w
}

func TestHandleAdminAccountRemove_Success(t *testing.T) {
	root := t.TempDir()
	srv := newServerForAdminTest(t, root, nil)
	stageAccountOnDisk(t, root, "alice", true, nil)
	if w := postAdminAccount(t, srv, "alice", "127.0.0.1:1"); w.Code != http.StatusOK {
		t.Fatalf("setup: want 200, got %d: %s", w.Code, w.Body.String())
	}

	w := deleteAdminAccount(t, srv, "alice", "127.0.0.1:1")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] != "deactivated" {
		t.Errorf("status = %v, want \"deactivated\"", resp["status"])
	}
	if resp["account_id"] != "alice" {
		t.Errorf("account_id = %v, want \"alice\"", resp["account_id"])
	}
	if srv.accounts.Runtime("alice") != nil {
		t.Error("alice still registered after 200 deactivation")
	}
}

func TestHandleAdminAccountRemove_NotActive404(t *testing.T) {
	root := t.TempDir()
	srv := newServerForAdminTest(t, root, nil)

	w := deleteAdminAccount(t, srv, "ghost", "127.0.0.1:1")
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAdminAccountRemove_InvalidID400(t *testing.T) {
	root := t.TempDir()
	srv := newServerForAdminTest(t, root, nil)

	w := deleteAdminAccount(t, srv, "../escape", "127.0.0.1:1")
	// Chi's URL parameter contains "../escape" URL-decoded — either 400 or 404
	// is acceptable (invalid vs not found). What matters: not 200, not 500.
	if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
		t.Fatalf("want 400 or 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAdminAccountRemove_NonLocalhostRejected(t *testing.T) {
	root := t.TempDir()
	srv := newServerForAdminTest(t, root, nil)
	stageAccountOnDisk(t, root, "alice", true, nil)
	if w := postAdminAccount(t, srv, "alice", "127.0.0.1:1"); w.Code != http.StatusOK {
		t.Fatalf("setup: want 200, got %d", w.Code)
	}

	w := deleteAdminAccount(t, srv, "alice", "10.0.0.5:44444")
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
	}
	if srv.accounts.Runtime("alice") == nil {
		t.Error("alice dropped despite localhost-gate rejection")
	}
}

func TestHandleAdminAccountAdd_Success(t *testing.T) {
	root := t.TempDir()
	srv := newServerForAdminTest(t, root, nil)
	stageAccountOnDisk(t, root, "alice", true, nil)

	w := postAdminAccount(t, srv, "alice", "127.0.0.1:54321")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] != "activated" {
		t.Errorf("status = %v, want \"activated\"", resp["status"])
	}
	if resp["account_id"] != "alice" {
		t.Errorf("account_id = %v, want \"alice\"", resp["account_id"])
	}
	if srv.accounts.Runtime("alice") == nil {
		t.Error("alice not registered after 200")
	}
}

func TestHandleAdminAccountAdd_NotFoundOnDisk(t *testing.T) {
	root := t.TempDir()
	srv := newServerForAdminTest(t, root, nil)

	w := postAdminAccount(t, srv, "ghost", "127.0.0.1:1")
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAdminAccountAdd_BlankAccountID(t *testing.T) {
	root := t.TempDir()
	srv := newServerForAdminTest(t, root, nil)

	w := postAdminAccount(t, srv, "", "127.0.0.1:1")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAdminAccountAdd_InvalidID(t *testing.T) {
	root := t.TempDir()
	srv := newServerForAdminTest(t, root, nil)

	w := postAdminAccount(t, srv, "../escape", "127.0.0.1:1")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAdminAccountAdd_DuplicateReturns409(t *testing.T) {
	root := t.TempDir()
	srv := newServerForAdminTest(t, root, nil)
	stageAccountOnDisk(t, root, "alice", true, nil)

	if w := postAdminAccount(t, srv, "alice", "127.0.0.1:1"); w.Code != http.StatusOK {
		t.Fatalf("first: want 200, got %d: %s", w.Code, w.Body.String())
	}
	w := postAdminAccount(t, srv, "alice", "127.0.0.1:1")
	if w.Code != http.StatusConflict {
		t.Fatalf("second: want 409, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleAdminAccountAdd_NonLocalhostRejected guards the localhost-only
// gate that sits atop the standard /api/v1 API-key check. A request from a
// non-loopback address must be rejected BEFORE AddAccount runs — otherwise a
// stolen API key would give remote account provisioning.
func TestHandleAdminAccountAdd_NonLocalhostRejected(t *testing.T) {
	root := t.TempDir()
	srv := newServerForAdminTest(t, root, nil)
	stageAccountOnDisk(t, root, "alice", true, nil)

	w := postAdminAccount(t, srv, "alice", "10.0.0.5:44444")
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
	}
	if srv.accounts.Runtime("alice") != nil {
		t.Error("alice registered despite localhost-gate rejection")
	}
}

// TestHandleAdminAccountAdd_HotReloadRouterReflectsImmediately is the AC-U3
// end-to-end guard: once POST /api/v1/admin/accounts returns 200, the
// dispatch path must see the new account *without* a server restart. A
// regression here would push every new team-space member through a kill-9 +
// relaunch, which is the exact pain AC-U3 exists to eliminate. The 30s
// budget comes directly from the spec; in practice AddAccount is synchronous
// and completes in milliseconds, so the bounded wait also guards against a
// regression where hot-add silently defers work to a background goroutine.
func TestHandleAdminAccountAdd_HotReloadRouterReflectsImmediately(t *testing.T) {
	root := t.TempDir()
	srv := newServerForAdminTest(t, root, nil)
	stageAccountOnDisk(t, root, "charlie", true, nil)

	// Pre-add: charlie is unknown — Route() must drop with no fallback.
	preDrop := srv.accounts.DropCount()
	if got := srv.accounts.Route(core.Event{
		Type:      core.EventTelegram,
		AccountID: "charlie",
	}); got != nil {
		t.Fatal("pre-add: charlie should drop (no fallback) but Route returned a session")
	}
	if got := srv.accounts.DropCount(); got != preDrop+1 {
		t.Errorf("pre-add DropCount = %d, want %d", got, preDrop+1)
	}

	// Hot-add — enforce the 30s AC-U3 budget on the HTTP round-trip. The
	// goroutine + select pattern also catches the regression where AddAccount
	// blocks indefinitely (e.g. a bad channel spawn waiting on a network
	// dial) instead of returning an error promptly.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- postAdminAccount(t, srv, "charlie", "127.0.0.1:4242")
	}()
	var w *httptest.ResponseRecorder
	select {
	case w = <-done:
	case <-ctx.Done():
		t.Fatal("AddAccount exceeded 30s AC-U3 budget")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("AddAccount HTTP status = %d, want 200: %s", w.Code, w.Body.String())
	}
	t.Logf("AddAccount completed in %v (AC-U3 budget: 30s)", time.Since(start))

	// Post-add: charlie is immediately in the router and fresh events route
	// cleanly without bumping the drop counter.
	if got := srv.accounts.Runtime("charlie"); got == nil {
		t.Fatal("post-add: charlie missing from router — hot-reload failed")
	}
	dropBefore := srv.accounts.DropCount()
	if got := srv.accounts.Route(core.Event{
		Type:      core.EventTelegram,
		AccountID: "charlie",
	}); got == nil {
		t.Fatal("post-add: charlie event dropped — hot-reload did not reach dispatch path")
	}
	if got := srv.accounts.DropCount(); got != dropBefore {
		t.Errorf("post-add: DropCount advanced (%d → %d) — legitimate traffic is being dropped", dropBefore, got)
	}
}
