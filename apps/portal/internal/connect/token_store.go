package connect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrProviderTokenNotFound = errors.New("connect provider token not found")

type ProviderTokenRecord struct {
	UserID       string
	ProviderID   string
	AccessToken  string
	RefreshToken string
	TokenType    string
	Scope        string
	Username     string
	ExpiresAt    *time.Time
}

type UsageRecord struct {
	UserID       string
	ProviderID   string
	Operation    string
	Quantity     int
	MonthlyLimit int
	Now          time.Time
	Metadata     map[string]any
}

type BrokerTokenStore interface {
	SaveProviderToken(context.Context, ProviderTokenRecord) error
	LoadProviderToken(context.Context, string, string) (ProviderTokenRecord, error)
	RecordUsage(context.Context, UsageRecord) (bool, error)
}

type MemoryTokenStore struct {
	mu     sync.Mutex
	now    time.Time
	tokens map[string]ProviderTokenRecord
	usage  map[string]int
}

func NewMemoryTokenStore(now time.Time) *MemoryTokenStore {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return &MemoryTokenStore{
		now:    now.UTC(),
		tokens: make(map[string]ProviderTokenRecord),
		usage:  make(map[string]int),
	}
}

func (s *MemoryTokenStore) SaveProviderToken(_ context.Context, record ProviderTokenRecord) error {
	if record.UserID == "" || record.ProviderID == "" {
		return fmt.Errorf("user_id and provider_id are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[tokenStoreKey(record.UserID, record.ProviderID)] = cloneProviderTokenRecord(record)
	return nil
}

func (s *MemoryTokenStore) LoadProviderToken(_ context.Context, userID, providerID string) (ProviderTokenRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.tokens[tokenStoreKey(userID, providerID)]
	if !ok {
		return ProviderTokenRecord{}, ErrProviderTokenNotFound
	}
	return cloneProviderTokenRecord(record), nil
}

func (s *MemoryTokenStore) RecordUsage(_ context.Context, record UsageRecord) (bool, error) {
	if record.Quantity < 0 {
		return false, fmt.Errorf("usage quantity must be non-negative")
	}
	now := record.Now
	if now.IsZero() {
		now = s.now
	}
	window := usageMonthStart(now)
	key := usageStoreKey(record.UserID, record.ProviderID, "post_reads", window)

	s.mu.Lock()
	defer s.mu.Unlock()
	used := s.usage[key]
	if record.MonthlyLimit > 0 && used+record.Quantity > record.MonthlyLimit {
		return false, nil
	}
	s.usage[key] = used + record.Quantity
	return true, nil
}

type PostgresTokenStore struct {
	pool   *pgxpool.Pool
	cipher *TokenCipher
}

func NewPostgresTokenStore(pool *pgxpool.Pool, cipher *TokenCipher) *PostgresTokenStore {
	return &PostgresTokenStore{pool: pool, cipher: cipher}
}

func (s *PostgresTokenStore) SaveProviderToken(ctx context.Context, record ProviderTokenRecord) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("connect token store unavailable")
	}
	accessCiphertext, err := s.cipher.Encrypt(record.AccessToken)
	if err != nil {
		return err
	}
	var refreshCiphertext []byte
	if record.RefreshToken != "" {
		refreshCiphertext, err = s.cipher.Encrypt(record.RefreshToken)
		if err != nil {
			return err
		}
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO connect_provider_tokens
			(user_id, provider_id, access_token_ciphertext, refresh_token_ciphertext, token_type, scope, username, expires_at, updated_at)
		VALUES ($1, $2, $3, nullif($4, '')::bytea, $5, $6, $7, $8, now())
		ON CONFLICT (user_id, provider_id)
		DO UPDATE SET access_token_ciphertext = excluded.access_token_ciphertext,
			refresh_token_ciphertext = excluded.refresh_token_ciphertext,
			token_type = excluded.token_type,
			scope = excluded.scope,
			username = excluded.username,
			expires_at = excluded.expires_at,
			updated_at = now()
	`, record.UserID, record.ProviderID, accessCiphertext, refreshCiphertext, nonEmpty(record.TokenType, "Bearer"), record.Scope, record.Username, record.ExpiresAt)
	return err
}

func (s *PostgresTokenStore) LoadProviderToken(ctx context.Context, userID, providerID string) (ProviderTokenRecord, error) {
	if s == nil || s.pool == nil {
		return ProviderTokenRecord{}, fmt.Errorf("connect token store unavailable")
	}
	var record ProviderTokenRecord
	var accessCiphertext []byte
	var refreshCiphertext []byte
	err := s.pool.QueryRow(ctx, `
		SELECT user_id::text, provider_id, access_token_ciphertext, COALESCE(refresh_token_ciphertext, ''::bytea),
		       token_type, scope, username, expires_at
		FROM connect_provider_tokens
		WHERE user_id = $1 AND provider_id = $2
	`, userID, providerID).Scan(&record.UserID, &record.ProviderID, &accessCiphertext, &refreshCiphertext, &record.TokenType, &record.Scope, &record.Username, &record.ExpiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProviderTokenRecord{}, ErrProviderTokenNotFound
		}
		return ProviderTokenRecord{}, err
	}
	accessToken, err := s.cipher.Decrypt(accessCiphertext)
	if err != nil {
		return ProviderTokenRecord{}, err
	}
	record.AccessToken = accessToken
	if len(refreshCiphertext) > 0 {
		refreshToken, err := s.cipher.Decrypt(refreshCiphertext)
		if err != nil {
			return ProviderTokenRecord{}, err
		}
		record.RefreshToken = refreshToken
	}
	return record, nil
}

func (s *PostgresTokenStore) RecordUsage(ctx context.Context, record UsageRecord) (bool, error) {
	if s == nil || s.pool == nil {
		return false, fmt.Errorf("connect token store unavailable")
	}
	if record.Quantity < 0 {
		return false, fmt.Errorf("usage quantity must be non-negative")
	}
	now := record.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	window := usageMonthStart(now)
	metadata, err := json.Marshal(nonNilMap(record.Metadata))
	if err != nil {
		return false, fmt.Errorf("marshal usage metadata: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	var used int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(quantity), 0)::int
		FROM connect_provider_usage_events
		WHERE user_id = $1 AND provider_id = $2 AND quota_key = 'post_reads' AND window_start = $3
	`, record.UserID, record.ProviderID, window).Scan(&used); err != nil {
		return false, err
	}
	if record.MonthlyLimit > 0 && used+record.Quantity > record.MonthlyLimit {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO connect_provider_usage_events
			(user_id, provider_id, operation, quantity, quota_key, window_start, metadata_json, created_at)
		VALUES ($1, $2, $3, $4, 'post_reads', $5, $6::jsonb, now())
	`, record.UserID, record.ProviderID, record.Operation, record.Quantity, window, string(metadata)); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func cloneProviderTokenRecord(record ProviderTokenRecord) ProviderTokenRecord {
	out := record
	if record.ExpiresAt != nil {
		t := *record.ExpiresAt
		out.ExpiresAt = &t
	}
	return out
}

func tokenStoreKey(userID, providerID string) string {
	return userID + "\x00" + providerID
}

func usageStoreKey(userID, providerID, quotaKey string, window time.Time) string {
	return userID + "\x00" + providerID + "\x00" + quotaKey + "\x00" + window.Format(time.RFC3339)
}

func usageMonthStart(now time.Time) time.Time {
	utc := now.UTC()
	return time.Date(utc.Year(), utc.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func nonEmpty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func nonNilMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}
