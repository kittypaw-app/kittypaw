package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const (
	ScheduledRunStatusQueued    = "queued"
	ScheduledRunStatusRunning   = "running"
	ScheduledRunStatusSucceeded = "succeeded"
	ScheduledRunStatusFailed    = "failed"
)

type ScheduledRun struct {
	ID             int64
	JobKey         string
	JobType        string
	JobID          string
	TriggerType    string
	DueAt          time.Time
	Status         string
	Attempt        int
	ClaimToken     string
	ClaimedAt      *time.Time
	StartedAt      *time.Time
	LeaseExpiresAt *time.Time
	FinishedAt     *time.Time
	ErrorClass     string
	ErrorMessage   string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type ClaimScheduledRunRequest struct {
	JobKey        string
	JobType       string
	JobID         string
	TriggerType   string
	DueAt         time.Time
	Now           time.Time
	LeaseDuration time.Duration
}

type scheduledRunScanner interface {
	Scan(dest ...any) error
}

func (s *Store) ClaimScheduledRun(req ClaimScheduledRunRequest) (*ScheduledRun, bool, error) {
	req.JobKey = strings.TrimSpace(req.JobKey)
	req.JobType = strings.TrimSpace(req.JobType)
	req.JobID = strings.TrimSpace(req.JobID)
	req.TriggerType = strings.TrimSpace(req.TriggerType)
	if req.JobKey == "" {
		return nil, false, fmt.Errorf("scheduled run job_key is required")
	}
	if req.JobType == "" {
		return nil, false, fmt.Errorf("scheduled run job_type is required")
	}
	if req.JobID == "" {
		req.JobID = req.JobKey
	}
	if req.TriggerType == "" {
		req.TriggerType = req.JobType
	}
	if req.DueAt.IsZero() {
		return nil, false, fmt.Errorf("scheduled run due_at is required")
	}
	now := req.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	leaseDuration := req.LeaseDuration
	if leaseDuration <= 0 {
		leaseDuration = 10 * time.Minute
	}
	dueAt := req.DueAt.UTC()
	leaseExpiresAt := now.Add(leaseDuration).UTC()
	claimToken := newProjectStoreID("claim_")

	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()

	active, ok, err := getActiveScheduledRunForUpdate(tx, req.JobKey)
	if err != nil {
		return nil, false, err
	}
	if ok {
		if active.Status == ScheduledRunStatusRunning && active.LeaseExpiresAt != nil && active.LeaseExpiresAt.After(now) {
			if err := tx.Commit(); err != nil {
				return nil, false, err
			}
			return nil, false, nil
		}
		run, err := claimExistingScheduledRun(tx, active.ID, claimToken, now, leaseExpiresAt)
		if err != nil {
			return nil, false, err
		}
		if err := tx.Commit(); err != nil {
			return nil, false, err
		}
		return run, true, nil
	}

	nowText := formatScheduledRunTime(now)
	res, err := tx.Exec(`
		INSERT OR IGNORE INTO scheduled_runs (
			job_key, job_type, job_id, trigger_type, due_at,
			status, attempt, claim_token, claimed_at, started_at, lease_expires_at,
			created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?)`,
		req.JobKey, req.JobType, req.JobID, req.TriggerType, formatScheduledRunTime(dueAt),
		ScheduledRunStatusRunning, claimToken, nowText, nowText, formatScheduledRunTime(leaseExpiresAt),
		nowText, nowText)
	if err != nil {
		return nil, false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	if affected == 0 {
		if err := tx.Commit(); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, false, err
	}
	run, err := getScheduledRunTx(tx, id)
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return run, true, nil
}

func (s *Store) ReleaseScheduledRunClaim(id int64, claimToken string, now time.Time, reason string) (bool, error) {
	now = now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	res, err := s.db.Exec(`
		UPDATE scheduled_runs
		SET status = ?, claim_token = NULL, claimed_at = NULL, started_at = NULL,
			lease_expires_at = NULL, error_class = ?, error_message = ?, updated_at = ?
		WHERE id = ? AND claim_token = ? AND status = ?`,
		ScheduledRunStatusQueued, "released", trimScheduledRunError(reason),
		formatScheduledRunTime(now), id, claimToken, ScheduledRunStatusRunning)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store) FinishScheduledRun(id int64, claimToken, status, errorClass, errorMessage string, now time.Time) (bool, error) {
	status = strings.TrimSpace(status)
	if status != ScheduledRunStatusSucceeded && status != ScheduledRunStatusFailed {
		return false, fmt.Errorf("invalid scheduled run terminal status: %s", status)
	}
	now = now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	res, err := s.db.Exec(`
		UPDATE scheduled_runs
		SET status = ?, finished_at = ?, lease_expires_at = NULL,
			error_class = ?, error_message = ?, updated_at = ?
		WHERE id = ? AND claim_token = ? AND status = ?`,
		status, formatScheduledRunTime(now), strings.TrimSpace(errorClass),
		trimScheduledRunError(errorMessage), formatScheduledRunTime(now),
		id, claimToken, ScheduledRunStatusRunning)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store) GetScheduledRun(id int64) (*ScheduledRun, bool, error) {
	run, err := getScheduledRunTx(s.db, id)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return run, true, nil
}

func getActiveScheduledRunForUpdate(tx *sql.Tx, jobKey string) (*ScheduledRun, bool, error) {
	run, err := scanScheduledRun(tx.QueryRow(scheduledRunSelectSQL+`
		WHERE job_key = ? AND status IN (?, ?)
		ORDER BY due_at ASC, id ASC
		LIMIT 1`,
		jobKey, ScheduledRunStatusQueued, ScheduledRunStatusRunning))
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return run, true, nil
}

func claimExistingScheduledRun(tx *sql.Tx, id int64, claimToken string, now, leaseExpiresAt time.Time) (*ScheduledRun, error) {
	nowText := formatScheduledRunTime(now)
	if _, err := tx.Exec(`
		UPDATE scheduled_runs
		SET status = ?, attempt = attempt + 1, claim_token = ?,
			claimed_at = ?, started_at = ?, lease_expires_at = ?,
			updated_at = ?
		WHERE id = ?`,
		ScheduledRunStatusRunning, claimToken, nowText, nowText,
		formatScheduledRunTime(leaseExpiresAt), nowText, id); err != nil {
		return nil, err
	}
	return getScheduledRunTx(tx, id)
}

func getScheduledRunTx(q interface {
	QueryRow(query string, args ...any) *sql.Row
}, id int64) (*ScheduledRun, error) {
	return scanScheduledRun(q.QueryRow(scheduledRunSelectSQL+" WHERE id = ?", id))
}

const scheduledRunSelectSQL = `
	SELECT id, job_key, job_type, job_id, trigger_type, due_at, status, attempt,
		claim_token, claimed_at, started_at, lease_expires_at, finished_at,
		error_class, error_message, created_at, updated_at
	FROM scheduled_runs`

func scanScheduledRun(scanner scheduledRunScanner) (*ScheduledRun, error) {
	var run ScheduledRun
	var dueAt, createdAt, updatedAt string
	var claimToken, claimedAt, startedAt, leaseExpiresAt, finishedAt sql.NullString
	var errorClass, errorMessage sql.NullString
	if err := scanner.Scan(
		&run.ID, &run.JobKey, &run.JobType, &run.JobID, &run.TriggerType,
		&dueAt, &run.Status, &run.Attempt, &claimToken, &claimedAt, &startedAt,
		&leaseExpiresAt, &finishedAt, &errorClass, &errorMessage, &createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}
	parsedDueAt, err := parseScheduledRunTime(dueAt)
	if err != nil {
		return nil, fmt.Errorf("parse scheduled run due_at %q: %w", dueAt, err)
	}
	parsedCreatedAt, err := parseScheduledRunTime(createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse scheduled run created_at %q: %w", createdAt, err)
	}
	parsedUpdatedAt, err := parseScheduledRunTime(updatedAt)
	if err != nil {
		return nil, fmt.Errorf("parse scheduled run updated_at %q: %w", updatedAt, err)
	}
	run.DueAt = parsedDueAt
	run.CreatedAt = parsedCreatedAt
	run.UpdatedAt = parsedUpdatedAt
	run.ClaimToken = claimToken.String
	run.ClaimedAt, err = parseScheduledRunOptionalTime(claimedAt)
	if err != nil {
		return nil, err
	}
	run.StartedAt, err = parseScheduledRunOptionalTime(startedAt)
	if err != nil {
		return nil, err
	}
	run.LeaseExpiresAt, err = parseScheduledRunOptionalTime(leaseExpiresAt)
	if err != nil {
		return nil, err
	}
	run.FinishedAt, err = parseScheduledRunOptionalTime(finishedAt)
	if err != nil {
		return nil, err
	}
	run.ErrorClass = errorClass.String
	run.ErrorMessage = errorMessage.String
	return &run, nil
}

func parseScheduledRunOptionalTime(raw sql.NullString) (*time.Time, error) {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil, nil
	}
	t, err := parseScheduledRunTime(raw.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func parseScheduledRunTime(raw string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

func formatScheduledRunTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func trimScheduledRunError(raw string) string {
	raw = strings.TrimSpace(raw)
	const max = 1000
	if len(raw) <= max {
		return raw
	}
	return raw[:max] + "...(truncated)"
}
