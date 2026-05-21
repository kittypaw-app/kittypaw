package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const (
	DelegationJobStatusQueued    = "queued"
	DelegationJobStatusRunning   = "running"
	DelegationJobStatusSucceeded = "succeeded"
	DelegationJobStatusFailed    = "failed"
	DelegationJobStatusCanceled  = "canceled"
)

type DelegationJob struct {
	ID                     string     `json:"id"`
	AccountID              string     `json:"account_id"`
	StaffID                string     `json:"staff_id"`
	Task                   string     `json:"task"`
	ParentConversationID   string     `json:"parent_conversation_id"`
	DelegateConversationID string     `json:"delegate_conversation_id"`
	ParentStaffID          string     `json:"parent_staff_id,omitempty"`
	ParentEventJSON        string     `json:"-"`
	Status                 string     `json:"status"`
	Attempt                int        `json:"attempt"`
	ClaimToken             string     `json:"-"`
	ClaimedAt              *time.Time `json:"claimed_at,omitempty"`
	StartedAt              *time.Time `json:"started_at,omitempty"`
	LeaseExpiresAt         *time.Time `json:"lease_expires_at,omitempty"`
	FinishedAt             *time.Time `json:"finished_at,omitempty"`
	Result                 string     `json:"result,omitempty"`
	ErrorClass             string     `json:"error_class,omitempty"`
	ErrorMessage           string     `json:"error_message,omitempty"`
	TokenUsage             int64      `json:"token_usage"`
	DurationMS             int64      `json:"duration_ms"`
	Depth                  int        `json:"depth"`
	MaxDepth               int        `json:"max_depth"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

type CreateDelegationJobRequest struct {
	AccountID              string
	StaffID                string
	Task                   string
	ParentConversationID   string
	DelegateConversationID string
	ParentStaffID          string
	ParentEventJSON        string
	Depth                  int
	MaxDepth               int
	Now                    time.Time
}

type ClaimDelegationJobsRequest struct {
	AccountID     string
	Limit         int
	Now           time.Time
	LeaseDuration time.Duration
}

type FinishDelegationJobRequest struct {
	ID                     string
	ClaimToken             string
	Status                 string
	Result                 string
	ErrorClass             string
	ErrorMessage           string
	TokenUsage             int64
	DurationMS             int64
	DelegateConversationID string
	Now                    time.Time
}

type DelegationJobListFilter struct {
	AccountID            string
	Status               string
	ParentConversationID string
	Limit                int
}

type delegationJobScanner interface {
	Scan(dest ...any) error
}

func (s *Store) CreateDelegationJob(req CreateDelegationJobRequest) (*DelegationJob, error) {
	req.AccountID = strings.TrimSpace(req.AccountID)
	req.StaffID = strings.TrimSpace(req.StaffID)
	req.Task = strings.TrimSpace(req.Task)
	req.ParentConversationID = strings.TrimSpace(req.ParentConversationID)
	req.DelegateConversationID = strings.TrimSpace(req.DelegateConversationID)
	req.ParentStaffID = strings.TrimSpace(req.ParentStaffID)
	req.ParentEventJSON = strings.TrimSpace(req.ParentEventJSON)
	if req.AccountID == "" {
		return nil, fmt.Errorf("delegation job account_id is required")
	}
	if req.StaffID == "" {
		return nil, fmt.Errorf("delegation job staff_id is required")
	}
	if req.Task == "" {
		return nil, fmt.Errorf("delegation job task is required")
	}
	if req.Depth <= 0 {
		req.Depth = 1
	}
	if req.MaxDepth <= 0 {
		req.MaxDepth = 3
	}
	now := req.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	id := newProjectStoreID("djob_")
	nowText := formatDelegationJobTime(now)
	_, err := s.db.Exec(`
		INSERT INTO delegation_jobs (
			id, account_id, staff_id, task, parent_conversation_id,
			delegate_conversation_id, parent_staff_id, parent_event_json,
			status, depth, max_depth, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, req.AccountID, req.StaffID, req.Task, req.ParentConversationID,
		req.DelegateConversationID, req.ParentStaffID, req.ParentEventJSON,
		DelegationJobStatusQueued, req.Depth, req.MaxDepth, nowText, nowText)
	if err != nil {
		return nil, err
	}
	job, _, err := s.GetDelegationJob(id)
	return job, err
}

func (s *Store) ClaimDelegationJobs(req ClaimDelegationJobsRequest) ([]DelegationJob, error) {
	req.AccountID = strings.TrimSpace(req.AccountID)
	if req.AccountID == "" {
		return nil, fmt.Errorf("delegation job account_id is required")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 1
	}
	if limit > 20 {
		limit = 20
	}
	now := req.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	leaseDuration := req.LeaseDuration
	if leaseDuration <= 0 {
		leaseDuration = 10 * time.Minute
	}
	nowText := formatDelegationJobTime(now)
	leaseExpiresAt := formatDelegationJobTime(now.Add(leaseDuration).UTC())

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.Query(`
		SELECT id
		FROM delegation_jobs
		WHERE account_id = ?
			AND (
				status = ?
				OR (status = ? AND COALESCE(lease_expires_at, '') <= ?)
			)
		ORDER BY created_at ASC, id ASC
		LIMIT ?`,
		req.AccountID, DelegationJobStatusQueued, DelegationJobStatusRunning, nowText, limit)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	claimed := make([]DelegationJob, 0, len(ids))
	for _, id := range ids {
		claimToken := newProjectStoreID("claim_")
		res, err := tx.Exec(`
			UPDATE delegation_jobs
			SET status = ?, attempt = attempt + 1, claim_token = ?,
				claimed_at = ?, started_at = COALESCE(started_at, ?),
				lease_expires_at = ?, updated_at = ?
			WHERE id = ?
				AND account_id = ?
				AND (
					status = ?
					OR (status = ? AND COALESCE(lease_expires_at, '') <= ?)
				)`,
			DelegationJobStatusRunning, claimToken, nowText, nowText,
			leaseExpiresAt, nowText, id, req.AccountID,
			DelegationJobStatusQueued, DelegationJobStatusRunning, nowText)
		if err != nil {
			return nil, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return nil, err
		}
		if n == 0 {
			continue
		}
		job, err := getDelegationJobTx(tx, id)
		if err != nil {
			return nil, err
		}
		claimed = append(claimed, *job)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return claimed, nil
}

func (s *Store) FinishDelegationJob(req FinishDelegationJobRequest) (bool, error) {
	req.ID = strings.TrimSpace(req.ID)
	req.ClaimToken = strings.TrimSpace(req.ClaimToken)
	req.Status = strings.TrimSpace(req.Status)
	req.ErrorClass = strings.TrimSpace(req.ErrorClass)
	req.ErrorMessage = trimScheduledRunError(req.ErrorMessage)
	req.DelegateConversationID = strings.TrimSpace(req.DelegateConversationID)
	if req.ID == "" {
		return false, fmt.Errorf("delegation job id is required")
	}
	if req.ClaimToken == "" {
		return false, fmt.Errorf("delegation job claim_token is required")
	}
	if !isTerminalDelegationJobStatus(req.Status) {
		return false, fmt.Errorf("invalid delegation job terminal status: %s", req.Status)
	}
	now := req.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	nowText := formatDelegationJobTime(now)
	res, err := s.db.Exec(`
		UPDATE delegation_jobs
		SET status = ?, finished_at = ?, lease_expires_at = NULL,
			result = ?, error_class = ?, error_message = ?,
			token_usage = ?, duration_ms = ?,
			delegate_conversation_id = CASE WHEN ? = '' THEN delegate_conversation_id ELSE ? END,
			updated_at = ?
		WHERE id = ? AND claim_token = ? AND status = ?`,
		req.Status, nowText, req.Result, req.ErrorClass, req.ErrorMessage,
		req.TokenUsage, req.DurationMS, req.DelegateConversationID, req.DelegateConversationID,
		nowText, req.ID, req.ClaimToken, DelegationJobStatusRunning)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store) ReleaseDelegationJobClaim(id, claimToken string, now time.Time, reason string) (bool, error) {
	id = strings.TrimSpace(id)
	claimToken = strings.TrimSpace(claimToken)
	if id == "" {
		return false, fmt.Errorf("delegation job id is required")
	}
	if claimToken == "" {
		return false, fmt.Errorf("delegation job claim_token is required")
	}
	now = now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	res, err := s.db.Exec(`
		UPDATE delegation_jobs
		SET status = ?, claim_token = '', claimed_at = NULL, lease_expires_at = NULL,
			error_class = ?, error_message = ?, updated_at = ?
		WHERE id = ? AND claim_token = ? AND status = ?`,
		DelegationJobStatusQueued, "released", trimScheduledRunError(reason),
		formatDelegationJobTime(now), id, claimToken, DelegationJobStatusRunning)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store) ExtendDelegationJobLease(id, claimToken string, now time.Time, leaseDuration time.Duration) (bool, error) {
	id = strings.TrimSpace(id)
	claimToken = strings.TrimSpace(claimToken)
	if id == "" {
		return false, fmt.Errorf("delegation job id is required")
	}
	if claimToken == "" {
		return false, fmt.Errorf("delegation job claim_token is required")
	}
	now = now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if leaseDuration <= 0 {
		leaseDuration = 10 * time.Minute
	}
	res, err := s.db.Exec(`
		UPDATE delegation_jobs
		SET lease_expires_at = ?, updated_at = ?
		WHERE id = ? AND claim_token = ? AND status = ?`,
		formatDelegationJobTime(now.Add(leaseDuration).UTC()), formatDelegationJobTime(now),
		id, claimToken, DelegationJobStatusRunning)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store) CancelDelegationJob(id, actorID, reason string) (*DelegationJob, error) {
	return s.CancelDelegationJobForAccount("", id, actorID, reason)
}

func (s *Store) CancelDelegationJobForAccount(accountID, id, actorID, reason string) (*DelegationJob, error) {
	id = strings.TrimSpace(id)
	accountID = strings.TrimSpace(accountID)
	if id == "" {
		return nil, fmt.Errorf("delegation job id is required")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "canceled"
	}
	if actorID = strings.TrimSpace(actorID); actorID != "" {
		reason = actorID + ": " + reason
	}
	nowText := formatDelegationJobTime(time.Now().UTC())
	query := `
		UPDATE delegation_jobs
		SET status = ?, finished_at = COALESCE(finished_at, ?),
			lease_expires_at = NULL, error_class = ?, error_message = ?, updated_at = ?
		WHERE id = ? AND status IN (?, ?)`
	args := []any{
		DelegationJobStatusCanceled, nowText, "canceled", trimScheduledRunError(reason),
		nowText, id, DelegationJobStatusQueued, DelegationJobStatusRunning,
	}
	if accountID != "" {
		query += ` AND account_id = ?`
		args = append(args, accountID)
	}
	res, err := s.db.Exec(query, args...)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		job, ok, err := s.GetDelegationJob(id)
		if err != nil {
			return nil, err
		}
		if !ok || (accountID != "" && job.AccountID != accountID) {
			return nil, sql.ErrNoRows
		}
		return job, nil
	}
	job, _, err := s.GetDelegationJob(id)
	return job, err
}

func (s *Store) GetDelegationJob(id string) (*DelegationJob, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, false, nil
	}
	job, err := getDelegationJobTx(s.db, id)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return job, true, nil
}

func (s *Store) ListDelegationJobs(filter DelegationJobListFilter) ([]DelegationJob, error) {
	filter.AccountID = strings.TrimSpace(filter.AccountID)
	if filter.AccountID == "" {
		return nil, fmt.Errorf("delegation job account_id is required")
	}
	filter.Status = strings.TrimSpace(filter.Status)
	filter.ParentConversationID = strings.TrimSpace(filter.ParentConversationID)
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	query := delegationJobSelectSQL + ` WHERE account_id = ?`
	args := []any{filter.AccountID}
	if filter.Status != "" {
		query += ` AND status = ?`
		args = append(args, filter.Status)
	}
	if filter.ParentConversationID != "" {
		query += ` AND parent_conversation_id = ?`
		args = append(args, filter.ParentConversationID)
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DelegationJob{}
	for rows.Next() {
		job, err := scanDelegationJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *job)
	}
	return out, rows.Err()
}

func getDelegationJobTx(q interface {
	QueryRow(query string, args ...any) *sql.Row
}, id string) (*DelegationJob, error) {
	return scanDelegationJob(q.QueryRow(delegationJobSelectSQL+" WHERE id = ?", id))
}

const delegationJobSelectSQL = `
	SELECT id, account_id, staff_id, task, parent_conversation_id,
		delegate_conversation_id, parent_staff_id, parent_event_json,
		status, attempt, claim_token, claimed_at, started_at,
		lease_expires_at, finished_at, result, error_class, error_message,
		token_usage, duration_ms, depth, max_depth, created_at, updated_at
	FROM delegation_jobs`

func scanDelegationJob(scanner delegationJobScanner) (*DelegationJob, error) {
	var job DelegationJob
	var claimToken, claimedAt, startedAt, leaseExpiresAt, finishedAt sql.NullString
	var createdAt, updatedAt string
	if err := scanner.Scan(
		&job.ID, &job.AccountID, &job.StaffID, &job.Task, &job.ParentConversationID,
		&job.DelegateConversationID, &job.ParentStaffID, &job.ParentEventJSON,
		&job.Status, &job.Attempt, &claimToken, &claimedAt, &startedAt,
		&leaseExpiresAt, &finishedAt, &job.Result, &job.ErrorClass, &job.ErrorMessage,
		&job.TokenUsage, &job.DurationMS, &job.Depth, &job.MaxDepth, &createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}
	var err error
	job.CreatedAt, err = parseDelegationJobTime(createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse delegation job created_at %q: %w", createdAt, err)
	}
	job.UpdatedAt, err = parseDelegationJobTime(updatedAt)
	if err != nil {
		return nil, fmt.Errorf("parse delegation job updated_at %q: %w", updatedAt, err)
	}
	job.ClaimToken = claimToken.String
	job.ClaimedAt, err = parseDelegationJobOptionalTime(claimedAt)
	if err != nil {
		return nil, err
	}
	job.StartedAt, err = parseDelegationJobOptionalTime(startedAt)
	if err != nil {
		return nil, err
	}
	job.LeaseExpiresAt, err = parseDelegationJobOptionalTime(leaseExpiresAt)
	if err != nil {
		return nil, err
	}
	job.FinishedAt, err = parseDelegationJobOptionalTime(finishedAt)
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func isTerminalDelegationJobStatus(status string) bool {
	return status == DelegationJobStatusSucceeded ||
		status == DelegationJobStatusFailed ||
		status == DelegationJobStatusCanceled
}

func parseDelegationJobOptionalTime(raw sql.NullString) (*time.Time, error) {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil, nil
	}
	t, err := parseDelegationJobTime(raw.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func parseDelegationJobTime(raw string) (time.Time, error) {
	return parseScheduledRunTime(raw)
}

func formatDelegationJobTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000000000Z07:00")
}
