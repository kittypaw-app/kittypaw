package connectadmin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

type Store interface {
	UpsertProviderPolicy(context.Context, ProviderPolicy) error
	GetProviderPolicy(context.Context, string) (ProviderPolicy, error)
	ListProviderPolicies(context.Context) ([]ProviderPolicy, error)
	UpsertUserEntitlement(context.Context, UserEntitlement) error
	UpdateUserEntitlementWithAudit(context.Context, UserEntitlement, AuditEvent) error
	ListUserEntitlements(context.Context, UserEntitlementListOptions) (UserEntitlementListResult, error)
	UserAllowed(context.Context, string, string) (bool, error)
	AppendAuditEvent(context.Context, AuditEvent) error
	ListAuditEvents(context.Context, int) ([]AuditEvent, error)
	EnsureDefaultPolicies(context.Context, ProviderRegistry) error
}

type sqlExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func NewStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) UpsertProviderPolicy(ctx context.Context, p ProviderPolicy) error {
	if p.RequestedScopes == nil {
		p.RequestedScopes = []string{}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO connect_provider_policies
			(provider_id, enabled, default_entitlement, requested_scopes, verification_status, cost_mode, notes, updated_by, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, nullif($8, '')::uuid, now())
		ON CONFLICT (provider_id)
		DO UPDATE SET enabled = excluded.enabled,
			default_entitlement = excluded.default_entitlement,
			requested_scopes = excluded.requested_scopes,
			verification_status = excluded.verification_status,
			cost_mode = excluded.cost_mode,
			notes = excluded.notes,
			updated_by = excluded.updated_by,
			updated_at = now()
	`, p.ProviderID, p.Enabled, p.DefaultEntitlement, p.RequestedScopes, p.VerificationStatus, p.CostMode, p.Notes, p.UpdatedBy)
	return err
}

func (s *PostgresStore) EnsureDefaultPolicies(ctx context.Context, registry ProviderRegistry) error {
	for _, provider := range registry.List() {
		p := provider.DefaultPolicy
		if p.RequestedScopes == nil {
			p.RequestedScopes = []string{}
		}
		_, err := s.pool.Exec(ctx, `
			INSERT INTO connect_provider_policies
				(provider_id, enabled, default_entitlement, requested_scopes, verification_status, cost_mode, notes, updated_by, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, nullif($8, '')::uuid, now())
			ON CONFLICT (provider_id) DO NOTHING
		`, p.ProviderID, p.Enabled, p.DefaultEntitlement, p.RequestedScopes, p.VerificationStatus, p.CostMode, p.Notes, p.UpdatedBy)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresStore) GetProviderPolicy(ctx context.Context, providerID string) (ProviderPolicy, error) {
	var p ProviderPolicy
	err := s.pool.QueryRow(ctx, `
		SELECT provider_id, enabled, default_entitlement, requested_scopes, verification_status, cost_mode, notes,
		       COALESCE(updated_by::text, ''), updated_at
		FROM connect_provider_policies
		WHERE provider_id = $1
	`, providerID).Scan(&p.ProviderID, &p.Enabled, &p.DefaultEntitlement, &p.RequestedScopes, &p.VerificationStatus, &p.CostMode, &p.Notes, &p.UpdatedBy, &p.UpdatedAt)
	return p, err
}

func (s *PostgresStore) ListProviderPolicies(ctx context.Context) ([]ProviderPolicy, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT provider_id, enabled, default_entitlement, requested_scopes, verification_status, cost_mode, notes,
		       COALESCE(updated_by::text, ''), updated_at
		FROM connect_provider_policies
		ORDER BY provider_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProviderPolicy
	for rows.Next() {
		var p ProviderPolicy
		if err := rows.Scan(&p.ProviderID, &p.Enabled, &p.DefaultEntitlement, &p.RequestedScopes, &p.VerificationStatus, &p.CostMode, &p.Notes, &p.UpdatedBy, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *PostgresStore) UpsertUserEntitlement(ctx context.Context, e UserEntitlement) error {
	return upsertUserEntitlement(ctx, s.pool, e)
}

func (s *PostgresStore) UpdateUserEntitlementWithAudit(ctx context.Context, e UserEntitlement, audit AuditEvent) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := upsertUserEntitlement(ctx, tx, e); err != nil {
		return err
	}
	if err := appendAuditEvent(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func upsertUserEntitlement(ctx context.Context, exec sqlExecutor, e UserEntitlement) error {
	if e.QuotaJSON == nil {
		e.QuotaJSON = map[string]any{}
	}
	quota, err := json.Marshal(e.QuotaJSON)
	if err != nil {
		return fmt.Errorf("marshal quota: %w", err)
	}
	_, err = exec.Exec(ctx, `
		INSERT INTO connect_user_entitlements
			(user_id, provider_id, status, quota_json, reason, granted_by, granted_at, revoked_at)
		VALUES ($1, $2, $3, $4::jsonb, $5, nullif($6, '')::uuid, now(), CASE WHEN $3 = 'revoked' THEN now() ELSE null END)
		ON CONFLICT (user_id, provider_id)
		DO UPDATE SET status = excluded.status,
			quota_json = excluded.quota_json,
			reason = excluded.reason,
			granted_by = excluded.granted_by,
			granted_at = now(),
			revoked_at = CASE
				WHEN excluded.status = 'revoked' AND connect_user_entitlements.status = 'revoked' THEN connect_user_entitlements.revoked_at
				WHEN excluded.status = 'revoked' THEN now()
				ELSE null
			END
	`, e.UserID, e.ProviderID, e.Status, string(quota), e.Reason, e.GrantedBy)
	return err
}

func (s *PostgresStore) ListUserEntitlements(ctx context.Context, opts UserEntitlementListOptions) (UserEntitlementListResult, error) {
	opts = normalizeUserEntitlementListOptions(opts)
	where, args := userEntitlementListWhere(opts)

	var total int
	countSQL := `
		SELECT COUNT(*)
		FROM connect_user_entitlements e
		JOIN users u ON u.id = e.user_id
	` + where
	if err := s.pool.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return UserEntitlementListResult{}, err
	}

	offset := (opts.Page - 1) * opts.PerPage
	listArgs := append(append([]any(nil), args...), opts.PerPage, offset)
	listSQL := `
		SELECT e.id::text, e.user_id::text, u.email, u.name, e.provider_id, e.status,
		       e.quota_json, e.reason, COALESCE(e.granted_by::text, ''), e.granted_at, e.revoked_at
		FROM connect_user_entitlements e
		JOIN users u ON u.id = e.user_id
	` + where + `
		ORDER BY e.granted_at DESC, lower(u.email), e.provider_id
		LIMIT $` + fmt.Sprint(len(args)+1) + ` OFFSET $` + fmt.Sprint(len(args)+2)

	rows, err := s.pool.Query(ctx, listSQL, listArgs...)
	if err != nil {
		return UserEntitlementListResult{}, err
	}
	defer rows.Close()

	var items []UserEntitlementRow
	for rows.Next() {
		var row UserEntitlementRow
		var quotaJSON []byte
		if err := rows.Scan(&row.ID, &row.UserID, &row.UserEmail, &row.UserName, &row.ProviderID, &row.Status, &quotaJSON, &row.Reason, &row.GrantedBy, &row.GrantedAt, &row.RevokedAt); err != nil {
			return UserEntitlementListResult{}, err
		}
		if len(quotaJSON) == 0 {
			row.QuotaJSON = map[string]any{}
		} else if err := json.Unmarshal(quotaJSON, &row.QuotaJSON); err != nil {
			return UserEntitlementListResult{}, fmt.Errorf("decode entitlement quota: %w", err)
		}
		if row.QuotaJSON == nil {
			row.QuotaJSON = map[string]any{}
		}
		items = append(items, row)
	}
	if err := rows.Err(); err != nil {
		return UserEntitlementListResult{}, err
	}

	return UserEntitlementListResult{
		Items:   items,
		Page:    opts.Page,
		PerPage: opts.PerPage,
		Total:   total,
	}, nil
}

func normalizeUserEntitlementListOptions(opts UserEntitlementListOptions) UserEntitlementListOptions {
	if opts.Page < 1 {
		opts.Page = 1
	}
	switch {
	case opts.PerPage < 1:
		opts.PerPage = 25
	case opts.PerPage > 100:
		opts.PerPage = 100
	}
	opts.ProviderID = strings.TrimSpace(opts.ProviderID)
	opts.Status = strings.TrimSpace(opts.Status)
	opts.EmailQuery = strings.TrimSpace(opts.EmailQuery)
	return opts
}

func userEntitlementListWhere(opts UserEntitlementListOptions) (string, []any) {
	var clauses []string
	var args []any
	if opts.ProviderID != "" {
		args = append(args, opts.ProviderID)
		clauses = append(clauses, fmt.Sprintf("e.provider_id = $%d", len(args)))
	}
	if opts.Status != "" {
		args = append(args, opts.Status)
		clauses = append(clauses, fmt.Sprintf("e.status = $%d", len(args)))
	}
	if opts.EmailQuery != "" {
		args = append(args, "%"+opts.EmailQuery+"%")
		clauses = append(clauses, fmt.Sprintf("u.email ILIKE $%d", len(args)))
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func (s *PostgresStore) UserAllowed(ctx context.Context, userID, providerID string) (bool, error) {
	var enabled bool
	var defaultEntitlement string
	err := s.pool.QueryRow(ctx, `
		SELECT enabled, default_entitlement
		FROM connect_provider_policies
		WHERE provider_id = $1
	`, providerID).Scan(&enabled, &defaultEntitlement)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	if !enabled {
		return false, nil
	}

	var status string
	var revoked bool
	err = s.pool.QueryRow(ctx, `
		SELECT status, revoked_at IS NOT NULL
		FROM connect_user_entitlements
		WHERE user_id = $1 AND provider_id = $2
	`, userID, providerID).Scan(&status, &revoked)
	if err != nil {
		if err == pgx.ErrNoRows {
			return defaultEntitlement == DefaultEntitlementAllow, nil
		}
		return false, err
	}
	return status == EntitlementAllowed && !revoked, nil
}

func (s *PostgresStore) UserQuotaJSON(ctx context.Context, userID, providerID string) (map[string]any, error) {
	var quotaJSON []byte
	err := s.pool.QueryRow(ctx, `
		SELECT quota_json
		FROM connect_user_entitlements
		WHERE user_id = $1 AND provider_id = $2 AND status = $3 AND revoked_at IS NULL
	`, userID, providerID, EntitlementAllowed).Scan(&quotaJSON)
	if err != nil {
		if err == pgx.ErrNoRows {
			return map[string]any{}, nil
		}
		return nil, err
	}
	if len(quotaJSON) == 0 {
		return map[string]any{}, nil
	}
	var quota map[string]any
	if err := json.Unmarshal(quotaJSON, &quota); err != nil {
		return nil, fmt.Errorf("decode entitlement quota: %w", err)
	}
	if quota == nil {
		quota = map[string]any{}
	}
	return quota, nil
}

func (s *PostgresStore) AppendAuditEvent(ctx context.Context, e AuditEvent) error {
	return appendAuditEvent(ctx, s.pool, e)
}

func appendAuditEvent(ctx context.Context, exec sqlExecutor, e AuditEvent) error {
	beforeJSON, err := json.Marshal(e.Before)
	if err != nil {
		return fmt.Errorf("marshal before: %w", err)
	}
	afterJSON, err := json.Marshal(e.After)
	if err != nil {
		return fmt.Errorf("marshal after: %w", err)
	}
	_, err = exec.Exec(ctx, `
		INSERT INTO connect_admin_audit_events
			(actor_user_id, action, provider_id, target_user_id, before_json, after_json)
		VALUES (nullif($1, '')::uuid, $2, nullif($3, ''), nullif($4, '')::uuid, $5::jsonb, $6::jsonb)
	`, e.ActorUserID, e.Action, e.ProviderID, e.TargetUserID, string(beforeJSON), string(afterJSON))
	return err
}

func (s *PostgresStore) ListAuditEvents(ctx context.Context, limit int) ([]AuditEvent, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, COALESCE(actor_user_id::text, ''), action, COALESCE(provider_id, ''),
		       COALESCE(target_user_id::text, ''), COALESCE(before_json, '{}'::jsonb),
		       COALESCE(after_json, '{}'::jsonb), created_at
		FROM connect_admin_audit_events
		ORDER BY created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEvent
	for rows.Next() {
		var beforeJSON, afterJSON []byte
		var e AuditEvent
		if err := rows.Scan(&e.ID, &e.ActorUserID, &e.Action, &e.ProviderID, &e.TargetUserID, &beforeJSON, &afterJSON, &e.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(beforeJSON, &e.Before)
		_ = json.Unmarshal(afterJSON, &e.After)
		out = append(out, e)
	}
	return out, rows.Err()
}
