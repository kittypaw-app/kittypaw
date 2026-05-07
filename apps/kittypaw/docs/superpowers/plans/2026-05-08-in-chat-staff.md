# In-Chat Staff Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Implement guided in-chat staff creation and switching so chat claims match durable `staff_meta` plus `staff/<id>/SOUL.md` state.

**Architecture:** Add store support for display names and aliases, add deterministic staff draft helpers in `engine`, expand `/staff` commands, and add pipeline intents for natural-language opt-in, draft approval, draft cancellation, and post-create switching. Keep direct LLM staff creation from bypassing the draft flow by changing the chat-facing `Staff.create` behavior to create a draft instead of durable staff.

**Tech Stack:** Go, SQLite migrations, existing `store.Store`, `engine.Session`, deterministic pipeline branches, Go unit tests.

---

### Task 1: Store Metadata And Alias Support

**Files:**
- Create: `store/migrations/025_staff_aliases.sql`
- Modify: `store/store.go`
- Modify: `store/store_test.go`

- [x] **Step 1: Write failing store tests**

Add tests that require `display_name` and alias resolution:

```go
func TestStaffMetaDisplayNameAndAliases(t *testing.T) {
    st := openTestStore(t)
    if err := st.UpsertStaffMetaWithDisplayName("dev-pm", "개발 PM", "요구사항 정리", "[]", "test"); err != nil {
        t.Fatalf("UpsertStaffMetaWithDisplayName() error = %v", err)
    }
    if err := st.ReplaceStaffAliases("dev-pm", []string{"개발PM", "개발 PM", "PM"}); err != nil {
        t.Fatalf("ReplaceStaffAliases() error = %v", err)
    }
    meta, ok, err := st.GetStaffMeta("dev-pm")
    if err != nil || !ok {
        t.Fatalf("GetStaffMeta(dev-pm) = ok %v err %v", ok, err)
    }
    if meta.DisplayName != "개발 PM" {
        t.Fatalf("DisplayName = %q, want 개발 PM", meta.DisplayName)
    }
    resolved, ok, err := st.ResolveStaffID("개발PM")
    if err != nil || !ok || resolved != "dev-pm" {
        t.Fatalf("ResolveStaffID(개발PM) = %q ok=%v err=%v, want dev-pm true nil", resolved, ok, err)
    }
    aliases, err := st.ListStaffAliases("dev-pm")
    if err != nil {
        t.Fatalf("ListStaffAliases() error = %v", err)
    }
    if strings.Join(aliases, ",") != "PM,개발 PM,개발PM" {
        t.Fatalf("aliases = %#v", aliases)
    }
}
```

Update migration-count expectations from 25 to 26.

- [x] **Step 2: Run failing test**

Run: `go test ./store -run 'TestOpenAndMigrate|TestStaffMetaDisplayNameAndAliases'`

Expected: fail because migration 025, display name field, and alias methods do not exist.

- [x] **Step 3: Implement minimal store support**

Add migration 025:

```sql
ALTER TABLE staff_meta ADD COLUMN display_name TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS staff_aliases (
    alias TEXT PRIMARY KEY,
    staff_id TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    FOREIGN KEY (staff_id) REFERENCES staff_meta(id) ON DELETE CASCADE
);
```

Add `DisplayName string` to `StaffMeta`, preserve `UpsertStaffMeta` by delegating to `UpsertStaffMetaWithDisplayName`, scan `display_name`, and add `ReplaceStaffAliases`, `ListStaffAliases`, and `ResolveStaffID`.

- [x] **Step 4: Run store tests**

Run: `go test ./store -run 'TestOpenAndMigrate|TestStaffMetaDisplayNameAndAliases|TestStaffMetaCRUD|TestMigrationProfileMetaToStaffMeta'`

Expected: pass.

### Task 2: Draft Helpers And Durable Staff Commit

**Files:**
- Create: `engine/staff_draft.go`
- Modify: `engine/session_test.go`

- [x] **Step 1: Write failing engine draft tests**

Add tests for draft generation, approval commit, post-create switch prompt, and switch confirmation.

Run: `go test ./engine -run 'TestStaffDraft'`

Expected: fail because draft helpers do not exist.

- [x] **Step 2: Implement draft helpers**

Create `engine/staff_draft.go` with:

- `StaffDraft` struct.
- `buildStaffDraft(role, source string) StaffDraft`.
- `savePendingStaffDraft(st *store.Store, conversationID string, draft StaffDraft) error`.
- `loadPendingStaffDraft(st *store.Store, conversationID string) (StaffDraft, bool, error)`.
- `clearPendingStaffDraft(st *store.Store, conversationID string) error`.
- `commitStaffDraft(baseDir string, st *store.Store, draft StaffDraft) error`.
- Korean/English role keyword mapping for `개발PM` -> `dev-pm`, display `개발 PM`.

Commit must write `staff/<id>/SOUL.md`, upsert metadata with display name, and replace aliases.

- [x] **Step 3: Run draft tests**

Run: `go test ./engine -run 'TestStaffDraft'`

Expected: pass.

### Task 3: Slash Command Expansion

**Files:**
- Modify: `engine/commands.go`
- Modify: `engine/commands_test.go`

- [x] **Step 1: Write failing command tests**

Cover:

- `/staff` shows current/list usage.
- `/staff current` reports current staff.
- `/staff list` lists real staff.
- `/staff show dev-pm` shows metadata and SOUL status.
- `/staff use missing` fails and does not set context.
- `/staff hire 개발PM` creates a pending draft but no durable staff.
- `/staff cancel` clears a pending draft.

Run: `go test ./engine -run 'TestSlashStaff'`

Expected: fail because commands do not exist or old `/staff <id>` accepts ghost staff.

- [x] **Step 2: Implement commands**

Update `/staff` parsing to subcommands while keeping `/staff <id>` as a compatibility alias for `/staff use <id>`. Use `Store.ResolveStaffID` and require an active `staff_meta` row for switching. Use draft helpers for `hire` and `cancel`.

- [x] **Step 3: Run command tests**

Run: `go test ./engine -run 'TestSlashStaff'`

Expected: pass.

### Task 4: Natural-Language Staff Pipeline

**Files:**
- Modify: `engine/pipeline.go`
- Modify: `engine/session_test.go`
- Modify: `core/skillmeta.go`
- Modify: `engine/executor.go`

- [x] **Step 1: Write failing natural-language tests**

Cover:

- `개발PM 한 명 만들어줘` asks whether to use KittyPaw Staff and creates no draft.
- Affirmative reply after opt-in creates and shows a pending draft.
- Approval reply commits metadata plus `SOUL.md` and asks whether to use it.
- Affirmative reply after commit switches the conversation.
- `Staff.create("finance", "재무담당 스태프")` from LLM returns a draft-oriented response instead of durable creation.

Run: `go test ./engine -run 'TestStaffNaturalLanguage|TestRunCanCreateStaffFromConversationRequest'`

Expected: fail under current behavior.

- [x] **Step 2: Implement pipeline intents and branches**

Add intent kinds:

```go
IntentStaffCreateRequest
IntentStaffCreateOptIn
IntentStaffDraftApprove
IntentStaffDraftCancel
IntentStaffPostCreateSwitch
```

Add classifier rules using pending draft/opt-in state from `user_context`. Add branches that ask opt-in, create draft, commit draft, cancel draft, and switch after creation.

- [x] **Step 3: Restrict chat-facing `Staff.create`**

Change `Staff.create` to save a pending draft using the current conversation ID and return a response that asks for approval. Keep `Staff.update` as metadata-only for now, but do not let `Staff.create` claim durable creation.

- [x] **Step 4: Run natural-language tests**

Run: `go test ./engine -run 'TestStaffNaturalLanguage|TestRunCanCreateStaffFromConversationRequest'`

Expected: pass.

### Task 5: Regression, Review, Commit, Deploy

**Files:**
- Verify all touched files.

- [x] **Step 1: Run focused tests**

Run: `go test ./store ./engine`

Expected: pass.

- [x] **Step 2: Run broader tests**

Run: `go test ./...`

Expected: pass or report exact pre-existing/non-feature failures.

- [x] **Step 3: Code review**

Review the diff for behavioral regressions, missing rollback, missing tests, and unrelated edits. Fix any important issues and re-run affected tests.

- [x] **Step 4: Commit**

Commit only files related to the staff feature. Do not include the pre-existing `../../TASKS.md` modification.

- [x] **Step 5: Deploy**

Use the repo's deployment path after inspecting available deploy docs/scripts. If deployment is an Enumalabs app deployment, run the appropriate deployment command/tool and verify deployment status/logs.
