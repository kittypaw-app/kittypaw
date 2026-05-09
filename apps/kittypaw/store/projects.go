package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	ProjectStateActive   = "active"
	ProjectStateArchived = "archived"

	ProjectFolderEmptyish = "empty-ish"
	ProjectFolderNonEmpty = "non-empty"

	TicketStatusDraft      = "draft"
	TicketStatusBacklog    = "backlog"
	TicketStatusReady      = "ready"
	TicketStatusInProgress = "in_progress"
	TicketStatusBlocked    = "blocked"
	TicketStatusReview     = "review"
	TicketStatusDone       = "done"
	TicketStatusArchived   = "archived"
)

type ProjectFolderClass string

type Project struct {
	ID                    string `json:"id"`
	Key                   string `json:"key"`
	Name                  string `json:"name"`
	RootPath              string `json:"root_path"`
	State                 string `json:"state"`
	NextTicketSeq         int    `json:"next_ticket_seq"`
	ProjectConversationID string `json:"project_conversation_id"`
	CreatedBy             string `json:"created_by"`
	ArchivedAt            string `json:"archived_at,omitempty"`
	CreatedAt             string `json:"created_at"`
	UpdatedAt             string `json:"updated_at"`
}

type CreateProjectRequest struct {
	Key       string
	Name      string
	RootPath  string
	CreatedBy string
}

type ConversationScope struct {
	ConversationID string `json:"conversation_id"`
	ScopeType      string `json:"scope_type"`
	ScopeID        string `json:"scope_id"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type Ticket struct {
	ID                   string `json:"id"`
	ProjectID            string `json:"project_id"`
	Key                  string `json:"key"`
	Title                string `json:"title"`
	Body                 string `json:"body"`
	Status               string `json:"status"`
	Priority             int    `json:"priority"`
	LabelsJSON           string `json:"labels_json"`
	TicketConversationID string `json:"ticket_conversation_id"`
	CreatedBy            string `json:"created_by"`
	ArchivedAt           string `json:"archived_at,omitempty"`
	CreatedAt            string `json:"created_at"`
	UpdatedAt            string `json:"updated_at"`
}

type CreateTicketRequest struct {
	ProjectID string
	Title     string
	Body      string
	Status    string
	Priority  int
	Labels    []string
	CreatedBy string
}

var projectKeyUnsafe = regexp.MustCompile(`[^A-Z0-9]+`)

func newProjectStoreID(prefix string) string {
	return prefix + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
}

func projectNow() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func normalizeProjectKey(input string) string {
	key := strings.ToUpper(strings.TrimSpace(input))
	key = projectKeyUnsafe.ReplaceAllString(key, "")
	if key == "" {
		return "PROJECT"
	}
	return key
}

func canonicalProjectRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", errors.New("root path is required")
	}
	cleaned := filepath.Clean(root)
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return canonical, nil
}

func (s *Store) CreateProject(req CreateProjectRequest) (*Project, error) {
	key := normalizeProjectKey(req.Key)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = key
	}
	root, err := canonicalProjectRoot(req.RootPath)
	if err != nil {
		return nil, err
	}
	id := newProjectStoreID("prj_")
	conversationID := "project:" + id
	now := projectNow()

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec(`
		INSERT INTO projects (
			id, key, name, root_path, state, next_ticket_seq,
			project_conversation_id, created_by, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, 1, ?, ?, ?, ?)`,
		id, key, name, root, ProjectStateActive, conversationID, strings.TrimSpace(req.CreatedBy), now, now); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`
		INSERT INTO project_driver_settings (
			project_id, default_driver_id, default_mode,
			default_worktree_policy, autonomy_policy, created_at, updated_at
		)
		VALUES (?, 'codex', 'one_shot', 'preserve', 'edit_and_test', ?, ?)`,
		id, now, now); err != nil {
		return nil, err
	}
	if err := setConversationScopeTx(tx, conversationID, "project", id, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetProject(id)
}

func (s *Store) GetProject(idOrKey string) (*Project, error) {
	idOrKey = strings.TrimSpace(idOrKey)
	if idOrKey == "" {
		return nil, sql.ErrNoRows
	}
	key := normalizeProjectKey(idOrKey)
	return scanProject(s.db.QueryRow(`
		SELECT id, key, name, root_path, state, next_ticket_seq,
		       project_conversation_id, created_by, archived_at, created_at, updated_at
		FROM projects
		WHERE id = ? OR key = ?`, idOrKey, key))
}

func (s *Store) ListProjects(includeArchived bool) ([]Project, error) {
	query := `
		SELECT id, key, name, root_path, state, next_ticket_seq,
		       project_conversation_id, created_by, archived_at, created_at, updated_at
		FROM projects`
	if !includeArchived {
		query += ` WHERE state != 'archived'`
	}
	query += ` ORDER BY updated_at DESC, created_at DESC`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Project
	for rows.Next() {
		project, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *project)
	}
	return out, rows.Err()
}

func (s *Store) UpdateProjectKey(projectID, key string) (*Project, error) {
	key = normalizeProjectKey(key)
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	var count int
	if err := tx.QueryRow("SELECT COUNT(*) FROM tickets WHERE project_id = ?", projectID).Scan(&count); err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, errors.New("project key is locked after the first ticket")
	}
	if _, err := tx.Exec("UPDATE projects SET key = ?, updated_at = ? WHERE id = ?", key, projectNow(), projectID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetProject(projectID)
}

func scanProject(row interface {
	Scan(dest ...any) error
}) (*Project, error) {
	var p Project
	if err := row.Scan(
		&p.ID, &p.Key, &p.Name, &p.RootPath, &p.State, &p.NextTicketSeq,
		&p.ProjectConversationID, &p.CreatedBy, &p.ArchivedAt, &p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) SetConversationScope(conversationID, scopeType, scopeID string) error {
	return setConversationScopeTx(s.db, conversationID, scopeType, scopeID, projectNow())
}

func setConversationScopeTx(exec interface {
	Exec(query string, args ...any) (sql.Result, error)
}, conversationID, scopeType, scopeID, now string) error {
	conversationID = strings.TrimSpace(conversationID)
	scopeType = strings.TrimSpace(scopeType)
	scopeID = strings.TrimSpace(scopeID)
	if conversationID == "" {
		return errors.New("conversation id is required")
	}
	switch scopeType {
	case "general", "project", "ticket":
	default:
		return fmt.Errorf("invalid conversation scope type: %s", scopeType)
	}
	_, err := exec.Exec(`
		INSERT INTO conversation_scope (conversation_id, scope_type, scope_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(conversation_id) DO UPDATE SET
			scope_type = excluded.scope_type,
			scope_id = excluded.scope_id,
			updated_at = excluded.updated_at`,
		conversationID, scopeType, scopeID, now, now)
	return err
}

func (s *Store) ConversationScope(conversationID string) (*ConversationScope, bool, error) {
	var scope ConversationScope
	err := s.db.QueryRow(`
		SELECT conversation_id, scope_type, scope_id, created_at, updated_at
		FROM conversation_scope
		WHERE conversation_id = ?`, strings.TrimSpace(conversationID)).
		Scan(&scope.ConversationID, &scope.ScopeType, &scope.ScopeID, &scope.CreatedAt, &scope.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &scope, true, nil
}

func (s *Store) CreateTicket(req CreateTicketRequest) (*Ticket, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return nil, errors.New("ticket title is required")
	}
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = TicketStatusBacklog
	}
	if !validTicketStatus(status) {
		return nil, fmt.Errorf("invalid ticket status: %s", status)
	}
	labelsJSON := "[]"
	if len(req.Labels) > 0 {
		data, err := json.Marshal(req.Labels)
		if err != nil {
			return nil, err
		}
		labelsJSON = string(data)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	project, err := scanProject(tx.QueryRow(`
		SELECT id, key, name, root_path, state, next_ticket_seq,
		       project_conversation_id, created_by, archived_at, created_at, updated_at
		FROM projects
		WHERE id = ? OR key = ?`, req.ProjectID, normalizeProjectKey(req.ProjectID)))
	if err != nil {
		return nil, err
	}

	ticketID := newProjectStoreID("tkt_")
	ticketKey := fmt.Sprintf("%s-%03d", project.Key, project.NextTicketSeq)
	conversationID := "ticket:" + ticketID
	now := projectNow()
	if _, err := tx.Exec(`
		INSERT INTO tickets (
			id, project_id, key, title, body, status, priority, labels_json,
			ticket_conversation_id, created_by, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ticketID, project.ID, ticketKey, title, strings.TrimSpace(req.Body), status, req.Priority,
		labelsJSON, conversationID, strings.TrimSpace(req.CreatedBy), now, now); err != nil {
		return nil, err
	}
	if _, err := tx.Exec("UPDATE projects SET next_ticket_seq = next_ticket_seq + 1, updated_at = ? WHERE id = ?", now, project.ID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`
		INSERT INTO ticket_actions (id, ticket_id, actor_id, action_type, to_status, message, created_at)
		VALUES (?, ?, ?, 'created', ?, ?, ?)`,
		newProjectStoreID("act_"), ticketID, strings.TrimSpace(req.CreatedBy), status, title, now); err != nil {
		return nil, err
	}
	if err := setConversationScopeTx(tx, conversationID, "ticket", ticketID, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetTicket(ticketID)
}

func (s *Store) GetTicket(idOrKey string) (*Ticket, error) {
	idOrKey = strings.TrimSpace(idOrKey)
	if idOrKey == "" {
		return nil, sql.ErrNoRows
	}
	return scanTicket(s.db.QueryRow(`
		SELECT id, project_id, key, title, body, status, priority, labels_json,
		       ticket_conversation_id, created_by, archived_at, created_at, updated_at
		FROM tickets
		WHERE id = ? OR key = ?`, idOrKey, strings.ToUpper(idOrKey)))
}

func scanTicket(row interface {
	Scan(dest ...any) error
}) (*Ticket, error) {
	var t Ticket
	if err := row.Scan(
		&t.ID, &t.ProjectID, &t.Key, &t.Title, &t.Body, &t.Status, &t.Priority, &t.LabelsJSON,
		&t.TicketConversationID, &t.CreatedBy, &t.ArchivedAt, &t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &t, nil
}

func validTicketStatus(status string) bool {
	switch status {
	case TicketStatusDraft, TicketStatusBacklog, TicketStatusReady, TicketStatusInProgress,
		TicketStatusBlocked, TicketStatusReview, TicketStatusDone, TicketStatusArchived:
		return true
	default:
		return false
	}
}

func ClassifyProjectFolder(root string) (ProjectFolderClass, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	incidental := map[string]bool{
		".git":         true,
		".DS_Store":    true,
		".gitignore":   true,
		".env.example": true,
		"README":       true,
		"README.md":    true,
		"README.txt":   true,
	}
	indicators := map[string]bool{
		"package.json":     true,
		"go.mod":           true,
		"Cargo.toml":       true,
		"Package.swift":    true,
		"pyproject.toml":   true,
		"requirements.txt": true,
		"Gemfile":          true,
		"pom.xml":          true,
		"build.gradle":     true,
		"Makefile":         true,
	}
	meaningful := 0
	for _, entry := range entries {
		name := entry.Name()
		if indicators[name] {
			return ProjectFolderNonEmpty, nil
		}
		if incidental[name] {
			continue
		}
		meaningful++
	}
	if meaningful > 0 {
		return ProjectFolderNonEmpty, nil
	}
	return ProjectFolderEmptyish, nil
}

func (s *Store) SelectProjectPM() (string, error) {
	staff, err := s.ListActiveStaff()
	if err != nil {
		return "", err
	}
	for _, candidate := range staff {
		if staffHasProjectManagerMetadata(candidate) {
			return candidate.ID, nil
		}
	}
	for _, candidate := range staff {
		if staffLooksLikePM(candidate) {
			return candidate.ID, nil
		}
		aliases, err := s.ListStaffAliases(candidate.ID)
		if err != nil {
			return "", err
		}
		for _, alias := range aliases {
			if pmNameMatch(alias) {
				return candidate.ID, nil
			}
		}
	}
	return "default", nil
}

func staffHasProjectManagerMetadata(staff StaffMeta) bool {
	var tags []string
	if err := json.Unmarshal([]byte(staff.EquippedSkills), &tags); err == nil {
		for _, tag := range tags {
			switch strings.ToLower(strings.TrimSpace(tag)) {
			case "project-manager", "project_manager", "pm":
				return true
			}
		}
	}
	desc := strings.ToLower(staff.Description)
	return strings.Contains(desc, "role:pm") || strings.Contains(desc, "role:project-manager")
}

func staffLooksLikePM(staff StaffMeta) bool {
	return pmNameMatch(staff.ID) || pmNameMatch(staff.DisplayName)
}

func pmNameMatch(input string) bool {
	collapsed := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(input), " ", ""))
	switch collapsed {
	case "pm", "projectmanager", "project-manager", "project_manager", "개발pm", "프로젝트매니저":
		return true
	default:
		return false
	}
}
