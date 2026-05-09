# Projects Board Replacement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the user-facing Workspace/Kanban product surface with Projects, Board, Tickets, Jobs, Drivers, project/ticket chats, and Project Brief Drafts for MVP 1.

**Architecture:** Keep the existing account-local SQLite store and web server shape, but introduce new Projects-native store/API/UI/engine surfaces. Reuse the existing file indexer implementation internally as the project file index backend while removing Workspace as a primary product concept from the UI/API. Keep actual Codex/Claude/shell execution out of MVP 1; Jobs are planned/approved records only.

**Tech Stack:** Go, SQLite migrations embedded from `store/migrations/*.sql`, chi HTTP routes, vanilla JS web assets, existing Go test harness, optional e2e build tag for live LLM verification.

---

## Scope And Constraints

- Work in the main worktree because the user explicitly requested no separate branch/worktree.
- Use TDD for behavioral changes: add or replace tests first, run them to verify failure, then implement.
- Preserve old SQLite migration files so existing databases can still open. Add a new migration for Projects schema and stop exposing old Kanban/Workspace routes as primary APIs.
- Do not implement driver process execution in MVP 1. `jobs` can be created, approved, canceled, and inspected, but `start` returns a clear "driver execution is not available in MVP 1" response.
- Do not remove the low-level `workspace_files` and `workspace_fts` tables in this phase. Treat `workspace_id` in those tables as an internal index owner ID that can be a project ID.

## File Map

- Create `store/projects.go`: Projects, tickets, dependencies, actions/messages, brief drafts, jobs, drivers, staff assignment store models and methods.
- Create `store/migrations/027_projects.sql`: Projects-native schema and `conversation_scope`.
- Modify `store/store_test.go`: migration count, conversation scope tests if kept in store-wide tests.
- Create `store/projects_test.go`: store-level TDD coverage for Project/Ticket/Brief/Job/Driver behavior.
- Create `server/api_projects.go`: Projects/Tickets/Jobs/Drivers API handlers and account-store middleware.
- Modify `server/server.go`: remove old Kanban route registration, add Projects routes, keep old Workspace settings routes hidden or make them non-primary.
- Replace `server/api_kanban_test.go` with `server/api_projects_test.go`: API route behavior and old route removal tests.
- Replace `engine/kanban_tool_test.go` with `engine/projects_tool_test.go`: Projects tool behavior.
- Modify `engine/executor.go`: register `Projects` tool and remove `Kanban` from the built-in tool surface.
- Modify `engine/code_normalize.go` and tests: preserve bare `Projects.*` calls, stop special-casing Kanban.
- Create `server/web/projects.js`: Projects home, project board, project chat tab placeholder hook, jobs tab, settings tab, new project picker/confirm, brief draft UI.
- Remove or stop loading `server/web/kanban.js`.
- Modify `server/web/app.js`, `server/web/index.html`, `server/web/style.css`, `server/web/i18n.generated.js`: route `/projects`, load Projects asset, navigation copy.
- Modify `server/web_kanban_test.go` into Projects UI tests or replace with `server/web_projects_test.go`.
- Modify `server/web_chat_test.go`, `server/web_i18n_test.go`, `server/web_settings_script_test.go`: Workspace/Kanban user-facing expectations.
- Modify `server/api_settings.go` and `server/web/settings.js`: remove "Workspaces" as settings primary UI. Keep directory browsing helpers reusable for Project creation.
- Modify `server/account_deps.go` and `server/account_config.go`: seed/index Projects rather than settings Workspaces for startup indexing.
- Modify `engine/indexer.go`, `engine/live_indexer.go`, `engine/watcher.go` only if names leak into user-facing API; otherwise keep internal method names for this phase and call them with project IDs.
- Modify `TASKS.md` and `apps/kittypaw/TASKS.md` after each completed task.

## New Store Contracts

Use these Go names and JSON names consistently:

```go
const (
	ProjectStateActive   = "active"
	ProjectStateArchived = "archived"

	TicketStatusDraft      = "draft"
	TicketStatusBacklog    = "backlog"
	TicketStatusReady      = "ready"
	TicketStatusInProgress = "in_progress"
	TicketStatusBlocked    = "blocked"
	TicketStatusReview     = "review"
	TicketStatusDone       = "done"
	TicketStatusArchived   = "archived"

	JobStatusPlanned  = "planned"
	JobStatusApproved = "approved"
	JobStatusRunning  = "running"
	JobStatusSucceeded = "succeeded"
	JobStatusFailed   = "failed"
	JobStatusCanceled = "canceled"

	JobModeOneShot = "one_shot"
	JobModePTY     = "pty"
	JobModeTmux    = "tmux"
)
```

`Project.Key` is immutable after the first ticket exists. `Project.ID` is an internal `prj_` ID. `Ticket.Key` is user-facing, e.g. `KITTY-001`.

`conversation_scope` stores one row per conversation ID:

```sql
CREATE TABLE IF NOT EXISTS conversation_scope (
    conversation_id TEXT PRIMARY KEY,
    scope_type TEXT NOT NULL CHECK(scope_type IN ('general', 'project', 'ticket')),
    scope_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
```

## Task 0: Baseline And Plan Commit

**Files:**
- Modify: `TASKS.md`
- Modify: `apps/kittypaw/TASKS.md`
- Create: `docs/superpowers/plans/2026-05-09-projects-board-implementation.md`

- [x] **Step 1: Clean stale task tracker state**

Already committed as:

```bash
git show --stat --oneline 1768380
```

Expected: `docs: align task tracker with projects board work`.

- [ ] **Step 2: Commit this implementation plan**

Run:

```bash
git add docs/superpowers/plans/2026-05-09-projects-board-implementation.md TASKS.md
git commit -m "docs(kittypaw): plan projects board implementation"
```

Expected: one docs commit, worktree clean except intentional future implementation changes.

## Task 1: Store Schema And Core Project Lifecycle

**Files:**
- Create: `store/migrations/027_projects.sql`
- Create: `store/projects.go`
- Create: `store/projects_test.go`
- Modify: `store/store_test.go`

- [ ] **Step 1: Write failing migration/schema test**

Add to `store/projects_test.go`:

```go
func TestProjectsMigrationCreatesCoreTables(t *testing.T) {
	st := openTestStore(t)
	for _, table := range []string{
		"projects",
		"project_staff_assignments",
		"project_driver_settings",
		"tickets",
		"ticket_dependencies",
		"ticket_actions",
		"ticket_messages",
		"ticket_staff_assignments",
		"project_brief_drafts",
		"jobs",
		"job_events",
		"driver_definitions",
		"conversation_scope",
	} {
		var name string
		err := st.db.QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
}
```

Run:

```bash
go test ./store -run TestProjectsMigrationCreatesCoreTables -count=1 -v
```

Expected before implementation: FAIL because `projects` is missing.

- [ ] **Step 2: Add Projects schema migration**

Create `store/migrations/027_projects.sql` with the tables from the File Map. Required columns:

```sql
projects(id, key, name, root_path, state, next_ticket_seq, project_conversation_id, created_by, archived_at, created_at, updated_at)
project_staff_assignments(id, project_id, staff_id, role, is_primary, created_at)
project_driver_settings(project_id, default_driver_id, default_mode, default_worktree_policy, autonomy_policy, created_at, updated_at)
tickets(id, project_id, key, title, body, status, priority, labels_json, ticket_conversation_id, created_by, archived_at, created_at, updated_at)
ticket_dependencies(id, project_id, blocker_ticket_id, blocked_ticket_id, type, created_by, created_at)
ticket_actions(id, ticket_id, actor_id, action_type, from_status, to_status, message, metadata_json, created_at)
ticket_messages(id, ticket_id, conversation_id, author_id, body, metadata_json, created_at)
ticket_staff_assignments(id, ticket_id, staff_id, role, is_primary, created_at)
project_brief_drafts(id, project_id, status, title, brief_json, proposed_tickets_json, created_by, committed_at, created_at, updated_at)
driver_definitions(id, display_name, command, supported_modes_json, default_args_json, enabled, created_at, updated_at)
jobs(id, project_id, ticket_id, driver_id, mode, status, worktree_path, branch_name, prompt_summary, prompt_text, result_summary, log_tail, error_excerpt, log_truncated, driver_snapshot_json, created_by, approved_by, started_at, finished_at, created_at, updated_at)
job_events(id, job_id, type, actor_id, message, metadata_json, created_at)
conversation_scope(conversation_id, scope_type, scope_id, created_at, updated_at)
```

Add indexes for project key, ticket key, ticket project/status, jobs ticket/status, and assignments.

- [ ] **Step 3: Update migration count**

In `store/store_test.go`, update `TestOpenAndMigrate` from `27` to `28`.

Run:

```bash
go test ./store -run 'TestOpenAndMigrate|TestProjectsMigrationCreatesCoreTables' -count=1 -v
```

Expected after schema: PASS.

- [ ] **Step 4: Write failing project lifecycle tests**

Add tests:

```go
func TestCreateProjectSetsConversationScopeAndDefaults(t *testing.T)
func TestProjectKeyLocksAfterFirstTicket(t *testing.T)
func TestClassifyProjectFolderEmptyishAndNonEmpty(t *testing.T)
func TestSelectProjectPMUsesMetadataAliasThenDefault(t *testing.T)
```

These tests must assert:

- `CreateProject` canonicalizes `root_path`, creates `project_conversation_id`, stores `conversation_scope(scope_type='project')`.
- `project_driver_settings` defaults to `default_driver_id='codex'`, `default_mode='one_shot'`, `autonomy_policy='edit_and_test'`.
- `UpdateProjectKey` succeeds before first ticket and fails after `CreateTicket`.
- folder with only `.git`, `.DS_Store`, `.gitignore`, `README.md` is `empty-ish`; folder with `go.mod` or `package.json` is `non-empty`.
- PM selection returns staff tagged `project-manager`, then alias `pm`, then `default`.

Run:

```bash
go test ./store -run 'TestCreateProjectSetsConversationScopeAndDefaults|TestProjectKeyLocksAfterFirstTicket|TestClassifyProjectFolderEmptyishAndNonEmpty|TestSelectProjectPMUsesMetadataAliasThenDefault' -count=1 -v
```

Expected before implementation: FAIL because methods are undefined.

- [ ] **Step 5: Implement minimal project store methods**

In `store/projects.go`, implement:

```go
func (s *Store) CreateProject(req CreateProjectRequest) (*Project, error)
func (s *Store) GetProject(idOrKey string) (*Project, error)
func (s *Store) ListProjects(includeArchived bool) ([]Project, error)
func (s *Store) UpdateProjectKey(projectID, key string) (*Project, error)
func (s *Store) SetConversationScope(conversationID, scopeType, scopeID string) error
func (s *Store) ConversationScope(conversationID string) (*ConversationScope, bool, error)
func ClassifyProjectFolder(root string) (ProjectFolderClass, error)
func (s *Store) SelectProjectPM() (string, error)
```

Use `filepath.EvalSymlinks(filepath.Clean(root))` for root path. If `EvalSymlinks` fails because the path does not exist, return an error.

Run:

```bash
go test ./store -run 'TestCreateProjectSetsConversationScopeAndDefaults|TestProjectKeyLocksAfterFirstTicket|TestClassifyProjectFolderEmptyishAndNonEmpty|TestSelectProjectPMUsesMetadataAliasThenDefault' -count=1 -v
```

Expected after implementation: PASS.

- [ ] **Step 6: Commit Task 1**

Run:

```bash
git add store/migrations/027_projects.sql store/projects.go store/projects_test.go store/store_test.go
git commit -m "feat(store): add projects schema and lifecycle"
```

## Task 2: Tickets, Board View, Actions, Brief Drafts, Jobs, Drivers

**Files:**
- Modify: `store/projects.go`
- Modify: `store/projects_test.go`

- [ ] **Step 1: Write failing ticket and board tests**

Add tests:

```go
func TestCreateTicketAllocatesProjectKeyAndConversationScope(t *testing.T)
func TestMoveTicketCreatesStatusAction(t *testing.T)
func TestBoardGroupsTicketsByStatus(t *testing.T)
func TestArchiveTicketUsesArchivedStatusAndTimestamp(t *testing.T)
func TestTicketDependenciesAreExplicitRecords(t *testing.T)
```

Run:

```bash
go test ./store -run 'TestCreateTicketAllocatesProjectKeyAndConversationScope|TestMoveTicketCreatesStatusAction|TestBoardGroupsTicketsByStatus|TestArchiveTicketUsesArchivedStatusAndTimestamp|TestTicketDependenciesAreExplicitRecords' -count=1 -v
```

Expected before implementation: FAIL.

- [ ] **Step 2: Implement tickets and board methods**

Add:

```go
func (s *Store) CreateTicket(req CreateTicketRequest) (*Ticket, error)
func (s *Store) GetTicket(idOrKey string) (*Ticket, error)
func (s *Store) ListTickets(filter TicketListFilter) ([]Ticket, error)
func (s *Store) UpdateTicket(ticketID string, req UpdateTicketRequest) (*Ticket, error)
func (s *Store) MoveTicket(ticketID string, req MoveTicketRequest) (*Ticket, error)
func (s *Store) ArchiveTicket(ticketID, actorID string) (*Ticket, error)
func (s *Store) ListTicketActions(ticketID string) ([]TicketAction, error)
func (s *Store) CreateTicketDependency(req CreateTicketDependencyRequest) (*TicketDependency, error)
func (s *Store) ProjectBoard(projectID string) (*ProjectBoard, error)
```

`CreateTicket` must increment `projects.next_ticket_seq` transactionally and create `ticket_conversation_id = "ticket:" + ticketID`.

Run:

```bash
go test ./store -run 'TestCreateTicketAllocatesProjectKeyAndConversationScope|TestMoveTicketCreatesStatusAction|TestBoardGroupsTicketsByStatus|TestArchiveTicketUsesArchivedStatusAndTimestamp|TestTicketDependenciesAreExplicitRecords' -count=1 -v
```

Expected after implementation: PASS.

- [ ] **Step 3: Write failing brief draft tests**

Add:

```go
func TestCreateAndUpdateProjectBriefDraft(t *testing.T)
func TestCommitProjectBriefDraftCreatesTicketsDependenciesAssignmentsAndMessages(t *testing.T)
func TestCommitProjectBriefDraftIsIdempotentlyRejectedAfterCommit(t *testing.T)
```

Use `brief_json` and `proposed_tickets_json` that include two proposed tickets where the second depends on the first.

Run:

```bash
go test ./store -run 'TestCreateAndUpdateProjectBriefDraft|TestCommitProjectBriefDraftCreatesTicketsDependenciesAssignmentsAndMessages|TestCommitProjectBriefDraftIsIdempotentlyRejectedAfterCommit' -count=1 -v
```

Expected before implementation: FAIL.

- [ ] **Step 4: Implement brief draft methods**

Add:

```go
func (s *Store) CreateProjectBriefDraft(req CreateProjectBriefDraftRequest) (*ProjectBriefDraft, error)
func (s *Store) UpdateProjectBriefDraft(draftID string, req UpdateProjectBriefDraftRequest) (*ProjectBriefDraft, error)
func (s *Store) GetProjectBriefDraft(draftID string) (*ProjectBriefDraft, error)
func (s *Store) ListProjectBriefDrafts(projectID string) ([]ProjectBriefDraft, error)
func (s *Store) CommitProjectBriefDraft(draftID string, actorID string) (*CommitProjectBriefDraftResult, error)
```

Commit behavior:

- creates tickets in `ready` when proposal priority is high, otherwise `backlog`;
- creates dependencies after all tickets exist;
- creates ticket staff assignments when proposal has staff;
- creates one ticket message with PM briefing text;
- records draft `status='committed'` and `committed_at`;
- returns an error if already committed.

Run:

```bash
go test ./store -run 'TestCreateAndUpdateProjectBriefDraft|TestCommitProjectBriefDraftCreatesTicketsDependenciesAssignmentsAndMessages|TestCommitProjectBriefDraftIsIdempotentlyRejectedAfterCommit' -count=1 -v
```

Expected after implementation: PASS.

- [ ] **Step 5: Write failing driver and job tests**

Add:

```go
func TestEnsureDefaultDriversAndListDrivers(t *testing.T)
func TestPlanApproveCancelJobWithoutDriverExecution(t *testing.T)
func TestJobPlanStoresResolvedDriverSnapshot(t *testing.T)
```

Run:

```bash
go test ./store -run 'TestEnsureDefaultDriversAndListDrivers|TestPlanApproveCancelJobWithoutDriverExecution|TestJobPlanStoresResolvedDriverSnapshot' -count=1 -v
```

Expected before implementation: FAIL.

- [ ] **Step 6: Implement driver and job methods**

Add:

```go
func (s *Store) EnsureDefaultDrivers() error
func (s *Store) ListDrivers() ([]DriverDefinition, error)
func (s *Store) UpsertDriver(req UpsertDriverRequest) (*DriverDefinition, error)
func (s *Store) PlanJob(req PlanJobRequest) (*Job, error)
func (s *Store) GetJob(jobID string) (*Job, error)
func (s *Store) ListJobs(filter JobListFilter) ([]Job, error)
func (s *Store) ApproveJob(jobID, actorID string) (*Job, error)
func (s *Store) CancelJob(jobID, actorID, reason string) (*Job, error)
func (s *Store) AddJobEvent(req AddJobEventRequest) (*JobEvent, error)
func (s *Store) ListJobEvents(jobID string) ([]JobEvent, error)
```

Default drivers:

- `codex`: display `Codex`, command `codex`, supported modes `["one_shot"]`
- `claude`: display `Claude`, command `claude`, supported modes `["one_shot"]`
- `shell`: display `Shell`, command `sh`, supported modes `["one_shot"]`

Run:

```bash
go test ./store -run 'TestEnsureDefaultDriversAndListDrivers|TestPlanApproveCancelJobWithoutDriverExecution|TestJobPlanStoresResolvedDriverSnapshot' -count=1 -v
```

Expected after implementation: PASS.

- [ ] **Step 7: Commit Task 2**

Run:

```bash
git add store/projects.go store/projects_test.go
git commit -m "feat(store): add tickets briefs jobs and drivers"
```

## Task 3: Projects API And Old Kanban Route Removal

**Files:**
- Create: `server/api_projects.go`
- Create: `server/api_projects_test.go`
- Modify: `server/server.go`
- Remove or stop compiling: `server/api_kanban_test.go` expectations

- [ ] **Step 1: Write failing API tests**

Create `server/api_projects_test.go` with:

```go
func TestProjectsAPIRequiresAuthAndUsesAccountStore(t *testing.T)
func TestProjectsAPICreateListShowBoard(t *testing.T)
func TestProjectsAPITicketLifecycle(t *testing.T)
func TestProjectsAPIBriefDraftCommit(t *testing.T)
func TestProjectsAPIJobPlanApprovalNoExecutionStart(t *testing.T)
func TestOldKanbanRoutesAreRemoved(t *testing.T)
```

`TestOldKanbanRoutesAreRemoved` must assert:

```go
GET /api/v1/kanban/tasks -> 404
GET /api/v1/projects/{project}/milestones -> 404
```

Run:

```bash
go test ./server -run 'TestProjectsAPI|TestOldKanbanRoutesAreRemoved' -count=1 -v
```

Expected before implementation: FAIL because handlers/routes do not exist and old Kanban routes still exist.

- [ ] **Step 2: Implement API handlers**

In `server/api_projects.go`, implement handlers for:

```text
GET  /api/v1/projects
POST /api/v1/projects
GET  /api/v1/projects/{project}
GET  /api/v1/projects/{project}/board
GET  /api/v1/projects/{project}/brief-drafts
POST /api/v1/projects/{project}/brief-drafts
PATCH /api/v1/projects/{project}/brief-drafts/{draft}
POST /api/v1/projects/{project}/brief-drafts/{draft}/commit

GET  /api/v1/tickets?project={project}
POST /api/v1/tickets
GET  /api/v1/tickets/{ticket}
PATCH /api/v1/tickets/{ticket}
POST /api/v1/tickets/{ticket}/actions
POST /api/v1/tickets/{ticket}/archive
GET  /api/v1/tickets/{ticket}/jobs
POST /api/v1/tickets/{ticket}/jobs/plan

GET  /api/v1/jobs/{job}
POST /api/v1/jobs/{job}/approve
POST /api/v1/jobs/{job}/start
POST /api/v1/jobs/{job}/cancel
GET  /api/v1/jobs/{job}/logs

GET  /api/v1/drivers
POST /api/v1/drivers
PATCH /api/v1/drivers/{driver}
```

Use an auth/store middleware named `requireProjectsAPIAccess`, mirroring the account-store behavior of `requireKanbanAPIAccess`.

`POST /api/v1/jobs/{job}/start` must return HTTP 409 with:

```json
{"error":"driver execution is not available in MVP 1"}
```

- [ ] **Step 3: Wire Projects routes and remove Kanban routes**

In `server/server.go`, replace the Kanban route group with Projects routes. Keep `/api/v1/projects` only for Projects, not Kanban.

Run:

```bash
go test ./server -run 'TestProjectsAPI|TestOldKanbanRoutesAreRemoved' -count=1 -v
```

Expected after implementation: PASS.

- [ ] **Step 4: Commit Task 3**

Run:

```bash
git add server/api_projects.go server/api_projects_test.go server/server.go
git rm server/api_kanban_test.go
git commit -m "feat(server): expose projects tickets jobs and drivers api"
```

## Task 4: Engine Projects Tool And Slash Commands

**Files:**
- Modify: `engine/executor.go`
- Modify: `engine/code_normalize.go`
- Create: `engine/projects_tool_test.go`
- Modify: `engine/code_normalize_test.go`
- Remove: `engine/kanban_tool_test.go`
- Modify: `engine/commands.go`
- Modify: `engine/commands_test.go`

- [ ] **Step 1: Write failing Projects tool tests**

Create `engine/projects_tool_test.go`:

```go
func TestExecuteProjectsCreateShowTicketMoveCommentAndBriefCommit(t *testing.T)
func TestExecuteProjectsPlanJobAndRejectStart(t *testing.T)
func TestExecuteKanbanToolIsUnknown(t *testing.T)
```

The first test should call `resolveSkillCall` with `SkillName: "Projects"` and methods:

```text
list, current, show, listTickets, createTicket, showTicket, moveTicket,
commentTicket, createBriefDraft, updateBriefDraft, commitBriefDraft
```

Run:

```bash
go test ./engine -run 'TestExecuteProjects|TestExecuteKanbanToolIsUnknown' -count=1 -v
```

Expected before implementation: FAIL because `Projects` is unknown and `Kanban` still exists.

- [ ] **Step 2: Implement Projects tool**

In `engine/executor.go`:

- switch `case "Projects": return executeProjects(ctx, call, s)`
- remove `case "Kanban"`
- add methods listed in the spec
- return JSON envelopes with keys matching API names: `project`, `projects`, `ticket`, `tickets`, `draft`, `job`, `success`, `error`
- enforce approval-required operations by creating draft/plan records only; do not start drivers.

Run:

```bash
go test ./engine -run 'TestExecuteProjects|TestExecuteKanbanToolIsUnknown' -count=1 -v
```

Expected after implementation: PASS.

- [ ] **Step 3: Update code normalization**

Update `engine/code_normalize.go` so built-in call preservation includes `Projects.` and no longer includes `Kanban.`.

Update `engine/code_normalize_test.go`:

```go
func TestNormalizeGeneratedCodeKeepsBareProjectsCall(t *testing.T)
func TestNormalizeGeneratedCodeDoesNotSpecialCaseBareKanbanCall(t *testing.T)
```

Run:

```bash
go test ./engine -run 'TestNormalizeGeneratedCodeKeepsBareProjectsCall|TestNormalizeGeneratedCodeDoesNotSpecialCaseBareKanbanCall' -count=1 -v
```

Expected: PASS.

- [ ] **Step 4: Add deterministic slash command parsing**

Add command support in `engine/commands.go`:

```text
/projects
/project current
/project use <key>
/project show <key>
/project new
/project settings
/tickets
/ticket show <key>
/ticket chat <key>
/ticket job <key>
/ticket move <key> <status>
/ticket block <key> <reason>
/ticket done <key>
```

`/project new` returns text instructing the web UI to open the Projects folder picker; direct path is not supported in MVP 1.

Run:

```bash
go test ./engine -run 'TestCommandProject|TestCommandTicket' -count=1 -v
```

Expected after tests and implementation: PASS.

- [ ] **Step 5: Commit Task 4**

Run:

```bash
git add engine/executor.go engine/code_normalize.go engine/projects_tool_test.go engine/code_normalize_test.go engine/commands.go engine/commands_test.go
git rm engine/kanban_tool_test.go
git commit -m "feat(engine): replace kanban tool with projects tool"
```

## Task 5: Projects Web UI And Settings Cleanup

**Files:**
- Create: `server/web/projects.js`
- Modify: `server/web/app.js`
- Modify: `server/web/index.html`
- Modify: `server/web/style.css`
- Modify: `server/web/i18n.generated.js`
- Create: `server/web_projects_test.go`
- Modify: `server/web_i18n_test.go`
- Modify: `server/web_chat_test.go`
- Modify: `server/web/settings.js`
- Modify: `server/web_settings_script_test.go`
- Remove: `server/web/kanban.js`
- Remove: `server/web_kanban_test.go`

- [ ] **Step 1: Write failing static asset and route tests**

Create `server/web_projects_test.go`:

```go
func TestWebIndexLoadsProjectsAssetAndNotKanbanAsset(t *testing.T)
func TestProjectsWebUsesProjectsTicketsJobsDriversAPIs(t *testing.T)
func TestProjectsWebDoesNotUseWorkspaceOrKanbanPrimaryAPIs(t *testing.T)
func TestProjectsWebNewProjectResolvesEditedFolderBeforeSave(t *testing.T)
```

Run:

```bash
go test ./server -run 'TestWebIndexLoadsProjectsAssetAndNotKanbanAsset|TestProjectsWeb' -count=1 -v
```

Expected before implementation: FAIL because `kanban.js` is still loaded and `projects.js` does not exist.

- [ ] **Step 2: Replace Kanban web surface with Projects**

Implement `server/web/projects.js` with:

- `Projects.init(container)`
- `Projects.renderHome()`
- `Projects.showNewProjectForm()`
- `Projects.createProjectFromSelection()`
- `Projects.openProject(projectIDOrKey)`
- `Projects.renderBoard()`
- `Projects.openTicket(ticketIDOrKey)`
- `Projects.renderBriefDraft(draft)`
- API calls only to `/api/v1/projects`, `/api/v1/tickets`, `/api/v1/jobs`, `/api/v1/drivers`

Use existing directory browsing helpers from settings by extracting or duplicating the minimal picker code. The save button must resolve the current text input before saving, matching the earlier review fix.

In `server/web/app.js`:

- `isKanbanSurface` becomes `isProjectsSurface`
- `/projects` starts Projects
- root redirect goes to `/projects` once setup is complete

In `server/web/index.html`, load `/projects.js` and stop loading `/kanban.js`.

Run:

```bash
go test ./server -run 'TestWebIndexLoadsProjectsAssetAndNotKanbanAsset|TestProjectsWeb' -count=1 -v
```

Expected after implementation: PASS.

- [ ] **Step 3: Remove Workspaces primary settings UI**

In `server/web/settings.js`, remove the Workspaces section from the default settings render. Keep reusable directory request methods if Projects uses them.

Update tests so settings no longer asserts a Workspaces management section. Keep low-level `/api/settings/directories` tests.

Run:

```bash
go test ./server -run 'TestWebSettings|TestSettings' -count=1 -v
```

Expected after implementation: PASS.

- [ ] **Step 4: Commit Task 5**

Run:

```bash
git add server/web/projects.js server/web/app.js server/web/index.html server/web/style.css server/web/i18n.generated.js server/web_projects_test.go server/web_i18n_test.go server/web_chat_test.go server/web/settings.js server/web_settings_script_test.go
git rm server/web/kanban.js server/web_kanban_test.go
git commit -m "feat(web): replace kanban workspace UI with projects board"
```

## Task 6: Project Indexing And Conversation Scope Wiring

**Files:**
- Modify: `server/account_deps.go`
- Modify: `server/account_config.go`
- Modify: `server/api_workspace.go`
- Modify: `engine/executor.go`
- Modify: `engine/session.go`
- Modify: `engine/indexer_test.go`
- Create or modify: `server/api_projects_test.go`

- [ ] **Step 1: Write failing startup/index scope tests**

Add tests:

```go
func TestStartupIndexesProjectsNotSettingsWorkspaces(t *testing.T)
func TestProjectChatScopesFileSearchToProjectRoot(t *testing.T)
func TestGeneralChatRequiresProjectChoiceForFileSearch(t *testing.T)
```

Run:

```bash
go test ./server ./engine -run 'TestStartupIndexesProjectsNotSettingsWorkspaces|TestProjectChatScopesFileSearchToProjectRoot|TestGeneralChatRequiresProjectChoiceForFileSearch' -count=1 -v
```

Expected before implementation: FAIL because startup lists workspaces and file search reads workspace registrations.

- [ ] **Step 2: Reuse indexer with project IDs**

Change startup indexing to call `ListProjects(false)` and pass `project.ID` and `project.RootPath` to existing indexer methods. Keep `workspace_files.workspace_id` as the internal column name in this phase.

Change file/search root resolution:

- if conversation scope is `project`, use that project root;
- if scope is `ticket`, load ticket, then project root;
- if no scope, return an error asking the user to choose a project.

Run:

```bash
go test ./server ./engine -run 'TestStartupIndexesProjectsNotSettingsWorkspaces|TestProjectChatScopesFileSearchToProjectRoot|TestGeneralChatRequiresProjectChoiceForFileSearch' -count=1 -v
```

Expected after implementation: PASS.

- [ ] **Step 3: Hide old Workspace API from `/api/v1`**

Remove route registration for:

```text
GET /api/v1/workspaces
POST /api/v1/workspaces
DELETE /api/v1/workspaces/{id}
```

Keep `/api/settings/directories` for picker browsing. If old `/api/settings/workspaces` remains temporarily for compatibility, it must not be reachable from the default UI.

Run:

```bash
go test ./server -run 'TestOldWorkspaceRoutesAreRemoved|TestProjectsAPI' -count=1 -v
```

Expected: old `/api/v1/workspaces` returns 404.

- [ ] **Step 4: Commit Task 6**

Run:

```bash
git add server/account_deps.go server/account_config.go server/api_workspace.go engine/executor.go engine/session.go engine/indexer_test.go server/api_projects_test.go
git commit -m "feat(projects): scope indexing and file tools to projects"
```

## Task 7: E2E And LLM Regression Coverage

**Files:**
- Create or modify: `engine/e2e_projects_test.go`
- Modify: `Makefile`
- Modify: `TASKS.md`
- Modify: `apps/kittypaw/TASKS.md`

- [x] **Step 1: Add deterministic e2e test for staff/project brief flow**

Add a non-live e2e test that uses a fake LLM provider and verifies the chat sequence:

```text
사용자: 우리 대화내용을 보고 pm 을 한사람 채용해주세요.
kittypaw: Staff 기능으로 새 역할을 만들까요?
사용자: 네네
kittypaw: staff 초안 ... 이대로 생성할까요?
사용자: 네
kittypaw: 생성 완료 + project/ticket context remains coherent
```

The test must assert the model does not end with generic "도움이 됐다니 좋아요" after the approval.

Run:

```bash
go test -tags e2e ./engine -run TestE2EProjectsStaffDraftApprovalDoesNotLoseContext -count=1 -v
```

Expected before implementation: FAIL if route/tool context is missing; PASS after prompt/context fixes are in place.

- [x] **Step 2: Add live LLM e2e test gated behind env**

Add:

```go
func TestE2ELiveProjectsStaffDraftReproducesContextualPMRequest(t *testing.T)
```

Skip unless `KITTYPAW_E2E_LIVE=1`. Use the exact regression transcript from the user report and assert:

- "Staff 기능으로 새 역할을 만들까요?" appears without "KittyPaw";
- approval produces a draft or creation action, not generic closing;
- repeated request recognizes existing draft or existing staff coherently.

Update Makefile:

```make
test-e2e-live:
	KITTYPAW_E2E_LIVE=1 KITTYPAW_E2E_ACCOUNT=$(KITTYPAW_E2E_ACCOUNT) go test -tags e2e ./engine -run 'TestE2ELiveStaffDraftReproducesContextualPMRequest|TestE2ELiveProjectsStaffDraftReproducesContextualPMRequest' -v -count=1
```

- [x] **Step 3: Run project-focused verification**

Run:

```bash
go test ./store ./server ./engine -count=1
go test -tags e2e ./engine -run 'TestE2EProjectsStaffDraftApprovalDoesNotLoseContext' -count=1 -v
```

Expected: PASS.

- [x] **Step 4: Commit Task 7**

Run:

```bash
git add engine/e2e_projects_test.go Makefile TASKS.md apps/kittypaw/TASKS.md
git commit -m "test(projects): cover chat led staff and project regressions"
```

## Task 8: Full Verification, Review, Fixes, Release

**Files:**
- Review findings may touch the Projects files from Tasks 1-7. Before editing, run `git status --short` and ignore unrelated user changes.

- [ ] **Step 1: Run full local verification**

Run:

```bash
make test-unit
go test ./server ./store ./engine -count=1
go test -tags e2e ./engine -run 'TestE2EProjectsStaffDraftApprovalDoesNotLoseContext' -count=1 -v
make build
```

Expected: PASS.

- [ ] **Step 2: Run live LLM e2e when credentials are available**

Run:

```bash
KITTYPAW_E2E_LIVE=1 KITTYPAW_E2E_ACCOUNT=jinto go test -tags e2e ./engine -run 'TestE2ELiveProjectsStaffDraftReproducesContextualPMRequest' -count=1 -v
```

Expected: PASS or SKIP with a clear missing-credentials reason.

- [ ] **Step 3: Request review**

Run the repository review flow the user requested. If `/review` returns findings, fix them using TDD where applicable, then rerun affected tests.

- [ ] **Step 4: Final commit if review produced fixes**

Run:

```bash
git status --short
git add store server engine Makefile TASKS.md
git commit -m "fix(projects): address review findings"
```

Expected: clean worktree.

- [ ] **Step 5: Tag and deploy**

After verification and review fixes:

```bash
git tag kittypaw/v0.5.16
git push origin main kittypaw/v0.5.16
```

Use the existing release/deploy workflow for namespaced `kittypaw/v*` tags. Do not create plain `v0.5.16`.

## Self-Review

- Spec coverage: MVP 1 store, API, UI, engine tool, conversation scope, indexing scope, job plan records, route removal, and tests are covered. Actual driver execution, PTY/tmux, full log chunks, hard delete UI, include/exclude editor UI, direct `/project new <path>`, and milestones remain excluded as specified.
- Placeholder scan: no `TBD` or open-ended "handle later" steps. Later phases are explicitly excluded rather than hidden inside vague tasks.
- Type consistency: Project/Ticket/Job/Driver names match the design spec and replace Kanban/Workspace only at user-facing boundaries. Existing internal index column names may remain until a later storage cleanup.
