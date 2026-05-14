package store

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	MemoryScopeGlobal       = "global"
	MemoryScopeConversation = "conversation"
	MemoryScopeProject      = "project"
	MemoryScopeChannel      = "channel"
)

var ErrMemoryCurationNotApplyable = errors.New("memory curation candidate is not applyable")

// MemoryScope identifies the context where a memory is valid.
type MemoryScope struct {
	Type string
	ID   string
}

// UserMemoryWrite is the structured write form for prompt-safe user memory.
type UserMemoryWrite struct {
	Key        string
	Value      string
	Kind       string
	ScopeType  string
	ScopeID    string
	Source     string
	Confidence float64
	Sensitive  bool
	ExpiresAt  string
}

// MemoryRecord is a structured memory row with scope and provenance metadata.
type MemoryRecord struct {
	ID         int64   `json:"id,omitempty"`
	Key        string  `json:"key"`
	Value      string  `json:"value"`
	Kind       string  `json:"kind,omitempty"`
	ScopeType  string  `json:"scope_type,omitempty"`
	ScopeID    string  `json:"scope_id,omitempty"`
	Source     string  `json:"source,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
	Sensitive  bool    `json:"sensitive,omitempty"`
	ExpiresAt  string  `json:"expires_at,omitempty"`
	CreatedAt  string  `json:"created_at,omitempty"`
	UpdatedAt  string  `json:"updated_at,omitempty"`
	LastUsedAt string  `json:"last_used_at,omitempty"`
}

// PendingMemoryRecord is a proposed memory that requires explicit user approval.
type PendingMemoryRecord struct {
	ID         int64   `json:"id"`
	Key        string  `json:"key"`
	Value      string  `json:"value"`
	Kind       string  `json:"kind,omitempty"`
	ScopeType  string  `json:"scope_type,omitempty"`
	ScopeID    string  `json:"scope_id,omitempty"`
	Source     string  `json:"source,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
	Reason     string  `json:"reason,omitempty"`
	Status     string  `json:"status"`
	CreatedAt  string  `json:"created_at"`
	ExpiresAt  string  `json:"expires_at,omitempty"`
	ResolvedAt string  `json:"resolved_at,omitempty"`
}

// MemoryCurationCandidate is a reviewable cleanup suggestion for user memory.
type MemoryCurationCandidate struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Action    string         `json:"action"`
	Summary   string         `json:"summary"`
	Reason    string         `json:"reason,omitempty"`
	ScopeType string         `json:"scope_type,omitempty"`
	ScopeID   string         `json:"scope_id,omitempty"`
	Subject   string         `json:"subject,omitempty"`
	Applyable bool           `json:"applyable"`
	Records   []MemoryRecord `json:"records,omitempty"`
	TargetIDs []int64        `json:"target_ids,omitempty"`
}

func (s *Store) SetScopedUserMemory(req UserMemoryWrite) error {
	return setScopedUserMemoryWithExecer(s.db, req)
}

type userMemoryExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func setScopedUserMemoryWithExecer(exec userMemoryExecer, req UserMemoryWrite) error {
	normalized, err := normalizeUserMemoryWrite(req)
	if err != nil {
		return err
	}
	if normalized.Sensitive || !isPromptSafeUserMemory(normalized.Key, normalized.Value) {
		return ErrUnsafeUserMemory
	}
	_, err = exec.Exec(`
		INSERT INTO memories (
			key, value, kind, scope_type, scope_id, source,
			confidence, sensitive, expires_at, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, NULLIF(?, ''), datetime('now'), datetime('now'))
		ON CONFLICT(scope_type, scope_id, key) DO UPDATE SET
			value      = excluded.value,
			kind       = excluded.kind,
			source     = excluded.source,
			confidence = excluded.confidence,
			sensitive  = excluded.sensitive,
			expires_at = excluded.expires_at,
			updated_at = datetime('now')`,
		normalized.Key,
		normalized.Value,
		normalized.Kind,
		normalized.ScopeType,
		normalized.ScopeID,
		normalized.Source,
		normalized.Confidence,
		normalized.ExpiresAt,
	)
	return err
}

func (s *Store) CreatePendingUserMemory(req UserMemoryWrite, reason string) (PendingMemoryRecord, error) {
	normalized, err := normalizeUserMemoryWrite(req)
	if err != nil {
		return PendingMemoryRecord{}, err
	}
	if userMemoryCredentialUnsafe(normalized.Key, normalized.Value) {
		return PendingMemoryRecord{}, ErrUnsafeUserMemory
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "sensitive"
	}
	res, err := s.db.Exec(`
		INSERT INTO pending_memories (
			key, value, kind, scope_type, scope_id, source,
			confidence, reason, status, created_at, expires_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending', datetime('now'), NULLIF(?, ''))`,
		normalized.Key,
		normalized.Value,
		normalized.Kind,
		normalized.ScopeType,
		normalized.ScopeID,
		normalized.Source,
		normalized.Confidence,
		reason,
		normalized.ExpiresAt,
	)
	if err != nil {
		return PendingMemoryRecord{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return PendingMemoryRecord{}, err
	}
	return s.pendingUserMemory(id)
}

func (s *Store) ListPendingUserMemory(limit int) ([]PendingMemoryRecord, error) {
	limit = normalizeUserMemoryLimit(limit)
	rows, err := s.db.Query(`
		SELECT id, key, value, kind, scope_type, scope_id, source,
		       confidence, reason, status, created_at, expires_at, resolved_at
		FROM pending_memories
		WHERE status = 'pending'
		  AND (expires_at IS NULL OR expires_at = '' OR expires_at > datetime('now'))
		ORDER BY created_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PendingMemoryRecord
	for rows.Next() {
		rec, err := scanPendingMemoryRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *Store) ConfirmPendingUserMemory(id int64, source string) (MemoryRecord, bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return MemoryRecord{}, false, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	pending, ok, err := pendingUserMemoryByIDWithQuerier(tx, id, true)
	if err != nil || !ok {
		return MemoryRecord{}, ok, err
	}
	if source = strings.TrimSpace(source); source != "" {
		pending.Source = source
	}
	if err := setScopedUserMemoryWithExecer(tx, UserMemoryWrite{
		Key:        pending.Key,
		Value:      pending.Value,
		Kind:       pending.Kind,
		ScopeType:  pending.ScopeType,
		ScopeID:    pending.ScopeID,
		Source:     pending.Source,
		Confidence: pending.Confidence,
		ExpiresAt:  pending.ExpiresAt,
	}); err != nil {
		return MemoryRecord{}, false, err
	}
	res, err := tx.Exec(`
		UPDATE pending_memories
		SET status = 'confirmed', resolved_at = datetime('now')
		WHERE id = ? AND status = 'pending'`, id)
	if err != nil {
		return MemoryRecord{}, false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return MemoryRecord{}, false, err
	}
	if n == 0 {
		return MemoryRecord{}, false, nil
	}
	if err := tx.Commit(); err != nil {
		return MemoryRecord{}, false, err
	}
	committed = true
	rec, ok, err := s.GetMemoryRecordInScopes(pending.Key, []MemoryScope{{Type: pending.ScopeType, ID: pending.ScopeID}})
	return rec, ok, err
}

func (s *Store) CurateMemory(limit int) ([]MemoryCurationCandidate, error) {
	limit = normalizeMemoryCurationLimit(limit)
	records, err := s.ListMemoryRecords(500)
	if err != nil {
		return nil, err
	}
	candidates := make([]MemoryCurationCandidate, 0)
	candidates = append(candidates, memoryDuplicateCurationCandidates(records)...)
	candidates = append(candidates, memoryStaleCurationCandidates(records, time.Now().UTC())...)
	candidates = append(candidates, memoryConflictCurationCandidates(records)...)
	sort.SliceStable(candidates, func(i, j int) bool {
		left := memoryCurationTypeRank(candidates[i].Type)
		right := memoryCurationTypeRank(candidates[j].Type)
		if left != right {
			return left < right
		}
		return candidates[i].ID < candidates[j].ID
	})
	if len(candidates) > limit {
		return candidates[:limit], nil
	}
	return candidates, nil
}

func (s *Store) ApplyMemoryCurationCandidate(id string) (MemoryCurationCandidate, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return MemoryCurationCandidate{}, false, nil
	}
	candidates, err := s.CurateMemory(500)
	if err != nil {
		return MemoryCurationCandidate{}, false, err
	}
	for _, cand := range candidates {
		if cand.ID != id {
			continue
		}
		if !cand.Applyable {
			return cand, false, ErrMemoryCurationNotApplyable
		}
		switch cand.Action {
		case "delete_duplicates", "delete_stale":
			if _, err := s.deleteMemoryRecordsByID(cand.TargetIDs); err != nil {
				return MemoryCurationCandidate{}, false, err
			}
			return cand, true, nil
		default:
			return cand, false, ErrMemoryCurationNotApplyable
		}
	}
	return MemoryCurationCandidate{}, false, nil
}

func (s *Store) RejectPendingUserMemory(id int64) (bool, error) {
	if id <= 0 {
		return false, nil
	}
	res, err := s.db.Exec(`
		UPDATE pending_memories
		SET status = 'rejected', resolved_at = datetime('now')
		WHERE id = ? AND status = 'pending'`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) ListMemoryRecords(limit int) ([]MemoryRecord, error) {
	records, err := s.structuredMemoryRecords("", nil, limit, true)
	if err != nil {
		return nil, err
	}
	legacy, err := s.legacyUserMemoryRecords("", limit)
	if err != nil {
		return nil, err
	}
	return mergeMemoryRecords(records, legacy, nil, limit, true, ""), nil
}

func (s *Store) SearchMemoryRecords(query string, limit int) ([]MemoryRecord, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	records, err := s.structuredMemoryRecords(query, nil, limit, true)
	if err != nil {
		return nil, err
	}
	legacy, err := s.legacyUserMemoryRecords(query, limit)
	if err != nil {
		return nil, err
	}
	return mergeMemoryRecords(records, legacy, nil, limit, true, query), nil
}

func (s *Store) SearchMemoryRecordsInScopes(query string, scopes []MemoryScope, limit int) ([]MemoryRecord, error) {
	query = strings.TrimSpace(query)
	records, err := s.structuredMemoryRecords(query, scopes, limit, false)
	if err != nil {
		return nil, err
	}
	legacy, err := s.legacyUserMemoryRecords(query, limit)
	if err != nil {
		return nil, err
	}
	return mergeMemoryRecords(records, legacy, normalizeMemoryScopes(scopes), limit, false, query), nil
}

func (s *Store) GetMemoryRecordInScopes(key string, scopes []MemoryScope) (MemoryRecord, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return MemoryRecord{}, false, nil
	}
	records, err := s.structuredMemoryRecordsByKey(key, scopes)
	if err != nil {
		return MemoryRecord{}, false, err
	}
	var legacy []MemoryRecord
	if rec, ok, err := s.legacyUserMemoryRecordByKey(key); err != nil {
		return MemoryRecord{}, false, err
	} else if ok {
		legacy = append(legacy, rec)
	}
	merged := mergeMemoryRecords(records, legacy, normalizeMemoryScopes(scopes), 1, false, key)
	if len(merged) == 0 {
		return MemoryRecord{}, false, nil
	}
	return merged[0], true, nil
}

func (s *Store) getGlobalMemory(key string) (MemoryRecord, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return MemoryRecord{}, false, nil
	}
	row := s.db.QueryRow(`
		SELECT id, key, value, kind, scope_type, scope_id, source,
		       confidence, sensitive, expires_at, created_at, updated_at, last_used_at
		FROM memories
		WHERE key = ?
		  AND scope_type = ?
		  AND scope_id = ''
		  AND sensitive = 0
		  AND (expires_at IS NULL OR expires_at = '' OR expires_at > datetime('now'))`,
		key, MemoryScopeGlobal)
	rec, scanErr := scanMemoryRecord(row)
	if scanErr == sql.ErrNoRows {
		return MemoryRecord{}, false, nil
	}
	if scanErr != nil {
		return MemoryRecord{}, false, scanErr
	}
	if !isPromptSafeUserMemory(rec.Key, rec.Value) {
		return MemoryRecord{}, false, nil
	}
	return rec, true, nil
}

func (s *Store) memoryContextFactLines(scopes []MemoryScope, limit int) ([]string, error) {
	records, err := s.SearchMemoryRecordsInScopes("", scopes, limit)
	if err != nil {
		return nil, err
	}
	lines := make([]string, 0, len(records))
	for _, rec := range records {
		prefix := memoryPromptScopePrefix(rec)
		lines = append(lines, fmt.Sprintf("- %s%s: %s",
			prefix,
			sanitizeForPrompt(rec.Key, 100),
			sanitizeForPrompt(rec.Value, 500),
		))
	}
	return lines, nil
}

func (s *Store) deleteStructuredUserMemory(key string) (int, error) {
	return s.deleteStructuredUserMemoryInScope(key, MemoryScope{Type: MemoryScopeGlobal})
}

func (s *Store) deleteStructuredUserMemoryInScope(key string, scope MemoryScope) (int, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return 0, nil
	}
	normalized, ok := normalizeMemoryScope(scope)
	if !ok {
		return 0, fmt.Errorf("invalid memory scope")
	}
	rows, err := s.db.Query(`
		SELECT id, key, value
		FROM memories
		WHERE key = ? AND scope_type = ? AND scope_id = ? AND sensitive = 0`,
		key, normalized.Type, normalized.ID)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		var rowKey, value string
		if err := rows.Scan(&id, &rowKey, &value); err != nil {
			rows.Close()
			return 0, err
		}
		if isPromptSafeUserMemory(rowKey, value) {
			ids = append(ids, id)
		}
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	deleted := 0
	for _, id := range ids {
		res, err := s.db.Exec("DELETE FROM memories WHERE id = ?", id)
		if err != nil {
			return deleted, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			deleted += int(n)
		}
	}
	return deleted, nil
}

func (s *Store) DeleteUserMemoryInScope(key string, scope MemoryScope) (bool, error) {
	structuredDeleted, err := s.deleteStructuredUserMemoryInScope(key, scope)
	if err != nil {
		return false, err
	}
	normalized, ok := normalizeMemoryScope(scope)
	if !ok {
		return false, fmt.Errorf("invalid memory scope")
	}
	if normalized.Type != MemoryScopeGlobal {
		return structuredDeleted > 0, nil
	}
	_, exists, err := s.GetUserMemory(key)
	if err != nil || !exists {
		return structuredDeleted > 0, err
	}
	legacyDeleted, err := s.DeleteUserContext(key)
	return structuredDeleted > 0 || legacyDeleted, err
}

func (s *Store) DeleteUserMemoryInScopes(key string, scopes []MemoryScope) (bool, error) {
	rec, ok, err := s.GetMemoryRecordInScopes(key, scopes)
	if err != nil || !ok {
		return false, err
	}
	if rec.ID > 0 {
		deleted, err := s.deleteMemoryRecordsByID([]int64{rec.ID})
		if err != nil {
			return deleted > 0, err
		}
		if rec.ScopeType != MemoryScopeGlobal {
			return deleted > 0, nil
		}
		_, exists, err := s.GetUserMemory(key)
		if err != nil || !exists {
			return deleted > 0, err
		}
		legacyDeleted, err := s.DeleteUserContext(key)
		return deleted > 0 || legacyDeleted, err
	}
	return s.DeleteUserMemoryInScope(key, MemoryScope{Type: MemoryScopeGlobal})
}

func (s *Store) deletePromptSafeStructuredUserMemory() (int, error) {
	rows, err := s.db.Query(`
		SELECT id, key, value
		FROM memories
		WHERE sensitive = 0`)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		var key, value string
		if err := rows.Scan(&id, &key, &value); err != nil {
			rows.Close()
			return 0, err
		}
		if isPromptSafeUserMemory(key, value) {
			ids = append(ids, id)
		}
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	deleted := 0
	for _, id := range ids {
		res, err := s.db.Exec("DELETE FROM memories WHERE id = ?", id)
		if err != nil {
			return deleted, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			deleted += int(n)
		}
	}
	return deleted, nil
}

func (s *Store) deleteMemoryRecordsByID(ids []int64) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	deleted := 0
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		var key, value string
		var sensitive int
		err := tx.QueryRow(`SELECT key, value, sensitive FROM memories WHERE id = ?`, id).Scan(&key, &value, &sensitive)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return deleted, err
		}
		if sensitive != 0 || !isPromptSafeUserMemory(key, value) {
			continue
		}
		res, err := tx.Exec(`DELETE FROM memories WHERE id = ?`, id)
		if err != nil {
			return deleted, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			deleted += int(n)
		}
	}
	if err := tx.Commit(); err != nil {
		return deleted, err
	}
	committed = true
	return deleted, nil
}

func normalizeUserMemoryWrite(req UserMemoryWrite) (UserMemoryWrite, error) {
	req.Key = strings.TrimSpace(req.Key)
	req.Value = strings.TrimSpace(req.Value)
	req.Kind = normalizeMemoryKind(req.Kind, req.Key)
	req.Source = strings.TrimSpace(req.Source)
	if req.Source == "" {
		req.Source = "runner"
	}
	if req.Confidence <= 0 {
		req.Confidence = 1
	}
	if req.Confidence > 1 {
		req.Confidence = 1
	}
	scope, ok := normalizeMemoryScope(MemoryScope{Type: req.ScopeType, ID: req.ScopeID})
	if !ok {
		return UserMemoryWrite{}, fmt.Errorf("invalid memory scope")
	}
	req.ScopeType = scope.Type
	req.ScopeID = scope.ID
	if req.Key == "" || req.Value == "" {
		return UserMemoryWrite{}, ErrUnsafeUserMemory
	}
	return req, nil
}

func UserMemoryConfirmationReason(key, value string) string {
	k := strings.ToLower(strings.TrimSpace(key))
	v := strings.ToLower(strings.TrimSpace(value))
	switch {
	case strings.Contains(k, "email") || strings.Contains(k, "이메일") || looksLikeEmail(v):
		return "email"
	case strings.Contains(k, "phone") || strings.Contains(k, "전화") || strings.Contains(k, "tel"):
		return "phone"
	case strings.Contains(k, "address") || strings.Contains(k, "주소"):
		return "address"
	case strings.Contains(k, "birthday") || strings.Contains(k, "birthdate") || strings.Contains(k, "생년월일") || strings.Contains(k, "생일"):
		return "birthdate"
	case strings.Contains(k, "health") || strings.Contains(k, "medical") || strings.Contains(k, "diagnosis") || strings.Contains(k, "건강") || strings.Contains(k, "병원"):
		return "health"
	default:
		return ""
	}
}

func looksLikeEmail(value string) bool {
	at := strings.Index(value, "@")
	return at > 0 && at < len(value)-1 && strings.Contains(value[at+1:], ".")
}

func userMemoryCredentialUnsafe(key, value string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	v := strings.ToLower(strings.TrimSpace(value))
	for _, marker := range []string{
		"api_key",
		"apikey",
		"bot_token",
		"relay_token",
		"access_token",
		"refresh_token",
		"token",
		"secret",
		"password",
		"credential",
		"oauth",
	} {
		if strings.Contains(k, marker) || strings.Contains(v, marker) {
			return true
		}
	}
	return strings.HasPrefix(v, "sk-")
}

func normalizeMemoryKind(kind, key string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != "" {
		return kind
	}
	k := strings.ToLower(strings.TrimSpace(key))
	switch {
	case strings.Contains(k, "preference") || strings.HasPrefix(k, "pref."):
		return "preference"
	case strings.Contains(k, "decision"):
		return "decision"
	case strings.Contains(k, "task"):
		return "ongoing_task"
	case strings.Contains(k, "question"):
		return "open_question"
	case strings.Contains(k, "state"):
		return "state"
	default:
		return "fact"
	}
}

func normalizeMemoryScope(scope MemoryScope) (MemoryScope, bool) {
	scope.Type = strings.ToLower(strings.TrimSpace(scope.Type))
	scope.ID = strings.TrimSpace(scope.ID)
	if scope.Type == "" {
		scope.Type = MemoryScopeGlobal
	}
	switch scope.Type {
	case MemoryScopeGlobal:
		scope.ID = ""
	case MemoryScopeConversation, MemoryScopeProject, MemoryScopeChannel:
		if scope.ID == "" {
			return MemoryScope{}, false
		}
	default:
		return MemoryScope{}, false
	}
	return scope, true
}

func normalizeMemoryScopes(scopes []MemoryScope) []MemoryScope {
	out := make([]MemoryScope, 0, len(scopes)+1)
	seen := map[string]bool{}
	for _, scope := range scopes {
		normalized, ok := normalizeMemoryScope(scope)
		if !ok {
			continue
		}
		key := normalized.Type + "\x00" + normalized.ID
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, normalized)
	}
	if !seen[MemoryScopeGlobal+"\x00"] {
		out = append(out, MemoryScope{Type: MemoryScopeGlobal})
	}
	return out
}

func (s *Store) structuredMemoryRecords(query string, scopes []MemoryScope, limit int, allScopes bool) ([]MemoryRecord, error) {
	limit = normalizeUserMemoryLimit(limit)
	clauses := []string{
		"sensitive = 0",
		"(expires_at IS NULL OR expires_at = '' OR expires_at > datetime('now'))",
	}
	var args []any
	var orderArgs []any
	orderBy := "updated_at DESC"
	query = strings.TrimSpace(query)
	if query != "" {
		clause, searchArgs := memorySearchClause(query)
		clauses = append(clauses, clause)
		args = append(args, searchArgs...)
	}
	if !allScopes {
		normalizedScopes := normalizeMemoryScopes(scopes)
		scopeClauses := make([]string, 0, len(scopes)+1)
		orderCases := make([]string, 0, len(normalizedScopes))
		for i, scope := range normalizedScopes {
			scopeClauses = append(scopeClauses, "(scope_type = ? AND scope_id = ?)")
			args = append(args, scope.Type, scope.ID)
			orderCases = append(orderCases, fmt.Sprintf("WHEN scope_type = ? AND scope_id = ? THEN %d", i))
			orderArgs = append(orderArgs, scope.Type, scope.ID)
		}
		clauses = append(clauses, "("+strings.Join(scopeClauses, " OR ")+")")
		orderBy = fmt.Sprintf("CASE %s ELSE %d END, updated_at DESC", strings.Join(orderCases, " "), len(normalizedScopes)+1)
	}
	args = append(args, orderArgs...)
	args = append(args, limit*4)
	rows, err := s.db.Query(`
		SELECT id, key, value, kind, scope_type, scope_id, source,
		       confidence, sensitive, expires_at, created_at, updated_at, last_used_at
		FROM memories
		WHERE `+strings.Join(clauses, " AND ")+`
		ORDER BY `+orderBy+`
		LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []MemoryRecord
	for rows.Next() {
		rec, err := scanMemoryRecord(rows)
		if err != nil {
			return nil, err
		}
		if !isPromptSafeUserMemory(rec.Key, rec.Value) {
			continue
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func (s *Store) structuredMemoryRecordsByKey(key string, scopes []MemoryScope) ([]MemoryRecord, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, nil
	}
	scopeClauses := make([]string, 0, len(scopes)+1)
	var args []any
	args = append(args, key)
	for _, scope := range normalizeMemoryScopes(scopes) {
		scopeClauses = append(scopeClauses, "(scope_type = ? AND scope_id = ?)")
		args = append(args, scope.Type, scope.ID)
	}
	rows, err := s.db.Query(`
		SELECT id, key, value, kind, scope_type, scope_id, source,
		       confidence, sensitive, expires_at, created_at, updated_at, last_used_at
		FROM memories
		WHERE key = ?
		  AND sensitive = 0
		  AND (expires_at IS NULL OR expires_at = '' OR expires_at > datetime('now'))
		  AND (`+strings.Join(scopeClauses, " OR ")+`)
		ORDER BY updated_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []MemoryRecord
	for rows.Next() {
		rec, err := scanMemoryRecord(rows)
		if err != nil {
			return nil, err
		}
		if !isPromptSafeUserMemory(rec.Key, rec.Value) {
			continue
		}
		records = append(records, rec)
	}
	return records, rows.Err()
}

func (s *Store) legacyUserMemoryRecords(query string, limit int) ([]MemoryRecord, error) {
	limit = normalizeUserMemoryLimit(limit)
	clauses := []string{"1 = 1"}
	var args []any
	query = strings.TrimSpace(query)
	if query != "" {
		clause, searchArgs := memorySearchClause(query)
		clauses = append(clauses, clause)
		args = append(args, searchArgs...)
	}
	args = append(args, limit*4)
	rows, err := s.db.Query(`
		SELECT key, value, source, updated_at
		FROM user_context
		WHERE `+strings.Join(clauses, " AND ")+`
		ORDER BY updated_at DESC
		LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []MemoryRecord
	for rows.Next() {
		var rec MemoryRecord
		if err := rows.Scan(&rec.Key, &rec.Value, &rec.Source, &rec.UpdatedAt); err != nil {
			return nil, err
		}
		if !isPromptSafeUserMemory(rec.Key, rec.Value) {
			continue
		}
		rec.Kind = normalizeMemoryKind("", rec.Key)
		rec.ScopeType = MemoryScopeGlobal
		rec.Confidence = 1
		rec.CreatedAt = rec.UpdatedAt
		records = append(records, rec)
	}
	return records, rows.Err()
}

func (s *Store) legacyUserMemoryRecordByKey(key string) (MemoryRecord, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return MemoryRecord{}, false, nil
	}
	var rec MemoryRecord
	err := s.db.QueryRow(`
		SELECT key, value, source, updated_at
		FROM user_context
		WHERE key = ?`, key).
		Scan(&rec.Key, &rec.Value, &rec.Source, &rec.UpdatedAt)
	if err == sql.ErrNoRows {
		return MemoryRecord{}, false, nil
	}
	if err != nil {
		return MemoryRecord{}, false, err
	}
	if !isPromptSafeUserMemory(rec.Key, rec.Value) {
		return MemoryRecord{}, false, nil
	}
	rec.Kind = normalizeMemoryKind("", rec.Key)
	rec.ScopeType = MemoryScopeGlobal
	rec.Confidence = 1
	rec.CreatedAt = rec.UpdatedAt
	return rec, true, nil
}

func (s *Store) pendingUserMemory(id int64) (PendingMemoryRecord, error) {
	rec, ok, err := s.pendingUserMemoryByID(id, false)
	if err != nil {
		return PendingMemoryRecord{}, err
	}
	if !ok {
		return PendingMemoryRecord{}, sql.ErrNoRows
	}
	return rec, nil
}

func (s *Store) pendingUserMemoryIfPending(id int64) (PendingMemoryRecord, bool, error) {
	return s.pendingUserMemoryByID(id, true)
}

func (s *Store) pendingUserMemoryByID(id int64, pendingOnly bool) (PendingMemoryRecord, bool, error) {
	return pendingUserMemoryByIDWithQuerier(s.db, id, pendingOnly)
}

type pendingMemoryQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

func pendingUserMemoryByIDWithQuerier(q pendingMemoryQuerier, id int64, pendingOnly bool) (PendingMemoryRecord, bool, error) {
	if id <= 0 {
		return PendingMemoryRecord{}, false, nil
	}
	query := `
		SELECT id, key, value, kind, scope_type, scope_id, source,
		       confidence, reason, status, created_at, expires_at, resolved_at
		FROM pending_memories
		WHERE id = ?`
	if pendingOnly {
		query += ` AND status = 'pending'
			AND (expires_at IS NULL OR expires_at = '' OR expires_at > datetime('now'))`
	}
	rec, err := scanPendingMemoryRecord(q.QueryRow(query, id))
	if err == sql.ErrNoRows {
		return PendingMemoryRecord{}, false, nil
	}
	return rec, err == nil, err
}

type pendingMemoryScanner interface {
	Scan(dest ...any) error
}

func scanPendingMemoryRecord(scanner pendingMemoryScanner) (PendingMemoryRecord, error) {
	var rec PendingMemoryRecord
	var expiresAt sql.NullString
	var resolvedAt sql.NullString
	if err := scanner.Scan(
		&rec.ID,
		&rec.Key,
		&rec.Value,
		&rec.Kind,
		&rec.ScopeType,
		&rec.ScopeID,
		&rec.Source,
		&rec.Confidence,
		&rec.Reason,
		&rec.Status,
		&rec.CreatedAt,
		&expiresAt,
		&resolvedAt,
	); err != nil {
		return PendingMemoryRecord{}, err
	}
	if expiresAt.Valid {
		rec.ExpiresAt = expiresAt.String
	}
	if resolvedAt.Valid {
		rec.ResolvedAt = resolvedAt.String
	}
	return rec, nil
}

type memoryScanner interface {
	Scan(dest ...any) error
}

func scanMemoryRecord(scanner memoryScanner) (MemoryRecord, error) {
	var rec MemoryRecord
	var sensitive int
	var expiresAt sql.NullString
	var lastUsedAt sql.NullString
	if err := scanner.Scan(
		&rec.ID,
		&rec.Key,
		&rec.Value,
		&rec.Kind,
		&rec.ScopeType,
		&rec.ScopeID,
		&rec.Source,
		&rec.Confidence,
		&sensitive,
		&expiresAt,
		&rec.CreatedAt,
		&rec.UpdatedAt,
		&lastUsedAt,
	); err != nil {
		return MemoryRecord{}, err
	}
	rec.Sensitive = sensitive != 0
	if expiresAt.Valid {
		rec.ExpiresAt = expiresAt.String
	}
	if lastUsedAt.Valid {
		rec.LastUsedAt = lastUsedAt.String
	}
	return rec, nil
}

func mergeMemoryRecords(primary, legacy []MemoryRecord, scopes []MemoryScope, limit int, allScopes bool, query string) []MemoryRecord {
	limit = normalizeUserMemoryLimit(limit)
	query = strings.ToLower(strings.TrimSpace(query))
	merged := make([]MemoryRecord, 0, len(primary)+len(legacy))
	seen := map[string]bool{}
	add := func(rec MemoryRecord) {
		key := rec.ScopeType + "\x00" + rec.ScopeID + "\x00" + rec.Key
		if seen[key] {
			return
		}
		seen[key] = true
		merged = append(merged, rec)
	}
	for _, rec := range primary {
		add(rec)
	}
	for _, rec := range legacy {
		add(rec)
	}

	if allScopes {
		sort.SliceStable(merged, func(i, j int) bool {
			left := memoryRecordRelevance(merged[i], query)
			right := memoryRecordRelevance(merged[j], query)
			if left != right {
				return left < right
			}
			return merged[i].UpdatedAt > merged[j].UpdatedAt
		})
	} else {
		ranks := memoryScopeRanks(scopes)
		sort.SliceStable(merged, func(i, j int) bool {
			left := memoryRecordScopeRank(merged[i], ranks)
			right := memoryRecordScopeRank(merged[j], ranks)
			if left != right {
				return left < right
			}
			left = memoryRecordRelevance(merged[i], query)
			right = memoryRecordRelevance(merged[j], query)
			if left != right {
				return left < right
			}
			return merged[i].UpdatedAt > merged[j].UpdatedAt
		})
	}
	if len(merged) > limit {
		return merged[:limit]
	}
	return merged
}

func memoryRecordRelevance(rec MemoryRecord, query string) int {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return 0
	}
	key := strings.ToLower(strings.TrimSpace(rec.Key))
	value := strings.ToLower(strings.TrimSpace(rec.Value))
	terms := memorySearchTerms(query)
	switch {
	case key == query:
		return 0
	case strings.TrimPrefix(key, "memory:") == query:
		return 1
	case strings.Contains(key, query):
		return 2
	case strings.Contains(value, query):
		return 3
	default:
		if len(terms) == 0 {
			return 9
		}
		if memoryTextContainsAllTerms(key, terms) {
			return 4
		}
		if memoryTextContainsAllTerms(value, terms) {
			return 5
		}
		if memoryTextContainsAllTerms(key+" "+value, terms) {
			return 6
		}
		if memoryTextContainsAnyTerm(key, terms) {
			return 7
		}
		if memoryTextContainsAnyTerm(value, terms) {
			return 8
		}
		return 9
	}
}

func memorySearchClause(query string) (string, []any) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return "", nil
	}
	var parts []string
	var args []any
	addLike := func(value string) {
		parts = append(parts, "(lower(key) LIKE ? OR lower(value) LIKE ?)")
		like := "%" + value + "%"
		args = append(args, like, like)
	}
	addLike(query)
	for _, term := range memorySearchTerms(query) {
		if term == query {
			continue
		}
		addLike(term)
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

func memorySearchTerms(query string) []string {
	seen := map[string]bool{}
	var terms []string
	for _, term := range strings.Fields(strings.ToLower(strings.TrimSpace(query))) {
		term = strings.Trim(term, " \t\r\n.,;:!?()[]{}\"'`")
		if term == "" || seen[term] {
			continue
		}
		seen[term] = true
		terms = append(terms, term)
	}
	return terms
}

func memoryTextContainsAllTerms(text string, terms []string) bool {
	for _, term := range terms {
		if !strings.Contains(text, term) {
			return false
		}
	}
	return true
}

func memoryTextContainsAnyTerm(text string, terms []string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func memoryDuplicateCurationCandidates(records []MemoryRecord) []MemoryCurationCandidate {
	groups := map[string][]MemoryRecord{}
	for _, rec := range records {
		if rec.ID <= 0 {
			continue
		}
		subject := memoryCurationSubject(rec.Key)
		if subject == "" {
			continue
		}
		value := memoryCurationValue(rec.Value)
		if len(value) < 4 {
			continue
		}
		key := rec.ScopeType + "\x00" + rec.ScopeID + "\x00" + subject + "\x00" + value
		groups[key] = append(groups[key], rec)
	}
	var candidates []MemoryCurationCandidate
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		sortMemoryRecordsForCurationKeep(group)
		keep := group[0]
		targets := group[1:]
		targetIDs := memoryRecordIDs(targets)
		if len(targetIDs) == 0 {
			continue
		}
		subject := memoryCurationSubject(keep.Key)
		cand := MemoryCurationCandidate{
			Type:      "duplicate",
			Action:    "delete_duplicates",
			Summary:   fmt.Sprintf("Duplicate memory value in %s; keep %q and delete %d older duplicate(s).", memoryCurationScopeLabel(keep), keep.Key, len(targetIDs)),
			Reason:    "same subject and normalized value within the same scope",
			ScopeType: keep.ScopeType,
			ScopeID:   keep.ScopeID,
			Subject:   subject,
			Applyable: true,
			Records:   append([]MemoryRecord(nil), group...),
			TargetIDs: targetIDs,
		}
		cand.ID = memoryCurationID(cand)
		candidates = append(candidates, cand)
	}
	return candidates
}

func memoryStaleCurationCandidates(records []MemoryRecord, now time.Time) []MemoryCurationCandidate {
	cutoff := now.Add(-30 * 24 * time.Hour)
	var candidates []MemoryCurationCandidate
	for _, rec := range records {
		if rec.ID <= 0 || !memoryCurationEphemeralKind(rec.Kind) {
			continue
		}
		updated, ok := parseMemoryRecordTime(rec.UpdatedAt)
		if !ok || updated.After(cutoff) {
			continue
		}
		cand := MemoryCurationCandidate{
			Type:      "stale",
			Action:    "delete_stale",
			Summary:   fmt.Sprintf("Stale %s memory %q has not changed since %s.", rec.Kind, rec.Key, rec.UpdatedAt),
			Reason:    "ephemeral memory older than 30 days",
			ScopeType: rec.ScopeType,
			ScopeID:   rec.ScopeID,
			Subject:   memoryCurationSubject(rec.Key),
			Applyable: true,
			Records:   []MemoryRecord{rec},
			TargetIDs: []int64{rec.ID},
		}
		cand.ID = memoryCurationID(cand)
		candidates = append(candidates, cand)
	}
	return candidates
}

func memoryConflictCurationCandidates(records []MemoryRecord) []MemoryCurationCandidate {
	groups := map[string][]MemoryRecord{}
	for _, rec := range records {
		subject := memoryCurationSubject(rec.Key)
		if subject == "" {
			continue
		}
		key := rec.ScopeType + "\x00" + rec.ScopeID + "\x00" + subject
		groups[key] = append(groups[key], rec)
	}
	var candidates []MemoryCurationCandidate
	for _, group := range groups {
		if len(group) < 2 || !memoryCurationHasDistinctValues(group) {
			continue
		}
		sortMemoryRecordsForCurationKeep(group)
		base := group[0]
		subject := memoryCurationSubject(base.Key)
		cand := MemoryCurationCandidate{
			Type:      "conflict",
			Action:    "review_conflict",
			Summary:   fmt.Sprintf("Conflicting memory values for %q in %s need review.", subject, memoryCurationScopeLabel(base)),
			Reason:    "same memory subject has multiple distinct values",
			ScopeType: base.ScopeType,
			ScopeID:   base.ScopeID,
			Subject:   subject,
			Applyable: false,
			Records:   append([]MemoryRecord(nil), group...),
		}
		cand.ID = memoryCurationID(cand)
		candidates = append(candidates, cand)
	}
	return candidates
}

func normalizeMemoryCurationLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 500 {
		return 500
	}
	return limit
}

func memoryCurationTypeRank(typ string) int {
	switch typ {
	case "duplicate":
		return 0
	case "stale":
		return 1
	case "conflict":
		return 2
	default:
		return 9
	}
}

func memoryCurationID(cand MemoryCurationCandidate) string {
	var b strings.Builder
	b.WriteString(cand.Type)
	b.WriteByte('\n')
	b.WriteString(cand.Action)
	b.WriteByte('\n')
	b.WriteString(cand.ScopeType)
	b.WriteByte('\n')
	b.WriteString(cand.ScopeID)
	b.WriteByte('\n')
	b.WriteString(cand.Subject)
	for _, id := range cand.TargetIDs {
		b.WriteByte('\n')
		b.WriteString(strconv.FormatInt(id, 10))
	}
	for _, rec := range cand.Records {
		b.WriteByte('\n')
		b.WriteString(rec.ScopeType)
		b.WriteByte('\x00')
		b.WriteString(rec.ScopeID)
		b.WriteByte('\x00')
		b.WriteString(rec.Key)
		b.WriteByte('\x00')
		b.WriteString(memoryCurationValue(rec.Value))
	}
	sum := sha256.Sum256([]byte(b.String()))
	return fmt.Sprintf("%x", sum[:8])
}

func memoryRecordIDs(records []MemoryRecord) []int64 {
	ids := make([]int64, 0, len(records))
	for _, rec := range records {
		if rec.ID > 0 {
			ids = append(ids, rec.ID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func sortMemoryRecordsForCurationKeep(records []MemoryRecord) {
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].UpdatedAt != records[j].UpdatedAt {
			return records[i].UpdatedAt > records[j].UpdatedAt
		}
		if records[i].Confidence != records[j].Confidence {
			return records[i].Confidence > records[j].Confidence
		}
		return records[i].Key < records[j].Key
	})
}

func memoryCurationHasDistinctValues(records []MemoryRecord) bool {
	values := map[string]bool{}
	for _, rec := range records {
		value := memoryCurationValue(rec.Value)
		if value == "" {
			continue
		}
		values[value] = true
		if len(values) > 1 {
			return true
		}
	}
	return false
}

func memoryCurationValue(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func memoryCurationSubject(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, prefix := range []string{
		"memory:",
		"preference:",
		"pref.",
		"fact.",
		"user:",
		"identity:",
	} {
		key = strings.TrimPrefix(key, prefix)
	}
	key = strings.ReplaceAll(key, ".", ":")
	key = strings.Trim(key, ": ")
	if key == "" {
		return ""
	}
	parts := strings.Split(key, ":")
	return strings.TrimSpace(parts[len(parts)-1])
}

func memoryCurationEphemeralKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "ongoing_task", "open_question", "state":
		return true
	default:
		return false
	}
}

func parseMemoryRecordTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{
		"2006-01-02 15:04:05",
		time.RFC3339,
		time.RFC3339Nano,
	} {
		t, err := time.Parse(layout, value)
		if err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func memoryCurationScopeLabel(rec MemoryRecord) string {
	if rec.ScopeType == "" || rec.ScopeType == MemoryScopeGlobal {
		return "global memory"
	}
	if rec.ScopeID == "" {
		return rec.ScopeType + " memory"
	}
	return rec.ScopeType + ":" + rec.ScopeID
}

func memoryScopeRanks(scopes []MemoryScope) map[string]int {
	ranks := map[string]int{}
	for i, scope := range normalizeMemoryScopes(scopes) {
		ranks[scope.Type+"\x00"+scope.ID] = i
	}
	return ranks
}

func memoryRecordScopeRank(rec MemoryRecord, ranks map[string]int) int {
	if rank, ok := ranks[rec.ScopeType+"\x00"+rec.ScopeID]; ok {
		return rank
	}
	return len(ranks) + 1
}

func memoryPromptScopePrefix(rec MemoryRecord) string {
	if rec.ScopeType == "" || rec.ScopeType == MemoryScopeGlobal {
		return ""
	}
	if rec.ScopeID == "" {
		return "[" + rec.ScopeType + "] "
	}
	return "[" + rec.ScopeType + ":" + sanitizeForPrompt(rec.ScopeID, 80) + "] "
}
