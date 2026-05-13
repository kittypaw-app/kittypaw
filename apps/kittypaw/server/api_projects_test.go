package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/engine"
	"github.com/jinto/kittypaw/store"
)

func TestProjectsAPIRequiresAuthAndUsesAccountStore(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Server.APIKey = "api-key"
	srv := newServerWithLocalUserAndConfig(t, "alice", "pw", &cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("projects without auth code = %d, want 401", rr.Code)
	}

	var body struct {
		Projects []any `json:"projects"`
	}
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/projects", nil, http.StatusOK, &body)
	if len(body.Projects) != 0 {
		t.Fatalf("projects = %+v, want empty", body.Projects)
	}

	multi := newMultiAccountAuthTestServer(t, "alice", map[string]string{
		"alice": "alice-pw",
		"bob":   "bob-pw",
	}, map[string]*core.Config{
		"alice": {},
		"bob":   {},
	})
	bobCookie := loginSessionCookie(t, multi, "bob", "bob-pw")
	data, err := json.Marshal(map[string]string{
		"key":       "bob",
		"name":      "Bob Project",
		"root_path": t.TempDir(),
	})
	if err != nil {
		t.Fatalf("marshal bob project: %v", err)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(bobCookie)
	rr = httptest.NewRecorder()
	multi.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("bob project create code = %d body=%s, want 201", rr.Code, rr.Body.String())
	}
	bobProjects, err := multi.accountDepsForID("bob").Store.ListProjects(false)
	if err != nil {
		t.Fatalf("list bob projects: %v", err)
	}
	aliceProjects, err := multi.accountDepsForID("alice").Store.ListProjects(false)
	if err != nil {
		t.Fatalf("list alice projects: %v", err)
	}
	if len(bobProjects) != 1 || bobProjects[0].Key != "BOB" {
		t.Fatalf("bob projects = %+v, want BOB", bobProjects)
	}
	if len(aliceProjects) != 0 {
		t.Fatalf("alice projects = %+v, want no cross-account write", aliceProjects)
	}
}

func TestProjectsAPICreateListShowBoard(t *testing.T) {
	srv := newProjectsAPITestServer(t)
	project := projectsAPICreateProject(t, srv, "kitty")

	var listed struct {
		Projects []struct {
			ID       string `json:"id"`
			Key      string `json:"key"`
			RootPath string `json:"root_path"`
		} `json:"projects"`
	}
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/projects", nil, http.StatusOK, &listed)
	if len(listed.Projects) != 1 || listed.Projects[0].Key != "KITTY" || listed.Projects[0].RootPath == "" {
		t.Fatalf("listed projects = %+v", listed.Projects)
	}

	var shown struct {
		Project struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		} `json:"project"`
	}
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/projects/KITTY", nil, http.StatusOK, &shown)
	if shown.Project.ID != project.ID || shown.Project.Key != "KITTY" {
		t.Fatalf("shown = %+v, created = %+v", shown.Project, project)
	}

	var board struct {
		Board struct {
			ProjectID string           `json:"project_id"`
			Columns   map[string][]any `json:"columns"`
		} `json:"board"`
	}
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/projects/KITTY/board", nil, http.StatusOK, &board)
	if board.Board.ProjectID != project.ID || board.Board.Columns["backlog"] == nil {
		t.Fatalf("board = %+v, project = %+v", board.Board, project)
	}
}

func TestProjectsAPICreateRecordsKickoffMessage(t *testing.T) {
	srv := newProjectsAPITestServer(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/project\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var created struct {
		Project struct {
			ProjectConversationID string `json:"project_conversation_id"`
		} `json:"project"`
		KickoffMessage string `json:"kickoff_message"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/projects", map[string]any{
		"key":       "scan",
		"name":      "Scan",
		"root_path": root,
	}, http.StatusCreated, &created)

	if created.KickoffMessage != "내용을 파악해서 티켓 초안을 만들까요?" {
		t.Fatalf("kickoff_message = %q", created.KickoffMessage)
	}
	turns, err := srv.store.ListConversationTurns(10)
	if err != nil {
		t.Fatalf("ListConversationTurns: %v", err)
	}
	for _, turn := range turns {
		if turn.ChatID == created.Project.ProjectConversationID && turn.Role == core.RoleAssistant && turn.Content == created.KickoffMessage {
			return
		}
	}
	t.Fatalf("kickoff assistant turn not recorded for project chat %q: %+v", created.Project.ProjectConversationID, turns)
}

func TestProjectsAPITicketLifecycle(t *testing.T) {
	srv := newProjectsAPITestServer(t)
	project := projectsAPICreateProject(t, srv, "kitty")

	var created struct {
		Ticket struct {
			ID     string `json:"id"`
			Key    string `json:"key"`
			Status string `json:"status"`
		} `json:"ticket"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/tickets", map[string]any{
		"project": project.ID,
		"title":   "Implement Projects",
	}, http.StatusCreated, &created)
	if created.Ticket.Key != "KITTY-001" || created.Ticket.Status != "backlog" {
		t.Fatalf("created ticket = %+v", created.Ticket)
	}

	var moved struct {
		Ticket struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"ticket"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/tickets/"+created.Ticket.ID+"/actions", map[string]any{
		"type":    "move",
		"status":  "in_progress",
		"message": "starting",
	}, http.StatusOK, &moved)
	if moved.Ticket.Status != "in_progress" {
		t.Fatalf("moved ticket = %+v", moved.Ticket)
	}

	var listed struct {
		Tickets []struct {
			ID string `json:"id"`
		} `json:"tickets"`
	}
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/tickets?project="+project.ID, nil, http.StatusOK, &listed)
	if len(listed.Tickets) != 1 || listed.Tickets[0].ID != created.Ticket.ID {
		t.Fatalf("listed tickets = %+v", listed.Tickets)
	}

	var archived struct {
		Ticket struct {
			Status     string `json:"status"`
			ArchivedAt string `json:"archived_at"`
		} `json:"ticket"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/tickets/"+created.Ticket.ID+"/archive", map[string]any{"actor_id": "alice"}, http.StatusOK, &archived)
	if archived.Ticket.Status != "archived" || archived.Ticket.ArchivedAt == "" {
		t.Fatalf("archived ticket = %+v", archived.Ticket)
	}
}

func TestProjectsAPIBriefDraftCommit(t *testing.T) {
	srv := newProjectsAPITestServer(t)
	project := projectsAPICreateProject(t, srv, "kitty")

	var created struct {
		Draft struct {
			ID string `json:"id"`
		} `json:"draft"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/projects/"+project.ID+"/brief-drafts", map[string]any{
		"title":                 "Brief",
		"brief_json":            `{"summary":"scan"}`,
		"proposed_tickets_json": `[{"temp_id":"a","title":"A","priority":9},{"temp_id":"b","title":"B","dependencies":[{"blocker_temp_id":"a","type":"blocks"}]}]`,
	}, http.StatusCreated, &created)
	if created.Draft.ID == "" {
		t.Fatalf("created draft = %+v", created.Draft)
	}

	var committed struct {
		Result struct {
			Tickets []struct {
				Key string `json:"key"`
			} `json:"tickets"`
		} `json:"result"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/projects/"+project.ID+"/brief-drafts/"+created.Draft.ID+"/commit", map[string]any{"actor_id": "pm"}, http.StatusOK, &committed)
	if len(committed.Result.Tickets) != 2 || committed.Result.Tickets[0].Key != "KITTY-001" {
		t.Fatalf("committed result = %+v", committed.Result)
	}
}

func TestProjectsAPIJobStartAndLogsUseRuntime(t *testing.T) {
	srv := newProjectsAPITestServerWithRunner(t, fakeServerJobRunner{
		Stdout:     "api job log\n",
		ResultText: "api done",
		ExitCode:   0,
	})
	project := projectsAPICreateGitProject(t, srv, "kitty")
	ticket := projectsAPICreateTicket(t, srv, project.ID, "Run job")
	planned := projectsAPIPlanJob(t, srv, ticket.ID, "shell", "echo ok")

	var approved struct {
		Job struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"job"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/jobs/"+planned.ID+"/approve", map[string]any{"actor_id": "alice"}, http.StatusOK, &approved)

	var started struct {
		Job struct {
			ID           string `json:"id"`
			Status       string `json:"status"`
			WorktreePath string `json:"worktree_path"`
		} `json:"job"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/jobs/"+planned.ID+"/start", map[string]any{"actor_id": "alice"}, http.StatusAccepted, &started)
	if started.Job.Status != "running" || started.Job.WorktreePath == "" {
		t.Fatalf("started = %+v", started.Job)
	}
	if !srv.runtime.ProjectJobRuntime.WaitForJob(planned.ID, 2*time.Second) {
		t.Fatal("job did not finish")
	}

	var logs struct {
		Job struct {
			Status        string `json:"status"`
			ResultSummary string `json:"result_summary"`
			ExitCode      int    `json:"exit_code"`
		} `json:"job"`
		LogTail string `json:"log_tail"`
		Events  []struct {
			Type string `json:"type"`
		} `json:"events"`
	}
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/jobs/"+planned.ID+"/logs", nil, http.StatusOK, &logs)
	if logs.Job.Status != "succeeded" || logs.Job.ResultSummary != "api done" || logs.Job.ExitCode != 0 {
		t.Fatalf("logs job = %+v", logs.Job)
	}
	if !strings.Contains(logs.LogTail, "api job log") || len(logs.Events) == 0 {
		t.Fatalf("logs = %+v", logs)
	}
}

func TestProjectsAPIJobInputUsesRuntime(t *testing.T) {
	session := &fakeServerPTYSession{InputCh: make(chan string, 1), ResultCh: make(chan engine.JobPTYResult, 1)}
	srv := newProjectsAPITestServerWithPTYRunner(t, fakeServerPTYRunner{Session: session})
	project := projectsAPICreateGitProject(t, srv, "kitty")
	ticket := projectsAPICreateTicket(t, srv, project.ID, "Run pty job")
	planned := projectsAPIPlanJobWithMode(t, srv, ticket.ID, "shell", store.JobModePTY, "cat")
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/jobs/"+planned.ID+"/approve", map[string]any{"actor_id": "alice"}, http.StatusOK, nil)
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/jobs/"+planned.ID+"/start", map[string]any{"actor_id": "alice"}, http.StatusAccepted, nil)
	defer func() {
		session.ResultCh <- engine.JobPTYResult{ExitCode: 0, Summary: "done"}
	}()

	var input struct {
		Accepted bool `json:"accepted"`
		Job      struct {
			ID string `json:"id"`
		} `json:"job"`
		Event struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"event"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/jobs/"+planned.ID+"/input", map[string]any{
		"actor_id": "web",
		"text":     "hello\n",
	}, http.StatusOK, &input)
	if !input.Accepted || input.Job.ID != planned.ID || input.Event.Type != "input" || input.Event.Message != "hello\n" {
		t.Fatalf("input response = %+v", input)
	}
	select {
	case got := <-session.InputCh:
		if got != "hello\n" {
			t.Fatalf("input text = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("PTY input was not forwarded")
	}
}

func TestProjectsAPIStartNonGitReturnsStructuredCodeAndGitInitDoesNotStage(t *testing.T) {
	srv := newProjectsAPITestServerWithRunner(t, fakeServerJobRunner{ExitCode: 0})
	project := projectsAPICreateProject(t, srv, "kitty")
	ticket := projectsAPICreateTicket(t, srv, project.ID, "Run job")
	planned := projectsAPIPlanJob(t, srv, ticket.ID, "shell", "echo ok")
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/jobs/"+planned.ID+"/approve", map[string]any{"actor_id": "alice"}, http.StatusOK, nil)

	var startErr struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/jobs/"+planned.ID+"/start", map[string]any{"actor_id": "alice"}, http.StatusConflict, &startErr)
	if startErr.Code != store.ProjectJobErrProjectNotGitRepository {
		t.Fatalf("startErr = %+v", startErr)
	}

	var initResp struct {
		Git struct {
			IsGitRepository bool `json:"is_git_repository"`
			HasHead         bool `json:"has_head"`
		} `json:"git"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/projects/"+project.ID+"/git/init", nil, http.StatusOK, &initResp)
	if !initResp.Git.IsGitRepository || initResp.Git.HasHead {
		t.Fatalf("init git status = %+v", initResp.Git)
	}
}

func TestOldKanbanRoutesAreRemoved(t *testing.T) {
	srv := newProjectsAPITestServer(t)
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/kanban/tasks", nil, http.StatusNotFound, nil)
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/projects/KITTY/milestones", nil, http.StatusNotFound, nil)
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/workspaces", nil, http.StatusNotFound, nil)
}

func newProjectsAPITestServer(t *testing.T) *Server {
	t.Helper()
	cfg := core.DefaultConfig()
	cfg.Server.APIKey = "api-key"
	return newServerWithLocalUserAndConfig(t, "alice", "pw", &cfg)
}

type fakeServerJobRunner struct {
	Stdout     string
	ResultText string
	ExitCode   int
}

func (r fakeServerJobRunner) Run(ctx context.Context, spec engine.JobCommandSpec) engine.JobCommandResult {
	if r.Stdout != "" {
		spec.Emit([]byte(r.Stdout))
	}
	return engine.JobCommandResult{ExitCode: r.ExitCode, Summary: r.ResultText}
}

func newProjectsAPITestServerWithRunner(t *testing.T, runner engine.JobCommandRunner) *Server {
	t.Helper()
	cfg := core.DefaultConfig()
	cfg.Server.APIKey = "api-key"
	cfg.Workspace.LiveIndex = false
	deps := buildAccountDeps(t, filepath.Join(t.TempDir(), "accounts"), "alice", &cfg)
	deps.JobRuntime = engine.NewProjectJobRuntime(engine.ProjectJobRuntimeOptions{
		Store:     deps.Store,
		AccountID: deps.Account.ID,
		BaseDir:   deps.Account.BaseDir,
		Runner:    runner,
	})
	srv := New([]*AccountDeps{deps}, "test")
	if srv.runtime != nil {
		srv.runtime.Indexer = nil
	}
	deps.LiveIndexer = nil
	return srv
}

func newProjectsAPITestServerWithPTYRunner(t *testing.T, runner engine.JobPTYRunner) *Server {
	t.Helper()
	cfg := core.DefaultConfig()
	cfg.Server.APIKey = "api-key"
	cfg.Workspace.LiveIndex = false
	deps := buildAccountDeps(t, filepath.Join(t.TempDir(), "accounts"), "alice", &cfg)
	deps.JobRuntime = engine.NewProjectJobRuntime(engine.ProjectJobRuntimeOptions{
		Store:     deps.Store,
		AccountID: deps.Account.ID,
		BaseDir:   deps.Account.BaseDir,
		PTYRunner: runner,
	})
	srv := New([]*AccountDeps{deps}, "test")
	if srv.runtime != nil {
		srv.runtime.Indexer = nil
	}
	deps.LiveIndexer = nil
	return srv
}

type fakeServerPTYRunner struct {
	Session *fakeServerPTYSession
}

func (r fakeServerPTYRunner) Start(ctx context.Context, spec engine.JobPTYSpec) (engine.JobPTYSession, error) {
	if r.Session == nil {
		return nil, fmt.Errorf("missing fake PTY session")
	}
	if spec.Emit != nil {
		spec.Emit([]byte("server pty output\n"))
	}
	return r.Session, nil
}

type fakeServerPTYSession struct {
	InputCh  chan string
	ResultCh chan engine.JobPTYResult
}

func (s *fakeServerPTYSession) Input(text string) error {
	s.InputCh <- text
	return nil
}

func (s *fakeServerPTYSession) Wait(ctx context.Context) engine.JobPTYResult {
	select {
	case result := <-s.ResultCh:
		return result
	case <-ctx.Done():
		return engine.JobPTYResult{ExitCode: -1, ErrorText: ctx.Err().Error()}
	}
}

func (s *fakeServerPTYSession) Close() error {
	return nil
}

func projectsAPICreateProject(t *testing.T, srv *Server, key string) struct {
	ID  string
	Key string
} {
	t.Helper()
	var created struct {
		Project struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		} `json:"project"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/projects", map[string]any{
		"key":       key,
		"name":      key,
		"root_path": t.TempDir(),
	}, http.StatusCreated, &created)
	return struct {
		ID  string
		Key string
	}{ID: created.Project.ID, Key: created.Project.Key}
}

func projectsAPICreateGitProject(t *testing.T, srv *Server, key string) struct {
	ID  string
	Key string
} {
	t.Helper()
	root := t.TempDir()
	gitInitForServerTest(t, root)
	gitCommitFileForServerTest(t, root, "README.md", "clean\n")
	var created struct {
		Project struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		} `json:"project"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/projects", map[string]any{
		"key":       key,
		"name":      key,
		"root_path": root,
	}, http.StatusCreated, &created)
	return struct {
		ID  string
		Key string
	}{ID: created.Project.ID, Key: created.Project.Key}
}

func projectsAPICreateTicket(t *testing.T, srv *Server, projectID, title string) struct {
	ID  string
	Key string
} {
	t.Helper()
	var created struct {
		Ticket struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		} `json:"ticket"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/tickets", map[string]any{
		"project": projectID,
		"title":   title,
	}, http.StatusCreated, &created)
	return struct {
		ID  string
		Key string
	}{ID: created.Ticket.ID, Key: created.Ticket.Key}
}

func projectsAPIPlanJob(t *testing.T, srv *Server, ticketID, driverID, prompt string) struct{ ID string } {
	t.Helper()
	return projectsAPIPlanJobWithMode(t, srv, ticketID, driverID, store.JobModeOneShot, prompt)
}

func projectsAPIPlanJobWithMode(t *testing.T, srv *Server, ticketID, driverID, mode, prompt string) struct{ ID string } {
	t.Helper()
	var planned struct {
		Job struct {
			ID string `json:"id"`
		} `json:"job"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/tickets/"+ticketID+"/jobs/plan", map[string]any{
		"driver_id":      driverID,
		"mode":           mode,
		"prompt_summary": "Run job",
		"prompt_text":    prompt,
	}, http.StatusCreated, &planned)
	return struct{ ID string }{ID: planned.Job.ID}
}

func gitInitForServerTest(t *testing.T, root string) {
	t.Helper()
	runGitForServerTest(t, root, "init")
	runGitForServerTest(t, root, "config", "user.email", "kittypaw@example.test")
	runGitForServerTest(t, root, "config", "user.name", "KittyPaw Test")
}

func gitCommitFileForServerTest(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	runGitForServerTest(t, root, "add", name)
	runGitForServerTest(t, root, "commit", "-m", "initial")
}

func runGitForServerTest(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

func projectsAPIRequest(t *testing.T, srv *Server, method, path string, body any, wantStatus int, dst any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("x-api-key", "api-key")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != wantStatus {
		t.Fatalf("%s %s code = %d body=%s, want %d", method, path, rr.Code, rr.Body.String(), wantStatus)
	}
	if dst != nil {
		if err := json.NewDecoder(rr.Body).Decode(dst); err != nil {
			t.Fatalf("decode %s %s response: %v body=%s", method, path, err, rr.Body.String())
		}
	}
	return rr
}
