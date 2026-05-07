package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/store"
)

// TestLegacyMigration_PreservesDBRows enforces the AC-T9 completion bar:
// a pre-multi-account install's kittypaw.db must retain its rows after
// MigrateLegacyLayout moves the whole layout into accounts/default/. A
// byte-equality check would miss a SQLite corruption, so we re-open the DB
// through the store API at the new path and read back a seeded row.
//
// We deliberately do NOT call OpenAccountDeps here — that path also opens
// the LLM provider, which requires a populated [llm] stanza and would
// conflate DB-preservation regressions with provider-wiring regressions.
// DiscoverAccounts confirms the account is visible; the new Store.Open is
// the narrowly-scoped "rows survived the rename" probe.
func TestLegacyMigration_PreservesDBRows(t *testing.T) {
	base := t.TempDir()

	if err := os.WriteFile(
		filepath.Join(base, "config.toml"),
		[]byte("# legacy config\n"), 0o644,
	); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	legacyData := filepath.Join(base, "data")
	if err := os.MkdirAll(legacyData, 0o755); err != nil {
		t.Fatalf("seed data dir: %v", err)
	}

	legacyDB := filepath.Join(legacyData, "kittypaw.db")
	stLegacy, err := store.Open(legacyDB)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	seed := &core.ConversationState{
		ConversationID: "legacy-runner",
		SystemPrompt:   "legacy system prompt",
		Turns: []core.ConversationTurn{
			{Role: core.RoleUser, Content: "pre-migration message", Timestamp: "1"},
			{Role: core.RoleAssistant, Content: "pre-migration reply", Timestamp: "2"},
		},
	}
	if err := stLegacy.SaveConversationState(seed); err != nil {
		t.Fatalf("seed SaveConversationState: %v", err)
	}
	if err := stLegacy.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	if err := core.MigrateLegacyLayout(base); err != nil {
		t.Fatalf("MigrateLegacyLayout: %v", err)
	}

	accounts, err := core.DiscoverAccounts(filepath.Join(base, "accounts"))
	if err != nil {
		t.Fatalf("DiscoverAccounts: %v", err)
	}
	if len(accounts) != 1 || accounts[0].ID != "default" {
		t.Fatalf("expected single 'default' account post-migration, got %+v", accounts)
	}

	migratedDB := accounts[0].DBPath()
	if migratedDB == legacyDB {
		t.Fatalf("migrated DB path %s unchanged from legacy", migratedDB)
	}
	stNew, err := store.Open(migratedDB)
	if err != nil {
		t.Fatalf("reopen migrated db: %v", err)
	}
	defer func() { _ = stNew.Close() }()

	got, err := stNew.LoadConversationState()
	if err != nil {
		t.Fatalf("LoadConversationState post-migration: %v", err)
	}
	if got == nil {
		t.Fatal("LoadState returned nil — the seeded runner row was lost in migration")
	}
	if got.SystemPrompt != "legacy system prompt" {
		t.Errorf("SystemPrompt = %q, want preserved seed", got.SystemPrompt)
	}
	if len(got.Turns) != 2 {
		t.Fatalf("Turns count = %d, want 2 (seeded rows lost)", len(got.Turns))
	}
	if got.Turns[0].Content != "pre-migration message" {
		t.Errorf("Turns[0].Content = %q, want preserved seed", got.Turns[0].Content)
	}
}

// TestLegacyMigration_ConfigPermissionPreserved locks in that the 0600
// secrets.json keeps its restrictive mode through the move+rename. A
// staging-via-copy would reset the mode to the process umask; our
// implementation uses os.Rename, which preserves the inode and therefore
// the mode — this test guards against a regression to copy-and-delete.
func TestLegacyMigration_ConfigPermissionPreserved(t *testing.T) {
	base := t.TempDir()

	if err := os.WriteFile(
		filepath.Join(base, "config.toml"),
		[]byte("# legacy"), 0o640,
	); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(base, "secrets.json"),
		[]byte(`{"telegram": {"bot_token": "secret"}}`),
		0o600,
	); err != nil {
		t.Fatalf("seed secrets: %v", err)
	}

	if err := core.MigrateLegacyLayout(base); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for path, want := range map[string]os.FileMode{
		filepath.Join(base, "accounts", "default", "config.toml"):  0o640,
		filepath.Join(base, "accounts", "default", "secrets.json"): 0o600,
	} {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := fi.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o (os.Rename must preserve perms)", path, got, want)
		}
	}
}
