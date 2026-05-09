package engine

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/store"
)

func TestProjectChatScopesFileSearchToProjectRoot(t *testing.T) {
	st := openTestStore(t)
	rootA := t.TempDir()
	rootB := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootA, "a.txt"), []byte("sharedtoken project-a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootB, "b.txt"), []byte("sharedtoken project-b"), 0o644); err != nil {
		t.Fatal(err)
	}
	projectA, err := st.CreateProject(store.CreateProjectRequest{Key: "a", Name: "A", RootPath: rootA})
	if err != nil {
		t.Fatalf("CreateProject A: %v", err)
	}
	projectB, err := st.CreateProject(store.CreateProjectRequest{Key: "b", Name: "B", RootPath: rootB})
	if err != nil {
		t.Fatalf("CreateProject B: %v", err)
	}
	indexer := NewFTS5Indexer(st)
	if _, err := indexer.Index(context.Background(), projectA.ID, projectA.RootPath); err != nil {
		t.Fatalf("index A: %v", err)
	}
	if _, err := indexer.Index(context.Background(), projectB.ID, projectB.RootPath); err != nil {
		t.Fatalf("index B: %v", err)
	}

	sess := &Session{Store: st, Config: fullAccessConfig(), Indexer: indexer}
	result := resolveFileSearchForTest(t, sess, projectA.ProjectConversationID, "sharedtoken")

	if !strings.Contains(result, "a.txt") {
		t.Fatalf("project scoped search missing project A hit: %s", result)
	}
	if strings.Contains(result, "b.txt") {
		t.Fatalf("project scoped search leaked project B hit: %s", result)
	}
}

func TestTicketChatScopesFileSearchToTicketProjectRoot(t *testing.T) {
	st := openTestStore(t)
	rootA := t.TempDir()
	rootB := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootA, "ticket.txt"), []byte("ticketshared project-a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootB, "other.txt"), []byte("ticketshared project-b"), 0o644); err != nil {
		t.Fatal(err)
	}
	projectA, err := st.CreateProject(store.CreateProjectRequest{Key: "a", Name: "A", RootPath: rootA})
	if err != nil {
		t.Fatalf("CreateProject A: %v", err)
	}
	projectB, err := st.CreateProject(store.CreateProjectRequest{Key: "b", Name: "B", RootPath: rootB})
	if err != nil {
		t.Fatalf("CreateProject B: %v", err)
	}
	ticket, err := st.CreateTicket(store.CreateTicketRequest{ProjectID: projectA.ID, Title: "Scoped ticket"})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	indexer := NewFTS5Indexer(st)
	if _, err := indexer.Index(context.Background(), projectA.ID, projectA.RootPath); err != nil {
		t.Fatalf("index A: %v", err)
	}
	if _, err := indexer.Index(context.Background(), projectB.ID, projectB.RootPath); err != nil {
		t.Fatalf("index B: %v", err)
	}

	sess := &Session{Store: st, Config: fullAccessConfig(), Indexer: indexer}
	result := resolveFileSearchForTest(t, sess, ticket.TicketConversationID, "ticketshared")

	if !strings.Contains(result, "ticket.txt") {
		t.Fatalf("ticket scoped search missing ticket project hit: %s", result)
	}
	if strings.Contains(result, "other.txt") {
		t.Fatalf("ticket scoped search leaked other project hit: %s", result)
	}
}

func TestGeneralChatRequiresProjectChoiceForProjectFileSearch(t *testing.T) {
	st := openTestStore(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("generaltoken"), 0o644); err != nil {
		t.Fatal(err)
	}
	project, err := st.CreateProject(store.CreateProjectRequest{Key: "a", Name: "A", RootPath: root})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	indexer := NewFTS5Indexer(st)
	if _, err := indexer.Index(context.Background(), project.ID, project.RootPath); err != nil {
		t.Fatalf("index: %v", err)
	}

	sess := &Session{Store: st, Config: fullAccessConfig(), Indexer: indexer}
	_, err = resolveSkillCall(
		ContextWithConversationID(context.Background(), "account"),
		core.SkillCall{SkillName: "File", Method: "search", Args: []json.RawMessage{json.RawMessage(`"generaltoken"`)}},
		sess,
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "project") {
		t.Fatalf("general File.search error = %v, want project choice error", err)
	}
}

func TestFileSearchUsesEventConversationScopeBeforeAgentLoop(t *testing.T) {
	st := openTestStore(t)
	rootA := t.TempDir()
	rootB := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootA, "a.txt"), []byte("prelooptoken project-a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootB, "b.txt"), []byte("prelooptoken project-b"), 0o644); err != nil {
		t.Fatal(err)
	}
	projectA, err := st.CreateProject(store.CreateProjectRequest{Key: "a", Name: "A", RootPath: rootA})
	if err != nil {
		t.Fatalf("CreateProject A: %v", err)
	}
	projectB, err := st.CreateProject(store.CreateProjectRequest{Key: "b", Name: "B", RootPath: rootB})
	if err != nil {
		t.Fatalf("CreateProject B: %v", err)
	}
	indexer := NewFTS5Indexer(st)
	if _, err := indexer.Index(context.Background(), projectA.ID, projectA.RootPath); err != nil {
		t.Fatalf("index A: %v", err)
	}
	if _, err := indexer.Index(context.Background(), projectB.ID, projectB.RootPath); err != nil {
		t.Fatalf("index B: %v", err)
	}
	payload, err := json.Marshal(core.ChatPayload{
		ChatID:         projectA.ProjectConversationID,
		SessionID:      "browser-session",
		ConversationID: projectA.ProjectConversationID,
		Text:           "search before loop",
	})
	if err != nil {
		t.Fatal(err)
	}
	event := core.Event{Type: core.EventWebChat, Payload: payload}
	sess := &Session{Store: st, Config: fullAccessConfig(), Indexer: indexer}
	result, err := resolveSkillCall(
		ContextWithEvent(context.Background(), &event),
		core.SkillCall{SkillName: "File", Method: "search", Args: []json.RawMessage{json.RawMessage(`"prelooptoken"`)}},
		sess,
		nil,
	)
	if err != nil {
		t.Fatalf("File.search with event scope: %v", err)
	}
	if !strings.Contains(result, "a.txt") {
		t.Fatalf("event-scoped search missing project A hit: %s", result)
	}
	if strings.Contains(result, "b.txt") {
		t.Fatalf("event-scoped search leaked project B hit: %s", result)
	}
}

func TestConversationKeyForEventUsesScopedConversationID(t *testing.T) {
	st := openTestStore(t)
	project, err := st.CreateProject(store.CreateProjectRequest{Key: "scope", Name: "Scope", RootPath: t.TempDir()})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	payload, err := json.Marshal(core.ChatPayload{
		ChatID:         "browser-session",
		SessionID:      "browser-session",
		ConversationID: project.ProjectConversationID,
		Text:           "이 프로젝트 파일을 찾아줘",
	})
	if err != nil {
		t.Fatal(err)
	}
	event := core.Event{Type: core.EventWebChat, Payload: payload}
	sess := &Session{Store: st, AccountID: "account"}

	if got := conversationKeyForEvent(sess, &event); got != project.ProjectConversationID {
		t.Fatalf("conversationKeyForEvent = %q, want project conversation %q", got, project.ProjectConversationID)
	}
}

func TestProjectChatPromptHistoryIsScoped(t *testing.T) {
	sess := newTestSession(t)
	provider := &promptCaptureProvider{response: `return "ok";`}
	sess.Provider = provider
	projectA, err := sess.Store.CreateProject(store.CreateProjectRequest{Key: "a", Name: "A", RootPath: t.TempDir()})
	if err != nil {
		t.Fatalf("CreateProject A: %v", err)
	}
	projectB, err := sess.Store.CreateProject(store.CreateProjectRequest{Key: "b", Name: "B", RootPath: t.TempDir()})
	if err != nil {
		t.Fatalf("CreateProject B: %v", err)
	}
	if err := sess.Store.AddConversationTurn(&core.ConversationTurn{
		Role:      core.RoleUser,
		Content:   "PROJECT_A_SECRET_HISTORY",
		Channel:   "project",
		ChatID:    projectA.ProjectConversationID,
		Timestamp: core.NowTimestamp(),
	}); err != nil {
		t.Fatalf("AddConversationTurn A: %v", err)
	}
	payload, err := json.Marshal(core.ChatPayload{
		ChatID:         projectB.ProjectConversationID,
		SessionID:      "browser-session",
		ConversationID: projectB.ProjectConversationID,
		Text:           "프로젝트 B만 보고 답해줘",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := sess.Run(context.Background(), core.Event{Type: core.EventWebChat, Payload: payload}, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	prompt := llmMessagesText(provider.messages)
	if strings.Contains(prompt, "PROJECT_A_SECRET_HISTORY") {
		t.Fatalf("project B prompt leaked project A history:\n%s", prompt)
	}
	if !strings.Contains(prompt, "프로젝트 B만 보고 답해줘") {
		t.Fatalf("project B prompt missing current turn:\n%s", prompt)
	}
}

func TestProjectKickoffApprovalCreatesBriefDraftFromScan(t *testing.T) {
	sess := newTestSession(t, mockResp(`return "fallback";`))
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/project\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Example\n\nTODO: add tests\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	project, err := sess.Store.CreateProject(store.CreateProjectRequest{Key: "scan", Name: "Scan", RootPath: root})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := sess.Store.AddConversationTurn(&core.ConversationTurn{
		Role:      core.RoleAssistant,
		Content:   "내용을 파악해서 티켓 초안을 만들까요?",
		Channel:   "project",
		ChatID:    project.ProjectConversationID,
		Timestamp: core.NowTimestamp(),
	}); err != nil {
		t.Fatalf("AddConversationTurn: %v", err)
	}
	payload, err := json.Marshal(core.ChatPayload{
		ChatID:         "browser-session",
		SessionID:      "browser-session",
		ConversationID: project.ProjectConversationID,
		Text:           "네네",
	})
	if err != nil {
		t.Fatal(err)
	}

	out, err := sess.Run(context.Background(), core.Event{Type: core.EventWebChat, Payload: payload}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(out, "fallback") {
		t.Fatalf("project kickoff approval fell through to LLM fallback: %q", out)
	}
	if !strings.Contains(out, "티켓 초안") || !strings.Contains(out, "SCAN") {
		t.Fatalf("kickoff response = %q, want ticket draft summary", out)
	}
	drafts, err := sess.Store.ListProjectBriefDrafts(project.ID)
	if err != nil {
		t.Fatalf("ListProjectBriefDrafts: %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("draft count = %d, want 1", len(drafts))
	}
	if !strings.Contains(drafts[0].BriefJSON, "go.mod") || !strings.Contains(drafts[0].ProposedTicketsJSON, "TODO") {
		t.Fatalf("draft did not include scan evidence: brief=%s proposed=%s", drafts[0].BriefJSON, drafts[0].ProposedTicketsJSON)
	}
}

func TestProjectKickoffDraftApprovalCommitsTickets(t *testing.T) {
	sess := newTestSession(t, mockResp(`return "fallback";`))
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/project\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	project, err := sess.Store.CreateProject(store.CreateProjectRequest{Key: "scan", Name: "Scan", RootPath: root})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	draft, err := sess.Store.CreateProjectBriefDraft(store.CreateProjectBriefDraftRequest{
		ProjectID: project.ID,
		Title:     "Scan draft",
		BriefJSON: `{"summary":"scan"}`,
		ProposedTicketsJSON: `[{
			"temp_id":"first",
			"title":"첫 작업 정리",
			"body":"스캔 결과를 바탕으로 첫 작업을 정리합니다.",
			"status":"backlog",
			"priority":5,
			"staff_role":"pm"
		}]`,
		CreatedBy: "project_kickoff",
	})
	if err != nil {
		t.Fatalf("CreateProjectBriefDraft: %v", err)
	}
	if err := sess.Store.AddConversationTurn(&core.ConversationTurn{
		Role:      core.RoleAssistant,
		Content:   formatProjectKickoffDraftResponse(project, draft.ID, []projectKickoffTicket{{TempID: "first", Title: "첫 작업 정리"}}),
		Channel:   "project",
		ChatID:    project.ProjectConversationID,
		Timestamp: core.NowTimestamp(),
	}); err != nil {
		t.Fatalf("AddConversationTurn: %v", err)
	}
	payload, err := json.Marshal(core.ChatPayload{
		ChatID:         "browser-session",
		SessionID:      "browser-session",
		ConversationID: project.ProjectConversationID,
		Text:           "네",
	})
	if err != nil {
		t.Fatal(err)
	}

	out, err := sess.Run(context.Background(), core.Event{Type: core.EventWebChat, Payload: payload}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(out, "fallback") {
		t.Fatalf("project draft approval fell through to LLM fallback: %q", out)
	}
	if !strings.Contains(out, "생성했어요") || !strings.Contains(out, "SCAN-001") {
		t.Fatalf("draft approval response = %q, want committed ticket summary", out)
	}
	tickets, err := sess.Store.ListTickets(store.TicketListFilter{ProjectID: project.ID})
	if err != nil {
		t.Fatalf("ListTickets: %v", err)
	}
	if len(tickets) != 1 || tickets[0].Title != "첫 작업 정리" {
		t.Fatalf("tickets = %+v, want committed first ticket", tickets)
	}
}

func TestProjectKickoffScanIncludesGitSignals(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if out, err := exec.Command("git", "init", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "KittyPaw Test")
	readme := filepath.Join(root, "README.md")
	if err := os.WriteFile(readme, []byte("# Example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README.md")
	runGit("commit", "-m", "initial project")
	if err := os.WriteFile(readme, []byte("# Example\n\nTODO: refine plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	scan := scanProjectForKickoff(root)
	if !strings.Contains(strings.Join(scan.RecentCommits, "\n"), "initial project") {
		t.Fatalf("recent commits = %+v, want initial project commit", scan.RecentCommits)
	}
	if !strings.Contains(strings.Join(scan.GitStatus, "\n"), "README.md") {
		t.Fatalf("git status = %+v, want README.md change", scan.GitStatus)
	}
}

func TestProjectKickoffTodoScanSkipsSymlinkTargets(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("TODO: outside project should not be read\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked-outside.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	todos := scanProjectTodos(root)
	if len(todos) != 0 {
		t.Fatalf("scanProjectTodos followed symlink outside project: %+v", todos)
	}
}

func resolveFileSearchForTest(t *testing.T, sess *Session, conversationID, query string) string {
	t.Helper()
	rawQuery, err := json.Marshal(query)
	if err != nil {
		t.Fatal(err)
	}
	result, err := resolveSkillCall(
		ContextWithConversationID(context.Background(), conversationID),
		core.SkillCall{SkillName: "File", Method: "search", Args: []json.RawMessage{rawQuery}},
		sess,
		nil,
	)
	if err != nil {
		t.Fatalf("File.search: %v", err)
	}
	return result
}

func llmMessagesText(messages []core.LlmMessage) string {
	var b strings.Builder
	for _, msg := range messages {
		b.WriteString(string(msg.Role))
		b.WriteString(": ")
		b.WriteString(msg.Content)
		b.WriteByte('\n')
		for _, block := range msg.ContentBlocks {
			b.WriteString(block.Content)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func fullAccessConfig() *core.Config {
	cfg := core.DefaultConfig()
	cfg.AutonomyLevel = core.AutonomyFull
	return &cfg
}

func TestProjectsToolStartJobCallsRuntime(t *testing.T) {
	st := openTestStore(t)
	project := createProjectsScopeRuntimeProject(t, st, "kitty")
	ticket, err := st.CreateTicket(store.CreateTicketRequest{ProjectID: project.ID, Title: "Run"})
	if err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}
	if err := st.EnsureDefaultDrivers(); err != nil {
		t.Fatalf("EnsureDefaultDrivers() error = %v", err)
	}
	job, err := st.PlanJob(store.PlanJobRequest{
		ProjectID:     project.ID,
		TicketID:      ticket.ID,
		DriverID:      "shell",
		Mode:          store.JobModeOneShot,
		PromptSummary: "Run",
		PromptText:    "echo ok",
	})
	if err != nil {
		t.Fatalf("PlanJob() error = %v", err)
	}
	if _, err := st.ApproveJob(job.ID, "pm"); err != nil {
		t.Fatalf("ApproveJob() error = %v", err)
	}
	rt := NewProjectJobRuntime(ProjectJobRuntimeOptions{
		Store:     st,
		AccountID: "alice",
		BaseDir:   t.TempDir(),
		Runner:    fakeProjectsToolRunner{ExitCode: 0, ResultText: "done"},
	})
	sess := &Session{Store: st, ProjectJobRuntime: rt}

	result, err := executeProjects(context.Background(), skillCallForProjectsTest("startJob", job.ID, map[string]any{"actor_id": "pm"}), sess)
	if err != nil {
		t.Fatalf("executeProjects(startJob) error = %v", err)
	}
	if !strings.Contains(result, `"status":"running"`) {
		t.Fatalf("result = %s, want running job", result)
	}
}

func TestProjectsToolJobLogsReturnsCurrentJobAndEvents(t *testing.T) {
	st := openTestStore(t)
	project := createProjectsScopeRuntimeProject(t, st, "kitty")
	ticket, err := st.CreateTicket(store.CreateTicketRequest{ProjectID: project.ID, Title: "Logs"})
	if err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}
	if err := st.EnsureDefaultDrivers(); err != nil {
		t.Fatalf("EnsureDefaultDrivers() error = %v", err)
	}
	job, err := st.PlanJob(store.PlanJobRequest{
		ProjectID:     project.ID,
		TicketID:      ticket.ID,
		DriverID:      "shell",
		Mode:          store.JobModeOneShot,
		PromptSummary: "Logs",
		PromptText:    "echo ok",
	})
	if err != nil {
		t.Fatalf("PlanJob() error = %v", err)
	}
	if _, err := st.AddJobEvent(store.AddJobEventRequest{JobID: job.ID, Type: "log", Message: "hello"}); err != nil {
		t.Fatalf("AddJobEvent() error = %v", err)
	}
	rt := NewProjectJobRuntime(ProjectJobRuntimeOptions{Store: st, AccountID: "alice", BaseDir: t.TempDir(), Runner: fakeProjectsToolRunner{ExitCode: 0}})
	sess := &Session{Store: st, ProjectJobRuntime: rt}

	result, err := executeProjects(context.Background(), skillCallForProjectsTest("jobLogs", job.ID), sess)
	if err != nil {
		t.Fatalf("executeProjects(jobLogs) error = %v", err)
	}
	if !strings.Contains(result, `"events"`) || !strings.Contains(result, `"job"`) {
		t.Fatalf("result = %s, want job logs", result)
	}
}

type fakeProjectsToolRunner struct {
	ExitCode   int
	ResultText string
}

func (r fakeProjectsToolRunner) Run(ctx context.Context, spec JobCommandSpec) JobCommandResult {
	return JobCommandResult{ExitCode: r.ExitCode, Summary: r.ResultText}
}

func createProjectsScopeRuntimeProject(t *testing.T, st *store.Store, key string) *store.Project {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	gitCommitFile(t, root, "README.md", "clean\n")
	project, err := st.CreateProject(store.CreateProjectRequest{Key: key, Name: key, RootPath: root})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	return project
}

func skillCallForProjectsTest(method string, args ...any) core.SkillCall {
	raw := make([]json.RawMessage, 0, len(args))
	for _, arg := range args {
		data, err := json.Marshal(arg)
		if err != nil {
			panic(err)
		}
		raw = append(raw, data)
	}
	return core.SkillCall{SkillName: "Projects", Method: method, Args: raw}
}
