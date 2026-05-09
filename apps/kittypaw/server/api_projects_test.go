package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jinto/kittypaw/core"
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

func TestProjectsAPIJobPlanApprovalNoExecutionStart(t *testing.T) {
	srv := newProjectsAPITestServer(t)
	project := projectsAPICreateProject(t, srv, "kitty")
	ticket := projectsAPICreateTicket(t, srv, project.ID, "Run job")

	var planned struct {
		Job struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"job"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/tickets/"+ticket.ID+"/jobs/plan", map[string]any{
		"driver_id":      "codex",
		"mode":           "one_shot",
		"prompt_summary": "Run job",
		"prompt_text":    "Run this ticket.",
	}, http.StatusCreated, &planned)
	if planned.Job.Status != "planned" {
		t.Fatalf("planned job = %+v", planned.Job)
	}

	var approved struct {
		Job struct {
			Status string `json:"status"`
		} `json:"job"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/jobs/"+planned.Job.ID+"/approve", map[string]any{"actor_id": "alice"}, http.StatusOK, &approved)
	if approved.Job.Status != "approved" {
		t.Fatalf("approved job = %+v", approved.Job)
	}
	var startErr struct {
		Error string `json:"error"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/jobs/"+planned.Job.ID+"/start", nil, http.StatusConflict, &startErr)
	if startErr.Error != "driver execution is not available in MVP 1" {
		t.Fatalf("start error = %+v", startErr)
	}
}

func TestOldKanbanRoutesAreRemoved(t *testing.T) {
	srv := newProjectsAPITestServer(t)
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/kanban/tasks", nil, http.StatusNotFound, nil)
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/projects/KITTY/milestones", nil, http.StatusNotFound, nil)
}

func newProjectsAPITestServer(t *testing.T) *Server {
	t.Helper()
	cfg := core.DefaultConfig()
	cfg.Server.APIKey = "api-key"
	return newServerWithLocalUserAndConfig(t, "alice", "pw", &cfg)
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
