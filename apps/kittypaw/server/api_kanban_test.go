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

func TestKanbanAPITaskActionsCommentsRunsAndLinks(t *testing.T) {
	srv := newKanbanAPITestServer(t)
	kanbanAPICreateProject(t, srv, "kitty")
	parentID := kanbanAPICreateTask(t, srv, "kitty", "Parent task")
	childID := kanbanAPICreateTask(t, srv, "kitty", "Child task")

	kanbanAPIRequest(t, srv, http.MethodPost, "/api/v1/kanban/tasks/"+parentID+"/links", map[string]any{
		"child_id": childID,
	}, http.StatusOK, nil)

	var claimed struct {
		Run struct {
			ID      string `json:"id"`
			TaskID  string `json:"task_id"`
			Outcome string `json:"outcome"`
			WorkDir string `json:"work_dir"`
		} `json:"run"`
	}
	kanbanAPIRequest(t, srv, http.MethodPost, "/api/v1/kanban/tasks/"+parentID+"/claim", map[string]any{
		"actor": "alice",
	}, http.StatusOK, &claimed)
	if claimed.Run.ID == "" || claimed.Run.TaskID != parentID || claimed.Run.Outcome != "running" || claimed.Run.WorkDir != "/repo/kitty" {
		t.Fatalf("claimed run = %+v", claimed.Run)
	}

	var completed struct {
		Task struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"task"`
	}
	kanbanAPIRequest(t, srv, http.MethodPost, "/api/v1/kanban/tasks/"+parentID+"/complete", map[string]any{
		"actor":   "alice",
		"summary": "implemented",
		"metadata": map[string]any{
			"tests": []string{"go test ./server"},
		},
	}, http.StatusOK, &completed)
	if completed.Task.ID != parentID || completed.Task.Status != "done" {
		t.Fatalf("completed task = %+v", completed.Task)
	}

	var runs struct {
		Runs []struct {
			ID           string `json:"id"`
			Outcome      string `json:"outcome"`
			Summary      string `json:"summary"`
			MetadataJSON string `json:"metadata_json"`
		} `json:"runs"`
	}
	kanbanAPIRequest(t, srv, http.MethodGet, "/api/v1/kanban/tasks/"+parentID+"/runs", nil, http.StatusOK, &runs)
	if len(runs.Runs) != 1 || runs.Runs[0].Outcome != "completed" || runs.Runs[0].Summary != "implemented" || !strings.Contains(runs.Runs[0].MetadataJSON, "go test ./server") {
		t.Fatalf("runs = %+v", runs.Runs)
	}

	var commentCreated struct {
		Comment struct {
			ID   string `json:"id"`
			Body string `json:"body"`
		} `json:"comment"`
	}
	kanbanAPIRequest(t, srv, http.MethodPost, "/api/v1/kanban/tasks/"+parentID+"/comments", map[string]any{
		"author": "alice",
		"body":   "ready for review",
	}, http.StatusCreated, &commentCreated)
	if commentCreated.Comment.ID == "" || commentCreated.Comment.Body != "ready for review" {
		t.Fatalf("comment = %+v", commentCreated.Comment)
	}

	var comments struct {
		Comments []struct {
			ID string `json:"id"`
		} `json:"comments"`
	}
	kanbanAPIRequest(t, srv, http.MethodGet, "/api/v1/kanban/tasks/"+parentID+"/comments", nil, http.StatusOK, &comments)
	if len(comments.Comments) != 1 || comments.Comments[0].ID != commentCreated.Comment.ID {
		t.Fatalf("comments = %+v", comments.Comments)
	}

	var child struct {
		Task struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"task"`
	}
	kanbanAPIRequest(t, srv, http.MethodGet, "/api/v1/kanban/tasks/"+childID, nil, http.StatusOK, &child)
	if child.Task.ID != childID || child.Task.Status != "ready" {
		t.Fatalf("child after parent complete = %+v", child.Task)
	}
}

func TestKanbanAPIValidationAndNotFound(t *testing.T) {
	srv := newKanbanAPITestServer(t)

	kanbanAPIRequest(t, srv, http.MethodPost, "/api/v1/projects", map[string]any{
		"slug":      "bad",
		"root_path": "relative/path",
	}, http.StatusBadRequest, nil)

	kanbanAPIRequest(t, srv, http.MethodGet, "/api/v1/projects/missing", nil, http.StatusNotFound, nil)
	kanbanAPIRequest(t, srv, http.MethodGet, "/api/v1/kanban/tasks", nil, http.StatusBadRequest, nil)

	kanbanAPICreateProject(t, srv, "kitty")
	kanbanAPIRequest(t, srv, http.MethodPost, "/api/v1/kanban/tasks", map[string]any{
		"project": "kitty",
		"title":   "Bad status",
		"status":  "bogus",
	}, http.StatusBadRequest, nil)

	taskID := kanbanAPICreateTask(t, srv, "kitty", "Needs summary")
	kanbanAPIRequest(t, srv, http.MethodPost, "/api/v1/kanban/tasks/"+taskID+"/complete", map[string]any{
		"actor": "alice",
	}, http.StatusBadRequest, nil)
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

func kanbanAPICreateTask(t *testing.T, srv *Server, project, title string) string {
	t.Helper()
	var created struct {
		Task struct {
			ID string `json:"id"`
		} `json:"task"`
	}
	kanbanAPIRequest(t, srv, http.MethodPost, "/api/v1/kanban/tasks", map[string]any{
		"project": project,
		"title":   title,
		"status":  "todo",
	}, http.StatusCreated, &created)
	if created.Task.ID == "" {
		t.Fatalf("created task id is empty for %q", title)
	}
	return created.Task.ID
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
