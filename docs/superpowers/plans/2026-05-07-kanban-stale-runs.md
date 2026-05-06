# Kanban Stale Runs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a read-only stale Kanban Run query that future dispatchers and human operators can use before reclaiming work.

**Architecture:** Add the deterministic stale cutoff query in `store`, expose it through an authenticated read-only server route, then add a thin CLI command that parses durations and prints compact results. Do not add background workers, automatic reclaim, schema migrations, or Web UI changes.

**Tech Stack:** Go store tests, SQLite queries, chi HTTP handlers, Cobra CLI commands.

---

## File Map

- Modify `apps/kittypaw/store/kanban.go`
  - Add `KanbanStaleRun`, `KanbanStaleRunFilter`, and `ListStaleKanbanRuns`.
- Modify `apps/kittypaw/store/kanban_test.go`
  - Add stale cutoff, ordering, project filter, and validation tests.
- Modify `apps/kittypaw/server/api_kanban.go`
  - Add `handleKanbanStaleRunsList`.
- Modify `apps/kittypaw/server/server.go`
  - Register `GET /api/v1/kanban/runs/stale`.
- Modify `apps/kittypaw/server/api_kanban_test.go`
  - Add API stale query and validation tests.
- Modify `apps/kittypaw/cli/cmd_kanban.go`
  - Add `kanban stale` flags, constructor, and run helper.
- Modify `apps/kittypaw/cli/cmd_kanban_test.go`
  - Add command/flag assertions and CLI behavior tests.

---

### Task 1: Store Stale Run Query

**Files:**
- Modify: `apps/kittypaw/store/kanban.go`
- Modify: `apps/kittypaw/store/kanban_test.go`

- [ ] **Step 1: Add failing store tests**

Append this to `apps/kittypaw/store/kanban_test.go`:

```go
func TestKanbanListStaleRunsFiltersOrdersAndLimits(t *testing.T) {
	st := openTestStore(t)
	kitty, err := st.CreateKanbanProject(CreateKanbanProjectRequest{Slug: "kitty", Name: "KittyPaw", RootPath: "/repo/kitty"})
	if err != nil {
		t.Fatalf("CreateKanbanProject kitty: %v", err)
	}
	space, err := st.CreateKanbanProject(CreateKanbanProjectRequest{Slug: "space", Name: "Space", RootPath: "/repo/space"})
	if err != nil {
		t.Fatalf("CreateKanbanProject space: %v", err)
	}

	oldestTask := mustCreateKanbanTaskForStaleTest(t, st, kitty.ID, "Oldest")
	oldTask := mustCreateKanbanTaskForStaleTest(t, st, kitty.ID, "Old")
	freshTask := mustCreateKanbanTaskForStaleTest(t, st, kitty.ID, "Fresh")
	otherProjectTask := mustCreateKanbanTaskForStaleTest(t, st, space.ID, "Other project")
	canceledTask := mustCreateKanbanTaskForStaleTest(t, st, kitty.ID, "Canceled")

	oldestRun := mustClaimKanbanTaskForStaleTest(t, st, oldestTask.ID, "alice")
	oldRun := mustClaimKanbanTaskForStaleTest(t, st, oldTask.ID, "bob")
	freshRun := mustClaimKanbanTaskForStaleTest(t, st, freshTask.ID, "carol")
	otherRun := mustClaimKanbanTaskForStaleTest(t, st, otherProjectTask.ID, "dave")
	canceledRun := mustClaimKanbanTaskForStaleTest(t, st, canceledTask.ID, "erin")

	mustSetKanbanRunHeartbeatForStaleTest(t, st, oldestRun.ID, "2026-05-07T01:00:00Z")
	mustSetKanbanRunHeartbeatForStaleTest(t, st, oldRun.ID, "2026-05-07T01:30:00Z")
	mustSetKanbanRunHeartbeatForStaleTest(t, st, freshRun.ID, "2026-05-07T02:30:00Z")
	mustSetKanbanRunHeartbeatForStaleTest(t, st, otherRun.ID, "2026-05-07T01:15:00Z")
	mustSetKanbanRunHeartbeatForStaleTest(t, st, canceledRun.ID, "2026-05-07T01:10:00Z")
	if _, err := st.CancelKanbanTask(canceledTask.ID, CancelKanbanTaskRequest{Actor: "erin", Reason: "stop"}); err != nil {
		t.Fatalf("CancelKanbanTask: %v", err)
	}

	stale, err := st.ListStaleKanbanRuns(KanbanStaleRunFilter{
		ProjectID:    kitty.ID,
		StaleBefore: "2026-05-07T02:00:00Z",
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("ListStaleKanbanRuns: %v", err)
	}
	if len(stale) != 2 {
		t.Fatalf("stale len = %d, want 2: %+v", len(stale), stale)
	}
	if stale[0].Run.ID != oldestRun.ID || stale[1].Run.ID != oldRun.ID {
		t.Fatalf("stale order = %+v", stale)
	}
	if stale[0].Task.ID != oldestTask.ID || stale[0].ProjectSlug != "kitty" || stale[0].ProjectName != "KittyPaw" {
		t.Fatalf("stale context = %+v", stale[0])
	}

	limited, err := st.ListStaleKanbanRuns(KanbanStaleRunFilter{
		StaleBefore: "2026-05-07T02:00:00Z",
		Limit:       1,
	})
	if err != nil {
		t.Fatalf("ListStaleKanbanRuns limited: %v", err)
	}
	if len(limited) != 1 || limited[0].Run.ID != oldestRun.ID {
		t.Fatalf("limited stale = %+v", limited)
	}
}

func TestKanbanListStaleRunsRequiresCutoff(t *testing.T) {
	st := openTestStore(t)
	if _, err := st.ListStaleKanbanRuns(KanbanStaleRunFilter{}); err == nil {
		t.Fatal("expected missing stale cutoff to fail")
	}
}

func mustCreateKanbanTaskForStaleTest(t *testing.T, st *Store, projectID, title string) *KanbanTask {
	t.Helper()
	task, err := st.CreateKanbanTask(CreateKanbanTaskRequest{
		ProjectID: projectID,
		Title:     title,
		Status:    KanbanStatusTodo,
	})
	if err != nil {
		t.Fatalf("CreateKanbanTask %q: %v", title, err)
	}
	return task
}

func mustClaimKanbanTaskForStaleTest(t *testing.T, st *Store, taskID, actor string) *KanbanRun {
	t.Helper()
	run, err := st.ClaimKanbanTask(taskID, ClaimKanbanTaskRequest{Actor: actor})
	if err != nil {
		t.Fatalf("ClaimKanbanTask %s: %v", taskID, err)
	}
	return run
}

func mustSetKanbanRunHeartbeatForStaleTest(t *testing.T, st *Store, runID, heartbeat string) {
	t.Helper()
	if _, err := st.db.Exec(`UPDATE kanban_task_runs SET heartbeat_at = ? WHERE id = ?`, heartbeat, runID); err != nil {
		t.Fatalf("set run heartbeat %s: %v", runID, err)
	}
}
```

- [ ] **Step 2: Verify the store tests fail**

Run:

```bash
cd apps/kittypaw
go test ./store -run 'TestKanban.*Stale' -count=1
```

Expected: fail because `KanbanStaleRunFilter` and `ListStaleKanbanRuns` are undefined.

- [ ] **Step 3: Add store types and implementation**

In `apps/kittypaw/store/kanban.go`, add this type near `KanbanRun`:

```go
type KanbanStaleRun struct {
	Run         KanbanRun  `json:"run"`
	Task        KanbanTask `json:"task"`
	ProjectSlug string     `json:"project_slug"`
	ProjectName string     `json:"project_name"`
}
```

Add this filter near `KanbanTaskListFilter`:

```go
type KanbanStaleRunFilter struct {
	ProjectID    string
	StaleBefore string
	Limit       int
}
```

Add this method near `ListKanbanRuns`:

```go
func (s *Store) ListStaleKanbanRuns(filter KanbanStaleRunFilter) ([]KanbanStaleRun, error) {
	staleBefore := strings.TrimSpace(filter.StaleBefore)
	if staleBefore == "" {
		return nil, fmt.Errorf("stale before is required")
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	query := `
		SELECT
			r.id, r.task_id, r.actor, r.work_dir, r.work_dir_provider, r.outcome,
			r.summary, r.metadata_json, r.error, r.started_at, r.heartbeat_at, r.finished_at,
			t.id, t.project_id, t.board_id, t.milestone_id, t.title, t.body, t.status,
			t.priority, t.assignee, t.created_by, t.created_at, t.updated_at, t.completed_at,
			p.slug, p.name
		FROM kanban_task_runs r
		JOIN kanban_tasks t ON t.id = r.task_id
		JOIN kanban_projects p ON p.id = t.project_id
		WHERE r.outcome = ? AND t.status = ? AND p.archived = 0 AND r.heartbeat_at < ?`
	args := []any{KanbanRunRunning, KanbanStatusRunning, staleBefore}
	if strings.TrimSpace(filter.ProjectID) != "" {
		query += ` AND t.project_id = ?`
		args = append(args, strings.TrimSpace(filter.ProjectID))
	}
	query += ` ORDER BY r.heartbeat_at ASC, r.started_at ASC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []KanbanStaleRun
	for rows.Next() {
		var item KanbanStaleRun
		var milestone sql.NullString
		if err := rows.Scan(
			&item.Run.ID, &item.Run.TaskID, &item.Run.Actor, &item.Run.WorkDir, &item.Run.WorkDirProvider, &item.Run.Outcome,
			&item.Run.Summary, &item.Run.MetadataJSON, &item.Run.Error, &item.Run.StartedAt, &item.Run.HeartbeatAt, &item.Run.FinishedAt,
			&item.Task.ID, &item.Task.ProjectID, &item.Task.BoardID, &milestone, &item.Task.Title, &item.Task.Body, &item.Task.Status,
			&item.Task.Priority, &item.Task.Assignee, &item.Task.CreatedBy, &item.Task.CreatedAt, &item.Task.UpdatedAt, &item.Task.CompletedAt,
			&item.ProjectSlug, &item.ProjectName,
		); err != nil {
			return nil, err
		}
		if milestone.Valid {
			item.Task.MilestoneID = milestone.String
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run store tests**

Run:

```bash
cd apps/kittypaw
go test ./store -run 'TestKanban.*Stale' -count=1
```

Expected: pass.

- [ ] **Step 5: Commit store work**

Run:

```bash
git add apps/kittypaw/store/kanban.go apps/kittypaw/store/kanban_test.go
git commit -m "feat(store): list stale kanban runs"
```

---

### Task 2: Server Stale Run API

**Files:**
- Modify: `apps/kittypaw/server/api_kanban.go`
- Modify: `apps/kittypaw/server/server.go`
- Modify: `apps/kittypaw/server/api_kanban_test.go`

- [ ] **Step 1: Add failing API tests**

Add `time` to the `apps/kittypaw/server/api_kanban_test.go` imports, then append:

```go
func TestKanbanAPIStaleRunsList(t *testing.T) {
	srv := newKanbanAPITestServer(t)
	kanbanAPICreateProject(t, srv, "kitty")
	kanbanAPICreateProject(t, srv, "space")
	oldTaskID := kanbanAPICreateTask(t, srv, "kitty", "Old run")
	otherTaskID := kanbanAPICreateTask(t, srv, "space", "Other run")

	var oldClaimed, otherClaimed struct {
		Run struct {
			ID string `json:"id"`
		} `json:"run"`
	}
	kanbanAPIRequest(t, srv, http.MethodPost, "/api/v1/kanban/tasks/"+oldTaskID+"/claim", map[string]any{"actor": "alice"}, http.StatusOK, &oldClaimed)
	kanbanAPIRequest(t, srv, http.MethodPost, "/api/v1/kanban/tasks/"+otherTaskID+"/claim", map[string]any{"actor": "carol"}, http.StatusOK, &otherClaimed)
	time.Sleep(2100 * time.Millisecond)

	var listed struct {
		StaleBefore string `json:"stale_before"`
		StaleRuns   []struct {
			Run struct {
				ID      string `json:"id"`
				Actor   string `json:"actor"`
				Outcome string `json:"outcome"`
			} `json:"run"`
			Task struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"task"`
			ProjectSlug string `json:"project_slug"`
		} `json:"stale_runs"`
	}
	kanbanAPIRequest(t, srv, http.MethodGet, "/api/v1/kanban/runs/stale?project=kitty&stale_after=1s&limit=10", nil, http.StatusOK, &listed)
	if listed.StaleBefore == "" {
		t.Fatalf("stale_before is empty: %+v", listed)
	}
	if len(listed.StaleRuns) != 1 || listed.StaleRuns[0].Run.ID != oldClaimed.Run.ID || listed.StaleRuns[0].Run.Actor != "alice" || listed.StaleRuns[0].Run.Outcome != "running" || listed.StaleRuns[0].Task.ID != oldTaskID || listed.StaleRuns[0].ProjectSlug != "kitty" {
		t.Fatalf("stale runs = %+v", listed.StaleRuns)
	}
}

func TestKanbanAPIStaleRunsValidation(t *testing.T) {
	srv := newKanbanAPITestServer(t)
	kanbanAPICreateProject(t, srv, "kitty")

	kanbanAPIRequest(t, srv, http.MethodGet, "/api/v1/kanban/runs/stale", nil, http.StatusBadRequest, nil)
	kanbanAPIRequest(t, srv, http.MethodGet, "/api/v1/kanban/runs/stale?stale_after=0s", nil, http.StatusBadRequest, nil)
	kanbanAPIRequest(t, srv, http.MethodGet, "/api/v1/kanban/runs/stale?stale_after=nope", nil, http.StatusBadRequest, nil)
	kanbanAPIRequest(t, srv, http.MethodGet, "/api/v1/kanban/runs/stale?stale_after=1m&limit=0", nil, http.StatusBadRequest, nil)
	kanbanAPIRequest(t, srv, http.MethodGet, "/api/v1/kanban/runs/stale?stale_after=1m&project=missing", nil, http.StatusNotFound, nil)
}
```

- [ ] **Step 2: Verify API tests fail**

Run:

```bash
cd apps/kittypaw
go test ./server -run 'TestKanbanAPI.*Stale' -count=1
```

Expected: fail because the route and handler are missing.

- [ ] **Step 3: Add API handler**

In `apps/kittypaw/server/api_kanban.go`, add `strconv` to imports and add this handler near `handleKanbanTasksList`:

```go
func (s *Server) handleKanbanStaleRunsList(w http.ResponseWriter, r *http.Request) {
	staleAfterRaw := strings.TrimSpace(r.URL.Query().Get("stale_after"))
	if staleAfterRaw == "" {
		writeError(w, http.StatusBadRequest, "stale_after is required")
		return
	}
	staleAfter, err := time.ParseDuration(staleAfterRaw)
	if err != nil || staleAfter <= 0 {
		writeError(w, http.StatusBadRequest, "positive stale_after duration is required")
		return
	}

	limit := 50
	if limitRaw := strings.TrimSpace(r.URL.Query().Get("limit")); limitRaw != "" {
		parsed, err := strconv.Atoi(limitRaw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "positive limit is required")
			return
		}
		limit = parsed
	}

	projectID := ""
	if projectArg := strings.TrimSpace(r.URL.Query().Get("project")); projectArg != "" {
		project, err := kanbanResolveProject(s.store, projectArg)
		if err != nil {
			kanbanWriteStoreError(w, err)
			return
		}
		projectID = project.ID
	}

	cutoff := time.Now().UTC().Add(-staleAfter).Format("2006-01-02T15:04:05Z")
	staleRuns, err := s.store.ListStaleKanbanRuns(store.KanbanStaleRunFilter{
		ProjectID:    projectID,
		StaleBefore: cutoff,
		Limit:       limit,
	})
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"stale_runs":  kanbanSliceOrEmpty(staleRuns),
		"stale_before": cutoff,
	})
}
```

- [ ] **Step 4: Register the route**

In `apps/kittypaw/server/server.go`, add this route inside the Kanban route group before task routes:

```go
r.Get("/kanban/runs/stale", s.handleKanbanStaleRunsList)
```

- [ ] **Step 5: Run API tests**

Run:

```bash
cd apps/kittypaw
go test ./server -run 'TestKanbanAPI.*Stale' -count=1
```

Expected: pass.

- [ ] **Step 6: Commit API work**

Run:

```bash
git add apps/kittypaw/server/api_kanban.go apps/kittypaw/server/server.go apps/kittypaw/server/api_kanban_test.go
git commit -m "feat(server): expose stale kanban runs"
```

---

### Task 3: CLI Stale Run Command

**Files:**
- Modify: `apps/kittypaw/cli/cmd_kanban.go`
- Modify: `apps/kittypaw/cli/cmd_kanban_test.go`

- [ ] **Step 1: Add failing CLI tests**

Add `time` to the `apps/kittypaw/cli/cmd_kanban_test.go` imports.

Update `TestKanbanCommandExposesTaskWorkflow` to include:

```go
{"kanban", "stale"},
```

Update `TestKanbanCommandFlags` with:

```go
stale := mustFindCommand(t, root, []string{"kanban", "stale"})
for _, flag := range []string{"project", "stale-after", "limit", "account"} {
	if stale.Flag(flag) == nil {
		t.Fatalf("kanban stale missing --%s", flag)
	}
}
```

Append these tests to `apps/kittypaw/cli/cmd_kanban_test.go`:

```go
func TestKanbanStaleListsStaleRuns(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	t.Setenv("KITTYPAW_ACCOUNT", "")
	mustWriteTestConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))

	st, err := openStoreForAccount("alice")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	project, err := st.CreateKanbanProject(store.CreateKanbanProjectRequest{Slug: "kitty", Name: "KittyPaw", RootPath: t.TempDir()})
	if err != nil {
		t.Fatalf("CreateKanbanProject: %v", err)
	}
	task, err := st.CreateKanbanTask(store.CreateKanbanTaskRequest{ProjectID: project.ID, Title: "Investigate stale run", Status: store.KanbanStatusTodo})
	if err != nil {
		t.Fatalf("CreateKanbanTask: %v", err)
	}
	run, err := st.ClaimKanbanTask(task.ID, store.ClaimKanbanTaskRequest{Actor: "alice"})
	if err != nil {
		t.Fatalf("ClaimKanbanTask: %v", err)
	}
	_ = st.Close()
	time.Sleep(2100 * time.Millisecond)

	var runErr error
	out := captureStdout(t, func() {
		runErr = runKanbanStale(&kanbanStaleFlags{
			shared:     &kanbanSharedFlags{accountID: "alice"},
			project:    "kitty",
			staleAfter: "1s",
			limit:      10,
		})
	})
	if runErr != nil {
		t.Fatalf("runKanbanStale: %v", runErr)
	}
	for _, want := range []string{task.ID, run.ID, "kitty", "alice", "Investigate stale run"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stale output = %q, missing %q", out, want)
		}
	}
}

func TestKanbanStaleValidatesDuration(t *testing.T) {
	err := runKanbanStale(&kanbanStaleFlags{
		shared:     &kanbanSharedFlags{accountID: "alice"},
		staleAfter: "0s",
	})
	if err == nil || !strings.Contains(err.Error(), "positive --stale-after") {
		t.Fatalf("runKanbanStale error = %v, want positive stale-after validation", err)
	}
}
```

- [ ] **Step 2: Verify CLI tests fail**

Run:

```bash
cd apps/kittypaw
go test ./cli -run 'TestKanban.*Stale|TestKanbanCommand' -count=1
```

Expected: fail because `kanban stale` and `runKanbanStale` are missing.

- [ ] **Step 3: Add CLI flags and command**

In `apps/kittypaw/cli/cmd_kanban.go`, add this flag struct near `kanbanListFlags`:

```go
type kanbanStaleFlags struct {
	shared     *kanbanSharedFlags
	project    string
	staleAfter string
	limit      int
}
```

Add `newKanbanStaleCmd(flags)` to `newKanbanCmd()` after `newKanbanListCmd(flags)`.

Add this constructor near `newKanbanListCmd`:

```go
func newKanbanStaleCmd(shared *kanbanSharedFlags) *cobra.Command {
	flags := &kanbanStaleFlags{shared: shared}
	cmd := &cobra.Command{
		Use:   "stale",
		Short: "List stale running Kanban runs",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKanbanStale(flags)
		},
	}
	cmd.Flags().StringVar(&flags.project, "project", "", "project id or slug")
	cmd.Flags().StringVar(&flags.staleAfter, "stale-after", "", "stale duration threshold, for example 10m or 1h")
	cmd.Flags().IntVar(&flags.limit, "limit", 50, "maximum stale runs to list")
	return cmd
}
```

- [ ] **Step 4: Add CLI run helper**

Add this near `runKanbanList`:

```go
func runKanbanStale(flags *kanbanStaleFlags) error {
	if flags == nil {
		flags = &kanbanStaleFlags{shared: &kanbanSharedFlags{}}
	}
	staleAfterRaw := strings.TrimSpace(flags.staleAfter)
	if staleAfterRaw == "" {
		return fmt.Errorf("--stale-after is required")
	}
	staleAfter, err := time.ParseDuration(staleAfterRaw)
	if err != nil || staleAfter <= 0 {
		return fmt.Errorf("positive --stale-after duration is required")
	}
	if flags.limit <= 0 {
		return fmt.Errorf("--limit must be positive")
	}

	st, err := openKanbanCommandStore(kanbanAccountID(flags.shared))
	if err != nil {
		return err
	}
	defer st.Close()

	projectID := ""
	if strings.TrimSpace(flags.project) != "" {
		project, err := resolveKanbanProject(st, flags.project)
		if err != nil {
			return err
		}
		projectID = project.ID
	}
	cutoff := time.Now().UTC().Add(-staleAfter).Format("2006-01-02T15:04:05Z")
	staleRuns, err := st.ListStaleKanbanRuns(store.KanbanStaleRunFilter{
		ProjectID:    projectID,
		StaleBefore: cutoff,
		Limit:       flags.limit,
	})
	if err != nil {
		return err
	}
	if len(staleRuns) == 0 {
		fmt.Println("No stale runs.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TASK\tRUN\tPROJECT\tACTOR\tHEARTBEAT\tTITLE")
	for _, item := range staleRuns {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			item.Task.ID,
			item.Run.ID,
			item.ProjectSlug,
			item.Run.Actor,
			item.Run.HeartbeatAt,
			item.Task.Title,
		)
	}
	return w.Flush()
}
```

- [ ] **Step 5: Run CLI tests**

Run:

```bash
cd apps/kittypaw
go test ./cli -run 'TestKanban.*Stale|TestKanbanCommand' -count=1
```

Expected: pass.

- [ ] **Step 6: Commit CLI work**

Run:

```bash
git add apps/kittypaw/cli/cmd_kanban.go apps/kittypaw/cli/cmd_kanban_test.go
git commit -m "feat(cli): add kanban stale runs"
```

---

### Task 4: Final Verification And Review

**Files:**
- Review all changed files.

- [ ] **Step 1: Run focused verification**

Run:

```bash
cd apps/kittypaw
go test ./store -run 'TestKanban.*Stale' -count=1
go test ./server -run 'TestKanbanAPI.*Stale' -count=1
go test ./cli -run 'TestKanban.*Stale|TestKanbanCommand' -count=1
```

Expected: all pass.

- [ ] **Step 2: Run short package verification**

Run:

```bash
cd apps/kittypaw
go test ./... -short -count=1
```

Expected: pass.

- [ ] **Step 3: Review the diff**

Run:

```bash
git diff main...HEAD -- apps/kittypaw/store/kanban.go apps/kittypaw/server/api_kanban.go apps/kittypaw/server/server.go apps/kittypaw/cli/cmd_kanban.go
```

Check:

- stale query is read-only
- API and CLI validate positive durations
- project filter resolves through existing helpers
- no automatic reclaim or dispatcher behavior was added

- [ ] **Step 4: Commit review fixes if needed**

If review finds issues, fix with a focused failing test first, then run the relevant focused tests and commit:

```bash
git add apps/kittypaw/store/kanban.go apps/kittypaw/store/kanban_test.go apps/kittypaw/server/api_kanban.go apps/kittypaw/server/server.go apps/kittypaw/server/api_kanban_test.go apps/kittypaw/cli/cmd_kanban.go apps/kittypaw/cli/cmd_kanban_test.go
git commit -m "fix: tighten kanban stale runs"
```

- [ ] **Step 5: Final status**

Run:

```bash
git status --short --branch
git log --oneline --max-count=10 main..HEAD
```

Expected: clean branch containing design, plan, store, server, and CLI commits.
