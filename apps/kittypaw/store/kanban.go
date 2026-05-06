package store

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// KanbanProject is a local account-owned workstream.
type KanbanProject struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	RootPath  string `json:"root_path"`
	Archived  bool   `json:"archived"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// KanbanBoard is a project-level flow view.
type KanbanBoard struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
	Archived  bool   `json:"archived"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// KanbanMilestone is a project-level goal, release, or delivery scope.
type KanbanMilestone struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	TargetDate  string `json:"target_date"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type CreateKanbanProjectRequest struct {
	Slug     string
	Name     string
	RootPath string
}

type CreateKanbanMilestoneRequest struct {
	ProjectID   string
	Title       string
	Description string
	TargetDate  string
}

var kanbanSlugUnsafe = regexp.MustCompile(`[^a-z0-9]+`)

func newKanbanID(prefix string) string {
	return prefix + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
}

func kanbanSlug(input string) string {
	s := strings.ToLower(strings.TrimSpace(input))
	s = kanbanSlugUnsafe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "item"
	}
	return s
}

func kanbanNow() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}

// CreateKanbanProject inserts a project and its default board atomically.
func (s *Store) CreateKanbanProject(req CreateKanbanProjectRequest) (*KanbanProject, error) {
	slug := kanbanSlug(req.Slug)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = slug
	}
	rootPath := strings.TrimSpace(req.RootPath)
	if rootPath == "" {
		return nil, fmt.Errorf("root path is required")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	projectID := newKanbanID("prj_")
	now := kanbanNow()
	if _, err := tx.Exec(`
		INSERT INTO kanban_projects (id, slug, name, root_path, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		projectID, slug, name, rootPath, now, now); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`
		INSERT INTO kanban_boards (id, project_id, slug, name, is_default, created_at, updated_at)
		VALUES (?, ?, 'default', 'Default', 1, ?, ?)`,
		newKanbanID("brd_"), projectID, now, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetKanbanProject(projectID)
}

func (s *Store) GetKanbanProject(idOrSlug string) (*KanbanProject, error) {
	var p KanbanProject
	err := s.db.QueryRow(`
		SELECT id, slug, name, root_path, archived, created_at, updated_at
		FROM kanban_projects
		WHERE id = ? OR slug = ?`, idOrSlug, idOrSlug).
		Scan(&p.ID, &p.Slug, &p.Name, &p.RootPath, &p.Archived, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) ListKanbanProjects(includeArchived bool) ([]KanbanProject, error) {
	query := `SELECT id, slug, name, root_path, archived, created_at, updated_at FROM kanban_projects`
	if !includeArchived {
		query += ` WHERE archived = 0`
	}
	query += ` ORDER BY created_at`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []KanbanProject
	for rows.Next() {
		var p KanbanProject
		if err := rows.Scan(&p.ID, &p.Slug, &p.Name, &p.RootPath, &p.Archived, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetDefaultKanbanBoard(projectID string) (*KanbanBoard, error) {
	return s.scanKanbanBoard(s.db.QueryRow(`
		SELECT id, project_id, slug, name, is_default, archived, created_at, updated_at
		FROM kanban_boards WHERE project_id = ? AND is_default = 1`, projectID))
}

func (s *Store) GetKanbanBoard(projectID, idOrSlug string) (*KanbanBoard, error) {
	return s.scanKanbanBoard(s.db.QueryRow(`
		SELECT id, project_id, slug, name, is_default, archived, created_at, updated_at
		FROM kanban_boards
		WHERE project_id = ? AND (id = ? OR slug = ?)`, projectID, idOrSlug, idOrSlug))
}

func (s *Store) ListKanbanBoards(projectID string) ([]KanbanBoard, error) {
	rows, err := s.db.Query(`
		SELECT id, project_id, slug, name, is_default, archived, created_at, updated_at
		FROM kanban_boards WHERE project_id = ? AND archived = 0 ORDER BY is_default DESC, created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []KanbanBoard
	for rows.Next() {
		var b KanbanBoard
		if err := rows.Scan(&b.ID, &b.ProjectID, &b.Slug, &b.Name, &b.IsDefault, &b.Archived, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) scanKanbanBoard(row *sql.Row) (*KanbanBoard, error) {
	var b KanbanBoard
	if err := row.Scan(&b.ID, &b.ProjectID, &b.Slug, &b.Name, &b.IsDefault, &b.Archived, &b.CreatedAt, &b.UpdatedAt); err != nil {
		return nil, err
	}
	return &b, nil
}

func (s *Store) CreateKanbanMilestone(req CreateKanbanMilestoneRequest) (*KanbanMilestone, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}
	id := newKanbanID("ms_")
	now := kanbanNow()
	if _, err := s.db.Exec(`
		INSERT INTO kanban_milestones (id, project_id, slug, title, description, status, target_date, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'open', ?, ?, ?)`,
		id, req.ProjectID, kanbanSlug(title), title, req.Description, req.TargetDate, now, now); err != nil {
		return nil, err
	}
	return s.GetKanbanMilestone(req.ProjectID, id)
}

func (s *Store) GetKanbanMilestone(projectID, idOrSlug string) (*KanbanMilestone, error) {
	var m KanbanMilestone
	err := s.db.QueryRow(`
		SELECT id, project_id, slug, title, description, status, target_date, created_at, updated_at
		FROM kanban_milestones
		WHERE project_id = ? AND (id = ? OR slug = ?)`, projectID, idOrSlug, idOrSlug).
		Scan(&m.ID, &m.ProjectID, &m.Slug, &m.Title, &m.Description, &m.Status, &m.TargetDate, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Store) ListKanbanMilestones(projectID string) ([]KanbanMilestone, error) {
	rows, err := s.db.Query(`
		SELECT id, project_id, slug, title, description, status, target_date, created_at, updated_at
		FROM kanban_milestones WHERE project_id = ? ORDER BY created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []KanbanMilestone
	for rows.Next() {
		var m KanbanMilestone
		if err := rows.Scan(&m.ID, &m.ProjectID, &m.Slug, &m.Title, &m.Description, &m.Status, &m.TargetDate, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
