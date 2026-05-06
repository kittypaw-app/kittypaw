package connectadmin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) UpsertProviderPolicy(ctx context.Context, p ProviderPolicy) error {
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
	quota, err := json.Marshal(e.QuotaJSON)
	if err != nil {
		return fmt.Errorf("marshal quota: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO connect_user_entitlements
			(user_id, provider_id, status, quota_json, reason, granted_by, granted_at, revoked_at)
		VALUES ($1, $2, $3, $4::jsonb, $5, nullif($6, '')::uuid, now(), CASE WHEN $3 = 'revoked' THEN now() ELSE null END)
		ON CONFLICT (user_id, provider_id)
		DO UPDATE SET status = excluded.status,
			quota_json = excluded.quota_json,
			reason = excluded.reason,
			granted_by = excluded.granted_by,
			granted_at = now(),
			revoked_at = CASE WHEN excluded.status = 'revoked' THEN now() ELSE null END
	`, e.UserID, e.ProviderID, e.Status, string(quota), e.Reason, e.GrantedBy)
	return err
}

func (s *PostgresStore) UserAllowed(ctx context.Context, userID, providerID string) (bool, error) {
	var status string
	var revoked bool
	err := s.pool.QueryRow(ctx, `
		SELECT status, revoked_at IS NOT NULL
		FROM connect_user_entitlements
		WHERE user_id = $1 AND provider_id = $2
	`, userID, providerID).Scan(&status, &revoked)
	if err != nil {
		if err == pgx.ErrNoRows {
			var allowedByDefault bool
			err := s.pool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1
					FROM connect_provider_policies
					WHERE provider_id = $1
						AND enabled = true
						AND default_entitlement = 'allow'
				)
			`, providerID).Scan(&allowedByDefault)
			if err != nil {
				return false, err
			}
			return allowedByDefault, nil
		}
		return false, err
	}
	return status == EntitlementAllowed && !revoked, nil
}

func (s *PostgresStore) AppendAuditEvent(ctx context.Context, e AuditEvent) error {
	beforeJSON, err := json.Marshal(e.Before)
	if err != nil {
		return fmt.Errorf("marshal before: %w", err)
	}
	afterJSON, err := json.Marshal(e.After)
	if err != nil {
		return fmt.Errorf("marshal after: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
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
