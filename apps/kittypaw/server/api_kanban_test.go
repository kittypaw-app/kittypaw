package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
)

func TestKanbanAPIRequiresAuthAndRegistersProjectRoutes(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Server.APIKey = "api-key"
	srv := newServerWithLocalUserAndConfig(t, "alice", "pw", &cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("projects without auth code = %d, want 401", rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	req.Header.Set("x-api-key", "api-key")
	rr = httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("projects with auth code = %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"projects":[]`) {
		t.Fatalf("projects body = %s, want empty projects envelope", rr.Body.String())
	}
}

func TestKanbanAPIProjectMilestoneLifecycle(t *testing.T) {
	srv := newKanbanAPITestServer(t)

	var created struct {
		Project struct {
			ID       string `json:"id"`
			Slug     string `json:"slug"`
			Name     string `json:"name"`
			RootPath string `json:"root_path"`
		} `json:"project"`
		DefaultBoard struct {
			Slug      string `json:"slug"`
			IsDefault bool   `json:"is_default"`
		} `json:"default_board"`
	}
	kanbanAPIRequest(t, srv, http.MethodPost, "/api/v1/projects", map[string]any{
		"slug":      "kitty",
		"name":      "KittyPaw",
		"root_path": "/repo/kitty",
	}, http.StatusCreated, &created)
	if created.Project.ID == "" || created.Project.Slug != "kitty" || created.Project.Name != "KittyPaw" || created.Project.RootPath != "/repo/kitty" {
		t.Fatalf("created project = %+v", created.Project)
	}
	if created.DefaultBoard.Slug != "default" || !created.DefaultBoard.IsDefault {
		t.Fatalf("default board = %+v", created.DefaultBoard)
	}

	var shown struct {
		Project struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
		} `json:"project"`
	}
	kanbanAPIRequest(t, srv, http.MethodGet, "/api/v1/projects/kitty", nil, http.StatusOK, &shown)
	if shown.Project.ID != created.Project.ID || shown.Project.Slug != "kitty" {
		t.Fatalf("shown project = %+v, created = %+v", shown.Project, created.Project)
	}

	var boards struct {
		Boards []struct {
			Slug      string `json:"slug"`
			IsDefault bool   `json:"is_default"`
		} `json:"boards"`
	}
	kanbanAPIRequest(t, srv, http.MethodGet, "/api/v1/projects/kitty/boards", nil, http.StatusOK, &boards)
	if len(boards.Boards) != 1 || boards.Boards[0].Slug != "default" || !boards.Boards[0].IsDefault {
		t.Fatalf("boards = %+v", boards.Boards)
	}

	var milestoneCreated struct {
		Milestone struct {
			ID         string `json:"id"`
			Slug       string `json:"slug"`
			Title      string `json:"title"`
			TargetDate string `json:"target_date"`
		} `json:"milestone"`
	}
	kanbanAPIRequest(t, srv, http.MethodPost, "/api/v1/projects/kitty/milestones", map[string]any{
		"title":       "Kanban API",
		"description": "HTTP phase",
		"target_date": "2026-05-31",
	}, http.StatusCreated, &milestoneCreated)
	if milestoneCreated.Milestone.ID == "" || milestoneCreated.Milestone.Slug != "kanban-api" || milestoneCreated.Milestone.TargetDate != "2026-05-31" {
		t.Fatalf("milestone = %+v", milestoneCreated.Milestone)
	}

	var milestones struct {
		Milestones []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"milestones"`
	}
	kanbanAPIRequest(t, srv, http.MethodGet, "/api/v1/projects/kitty/milestones", nil, http.StatusOK, &milestones)
	if len(milestones.Milestones) != 1 || milestones.Milestones[0].ID != milestoneCreated.Milestone.ID {
		t.Fatalf("milestones = %+v", milestones.Milestones)
	}
}

func TestKanbanAPITaskCreateListShow(t *testing.T) {
	srv := newKanbanAPITestServer(t)
	kanbanAPICreateProject(t, srv, "kitty")
	kanbanAPICreateMilestone(t, srv, "kitty", "Kanban API")

	var created struct {
		Task struct {
			ID          string `json:"id"`
			ProjectID   string `json:"project_id"`
			MilestoneID string `json:"milestone_id"`
			Title       string `json:"title"`
			Status      string `json:"status"`
			Assignee    string `json:"assignee"`
		} `json:"task"`
	}
	kanbanAPIRequest(t, srv, http.MethodPost, "/api/v1/kanban/tasks", map[string]any{
		"project":    "kitty",
		"milestone":  "kanban-api",
		"title":      "Expose task API",
		"body":       "HTTP task create/list/show",
		"status":     "todo",
		"priority":   3,
		"assignee":   "alice",
		"created_by": "bob",
	}, http.StatusCreated, &created)
	if created.Task.ID == "" || created.Task.Title != "Expose task API" || created.Task.Status != "todo" || created.Task.Assignee != "alice" || created.Task.MilestoneID == "" {
		t.Fatalf("created task = %+v", created.Task)
	}

	var listed struct {
		Tasks []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Title  string `json:"title"`
		} `json:"tasks"`
	}
	kanbanAPIRequest(t, srv, http.MethodGet, "/api/v1/kanban/tasks?project=kitty&status=todo", nil, http.StatusOK, &listed)
	if len(listed.Tasks) != 1 || listed.Tasks[0].ID != created.Task.ID || listed.Tasks[0].Status != "todo" {
		t.Fatalf("listed tasks = %+v", listed.Tasks)
	}

	var shown struct {
		Task struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"task"`
		Comments []any `json:"comments"`
		Events   []any `json:"events"`
		Runs     []any `json:"runs"`
	}
	kanbanAPIRequest(t, srv, http.MethodGet, "/api/v1/kanban/tasks/"+created.Task.ID, nil, http.StatusOK, &shown)
	if shown.Task.ID != created.Task.ID || shown.Task.Title != "Expose task API" {
		t.Fatalf("shown task = %+v", shown.Task)
	}
	if shown.Comments == nil || shown.Events == nil || shown.Runs == nil {
		t.Fatalf("task detail missing comments/events/runs envelopes: %+v", shown)
	}
}

func newKanbanAPITestServer(t *testing.T) *Server {
	t.Helper()
	cfg := core.DefaultConfig()
	cfg.Server.APIKey = "api-key"
	return newServerWithLocalUserAndConfig(t, "alice", "pw", &cfg)
}

func kanbanAPICreateProject(t *testing.T, srv *Server, slug string) {
	t.Helper()
	kanbanAPIRequest(t, srv, http.MethodPost, "/api/v1/projects", map[string]any{
		"slug":      slug,
		"name":      slug,
		"root_path": "/repo/" + slug,
	}, http.StatusCreated, nil)
}

func kanbanAPICreateMilestone(t *testing.T, srv *Server, project, title string) {
	t.Helper()
	kanbanAPIRequest(t, srv, http.MethodPost, "/api/v1/projects/"+project+"/milestones", map[string]any{
		"title": title,
	}, http.StatusCreated, nil)
}

func kanbanAPIRequest(t *testing.T, srv *Server, method, path string, body any, wantStatus int, dst any) *httptest.ResponseRecorder {
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
