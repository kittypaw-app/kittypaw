package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

	sess := &AccountRuntime{Store: st, Config: fullAccessConfig(), Indexer: indexer}
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

	sess := &AccountRuntime{Store: st, Config: fullAccessConfig(), Indexer: indexer}
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

	sess := &AccountRuntime{Store: st, Config: fullAccessConfig(), Indexer: indexer}
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

func TestBundledFileStatsStillRequiresProjectChoiceInGeneralChat(t *testing.T) {
	st := openTestStore(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("statstoken"), 0o644); err != nil {
		t.Fatal(err)
	}
	project, err := st.CreateProject(store.CreateProjectRequest{Key: "stats", Name: "Stats", RootPath: root})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	indexer := NewFTS5Indexer(st)
	if _, err := indexer.Index(context.Background(), project.ID, project.RootPath); err != nil {
		t.Fatalf("index: %v", err)
	}

	bundleRoot := t.TempDir()
	sess := &AccountRuntime{Store: st, Config: fullAccessConfig(), Indexer: indexer}
	ctx := contextWithPromptModeResourceRoot(ContextWithConversationID(context.Background(), "account"), bundleRoot)
	_, err = resolveSkillCall(ctx, core.SkillCall{SkillName: "File", Method: "stats"}, sess, nil)
	if err == nil || !strings.Contains(err.Error(), "project") {
		t.Fatalf("bundled File.stats error = %v, want project choice error", err)
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
		ChatID:          projectA.ProjectConversationID,
		SourceSessionID: "browser-session",
		ConversationID:  projectA.ProjectConversationID,
		Text:            "search before loop",
	})
	if err != nil {
		t.Fatal(err)
	}
	event := core.Event{Type: core.EventWebChat, Payload: payload}
	sess := &AccountRuntime{Store: st, Config: fullAccessConfig(), Indexer: indexer}
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
		ChatID:          "browser-session",
		SourceSessionID: "browser-session",
		ConversationID:  project.ProjectConversationID,
		Text:            "이 프로젝트 파일을 찾아줘",
	})
	if err != nil {
		t.Fatal(err)
	}
	event := core.Event{Type: core.EventWebChat, Payload: payload}
	sess := &AccountRuntime{Store: st, AccountID: "account"}

	if got := conversationKeyForEvent(sess, &event); got != project.ProjectConversationID {
		t.Fatalf("conversationKeyForEvent = %q, want project conversation %q", got, project.ProjectConversationID)
	}
}

func TestConversationKeyForEventUsesIndexedConversationID(t *testing.T) {
	st := openTestStore(t)
	if err := st.AddConversationTurn(&core.ConversationTurn{
		ConversationID: "general:indexed",
		Role:           core.RoleUser,
		Content:        "indexed thread",
		Timestamp:      "1",
	}); err != nil {
		t.Fatalf("AddConversationTurn: %v", err)
	}

	payload, err := json.Marshal(core.ChatPayload{
		ChatID:          "browser-session",
		SourceSessionID: "browser-session",
		ConversationID:  "general:indexed",
		Text:            "이 대화를 이어서 해줘",
	})
	if err != nil {
		t.Fatal(err)
	}
	event := core.Event{Type: core.EventWebChat, Payload: payload}
	sess := &AccountRuntime{Store: st, AccountID: "account"}

	if got := conversationKeyForEvent(sess, &event); got != "general:indexed" {
		t.Fatalf("conversationKeyForEvent = %q, want indexed conversation", got)
	}
	state, err := sess.loadConversationStateForRun("general:indexed")
	if err != nil {
		t.Fatalf("loadConversationStateForRun: %v", err)
	}
	if state == nil || state.ConversationID != "general:indexed" || len(state.Turns) != 1 {
		t.Fatalf("state = %+v, want indexed conversation history", state)
	}
}

func TestConversationKeyForEventDefaultsToGeneralConversation(t *testing.T) {
	st := openTestStore(t)
	payload, err := json.Marshal(core.ChatPayload{
		ChatID:          "browser-session",
		SourceSessionID: "browser-session",
		Text:            "일반 대화",
	})
	if err != nil {
		t.Fatal(err)
	}
	event := core.Event{Type: core.EventWebChat, Payload: payload}
	sess := &AccountRuntime{Store: st, AccountID: "account"}

	if got := conversationKeyForEvent(sess, &event); got != "general:web_chat:browser-session" {
		t.Fatalf("conversationKeyForEvent = %q, want source-derived general conversation", got)
	}
}

func TestConversationKeyForEventUsesActiveConversationRoute(t *testing.T) {
	st := openTestStore(t)
	if err := st.EnsureConversation("general:web_chat:browser-session", "general", "web_chat:browser-session"); err != nil {
		t.Fatalf("EnsureConversation(parent): %v", err)
	}
	if err := st.EnsureConversation("general:conv_child", "general", "conv_child"); err != nil {
		t.Fatalf("EnsureConversation(child): %v", err)
	}
	if err := st.UpsertConversationRoute(store.ConversationRoute{
		RouteKey:       "web_chat:browser-session",
		ConversationID: "general:conv_child",
	}); err != nil {
		t.Fatalf("UpsertConversationRoute: %v", err)
	}
	payload, err := json.Marshal(core.ChatPayload{
		ChatID:          "browser-session",
		SourceSessionID: "browser-session",
		Text:            "일반 대화",
	})
	if err != nil {
		t.Fatal(err)
	}
	event := core.Event{Type: core.EventWebChat, Payload: payload}
	sess := &AccountRuntime{Store: st, AccountID: "account"}

	if got := conversationKeyForEvent(sess, &event); got != "general:conv_child" {
		t.Fatalf("conversationKeyForEvent = %q, want active route conversation", got)
	}
}

func TestConversationKeyForEventDerivesSourceConversationForAccountEvents(t *testing.T) {
	st := openTestStore(t)
	payload, err := json.Marshal(core.ChatPayload{
		ChatID:          "C123ABC",
		SourceSessionID: "U123ABC",
		Text:            "슬랙 채널 대화",
	})
	if err != nil {
		t.Fatal(err)
	}
	event := core.Event{Type: core.EventSlack, AccountID: "account", Payload: payload}
	sess := &AccountRuntime{Store: st, AccountID: "account"}

	if got := conversationKeyForEvent(sess, &event); got != "general:slack:c123abc" {
		t.Fatalf("conversationKeyForEvent = %q, want channel-derived general conversation", got)
	}
	state, err := sess.loadConversationStateForRun("general:slack:c123abc")
	if err != nil {
		t.Fatalf("loadConversationStateForRun: %v", err)
	}
	if state != nil {
		t.Fatalf("state = %+v, want empty derived conversation before first turn", state)
	}
	if _, ok, err := st.Conversation("general:slack:c123abc"); err != nil || !ok {
		t.Fatalf("derived conversation exists = %v err=%v, want true nil", ok, err)
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
		ChatID:          projectB.ProjectConversationID,
		SourceSessionID: "browser-session",
		ConversationID:  projectB.ProjectConversationID,
		Text:            "프로젝트 B만 보고 답해줘",
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
		ChatID:          "browser-session",
		SourceSessionID: "browser-session",
		ConversationID:  project.ProjectConversationID,
		Text:            "네네",
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
		ChatID:          "browser-session",
		SourceSessionID: "browser-session",
		ConversationID:  project.ProjectConversationID,
		Text:            "네",
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

func resolveFileSearchForTest(t *testing.T, sess *AccountRuntime, conversationID, query string) string {
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
	sess := &AccountRuntime{Store: st, ProjectJobRuntime: rt}

	result, err := executeProjects(context.Background(), skillCallForProjectsTest("startJob", job.ID, map[string]any{"actor_id": "pm"}), sess)
	if err != nil {
		t.Fatalf("executeProjects(startJob) error = %v", err)
	}
	if !strings.Contains(result, `"status":"running"`) {
		t.Fatalf("result = %s, want running job", result)
	}
}

func TestProjectsToolCancelJobStopsRuntimeJob(t *testing.T) {
	st := openTestStore(t)
	project := createProjectsScopeRuntimeProject(t, st, "kitty")
	ticket, err := st.CreateTicket(store.CreateTicketRequest{ProjectID: project.ID, Title: "Cancel"})
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
		PromptSummary: "Cancel",
		PromptText:    "sleep",
	})
	if err != nil {
		t.Fatalf("PlanJob() error = %v", err)
	}
	if _, err := st.ApproveJob(job.ID, "pm"); err != nil {
		t.Fatalf("ApproveJob() error = %v", err)
	}
	block := make(chan struct{})
	rt := NewProjectJobRuntime(ProjectJobRuntimeOptions{
		Store:     st,
		AccountID: "alice",
		BaseDir:   t.TempDir(),
		Runner:    fakeBlockingJobCommandRunner{Block: block},
	})
	defer func() {
		close(block)
		_ = rt.WaitForJob(job.ID, 2*time.Second)
	}()
	sess := &AccountRuntime{Store: st, ProjectJobRuntime: rt}

	if _, err := executeProjects(context.Background(), skillCallForProjectsTest("startJob", job.ID, map[string]any{"actor_id": "pm"}), sess); err != nil {
		t.Fatalf("executeProjects(startJob) error = %v", err)
	}
	result, err := executeProjects(context.Background(), skillCallForProjectsTest("cancelJob", job.ID, map[string]any{"actor_id": "pm", "reason": "not now"}), sess)
	if err != nil {
		t.Fatalf("executeProjects(cancelJob) error = %v", err)
	}
	if !strings.Contains(result, `"status":"canceled"`) {
		t.Fatalf("result = %s, want canceled job", result)
	}
	if !rt.WaitForJob(job.ID, 500*time.Millisecond) {
		t.Fatal("cancelJob did not stop the runtime job")
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
	sess := &AccountRuntime{Store: st, ProjectJobRuntime: rt}

	result, err := executeProjects(context.Background(), skillCallForProjectsTest("jobLogs", job.ID), sess)
	if err != nil {
		t.Fatalf("executeProjects(jobLogs) error = %v", err)
	}
	if !strings.Contains(result, `"events"`) || !strings.Contains(result, `"job"`) {
		t.Fatalf("result = %s, want job logs", result)
	}
}

func TestExecuteProjectsAppendJobInputUsesRuntime(t *testing.T) {
	st := openTestStore(t)
	project := createProjectsScopeRuntimeProject(t, st, "kitty")
	ticket, err := st.CreateTicket(store.CreateTicketRequest{ProjectID: project.ID, Title: "PTY"})
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
		Mode:          store.JobModePTY,
		PromptSummary: "PTY",
		PromptText:    "cat",
		CreatedBy:     "pm",
	})
	if err != nil {
		t.Fatalf("PlanJob() error = %v", err)
	}
	approved, err := st.ApproveJob(job.ID, "pm")
	if err != nil {
		t.Fatalf("ApproveJob() error = %v", err)
	}
	session := &fakeProjectsToolPTYSession{InputCh: make(chan string, 1), ResultCh: make(chan JobPTYResult, 1)}
	rt := NewProjectJobRuntime(ProjectJobRuntimeOptions{Store: st, AccountID: "alice", BaseDir: t.TempDir(), PTYRunner: fakeProjectsToolPTYRunner{Session: session}})
	sess := &AccountRuntime{Store: st, ProjectJobRuntime: rt}
	if _, err := executeProjects(context.Background(), skillCallForProjectsTest("startJob", approved.ID, map[string]any{"actor_id": "pm"}), sess); err != nil {
		t.Fatalf("executeProjects(startJob) error = %v", err)
	}
	defer func() {
		session.ResultCh <- JobPTYResult{ExitCode: 0, Summary: "done"}
		_ = rt.WaitForJob(approved.ID, 2*time.Second)
	}()

	result, err := executeProjects(context.Background(), skillCallForProjectsTest("appendJobInput", approved.ID, map[string]any{"actor_id": "pm", "text": "yes\n"}), sess)
	if err != nil {
		t.Fatalf("executeProjects(appendJobInput) error = %v", err)
	}
	if !strings.Contains(result, `"event"`) || !strings.Contains(result, `"input"`) {
		t.Fatalf("appendJobInput result = %s", result)
	}
	select {
	case got := <-session.InputCh:
		if got != "yes\n" {
			t.Fatalf("input = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("input was not forwarded")
	}
}

type fakeProjectsToolRunner struct {
	ExitCode   int
	ResultText string
}

func (r fakeProjectsToolRunner) Run(ctx context.Context, spec JobCommandSpec) JobCommandResult {
	return JobCommandResult{ExitCode: r.ExitCode, Summary: r.ResultText}
}

type fakeProjectsToolPTYRunner struct {
	Session *fakeProjectsToolPTYSession
}

func (r fakeProjectsToolPTYRunner) Start(ctx context.Context, spec JobPTYSpec) (JobPTYSession, error) {
	if r.Session == nil {
		return nil, fmt.Errorf("missing fake PTY session")
	}
	if spec.Emit != nil {
		spec.Emit([]byte("tool pty output\n"))
	}
	return r.Session, nil
}

type fakeProjectsToolPTYSession struct {
	InputCh  chan string
	ResultCh chan JobPTYResult
}

func (s *fakeProjectsToolPTYSession) Input(text string) error {
	s.InputCh <- text
	return nil
}

func (s *fakeProjectsToolPTYSession) Wait(ctx context.Context) JobPTYResult {
	select {
	case result := <-s.ResultCh:
		return result
	case <-ctx.Done():
		return JobPTYResult{ExitCode: -1, ErrorText: ctx.Err().Error()}
	}
}

func (s *fakeProjectsToolPTYSession) Close() error {
	return nil
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
