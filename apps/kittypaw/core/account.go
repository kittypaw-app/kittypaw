package core

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// validAccountID restricts account names to a safe ASCII subset that can never
// traverse the filesystem ("../"), collide under case-insensitive FS, or
// surprise a logging/audit pipeline with unicode. 1-32 chars, lowercase.
// Leading underscore is allowed to accommodate reserved-form IDs like
// `_default_` / `_shared_` for future multi-user support.
var validAccountID = regexp.MustCompile(`^[a-z0-9_][a-z0-9_-]{0,31}$`)

// ValidateAccountID returns nil if id is safe to use as a filesystem
// directory name and as a AccountRouter map key. A AccountID is trusted as
// a privacy boundary, so any CLI/HTTP flow that accepts user-supplied
// IDs must call this before persisting.
func ValidateAccountID(id string) error {
	if !validAccountID.MatchString(id) {
		return fmt.Errorf("invalid account id %q: must match %s", id, validAccountID.String())
	}
	return nil
}

// Account represents a single user/workspace with isolated data.
type Account struct {
	ID      string
	BaseDir string // e.g. ~/.kittypaw/accounts/<id>/
	Config  *Config
}

// DataDir returns the account's database directory.
func (t *Account) DataDir() string {
	return filepath.Join(t.BaseDir, "data")
}

// SkillsDir returns the account's skills directory.
func (t *Account) SkillsDir() string {
	return filepath.Join(t.BaseDir, "skills")
}

// StaffDir returns the account's staff identity directory.
func (t *Account) StaffDir() string {
	return filepath.Join(t.BaseDir, "staff")
}

// SecretsPath returns the path to the account's secrets file.
func (t *Account) SecretsPath() string {
	return filepath.Join(t.BaseDir, "secrets.json")
}

// DBPath returns the path to the account's SQLite database.
// Migrates legacy gopaw.db → kittypaw.db on first access.
func (t *Account) DBPath() string {
	dbDir := filepath.Join(t.BaseDir, "data")
	newPath := filepath.Join(dbDir, "kittypaw.db")
	if _, err := os.Stat(newPath); os.IsNotExist(err) {
		oldPath := filepath.Join(dbDir, "gopaw.db")
		if _, err := os.Stat(oldPath); err == nil {
			_ = os.Rename(oldPath, newPath)
			// Also migrate WAL and SHM files if they exist.
			_ = os.Rename(oldPath+"-wal", newPath+"-wal")
			_ = os.Rename(oldPath+"-shm", newPath+"-shm")
		}
	}
	return newPath
}

// PackagesDir returns the account's npm packages directory.
func (t *Account) PackagesDir() string {
	return filepath.Join(t.BaseDir, "packages")
}

func (t *Account) migrateStaffDir() error {
	profilesDir := filepath.Join(t.BaseDir, "profiles")
	staffDir := t.StaffDir()

	if _, err := os.Stat(profilesDir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat legacy profiles dir: %w", err)
	}

	if _, err := os.Stat(staffDir); err == nil {
		slog.Warn("legacy profiles/ and staff/ both exist; using staff/",
			"account", t.ID, "profiles_dir", profilesDir, "staff_dir", staffDir)
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat staff dir: %w", err)
	}

	if err := os.Rename(profilesDir, staffDir); err != nil {
		return fmt.Errorf("rename profiles to staff: %w", err)
	}
	return nil
}

// EnsureDirs creates all required directories for the account.
func (t *Account) EnsureDirs() error {
	if err := t.migrateStaffDir(); err != nil {
		return err
	}
	dirs := []string{
		t.BaseDir,
		t.DataDir(),
		t.SkillsDir(),
		t.StaffDir(),
		t.PackagesDir(),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
	}
	return nil
}

// AccountRegistry manages all loaded accounts. Safe for concurrent access.
type AccountRegistry struct {
	mu        sync.RWMutex
	accounts  map[string]*Account
	baseDir   string // e.g. ~/.kittypaw/accounts/
	defaultID string
}

// NewAccountRegistry creates a registry rooted at baseDir.
func NewAccountRegistry(baseDir, defaultID string) *AccountRegistry {
	if defaultID == "" {
		defaultID = "default"
	}
	return &AccountRegistry{
		accounts:  make(map[string]*Account),
		baseDir:   baseDir,
		defaultID: defaultID,
	}
}

// Register adds an account to the registry.
func (r *AccountRegistry) Register(t *Account) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.accounts[t.ID] = t
}

// Unregister removes an account from the registry. Returns true if one was
// present. Used by hot-add rollback when a downstream step fails after
// the registry has already accepted the account — we must retract the
// Share.read / Fanout surface so peers cannot resolve a half-bound account.
func (r *AccountRegistry) Unregister(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.accounts[id]; !ok {
		return false
	}
	delete(r.accounts, id)
	return true
}

// Get returns the account with the given ID, or nil if not found.
func (r *AccountRegistry) Get(id string) *Account {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.accounts[id]
}

// List returns all registered account IDs.
func (r *AccountRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.accounts))
	for id := range r.accounts {
		ids = append(ids, id)
	}
	return ids
}

// DefaultID returns the configured default account ID.
func (r *AccountRegistry) DefaultID() string {
	return r.defaultID
}

// BaseDir returns the accounts root directory.
func (r *AccountRegistry) BaseDir() string {
	return r.baseDir
}

// ValidateAccountChannels fails fast if two accounts declare the same
// Telegram bot token or Kakao relay WebSocket URL. Without this check the
// Telegram long-poll would silently race (one account's bot would steal
// updates from another) and the Kakao relay would dual-bind a single
// user account — both scenarios cause hard-to-diagnose message loss.
//
// accountChannels maps accountID → channel configs. Returns an aggregated
// error listing every duplicate.
func ValidateAccountChannels(accountChannels map[string][]ChannelConfig) error {
	telegramSeen := make(map[string]string) // token → owning account
	kakaoSeen := make(map[string]string)    // wsURL → owning account
	var dupes []string

	// Deterministic iteration order for stable error messages.
	ids := make([]string, 0, len(accountChannels))
	for id := range accountChannels {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, tid := range ids {
		for _, cfg := range accountChannels[tid] {
			switch cfg.ChannelType {
			case ChannelTelegram:
				token := strings.TrimSpace(cfg.Token)
				if token == "" {
					continue
				}
				if prev, ok := telegramSeen[token]; ok {
					dupes = append(dupes, fmt.Sprintf(
						"telegram bot_token collides between accounts %q and %q",
						prev, tid))
					continue
				}
				telegramSeen[token] = tid
			case ChannelKakaoTalk:
				url := strings.TrimSpace(cfg.KakaoWSURL)
				if url == "" {
					continue
				}
				if prev, ok := kakaoSeen[url]; ok {
					dupes = append(dupes, fmt.Sprintf(
						"kakao relay URL collides between accounts %q and %q",
						prev, tid))
					continue
				}
				kakaoSeen[url] = tid
			}
		}
	}

	if len(dupes) == 0 {
		return nil
	}
	return fmt.Errorf("duplicate channel credentials across accounts: %v", dupes)
}

// ChatBelongsToAccount reports whether chatID belongs to the account whose
// Config is cfg. An account with no AllowedChatIDs configured returns true —
// the check is permissive for legacy single-account installs and for channels
// like web_chat whose ownership is tracked by SessionID, not chat_id.
//
// When AllowedChatIDs is non-empty the check is strict: only IDs in that list
// pass. This is the last line of defense against a compromised bot token or
// a crafted inbound payload that claims AccountID=alice while carrying bob's
// chat_id — a mismatch must never reach the runner loop or it would mix
// accounts' conversation histories in the DB (AC-T7).
func ChatBelongsToAccount(cfg *Config, chatID string) bool {
	allowed := cfgAllowedChatIDs(cfg)
	if len(allowed) == 0 {
		return true
	}
	for _, owned := range allowed {
		if owned == chatID {
			return true
		}
	}
	return false
}

// UserBelongsToAccount reports whether userID is allowed to act within the
// account. An account with no AllowedUserIDs configured is permissive for
// backwards compatibility and one-to-one bot deployments.
func UserBelongsToAccount(cfg *Config, userID string) bool {
	allowed := cfgAllowedUserIDs(cfg)
	if len(allowed) == 0 {
		return true
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return false
	}
	for _, owned := range allowed {
		if strings.TrimSpace(owned) == userID {
			return true
		}
	}
	return false
}

func cfgAllowedChatIDs(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	out := append([]string(nil), cfg.AllowedChatIDs...)
	for _, ch := range cfg.Channels {
		out = append(out, ch.AllowedChatIDs...)
	}
	return out
}

func cfgAllowedUserIDs(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	out := append([]string(nil), cfg.AllowedUserIDs...)
	for _, ch := range cfg.Channels {
		out = append(out, ch.AllowedUserIDs...)
	}
	return out
}

func FirstAllowedChatID(cfg *Config) string {
	for _, id := range cfgAllowedChatIDs(cfg) {
		if strings.TrimSpace(id) != "" {
			return id
		}
	}
	return ""
}

// ValidateTeamSpaceAccounts fails fast when a team-space account declares
// channel configs. Team spaces are coordinators, not channel owners.
func ValidateTeamSpaceAccounts(accounts []*Account) error {
	var offenders []string
	for _, t := range accounts {
		if t == nil || t.Config == nil || !t.Config.IsTeamSpaceAccount() {
			continue
		}
		if len(t.Config.Channels) == 0 {
			continue
		}
		types := make([]string, 0, len(t.Config.Channels))
		for _, ch := range t.Config.Channels {
			types = append(types, string(ch.ChannelType))
		}
		offenders = append(offenders, fmt.Sprintf("%s:%v", t.ID, types))
	}
	if len(offenders) == 0 {
		return nil
	}
	return fmt.Errorf("team space must not declare channels: %v", offenders)
}

// ValidateFamilyAccounts is a legacy compatibility alias for
// ValidateTeamSpaceAccounts.
func ValidateFamilyAccounts(accounts []*Account) error {
	return ValidateTeamSpaceAccounts(accounts)
}

// ValidateTeamSpaceMemberships verifies that every configured team-space member
// is an existing personal account. An omitted or empty members list is deny-all
// and is valid.
//
// This is the core validator only. Startup, reload, and hot-add paths are
// responsible for calling it when they wire team-space account loading.
func ValidateTeamSpaceMemberships(accounts []*Account) error {
	byID := make(map[string]*Account, len(accounts))
	for _, account := range accounts {
		if account == nil {
			continue
		}
		byID[account.ID] = account
	}

	var problems []string
	for _, team := range accounts {
		if team == nil || team.Config == nil || !team.Config.IsTeamSpaceAccount() {
			continue
		}
		for _, member := range team.Config.TeamSpace.Members {
			if err := ValidateAccountID(member); err != nil {
				problems = append(problems, fmt.Sprintf("%s:%s invalid member id: %v", team.ID, member, err))
				continue
			}
			if member == team.ID {
				problems = append(problems, fmt.Sprintf("%s must not list itself as a team-space member", team.ID))
				continue
			}
			target := byID[member]
			if target == nil {
				problems = append(problems, fmt.Sprintf("%s references unknown member %s", team.ID, member))
				continue
			}
			if target.Config != nil && target.Config.IsTeamSpaceAccount() {
				problems = append(problems, fmt.Sprintf("%s member %s is another team space", team.ID, member))
			}
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("team-space membership validation failed: %s", strings.Join(problems, "; "))
}

// MigrateTenantsToAccounts renames a legacy ~/.kittypaw/tenants/ directory
// to ~/.kittypaw/accounts/ at boot. The terminology shifted with the
// whole-tree tenant→account rename; this is a one-shot, idempotent rename
// invoked before MigrateLegacyLayout so the rest of bootstrap sees the new
// path.
//
// Defenses:
//   - os.Lstat (not Stat) on both paths — refuses to follow a symlink at
//     either location. A symlink at ~/.kittypaw/tenants would otherwise
//     cause os.Rename to move only the link (target untouched) and a
//     symlink at ~/.kittypaw/accounts would let an attacker redirect new
//     account writes outside KittyPaw's data root.
//   - If accounts/ exists but is empty, treat it as a leftover (some shells
//     pre-create it) and remove it before renaming. Aborts only when
//     accounts/ holds real data — manual intervention is required to choose
//     which side wins.
func MigrateTenantsToAccounts(baseDir string) error {
	oldDir := filepath.Join(baseDir, "tenants")
	newDir := filepath.Join(baseDir, "accounts")

	oldStat, oldErr := os.Lstat(oldDir)
	if os.IsNotExist(oldErr) {
		return nil
	}
	if oldErr != nil {
		return fmt.Errorf("stat tenants dir: %w", oldErr)
	}
	if oldStat.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink — refusing to migrate (resolve manually)", oldDir)
	}
	if !oldStat.IsDir() {
		return fmt.Errorf("%s exists but is not a directory", oldDir)
	}

	if newStat, err := os.Lstat(newDir); err == nil {
		if newStat.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink — refusing to migrate (resolve manually)", newDir)
		}
		if newStat.IsDir() && isEmptyDir(newDir) {
			if err := os.Remove(newDir); err != nil {
				return fmt.Errorf("remove empty accounts dir: %w", err)
			}
		} else {
			return fmt.Errorf("both %s and %s exist — manual cleanup required", oldDir, newDir)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat accounts dir: %w", err)
	}

	slog.Info("migrate: renaming legacy tenants dir to accounts", "from", oldDir, "to", newDir)
	if err := os.Rename(oldDir, newDir); err != nil {
		return fmt.Errorf("rename tenants → accounts: %w", err)
	}
	return nil
}

// MigrateLegacyLayout moves a pre-multi-account ~/.kittypaw layout into
// accounts/default/ so existing v0.x installs upgrade without manual file
// surgery. It is a one-way, idempotent operation invoked at server
// bootstrap.
//
// Detection: legacy layout has config.toml at baseDir AND no accounts/
// subdirectory yet. If accounts/ already exists (even empty) we step
// aside — the user may have scaffolded an account manually and we must
// not drop legacy files on top of it.
//
// Moved (account-scoped): config.toml, secrets.json, data/, skills/,
// staff/, profiles/, packages/. Left in place (server-wide): server.toml,
// daemon.pid, daemon.log, anything else under baseDir.
//
// Atomicity: files are first relocated into a staging directory
// (accounts/.default.staging/), and only once every move succeeds is the
// staging dir renamed to accounts/default/. If any intermediate step
// fails, the caller sees an error, nothing has moved from the user's
// perspective, and the next boot can retry cleanly — the legacy-guard
// (config.toml-at-baseDir) still holds.
func MigrateLegacyLayout(baseDir string) error {
	legacyCfg := filepath.Join(baseDir, "config.toml")
	if _, err := os.Stat(legacyCfg); os.IsNotExist(err) {
		return nil // Fresh install or already migrated.
	} else if err != nil {
		return fmt.Errorf("stat legacy config: %w", err)
	}

	// Clean up any abandoned staging dir from a previous crashed run
	// BEFORE the "accounts/ exists" guard, otherwise we'd wedge
	// permanently — the guard would see accounts/ and bail, but accounts/
	// holds only the half-done staging dir.
	accountsDir := filepath.Join(baseDir, "accounts")
	stagingDir := filepath.Join(accountsDir, ".default.staging")
	_ = os.RemoveAll(stagingDir)

	if _, err := os.Stat(accountsDir); err == nil {
		// accounts/ is non-empty (has real account dirs) — step aside.
		// Only the staging path is ours to clean up.
		if isEmptyDir(accountsDir) {
			_ = os.Remove(accountsDir)
		} else {
			slog.Warn("legacy config detected but accounts/ is non-empty — skipping migration; move config manually into accounts/default/",
				"legacy_config", legacyCfg, "accounts_dir", accountsDir)
			return nil
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat accounts dir: %w", err)
	}

	// Stage into accounts/.default.staging/ so an error mid-flight leaves
	// the user's legacy tree intact.
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}

	for _, name := range []string{"config.toml", "secrets.json", "data", "skills", "staff", "profiles", "packages"} {
		src := filepath.Join(baseDir, name)
		if _, err := os.Stat(src); os.IsNotExist(err) {
			continue
		} else if err != nil {
			_ = os.RemoveAll(stagingDir)
			return fmt.Errorf("stat %s: %w", src, err)
		}
		dst := filepath.Join(stagingDir, name)
		if err := os.Rename(src, dst); err != nil {
			_ = os.RemoveAll(stagingDir)
			return fmt.Errorf("stage %s → %s: %w", src, dst, err)
		}
	}

	finalDir := filepath.Join(accountsDir, "default")
	if err := os.Rename(stagingDir, finalDir); err != nil {
		return fmt.Errorf("commit staging → %s: %w", finalDir, err)
	}
	if err := (&Account{ID: DefaultAccountID, BaseDir: finalDir}).migrateStaffDir(); err != nil {
		return err
	}
	return nil
}

// isEmptyDir returns true if dir exists and contains no entries.
// Used to detect an accounts/ that only ever held our own staging dir.
func isEmptyDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	return len(entries) == 0
}

// DiscoverAccounts scans baseDir for account directories, loads their
// configs, and returns Account values. It does NOT register them — the
// caller is responsible for bootstrapping (Store, Session, etc.) and
// calling Register.
func DiscoverAccounts(baseDir string) ([]*Account, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read accounts dir: %w", err)
	}

	var accounts []*Account
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		// Reject anything that could traverse the filesystem or collide
		// with a case-insensitive FS — AccountID is a privacy boundary.
		if err := ValidateAccountID(id); err != nil {
			slog.Warn("discover: rejecting unsafe account dir name",
				"name", id, "error", err)
			continue
		}
		accountDir := filepath.Join(baseDir, id)
		cfgPath := filepath.Join(accountDir, "config.toml")

		cfg, err := LoadConfig(cfgPath)
		if err != nil {
			// Skip accounts with invalid or missing configs.
			continue
		}

		accounts = append(accounts, &Account{
			ID:      id,
			BaseDir: accountDir,
			Config:  cfg,
		})
	}
	return accounts, nil
}
