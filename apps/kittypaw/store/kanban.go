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

type KanbanTask struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	BoardID     string `json:"board_id"`
	MilestoneID string `json:"milestone_id,omitempty"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	Status      string `json:"status"`
	Priority    int    `json:"priority"`
	Assignee    string `json:"assignee"`
	CreatedBy   string `json:"created_by"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	CompletedAt string `json:"completed_at,omitempty"`
}

type KanbanRun struct {
	ID              string `json:"id"`
	TaskID          string `json:"task_id"`
	Actor           string `json:"actor"`
	WorkDir         string `json:"work_dir"`
	WorkDirProvider string `json:"work_dir_provider"`
	Outcome         string `json:"outcome"`
	Summary         string `json:"summary"`
	MetadataJSON    string `json:"metadata_json"`
	Error           string `json:"error"`
	StartedAt       string `json:"started_at"`
	HeartbeatAt     string `json:"heartbeat_at"`
	FinishedAt      string `json:"finished_at,omitempty"`
}

type KanbanComment struct {
	ID        string `json:"id"`
	TaskID    string `json:"task_id"`
	Author    string `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

type KanbanEvent struct {
	ID           string `json:"id"`
	TaskID       string `json:"task_id"`
	Actor        string `json:"actor"`
	EventType    string `json:"event_type"`
	Detail       string `json:"detail"`
	MetadataJSON string `json:"metadata_json"`
	CreatedAt    string `json:"created_at"`
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

type CreateKanbanTaskRequest struct {
	ProjectID   string
	BoardID     string
	MilestoneID string
	Title       string
	Body        string
	Status      string
	Priority    int
	Assignee    string
	CreatedBy   string
}

type ClaimKanbanTaskRequest struct {
	Actor           string
	WorkDir         string
	WorkDirProvider string
}

type CompleteKanbanTaskRequest struct {
	Actor        string
	Summary      string
	MetadataJSON string
}

type FailKanbanTaskRequest struct {
	Actor        string
	Summary      string
	Error        string
	MetadataJSON string
}

type BlockKanbanTaskRequest struct {
	Actor  string
	Reason string
}

type UnblockKanbanTaskRequest struct {
	Actor   string
	Comment string
}

type KanbanTaskListFilter struct {
	ProjectID   string
	BoardID     string
	MilestoneID string
	Status      string
}

const (
	KanbanStatusTriage   = "triage"
	KanbanStatusTodo     = "todo"
	KanbanStatusReady    = "ready"
	KanbanStatusRunning  = "running"
	KanbanStatusBlocked  = "blocked"
	KanbanStatusDone     = "done"
	KanbanStatusArchived = "archived"

	KanbanRunRunning   = "running"
	KanbanRunCompleted = "completed"
	KanbanRunBlocked   = "blocked"
	KanbanRunFailed    = "failed"
	KanbanRunCanceled  = "canceled"
	KanbanRunReclaimed = "reclaimed"

	KanbanWorkDirProjectRoot = "project_root"
	KanbanWorkDirManual      = "manual"
	KanbanWorkDirScratch     = "scratch"
)

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

func (s *Store) CreateKanbanTask(req CreateKanbanTaskRequest) (*KanbanTask, error) {
	if strings.TrimSpace(req.ProjectID) == "" {
		return nil, fmt.Errorf("project id is required")
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}
	boardID := req.BoardID
	if boardID == "" {
		board, err := s.GetDefaultKanbanBoard(req.ProjectID)
		if err != nil {
			return nil, fmt.Errorf("resolve default board: %w", err)
		}
		boardID = board.ID
	} else {
		board, err := s.GetKanbanBoard(req.ProjectID, boardID)
		if err != nil {
			return nil, fmt.Errorf("resolve board: %w", err)
		}
		boardID = board.ID
	}
	status := req.Status
	if status == "" {
		status = KanbanStatusTriage
	}
	var milestone any
	if req.MilestoneID != "" {
		ms, err := s.GetKanbanMilestone(req.ProjectID, req.MilestoneID)
		if err != nil {
			return nil, fmt.Errorf("resolve milestone: %w", err)
		}
		milestone = ms.ID
	}
	now := kanbanNow()
	id := newKanbanID("tsk_")

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		INSERT INTO kanban_tasks (
			id, project_id, board_id, milestone_id, title, body, status,
			priority, assignee, created_by, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, req.ProjectID, boardID, milestone, title, req.Body, status,
		req.Priority, req.Assignee, req.CreatedBy, now, now); err != nil {
		return nil, err
	}
	if err := recordKanbanEventTx(tx, id, req.CreatedBy, "created", title, "{}"); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetKanbanTask(id)
}

func (s *Store) GetKanbanTask(id string) (*KanbanTask, error) {
	return scanKanbanTask(s.db.QueryRow(`
		SELECT id, project_id, board_id, milestone_id, title, body, status,
			priority, assignee, created_by, created_at, updated_at, completed_at
		FROM kanban_tasks WHERE id = ?`, id))
}

func (s *Store) ListKanbanTasks(filter KanbanTaskListFilter) ([]KanbanTask, error) {
	query := `SELECT id, project_id, board_id, milestone_id, title, body, status,
		priority, assignee, created_by, created_at, updated_at, completed_at
		FROM kanban_tasks WHERE 1=1`
	var args []any
	if filter.ProjectID != "" {
		query += ` AND project_id = ?`
		args = append(args, filter.ProjectID)
	}
	if filter.BoardID != "" {
		query += ` AND board_id = ?`
		args = append(args, filter.BoardID)
	}
	if filter.MilestoneID != "" {
		query += ` AND milestone_id = ?`
		args = append(args, filter.MilestoneID)
	}
	if filter.Status != "" {
		query += ` AND status = ?`
		args = append(args, filter.Status)
	}
	query += ` ORDER BY priority DESC, created_at`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []KanbanTask
	for rows.Next() {
		task, err := scanKanbanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *task)
	}
	return out, rows.Err()
}

func (s *Store) ClaimKanbanTask(taskID string, req ClaimKanbanTaskRequest) (*KanbanRun, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	task, err := scanKanbanTask(tx.QueryRow(`
		SELECT id, project_id, board_id, milestone_id, title, body, status,
			priority, assignee, created_by, created_at, updated_at, completed_at
		FROM kanban_tasks WHERE id = ?`, taskID))
	if err != nil {
		return nil, err
	}
	if task.Status == KanbanStatusRunning || task.Status == KanbanStatusDone || task.Status == KanbanStatusArchived {
		return nil, fmt.Errorf("task %s is not claimable from status %q", taskID, task.Status)
	}
	project, err := scanKanbanProject(tx.QueryRow(`
		SELECT id, slug, name, root_path, archived, created_at, updated_at
		FROM kanban_projects WHERE id = ?`, task.ProjectID))
	if err != nil {
		return nil, err
	}
	workDir := strings.TrimSpace(req.WorkDir)
	provider := strings.TrimSpace(req.WorkDirProvider)
	if workDir == "" {
		workDir = project.RootPath
	}
	if provider == "" {
		provider = KanbanWorkDirProjectRoot
	}
	now := kanbanNow()
	runID := newKanbanID("run_")
	if _, err := tx.Exec(`
		INSERT INTO kanban_task_runs (
			id, task_id, actor, work_dir, work_dir_provider, outcome,
			started_at, heartbeat_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		runID, taskID, req.Actor, workDir, provider, KanbanRunRunning, now, now); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE kanban_tasks SET status = ?, updated_at = ? WHERE id = ?`,
		KanbanStatusRunning, now, taskID); err != nil {
		return nil, err
	}
	if err := recordKanbanEventTx(tx, taskID, req.Actor, "claimed", workDir, "{}"); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.getKanbanRun(runID)
}

func (s *Store) CompleteKanbanTask(taskID string, req CompleteKanbanTaskRequest) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := kanbanNow()
	metadata := normalizeKanbanJSON(req.MetadataJSON)
	res, err := tx.Exec(`
		UPDATE kanban_task_runs
		SET outcome = ?, summary = ?, metadata_json = ?, finished_at = ?, heartbeat_at = ?
		WHERE id = (
			SELECT id FROM kanban_task_runs
			WHERE task_id = ? AND outcome = ?
			ORDER BY started_at DESC
			LIMIT 1
		)`,
		KanbanRunCompleted, req.Summary, metadata, now, now, taskID, KanbanRunRunning)
	if err != nil {
		return err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return fmt.Errorf("task %s has no running run", taskID)
	}
	if _, err := tx.Exec(`
		UPDATE kanban_tasks
		SET status = ?, completed_at = ?, updated_at = ?
		WHERE id = ?`,
		KanbanStatusDone, now, now, taskID); err != nil {
		return err
	}
	if err := recordKanbanEventTx(tx, taskID, req.Actor, "completed", req.Summary, metadata); err != nil {
		return err
	}
	if err := s.promoteUnblockedChildren(tx, taskID, req.Actor); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) FailKanbanTask(taskID string, req FailKanbanTaskRequest) error {
	errorText := strings.TrimSpace(req.Error)
	if errorText == "" {
		return fmt.Errorf("error is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := kanbanNow()
	metadata := normalizeKanbanJSON(req.MetadataJSON)
	res, err := tx.Exec(`
		UPDATE kanban_task_runs
		SET outcome = ?, summary = ?, error = ?, metadata_json = ?, finished_at = ?, heartbeat_at = ?
		WHERE id = (
			SELECT id FROM kanban_task_runs
			WHERE task_id = ? AND outcome = ?
			ORDER BY started_at DESC
			LIMIT 1
		)`,
		KanbanRunFailed, strings.TrimSpace(req.Summary), errorText, metadata, now, now, taskID, KanbanRunRunning)
	if err != nil {
		return err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return fmt.Errorf("task %s has no running run", taskID)
	}
	if _, err := tx.Exec(`
		UPDATE kanban_tasks
		SET status = ?, updated_at = ?
		WHERE id = ?`,
		KanbanStatusTodo, now, taskID); err != nil {
		return err
	}
	detail := strings.TrimSpace(req.Summary)
	if detail == "" {
		detail = errorText
	}
	if err := recordKanbanEventTx(tx, taskID, req.Actor, "failed", detail, metadata); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) BlockKanbanTask(taskID string, req BlockKanbanTaskRequest) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := kanbanNow()
	if _, err := tx.Exec(`UPDATE kanban_tasks SET status = ?, updated_at = ? WHERE id = ?`,
		KanbanStatusBlocked, now, taskID); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		UPDATE kanban_task_runs
		SET outcome = ?, summary = ?, finished_at = ?, heartbeat_at = ?
		WHERE id = (
			SELECT id FROM kanban_task_runs
			WHERE task_id = ? AND outcome = ?
			ORDER BY started_at DESC
			LIMIT 1
		)`,
		KanbanRunBlocked, req.Reason, now, now, taskID, KanbanRunRunning); err != nil {
		return err
	}
	if err := recordKanbanEventTx(tx, taskID, req.Actor, "blocked", req.Reason, "{}"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UnblockKanbanTask(taskID string, req UnblockKanbanTaskRequest) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := kanbanNow()
	if _, err := tx.Exec(`UPDATE kanban_tasks SET status = ?, updated_at = ? WHERE id = ?`,
		KanbanStatusTodo, now, taskID); err != nil {
		return err
	}
	if strings.TrimSpace(req.Comment) != "" {
		if _, err := tx.Exec(`
			INSERT INTO kanban_task_comments (id, task_id, author, body, created_at)
			VALUES (?, ?, ?, ?, ?)`,
			newKanbanID("cmt_"), taskID, req.Actor, req.Comment, now); err != nil {
			return err
		}
	}
	if err := recordKanbanEventTx(tx, taskID, req.Actor, "unblocked", req.Comment, "{}"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AddKanbanTaskComment(taskID, author, body string) (*KanbanComment, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, fmt.Errorf("comment body is required")
	}
	id := newKanbanID("cmt_")
	now := kanbanNow()
	if _, err := s.db.Exec(`
		INSERT INTO kanban_task_comments (id, task_id, author, body, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		id, taskID, author, body, now); err != nil {
		return nil, err
	}
	comments, err := s.ListKanbanComments(taskID)
	if err != nil {
		return nil, err
	}
	for _, comment := range comments {
		if comment.ID == id {
			return &comment, nil
		}
	}
	return nil, sql.ErrNoRows
}

func (s *Store) ListKanbanComments(taskID string) ([]KanbanComment, error) {
	rows, err := s.db.Query(`
		SELECT id, task_id, author, body, created_at
		FROM kanban_task_comments WHERE task_id = ? ORDER BY created_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KanbanComment
	for rows.Next() {
		var c KanbanComment
		if err := rows.Scan(&c.ID, &c.TaskID, &c.Author, &c.Body, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ListKanbanEvents(taskID string) ([]KanbanEvent, error) {
	rows, err := s.db.Query(`
		SELECT id, task_id, actor, event_type, detail, metadata_json, created_at
		FROM kanban_task_events WHERE task_id = ? ORDER BY created_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KanbanEvent
	for rows.Next() {
		var e KanbanEvent
		if err := rows.Scan(&e.ID, &e.TaskID, &e.Actor, &e.EventType, &e.Detail, &e.MetadataJSON, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) ListKanbanRuns(taskID string) ([]KanbanRun, error) {
	rows, err := s.db.Query(`
		SELECT id, task_id, actor, work_dir, work_dir_provider, outcome,
			summary, metadata_json, error, started_at, heartbeat_at, finished_at
		FROM kanban_task_runs WHERE task_id = ? ORDER BY started_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KanbanRun
	for rows.Next() {
		run, err := scanKanbanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *run)
	}
	return out, rows.Err()
}

func (s *Store) LinkKanbanTasks(parentID, childID string) error {
	if parentID == "" || childID == "" {
		return fmt.Errorf("parent and child ids are required")
	}
	if parentID == childID {
		return fmt.Errorf("task cannot block itself")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	cycle, err := hasKanbanPath(tx, childID, parentID)
	if err != nil {
		return err
	}
	if cycle {
		return fmt.Errorf("kanban dependency cycle")
	}
	if _, err := tx.Exec(`
		INSERT INTO kanban_task_links (parent_id, child_id, link_type)
		VALUES (?, ?, 'blocks')`,
		parentID, childID); err != nil {
		return err
	}
	if err := recordKanbanEventTx(tx, childID, "", "linked", parentID, "{}"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) getKanbanRun(id string) (*KanbanRun, error) {
	return scanKanbanRun(s.db.QueryRow(`
		SELECT id, task_id, actor, work_dir, work_dir_provider, outcome,
			summary, metadata_json, error, started_at, heartbeat_at, finished_at
		FROM kanban_task_runs WHERE id = ?`, id))
}

type sqlScanner interface {
	Scan(dest ...any) error
}

func scanKanbanProject(row sqlScanner) (*KanbanProject, error) {
	var p KanbanProject
	if err := row.Scan(&p.ID, &p.Slug, &p.Name, &p.RootPath, &p.Archived, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

func scanKanbanTask(row sqlScanner) (*KanbanTask, error) {
	var t KanbanTask
	var milestone sql.NullString
	if err := row.Scan(
		&t.ID, &t.ProjectID, &t.BoardID, &milestone, &t.Title, &t.Body, &t.Status,
		&t.Priority, &t.Assignee, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt, &t.CompletedAt,
	); err != nil {
		return nil, err
	}
	if milestone.Valid {
		t.MilestoneID = milestone.String
	}
	return &t, nil
}

func scanKanbanRun(row sqlScanner) (*KanbanRun, error) {
	var r KanbanRun
	if err := row.Scan(
		&r.ID, &r.TaskID, &r.Actor, &r.WorkDir, &r.WorkDirProvider, &r.Outcome,
		&r.Summary, &r.MetadataJSON, &r.Error, &r.StartedAt, &r.HeartbeatAt, &r.FinishedAt,
	); err != nil {
		return nil, err
	}
	return &r, nil
}

func normalizeKanbanJSON(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "{}"
	}
	return raw
}

func recordKanbanEventTx(tx *sql.Tx, taskID, actor, eventType, detail, metadataJSON string) error {
	_, err := tx.Exec(`
		INSERT INTO kanban_task_events (id, task_id, actor, event_type, detail, metadata_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		newKanbanID("evt_"), taskID, actor, eventType, detail, normalizeKanbanJSON(metadataJSON), kanbanNow())
	return err
}

type sqlQueryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func hasKanbanPath(q sqlQueryer, startID, targetID string) (bool, error) {
	seen := map[string]bool{}
	stack := []string{startID}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		rows, err := q.Query(`
			SELECT child_id FROM kanban_task_links
			WHERE parent_id = ? AND link_type = 'blocks'`, id)
		if err != nil {
			return false, err
		}
		for rows.Next() {
			var child string
			if err := rows.Scan(&child); err != nil {
				rows.Close()
				return false, err
			}
			if child == targetID {
				rows.Close()
				return true, nil
			}
			stack = append(stack, child)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return false, err
		}
		rows.Close()
	}
	return false, nil
}

func (s *Store) promoteUnblockedChildren(tx *sql.Tx, parentID, actor string) error {
	rows, err := tx.Query(`
		SELECT child_id FROM kanban_task_links
		WHERE parent_id = ? AND link_type = 'blocks'`, parentID)
	if err != nil {
		return err
	}
	var children []string
	for rows.Next() {
		var child string
		if err := rows.Scan(&child); err != nil {
			rows.Close()
			return err
		}
		children = append(children, child)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, child := range children {
		var blockers int
		if err := tx.QueryRow(`
			SELECT COUNT(*)
			FROM kanban_task_links l
			JOIN kanban_tasks parent ON parent.id = l.parent_id
			WHERE l.child_id = ?
			  AND l.link_type = 'blocks'
			  AND parent.status != ?`,
			child, KanbanStatusDone).Scan(&blockers); err != nil {
			return err
		}
		if blockers != 0 {
			continue
		}
		now := kanbanNow()
		res, err := tx.Exec(`
			UPDATE kanban_tasks
			SET status = ?, updated_at = ?
			WHERE id = ? AND status = ?`,
			KanbanStatusReady, now, child, KanbanStatusTodo)
		if err != nil {
			return err
		}
		changed, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if changed > 0 {
			if err := recordKanbanEventTx(tx, child, actor, "promoted", "all blockers completed", "{}"); err != nil {
				return err
			}
		}
	}
	return nil
}
