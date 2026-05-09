package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestCreateProjectSetsConversationScopeAndDefaults(t *testing.T) {
	st := openTestStore(t)
	root := t.TempDir()

	project, err := st.CreateProject(CreateProjectRequest{
		Key:       "kitty",
		Name:      "KittyPaw",
		RootPath:  root,
		CreatedBy: "alice",
	})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	if project.ID == "" || project.Key != "KITTY" || project.Name != "KittyPaw" {
		t.Fatalf("project identity = %+v, want ID, KITTY, KittyPaw", project)
	}
	canonical, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		t.Fatalf("canonical root: %v", err)
	}
	if project.RootPath != canonical {
		t.Fatalf("RootPath = %q, want %q", project.RootPath, canonical)
	}
	if project.ProjectConversationID == "" {
		t.Fatalf("ProjectConversationID is empty: %+v", project)
	}
	scope, ok, err := st.ConversationScope(project.ProjectConversationID)
	if err != nil || !ok {
		t.Fatalf("ConversationScope() ok=%v err=%v", ok, err)
	}
	if scope.ScopeType != "project" || scope.ScopeID != project.ID {
		t.Fatalf("scope = %+v, want project scope for %s", scope, project.ID)
	}

	var defaultDriverID, defaultMode, worktreePolicy, autonomyPolicy string
	if err := st.db.QueryRow(`
		SELECT default_driver_id, default_mode, default_worktree_policy, autonomy_policy
		FROM project_driver_settings
		WHERE project_id = ?`, project.ID).Scan(&defaultDriverID, &defaultMode, &worktreePolicy, &autonomyPolicy); err != nil {
		t.Fatalf("read driver settings: %v", err)
	}
	if defaultDriverID != "codex" || defaultMode != "one_shot" || worktreePolicy != "preserve" || autonomyPolicy != "edit_and_test" {
		t.Fatalf("driver settings = %q %q %q %q", defaultDriverID, defaultMode, worktreePolicy, autonomyPolicy)
	}
}

func TestProjectKeyLocksAfterFirstTicket(t *testing.T) {
	st := openTestStore(t)
	project, err := st.CreateProject(CreateProjectRequest{
		Key:      "kitty",
		Name:     "KittyPaw",
		RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	renamed, err := st.UpdateProjectKey(project.ID, "paw")
	if err != nil {
		t.Fatalf("UpdateProjectKey(before ticket) error = %v", err)
	}
	if renamed.Key != "PAW" {
		t.Fatalf("renamed key = %q, want PAW", renamed.Key)
	}
	if _, err := st.CreateTicket(CreateTicketRequest{ProjectID: project.ID, Title: "First ticket"}); err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}
	if _, err := st.UpdateProjectKey(project.ID, "new"); err == nil {
		t.Fatal("UpdateProjectKey(after ticket) succeeded, want error")
	}
}

func TestClassifyProjectFolderEmptyishAndNonEmpty(t *testing.T) {
	emptyish := t.TempDir()
	if err := os.Mkdir(filepath.Join(emptyish, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(emptyish, ".DS_Store"), nil, 0o644); err != nil {
		t.Fatalf("write .DS_Store: %v", err)
	}
	if err := os.WriteFile(filepath.Join(emptyish, ".gitignore"), []byte("tmp\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(emptyish, "README.md"), []byte("# Emptyish\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	class, err := ClassifyProjectFolder(emptyish)
	if err != nil {
		t.Fatalf("ClassifyProjectFolder(emptyish) error = %v", err)
	}
	if class != ProjectFolderEmptyish {
		t.Fatalf("emptyish class = %q, want %q", class, ProjectFolderEmptyish)
	}

	nonEmpty := t.TempDir()
	if err := os.WriteFile(filepath.Join(nonEmpty, "go.mod"), []byte("module example.com/app\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	class, err = ClassifyProjectFolder(nonEmpty)
	if err != nil {
		t.Fatalf("ClassifyProjectFolder(nonEmpty) error = %v", err)
	}
	if class != ProjectFolderNonEmpty {
		t.Fatalf("nonEmpty class = %q, want %q", class, ProjectFolderNonEmpty)
	}
}

func TestSelectProjectPMUsesMetadataAliasThenDefault(t *testing.T) {
	st := openTestStore(t)

	pm, err := st.SelectProjectPM()
	if err != nil {
		t.Fatalf("SelectProjectPM(empty) error = %v", err)
	}
	if pm != "default" {
		t.Fatalf("empty PM = %q, want default", pm)
	}

	if err := st.UpsertStaffMetaWithDisplayName("dev-pm", "개발 PM", "요구사항 정리", "[]", "test"); err != nil {
		t.Fatalf("UpsertStaffMetaWithDisplayName(dev-pm): %v", err)
	}
	if err := st.ReplaceStaffAliases("dev-pm", []string{"개발PM", "PM"}); err != nil {
		t.Fatalf("ReplaceStaffAliases(dev-pm): %v", err)
	}
	pm, err = st.SelectProjectPM()
	if err != nil {
		t.Fatalf("SelectProjectPM(alias) error = %v", err)
	}
	if pm != "dev-pm" {
		t.Fatalf("alias PM = %q, want dev-pm", pm)
	}

	if err := st.UpsertStaffMetaWithDisplayName("lead", "Lead", "project planning", `["project-manager"]`, "test"); err != nil {
		t.Fatalf("UpsertStaffMetaWithDisplayName(lead): %v", err)
	}
	pm, err = st.SelectProjectPM()
	if err != nil {
		t.Fatalf("SelectProjectPM(metadata) error = %v", err)
	}
	if pm != "lead" {
		t.Fatalf("metadata PM = %q, want lead", pm)
	}
}

func TestCreateTicketAllocatesProjectKeyAndConversationScope(t *testing.T) {
	st := openTestStore(t)
	project := createProjectForProjectsTest(t, st, "kitty")

	first, err := st.CreateTicket(CreateTicketRequest{ProjectID: project.ID, Title: "First", CreatedBy: "alice"})
	if err != nil {
		t.Fatalf("CreateTicket(first) error = %v", err)
	}
	second, err := st.CreateTicket(CreateTicketRequest{ProjectID: project.ID, Title: "Second", Status: TicketStatusReady, CreatedBy: "alice"})
	if err != nil {
		t.Fatalf("CreateTicket(second) error = %v", err)
	}
	if first.Key != "KITTY-001" || second.Key != "KITTY-002" {
		t.Fatalf("ticket keys = %q %q, want KITTY-001 KITTY-002", first.Key, second.Key)
	}
	if first.Status != TicketStatusBacklog || second.Status != TicketStatusReady {
		t.Fatalf("statuses = %q %q, want backlog ready", first.Status, second.Status)
	}
	scope, ok, err := st.ConversationScope(first.TicketConversationID)
	if err != nil || !ok {
		t.Fatalf("ConversationScope(ticket) ok=%v err=%v", ok, err)
	}
	if scope.ScopeType != "ticket" || scope.ScopeID != first.ID {
		t.Fatalf("ticket scope = %+v, want ticket %s", scope, first.ID)
	}
	actions, err := st.ListTicketActions(first.ID)
	if err != nil {
		t.Fatalf("ListTicketActions() error = %v", err)
	}
	if len(actions) != 1 || actions[0].ActionType != "created" || actions[0].ToStatus != TicketStatusBacklog {
		t.Fatalf("created actions = %+v", actions)
	}
}

func TestMoveTicketCreatesStatusAction(t *testing.T) {
	st := openTestStore(t)
	project := createProjectForProjectsTest(t, st, "kitty")
	ticket, err := st.CreateTicket(CreateTicketRequest{ProjectID: project.ID, Title: "Move me"})
	if err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}

	moved, err := st.MoveTicket(ticket.ID, MoveTicketRequest{
		ActorID: "alice",
		Status:  TicketStatusInProgress,
		Message: "starting",
	})
	if err != nil {
		t.Fatalf("MoveTicket() error = %v", err)
	}
	if moved.Status != TicketStatusInProgress {
		t.Fatalf("moved status = %q, want %q", moved.Status, TicketStatusInProgress)
	}
	actions, err := st.ListTicketActions(ticket.ID)
	if err != nil {
		t.Fatalf("ListTicketActions() error = %v", err)
	}
	last := actions[len(actions)-1]
	if last.ActionType != "status_changed" || last.FromStatus != TicketStatusBacklog || last.ToStatus != TicketStatusInProgress || last.Message != "starting" {
		t.Fatalf("last action = %+v", last)
	}
}

func TestBoardGroupsTicketsByStatus(t *testing.T) {
	st := openTestStore(t)
	project := createProjectForProjectsTest(t, st, "kitty")
	if _, err := st.CreateTicket(CreateTicketRequest{ProjectID: project.ID, Title: "Backlog"}); err != nil {
		t.Fatalf("CreateTicket(backlog) error = %v", err)
	}
	if _, err := st.CreateTicket(CreateTicketRequest{ProjectID: project.ID, Title: "Ready", Status: TicketStatusReady}); err != nil {
		t.Fatalf("CreateTicket(ready) error = %v", err)
	}
	blocked, err := st.CreateTicket(CreateTicketRequest{ProjectID: project.ID, Title: "Blocked"})
	if err != nil {
		t.Fatalf("CreateTicket(blocked) error = %v", err)
	}
	if _, err := st.MoveTicket(blocked.ID, MoveTicketRequest{Status: TicketStatusBlocked, Message: "waiting"}); err != nil {
		t.Fatalf("MoveTicket(blocked) error = %v", err)
	}

	board, err := st.ProjectBoard(project.ID)
	if err != nil {
		t.Fatalf("ProjectBoard() error = %v", err)
	}
	if len(board.Columns[TicketStatusBacklog]) != 1 || len(board.Columns[TicketStatusReady]) != 1 || len(board.Columns[TicketStatusBlocked]) != 1 {
		t.Fatalf("board columns = %+v", board.Columns)
	}
}

func TestArchiveTicketUsesArchivedStatusAndTimestamp(t *testing.T) {
	st := openTestStore(t)
	project := createProjectForProjectsTest(t, st, "kitty")
	ticket, err := st.CreateTicket(CreateTicketRequest{ProjectID: project.ID, Title: "Archive me"})
	if err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}

	archived, err := st.ArchiveTicket(ticket.ID, "alice")
	if err != nil {
		t.Fatalf("ArchiveTicket() error = %v", err)
	}
	if archived.Status != TicketStatusArchived || archived.ArchivedAt == "" {
		t.Fatalf("archived ticket = %+v", archived)
	}
	list, err := st.ListTickets(TicketListFilter{ProjectID: project.ID})
	if err != nil {
		t.Fatalf("ListTickets(default) error = %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("default list = %+v, want archived hidden", list)
	}
	list, err = st.ListTickets(TicketListFilter{ProjectID: project.ID, IncludeArchived: true})
	if err != nil {
		t.Fatalf("ListTickets(include archived) error = %v", err)
	}
	if len(list) != 1 || list[0].Status != TicketStatusArchived {
		t.Fatalf("include archived list = %+v", list)
	}
}

func TestTicketDependenciesAreExplicitRecords(t *testing.T) {
	st := openTestStore(t)
	project := createProjectForProjectsTest(t, st, "kitty")
	blocker, err := st.CreateTicket(CreateTicketRequest{ProjectID: project.ID, Title: "Blocker"})
	if err != nil {
		t.Fatalf("CreateTicket(blocker) error = %v", err)
	}
	blocked, err := st.CreateTicket(CreateTicketRequest{ProjectID: project.ID, Title: "Blocked"})
	if err != nil {
		t.Fatalf("CreateTicket(blocked) error = %v", err)
	}

	dep, err := st.CreateTicketDependency(CreateTicketDependencyRequest{
		ProjectID:       project.ID,
		BlockerTicketID: blocker.ID,
		BlockedTicketID: blocked.ID,
		Type:            "blocks",
		CreatedBy:       "alice",
	})
	if err != nil {
		t.Fatalf("CreateTicketDependency() error = %v", err)
	}
	if dep.ProjectID != project.ID || dep.BlockerTicketID != blocker.ID || dep.BlockedTicketID != blocked.ID || dep.Type != "blocks" {
		t.Fatalf("dependency = %+v", dep)
	}
	deps, err := st.ListTicketDependencies(project.ID)
	if err != nil {
		t.Fatalf("ListTicketDependencies() error = %v", err)
	}
	if len(deps) != 1 || deps[0].ID != dep.ID {
		t.Fatalf("dependencies = %+v, want %+v", deps, dep)
	}
}

func TestCreateAndUpdateProjectBriefDraft(t *testing.T) {
	st := openTestStore(t)
	project := createProjectForProjectsTest(t, st, "kitty")

	draft, err := st.CreateProjectBriefDraft(CreateProjectBriefDraftRequest{
		ProjectID:           project.ID,
		Title:               "Initial brief",
		BriefJSON:           `{"summary":"initial"}`,
		ProposedTicketsJSON: `[]`,
		CreatedBy:           "pm",
	})
	if err != nil {
		t.Fatalf("CreateProjectBriefDraft() error = %v", err)
	}
	if draft.ID == "" || draft.Status != "draft" || draft.Title != "Initial brief" {
		t.Fatalf("draft = %+v", draft)
	}

	updated, err := st.UpdateProjectBriefDraft(draft.ID, UpdateProjectBriefDraftRequest{
		Title:               stringPtr("Updated brief"),
		BriefJSON:           stringPtr(`{"summary":"updated"}`),
		ProposedTicketsJSON: stringPtr(`[{"temp_id":"a","title":"A","priority":5}]`),
	})
	if err != nil {
		t.Fatalf("UpdateProjectBriefDraft() error = %v", err)
	}
	if updated.Title != "Updated brief" || updated.BriefJSON != `{"summary":"updated"}` {
		t.Fatalf("updated draft = %+v", updated)
	}
}

func TestCommitProjectBriefDraftCreatesTicketsDependenciesAssignmentsAndMessages(t *testing.T) {
	st := openTestStore(t)
	project := createProjectForProjectsTest(t, st, "kitty")

	draft, err := st.CreateProjectBriefDraft(CreateProjectBriefDraftRequest{
		ProjectID: project.ID,
		Title:     "Repo brief",
		BriefJSON: `{"summary":"repo scan"}`,
		ProposedTicketsJSON: `[
			{"temp_id":"scan","title":"Scan repo","body":"Summarize structure","priority":9,"staff_id":"dev-pm","staff_role":"developer"},
			{"temp_id":"fix","title":"Fix bug","body":"Address bug","priority":3,"dependencies":[{"blocker_temp_id":"scan","type":"blocks"}]}
		]`,
		CreatedBy: "pm",
	})
	if err != nil {
		t.Fatalf("CreateProjectBriefDraft() error = %v", err)
	}

	result, err := st.CommitProjectBriefDraft(draft.ID, "pm")
	if err != nil {
		t.Fatalf("CommitProjectBriefDraft() error = %v", err)
	}
	if len(result.Tickets) != 2 {
		t.Fatalf("committed tickets = %+v, want 2", result.Tickets)
	}
	if result.Tickets[0].Status != TicketStatusReady || result.Tickets[1].Status != TicketStatusBacklog {
		t.Fatalf("ticket statuses = %+v, want ready/backlog", result.Tickets)
	}
	deps, err := st.ListTicketDependencies(project.ID)
	if err != nil {
		t.Fatalf("ListTicketDependencies() error = %v", err)
	}
	if len(deps) != 1 || deps[0].BlockerTicketID != result.Tickets[0].ID || deps[0].BlockedTicketID != result.Tickets[1].ID {
		t.Fatalf("dependencies = %+v, tickets = %+v", deps, result.Tickets)
	}
	var assignmentCount int
	if err := st.db.QueryRow("SELECT COUNT(*) FROM ticket_staff_assignments WHERE ticket_id = ? AND staff_id = 'dev-pm' AND role = 'developer'", result.Tickets[0].ID).Scan(&assignmentCount); err != nil {
		t.Fatalf("count staff assignments: %v", err)
	}
	if assignmentCount != 1 {
		t.Fatalf("assignment count = %d, want 1", assignmentCount)
	}
	var messageCount int
	if err := st.db.QueryRow("SELECT COUNT(*) FROM ticket_messages WHERE ticket_id IN (?, ?)", result.Tickets[0].ID, result.Tickets[1].ID).Scan(&messageCount); err != nil {
		t.Fatalf("count ticket messages: %v", err)
	}
	if messageCount != 2 {
		t.Fatalf("ticket message count = %d, want 2", messageCount)
	}
	committed, err := st.GetProjectBriefDraft(draft.ID)
	if err != nil {
		t.Fatalf("GetProjectBriefDraft() error = %v", err)
	}
	if committed.Status != "committed" || committed.CommittedAt == "" {
		t.Fatalf("committed draft = %+v", committed)
	}
}

func TestCommitProjectBriefDraftIsIdempotentlyRejectedAfterCommit(t *testing.T) {
	st := openTestStore(t)
	project := createProjectForProjectsTest(t, st, "kitty")
	draft, err := st.CreateProjectBriefDraft(CreateProjectBriefDraftRequest{
		ProjectID:           project.ID,
		Title:               "One",
		BriefJSON:           `{}`,
		ProposedTicketsJSON: `[{"temp_id":"a","title":"A"}]`,
	})
	if err != nil {
		t.Fatalf("CreateProjectBriefDraft() error = %v", err)
	}
	if _, err := st.CommitProjectBriefDraft(draft.ID, "pm"); err != nil {
		t.Fatalf("CommitProjectBriefDraft(first) error = %v", err)
	}
	if _, err := st.CommitProjectBriefDraft(draft.ID, "pm"); err == nil {
		t.Fatal("CommitProjectBriefDraft(second) succeeded, want error")
	}
}

func TestEnsureDefaultDriversAndListDrivers(t *testing.T) {
	st := openTestStore(t)
	if err := st.EnsureDefaultDrivers(); err != nil {
		t.Fatalf("EnsureDefaultDrivers() error = %v", err)
	}
	drivers, err := st.ListDrivers()
	if err != nil {
		t.Fatalf("ListDrivers() error = %v", err)
	}
	if len(drivers) != 3 {
		t.Fatalf("drivers = %+v, want 3 defaults", drivers)
	}
	byID := map[string]DriverDefinition{}
	for _, driver := range drivers {
		byID[driver.ID] = driver
	}
	for _, id := range []string{"codex", "claude", "shell"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("driver %s missing from %+v", id, drivers)
		}
	}
	if byID["codex"].DisplayName != "Codex" || byID["codex"].Command != "codex" || !byID["codex"].Enabled {
		t.Fatalf("codex driver = %+v", byID["codex"])
	}
	if byID["codex"].SupportedModesJSON != `["one_shot"]` {
		t.Fatalf("codex modes = %s", byID["codex"].SupportedModesJSON)
	}
}

func TestPlanApproveCancelJobWithoutDriverExecution(t *testing.T) {
	st := openTestStore(t)
	project := createProjectForProjectsTest(t, st, "kitty")
	ticket, err := st.CreateTicket(CreateTicketRequest{ProjectID: project.ID, Title: "Implement feature"})
	if err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}
	if err := st.EnsureDefaultDrivers(); err != nil {
		t.Fatalf("EnsureDefaultDrivers() error = %v", err)
	}

	job, err := st.PlanJob(PlanJobRequest{
		ProjectID:     project.ID,
		TicketID:      ticket.ID,
		DriverID:      "codex",
		Mode:          JobModeOneShot,
		PromptSummary: "Implement feature",
		PromptText:    "Please implement the feature.",
		CreatedBy:     "pm",
	})
	if err != nil {
		t.Fatalf("PlanJob() error = %v", err)
	}
	if job.Status != JobStatusPlanned || job.DriverID != "codex" || job.Mode != JobModeOneShot {
		t.Fatalf("planned job = %+v", job)
	}
	approved, err := st.ApproveJob(job.ID, "alice")
	if err != nil {
		t.Fatalf("ApproveJob() error = %v", err)
	}
	if approved.Status != JobStatusApproved || approved.ApprovedBy != "alice" {
		t.Fatalf("approved job = %+v", approved)
	}
	canceled, err := st.CancelJob(job.ID, "alice", "not now")
	if err != nil {
		t.Fatalf("CancelJob() error = %v", err)
	}
	if canceled.Status != JobStatusCanceled || canceled.FinishedAt == "" {
		t.Fatalf("canceled job = %+v", canceled)
	}
	events, err := st.ListJobEvents(job.ID)
	if err != nil {
		t.Fatalf("ListJobEvents() error = %v", err)
	}
	if len(events) != 3 || events[0].Type != "planned" || events[1].Type != "approved" || events[2].Type != "canceled" {
		t.Fatalf("job events = %+v", events)
	}
}

func TestProjectJobRuntimeSchemaAddsExitCodeAndRunningGuard(t *testing.T) {
	st := openTestStore(t)
	project := createProjectForProjectsTest(t, st, "kitty")
	ticket, err := st.CreateTicket(CreateTicketRequest{ProjectID: project.ID, Title: "Run me"})
	if err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}
	if err := st.EnsureDefaultDrivers(); err != nil {
		t.Fatalf("EnsureDefaultDrivers() error = %v", err)
	}
	first := planApprovedJobForProjectsTest(t, st, project.ID, ticket.ID, "First")
	second := planApprovedJobForProjectsTest(t, st, project.ID, ticket.ID, "Second")

	started, err := st.StartJob(first.ID, StartJobRequest{
		ActorID:      "pm",
		WorktreePath: "/tmp/kittypaw/job-1",
		BranchName:   "kittypaw/KITTY-001/job-1",
	})
	if err != nil {
		t.Fatalf("StartJob(first) error = %v", err)
	}
	if started.Status != JobStatusRunning || started.ExitCode != 0 {
		t.Fatalf("started first = %+v, want running exit_code 0", started)
	}
	if _, err := st.StartJob(second.ID, StartJobRequest{
		ActorID:      "pm",
		WorktreePath: "/tmp/kittypaw/job-2",
		BranchName:   "kittypaw/KITTY-001/job-2",
	}); !IsProjectJobError(err, ProjectJobErrTicketHasRunningJob) {
		t.Fatalf("StartJob(second) error = %v, want %s", err, ProjectJobErrTicketHasRunningJob)
	}
}

func TestProjectJobLifecycleMovesTicketByOutcome(t *testing.T) {
	st := openTestStore(t)
	project := createProjectForProjectsTest(t, st, "kitty")
	ticket, err := st.CreateTicket(CreateTicketRequest{ProjectID: project.ID, Title: "Lifecycle"})
	if err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}
	if err := st.EnsureDefaultDrivers(); err != nil {
		t.Fatalf("EnsureDefaultDrivers() error = %v", err)
	}

	success := planApprovedJobForProjectsTest(t, st, project.ID, ticket.ID, "Success")
	if _, err := st.StartJob(success.ID, StartJobRequest{ActorID: "pm", WorktreePath: "/tmp/success", BranchName: "kittypaw/KITTY-001/success"}); err != nil {
		t.Fatalf("StartJob(success) error = %v", err)
	}
	done, err := st.SucceedJob(success.ID, FinishJobRequest{
		ActorID:       "runtime",
		ResultSummary: "implemented",
		LogTail:       "ok",
		ExitCode:      0,
		MetadataJSON:  `{"exit_code":0}`,
	})
	if err != nil {
		t.Fatalf("SucceedJob() error = %v", err)
	}
	if done.Status != JobStatusSucceeded || done.ExitCode != 0 {
		t.Fatalf("succeeded job = %+v", done)
	}
	ticket, err = st.GetTicket(ticket.ID)
	if err != nil {
		t.Fatalf("GetTicket(after success) error = %v", err)
	}
	if ticket.Status != TicketStatusReview {
		t.Fatalf("ticket status after success = %q, want review", ticket.Status)
	}

	failure := planApprovedJobForProjectsTest(t, st, project.ID, ticket.ID, "Failure")
	if _, err := st.StartJob(failure.ID, StartJobRequest{ActorID: "pm", WorktreePath: "/tmp/failure", BranchName: "kittypaw/KITTY-001/failure"}); err != nil {
		t.Fatalf("StartJob(failure) error = %v", err)
	}
	failed, err := st.FailJob(failure.ID, FinishJobRequest{
		ActorID:       "runtime",
		ResultSummary: "failed",
		LogTail:       "bad",
		ErrorExcerpt:  "exit status 2",
		ExitCode:      2,
		MetadataJSON:  `{"exit_code":2}`,
	})
	if err != nil {
		t.Fatalf("FailJob() error = %v", err)
	}
	if failed.Status != JobStatusFailed || failed.ExitCode != 2 {
		t.Fatalf("failed job = %+v", failed)
	}
	ticket, err = st.GetTicket(ticket.ID)
	if err != nil {
		t.Fatalf("GetTicket(after failure) error = %v", err)
	}
	if ticket.Status != TicketStatusBlocked {
		t.Fatalf("ticket status after failure = %q, want blocked", ticket.Status)
	}
}

func TestCancelRunningProjectJobMovesTicketBacklog(t *testing.T) {
	st := openTestStore(t)
	project := createProjectForProjectsTest(t, st, "kitty")
	ticket, err := st.CreateTicket(CreateTicketRequest{ProjectID: project.ID, Title: "Cancel"})
	if err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}
	if err := st.EnsureDefaultDrivers(); err != nil {
		t.Fatalf("EnsureDefaultDrivers() error = %v", err)
	}
	job := planApprovedJobForProjectsTest(t, st, project.ID, ticket.ID, "Cancel")
	if _, err := st.StartJob(job.ID, StartJobRequest{ActorID: "pm", WorktreePath: "/tmp/cancel", BranchName: "kittypaw/KITTY-001/cancel"}); err != nil {
		t.Fatalf("StartJob() error = %v", err)
	}
	canceled, err := st.CancelJob(job.ID, "alice", "stop requested")
	if err != nil {
		t.Fatalf("CancelJob() error = %v", err)
	}
	if canceled.Status != JobStatusCanceled || canceled.FinishedAt == "" {
		t.Fatalf("canceled = %+v", canceled)
	}
	ticket, err = st.GetTicket(ticket.ID)
	if err != nil {
		t.Fatalf("GetTicket() error = %v", err)
	}
	if ticket.Status != TicketStatusBacklog {
		t.Fatalf("ticket status = %q, want backlog", ticket.Status)
	}
}

func TestMarkRunningProjectJobsFailedOnStartup(t *testing.T) {
	st := openTestStore(t)
	project := createProjectForProjectsTest(t, st, "kitty")
	ticket, err := st.CreateTicket(CreateTicketRequest{ProjectID: project.ID, Title: "Interrupted"})
	if err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}
	if err := st.EnsureDefaultDrivers(); err != nil {
		t.Fatalf("EnsureDefaultDrivers() error = %v", err)
	}
	job := planApprovedJobForProjectsTest(t, st, project.ID, ticket.ID, "Interrupted")
	if _, err := st.StartJob(job.ID, StartJobRequest{ActorID: "pm", WorktreePath: "/tmp/interrupted", BranchName: "kittypaw/KITTY-001/interrupted"}); err != nil {
		t.Fatalf("StartJob() error = %v", err)
	}
	count, err := st.MarkRunningJobsFailedOnStartup("daemon stopped while the job was running")
	if err != nil {
		t.Fatalf("MarkRunningJobsFailedOnStartup() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	got, err := st.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if got.Status != JobStatusFailed || !strings.Contains(got.ErrorExcerpt, "daemon stopped") {
		t.Fatalf("job after startup recovery = %+v", got)
	}
}

func TestListJobEventsKeepsInsertionOrderWhenTimestampsTie(t *testing.T) {
	st := openTestStore(t)
	project := createProjectForProjectsTest(t, st, "kitty")
	ticket, err := st.CreateTicket(CreateTicketRequest{ProjectID: project.ID, Title: "Tie events"})
	if err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}
	if err := st.EnsureDefaultDrivers(); err != nil {
		t.Fatalf("EnsureDefaultDrivers() error = %v", err)
	}
	job, err := st.PlanJob(PlanJobRequest{
		ProjectID:     project.ID,
		TicketID:      ticket.ID,
		DriverID:      "codex",
		Mode:          JobModeOneShot,
		PromptSummary: "Tie events",
		PromptText:    "Tie events",
		CreatedBy:     "pm",
	})
	if err != nil {
		t.Fatalf("PlanJob() error = %v", err)
	}
	if _, err := st.db.Exec(`DELETE FROM job_events WHERE job_id = ?`, job.ID); err != nil {
		t.Fatalf("delete job events: %v", err)
	}
	const tiedAt = "2026-05-09T00:00:00Z"
	for _, id := range []string{"jev_z_inserted_first", "jev_a_inserted_second"} {
		if _, err := st.db.Exec(`
			INSERT INTO job_events (id, job_id, type, actor_id, message, metadata_json, created_at)
			VALUES (?, ?, ?, ?, ?, '{}', ?)`,
			id, job.ID, id, "test", id, tiedAt); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}

	events, err := st.ListJobEvents(job.ID)
	if err != nil {
		t.Fatalf("ListJobEvents() error = %v", err)
	}
	if len(events) != 2 || events[0].ID != "jev_z_inserted_first" || events[1].ID != "jev_a_inserted_second" {
		t.Fatalf("events = %+v, want insertion order for tied timestamps", events)
	}
}

func TestListJobEventsKeepsInsertionOrderWhenTimestampTextSortsDifferently(t *testing.T) {
	st := openTestStore(t)
	project := createProjectForProjectsTest(t, st, "kitty")
	ticket, err := st.CreateTicket(CreateTicketRequest{ProjectID: project.ID, Title: "Fraction events"})
	if err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}
	if err := st.EnsureDefaultDrivers(); err != nil {
		t.Fatalf("EnsureDefaultDrivers() error = %v", err)
	}
	job, err := st.PlanJob(PlanJobRequest{
		ProjectID:     project.ID,
		TicketID:      ticket.ID,
		DriverID:      "codex",
		Mode:          JobModeOneShot,
		PromptSummary: "Fraction events",
		PromptText:    "Fraction events",
		CreatedBy:     "pm",
	})
	if err != nil {
		t.Fatalf("PlanJob() error = %v", err)
	}
	if _, err := st.db.Exec(`DELETE FROM job_events WHERE job_id = ?`, job.ID); err != nil {
		t.Fatalf("delete job events: %v", err)
	}
	rows := []struct {
		id        string
		createdAt string
	}{
		{"jev_inserted_first", "2026-05-09T00:00:00.1234Z"},
		{"jev_inserted_second", "2026-05-09T00:00:00.123499Z"},
	}
	for _, row := range rows {
		if _, err := st.db.Exec(`
			INSERT INTO job_events (id, job_id, type, actor_id, message, metadata_json, created_at)
			VALUES (?, ?, ?, ?, ?, '{}', ?)`,
			row.id, job.ID, row.id, "test", row.id, row.createdAt); err != nil {
			t.Fatalf("insert %s: %v", row.id, err)
		}
	}

	events, err := st.ListJobEvents(job.ID)
	if err != nil {
		t.Fatalf("ListJobEvents() error = %v", err)
	}
	if len(events) != 2 || events[0].ID != "jev_inserted_first" || events[1].ID != "jev_inserted_second" {
		t.Fatalf("events = %+v, want insertion order despite timestamp text sort", events)
	}
}

func TestJobPlanStoresResolvedDriverSnapshot(t *testing.T) {
	st := openTestStore(t)
	project := createProjectForProjectsTest(t, st, "kitty")
	ticket, err := st.CreateTicket(CreateTicketRequest{ProjectID: project.ID, Title: "Use custom driver"})
	if err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}
	if _, err := st.UpsertDriver(UpsertDriverRequest{
		ID:                 "custom",
		DisplayName:        "Custom Driver",
		Command:            "custom-driver",
		SupportedModesJSON: `["one_shot","tmux"]`,
		DefaultArgsJSON:    `["--quiet"]`,
		Enabled:            true,
	}); err != nil {
		t.Fatalf("UpsertDriver() error = %v", err)
	}

	job, err := st.PlanJob(PlanJobRequest{
		ProjectID:     project.ID,
		TicketID:      ticket.ID,
		DriverID:      "custom",
		Mode:          JobModeTmux,
		PromptSummary: "Custom plan",
		PromptText:    "Run custom driver.",
	})
	if err != nil {
		t.Fatalf("PlanJob() error = %v", err)
	}
	if !strings.Contains(job.DriverSnapshotJSON, `"display_name":"Custom Driver"`) || !strings.Contains(job.DriverSnapshotJSON, `"command":"custom-driver"`) {
		t.Fatalf("driver snapshot = %s", job.DriverSnapshotJSON)
	}
}

func createProjectForProjectsTest(t *testing.T, st *Store, key string) *Project {
	t.Helper()
	project, err := st.CreateProject(CreateProjectRequest{
		Key:      key,
		Name:     key,
		RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("CreateProject(%s) error = %v", key, err)
	}
	return project
}

func planApprovedJobForProjectsTest(t *testing.T, st *Store, projectID, ticketID, summary string) *Job {
	t.Helper()
	job, err := st.PlanJob(PlanJobRequest{
		ProjectID:     projectID,
		TicketID:      ticketID,
		DriverID:      "codex",
		Mode:          JobModeOneShot,
		PromptSummary: summary,
		PromptText:    "Run " + summary,
		CreatedBy:     "pm",
	})
	if err != nil {
		t.Fatalf("PlanJob(%s) error = %v", summary, err)
	}
	approved, err := st.ApproveJob(job.ID, "pm")
	if err != nil {
		t.Fatalf("ApproveJob(%s) error = %v", summary, err)
	}
	return approved
}

func stringPtr(s string) *string {
	return &s
}
