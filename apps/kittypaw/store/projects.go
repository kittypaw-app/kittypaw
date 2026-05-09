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

	JobStatusPlanned   = "planned"
	JobStatusApproved  = "approved"
	JobStatusRunning   = "running"
	JobStatusSucceeded = "succeeded"
	JobStatusFailed    = "failed"
	JobStatusCanceled  = "canceled"

	JobModeOneShot = "one_shot"
	JobModePTY     = "pty"
	JobModeTmux    = "tmux"
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

type TicketAction struct {
	ID           string `json:"id"`
	TicketID     string `json:"ticket_id"`
	ActorID      string `json:"actor_id"`
	ActionType   string `json:"action_type"`
	FromStatus   string `json:"from_status"`
	ToStatus     string `json:"to_status"`
	Message      string `json:"message"`
	MetadataJSON string `json:"metadata_json"`
	CreatedAt    string `json:"created_at"`
}

type TicketMessage struct {
	ID             string `json:"id"`
	TicketID       string `json:"ticket_id"`
	ConversationID string `json:"conversation_id"`
	AuthorID       string `json:"author_id"`
	Body           string `json:"body"`
	MetadataJSON   string `json:"metadata_json"`
	CreatedAt      string `json:"created_at"`
}

type AddTicketMessageRequest struct {
	TicketID       string
	ConversationID string
	AuthorID       string
	Body           string
	MetadataJSON   string
}

type TicketListFilter struct {
	ProjectID       string
	Status          string
	IncludeArchived bool
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

type MoveTicketRequest struct {
	ActorID string
	Status  string
	Message string
}

type ProjectBoard struct {
	ProjectID string              `json:"project_id"`
	Columns   map[string][]Ticket `json:"columns"`
}

type TicketDependency struct {
	ID              string `json:"id"`
	ProjectID       string `json:"project_id"`
	BlockerTicketID string `json:"blocker_ticket_id"`
	BlockedTicketID string `json:"blocked_ticket_id"`
	Type            string `json:"type"`
	CreatedBy       string `json:"created_by"`
	CreatedAt       string `json:"created_at"`
}

type CreateTicketDependencyRequest struct {
	ProjectID       string
	BlockerTicketID string
	BlockedTicketID string
	Type            string
	CreatedBy       string
}

type ProjectBriefDraft struct {
	ID                  string `json:"id"`
	ProjectID           string `json:"project_id"`
	Status              string `json:"status"`
	Title               string `json:"title"`
	BriefJSON           string `json:"brief_json"`
	ProposedTicketsJSON string `json:"proposed_tickets_json"`
	CreatedBy           string `json:"created_by"`
	CommittedAt         string `json:"committed_at,omitempty"`
	CreatedAt           string `json:"created_at"`
	UpdatedAt           string `json:"updated_at"`
}

type CreateProjectBriefDraftRequest struct {
	ProjectID           string
	Title               string
	BriefJSON           string
	ProposedTicketsJSON string
	CreatedBy           string
}

type UpdateProjectBriefDraftRequest struct {
	Title               *string
	BriefJSON           *string
	ProposedTicketsJSON *string
}

type CommitProjectBriefDraftResult struct {
	Draft   ProjectBriefDraft `json:"draft"`
	Tickets []Ticket          `json:"tickets"`
}

type DriverDefinition struct {
	ID                 string `json:"id"`
	DisplayName        string `json:"display_name"`
	Command            string `json:"command"`
	SupportedModesJSON string `json:"supported_modes_json"`
	DefaultArgsJSON    string `json:"default_args_json"`
	Enabled            bool   `json:"enabled"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

type UpsertDriverRequest struct {
	ID                 string
	DisplayName        string
	Command            string
	SupportedModesJSON string
	DefaultArgsJSON    string
	Enabled            bool
}

type Job struct {
	ID                 string `json:"id"`
	ProjectID          string `json:"project_id"`
	TicketID           string `json:"ticket_id"`
	DriverID           string `json:"driver_id"`
	Mode               string `json:"mode"`
	Status             string `json:"status"`
	WorktreePath       string `json:"worktree_path"`
	BranchName         string `json:"branch_name"`
	PromptSummary      string `json:"prompt_summary"`
	PromptText         string `json:"prompt_text"`
	ResultSummary      string `json:"result_summary"`
	LogTail            string `json:"log_tail"`
	ErrorExcerpt       string `json:"error_excerpt"`
	LogTruncated       bool   `json:"log_truncated"`
	DriverSnapshotJSON string `json:"driver_snapshot_json"`
	CreatedBy          string `json:"created_by"`
	ApprovedBy         string `json:"approved_by"`
	StartedAt          string `json:"started_at,omitempty"`
	FinishedAt         string `json:"finished_at,omitempty"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

type PlanJobRequest struct {
	ProjectID     string
	TicketID      string
	DriverID      string
	Mode          string
	WorktreePath  string
	BranchName    string
	PromptSummary string
	PromptText    string
	CreatedBy     string
}

type JobEvent struct {
	ID           string `json:"id"`
	JobID        string `json:"job_id"`
	Type         string `json:"type"`
	ActorID      string `json:"actor_id"`
	Message      string `json:"message"`
	MetadataJSON string `json:"metadata_json"`
	CreatedAt    string `json:"created_at"`
}

type proposedBriefTicket struct {
	TempID       string                    `json:"temp_id"`
	Title        string                    `json:"title"`
	Body         string                    `json:"body"`
	Status       string                    `json:"status"`
	Priority     int                       `json:"priority"`
	StaffID      string                    `json:"staff_id"`
	StaffRole    string                    `json:"staff_role"`
	Dependencies []proposedBriefDependency `json:"dependencies"`
}

type proposedBriefDependency struct {
	BlockerTempID string `json:"blocker_temp_id"`
	Type          string `json:"type"`
}

var projectKeyUnsafe = regexp.MustCompile(`[^A-Z0-9]+`)

func newProjectStoreID(prefix string) string {
	return prefix + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
}

func projectNow() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
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

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	ticket, err := createTicketTx(tx, CreateTicketRequest{
		ProjectID: req.ProjectID,
		Title:     title,
		Body:      req.Body,
		Status:    status,
		Priority:  req.Priority,
		Labels:    req.Labels,
		CreatedBy: req.CreatedBy,
	})
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetTicket(ticket.ID)
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

func (s *Store) ListTickets(filter TicketListFilter) ([]Ticket, error) {
	query := `
		SELECT id, project_id, key, title, body, status, priority, labels_json,
		       ticket_conversation_id, created_by, archived_at, created_at, updated_at
		FROM tickets
		WHERE 1=1`
	var args []any
	if strings.TrimSpace(filter.ProjectID) != "" {
		query += " AND project_id = ?"
		args = append(args, strings.TrimSpace(filter.ProjectID))
	}
	if strings.TrimSpace(filter.Status) != "" {
		query += " AND status = ?"
		args = append(args, strings.TrimSpace(filter.Status))
	} else if !filter.IncludeArchived {
		query += " AND status != ?"
		args = append(args, TicketStatusArchived)
	}
	query += " ORDER BY priority DESC, created_at ASC"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Ticket
	for rows.Next() {
		ticket, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *ticket)
	}
	return out, rows.Err()
}

func (s *Store) MoveTicket(ticketID string, req MoveTicketRequest) (*Ticket, error) {
	status := strings.TrimSpace(req.Status)
	if !validTicketStatus(status) {
		return nil, fmt.Errorf("invalid ticket status: %s", status)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	ticket, err := scanTicket(tx.QueryRow(`
		SELECT id, project_id, key, title, body, status, priority, labels_json,
		       ticket_conversation_id, created_by, archived_at, created_at, updated_at
		FROM tickets
		WHERE id = ? OR key = ?`, strings.TrimSpace(ticketID), strings.ToUpper(strings.TrimSpace(ticketID))))
	if err != nil {
		return nil, err
	}
	now := projectNow()
	archivedAt := ticket.ArchivedAt
	if status == TicketStatusArchived && archivedAt == "" {
		archivedAt = now
	}
	if _, err := tx.Exec(`
		UPDATE tickets
		SET status = ?, archived_at = ?, updated_at = ?
		WHERE id = ?`, status, archivedAt, now, ticket.ID); err != nil {
		return nil, err
	}
	if err := insertTicketActionTx(tx, ticket.ID, strings.TrimSpace(req.ActorID), "status_changed", ticket.Status, status, strings.TrimSpace(req.Message), "{}", now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetTicket(ticket.ID)
}

func (s *Store) ArchiveTicket(ticketID, actorID string) (*Ticket, error) {
	return s.MoveTicket(ticketID, MoveTicketRequest{
		ActorID: strings.TrimSpace(actorID),
		Status:  TicketStatusArchived,
		Message: "archived",
	})
}

func (s *Store) ListTicketActions(ticketID string) ([]TicketAction, error) {
	rows, err := s.db.Query(`
		SELECT id, ticket_id, actor_id, action_type, from_status, to_status, message, metadata_json, created_at
		FROM ticket_actions
		WHERE ticket_id = ?
		ORDER BY created_at, id`, strings.TrimSpace(ticketID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TicketAction
	for rows.Next() {
		var action TicketAction
		if err := rows.Scan(
			&action.ID, &action.TicketID, &action.ActorID, &action.ActionType,
			&action.FromStatus, &action.ToStatus, &action.Message, &action.MetadataJSON, &action.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, action)
	}
	return out, rows.Err()
}

func (s *Store) AddTicketMessage(req AddTicketMessageRequest) (*TicketMessage, error) {
	ticket, err := s.GetTicket(req.TicketID)
	if err != nil {
		return nil, err
	}
	body := strings.TrimSpace(req.Body)
	if body == "" {
		return nil, errors.New("ticket message body is required")
	}
	conversationID := strings.TrimSpace(req.ConversationID)
	if conversationID == "" {
		conversationID = ticket.TicketConversationID
	}
	metadataJSON := normalizeJSONDocument(req.MetadataJSON, "{}")
	id := newProjectStoreID("msg_")
	now := projectNow()
	if _, err := s.db.Exec(`
		INSERT INTO ticket_messages (id, ticket_id, conversation_id, author_id, body, metadata_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, ticket.ID, conversationID, strings.TrimSpace(req.AuthorID), body, metadataJSON, now); err != nil {
		return nil, err
	}
	return s.GetTicketMessage(id)
}

func (s *Store) GetTicketMessage(id string) (*TicketMessage, error) {
	var msg TicketMessage
	if err := s.db.QueryRow(`
		SELECT id, ticket_id, conversation_id, author_id, body, metadata_json, created_at
		FROM ticket_messages
		WHERE id = ?`, strings.TrimSpace(id)).
		Scan(&msg.ID, &msg.TicketID, &msg.ConversationID, &msg.AuthorID, &msg.Body, &msg.MetadataJSON, &msg.CreatedAt); err != nil {
		return nil, err
	}
	return &msg, nil
}

func (s *Store) ProjectBoard(projectID string) (*ProjectBoard, error) {
	tickets, err := s.ListTickets(TicketListFilter{ProjectID: strings.TrimSpace(projectID)})
	if err != nil {
		return nil, err
	}
	columns := make(map[string][]Ticket, 8)
	for _, status := range []string{
		TicketStatusDraft,
		TicketStatusBacklog,
		TicketStatusReady,
		TicketStatusInProgress,
		TicketStatusBlocked,
		TicketStatusReview,
		TicketStatusDone,
		TicketStatusArchived,
	} {
		columns[status] = []Ticket{}
	}
	for _, ticket := range tickets {
		columns[ticket.Status] = append(columns[ticket.Status], ticket)
	}
	return &ProjectBoard{ProjectID: strings.TrimSpace(projectID), Columns: columns}, nil
}

func (s *Store) CreateTicketDependency(req CreateTicketDependencyRequest) (*TicketDependency, error) {
	depType := strings.TrimSpace(req.Type)
	if depType == "" {
		depType = "blocks"
	}
	switch depType {
	case "blocks", "relates_to", "duplicates":
	default:
		return nil, fmt.Errorf("invalid ticket dependency type: %s", depType)
	}
	id := newProjectStoreID("dep_")
	now := projectNow()
	if _, err := s.db.Exec(`
		INSERT INTO ticket_dependencies (
			id, project_id, blocker_ticket_id, blocked_ticket_id, type, created_by, created_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, strings.TrimSpace(req.ProjectID), strings.TrimSpace(req.BlockerTicketID),
		strings.TrimSpace(req.BlockedTicketID), depType, strings.TrimSpace(req.CreatedBy), now); err != nil {
		return nil, err
	}
	return s.GetTicketDependency(id)
}

func (s *Store) GetTicketDependency(id string) (*TicketDependency, error) {
	var dep TicketDependency
	if err := s.db.QueryRow(`
		SELECT id, project_id, blocker_ticket_id, blocked_ticket_id, type, created_by, created_at
		FROM ticket_dependencies
		WHERE id = ?`, strings.TrimSpace(id)).
		Scan(&dep.ID, &dep.ProjectID, &dep.BlockerTicketID, &dep.BlockedTicketID, &dep.Type, &dep.CreatedBy, &dep.CreatedAt); err != nil {
		return nil, err
	}
	return &dep, nil
}

func (s *Store) ListTicketDependencies(projectID string) ([]TicketDependency, error) {
	rows, err := s.db.Query(`
		SELECT id, project_id, blocker_ticket_id, blocked_ticket_id, type, created_by, created_at
		FROM ticket_dependencies
		WHERE project_id = ?
		ORDER BY created_at, id`, strings.TrimSpace(projectID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TicketDependency
	for rows.Next() {
		var dep TicketDependency
		if err := rows.Scan(&dep.ID, &dep.ProjectID, &dep.BlockerTicketID, &dep.BlockedTicketID, &dep.Type, &dep.CreatedBy, &dep.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, dep)
	}
	return out, rows.Err()
}

func insertTicketActionTx(exec interface {
	Exec(query string, args ...any) (sql.Result, error)
}, ticketID, actorID, actionType, fromStatus, toStatus, message, metadataJSON, now string) error {
	if metadataJSON == "" {
		metadataJSON = "{}"
	}
	_, err := exec.Exec(`
		INSERT INTO ticket_actions (
			id, ticket_id, actor_id, action_type, from_status, to_status, message, metadata_json, created_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		newProjectStoreID("act_"), ticketID, actorID, actionType, fromStatus, toStatus, message, metadataJSON, now)
	return err
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

func createTicketTx(tx *sql.Tx, req CreateTicketRequest) (*Ticket, error) {
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
	project, err := scanProject(tx.QueryRow(`
		SELECT id, key, name, root_path, state, next_ticket_seq,
		       project_conversation_id, created_by, archived_at, created_at, updated_at
		FROM projects
		WHERE id = ? OR key = ?`, strings.TrimSpace(req.ProjectID), normalizeProjectKey(req.ProjectID)))
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
	if err := insertTicketActionTx(tx, ticketID, strings.TrimSpace(req.CreatedBy), "created", "", status, title, "{}", now); err != nil {
		return nil, err
	}
	if err := setConversationScopeTx(tx, conversationID, "ticket", ticketID, now); err != nil {
		return nil, err
	}
	return scanTicket(tx.QueryRow(`
		SELECT id, project_id, key, title, body, status, priority, labels_json,
		       ticket_conversation_id, created_by, archived_at, created_at, updated_at
		FROM tickets
		WHERE id = ?`, ticketID))
}

func (s *Store) CreateProjectBriefDraft(req CreateProjectBriefDraftRequest) (*ProjectBriefDraft, error) {
	if strings.TrimSpace(req.ProjectID) == "" {
		return nil, errors.New("project id is required")
	}
	briefJSON := normalizeJSONDocument(req.BriefJSON, "{}")
	proposedJSON := normalizeJSONDocument(req.ProposedTicketsJSON, "[]")
	id := newProjectStoreID("brf_")
	now := projectNow()
	if _, err := s.db.Exec(`
		INSERT INTO project_brief_drafts (
			id, project_id, status, title, brief_json, proposed_tickets_json, created_by, created_at, updated_at
		)
		VALUES (?, ?, 'draft', ?, ?, ?, ?, ?, ?)`,
		id, strings.TrimSpace(req.ProjectID), strings.TrimSpace(req.Title), briefJSON,
		proposedJSON, strings.TrimSpace(req.CreatedBy), now, now); err != nil {
		return nil, err
	}
	return s.GetProjectBriefDraft(id)
}

func (s *Store) UpdateProjectBriefDraft(draftID string, req UpdateProjectBriefDraftRequest) (*ProjectBriefDraft, error) {
	current, err := s.GetProjectBriefDraft(draftID)
	if err != nil {
		return nil, err
	}
	if current.Status != "draft" {
		return nil, errors.New("only draft project briefs can be updated")
	}
	title := current.Title
	briefJSON := current.BriefJSON
	proposedTicketsJSON := current.ProposedTicketsJSON
	if req.Title != nil {
		title = strings.TrimSpace(*req.Title)
	}
	if req.BriefJSON != nil {
		briefJSON = normalizeJSONDocument(*req.BriefJSON, "{}")
	}
	if req.ProposedTicketsJSON != nil {
		proposedTicketsJSON = normalizeJSONDocument(*req.ProposedTicketsJSON, "[]")
	}
	if _, err := s.db.Exec(`
		UPDATE project_brief_drafts
		SET title = ?, brief_json = ?, proposed_tickets_json = ?, updated_at = ?
		WHERE id = ?`,
		title, briefJSON, proposedTicketsJSON, projectNow(), strings.TrimSpace(draftID)); err != nil {
		return nil, err
	}
	return s.GetProjectBriefDraft(draftID)
}

func (s *Store) GetProjectBriefDraft(draftID string) (*ProjectBriefDraft, error) {
	return scanProjectBriefDraft(s.db.QueryRow(`
		SELECT id, project_id, status, title, brief_json, proposed_tickets_json,
		       created_by, committed_at, created_at, updated_at
		FROM project_brief_drafts
		WHERE id = ?`, strings.TrimSpace(draftID)))
}

func (s *Store) ListProjectBriefDrafts(projectID string) ([]ProjectBriefDraft, error) {
	rows, err := s.db.Query(`
		SELECT id, project_id, status, title, brief_json, proposed_tickets_json,
		       created_by, committed_at, created_at, updated_at
		FROM project_brief_drafts
		WHERE project_id = ?
		ORDER BY created_at DESC`, strings.TrimSpace(projectID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ProjectBriefDraft
	for rows.Next() {
		draft, err := scanProjectBriefDraft(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *draft)
	}
	return out, rows.Err()
}

func (s *Store) CommitProjectBriefDraft(draftID string, actorID string) (*CommitProjectBriefDraftResult, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	draft, err := scanProjectBriefDraft(tx.QueryRow(`
		SELECT id, project_id, status, title, brief_json, proposed_tickets_json,
		       created_by, committed_at, created_at, updated_at
		FROM project_brief_drafts
		WHERE id = ?`, strings.TrimSpace(draftID)))
	if err != nil {
		return nil, err
	}
	if draft.Status != "draft" {
		return nil, errors.New("project brief draft is already committed")
	}
	var proposals []proposedBriefTicket
	if err := json.Unmarshal([]byte(draft.ProposedTicketsJSON), &proposals); err != nil {
		return nil, fmt.Errorf("parse proposed tickets: %w", err)
	}
	now := projectNow()
	createdBy := strings.TrimSpace(actorID)
	tempToTicket := make(map[string]Ticket, len(proposals))
	tickets := make([]Ticket, 0, len(proposals))
	for _, proposal := range proposals {
		status := strings.TrimSpace(proposal.Status)
		if status == "" {
			if proposal.Priority >= 8 {
				status = TicketStatusReady
			} else {
				status = TicketStatusBacklog
			}
		}
		ticket, err := createTicketTx(tx, CreateTicketRequest{
			ProjectID: draft.ProjectID,
			Title:     proposal.Title,
			Body:      proposal.Body,
			Status:    status,
			Priority:  proposal.Priority,
			CreatedBy: createdBy,
		})
		if err != nil {
			return nil, err
		}
		tickets = append(tickets, *ticket)
		if proposal.TempID != "" {
			tempToTicket[proposal.TempID] = *ticket
		}
		if strings.TrimSpace(proposal.StaffID) != "" {
			role := strings.TrimSpace(proposal.StaffRole)
			if role == "" {
				role = "owner"
			}
			if _, err := tx.Exec(`
				INSERT INTO ticket_staff_assignments (id, ticket_id, staff_id, role, is_primary, created_at)
				VALUES (?, ?, ?, ?, 1, ?)
				ON CONFLICT(ticket_id, staff_id, role) DO NOTHING`,
				newProjectStoreID("tsa_"), ticket.ID, strings.TrimSpace(proposal.StaffID), role, now); err != nil {
				return nil, err
			}
		}
		if _, err := tx.Exec(`
			INSERT INTO ticket_messages (id, ticket_id, conversation_id, author_id, body, metadata_json, created_at)
			VALUES (?, ?, ?, ?, ?, '{}', ?)`,
			newProjectStoreID("msg_"), ticket.ID, ticket.TicketConversationID, createdBy,
			"PM briefing: "+ticket.Title, now); err != nil {
			return nil, err
		}
	}
	for _, proposal := range proposals {
		if proposal.TempID == "" {
			continue
		}
		blocked, ok := tempToTicket[proposal.TempID]
		if !ok {
			continue
		}
		for _, dep := range proposal.Dependencies {
			blocker, ok := tempToTicket[dep.BlockerTempID]
			if !ok {
				continue
			}
			depType := dep.Type
			if depType == "" {
				depType = "blocks"
			}
			if _, err := tx.Exec(`
				INSERT INTO ticket_dependencies (
					id, project_id, blocker_ticket_id, blocked_ticket_id, type, created_by, created_at
				)
				VALUES (?, ?, ?, ?, ?, ?, ?)`,
				newProjectStoreID("dep_"), draft.ProjectID, blocker.ID, blocked.ID, depType, createdBy, now); err != nil {
				return nil, err
			}
		}
	}
	if _, err := tx.Exec(`
		UPDATE project_brief_drafts
		SET status = 'committed', committed_at = ?, updated_at = ?
		WHERE id = ?`, now, now, draft.ID); err != nil {
		return nil, err
	}
	committed, err := scanProjectBriefDraft(tx.QueryRow(`
		SELECT id, project_id, status, title, brief_json, proposed_tickets_json,
		       created_by, committed_at, created_at, updated_at
		FROM project_brief_drafts
		WHERE id = ?`, draft.ID))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &CommitProjectBriefDraftResult{Draft: *committed, Tickets: tickets}, nil
}

func scanProjectBriefDraft(row interface {
	Scan(dest ...any) error
}) (*ProjectBriefDraft, error) {
	var draft ProjectBriefDraft
	if err := row.Scan(
		&draft.ID, &draft.ProjectID, &draft.Status, &draft.Title, &draft.BriefJSON,
		&draft.ProposedTicketsJSON, &draft.CreatedBy, &draft.CommittedAt,
		&draft.CreatedAt, &draft.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &draft, nil
}

func normalizeJSONDocument(input, fallback string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return fallback
	}
	var raw any
	if err := json.Unmarshal([]byte(input), &raw); err != nil {
		return fallback
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return fallback
	}
	return string(data)
}

func (s *Store) EnsureDefaultDrivers() error {
	defaults := []UpsertDriverRequest{
		{ID: "codex", DisplayName: "Codex", Command: "codex", SupportedModesJSON: `["one_shot"]`, DefaultArgsJSON: `[]`, Enabled: true},
		{ID: "claude", DisplayName: "Claude", Command: "claude", SupportedModesJSON: `["one_shot"]`, DefaultArgsJSON: `[]`, Enabled: true},
		{ID: "shell", DisplayName: "Shell", Command: "sh", SupportedModesJSON: `["one_shot"]`, DefaultArgsJSON: `[]`, Enabled: true},
	}
	for _, driver := range defaults {
		if _, err := s.UpsertDriver(driver); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ListDrivers() ([]DriverDefinition, error) {
	rows, err := s.db.Query(`
		SELECT id, display_name, command, supported_modes_json, default_args_json, enabled, created_at, updated_at
		FROM driver_definitions
		ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DriverDefinition
	for rows.Next() {
		driver, err := scanDriver(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *driver)
	}
	return out, rows.Err()
}

func (s *Store) UpsertDriver(req UpsertDriverRequest) (*DriverDefinition, error) {
	id := strings.TrimSpace(req.ID)
	if id == "" {
		return nil, errors.New("driver id is required")
	}
	displayName := strings.TrimSpace(req.DisplayName)
	if displayName == "" {
		displayName = id
	}
	command := strings.TrimSpace(req.Command)
	if command == "" {
		return nil, errors.New("driver command is required")
	}
	modesJSON := normalizeJSONDocument(req.SupportedModesJSON, `["one_shot"]`)
	argsJSON := normalizeJSONDocument(req.DefaultArgsJSON, `[]`)
	now := projectNow()
	if _, err := s.db.Exec(`
		INSERT INTO driver_definitions (
			id, display_name, command, supported_modes_json, default_args_json, enabled, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			display_name = excluded.display_name,
			command = excluded.command,
			supported_modes_json = excluded.supported_modes_json,
			default_args_json = excluded.default_args_json,
			enabled = excluded.enabled,
			updated_at = excluded.updated_at`,
		id, displayName, command, modesJSON, argsJSON, boolToInt(req.Enabled), now, now); err != nil {
		return nil, err
	}
	return s.GetDriver(id)
}

func (s *Store) GetDriver(id string) (*DriverDefinition, error) {
	return scanDriver(s.db.QueryRow(`
		SELECT id, display_name, command, supported_modes_json, default_args_json, enabled, created_at, updated_at
		FROM driver_definitions
		WHERE id = ?`, strings.TrimSpace(id)))
}

func scanDriver(row interface {
	Scan(dest ...any) error
}) (*DriverDefinition, error) {
	var driver DriverDefinition
	var enabled int
	if err := row.Scan(
		&driver.ID, &driver.DisplayName, &driver.Command, &driver.SupportedModesJSON,
		&driver.DefaultArgsJSON, &enabled, &driver.CreatedAt, &driver.UpdatedAt,
	); err != nil {
		return nil, err
	}
	driver.Enabled = enabled != 0
	return &driver, nil
}

func (s *Store) PlanJob(req PlanJobRequest) (*Job, error) {
	driver, err := s.GetDriver(req.DriverID)
	if err != nil {
		return nil, err
	}
	if !driver.Enabled {
		return nil, fmt.Errorf("driver %q is disabled", driver.ID)
	}
	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = JobModeOneShot
	}
	if !driverSupportsMode(driver, mode) {
		return nil, fmt.Errorf("driver %q does not support mode %q", driver.ID, mode)
	}
	snapshot, err := json.Marshal(driver)
	if err != nil {
		return nil, err
	}
	id := newProjectStoreID("job_")
	now := projectNow()
	if _, err := s.db.Exec(`
		INSERT INTO jobs (
			id, project_id, ticket_id, driver_id, mode, status,
			worktree_path, branch_name, prompt_summary, prompt_text,
			driver_snapshot_json, created_by, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, strings.TrimSpace(req.ProjectID), strings.TrimSpace(req.TicketID), driver.ID, mode, JobStatusPlanned,
		strings.TrimSpace(req.WorktreePath), strings.TrimSpace(req.BranchName),
		strings.TrimSpace(req.PromptSummary), strings.TrimSpace(req.PromptText),
		string(snapshot), strings.TrimSpace(req.CreatedBy), now, now); err != nil {
		return nil, err
	}
	if _, err := s.AddJobEvent(AddJobEventRequest{JobID: id, Type: "planned", ActorID: req.CreatedBy, Message: req.PromptSummary}); err != nil {
		return nil, err
	}
	return s.GetJob(id)
}

func (s *Store) GetJob(jobID string) (*Job, error) {
	return scanJob(s.db.QueryRow(`
		SELECT id, project_id, ticket_id, driver_id, mode, status,
		       worktree_path, branch_name, prompt_summary, prompt_text,
		       result_summary, log_tail, error_excerpt, log_truncated,
		       driver_snapshot_json, created_by, approved_by, started_at,
		       finished_at, created_at, updated_at
		FROM jobs
		WHERE id = ?`, strings.TrimSpace(jobID)))
}

func (s *Store) ApproveJob(jobID, actorID string) (*Job, error) {
	job, err := s.GetJob(jobID)
	if err != nil {
		return nil, err
	}
	if job.Status != JobStatusPlanned {
		return nil, fmt.Errorf("job %q is not planned", job.ID)
	}
	now := projectNow()
	if _, err := s.db.Exec(`
		UPDATE jobs SET status = ?, approved_by = ?, updated_at = ? WHERE id = ?`,
		JobStatusApproved, strings.TrimSpace(actorID), now, job.ID); err != nil {
		return nil, err
	}
	if _, err := s.AddJobEvent(AddJobEventRequest{JobID: job.ID, Type: "approved", ActorID: actorID, Message: "approved"}); err != nil {
		return nil, err
	}
	return s.GetJob(job.ID)
}

func (s *Store) CancelJob(jobID, actorID, reason string) (*Job, error) {
	job, err := s.GetJob(jobID)
	if err != nil {
		return nil, err
	}
	now := projectNow()
	if _, err := s.db.Exec(`
		UPDATE jobs SET status = ?, finished_at = ?, updated_at = ? WHERE id = ?`,
		JobStatusCanceled, now, now, job.ID); err != nil {
		return nil, err
	}
	if _, err := s.AddJobEvent(AddJobEventRequest{JobID: job.ID, Type: "canceled", ActorID: actorID, Message: reason}); err != nil {
		return nil, err
	}
	return s.GetJob(job.ID)
}

type AddJobEventRequest struct {
	JobID        string
	Type         string
	ActorID      string
	Message      string
	MetadataJSON string
}

func (s *Store) AddJobEvent(req AddJobEventRequest) (*JobEvent, error) {
	id := newProjectStoreID("jev_")
	metadataJSON := normalizeJSONDocument(req.MetadataJSON, "{}")
	now := projectNow()
	if _, err := s.db.Exec(`
		INSERT INTO job_events (id, job_id, type, actor_id, message, metadata_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, strings.TrimSpace(req.JobID), strings.TrimSpace(req.Type), strings.TrimSpace(req.ActorID),
		strings.TrimSpace(req.Message), metadataJSON, now); err != nil {
		return nil, err
	}
	return s.getJobEvent(id)
}

func (s *Store) ListJobEvents(jobID string) ([]JobEvent, error) {
	rows, err := s.db.Query(`
		SELECT id, job_id, type, actor_id, message, metadata_json, created_at
		FROM job_events
		WHERE job_id = ?
		ORDER BY created_at, rowid`, strings.TrimSpace(jobID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []JobEvent
	for rows.Next() {
		event, err := scanJobEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *event)
	}
	return out, rows.Err()
}

func (s *Store) getJobEvent(id string) (*JobEvent, error) {
	return scanJobEvent(s.db.QueryRow(`
		SELECT id, job_id, type, actor_id, message, metadata_json, created_at
		FROM job_events
		WHERE id = ?`, strings.TrimSpace(id)))
}

func scanJob(row interface {
	Scan(dest ...any) error
}) (*Job, error) {
	var job Job
	var logTruncated int
	if err := row.Scan(
		&job.ID, &job.ProjectID, &job.TicketID, &job.DriverID, &job.Mode, &job.Status,
		&job.WorktreePath, &job.BranchName, &job.PromptSummary, &job.PromptText,
		&job.ResultSummary, &job.LogTail, &job.ErrorExcerpt, &logTruncated,
		&job.DriverSnapshotJSON, &job.CreatedBy, &job.ApprovedBy, &job.StartedAt,
		&job.FinishedAt, &job.CreatedAt, &job.UpdatedAt,
	); err != nil {
		return nil, err
	}
	job.LogTruncated = logTruncated != 0
	return &job, nil
}

func scanJobEvent(row interface {
	Scan(dest ...any) error
}) (*JobEvent, error) {
	var event JobEvent
	if err := row.Scan(&event.ID, &event.JobID, &event.Type, &event.ActorID, &event.Message, &event.MetadataJSON, &event.CreatedAt); err != nil {
		return nil, err
	}
	return &event, nil
}

func driverSupportsMode(driver *DriverDefinition, mode string) bool {
	var modes []string
	if err := json.Unmarshal([]byte(driver.SupportedModesJSON), &modes); err != nil {
		return false
	}
	for _, supported := range modes {
		if strings.TrimSpace(supported) == mode {
			return true
		}
	}
	return false
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
