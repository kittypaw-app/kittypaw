package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"

	"github.com/go-chi/chi/v5"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/engine"
)

// ErrAccountAlreadyActive is returned by AddAccount when an account with
// the same ID is already registered. HTTP callers translate this to
// 409 Conflict — not 500 — because retrying won't help.
var ErrAccountAlreadyActive = errors.New("account already active")

// ErrAccountNotActive is returned by RemoveAccount when the target account
// isn't registered on this server. HTTP callers translate this to 404.
var ErrAccountNotActive = errors.New("account not active")

// AddAccount registers an account that already exists on disk with the
// live server: opens its deps, builds a session, hot-wires it into
// the AccountRegistry / AccountRouter / ChannelSpawner, and spawns its
// channels — all without a server restart. This powers AC-U3 (30-second
// add of a new team-space member).
//
// Invariants (enforced under accountMu so two concurrent admin calls
// can't corrupt state):
//   - Account ID must be fresh (not already in AccountRouter).
//   - Bot-token / Kakao-URL collisions against live accounts are
//     rejected via ValidateAccountChannels BEFORE any channel spawn —
//     otherwise the new channel would silently steal updates.
//   - Team-space accounts must not declare channels (ValidateTeamSpaceAccounts).
//   - Team-space members must resolve to existing accounts.
//   - Any failure after a side-effect (registry, router, accountList,
//     spawner) unwinds every earlier side-effect in reverse order.
//     Deps opened by OpenAccountDeps are also closed on failure.
//
// Scheduling is account-aware: successful activation registers a scheduler
// for the new account and rollback removes it if any later side effect fails.
func (s *Server) AddAccount(t *core.Account) error {
	if t == nil || t.Config == nil {
		return fmt.Errorf("add account: account or config is nil")
	}
	if err := core.ValidateAccountID(t.ID); err != nil {
		return err
	}

	s.accountMu.Lock()
	locked := true
	defer func() {
		if locked {
			s.accountMu.Unlock()
		}
	}()

	if existing := s.accounts.Session(t.ID); existing != nil {
		return fmt.Errorf("%w: %q", ErrAccountAlreadyActive, t.ID)
	}
	if s.isAccountRemovalInProgress(t.ID) {
		return fmt.Errorf("%w: %q", ErrAccountAlreadyActive, t.ID)
	}

	// Build the would-be-final channel map so ValidateAccountChannels sees
	// the incoming account alongside every live one. Without the snapshot
	// a duplicate telegram bot_token would silently spawn a second
	// long-poller that races the original for updates.
	snapshot := make(map[string][]core.ChannelConfig, len(s.accountList)+1)
	for _, peer := range s.accountList {
		if peer == nil || peer.Config == nil {
			continue
		}
		snapshot[peer.ID] = peer.Config.Channels
	}
	snapshot[t.ID] = t.Config.Channels
	if err := core.ValidateAccountChannels(snapshot); err != nil {
		return fmt.Errorf("channel validation: %w", err)
	}
	proposedAccounts := append(append([]*core.Account(nil), s.accountList...), t)
	if err := core.ValidateTeamSpaceAccounts(proposedAccounts); err != nil {
		return fmt.Errorf("team space validation: %w", err)
	}
	if err := core.ValidateTeamSpaceMemberships(proposedAccounts); err != nil {
		return fmt.Errorf("team-space membership validation: %w", err)
	}

	td, err := OpenAccountDeps(t)
	if err != nil {
		return fmt.Errorf("open deps: %w", err)
	}

	// Rollback stack: undo side effects in LIFO order if any later step
	// fails. Each closure is responsible for exactly one revert.
	var rollback []func()
	var rollbackSchedulers []*engine.Scheduler
	rollbackAll := func() {
		for i := len(rollback) - 1; i >= 0; i-- {
			rollback[i]()
		}
	}

	sess := buildAccountSession(td, s.accountRegistry, s.eventCh)

	s.accountRegistry.Register(t)
	rollback = append(rollback, func() { s.accountRegistry.Unregister(t.ID) })

	s.accounts.Register(t.ID, sess)
	rollback = append(rollback, func() { s.accounts.Remove(t.ID) })

	if s.accountDeps == nil {
		s.accountDeps = make(map[string]*AccountDeps)
	}
	s.accountDeps[t.ID] = td
	rollback = append(rollback, func() { delete(s.accountDeps, t.ID) })

	if s.schedulers == nil {
		s.schedulers = NewAccountSchedulers()
	}
	s.schedulers.Register(t.ID, engine.NewScheduler(sess, td.PkgMgr))
	rollback = append(rollback, func() {
		if scheduler := s.schedulers.Detach(t.ID); scheduler != nil {
			scheduler.Stop()
			rollbackSchedulers = append(rollbackSchedulers, scheduler)
		}
	})

	// accountList is read under accountMu by future AddAccount calls for
	// their validation snapshot; StartChannels reads it only at boot (single
	// goroutine) and dispatchLoop does not read it. Append is safe here.
	s.accountList = append(s.accountList, t)
	rollback = append(rollback, func() {
		for i, peer := range s.accountList {
			if peer != nil && peer.ID == t.ID {
				s.accountList = append(s.accountList[:i], s.accountList[i+1:]...)
				return
			}
		}
	})

	if s.spawner != nil && len(t.Config.Channels) > 0 {
		// Reconcile is best-effort: a Phase-2/3 failure leaves earlier phases'
		// channels spawned. Register the cleanup BEFORE the call so a partial
		// success unwinds via Reconcile(_, nil), which drives the same Phase-1
		// path that deletes every (accountID, *) entry.
		rollback = append(rollback, func() {
			if err := s.spawner.Reconcile(t.ID, nil); err != nil {
				slog.Warn("account_add_rollback_spawner_cleanup_failed",
					"account", t.ID, "error", err)
			}
		})
		if err := s.spawner.Reconcile(t.ID, t.Config.Channels); err != nil {
			slog.Error("account_activate_reconcile_failed",
				"account", t.ID, "error", err)
			rollbackAll()
			s.accountMu.Unlock()
			locked = false
			waitSchedulers(rollbackSchedulers)
			_ = td.Close()
			return fmt.Errorf("reconcile channels: %w", err)
		}
	}

	slog.Info("account_activated",
		"account", t.ID,
		"is_shared", t.Config.IsSharedAccount(),
		"channels", len(t.Config.Channels),
	)
	return nil
}

func waitSchedulers(schedulers []*engine.Scheduler) {
	for _, scheduler := range schedulers {
		if scheduler != nil {
			scheduler.Wait()
		}
	}
}

// RemoveAccount is the inverse of AddAccount — it drains the account's
// channels, tears down every registry entry in LIFO order (mirroring
// AddAccount's build order), and finally closes the SQLite store + MCP
// registry held in accountDeps. The filesystem layout is NOT touched; the
// CLI that invoked this RPC owns the disk-side move-to-trash step so the
// server's trust boundary stays clean.
//
// Caller contract: ID must be a live account — unknown IDs return
// ErrAccountNotActive so HTTP callers can respond 404 without retry.
//
// If worker drain fails, RemoveAccount aborts before stopping channel
// pollers or touching any registry — the account stays runnable so the admin
// can retry after investigating (AC-RM5). Reconcile is idempotent, so a
// subsequent call picks up where the first left off.
func (s *Server) RemoveAccount(id string) error {
	if err := core.ValidateAccountID(id); err != nil {
		return err
	}

	s.accountMu.Lock()

	if s.accounts == nil || s.accounts.Session(id) == nil {
		s.accountMu.Unlock()
		return fmt.Errorf("%w: %q", ErrAccountNotActive, id)
	}
	s.setAccountRemovalInProgress(id, true)

	if err := s.stopChannelWorkersForAccount(id); err != nil {
		s.setAccountRemovalInProgress(id, false)
		s.accountMu.Unlock()
		return fmt.Errorf("drain channel workers: %w", err)
	}
	if s.spawner != nil {
		if err := s.spawner.Reconcile(id, nil); err != nil {
			slog.Error("account_remove_reconcile_failed",
				"account", id, "error", err)
			s.setAccountRemovalInProgress(id, false)
			s.accountMu.Unlock()
			return fmt.Errorf("drain channels: %w", err)
		}
	}

	td := s.accountDeps[id]
	var scheduler *engine.Scheduler
	if s.schedulers != nil {
		scheduler = s.schedulers.Detach(id)
	}
	scrubLiveTeamSpaceMembership(s.accountList, id)
	s.accounts.Remove(id)
	for i, peer := range s.accountList {
		if peer != nil && peer.ID == id {
			s.accountList = append(s.accountList[:i], s.accountList[i+1:]...)
			break
		}
	}
	s.accountRegistry.Unregister(id)
	delete(s.accountDeps, id)

	s.accountMu.Unlock()
	if scheduler != nil {
		scheduler.Stop()
		scheduler.Wait()
	}
	if td != nil {
		if err := td.Close(); err != nil {
			slog.Warn("account_remove_close_partial",
				"account", id, "error", err)
		}
	}
	s.setAccountRemovalInProgress(id, false)

	slog.Info("account_deactivated", "account", id)
	return nil
}

func scrubLiveTeamSpaceMembership(accounts []*core.Account, removedID string) {
	if removedID == "" {
		return
	}
	for _, account := range accounts {
		if account == nil || account.ID == removedID || account.Config == nil || !account.Config.IsTeamSpaceAccount() {
			continue
		}
		members := account.Config.TeamSpace.Members
		if len(members) == 0 {
			continue
		}
		next := members[:0]
		for _, member := range members {
			if member != removedID {
				next = append(next, member)
			}
		}
		for i := len(next); i < len(members); i++ {
			members[i] = ""
		}
		account.Config.TeamSpace.Members = next
	}
}

// handleAdminAccountAdd activates an account that already exists on disk.
// Request body: {"account_id": "charlie"}.
//
// The account directory must already be provisioned (typically by
// `kittypaw account add <name>`) — this handler does not create files,
// only reads the on-disk config and calls AddAccount. That split keeps
// the HTTP surface narrow: no bot tokens or LLM secrets in the request
// body, nothing for an attacker to inject beyond an account ID.
func (s *Server) handleAdminAccountAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AccountID string `json:"account_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.AccountID == "" {
		writeError(w, http.StatusBadRequest, "account_id is required")
		return
	}
	if err := core.ValidateAccountID(body.AccountID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	accountsRoot := s.accountRegistry.BaseDir()
	accountDir := filepath.Join(accountsRoot, body.AccountID)
	cfgPath := filepath.Join(accountDir, "config.toml")
	cfg, err := core.LoadConfig(cfgPath)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found on disk: "+err.Error())
		return
	}
	t := &core.Account{
		ID:      body.AccountID,
		BaseDir: accountDir,
		Config:  cfg,
	}

	if err := s.AddAccount(t); err != nil {
		switch {
		case errors.Is(err, ErrAccountAlreadyActive):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "activated",
		"account_id": body.AccountID,
		"is_shared":  cfg.IsSharedAccount(),
		"channels":   len(cfg.Channels),
	})
}

// handleAdminAccountRemove deactivates a live account. The account ID comes
// from the URL path (Chi param {id}) — no request body is needed and none
// is accepted. This symmetry with handleAdminAccountAdd keeps the admin
// surface narrow: no JSON attacks, no token leakage through request body.
//
// The server does NOT touch the filesystem — that's the CLI's job after a
// 200 response. Status mapping: 200 on success, 404 if not active, 400 on
// malformed ID, 500 on reconcile-drain failure (AC-RM5: CLI aborts before
// touching team-space config or disk).
func (s *Server) handleAdminAccountRemove(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "account id is required")
		return
	}
	if err := core.ValidateAccountID(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.RemoveAccount(id); err != nil {
		switch {
		case errors.Is(err, ErrAccountNotActive):
			writeError(w, http.StatusNotFound, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "deactivated",
		"account_id": id,
	})
}
