# Staff Runner Vocabulary Implementation Plan

> **For autonomous workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the pre-rename identity and runtime vocabulary with Staff, Runner, Account, and Conversation terms across Kittypaw as a breaking change.

**Architecture:** Keep the existing package boundaries. Core owns Staff identity and filesystem paths, Store owns staff metadata and conversation persistence, Engine/Sandbox expose Staff and Runner JavaScript tools, and Server/Client/CLI expose the new public names. This plan removes public compatibility aliases while migrating local disk and SQLite data to the new names.

**Tech Stack:** Go, SQLite migrations, goja sandbox, chi HTTP router, Cobra CLI, existing `go test` suites.

---

## File Structure

Core identity and account layout:

- Move the old core identity file to `apps/kittypaw/core/staff.go`
- Modify the old core identity tests
- Modify: `apps/kittypaw/core/types.go`
- Modify: `apps/kittypaw/core/config.go`
- Modify: `apps/kittypaw/core/account.go`
- Modify: `apps/kittypaw/core/account_setup_test.go`
- Modify: `apps/kittypaw/core/account_migrate_test.go`

Persistence:

- Create: `apps/kittypaw/store/migrations/024_staff_meta.sql`
- Modify: `apps/kittypaw/store/store.go`
- Modify: `apps/kittypaw/store/store_test.go`
- Modify: `apps/kittypaw/server/account_migrate_integration_test.go`

Engine and sandbox:

- Modify: `apps/kittypaw/core/skillmeta.go`
- Modify: `apps/kittypaw/sandbox/exec.go`
- Modify: `apps/kittypaw/sandbox/sandbox_test.go`
- Modify: `apps/kittypaw/engine/executor.go`
- Modify: `apps/kittypaw/engine/session.go`
- Modify: `apps/kittypaw/engine/session_test.go`
- Modify: `apps/kittypaw/engine/prompt.go`
- Modify: `apps/kittypaw/engine/prompt_test.go`
- Modify: `apps/kittypaw/engine/commands.go`
- Modify: `apps/kittypaw/engine/commands_test.go`
- Modify: `apps/kittypaw/engine/orchestration.go`
- Modify: `apps/kittypaw/engine/orchestration_test.go`
- Modify: `apps/kittypaw/engine/code_normalize.go`
- Modify: `apps/kittypaw/engine/kanban_tool_test.go`

Server, client, CLI, docs:

- Move the old server identity API file to `apps/kittypaw/server/api_staff.go`
- Modify: `apps/kittypaw/server/server.go`
- Modify: `apps/kittypaw/server/api.go`
- Modify: `apps/kittypaw/client/client.go`
- Modify: `apps/kittypaw/client/client_test.go`
- Modify: `apps/kittypaw/cli/main.go`
- Modify: `apps/kittypaw/cli/main_test.go`
- Modify: `apps/kittypaw/cli/cmd_setup_test.go`
- Modify: `apps/kittypaw/cli/cmd_kanban.go`
- Modify: `apps/kittypaw/CLAUDE.md`
- Modify: `apps/kittypaw/TASKS.md`
- Modify: `docs/superpowers/specs/2026-05-07-kanban-*.md`
- Modify: `docs/superpowers/plans/2026-05-07-kanban-*.md`

## Task 1: Core Staff Identity And Conversation Names

**Files:**

- Move the old core identity file to `apps/kittypaw/core/staff.go`
- Modify the old core identity tests
- Modify: `apps/kittypaw/core/types.go`
- Modify: `apps/kittypaw/core/config.go`
- Modify: `apps/kittypaw/core/account.go`
- Modify: `apps/kittypaw/core/account_setup_test.go`
- Modify: `apps/kittypaw/core/account_migrate_test.go`

- [ ] **Step 1: Rename core tests to the new Staff API and make them fail**

In the old core identity test file, rename the tests and switch paths from the
old identity directory to `staff`. Keep the file name for this step so the test
diff is easier to review.

```go
func TestLoadStaff_ExistingSoul(t *testing.T) {
	base := t.TempDir()
	staffDir := filepath.Join(base, "staff", "mybot")
	if err := os.MkdirAll(staffDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const soul = "custom staff soul"
	if err := os.WriteFile(filepath.Join(staffDir, "SOUL.md"), []byte(soul), 0o644); err != nil {
		t.Fatal(err)
	}

	staff, err := LoadStaff(base, "mybot")
	if err != nil {
		t.Fatalf("LoadStaff() error = %v", err)
	}
	if staff.ID != "mybot" || staff.Soul != soul {
		t.Fatalf("staff = %+v, want ID mybot and soul %q", staff, soul)
	}
}

func TestEnsureDefaultStaffCreatesStaffDir(t *testing.T) {
	base := t.TempDir()
	if err := EnsureDefaultStaff(base); err != nil {
		t.Fatalf("EnsureDefaultStaff() error = %v", err)
	}
	soulPath := filepath.Join(base, "staff", "default", "SOUL.md")
	if _, err := os.Stat(soulPath); err != nil {
		t.Fatalf("SOUL.md not created under staff/: %v", err)
	}
}

func TestValidateStaffID_Invalid(t *testing.T) {
	if err := ValidateStaffID("../evil"); err == nil {
		t.Fatal("expected invalid StaffID error")
	}
}
```

In `apps/kittypaw/core/account_setup_test.go`, change the directory assertion to:

```go
for _, sub := range []string{"data", "skills", "staff", "packages"} {
	mustExist(t, filepath.Join(account.BaseDir, sub))
}
```

- [ ] **Step 2: Run the failing core tests**

Run:

```sh
go test ./core -run 'TestLoadStaff|TestEnsureDefaultStaff|TestValidateStaffID|TestInitAccount' -count=1
```

Expected: FAIL with undefined symbols such as `LoadStaff`, `EnsureDefaultStaff`, and `ValidateStaffID`.

- [ ] **Step 3: Move and rename core Staff functions**

Run:

```sh
git mv <old-core-identity-file> apps/kittypaw/core/staff.go
```

In `apps/kittypaw/core/staff.go`, make these exact semantic replacements:

```go
// Staff holds the loaded identity data for a single staff.
type Staff struct {
	ID     string // staff directory name
	Nick   string // display name from config
	Soul   string // SOUL.md content
	UserMD string // USER.md content, optional
}
```

```go
// LoadStaff reads a staff member's SOUL.md and USER.md from disk.
func LoadStaff(base, name string) (*Staff, error) {
	if err := ValidateStaffID(name); err != nil {
		return nil, fmt.Errorf("load staff: %w", err)
	}

	staff := &Staff{ID: name}
	staffDir := filepath.Join(base, "staff", name)

	soulData, err := os.ReadFile(filepath.Join(staffDir, "SOUL.md"))
	if err != nil {
		slog.Warn("SOUL.md not found, using default preset",
			"staff", name, "path", staffDir)
		staff.Soul = Presets["default-assistant"].Soul
	} else {
		staff.Soul = string(soulData)
	}

	if userData, err := os.ReadFile(filepath.Join(staffDir, "USER.md")); err == nil {
		staff.UserMD = string(userData)
	}

	return staff, nil
}
```

Rename the remaining functions in the same file:

```go
func EnsureDefaultStaff(base string) error
func ApplyStaffPreset(base, staffID, presetID string) error
func DetectStaffDirty(base, staffID string) (bool, error)
func StaffPresetStatus(base, staffID string) PresetStatusResult
```

Every path in those functions must use `filepath.Join(base, "staff", staffID)` or `filepath.Join(base, "staff", "default")`.

- [ ] **Step 4: Rename core shared types and config fields**

In `apps/kittypaw/core/types.go`, replace the conversation state type:

```go
// ConversationState holds the mutable runtime state for the account conversation.
type ConversationState struct {
	ConversationID string             `json:"conversation_id,omitempty"`
	SystemPrompt   string             `json:"system_prompt"`
	Turns          []ConversationTurn `json:"turns"`
}
```

Replace the old identity ID validator with:

```go
// ValidateStaffID checks that a staff ID contains only safe characters.
func ValidateStaffID(id string) error {
	if id == "" {
		return fmt.Errorf("staff ID is empty")
	}
	if strings.Contains(id, "..") || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("staff ID contains path traversal characters: %q", id)
	}
	if !validSkillName.MatchString(id) {
		return fmt.Errorf("staff ID contains invalid characters: %q (allowed: a-z, A-Z, 0-9, _, -)", id)
	}
	return nil
}
```

In `apps/kittypaw/core/config.go`, replace the relevant config fields and types:

```go
	Runners      []RunnerConfig `toml:"runners"`
	Staff        []StaffConfig  `toml:"staff"`
	DefaultStaff string         `toml:"default_staff"`
```

```go
// RunnerConfig defines one runner's execution behavior.
type RunnerConfig struct {
	ID            string            `toml:"id"`
	Name          string            `toml:"name"`
	SystemPrompt  string            `toml:"system_prompt"`
	Channels      []string          `toml:"channels"`
	AllowedSkills []SkillPermission `toml:"allowed_skills"`
}

// StaffConfig defines a switchable staff identity.
type StaffConfig struct {
	ID       string   `toml:"id"`
	Nick     string   `toml:"nick"`
	Channels []string `toml:"channels"`
}
```

Update `DefaultConfig()` to set:

```go
DefaultStaff: "default",
```

Rename lookup helpers to:

```go
func (c *Config) FindRunner(id string) *RunnerConfig
func (c *Config) DefaultRunner() *RunnerConfig
```

- [ ] **Step 5: Rename account staff directory and add filesystem migration**

In `apps/kittypaw/core/account.go`, replace the old identity directory helper with:

```go
// StaffDir returns the account's staff identity directory.
func (t *Account) StaffDir() string {
	return filepath.Join(t.BaseDir, "staff")
}
```

Add this helper near `EnsureDirs`:

```go
func (t *Account) migrateStaffDir() error {
	legacyStaffDir := filepath.Join(t.BaseDir, "profiles")
	staffDir := t.StaffDir()

	if _, err := os.Stat(legacyStaffDir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat legacy profiles dir: %w", err)
	}

	if _, err := os.Stat(staffDir); err == nil {
		slog.Warn("legacy profiles/ and staff/ both exist; using staff/",
			"account", t.ID, "legacy_staff_dir", legacyStaffDir, "staff_dir", staffDir)
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat staff dir: %w", err)
	}

	if err := os.Rename(legacyStaffDir, staffDir); err != nil {
		return fmt.Errorf("rename legacy staff dir to staff: %w", err)
	}
	return nil
}
```

Call it at the start of `EnsureDirs`:

```go
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
```

In `MigrateLegacyLayout`, change the moved account-scoped list to include `staff` and `profiles` so root legacy installs can still be migrated:

```go
for _, name := range []string{"config.toml", "secrets.json", "data", "skills", "staff", "profiles", "packages"} {
```

After committing the staging directory to `accounts/default`, call:

```go
if err := (&Account{ID: DefaultAccountID, BaseDir: finalDir}).migrateStaffDir(); err != nil {
	return err
}
```

- [ ] **Step 6: Run core tests and commit**

Run:

```sh
go test ./core -count=1
```

Expected: PASS.

Commit:

```sh
git add apps/kittypaw/core
git commit -m "refactor(core): rename legacy staff dir to staff"
```

## Task 2: Store Staff Metadata And Conversation State

**Files:**

- Create: `apps/kittypaw/store/migrations/024_staff_meta.sql`
- Modify: `apps/kittypaw/store/store.go`
- Modify: `apps/kittypaw/store/store_test.go`
- Modify: `apps/kittypaw/server/account_migrate_integration_test.go`

- [ ] **Step 1: Write failing store tests for staff metadata**

In `apps/kittypaw/store/store_test.go`, replace the old identity metadata test with:

```go
func TestStaffMetaCRUD(t *testing.T) {
	st := newTestStore(t)

	if _, ok, err := st.GetStaffMeta("missing"); err != nil || ok {
		t.Fatalf("GetStaffMeta(missing) = ok %v err %v, want ok false nil err", ok, err)
	}

	if err := st.UpsertStaffMeta("staff-1", "dev staff", `["code","debug"]`, "admin"); err != nil {
		t.Fatalf("UpsertStaffMeta() error = %v", err)
	}

	got, ok, err := st.GetStaffMeta("staff-1")
	if err != nil || !ok {
		t.Fatalf("GetStaffMeta(staff-1) = ok %v err %v", ok, err)
	}
	if got.ID != "staff-1" || got.Description != "dev staff" || got.CreatedBy != "admin" || !got.Active {
		t.Fatalf("staff meta = %+v", got)
	}

	list, err := st.ListActiveStaff()
	if err != nil {
		t.Fatalf("ListActiveStaff() error = %v", err)
	}
	if len(list) != 1 || list[0].ID != "staff-1" {
		t.Fatalf("active staff = %+v", list)
	}

	if err := st.SetStaffActive("staff-1", false); err != nil {
		t.Fatalf("SetStaffActive(false) error = %v", err)
	}
	list, err = st.ListActiveStaff()
	if err != nil {
		t.Fatalf("ListActiveStaff() after inactive error = %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("active staff after inactive = %+v, want empty", list)
	}
}
```

Add a migration test that seeds old `profile_meta` through migrations up to 023 and then opens the current store. Use the existing migration helpers in the file; if there is no helper, create the table manually before opening the store:

```go
func TestMigrationStaffMetaToStaffMeta(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "kittypaw.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE profile_meta (
		id TEXT PRIMARY KEY,
		description TEXT NOT NULL DEFAULT '',
		equipped_skills TEXT NOT NULL DEFAULT '[]',
		active INTEGER NOT NULL DEFAULT 1,
		created_by TEXT NOT NULL DEFAULT 'manual',
		created_at TEXT NOT NULL DEFAULT '2026-05-07T00:00:00Z'
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO profile_meta (id, description, equipped_skills, active, created_by, created_at)
		VALUES ('coder', 'Code staff', '["git"]', 1, 'test', '2026-05-07T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()

	got, ok, err := st.GetStaffMeta("coder")
	if err != nil || !ok {
		t.Fatalf("GetStaffMeta(coder) = ok %v err %v", ok, err)
	}
	if got.Description != "Code staff" || got.EquippedSkills != `["git"]` || got.CreatedBy != "test" {
		t.Fatalf("migrated staff meta = %+v", got)
	}
}
```

- [ ] **Step 2: Run failing store tests**

Run:

```sh
go test ./store -run 'TestStaffMetaCRUD|TestMigrationStaffMetaToStaffMeta' -count=1
```

Expected: FAIL with undefined symbols such as `GetStaffMeta` or a missing `staff_meta` table.

- [ ] **Step 3: Add the staff metadata migration**

Create `apps/kittypaw/store/migrations/024_staff_meta.sql`:

```sql
CREATE TABLE IF NOT EXISTS staff_meta (
    id TEXT PRIMARY KEY,
    description TEXT NOT NULL DEFAULT '',
    equipped_skills TEXT NOT NULL DEFAULT '[]',
    active INTEGER NOT NULL DEFAULT 1,
    created_by TEXT NOT NULL DEFAULT 'manual',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

INSERT OR IGNORE INTO staff_meta (id, description, equipped_skills, active, created_by, created_at)
SELECT id, description, equipped_skills, active, created_by, created_at
FROM profile_meta;

DROP TABLE IF EXISTS profile_meta;
```

- [ ] **Step 4: Rename store types and methods**

In `apps/kittypaw/store/store.go`, replace the old identity metadata type with:

```go
// StaffMeta stores metadata about a switchable staff identity.
type StaffMeta struct {
	ID             string
	Description    string
	EquippedSkills string
	Active         bool
	CreatedBy      string
	CreatedAt      string
}
```

Replace the old identity management section with staff methods that query `staff_meta`:

```go
func (s *Store) UpsertStaffMeta(id, description, equippedSkills, createdBy string) error {
	_, err := s.db.Exec(`
		INSERT INTO staff_meta (id, description, equipped_skills, created_by)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			description     = excluded.description,
			equipped_skills = excluded.equipped_skills,
			created_by      = excluded.created_by`,
		id, description, equippedSkills, createdBy)
	return err
}

func (s *Store) GetStaffMeta(id string) (*StaffMeta, bool, error) {
	var staff StaffMeta
	var active int
	err := s.db.QueryRow(`
		SELECT id, description, equipped_skills, active, created_by, created_at
		FROM staff_meta WHERE id = ?`, id,
	).Scan(&staff.ID, &staff.Description, &staff.EquippedSkills, &active, &staff.CreatedBy, &staff.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	staff.Active = active != 0
	return &staff, true, nil
}

func (s *Store) ListActiveStaff() ([]StaffMeta, error) {
	rows, err := s.db.Query(`
		SELECT id, description, equipped_skills, active, created_by, created_at
		FROM staff_meta
		WHERE active = 1
		ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []StaffMeta
	for rows.Next() {
		var staff StaffMeta
		var active int
		if err := rows.Scan(&staff.ID, &staff.Description, &staff.EquippedSkills, &active, &staff.CreatedBy, &staff.CreatedAt); err != nil {
			return nil, err
		}
		staff.Active = active != 0
		out = append(out, staff)
	}
	return out, rows.Err()
}

func (s *Store) SetStaffActive(id string, active bool) error {
	_, err := s.db.Exec(
		"UPDATE staff_meta SET active = ? WHERE id = ?",
		boolToInt(active), id)
	return err
}

func (s *Store) UpdateEquippedStaffSkills(id, skills string) error {
	_, err := s.db.Exec(
		"UPDATE staff_meta SET equipped_skills = ? WHERE id = ?",
		skills, id)
	return err
}
```

Rename conversation state signatures:

```go
func (s *Store) SaveConversationState(state *core.ConversationState) error
func (s *Store) LoadConversationState() (*core.ConversationState, error)
```

Inside `LoadConversationState`, return:

```go
return &core.ConversationState{
	ConversationID: "account",
	SystemPrompt:   sysPrompt,
	Turns:          turns,
}, nil
```

- [ ] **Step 5: Run store tests and commit**

Run:

```sh
go test ./store -count=1
```

Expected: PASS.

Commit:

```sh
git add apps/kittypaw/store apps/kittypaw/server/account_migrate_integration_test.go
git commit -m "refactor(store): rename identity metadata to staff"
```

## Task 3: Engine And Sandbox Staff/Runner JavaScript Surface

**Files:**

- Modify: `apps/kittypaw/core/skillmeta.go`
- Modify: `apps/kittypaw/sandbox/exec.go`
- Modify: `apps/kittypaw/sandbox/sandbox_test.go`
- Modify: `apps/kittypaw/engine/executor.go`
- Modify: `apps/kittypaw/engine/session.go`
- Modify: `apps/kittypaw/engine/session_test.go`
- Modify: `apps/kittypaw/engine/prompt.go`
- Modify: `apps/kittypaw/engine/prompt_test.go`
- Modify: `apps/kittypaw/engine/commands.go`
- Modify: `apps/kittypaw/engine/commands_test.go`
- Modify: `apps/kittypaw/engine/orchestration.go`
- Modify: `apps/kittypaw/engine/orchestration_test.go`
- Modify: `apps/kittypaw/engine/code_normalize.go`
- Modify: `apps/kittypaw/engine/kanban_tool_test.go`

- [ ] **Step 1: Write failing sandbox and engine surface tests**

In `apps/kittypaw/sandbox/sandbox_test.go`, change observe calls to `Runner.observe` and add a negative assertion that the old global is gone:

```go
func TestRunnerObserveInterrupts(t *testing.T) {
	sb := New(core.SandboxConfig{TimeoutSecs: 2})
	code := `
		const data = "observed data";
		Runner.observe({data: data, label: "search"});
		return "after";
	`
	result, err := sb.ExecuteWithResolver(context.Background(), code, nil, nil)
	if err != nil {
		t.Fatalf("ExecuteWithResolver() error = %v", err)
	}
	if !result.Observe || len(result.Observations) != 1 {
		t.Fatalf("observe result = %+v", result)
	}
	if result.Observations[0].Data != "observed data" || result.Observations[0].Label != "search" {
		t.Fatalf("observation = %+v", result.Observations[0])
	}
	if strings.Contains(result.Output, "after") {
		t.Fatalf("code after Runner.observe executed: %q", result.Output)
	}
}

func TestOldRunnerGlobalRemoved(t *testing.T) {
	sb := New(core.SandboxConfig{TimeoutSecs: 2})
	result, err := sb.ExecuteWithResolver(context.Background(), "return typeof "+"Ag"+"ent"+";", nil, nil)
	if err != nil {
		t.Fatalf("ExecuteWithResolver() error = %v", err)
	}
	if strings.TrimSpace(result.Output) != "undefined" {
		t.Fatalf("old runner global type = %q, want undefined", result.Output)
	}
}
```

In `apps/kittypaw/engine/session_test.go`, change the Staff creation JS:

```go
const created = Staff.create("finance", "재무담당 스태프");
return created.success ? "finance staff created" : created.error;
```

Assert `active_staff:<conversation>` instead of the old active selection key.

- [ ] **Step 2: Run failing focused engine and sandbox tests**

Run:

```sh
go test ./sandbox ./engine -run 'TestRunnerObserve|TestLegacyRunnerGlobalRemoved|TestResolveStaffName|TestStaffSwitch|TestStaffCreate|TestCommandStaff' -count=1
```

Expected: FAIL with unknown `Runner`, unknown `Staff`, or old function names.

- [ ] **Step 3: Rename skill metadata and sandbox observe global**

In `apps/kittypaw/core/skillmeta.go`, replace the old entries with:

```go
	{Name: "Runner", Methods: []SkillMethodMeta{
		{Name: "delegate", Signature: "Runner.delegate(staffId, task, background?) — delegates a task to another staff identity"},
		{Name: "observe", Signature: "Runner.observe({data, label}) — pauses execution and sends data back for analysis. Engine re-calls LLM with observations in context."},
	}},
	{Name: "Staff", Methods: []SkillMethodMeta{
		{Name: "list", Signature: "Staff.list()"},
		{Name: "switch", Signature: "Staff.switch(id)"},
		{Name: "create", Signature: "Staff.create(id, desc)"},
		{Name: "update", Signature: "Staff.update(id, desc)"},
	}},
```

In `apps/kittypaw/sandbox/exec.go`, replace the observe registration block with:

```go
	// --- Runner.observe (VM control flow, not a skill call) ---
	// Registered after SkillRegistry loop so it replaces the resolver stub.
	if runnerVal := vm.Get("Runner"); runnerVal != nil && runnerVal != goja.Undefined() {
		runnerObj := runnerVal.ToObject(vm)
		runnerObj.Set("observe", func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) == 0 {
				panic(vm.ToValue("Runner.observe requires an argument"))
			}
			obs := core.Observation{}
			exported := call.Arguments[0].Export()
			switch v := exported.(type) {
			case map[string]any:
				if d, ok := v["data"].(string); ok {
					obs.Data = d
				} else if b, err := json.Marshal(v["data"]); err == nil {
					obs.Data = string(b)
				}
				if l, ok := v["label"].(string); ok {
					obs.Label = l
				}
			default:
				obs.Data = fmt.Sprintf("%v", exported)
			}
			const maxObsLen = 5000
			if runes := []rune(obs.Data); len(runes) > maxObsLen {
				obs.Data = string(runes[:maxObsLen])
			}
			observations = append(observations, obs)
			vm.Interrupt(observeSignal{})
			return goja.Undefined()
		})
	}
```

- [ ] **Step 4: Rename engine Staff resolver and active staff selection**

In `apps/kittypaw/engine/executor.go`, change `resolveSkillCall` cases:

```go
	case "Staff":
		return executeStaff(ctx, call, s)
	case "Runner":
		return executeRunner(ctx, call, s)
```

Rename the old identity tool executor to `executeStaff` and use:

```go
func executeStaff(ctx context.Context, call core.SkillCall, s *Session) (string, error) {
	switch call.Method {
	case "list":
		staff, err := s.Store.ListActiveStaff()
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"staff": staff})

	case "switch":
		if len(call.Args) == 0 {
			return jsonResult(map[string]any{"error": "staff id required"})
		}
		var id string
		if err := json.Unmarshal(call.Args[0], &id); err != nil {
			return jsonResult(map[string]any{"error": "invalid staff id argument"})
		}
		if err := core.ValidateStaffID(id); err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		base, err := core.ResolveBaseDir(s.BaseDir)
		if err != nil {
			return jsonResult(map[string]any{"error": "config dir: " + err.Error()})
		}
		if _, err := core.LoadStaff(base, id); err != nil {
			return jsonResult(map[string]any{"error": fmt.Sprintf("staff %q not found", id)})
		}
		conversationID := ConversationIDFromContext(ctx)
		if conversationID == "" {
			conversationID = "default"
		}
		key := fmt.Sprintf("active_staff:%s", conversationID)
		if err := s.Store.SetUserContext(key, id, "runner"); err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"success": true, "staff": id})

	case "create":
		if len(call.Args) < 2 {
			return jsonResult(map[string]any{"error": "id and description required"})
		}
		var id, desc string
		if err := json.Unmarshal(call.Args[0], &id); err != nil {
			return jsonResult(map[string]any{"error": "invalid id argument"})
		}
		if err := json.Unmarshal(call.Args[1], &desc); err != nil {
			return jsonResult(map[string]any{"error": "invalid description argument"})
		}
		if err := core.ValidateStaffID(id); err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		if err := s.Store.UpsertStaffMeta(id, desc, "[]", "runner"); err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"success": true})

	default:
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown Staff method: %s", call.Method)})
	}
}
```

Rename context helpers at the top of `executor.go`:

```go
const ctxKeyConversationID contextKey = "conversationID"

func ContextWithConversationID(ctx context.Context, conversationID string) context.Context {
	return context.WithValue(ctx, ctxKeyConversationID, conversationID)
}

func ConversationIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyConversationID).(string); ok {
		return v
	}
	return ""
}
```

- [ ] **Step 5: Rename session staff resolution and prompt loading**

In `apps/kittypaw/engine/session.go`, rename the old identity resolver to:

```go
// ResolveStaffName determines which staff identity to use for this request.
// Priority: mentionOverride > session override > channel binding > default.
func ResolveStaffName(
	config *core.Config,
	channelType string,
	conversationID string,
	mentionOverride string,
	st *store.Store,
) string {
	if mentionOverride != "" {
		return mentionOverride
	}
	if st != nil {
		key := fmt.Sprintf("active_staff:%s", conversationID)
		if val, ok, err := st.GetUserContext(key); err == nil && ok && val != "" {
			return val
		}
	}
	for _, sc := range config.Staff {
		for _, ch := range sc.Channels {
			if ch == channelType {
				return sc.ID
			}
		}
	}
	if config.DefaultStaff != "" {
		return config.DefaultStaff
	}
	return "default"
}
```

Replace the old prompt identity loader with:

```go
func loadStaffForPrompt(staffID string, config *core.Config, baseDir string) *core.Staff {
	base, err := core.ResolveBaseDir(baseDir)
	if err != nil {
		slog.Warn("failed to get config dir for staff", "error", err)
		return &core.Staff{ID: staffID, Soul: core.Presets["default-assistant"].Soul}
	}
	staff, err := core.LoadStaff(base, staffID)
	if err != nil {
		slog.Warn("failed to load staff", "name", staffID, "error", err)
		return &core.Staff{ID: staffID, Soul: core.Presets["default-assistant"].Soul}
	}
	for _, sc := range config.Staff {
		if sc.ID == staffID {
			staff.Nick = sc.Nick
			break
		}
	}
	return staff
}
```

In the execution loop, use:

```go
staffID := ResolveStaffName(s.Config, channelName, convKey, mentionOverride, s.Store)
staff := loadStaffForPrompt(staffID, s.Config, s.BaseDir)
messages := BuildPrompt(state, eventText, compaction, s.Config, channelName, staff, memoryContext, mcpToolsSection, observations, s.BaseDir)
```

Set context with:

```go
ctx = ContextWithConversationID(ctx, convKey)
```

- [ ] **Step 6: Rename prompt and command text**

In `apps/kittypaw/engine/prompt.go`, update `BuildPrompt` to accept `staff *core.Staff` and change observation guidance to:

```go
sb.WriteString("You previously called Runner.observe(). Analyze these results and write code to produce your response.\n")
sb.WriteString("Do NOT call Runner.observe() again unless you need additional data.\n\n")
```

In `apps/kittypaw/engine/code_normalize.go`, replace the token list segment with:

```go
"Memory.", "Storage.", "Runner.", "Staff.", "Share.",
```

In `apps/kittypaw/engine/commands.go`, replace the old identity command with `/staff`:

```go
	case "/staff":
		if len(parts) > 1 {
			return handleStaff(parts[1], s), true
		}
		return "사용법: /staff <staff-id>", true
```

`handleStaff` should set `active_staff:<conversation>` and return Korean text that contains `staff`:

```go
return fmt.Sprintf("기본 staff를 %q로 변경했습니다.", id)
```

- [ ] **Step 7: Rename delegation to Runner and StaffID**

In `apps/kittypaw/engine/orchestration.go`, rename the old identity fields in task specs:

```go
type PMTaskSpec struct {
	StaffID    string `json:"staff_id"`
	Task       string `json:"task"`
	Background bool   `json:"background"`
}
```

Update delegation execution to load staff:

```go
if err := core.ValidateStaffID(task.StaffID); err != nil {
	result.Result = fmt.Sprintf("invalid staff ID: %s", err)
	return result
}
meta, exists, err := st.GetStaffMeta(task.StaffID)
if err != nil {
	result.Result = fmt.Sprintf("staff lookup failed: %s", err)
	return result
}
if !exists || !meta.Active {
	result.Result = fmt.Sprintf("staff %q not found", task.StaffID)
	return result
}
```

In `executeRunner`, parse `staffID`:

```go
spec := PMTaskSpec{StaffID: staffID, Task: task, Background: background}
```

Return unknown method errors as:

```go
return jsonResult(map[string]any{"error": fmt.Sprintf("unknown Runner method: %s", call.Method)})
```

- [ ] **Step 8: Run focused engine and sandbox tests and commit**

Run:

```sh
go test ./sandbox ./engine -count=1
```

Expected: PASS.

Commit:

```sh
git add apps/kittypaw/core/skillmeta.go apps/kittypaw/sandbox apps/kittypaw/engine
git commit -m "refactor(engine): expose staff and runner tools"
```

## Task 4: Server, Client, CLI, Setup, And Kanban Public Surface

**Files:**

- Move: `apps/kittypaw/server/api_legacy_staff.go` to `apps/kittypaw/server/api_staff.go`
- Modify: `apps/kittypaw/server/server.go`
- Modify: `apps/kittypaw/server/api.go`
- Modify: `apps/kittypaw/client/client.go`
- Modify: `apps/kittypaw/client/client_test.go`
- Modify: `apps/kittypaw/cli/main.go`
- Modify: `apps/kittypaw/cli/main_test.go`
- Modify: `apps/kittypaw/cli/cmd_setup_test.go`
- Modify: `apps/kittypaw/cli/cmd_kanban.go`
- Modify: `apps/kittypaw/engine/kanban_tool_test.go`

- [ ] **Step 1: Write failing server and client tests for `/api/v1/staff`**

In `apps/kittypaw/client/client_test.go`, replace the old identity API tests with:

```go
func TestStaffList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/staff" {
			t.Errorf("path = %q, want /api/v1/staff", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"staff": []any{}})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	res, err := c.StaffList()
	if err != nil {
		t.Fatalf("StaffList() error = %v", err)
	}
	if res["staff"] == nil {
		t.Fatalf("response = %+v, want staff key", res)
	}
}

func TestStaffActivate(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewEncoder(w).Encode(map[string]any{"success": true})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	if _, err := c.StaffActivate("coder", ""); err != nil {
		t.Fatalf("StaffActivate() error = %v", err)
	}
	if gotPath != "/api/v1/staff/coder/activate" {
		t.Fatalf("path = %q, want /api/v1/staff/coder/activate", gotPath)
	}
}
```

- [ ] **Step 2: Run failing client tests**

Run:

```sh
go test ./client -run 'TestStaffList|TestStaffActivate' -count=1
```

Expected: FAIL with undefined `StaffList` and `StaffActivate`.

- [ ] **Step 3: Rename server route handlers**

Run:

```sh
git mv <old-server-identity-api-file> apps/kittypaw/server/api_staff.go
```

In `apps/kittypaw/server/api_staff.go`, rename handlers:

```go
func (s *Server) handleStaffList(w http.ResponseWriter, _ *http.Request)
func (s *Server) handleStaffCreate(w http.ResponseWriter, r *http.Request)
func (s *Server) handleStaffActivate(w http.ResponseWriter, r *http.Request)
```

Use store and core staff functions:

```go
staff, err := s.store.ListActiveStaff()
status := core.StaffPresetStatus(base, sm.ID)
soulPath := filepath.Join(base, "staff", sm.ID, "SOUL.md")
if err := core.ValidateStaffID(req.ID); err != nil
if err := s.store.UpsertStaffMeta(req.ID, req.Description, "[]", "api"); err != nil
if err := core.ApplyStaffPreset(s.session.BaseDir, req.ID, req.PresetID); err != nil
if _, exists, err := s.store.GetStaffMeta(id); err != nil
if err := s.store.SetStaffActive(id, true); err != nil
```

Respond with:

```go
writeJSON(w, http.StatusOK, map[string]any{"staff": entries})
```

In `apps/kittypaw/server/server.go`, replace routes:

```go
			// Staff
			r.Get("/staff", s.handleStaffList)
			r.Post("/staff", s.handleStaffCreate)
			r.Post("/staff/{id}/activate", s.handleStaffActivate)
```

In `apps/kittypaw/server/api.go`, replace old identity count response keys with staff:

```go
"staff": len(s.config.Staff),
```

- [ ] **Step 4: Rename client methods**

In `apps/kittypaw/client/client.go`, replace old identity methods with:

```go
// StaffList returns all staff with preset status.
func (c *Client) StaffList() (map[string]any, error) {
	return c.get("/api/v1/staff")
}

// StaffActivate activates a staff identity by ID, optionally applying a preset first.
func (c *Client) StaffActivate(id, presetID string) (map[string]any, error) {
	var body any
	if presetID != "" {
		body = map[string]string{"preset_id": presetID}
	}
	return c.post("/api/v1/staff/"+url.PathEscape(id)+"/activate", body)
}
```

- [ ] **Step 5: Rename CLI/setup public text and Kanban assignee wording**

In `apps/kittypaw/cli/main.go`, replace:

```go
if err := core.EnsureDefaultStaff(accountBaseDir); err != nil {
	return fmt.Errorf("ensure default staff: %w", err)
}
```

In `apps/kittypaw/cli/cmd_setup_test.go`, assert:

```go
mustExist(t, filepath.Join(root, "accounts", "alice", "staff", "default", "SOUL.md"))
```

In `apps/kittypaw/cli/main_test.go`, replace the old public policy checks with:

```go
if cmd, _, err := root.Find([]string{"persona"}); err == nil && cmd != nil && cmd.Name() == "persona" {
	t.Fatal("root command must not expose persona internals")
}
if cmd, _, err := root.Find([]string{"agent"}); err == nil && cmd != nil && cmd.Name() == "agent" {
	t.Fatal("root command must not expose agent management")
}
```

In `apps/kittypaw/cli/cmd_kanban.go`, update help text:

```go
cmd.Flags().StringVar(&flags.assignee, "assignee", "", "assignee staff ID or name")
```

In `apps/kittypaw/engine/kanban_tool_test.go`, replace test titles and actors:

```go
"title": "Runner task",
"created_by": "runner",
map[string]any{"actor": "runner"}
map[string]any{"author": "runner", "body": "note"}
```

- [ ] **Step 6: Run public surface tests and commit**

Run:

```sh
go test ./server ./client ./cli ./engine -run 'TestStaff|TestCommandStaff|TestSetup|TestKanban' -count=1
```

Expected: PASS or no tests matched in packages without matching test names.

Commit:

```sh
git add apps/kittypaw/server apps/kittypaw/client apps/kittypaw/cli apps/kittypaw/engine/kanban_tool_test.go
git commit -m "refactor(api): rename public identity surface to staff"
```

## Task 5: Documentation Sweep And Full Verification

**Files:**

- Modify: `apps/kittypaw/CLAUDE.md`
- Modify: `apps/kittypaw/TASKS.md`
- Modify: `docs/superpowers/specs/2026-05-07-kanban-*.md`
- Modify: `docs/superpowers/plans/2026-05-07-kanban-*.md`
- Modify: any Go file still returned by the targeted `rg` commands below

- [ ] **Step 1: Search for old domain vocabulary**

Run the old-domain vocabulary scan across `apps/kittypaw`,
`docs/superpowers/specs`, and `docs/superpowers/plans`.

Expected: results remain only for these allowed cases:

```text
HTTP User-Agent
browser profile directories
macOS LaunchAgent
.agents/skills or upstream agent registry paths
OAuth scope literal "profile"
historical migration comments that explicitly describe old data being moved
tests asserting old persona, profile, or agent surfaces are absent
```

- [ ] **Step 2: Update docs with Staff and Runner wording**

For Kanban docs under `docs/superpowers/specs/2026-05-07-kanban-*.md` and `docs/superpowers/plans/2026-05-07-kanban-*.md`, apply these vocabulary replacements by meaning:

```text
old runtime toolset -> runner toolset
old runtime tools -> runner tools
old runtime dispatcher -> runner dispatcher
old identity worker -> staff-assigned runner
old identity-specific LLM worker -> staff-specific runner
old assignee identity -> assignee staff
old created_by runtime literal -> created_by: "runner"
old actor runtime literal -> actor: "runner"
```

In `apps/kittypaw/CLAUDE.md`, replace directory descriptions with:

```text
core/          Types, config, skill management, staff identities/presets, account isolation, WebSocket protocol, setup wizard shared logic
sandbox/       JavaScript execution sandbox (in-process goja VM, Runner.observe interrupts)
engine/        Runner loop (observe + retry), skill executor, HTML-to-Markdown, SearchBackend, compaction, scheduling
```

- [ ] **Step 3: Run targeted package tests**

Run:

```sh
go test ./core ./store ./sandbox ./engine ./server ./client ./cli -count=1
```

Expected: PASS.

- [ ] **Step 4: Run repository short tests and diff checks**

Run:

```sh
go test ./... -short -count=1
git diff --check
```

Expected: both PASS with no whitespace errors.

- [ ] **Step 5: Confirm no disallowed old vocabulary remains**

Run the same old-domain vocabulary scan again.

Expected: every remaining result fits the allowed list from Step 1. If a result describes KittyPaw identity or runtime behavior, replace it with Staff, Runner, Account, or Conversation before committing.

- [ ] **Step 6: Commit docs and final sweep**

Commit:

```sh
git add apps/kittypaw/CLAUDE.md apps/kittypaw/TASKS.md docs/superpowers/specs docs/superpowers/plans apps/kittypaw
git commit -m "docs: align staff and runner vocabulary"
```

## Task 6: Final Review Preparation

**Files:**

- Modify: none unless verification finds a naming miss

- [ ] **Step 1: Check branch status**

Run:

```sh
git status --short --branch
```

Expected: clean worktree on `feature/kanban-dispatch-loop`.

- [ ] **Step 2: Summarize commits**

Run:

```sh
git log --oneline --decorate --max-count=8
```

Expected: includes the Staff/Runner design commit, plan commit if committed, and the implementation commits from Tasks 1 through 5.

- [ ] **Step 3: Request code review**

Use `superpowers:requesting-code-review` after all tests pass. Ask the reviewer to focus on:

```text
Breaking rename completeness
Accidental removal of unrelated platform LaunchAgent/User-Agent/browser profile terms
SQLite profile_meta to staff_meta migration correctness
Staff filesystem migration from profiles/ to staff/
Runner.observe behavior parity with the old observe flow
```
