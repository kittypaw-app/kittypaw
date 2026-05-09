package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/jinto/kittypaw/core"
)

// WorkspaceFile is a file entry in the workspace index.
type WorkspaceFile struct {
	ID          int64
	WorkspaceID string
	AbsPath     string
	RelPath     string
	Filename    string
	Extension   string
	Size        int64
	ModifiedAt  string
	HasContent  bool
}

// WorkspaceFTSRow is a search result from the workspace FTS5 index.
type WorkspaceFTSRow struct {
	FileID    int64
	AbsPath   string
	RelPath   string
	Filename  string
	Extension string
	Size      int64
	Score     float64
	Snippet   string
}

// ---------------------------------------------------------------------------
// DTO structs
// ---------------------------------------------------------------------------

// ConversationSummary describes the account-wide conversation timeline.
type ConversationSummary struct {
	TurnCount int    `json:"turn_count"`
	FirstAt   string `json:"first_at"`
	LastAt    string `json:"last_at"`
}

// ConversationTurnRecord is a persisted conversation turn with its row ID.
type ConversationTurnRecord struct {
	ID            int64     `json:"id"`
	Role          core.Role `json:"role"`
	Content       string    `json:"content"`
	Code          string    `json:"code,omitempty"`
	Result        string    `json:"result,omitempty"`
	Channel       string    `json:"channel,omitempty"`
	ChannelUserID string    `json:"channel_user_id,omitempty"`
	ChatID        string    `json:"chat_id,omitempty"`
	MessageID     string    `json:"message_id,omitempty"`
	Timestamp     string    `json:"timestamp"`
}

type conversationCompaction struct {
	StartTurnID int64
	EndTurnID   int64
	Summary     string
	CreatedAt   string
}

// ExecutionRecord captures one skill execution for history/analysis.
type ExecutionRecord struct {
	ID            int64
	SkillID       string
	SkillName     string
	StartedAt     string
	FinishedAt    string
	DurationMs    int64
	InputParams   string
	ResultSummary string
	Success       bool
	RetryCount    int
	UsageJSON     string
}

// ExecutionStats is an aggregated daily summary.
type ExecutionStats struct {
	TotalRuns        int
	Successful       int
	Failed           int
	AutoRetries      int
	TotalTokens      int64
	EstimatedCostUSD float64
}

// LLMCallUsageRecord captures token and estimated-cost data for one LLM API call.
type LLMCallUsageRecord struct {
	ID                       int64
	CallKind                 string
	Provider                 string
	Model                    string
	StartedAt                string
	FinishedAt               string
	DurationMs               int64
	InputTokens              int64
	OutputTokens             int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
	EstimatedCost            float64
	PricingSource            string
	PricingMatched           bool
	UsageJSON                string
}

// LLMUsageByModel is a daily aggregate grouped by provider/model.
type LLMUsageByModel struct {
	Provider                 string  `json:"provider"`
	Model                    string  `json:"model"`
	Calls                    int     `json:"calls"`
	InputTokens              int64   `json:"input_tokens"`
	OutputTokens             int64   `json:"output_tokens"`
	CacheCreationInputTokens int64   `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64   `json:"cache_read_input_tokens"`
	TotalTokens              int64   `json:"total_tokens"`
	EstimatedCostUSD         float64 `json:"estimated_cost_usd"`
}

// KeyValue is a generic key-value pair used for user context listings.
type KeyValue struct {
	Key   string
	Value string
}

// Checkpoint is a named snapshot of conversation progress.
type Checkpoint struct {
	ID        int64  `json:"id"`
	Label     string `json:"label"`
	TurnID    int64  `json:"turn_id"`
	CreatedAt string `json:"created_at"`
}

// FilePermissionRule controls file access for a workspace.
type FilePermissionRule struct {
	ID          string
	WorkspaceID string
	PathPattern string
	IsException bool
	CanRead     bool
	CanWrite    bool
	CanDelete   bool
	CreatedAt   string
}

// NetworkPermissionRule controls network access for a workspace.
type NetworkPermissionRule struct {
	ID             string
	WorkspaceID    string
	DomainPattern  string
	AllowedMethods string
	CreatedAt      string
}

// GlobalPath is a globally permitted filesystem path.
type GlobalPath struct {
	ID         string
	Path       string
	AccessType string
	CreatedAt  string
}

// StaffMeta stores metadata about a switchable staff identity.
type StaffMeta struct {
	ID             string
	DisplayName    string
	Description    string
	EquippedSkills string
	Active         bool
	CreatedBy      string
	CreatedAt      string
}

// AuditRecord is a single entry in the audit log.
type AuditRecord struct {
	ID        int64
	EventType string
	Detail    string
	Severity  string
	CreatedAt string
}

// PendingResponse is a response that failed delivery and is queued for retry.
type PendingResponse struct {
	ID         int64
	AccountID  string
	EventType  string
	ChatID     string
	Response   string
	RetryCount int
	CreatedAt  string
	NextRetry  string
}

// ---------------------------------------------------------------------------
// Store
// ---------------------------------------------------------------------------

// Store wraps a SQLite database providing all persistence for kittypaw.
type Store struct {
	db *sql.DB
}

// Open creates or opens a SQLite database at path, enables WAL mode and
// foreign keys, then runs all pending migrations in order.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store open: %w", err)
	}

	// In-memory SQLite gives every new connection a fresh, empty
	// database — the sql.DB pool's concurrent connections would each
	// see a different un-migrated DB. Pinning to one connection serializes
	// goroutines through the single migrated instance. Production always
	// opens a file path, so this branch only affects tests.
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	}

	// Pragmas: WAL for concurrency, foreign keys for integrity.
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("store pragma %q: %w", pragma, err)
		}
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store migrate: %w", err)
	}
	return s, nil
}

// Close releases the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// ---------------------------------------------------------------------------
// Migrations
// ---------------------------------------------------------------------------

func (s *Store) migrate() error {
	// Ensure the migrations meta table exists.
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS _migrations (
		filename TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return err
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}

	// Sort by filename to guarantee order.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}

		// Skip already-applied migrations.
		var count int
		if err := s.db.QueryRow(
			"SELECT COUNT(*) FROM _migrations WHERE filename = ?", name,
		).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			continue
		}

		data, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		// Apply migration body and the _migrations bookkeeping insert in a
		// single transaction so a multi-statement migration cannot leave
		// the database half-applied. Without this, a failure in statement
		// N of a migration would persist statements 1..N-1 while the
		// migration stays unrecorded, making subsequent boots replay the
		// already-applied prefix and fail again at statement N.
		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", name, err)
		}
		if _, err := tx.Exec(string(data)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.Exec(
			"INSERT INTO _migrations (filename) VALUES (?)", name,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Conversation State
// ---------------------------------------------------------------------------

type sqlExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// SaveConversationState upserts account-level runtime metadata. When the conversation is
// empty, provided turns seed the account-wide timeline; existing turns are not
// replaced, because AddConversationTurn owns durable history writes.
func (s *Store) SaveConversationState(state *core.ConversationState) error {
	if state == nil {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stateJSON, err := json.Marshal(state)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
		INSERT INTO conversation_state (id, system_prompt, state_json, updated_at)
		VALUES (1, ?, ?, datetime('now'))
		ON CONFLICT(id) DO UPDATE SET
			system_prompt          = excluded.system_prompt,
			state_json             = excluded.state_json,
			updated_at             = datetime('now')`,
		state.SystemPrompt, string(stateJSON))
	if err != nil {
		return err
	}

	var existing int
	if err := tx.QueryRow("SELECT COUNT(*) FROM v2_conversation_turns").Scan(&existing); err != nil {
		return err
	}
	if existing == 0 {
		for i := range state.Turns {
			if err := insertConversationTurn(tx, &state.Turns[i]); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// LoadConversationState retrieves account metadata and the most recent
// conversation turns.
func (s *Store) LoadConversationState() (*core.ConversationState, error) {
	var sysPrompt, conversationStaffID string
	stateExists := true
	err := s.db.QueryRow(
		"SELECT system_prompt, conversation_staff_id FROM conversation_state WHERE id = 1",
	).Scan(&sysPrompt, &conversationStaffID)
	if err == sql.ErrNoRows {
		stateExists = false
	} else if err != nil {
		return nil, err
	}

	turns, err := s.loadConversationStateTurns(core.MaxHistoryTurns)
	if err != nil {
		return nil, err
	}
	if !stateExists && len(turns) == 0 {
		return nil, nil
	}
	return &core.ConversationState{
		ConversationID:      "account",
		SystemPrompt:        sysPrompt,
		ConversationStaffID: conversationStaffID,
		Turns:               turns,
	}, nil
}

// ConversationStaff returns the account conversation's sticky staff override.
func (s *Store) ConversationStaff() (string, bool, error) {
	var staffID string
	err := s.db.QueryRow("SELECT conversation_staff_id FROM conversation_state WHERE id = 1").Scan(&staffID)
	if err == sql.ErrNoRows || staffID == "" {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return staffID, true, nil
}

// SetConversationStaff stores the account conversation's sticky staff override.
func (s *Store) SetConversationStaff(staffID string) error {
	if staffID != "" {
		if err := core.ValidateStaffID(staffID); err != nil {
			return err
		}
	}
	_, err := s.db.Exec(`
		INSERT INTO conversation_state (id, conversation_staff_id, updated_at)
		VALUES (1, ?, datetime('now'))
		ON CONFLICT(id) DO UPDATE SET
			conversation_staff_id = excluded.conversation_staff_id,
			updated_at = datetime('now')`,
		staffID)
	return err
}

// ClearConversationStaff clears the account conversation's sticky staff override.
func (s *Store) ClearConversationStaff() error {
	return s.SetConversationStaff("")
}

// AddConversationTurn appends one turn to the account-wide conversation.
func (s *Store) AddConversationTurn(turn *core.ConversationTurn) error {
	if turn == nil {
		return nil
	}
	if _, err := s.db.Exec(`
		INSERT INTO conversation_state (id, updated_at)
		VALUES (1, datetime('now'))
		ON CONFLICT(id) DO UPDATE SET updated_at = datetime('now')`); err != nil {
		return err
	}
	return insertConversationTurn(s.db, turn)
}

// ForgetConversation clears all account conversation turns and checkpoints.
func (s *Store) ForgetConversation() (int64, error) {
	res, err := s.db.Exec("DELETE FROM v2_conversation_turns")
	if err != nil {
		return 0, err
	}
	_, _ = s.db.Exec("DELETE FROM conversation_checkpoints")
	_, _ = s.db.Exec("DELETE FROM conversation_compactions")
	return res.RowsAffected()
}

// ConversationSummary returns aggregate information for the account timeline.
func (s *Store) ConversationSummary() (ConversationSummary, error) {
	var out ConversationSummary
	err := s.db.QueryRow(`
		SELECT COUNT(*), COALESCE(MIN(timestamp), ''), COALESCE(MAX(timestamp), '')
		FROM v2_conversation_turns`).Scan(&out.TurnCount, &out.FirstAt, &out.LastAt)
	return out, err
}

// ListConversationTurns returns recent account-wide turns in chronological
// order. A non-positive limit uses a conservative default.
func (s *Store) ListConversationTurns(limit int) ([]ConversationTurnRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT id, role, content, code, result, channel, channel_user_id, chat_id, message_id, timestamp
		FROM v2_conversation_turns
		ORDER BY id DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ConversationTurnRecord
	for rows.Next() {
		rec, err := scanConversationTurnRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// ListConversationTurnsForChat returns recent turns for a specific chat_id in
// chronological order. It keeps scoped project/ticket chat follow-ups from
// being displaced by unrelated account-wide turns.
func (s *Store) ListConversationTurnsForChat(chatID string, limit int) ([]ConversationTurnRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT id, role, content, code, result, channel, channel_user_id, chat_id, message_id, timestamp
		FROM v2_conversation_turns
		WHERE chat_id = ?
		ORDER BY id DESC
		LIMIT ?`, strings.TrimSpace(chatID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ConversationTurnRecord
	for rows.Next() {
		rec, err := scanConversationTurnRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// CompactConversation records a summary of older turns for prompt context while
// preserving every raw turn in v2_conversation_turns.
func (s *Store) CompactConversation(keepRecent int) (int, error) {
	if keepRecent <= 0 {
		keepRecent = 40
	}
	records, err := s.listAllConversationTurns()
	if err != nil {
		return 0, err
	}
	if len(records) <= keepRecent {
		return 0, nil
	}

	old := records[:len(records)-keepRecent]
	endTurnID := old[len(old)-1].ID
	latest, ok, err := s.latestConversationCompaction()
	if err != nil {
		return 0, err
	}
	if ok && latest.EndTurnID >= endTurnID {
		return 0, nil
	}

	summary := summarizeCompactedTurns(old)
	if _, err := s.db.Exec(`
		INSERT INTO conversation_compactions (start_turn_id, end_turn_id, summary)
		VALUES (?, ?, ?)`,
		old[0].ID, endTurnID, summary,
	); err != nil {
		return 0, err
	}
	return len(old), nil
}

func insertConversationTurn(exec sqlExecer, turn *core.ConversationTurn) error {
	_, err := exec.Exec(`
		INSERT INTO v2_conversation_turns
			(role, content, code, result, channel, channel_user_id, chat_id, message_id, timestamp)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(turn.Role),
		turn.Content,
		nullString(turn.Code),
		nullString(turn.Result),
		turn.Channel,
		turn.ChannelUserID,
		turn.ChatID,
		turn.MessageID,
		turn.Timestamp,
	)
	return err
}

func (s *Store) loadConversationStateTurns(limit int) ([]core.ConversationTurn, error) {
	compaction, ok, err := s.latestConversationCompaction()
	if err != nil {
		return nil, err
	}
	if !ok {
		records, err := s.ListConversationTurns(limit)
		if err != nil {
			return nil, err
		}
		return conversationRecordsToTurns(records), nil
	}

	records, err := s.listConversationTurnsAfter(compaction.EndTurnID, limit)
	if err != nil {
		return nil, err
	}
	turns := []core.ConversationTurn{{
		Role:      core.RoleAssistant,
		Content:   compaction.Summary,
		Timestamp: compaction.CreatedAt,
	}}
	return append(turns, conversationRecordsToTurns(records)...), nil
}

func (s *Store) listConversationTurnsAfter(turnID int64, limit int) ([]ConversationTurnRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT id, role, content, code, result, channel, channel_user_id, chat_id, message_id, timestamp
		FROM v2_conversation_turns
		WHERE id > ?
		ORDER BY id DESC
		LIMIT ?`, turnID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []ConversationTurnRecord
	for rows.Next() {
		rec, err := scanConversationTurnRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
		records[i], records[j] = records[j], records[i]
	}
	return records, nil
}

func (s *Store) latestConversationCompaction() (conversationCompaction, bool, error) {
	var out conversationCompaction
	err := s.db.QueryRow(`
		SELECT start_turn_id, end_turn_id, summary, created_at
		FROM conversation_compactions
		ORDER BY id DESC
		LIMIT 1`).Scan(&out.StartTurnID, &out.EndTurnID, &out.Summary, &out.CreatedAt)
	if err == sql.ErrNoRows {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	return out, true, nil
}

func conversationRecordsToTurns(records []ConversationTurnRecord) []core.ConversationTurn {
	turns := make([]core.ConversationTurn, 0, len(records))
	for i := range records {
		turns = append(turns, records[i].Turn())
	}
	return turns
}

func (s *Store) listAllConversationTurns() ([]ConversationTurnRecord, error) {
	rows, err := s.db.Query(`
		SELECT id, role, content, code, result, channel, channel_user_id, chat_id, message_id, timestamp
		FROM v2_conversation_turns
		ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ConversationTurnRecord
	for rows.Next() {
		rec, err := scanConversationTurnRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

type conversationTurnScanner interface {
	Scan(dest ...any) error
}

func scanConversationTurnRecord(scanner conversationTurnScanner) (ConversationTurnRecord, error) {
	var rec ConversationTurnRecord
	var role string
	var code, result sql.NullString
	if err := scanner.Scan(
		&rec.ID,
		&role,
		&rec.Content,
		&code,
		&result,
		&rec.Channel,
		&rec.ChannelUserID,
		&rec.ChatID,
		&rec.MessageID,
		&rec.Timestamp,
	); err != nil {
		return rec, err
	}
	rec.Role = core.Role(role)
	rec.Code = code.String
	rec.Result = result.String
	return rec, nil
}

func (r ConversationTurnRecord) Turn() core.ConversationTurn {
	return core.ConversationTurn{
		Role:          r.Role,
		Content:       r.Content,
		Code:          r.Code,
		Result:        r.Result,
		Channel:       r.Channel,
		ChannelUserID: r.ChannelUserID,
		ChatID:        r.ChatID,
		MessageID:     r.MessageID,
		Timestamp:     r.Timestamp,
	}
}

func summarizeCompactedTurns(records []ConversationTurnRecord) string {
	var userCount, assistantCount, codeCount, successCount, failureCount int
	channels := map[string]bool{}
	for i := range records {
		switch records[i].Role {
		case core.RoleUser:
			userCount++
		case core.RoleAssistant:
			assistantCount++
			if records[i].Code != "" {
				codeCount++
			}
			result := strings.ToLower(records[i].Result)
			if strings.Contains(result, "success") || strings.Contains(result, "output:") {
				successCount++
			} else if strings.Contains(result, "error") || strings.Contains(result, "fail") {
				failureCount++
			}
		}
		if records[i].Channel != "" {
			channels[records[i].Channel] = true
		}
	}
	channelNames := make([]string, 0, len(channels))
	for ch := range channels {
		channelNames = append(channelNames, ch)
	}
	sort.Strings(channelNames)
	channelText := "unknown"
	if len(channelNames) > 0 {
		channelText = strings.Join(channelNames, ", ")
	}
	return fmt.Sprintf(
		"[이전 대화 요약] 오래된 대화 %d개를 압축했습니다. 사용자 메시지 %d개, 어시스턴트 메시지 %d개, 코드 실행 %d번, 성공 %d번, 실패 %d번, 채널: %s.",
		len(records), userCount, assistantCount, codeCount, successCount, failureCount, channelText,
	)
}

// CountUserMessagesTotal returns the total number of user-role messages in the
// account conversation.
func (s *Store) CountUserMessagesTotal() (int, error) {
	var n int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM v2_conversation_turns WHERE role = 'user'",
	).Scan(&n)
	return n, err
}

// RecentUserMessagesAll returns user messages from the last `hours` hours,
// truncated so the combined length does not exceed maxChars.
//
// v2_conversation_turns.timestamp is written by core.NowTimestamp() as a unix epoch
// string ("1777394416"), not a SQL datetime. Comparing it against
// datetime('now', ...) (which yields "2026-04-29 01:30:00") would do a
// lexicographic compare where any "1*" < any "2*" — silently dropping
// every row. Cast to integer and compare against strftime('%s', ...) which
// emits the matching epoch-seconds form.
func (s *Store) RecentUserMessagesAll(hours int, maxChars int) ([]string, error) {
	rows, err := s.db.Query(`
		SELECT content FROM v2_conversation_turns
		WHERE role = 'user'
		  AND CAST(timestamp AS INTEGER) >= strftime('%s', 'now', ?)
		ORDER BY timestamp DESC`,
		fmt.Sprintf("-%d hours", hours))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	total := 0
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		if total+len(c) > maxChars {
			break
		}
		total += len(c)
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Execution History
// ---------------------------------------------------------------------------

// RecordExecution inserts a new execution history record.
func (s *Store) RecordExecution(rec *ExecutionRecord) error {
	_, err := s.db.Exec(`
		INSERT INTO execution_history
			(skill_id, skill_name, started_at, finished_at, duration_ms,
			 input_params, result_summary, success, retry_count, usage_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.SkillID, rec.SkillName, rec.StartedAt,
		nullString(rec.FinishedAt), rec.DurationMs,
		nullString(rec.InputParams), nullString(rec.ResultSummary),
		boolToInt(rec.Success), rec.RetryCount,
		nullString(rec.UsageJSON))
	return err
}

// RecordLLMCallUsage inserts a token/cost record for a single LLM call.
func (s *Store) RecordLLMCallUsage(rec *LLMCallUsageRecord) error {
	if rec == nil {
		return fmt.Errorf("llm usage record is nil")
	}
	callKind := rec.CallKind
	if callKind == "" {
		callKind = "llm"
	}
	_, err := s.db.Exec(`
		INSERT INTO llm_call_usage
			(call_kind, provider, model, started_at, finished_at, duration_ms,
			 input_tokens, output_tokens, cache_creation_input_tokens,
			 cache_read_input_tokens, estimated_cost_usd, pricing_source,
			 pricing_matched, usage_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		callKind, rec.Provider, rec.Model, rec.StartedAt, nullString(rec.FinishedAt),
		rec.DurationMs, rec.InputTokens, rec.OutputTokens,
		rec.CacheCreationInputTokens, rec.CacheReadInputTokens,
		rec.EstimatedCost, rec.PricingSource, boolToInt(rec.PricingMatched),
		nullString(rec.UsageJSON))
	return err
}

// RecentExecutions returns the most recent execution records.
func (s *Store) RecentExecutions(limit int) ([]ExecutionRecord, error) {
	rows, err := s.db.Query(`
		SELECT id, skill_id, skill_name, started_at,
			   COALESCE(finished_at,''), COALESCE(duration_ms,0),
			   COALESCE(input_params,''), COALESCE(result_summary,''),
			   success, retry_count, COALESCE(usage_json,'')
		FROM execution_history
		ORDER BY started_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanExecutions(rows)
}

// TodayStats returns aggregated execution statistics for the current day (UTC).
func (s *Store) TodayStats() (*ExecutionStats, error) {
	var st ExecutionStats
	err := s.db.QueryRow(`
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN success = 1 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN success = 0 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(retry_count), 0)
		FROM execution_history
		WHERE started_at >= date('now')`).Scan(
		&st.TotalRuns, &st.Successful, &st.Failed, &st.AutoRetries)
	if err != nil {
		return nil, err
	}

	var usageRows int
	var totalTokens sql.NullInt64
	var estimatedCost sql.NullFloat64
	var firstUsageStarted sql.NullString
	err = s.db.QueryRow(`
		SELECT
			COUNT(*),
			SUM(input_tokens + output_tokens + cache_creation_input_tokens + cache_read_input_tokens),
			SUM(estimated_cost_usd),
			MIN(started_at)
		FROM llm_call_usage
		WHERE started_at >= date('now')`).Scan(&usageRows, &totalTokens, &estimatedCost, &firstUsageStarted)
	if err != nil {
		return nil, err
	}
	if usageRows > 0 {
		st.TotalTokens = totalTokens.Int64
		st.EstimatedCostUSD = estimatedCost.Float64
		if firstUsageStarted.Valid {
			var legacyBefore sql.NullInt64
			if err := s.db.QueryRow(`
				SELECT SUM(
					COALESCE(json_extract(usage_json, '$.total_tokens'), 0) +
					COALESCE(json_extract(usage_json, '$.input_tokens'), 0) +
					COALESCE(json_extract(usage_json, '$.output_tokens'), 0) +
					COALESCE(json_extract(usage_json, '$.cache_creation_input_tokens'), 0) +
					COALESCE(json_extract(usage_json, '$.cache_read_input_tokens'), 0)
				)
				FROM execution_history
				WHERE started_at >= date('now')
				  AND started_at < ?
				  AND usage_json IS NOT NULL`, firstUsageStarted.String).Scan(&legacyBefore); err != nil {
				return nil, err
			}
			st.TotalTokens += legacyBefore.Int64
		}
		return &st, nil
	}

	// Compatibility for databases that only have execution_history.usage_json
	// rows. New usage accounting lives in llm_call_usage to avoid double counts.
	err = s.db.QueryRow(`
		SELECT SUM(
			COALESCE(json_extract(usage_json, '$.total_tokens'), 0) +
			COALESCE(json_extract(usage_json, '$.input_tokens'), 0) +
			COALESCE(json_extract(usage_json, '$.output_tokens'), 0) +
			COALESCE(json_extract(usage_json, '$.cache_creation_input_tokens'), 0) +
			COALESCE(json_extract(usage_json, '$.cache_read_input_tokens'), 0)
		)
		FROM execution_history
		WHERE started_at >= date('now')
		  AND usage_json IS NOT NULL`).Scan(&totalTokens)
	if err != nil {
		return nil, err
	}
	st.TotalTokens = totalTokens.Int64
	return &st, nil
}

// TodayLLMUsageByModel returns today's LLM usage grouped by provider/model.
func (s *Store) TodayLLMUsageByModel() ([]LLMUsageByModel, error) {
	rows, err := s.db.Query(`
		SELECT provider, model, COUNT(*),
		       COALESCE(SUM(input_tokens), 0),
		       COALESCE(SUM(output_tokens), 0),
		       COALESCE(SUM(cache_creation_input_tokens), 0),
		       COALESCE(SUM(cache_read_input_tokens), 0),
		       COALESCE(SUM(input_tokens + output_tokens + cache_creation_input_tokens + cache_read_input_tokens), 0) AS total_tokens,
		       COALESCE(SUM(estimated_cost_usd), 0) AS estimated_cost_usd
		FROM llm_call_usage
		WHERE started_at >= date('now')
		GROUP BY provider, model
		ORDER BY estimated_cost_usd DESC, total_tokens DESC, model ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []LLMUsageByModel
	for rows.Next() {
		var r LLMUsageByModel
		if err := rows.Scan(
			&r.Provider, &r.Model, &r.Calls,
			&r.InputTokens, &r.OutputTokens,
			&r.CacheCreationInputTokens, &r.CacheReadInputTokens,
			&r.TotalTokens, &r.EstimatedCostUSD,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SearchExecutions performs a full-text search over execution history.
func (s *Store) SearchExecutions(query string, limit int) ([]ExecutionRecord, error) {
	rows, err := s.db.Query(`
		SELECT e.id, e.skill_id, e.skill_name, e.started_at,
			   COALESCE(e.finished_at,''), COALESCE(e.duration_ms,0),
			   COALESCE(e.input_params,''), COALESCE(e.result_summary,''),
			   e.success, e.retry_count, COALESCE(e.usage_json,'')
		FROM execution_history e
		JOIN execution_fts f ON f.rowid = e.id
		WHERE execution_fts MATCH ?
		ORDER BY e.started_at DESC
		LIMIT ?`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanExecutions(rows)
}

// SkillExecutionCount returns how many times a specific skill has been executed.
func (s *Store) SkillExecutionCount(skillID string) (int, error) {
	var n int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM execution_history WHERE skill_id = ?", skillID,
	).Scan(&n)
	return n, err
}

// CleanupOldExecutions removes execution records older than the given number of
// days and returns how many rows were deleted.
func (s *Store) CleanupOldExecutions(days int) (int, error) {
	res, err := s.db.Exec(`
		DELETE FROM execution_history
		WHERE started_at < datetime('now', ?)`,
		fmt.Sprintf("-%d days", days))
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// ---------------------------------------------------------------------------
// Storage (namespaced KV)
// ---------------------------------------------------------------------------

// StorageGet retrieves a value from namespaced key-value storage.
func (s *Store) StorageGet(namespace, key string) (string, bool, error) {
	var v string
	err := s.db.QueryRow(
		"SELECT value FROM skill_storage WHERE namespace = ? AND key = ?",
		namespace, key,
	).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// StorageSet upserts a value in namespaced key-value storage.
func (s *Store) StorageSet(namespace, key, value string) error {
	_, err := s.db.Exec(`
		INSERT INTO skill_storage (namespace, key, value)
		VALUES (?, ?, ?)
		ON CONFLICT(namespace, key) DO UPDATE SET value = excluded.value`,
		namespace, key, value)
	return err
}

// StorageDelete removes a key from namespaced storage.
func (s *Store) StorageDelete(namespace, key string) error {
	_, err := s.db.Exec(
		"DELETE FROM skill_storage WHERE namespace = ? AND key = ?",
		namespace, key)
	return err
}

// StorageList returns all keys in a namespace.
func (s *Store) StorageList(namespace string) ([]string, error) {
	rows, err := s.db.Query(
		"SELECT key FROM skill_storage WHERE namespace = ? ORDER BY key",
		namespace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// ---------------------------------------------------------------------------
// User Context
// ---------------------------------------------------------------------------

// SetUserContext upserts a user context key.
func (s *Store) SetUserContext(key, value, source string) error {
	_, err := s.db.Exec(`
		INSERT INTO user_context (key, value, source, updated_at)
		VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(key) DO UPDATE SET
			value      = excluded.value,
			source     = excluded.source,
			updated_at = datetime('now')`,
		key, value, source)
	return err
}

// GetUserContext retrieves a single user context value.
func (s *Store) GetUserContext(key string) (string, bool, error) {
	var v string
	err := s.db.QueryRow(
		"SELECT value FROM user_context WHERE key = ?", key,
	).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// ListUserContextPrefix returns all key-value pairs whose keys start with
// the given prefix.
func (s *Store) ListUserContextPrefix(prefix string) ([]KeyValue, error) {
	rows, err := s.db.Query(
		"SELECT key, value FROM user_context WHERE key LIKE ? ORDER BY key",
		prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []KeyValue
	for rows.Next() {
		var kv KeyValue
		if err := rows.Scan(&kv.Key, &kv.Value); err != nil {
			return nil, err
		}
		out = append(out, kv)
	}
	return out, rows.Err()
}

// MemoryContextLines builds context sections for LLM prompt injection.
// Returns user facts, recent failures, and today's stats as markdown sections.
// Sections with no data are omitted entirely.
func (s *Store) MemoryContextLines() ([]string, error) {
	var sections []string

	// --- Remembered Facts (user_context, cap 20, most recent first) ---
	rows, err := s.db.Query(`
		SELECT key, value FROM user_context
		WHERE key NOT LIKE 'pending_staff_draft:%'
		  AND key NOT LIKE 'pending_staff_offer:%'
		  AND key NOT LIKE 'pending_staff_switch:%'
		  AND key NOT LIKE 'active_staff:%'
		ORDER BY updated_at DESC
		LIMIT 20`)
	if err != nil {
		return nil, fmt.Errorf("memory context facts: %w", err)
	}
	var factLines []string
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return nil, fmt.Errorf("memory context scan fact: %w", err)
		}
		factLines = append(factLines, fmt.Sprintf("- %s: %s", sanitizeForPrompt(k, 100), sanitizeForPrompt(v, 500)))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory context facts iter: %w", err)
	}
	if len(factLines) > 0 {
		sections = append(sections, "### Remembered Facts\n"+strings.Join(factLines, "\n"))
	}

	// --- Recent Failures (last 24h UTC, cap 5) ---
	cutoff := time.Now().UTC().Add(-24 * time.Hour).Format("2006-01-02T15:04:05Z")
	rows, err = s.db.Query(`
		SELECT skill_name, COALESCE(result_summary, ''), started_at
		FROM execution_history
		WHERE success = 0
		  AND started_at >= ?
		ORDER BY started_at DESC
		LIMIT 5`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("memory context failures: %w", err)
	}
	defer rows.Close()

	var failLines []string
	for rows.Next() {
		var name, summary, ts string
		if err := rows.Scan(&name, &summary, &ts); err != nil {
			return nil, fmt.Errorf("memory context scan failure: %w", err)
		}
		failLines = append(failLines, fmt.Sprintf("- %s: %s (%s)", name, sanitizeForPrompt(summary, 200), ts))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory context failures iter: %w", err)
	}
	if len(failLines) > 0 {
		sections = append(sections, "### Recent Failures\n"+strings.Join(failLines, "\n"))
	}

	// --- Today's Stats ---
	stats, err := s.TodayStats()
	if err != nil {
		return nil, fmt.Errorf("memory context stats: %w", err)
	}
	if stats.TotalRuns > 0 {
		section := fmt.Sprintf(
			"### Today's Stats\n- Runs: %d (success: %d, failed: %d)\n- Retries: %d\n- Tokens used: %d",
			stats.TotalRuns, stats.Successful, stats.Failed, stats.AutoRetries, stats.TotalTokens,
		)
		sections = append(sections, section)
	}

	return sections, nil
}

// sanitizeForPrompt strips newlines and caps length to prevent prompt injection
// and token explosion from user-supplied or skill-generated content.
func sanitizeForPrompt(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}

// DeleteUserContext removes a user context key. Returns true if a row was
// actually deleted.
func (s *Store) DeleteUserContext(key string) (bool, error) {
	res, err := s.db.Exec("DELETE FROM user_context WHERE key = ?", key)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ---------------------------------------------------------------------------
// Reflection / Evolution
// ---------------------------------------------------------------------------

// DeleteExpiredReflection removes user_context rows whose keys start with
// "reflection:" and whose updated_at is older than ttlDays days ago.
// Returns the number of deleted rows.
//
// Note: This performs a LIKE scan on user_context which is acceptable at
// current scale (<10K rows). If the table grows significantly, consider
// adding a partial index on the "reflection:" key prefix.
func (s *Store) DeleteExpiredReflection(ttlDays int) (int, error) {
	res, err := s.db.Exec(`
		DELETE FROM user_context
		WHERE key LIKE 'reflection:%'
		  AND updated_at <= datetime('now', ?)`,
		fmt.Sprintf("-%d days", ttlDays))
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// DeleteUserContextPrefix removes all user_context rows matching a key prefix.
// Returns the number of deleted rows.
func (s *Store) DeleteUserContextPrefix(prefix string) (int, error) {
	res, err := s.db.Exec(
		"DELETE FROM user_context WHERE key LIKE ?", prefix+"%")
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// ---------------------------------------------------------------------------
// Checkpoints
// ---------------------------------------------------------------------------

// CreateCheckpoint saves a checkpoint at the current latest conversation turn.
func (s *Store) CreateCheckpoint(label string) (int64, error) {
	var maxID int64
	err := s.db.QueryRow(
		"SELECT COALESCE(MAX(id), 0) FROM v2_conversation_turns",
	).Scan(&maxID)
	if err != nil {
		return 0, err
	}

	res, err := s.db.Exec(`
		INSERT INTO conversation_checkpoints (label, turn_id)
		VALUES (?, ?)`, label, maxID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// RollbackToCheckpoint deletes all conversation rows after the checkpoint's
// saved row ID. Returns the number of deleted rows.
func (s *Store) RollbackToCheckpoint(checkpointID int64) (int, error) {
	var turnID int64
	err := s.db.QueryRow(
		"SELECT turn_id FROM conversation_checkpoints WHERE id = ?",
		checkpointID,
	).Scan(&turnID)
	if err != nil {
		return 0, err
	}

	res, err := s.db.Exec(
		"DELETE FROM v2_conversation_turns WHERE id > ?",
		turnID)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// ListCheckpoints returns all account conversation checkpoints.
func (s *Store) ListCheckpoints() ([]Checkpoint, error) {
	rows, err := s.db.Query(`
		SELECT id, label, turn_id, created_at
		FROM conversation_checkpoints
		ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Checkpoint
	for rows.Next() {
		var c Checkpoint
		if err := rows.Scan(&c.ID, &c.Label, &c.TurnID, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Workspaces
// ---------------------------------------------------------------------------

// Workspace represents a registered workspace directory.
type Workspace struct {
	ID           string
	Name         string
	RootPath     string
	CreatedAt    string
	LastOpenedAt string
}

// SaveWorkspace upserts a workspace. The root_path UNIQUE constraint prevents
// duplicate paths under different IDs.
func (s *Store) SaveWorkspace(ws *Workspace) error {
	_, err := s.db.Exec(`
		INSERT INTO workspaces (id, name, root_path)
		VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name          = excluded.name,
			root_path     = excluded.root_path,
			last_opened_at = datetime('now')`,
		ws.ID, ws.Name, ws.RootPath)
	return err
}

// GetWorkspace returns a workspace by ID.
func (s *Store) GetWorkspace(id string) (*Workspace, error) {
	var ws Workspace
	err := s.db.QueryRow(`
		SELECT id, name, root_path, created_at, last_opened_at
		FROM workspaces WHERE id = ?`, id).
		Scan(&ws.ID, &ws.Name, &ws.RootPath, &ws.CreatedAt, &ws.LastOpenedAt)
	if err != nil {
		return nil, err
	}
	return &ws, nil
}

// ListWorkspaces returns all registered workspaces ordered by creation time.
func (s *Store) ListWorkspaces() ([]Workspace, error) {
	rows, err := s.db.Query(`
		SELECT id, name, root_path, created_at, last_opened_at
		FROM workspaces ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Workspace
	for rows.Next() {
		var ws Workspace
		if err := rows.Scan(&ws.ID, &ws.Name, &ws.RootPath, &ws.CreatedAt, &ws.LastOpenedAt); err != nil {
			return nil, err
		}
		out = append(out, ws)
	}
	return out, rows.Err()
}

// DeleteWorkspace removes a workspace by ID. Idempotent.
func (s *Store) DeleteWorkspace(id string) error {
	_, err := s.db.Exec("DELETE FROM workspaces WHERE id = ?", id)
	return err
}

// ListWorkspaceRootPaths returns just the root_path column for all workspaces.
// This is the hot path used by isPathAllowed.
func (s *Store) ListWorkspaceRootPaths() ([]string, error) {
	rows, err := s.db.Query("SELECT root_path FROM workspaces ORDER BY created_at")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type FileIndexRoot struct {
	ID       string
	RootPath string
}

// ListFileIndexRoots returns every directory root that should be visible to
// file tools and FTS indexing. The workspace_files table remains the storage
// backend name, but Projects are now first-class roots too.
func (s *Store) ListFileIndexRoots() ([]FileIndexRoot, error) {
	var out []FileIndexRoot
	wss, err := s.ListWorkspaces()
	if err != nil {
		return nil, err
	}
	for _, ws := range wss {
		out = append(out, FileIndexRoot{ID: ws.ID, RootPath: ws.RootPath})
	}

	rows, err := s.db.Query(`
		SELECT id, root_path
		FROM projects
		WHERE state != ?
		ORDER BY created_at`, ProjectStateArchived)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var root FileIndexRoot
		if err := rows.Scan(&root.ID, &root.RootPath); err != nil {
			return nil, err
		}
		out = append(out, root)
	}
	return out, rows.Err()
}

func (s *Store) ListFileIndexRootPaths() ([]string, error) {
	roots, err := s.ListFileIndexRoots()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		out = append(out, root.RootPath)
	}
	return out, nil
}

// SeedWorkspacesFromConfig inserts TOML-configured paths into the workspaces
// table if they don't already exist. Paths are canonicalised (Clean +
// EvalSymlinks when the target exists) before insertion so the live-indexer
// prefix match against fsnotify-emitted paths (which arrive symlink-resolved
// on macOS) stays consistent. Idempotent.
func (s *Store) SeedWorkspacesFromConfig(paths []string) error {
	ts := time.Now().UnixNano()
	for i, p := range paths {
		p = filepath.Clean(p)
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			p = resolved
		}
		// Use root_path as a natural dedup key.
		var exists int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM workspaces WHERE root_path = ?", p).Scan(&exists); err != nil {
			return fmt.Errorf("check workspace %q: %w", p, err)
		}
		if exists > 0 {
			continue
		}
		id := fmt.Sprintf("ws-seed-%d-%d", ts, i)
		if _, err := s.db.Exec(
			"INSERT INTO workspaces (id, name, root_path) VALUES (?, ?, ?)",
			id, p, p,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SeedWorkspaceRootsFromConfig(roots []core.WorkspaceRoot) error {
	ts := time.Now().UnixNano()
	for i, root := range roots {
		p := strings.TrimSpace(root.Path)
		if p == "" {
			continue
		}
		p = filepath.Clean(p)
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			p = resolved
		}
		var exists int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM workspaces WHERE root_path = ?", p).Scan(&exists); err != nil {
			return fmt.Errorf("check workspace %q: %w", p, err)
		}
		if exists > 0 {
			continue
		}
		id := strings.TrimSpace(root.Alias)
		if id == "" {
			id = fmt.Sprintf("ws-seed-%d-%d", ts, i)
		}
		if _, err := s.db.Exec(
			"INSERT INTO workspaces (id, name, root_path) VALUES (?, ?, ?)",
			id, id, p,
		); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Permissions
// ---------------------------------------------------------------------------

// SaveFileRule upserts a file permission rule.
func (s *Store) SaveFileRule(rule *FilePermissionRule) error {
	_, err := s.db.Exec(`
		INSERT INTO permission_file_rules
			(id, workspace_id, path_pattern, is_exception, can_read, can_write, can_delete)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			workspace_id = excluded.workspace_id,
			path_pattern = excluded.path_pattern,
			is_exception = excluded.is_exception,
			can_read     = excluded.can_read,
			can_write    = excluded.can_write,
			can_delete   = excluded.can_delete`,
		rule.ID, rule.WorkspaceID, rule.PathPattern,
		boolToInt(rule.IsException),
		boolToInt(rule.CanRead),
		boolToInt(rule.CanWrite),
		boolToInt(rule.CanDelete))
	return err
}

// ListFileRules returns all file permission rules for a workspace.
func (s *Store) ListFileRules(workspaceID string) ([]FilePermissionRule, error) {
	rows, err := s.db.Query(`
		SELECT id, workspace_id, path_pattern, is_exception,
			   can_read, can_write, can_delete, created_at
		FROM permission_file_rules
		WHERE workspace_id = ?
		ORDER BY created_at`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FilePermissionRule
	for rows.Next() {
		var r FilePermissionRule
		var isExc, canR, canW, canD int
		if err := rows.Scan(&r.ID, &r.WorkspaceID, &r.PathPattern,
			&isExc, &canR, &canW, &canD, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.IsException = isExc != 0
		r.CanRead = canR != 0
		r.CanWrite = canW != 0
		r.CanDelete = canD != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteFileRule removes a file permission rule by ID.
func (s *Store) DeleteFileRule(ruleID string) error {
	_, err := s.db.Exec(
		"DELETE FROM permission_file_rules WHERE id = ?", ruleID)
	return err
}

// SaveNetworkRule upserts a network permission rule.
func (s *Store) SaveNetworkRule(rule *NetworkPermissionRule) error {
	_, err := s.db.Exec(`
		INSERT INTO permission_network_rules
			(id, workspace_id, domain_pattern, allowed_methods)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			workspace_id    = excluded.workspace_id,
			domain_pattern  = excluded.domain_pattern,
			allowed_methods = excluded.allowed_methods`,
		rule.ID, rule.WorkspaceID, rule.DomainPattern, rule.AllowedMethods)
	return err
}

// ListNetworkRules returns all network permission rules for a workspace.
func (s *Store) ListNetworkRules(workspaceID string) ([]NetworkPermissionRule, error) {
	rows, err := s.db.Query(`
		SELECT id, workspace_id, domain_pattern, allowed_methods, created_at
		FROM permission_network_rules
		WHERE workspace_id = ?
		ORDER BY created_at`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []NetworkPermissionRule
	for rows.Next() {
		var r NetworkPermissionRule
		if err := rows.Scan(&r.ID, &r.WorkspaceID, &r.DomainPattern, &r.AllowedMethods, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveGlobalPath upserts a globally permitted filesystem path.
func (s *Store) SaveGlobalPath(path *GlobalPath) error {
	_, err := s.db.Exec(`
		INSERT INTO permission_global_paths (id, path, access_type)
		VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			path        = excluded.path,
			access_type = excluded.access_type`,
		path.ID, path.Path, path.AccessType)
	return err
}

// ListGlobalPaths returns all globally permitted paths.
func (s *Store) ListGlobalPaths() ([]GlobalPath, error) {
	rows, err := s.db.Query(`
		SELECT id, path, access_type, created_at
		FROM permission_global_paths
		ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []GlobalPath
	for rows.Next() {
		var g GlobalPath
		if err := rows.Scan(&g.ID, &g.Path, &g.AccessType, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GrantCapability records a global capability grant.
func (s *Store) GrantCapability(capability string) error {
	_, err := s.db.Exec(`
		INSERT INTO global_grants (capability)
		VALUES (?)
		ON CONFLICT(capability) DO NOTHING`, capability)
	return err
}

// HasCapabilityGrant checks whether a capability has been granted.
func (s *Store) HasCapabilityGrant(capability string) (bool, error) {
	var n int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM global_grants WHERE capability = ?", capability,
	).Scan(&n)
	return n > 0, err
}

// RevokeCapability removes a global capability grant.
func (s *Store) RevokeCapability(capability string) error {
	_, err := s.db.Exec(
		"DELETE FROM global_grants WHERE capability = ?", capability)
	return err
}

// ---------------------------------------------------------------------------
// Staff Management
// ---------------------------------------------------------------------------

// UpsertStaffMeta creates or updates a staff identity's metadata.
func (s *Store) UpsertStaffMeta(id, description, equippedSkills, createdBy string) error {
	return s.UpsertStaffMetaWithDisplayName(id, "", description, equippedSkills, createdBy)
}

// UpsertStaffMetaWithDisplayName creates or updates a staff identity's metadata.
func (s *Store) UpsertStaffMetaWithDisplayName(id, displayName, description, equippedSkills, createdBy string) error {
	_, err := s.db.Exec(`
		INSERT INTO staff_meta (id, display_name, description, equipped_skills, created_by)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			display_name    = CASE
				WHEN excluded.display_name != '' THEN excluded.display_name
				ELSE staff_meta.display_name
			END,
			description     = excluded.description,
			equipped_skills = excluded.equipped_skills,
			created_by      = excluded.created_by`,
		id, displayName, description, equippedSkills, createdBy)
	return err
}

// GetStaffMeta retrieves a single staff identity by ID.
func (s *Store) GetStaffMeta(id string) (*StaffMeta, bool, error) {
	var staff StaffMeta
	var active int
	err := s.db.QueryRow(`
		SELECT id, display_name, description, equipped_skills, active, created_by, created_at
		FROM staff_meta WHERE id = ?`, id,
	).Scan(&staff.ID, &staff.DisplayName, &staff.Description, &staff.EquippedSkills, &active, &staff.CreatedBy, &staff.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	staff.Active = active != 0
	return &staff, true, nil
}

// ListActiveStaff returns all staff identities where active = 1.
func (s *Store) ListActiveStaff() ([]StaffMeta, error) {
	rows, err := s.db.Query(`
		SELECT id, display_name, description, equipped_skills, active, created_by, created_at
		FROM staff_meta
		WHERE active = 1
		ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []StaffMeta
	for rows.Next() {
		var staff StaffMeta
		var active int
		if err := rows.Scan(&staff.ID, &staff.DisplayName, &staff.Description, &staff.EquippedSkills, &active, &staff.CreatedBy, &staff.CreatedAt); err != nil {
			return nil, err
		}
		staff.Active = active != 0
		out = append(out, staff)
	}
	return out, rows.Err()
}

// ReplaceStaffAliases replaces all aliases for a staff identity.
func (s *Store) ReplaceStaffAliases(staffID string, aliases []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM staff_aliases WHERE staff_id = ?", staffID); err != nil {
		_ = tx.Rollback()
		return err
	}
	seen := make(map[string]bool, len(aliases))
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" || alias == staffID || seen[alias] {
			continue
		}
		seen[alias] = true
		if _, err := tx.Exec("INSERT INTO staff_aliases (alias, staff_id) VALUES (?, ?)", alias, staffID); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// ListStaffAliases returns aliases for a staff identity.
func (s *Store) ListStaffAliases(staffID string) ([]string, error) {
	rows, err := s.db.Query("SELECT alias FROM staff_aliases WHERE staff_id = ? ORDER BY alias", staffID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var aliases []string
	for rows.Next() {
		var alias string
		if err := rows.Scan(&alias); err != nil {
			return nil, err
		}
		aliases = append(aliases, alias)
	}
	return aliases, rows.Err()
}

// ResolveStaffID resolves a canonical staff ID or alias to a staff ID.
func (s *Store) ResolveStaffID(idOrAlias string) (string, bool, error) {
	idOrAlias = strings.TrimSpace(idOrAlias)
	if idOrAlias == "" {
		return "", false, nil
	}
	if _, ok, err := s.GetStaffMeta(idOrAlias); err != nil || ok {
		return idOrAlias, ok, err
	}
	var staffID string
	err := s.db.QueryRow("SELECT staff_id FROM staff_aliases WHERE alias = ?", idOrAlias).Scan(&staffID)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return staffID, true, nil
}

// SetStaffActive enables or disables a staff identity.
func (s *Store) SetStaffActive(id string, active bool) error {
	_, err := s.db.Exec(
		"UPDATE staff_meta SET active = ? WHERE id = ?",
		boolToInt(active), id)
	return err
}

// UpdateEquippedStaffSkills replaces the equipped skills JSON for a staff identity.
func (s *Store) UpdateEquippedStaffSkills(id, skills string) error {
	_, err := s.db.Exec(
		"UPDATE staff_meta SET equipped_skills = ? WHERE id = ?",
		skills, id)
	return err
}

// ---------------------------------------------------------------------------
// Scheduled Skills
// ---------------------------------------------------------------------------

// GetLastRun returns the last run time for a scheduled skill, or nil if never
// run.
func (s *Store) GetLastRun(skillName string) (*time.Time, error) {
	var raw sql.NullString
	err := s.db.QueryRow(
		"SELECT last_run_at FROM skill_schedule WHERE skill_name = ?",
		skillName,
	).Scan(&raw)
	if err == sql.ErrNoRows || !raw.Valid {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t, err := time.Parse(time.RFC3339, raw.String)
	if err != nil {
		// Fall back to SQLite datetime format.
		t, err = time.Parse("2006-01-02 15:04:05", raw.String)
		if err != nil {
			return nil, fmt.Errorf("parse last_run_at %q: %w", raw.String, err)
		}
	}
	return &t, nil
}

// SetLastRun records the last execution time for a scheduled skill.
func (s *Store) SetLastRun(skillName string, t time.Time) error {
	_, err := s.db.Exec(`
		INSERT INTO skill_schedule (skill_name, last_run_at)
		VALUES (?, ?)
		ON CONFLICT(skill_name) DO UPDATE SET last_run_at = excluded.last_run_at`,
		skillName, t.UTC().Format(time.RFC3339))
	return err
}

// GetFailureCount returns the consecutive failure count for a scheduled skill.
func (s *Store) GetFailureCount(skillName string) (int, error) {
	var n int
	err := s.db.QueryRow(
		"SELECT failure_count FROM skill_schedule WHERE skill_name = ?",
		skillName,
	).Scan(&n)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return n, err
}

// IncrementFailureCount increases the failure count by one, upserting the row
// if needed.
func (s *Store) IncrementFailureCount(skillName string) error {
	_, err := s.db.Exec(`
		INSERT INTO skill_schedule (skill_name, failure_count)
		VALUES (?, 1)
		ON CONFLICT(skill_name) DO UPDATE SET
			failure_count = skill_schedule.failure_count + 1`,
		skillName)
	return err
}

// ResetFailureCount sets the failure count back to zero.
func (s *Store) ResetFailureCount(skillName string) error {
	_, err := s.db.Exec(`
		UPDATE skill_schedule SET failure_count = 0
		WHERE skill_name = ?`, skillName)
	return err
}

// ---------------------------------------------------------------------------
// Audit
// ---------------------------------------------------------------------------

// RecordAudit appends an entry to the audit log.
func (s *Store) RecordAudit(eventType, detail, severity string) error {
	_, err := s.db.Exec(`
		INSERT INTO audit_log (event_type, detail, severity)
		VALUES (?, ?, ?)`, eventType, detail, severity)
	return err
}

// RecentAuditEvents returns the most recent audit log entries.
func (s *Store) RecentAuditEvents(limit int) ([]AuditRecord, error) {
	rows, err := s.db.Query(`
		SELECT id, event_type, detail, severity, created_at
		FROM audit_log
		ORDER BY id DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AuditRecord
	for rows.Next() {
		var a AuditRecord
		if err := rows.Scan(&a.ID, &a.EventType, &a.Detail, &a.Severity, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Permission Audit
// ---------------------------------------------------------------------------

// LogPermissionEvent records a permission decision to the audit log.
func (s *Store) LogPermissionEvent(decision, channel, chatID, description, resource string) error {
	detail, _ := json.Marshal(map[string]string{
		"channel":     channel,
		"chat_id":     chatID,
		"description": description,
		"resource":    resource,
		"decision":    decision,
	})
	return s.RecordAudit("permission."+decision, string(detail), "info")
}

// QueryPermissionLog returns recent permission audit entries.
func (s *Store) QueryPermissionLog(limit int) ([]AuditRecord, error) {
	rows, err := s.db.Query(`
		SELECT id, event_type, detail, severity, created_at
		FROM audit_log
		WHERE event_type LIKE 'permission.%'
		ORDER BY id DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AuditRecord
	for rows.Next() {
		var a AuditRecord
		if err := rows.Scan(&a.ID, &a.EventType, &a.Detail, &a.Severity, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Pending Responses
// ---------------------------------------------------------------------------

const maxPendingRetries = 5

// EnqueueResponse saves a failed response for later retry, tagged with the
// owning accountID so retryPendingResponses can route back to the correct
// per-account channel instance.
func (s *Store) EnqueueResponse(accountID, eventType, chatID, response string) error {
	_, err := s.db.Exec(`
		INSERT INTO pending_responses (account_id, event_type, chat_id, response)
		VALUES (?, ?, ?, ?)`, accountID, eventType, chatID, response)
	return err
}

// DequeuePendingResponses returns up to limit responses whose next_retry is in the past.
func (s *Store) DequeuePendingResponses(limit int) ([]PendingResponse, error) {
	rows, err := s.db.Query(`
		SELECT id, account_id, event_type, chat_id, response, retry_count, created_at, next_retry
		FROM pending_responses
		WHERE next_retry <= datetime('now')
		ORDER BY next_retry ASC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PendingResponse
	for rows.Next() {
		var p PendingResponse
		if err := rows.Scan(&p.ID, &p.AccountID, &p.EventType, &p.ChatID, &p.Response,
			&p.RetryCount, &p.CreatedAt, &p.NextRetry); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MarkResponseDelivered removes a successfully delivered pending response.
func (s *Store) MarkResponseDelivered(id int64) error {
	_, err := s.db.Exec(`DELETE FROM pending_responses WHERE id = ?`, id)
	return err
}

// IncrementResponseRetry bumps the retry count and sets exponential backoff.
// Returns false if max retries exceeded (row deleted).
// The SELECT + UPDATE/DELETE is wrapped in a transaction for atomicity.
func (s *Store) IncrementResponseRetry(id int64) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var count int
	err = tx.QueryRow(`SELECT retry_count FROM pending_responses WHERE id = ?`, id).Scan(&count)
	if err != nil {
		return false, err
	}
	count++
	if count >= maxPendingRetries {
		_, err := tx.Exec(`DELETE FROM pending_responses WHERE id = ?`, id)
		if err != nil {
			return false, err
		}
		return false, tx.Commit()
	}
	// Exponential backoff: 60s, 120s, 240s, 480s
	delaySec := 30 * (1 << count)
	_, err = tx.Exec(`
		UPDATE pending_responses
		SET retry_count = ?, next_retry = datetime('now', '+' || ? || ' seconds')
		WHERE id = ?`, count, delaySec, id)
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// CleanupExpiredResponses deletes pending responses older than the given hours.
func (s *Store) CleanupExpiredResponses(hours int) (int, error) {
	result, err := s.db.Exec(`
		DELETE FROM pending_responses
		WHERE created_at < datetime('now', '-' || ? || ' hours')`, hours)
	if err != nil {
		return 0, err
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func scanExecutions(rows *sql.Rows) ([]ExecutionRecord, error) {
	var out []ExecutionRecord
	for rows.Next() {
		var r ExecutionRecord
		var success int
		if err := rows.Scan(
			&r.ID, &r.SkillID, &r.SkillName, &r.StartedAt,
			&r.FinishedAt, &r.DurationMs,
			&r.InputParams, &r.ResultSummary,
			&success, &r.RetryCount, &r.UsageJSON,
		); err != nil {
			return nil, err
		}
		r.Success = success != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Workspace File Index (FTS5)
// ---------------------------------------------------------------------------

// UpsertWorkspaceFile inserts or replaces a file metadata entry. Returns the
// row ID for linking to the FTS5 index. The indexed_at timestamp is always
// refreshed so callers can use it for stale-entry cleanup after reindex.
func (s *Store) UpsertWorkspaceFile(f *WorkspaceFile) (int64, error) {
	hasContent := 0
	if f.HasContent {
		hasContent = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO workspace_files (workspace_id, abs_path, rel_path, filename, extension, size, modified_at, has_content, indexed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(workspace_id, abs_path) DO UPDATE SET
			rel_path     = excluded.rel_path,
			filename     = excluded.filename,
			extension    = excluded.extension,
			size         = excluded.size,
			modified_at  = excluded.modified_at,
			has_content  = excluded.has_content,
			indexed_at   = datetime('now')`,
		f.WorkspaceID, f.AbsPath, f.RelPath, f.Filename, f.Extension, f.Size, f.ModifiedAt, hasContent)
	if err != nil {
		return 0, err
	}
	// Always query the actual id — LastInsertId is unreliable for ON CONFLICT
	// DO UPDATE (SQLite may return a stale or auto-incremented phantom value).
	var id int64
	err = s.db.QueryRow(
		"SELECT id FROM workspace_files WHERE workspace_id = ? AND abs_path = ?",
		f.WorkspaceID, f.AbsPath,
	).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// UpsertWorkspaceFTS replaces the FTS5 entry for a given file. For standalone
// FTS5 tables (no content= option), regular DELETE + INSERT is used.
func (s *Store) UpsertWorkspaceFTS(fileID int64, filename, body string) error {
	// Delete old entry if it exists. Standalone FTS5 supports regular DELETE.
	_, _ = s.db.Exec("DELETE FROM workspace_fts WHERE rowid = ?", fileID)
	_, err := s.db.Exec(
		"INSERT INTO workspace_fts(rowid, filename, body) VALUES(?, ?, ?)",
		fileID, filename, body)
	return err
}

// DeleteWorkspaceIndex removes all file metadata and FTS5 entries for a
// workspace atomically within a single transaction.
func (s *Store) DeleteWorkspaceIndex(wsID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		DELETE FROM workspace_fts
		WHERE rowid IN (SELECT id FROM workspace_files WHERE workspace_id = ?)`, wsID); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM workspace_files WHERE workspace_id = ?", wsID); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteWorkspaceFileByAbsPath removes a single file's metadata and FTS entry
// atomically. Returns nil if no matching row exists (idempotent). Used by the
// live indexer when an fsnotify Remove event fires.
func (s *Store) DeleteWorkspaceFileByAbsPath(workspaceID, absPath string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		DELETE FROM workspace_fts
		WHERE rowid IN (
			SELECT id FROM workspace_files
			WHERE workspace_id = ? AND abs_path = ?
		)`, workspaceID, absPath); err != nil {
		return err
	}
	if _, err := tx.Exec(
		"DELETE FROM workspace_files WHERE workspace_id = ? AND abs_path = ?",
		workspaceID, absPath); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteWorkspaceFilesByPrefix removes the workspace_files row at the exact
// path plus every row under it as a directory prefix, and the corresponding
// workspace_fts entries, atomically. Designed for fsnotify Remove events
// where the caller cannot tell whether the vanished path was a file or a
// directory: exact match covers the file case, the range clause covers the
// directory subtree.
//
// The range uses BINARY collation on abs_path. For a normalized prefix p,
// any descendant path begins with p+"/", so `abs_path >= p+"/" AND abs_path
// < p+"0"` matches exactly the subtree (since ASCII '/' == 0x2F and '0' ==
// 0x30, and no valid path fragment lies in [0x30, 0xFF] on the third byte
// while starting with p+"/"). Using bound parameters sidesteps LIKE escape
// issues when the caller's path contains %, _, or \ — nothing is
// interpreted as a pattern.
//
// Trailing slashes on the prefix are stripped so "/ws/dir/" and "/ws/dir"
// behave the same; fsnotify normally emits paths without them but this is
// defensive against future callers.
func (s *Store) DeleteWorkspaceFilesByPrefix(workspaceID, path string) error {
	normalized := strings.TrimRight(path, "/")
	if normalized == "" {
		// Refuse to scrub an entire workspace via an empty prefix — callers
		// that truly want that should use DeleteWorkspaceIndex.
		return nil
	}
	rangeStart := normalized + "/"
	rangeEnd := normalized + "0"

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		DELETE FROM workspace_fts
		WHERE rowid IN (
			SELECT id FROM workspace_files
			WHERE workspace_id = ?
			  AND (abs_path = ?
			       OR (abs_path >= ? AND abs_path < ?))
		)`, workspaceID, normalized, rangeStart, rangeEnd); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		DELETE FROM workspace_files
		WHERE workspace_id = ?
		  AND (abs_path = ?
		       OR (abs_path >= ? AND abs_path < ?))`,
		workspaceID, normalized, rangeStart, rangeEnd); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteStaleWorkspaceFiles removes workspace_files entries (and their FTS5
// counterparts) whose indexed_at is older than the given cutoff string
// (format: "2006-01-02 15:04:05"). Runs atomically in a single transaction.
func (s *Store) DeleteStaleWorkspaceFiles(wsID string, cutoff string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		DELETE FROM workspace_fts
		WHERE rowid IN (
			SELECT id FROM workspace_files
			WHERE workspace_id = ? AND indexed_at < ?
		)`, wsID, cutoff); err != nil {
		return err
	}
	if _, err := tx.Exec(
		"DELETE FROM workspace_files WHERE workspace_id = ? AND indexed_at < ?",
		wsID, cutoff); err != nil {
		return err
	}
	return tx.Commit()
}

// SearchWorkspaceFTS performs a full-text search across workspace files.
// Returns matching rows and the total count (independent of limit/offset).
// An empty query returns an error.
func (s *Store) SearchWorkspaceFTS(query, pathPrefix, ext string, limit, offset int) ([]WorkspaceFTSRow, int, error) {
	return s.SearchWorkspaceFTSScoped(query, pathPrefix, ext, nil, limit, offset)
}

// SearchWorkspaceFTSScoped performs full-text search within optional workspace roots.
func (s *Store) SearchWorkspaceFTSScoped(query, pathPrefix, ext string, workspaceIDs []string, limit, offset int) ([]WorkspaceFTSRow, int, error) {
	if query == "" {
		return nil, 0, fmt.Errorf("empty search query")
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	// Sanitize query to prevent FTS5 syntax abuse.
	safeQuery := sanitizeFTSQuery(query)
	if safeQuery == "" {
		return nil, 0, fmt.Errorf("empty search query after sanitization")
	}

	// Build WHERE clauses.
	where := "WHERE workspace_fts MATCH ?"
	args := []any{safeQuery}
	if ids := compactStrings(workspaceIDs); len(ids) > 0 {
		where += " AND wf.workspace_id IN (" + sqlPlaceholders(len(ids)) + ")"
		for _, id := range ids {
			args = append(args, id)
		}
	}
	if pathPrefix != "" {
		where += " AND wf.rel_path LIKE ? ESCAPE '\\'"
		args = append(args, escapeLIKE(pathPrefix)+"%")
	}
	if ext != "" {
		where += " AND wf.extension = ?"
		args = append(args, ext)
	}

	// Count total matches.
	countSQL := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM workspace_fts
		JOIN workspace_files wf ON wf.id = workspace_fts.rowid
		%s`, where)
	var total int
	if err := s.db.QueryRow(countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("search count: %w", err)
	}

	// Fetch results — copy args to avoid mutating the original slice.
	searchSQL := fmt.Sprintf(`
		SELECT wf.id, wf.abs_path, wf.rel_path, wf.filename, wf.extension, wf.size,
		       rank,
		       snippet(workspace_fts, 1, '', '', '…', 64)
		FROM workspace_fts
		JOIN workspace_files wf ON wf.id = workspace_fts.rowid
		%s
		ORDER BY rank
		LIMIT ? OFFSET ?`, where)
	searchArgs := make([]any, len(args)+2)
	copy(searchArgs, args)
	searchArgs[len(args)] = limit
	searchArgs[len(args)+1] = offset

	rows, err := s.db.Query(searchSQL, searchArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("search query: %w", err)
	}
	defer rows.Close()

	var out []WorkspaceFTSRow
	for rows.Next() {
		var r WorkspaceFTSRow
		if err := rows.Scan(&r.FileID, &r.AbsPath, &r.RelPath, &r.Filename,
			&r.Extension, &r.Size, &r.Score, &r.Snippet); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// AggregateWorkspaceFiles returns statistics about indexed workspace files.
// If pathPrefix is non-empty, only files under that relative path are counted.
func (s *Store) AggregateWorkspaceFiles(pathPrefix string) (
	totalFiles, indexedFiles int,
	totalSize int64,
	byExt map[string][2]int64, // [count, size]
	latestAt string,
	err error,
) {
	return s.AggregateWorkspaceFilesScoped(pathPrefix, nil)
}

// AggregateWorkspaceFilesScoped returns statistics within optional workspace roots.
func (s *Store) AggregateWorkspaceFilesScoped(pathPrefix string, workspaceIDs []string) (
	totalFiles, indexedFiles int,
	totalSize int64,
	byExt map[string][2]int64, // [count, size]
	latestAt string,
	err error,
) {
	byExt = make(map[string][2]int64)

	var whereParts []string
	var args []any
	if ids := compactStrings(workspaceIDs); len(ids) > 0 {
		whereParts = append(whereParts, "workspace_id IN ("+sqlPlaceholders(len(ids))+")")
		for _, id := range ids {
			args = append(args, id)
		}
	}
	if pathPrefix != "" {
		whereParts = append(whereParts, "rel_path LIKE ? ESCAPE '\\'")
		args = append(args, escapeLIKE(pathPrefix)+"%")
	}
	where := ""
	if len(whereParts) > 0 {
		where = " WHERE " + strings.Join(whereParts, " AND ")
	}

	// Totals.
	totalsSQL := fmt.Sprintf(`
		SELECT COUNT(*), COALESCE(SUM(CASE WHEN has_content = 1 THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(size), 0), COALESCE(MAX(indexed_at), '')
		FROM workspace_files%s`, where)
	if err = s.db.QueryRow(totalsSQL, args...).Scan(
		&totalFiles, &indexedFiles, &totalSize, &latestAt,
	); err != nil {
		return
	}

	// By extension.
	extSQL := fmt.Sprintf(`
		SELECT extension, COUNT(*), COALESCE(SUM(size), 0)
		FROM workspace_files%s
		GROUP BY extension
		ORDER BY COUNT(*) DESC`, where)
	rows, qErr := s.db.Query(extSQL, args...)
	if qErr != nil {
		err = qErr
		return
	}
	defer rows.Close()
	for rows.Next() {
		var ext string
		var cnt, sz int64
		if err = rows.Scan(&ext, &cnt, &sz); err != nil {
			return
		}
		byExt[ext] = [2]int64{cnt, sz}
	}
	err = rows.Err()
	return
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func sqlPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", n), ",")
}

// BeginTx starts a new database transaction. Used by the indexer for chunked
// batch inserts.
func (s *Store) BeginTx() (*sql.Tx, error) {
	return s.db.Begin()
}

// UpsertWorkspaceFileTx is the transactional variant of UpsertWorkspaceFile.
func (s *Store) UpsertWorkspaceFileTx(tx *sql.Tx, f *WorkspaceFile) (int64, error) {
	hasContent := 0
	if f.HasContent {
		hasContent = 1
	}
	_, err := tx.Exec(`
		INSERT INTO workspace_files (workspace_id, abs_path, rel_path, filename, extension, size, modified_at, has_content, indexed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(workspace_id, abs_path) DO UPDATE SET
			rel_path     = excluded.rel_path,
			filename     = excluded.filename,
			extension    = excluded.extension,
			size         = excluded.size,
			modified_at  = excluded.modified_at,
			has_content  = excluded.has_content,
			indexed_at   = datetime('now')`,
		f.WorkspaceID, f.AbsPath, f.RelPath, f.Filename, f.Extension, f.Size, f.ModifiedAt, hasContent)
	if err != nil {
		return 0, err
	}
	var id int64
	err = tx.QueryRow(
		"SELECT id FROM workspace_files WHERE workspace_id = ? AND abs_path = ?",
		f.WorkspaceID, f.AbsPath,
	).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// UpsertWorkspaceFTSTx is the transactional variant of UpsertWorkspaceFTS.
func (s *Store) UpsertWorkspaceFTSTx(tx *sql.Tx, fileID int64, filename, body string) error {
	_, _ = tx.Exec("DELETE FROM workspace_fts WHERE rowid = ?", fileID)
	_, err := tx.Exec(
		"INSERT INTO workspace_fts(rowid, filename, body) VALUES(?, ?, ?)",
		fileID, filename, body)
	return err
}

// SQLiteNow returns the current time from SQLite's datetime('now') function.
// Used to ensure clock consistency with indexed_at timestamps.
func (s *Store) SQLiteNow() (string, error) {
	var now string
	err := s.db.QueryRow("SELECT datetime('now')").Scan(&now)
	return now, err
}

// sanitizeFTSQuery strips FTS5 special operators from a user-provided query,
// quoting each term as a literal to prevent query syntax abuse.
func sanitizeFTSQuery(query string) string {
	terms := strings.Fields(query)
	safe := make([]string, 0, len(terms))
	for _, t := range terms {
		// Strip FTS5 operators and special syntax characters.
		t = strings.Map(func(r rune) rune {
			switch r {
			case '"', '*', '^', '{', '}', ':', '(', ')':
				return -1
			}
			return r
		}, t)
		t = strings.TrimSpace(t)
		// Skip FTS5 boolean keywords.
		upper := strings.ToUpper(t)
		if t == "" || upper == "AND" || upper == "OR" || upper == "NOT" || upper == "NEAR" {
			continue
		}
		safe = append(safe, `"`+t+`"`)
	}
	return strings.Join(safe, " ")
}

// escapeLIKE escapes SQL LIKE wildcards (%, _) in a string.
// Use with ESCAPE '\' in the SQL clause.
func escapeLIKE(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// ---------------------------------------------------------------------------
// llm_cache (generic LLM response cache; Phase 1 consumer: File.summary)
// ---------------------------------------------------------------------------

// LLMCacheRow is one row in the llm_cache table. Identity is the compound
// UNIQUE tuple (Kind, KeyHash, InputHash, Model, PromptHash); the remaining
// fields are the cached payload + provenance.
type LLMCacheRow struct {
	ID          int64
	Kind        string
	KeyHash     string
	InputHash   string
	Model       string
	PromptHash  string
	Result      string
	Metadata    string // JSON blob
	UsageInput  int64
	UsageOutput int64
	CreatedAt   string
}

// LookupLLMCache returns the cached row for the given identity tuple, or
// (nil, nil) on cache miss. DB errors propagate. This is the single
// read path for QuerySummary; the 5-column identity lookup matches the
// UNIQUE index exactly.
func (s *Store) LookupLLMCache(kind, keyHash, inputHash, model, promptHash string) (*LLMCacheRow, error) {
	row := &LLMCacheRow{}
	err := s.db.QueryRow(`
		SELECT id, kind, key_hash, input_hash, model, prompt_hash,
		       result, metadata, usage_input, usage_output, created_at
		FROM llm_cache
		WHERE kind = ? AND key_hash = ? AND input_hash = ? AND model = ? AND prompt_hash = ?`,
		kind, keyHash, inputHash, model, promptHash,
	).Scan(
		&row.ID, &row.Kind, &row.KeyHash, &row.InputHash, &row.Model, &row.PromptHash,
		&row.Result, &row.Metadata, &row.UsageInput, &row.UsageOutput, &row.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}

// InsertLLMCache upserts a row. ON CONFLICT lets force_refresh actually
// replace a stale or poisoned result — INSERT OR IGNORE would have kept
// the first value forever. Metadata defaults to "{}" when empty.
func (s *Store) InsertLLMCache(row *LLMCacheRow) error {
	if row == nil {
		return fmt.Errorf("InsertLLMCache: nil row")
	}
	metadata := row.Metadata
	if metadata == "" {
		metadata = "{}"
	}
	_, err := s.db.Exec(`
		INSERT INTO llm_cache
			(kind, key_hash, input_hash, model, prompt_hash,
			 result, metadata, usage_input, usage_output, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT (kind, key_hash, input_hash, model, prompt_hash)
		DO UPDATE SET
			result = excluded.result,
			metadata = excluded.metadata,
			usage_input = excluded.usage_input,
			usage_output = excluded.usage_output,
			created_at = excluded.created_at`,
		row.Kind, row.KeyHash, row.InputHash, row.Model, row.PromptHash,
		row.Result, metadata, row.UsageInput, row.UsageOutput,
	)
	return err
}

// DeleteLLMCacheByKeyHash removes every row for a given (kind, key_hash).
// Called from the live indexer's RemoveFile path so stale summaries do
// not accumulate after a file is deleted. Uses the idx_llm_cache_key
// index. An unknown tuple is a no-op.
func (s *Store) DeleteLLMCacheByKeyHash(kind, keyHash string) error {
	_, err := s.db.Exec(
		`DELETE FROM llm_cache WHERE kind = ? AND key_hash = ?`,
		kind, keyHash,
	)
	return err
}
