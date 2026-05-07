package core

import (
	"os"
	"path/filepath"
	"testing"
)

// seedLegacyLayout writes a pre-multi-account ~/.kittypaw layout: config.toml
// + data/kittypaw.db + skills/ + profiles/ + secrets.json directly under
// baseDir. The helper is shared between tests that need a realistic legacy
// starting point.
func seedLegacyLayout(t *testing.T, baseDir string) {
	t.Helper()
	for _, dir := range []string{"data", "skills", "profiles"} {
		if err := os.MkdirAll(filepath.Join(baseDir, dir), 0o755); err != nil {
			t.Fatalf("seed %s: %v", dir, err)
		}
	}
	writeLegacyFile(t, filepath.Join(baseDir, "config.toml"), "# legacy single-account config")
	writeLegacyFile(t, filepath.Join(baseDir, "data", "kittypaw.db"), "db-bytes")
	writeLegacyFile(t, filepath.Join(baseDir, "data", "kittypaw.db-wal"), "wal-bytes")
	writeLegacyFile(t, filepath.Join(baseDir, "secrets.json"), "{}")
	writeLegacyFile(t, filepath.Join(baseDir, "skills", "hello.md"), "skill body")
	writeLegacyFile(t, filepath.Join(baseDir, "profiles", "default.yaml"), "persona: default")
	// Server-wide files that must NOT move into accounts/default/.
	writeLegacyFile(t, filepath.Join(baseDir, "server.toml"), "# server config")
	writeLegacyFile(t, filepath.Join(baseDir, "daemon.pid"), "12345")
}

func writeLegacyFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be absent, got err=%v", path, err)
	}
}

// TestMigrateLegacyLayout_Moves enforces the migration happy path: a
// legacy single-account ~/.kittypaw becomes accounts/default/, account-scoped
// files move, and server-wide files stay. AC-T9 foundation — without this,
// users on v0.x can't upgrade without manual file surgery.
func TestMigrateLegacyLayout_Moves(t *testing.T) {
	base := t.TempDir()
	seedLegacyLayout(t, base)

	if err := MigrateLegacyLayout(base); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	defDir := filepath.Join(base, "accounts", "default")
	mustExist(t, filepath.Join(defDir, "config.toml"))
	mustExist(t, filepath.Join(defDir, "data", "kittypaw.db"))
	mustExist(t, filepath.Join(defDir, "data", "kittypaw.db-wal"))
	mustExist(t, filepath.Join(defDir, "secrets.json"))
	mustExist(t, filepath.Join(defDir, "skills", "hello.md"))
	mustExist(t, filepath.Join(defDir, "staff", "default.yaml"))

	// Legacy paths are gone.
	mustNotExist(t, filepath.Join(base, "config.toml"))
	mustNotExist(t, filepath.Join(base, "data"))
	mustNotExist(t, filepath.Join(base, "skills"))
	mustNotExist(t, filepath.Join(base, "profiles"))
	mustNotExist(t, filepath.Join(base, "secrets.json"))

	// Server-wide files stay at baseDir.
	mustExist(t, filepath.Join(base, "server.toml"))
	mustExist(t, filepath.Join(base, "daemon.pid"))
}

// TestMigrateLegacyLayout_Idempotent ensures re-running the migration on
// an already-migrated layout is a no-op and does not error. Server
// bootstrap runs this on every start — failing the second run would wedge
// the user out of their account.
func TestMigrateLegacyLayout_Idempotent(t *testing.T) {
	base := t.TempDir()
	seedLegacyLayout(t, base)

	if err := MigrateLegacyLayout(base); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if err := MigrateLegacyLayout(base); err != nil {
		t.Fatalf("second migrate should be a no-op: %v", err)
	}
	mustExist(t, filepath.Join(base, "accounts", "default", "config.toml"))
}

// TestMigrateLegacyLayout_SkipsWhenAccountsExists refuses to clobber an
// existing accounts/ directory — if the user has already created
// accounts/alice/ manually, the migration must step aside rather than
// dragging legacy files on top.
func TestMigrateLegacyLayout_SkipsWhenAccountsExists(t *testing.T) {
	base := t.TempDir()
	seedLegacyLayout(t, base)

	alice := filepath.Join(base, "accounts", "alice")
	if err := os.MkdirAll(alice, 0o755); err != nil {
		t.Fatalf("seed alice: %v", err)
	}

	if err := MigrateLegacyLayout(base); err != nil {
		t.Fatalf("migrate with existing accounts/: %v", err)
	}

	// Legacy files stay put — migration is a no-op when accounts/ exists.
	mustExist(t, filepath.Join(base, "config.toml"))
	mustExist(t, filepath.Join(base, "data", "kittypaw.db"))
	mustNotExist(t, filepath.Join(base, "accounts", "default"))
	mustExist(t, alice)
}

// TestMigrateLegacyLayout_NoLegacyFiles is also a no-op path — fresh
// installs (no config.toml at baseDir) need no migration.
func TestMigrateLegacyLayout_NoLegacyFiles(t *testing.T) {
	base := t.TempDir()
	if err := MigrateLegacyLayout(base); err != nil {
		t.Fatalf("migrate on empty dir: %v", err)
	}
	mustNotExist(t, filepath.Join(base, "accounts"))
}

// TestMigrateLegacyLayout_RecoversFromAbortedStaging enforces the
// staging-dir guarantee: if a previous run crashed after creating
// accounts/.default.staging/ but before the final rename, the next run
// cleans up and completes. Without this, the guard "accounts/ exists →
// no-op" would permanently wedge the user after any mid-migration
// crash.
func TestMigrateLegacyLayout_RecoversFromAbortedStaging(t *testing.T) {
	base := t.TempDir()
	seedLegacyLayout(t, base)

	// Simulate a crashed previous migration: staging dir exists but
	// never got promoted. Crucially accounts/ itself does not exist yet
	// (only the staging subdir), mirroring what os.RemoveAll would see.
	staging := filepath.Join(base, "accounts", ".default.staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("seed staging: %v", err)
	}
	writeLegacyFile(t, filepath.Join(staging, "garbage.txt"), "old run")

	if err := MigrateLegacyLayout(base); err != nil {
		t.Fatalf("migrate with stale staging: %v", err)
	}
	// accounts/default/ should be populated freshly; stale staging gone.
	mustExist(t, filepath.Join(base, "accounts", "default", "config.toml"))
	mustNotExist(t, staging)
	mustNotExist(t, filepath.Join(base, "accounts", "default", "garbage.txt"))
}

// TestValidateAccountID_Hostile enforces the account-ID allowlist. A
// AccountID doubles as a filesystem dir name and a router map key, so
// any input that could traverse (`../`) or collide under
// case-insensitive FS must be rejected at intake — never silently
// sanitized, which would make two distinct inputs map to the same
// account.
func TestValidateAccountID_Hostile(t *testing.T) {
	for _, bad := range []string{
		"",
		".",
		"..",
		"../escape",
		"a/b",
		"a\\b",
		"Alice",  // uppercase — case-insensitive FS collision risk
		"alice ", // trailing space
		" alice",
		"-alice",                              // leading hyphen not allowed (leading underscore IS, see Accepts)
		"가족",                                  // non-ASCII
		"abcdefghijklmnopqrstuvwxyz012345678", // 35 chars
	} {
		if err := ValidateAccountID(bad); err == nil {
			t.Errorf("ValidateAccountID(%q) should have rejected", bad)
		}
	}
}

// TestValidateAccountID_Accepts enumerates the shapes we DO allow so a
// future tightening of the regex is a deliberate decision, not a
// regression that breaks existing family setups. Leading underscore
// is accepted to reserve `_default_` / `_shared_` for multi-user.
func TestValidateAccountID_Accepts(t *testing.T) {
	for _, good := range []string{
		"default", "alice", "bob-2", "family_01", "a", "0abc",
		"_default_", "_shared_", "_alice",
	} {
		if err := ValidateAccountID(good); err != nil {
			t.Errorf("ValidateAccountID(%q) should accept: %v", good, err)
		}
	}
}

// TestDiscoverAccounts_RejectsUnsafeNames pushes the validator through
// DiscoverAccounts — the real callsite where an attacker-controlled dir
// name could land. Hostile directories must be skipped even if they
// contain a valid config.toml, so they never appear as routable
// accounts.
func TestDiscoverAccounts_RejectsUnsafeNames(t *testing.T) {
	base := t.TempDir()
	accountsDir := filepath.Join(base, "accounts")

	for _, name := range []string{"default", "..evil", "Mixed", ".hidden"} {
		dir := filepath.Join(accountsDir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		writeLegacyFile(t, filepath.Join(dir, "config.toml"), "# account")
	}

	accounts, err := DiscoverAccounts(accountsDir)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}

	ids := make(map[string]bool, len(accounts))
	for _, t := range accounts {
		ids[t.ID] = true
	}
	if !ids["default"] {
		t.Error("default should have been discovered")
	}
	for _, bad := range []string{"..evil", "Mixed", ".hidden"} {
		if ids[bad] {
			t.Errorf("unsafe account %q leaked into registry", bad)
		}
	}
}
