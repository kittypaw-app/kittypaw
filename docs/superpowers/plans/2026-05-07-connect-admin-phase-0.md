# Connect Admin Phase 0 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a portal-owned Connect admin control plane that can show provider status, grant/revoke per-user Connect entitlements, and fail closed for cost-bearing X Connect unless a user is explicitly allowed.

**Architecture:** Keep the existing one-binary `apps/portal` shape. Add portal-host-only admin routes under `/admin/connect`, backed by Postgres policy/entitlement/audit tables and a small admin allowlist from env. Keep Gmail's local-token privacy model; gate X OAuth initiation with a first-party portal access token and a short-lived preauth session so browser redirects never carry API bearer tokens.

**Tech Stack:** Go, chi, net/http server-rendered HTML, pgx/Postgres migrations, existing portal RS256 JWT auth middleware, Cobra CLI, account-scoped local secrets.

---

## File Structure

Create:

- `apps/portal/internal/admin/authz.go` - small portal admin middleware backed by `PORTAL_ADMIN_EMAILS`.
- `apps/portal/internal/admin/authz_test.go` - unit tests for allowlist parsing and 401/403/200 behavior.
- `apps/portal/migrations/009_create_connect_admin.up.sql` - provider policy, entitlement, and audit tables.
- `apps/portal/migrations/009_create_connect_admin.down.sql` - drops the Phase 0 tables in reverse FK order.
- `apps/portal/internal/connectadmin/types.go` - provider policy, entitlement, audit event structs and string constants.
- `apps/portal/internal/connectadmin/store.go` - Postgres store for policies, entitlements, and audit.
- `apps/portal/internal/connectadmin/store_integration_test.go` - migration-backed DB tests.
- `apps/portal/internal/connectadmin/provider_registry.go` - static provider registry derived from Connect provider constants and env configuration.
- `apps/portal/internal/connectadmin/provider_registry_test.go` - registry default policy tests.
- `apps/portal/internal/connectadmin/handler.go` - admin HTML handlers and form POST handlers.
- `apps/portal/internal/connectadmin/handler_test.go` - httptest coverage for admin pages and mutations with a fake store.
- `apps/portal/internal/connect/preauth_store.go` - short-lived in-memory X preauth sessions.
- `apps/portal/internal/connect/preauth_store_test.go` - consume-once and expiry tests.

Modify:

- `apps/portal/internal/config/config.go` - add `PortalAdminEmails []string`, parsed from `PORTAL_ADMIN_EMAILS`.
- `apps/portal/cmd/server/main.go` - wire admin routes, stores, provider registry, and X preauth session route.
- `apps/portal/cmd/server/main_test.go` - host boundary, admin auth, and X gating route tests.
- `apps/portal/internal/connect/handler.go` - add X session initiation and gated X login.
- `apps/portal/internal/connect/handler_test.go` - X entitlement/preauth behavior.
- `apps/kittypaw/cli/cmd_connect.go` - make `kittypaw connect x` require local portal login and create an X connect session before opening the browser.
- `apps/kittypaw/cli/cmd_connect_test.go` - X login URL/session tests; Gmail stays unchanged.
- `apps/portal/DEPLOY.md` - document `PORTAL_ADMIN_EMAILS` and X invite-only operation.
- `docs/operations/connect-x-oauth.md` - document X staff/invite-only state and cost warning.

Do not modify:

- Gmail scopes.
- X scopes.
- Portal token storage model for Gmail/X.
- `apps/kittyapi`, `apps/chat`, `apps/kakao`, or shared contracts.

---

## Task 1: Admin Allowlist Config And Middleware

**Files:**

- Modify: `apps/portal/internal/config/config.go`
- Create: `apps/portal/internal/admin/authz.go`
- Create: `apps/portal/internal/admin/authz_test.go`

- [ ] **Step 1: Write config parsing tests**

Add tests in `apps/portal/internal/config/config_test.go`:

```go
func TestLoadParsesPortalAdminEmails(t *testing.T) {
	pemStr := generatePEM(t, 2048)
	b64 := base64.StdEncoding.EncodeToString([]byte(pemStr))

	cfg, err := loadWithEnv(t, map[string]string{
		"JWT_PRIVATE_KEY_PEM_B64": b64,
		"PORTAL_ADMIN_EMAILS":     " alice@example.com,BOB@example.com ,, ",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"alice@example.com", "bob@example.com"}
	if !reflect.DeepEqual(cfg.PortalAdminEmails, want) {
		t.Fatalf("PortalAdminEmails = %#v, want %#v", cfg.PortalAdminEmails, want)
	}
}
```

Add `reflect` to the existing `config_test.go` imports.

- [ ] **Step 2: Run the failing config test**

Run:

```bash
go test ./apps/portal/internal/config -run TestLoadParsesPortalAdminEmails -count=1
```

Expected: compile failure mentioning `PortalAdminEmails` or missing parser.

- [ ] **Step 3: Implement config parsing**

In `apps/portal/internal/config/config.go`, add the field:

```go
PortalAdminEmails []string
```

Load it in `Load()`:

```go
PortalAdminEmails: splitLowerCSV(os.Getenv("PORTAL_ADMIN_EMAILS")),
```

Add this helper near `splitCSV`:

```go
func splitLowerCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.ToLower(strings.TrimSpace(part)); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
```

In `LoadForTest()`, set:

```go
PortalAdminEmails: []string{"admin@example.com"},
```

- [ ] **Step 4: Verify config test passes**

Run:

```bash
go test ./apps/portal/internal/config -run TestLoadParsesPortalAdminEmails -count=1
```

Expected: PASS.

- [ ] **Step 5: Write admin middleware tests**

Create `apps/portal/internal/admin/authz_test.go`:

```go
package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kittypaw-app/kittyportal/internal/auth"
	"github.com/kittypaw-app/kittyportal/internal/model"
)

func TestMiddlewareRequiresAuthenticatedUser(t *testing.T) {
	mw := Middleware([]string{"admin@example.com"})
	req := httptest.NewRequest(http.MethodGet, "/admin/connect", nil)
	w := httptest.NewRecorder()

	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not run")
	})).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestMiddlewareRejectsNonAdminEmail(t *testing.T) {
	mw := Middleware([]string{"admin@example.com"})
	req := httptest.NewRequest(http.MethodGet, "/admin/connect", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{
		ID:    "user-1",
		Email: "user@example.com",
	}))
	w := httptest.NewRecorder()

	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not run")
	})).ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestMiddlewareAllowsConfiguredAdminEmailCaseInsensitive(t *testing.T) {
	mw := Middleware([]string{"admin@example.com"})
	req := httptest.NewRequest(http.MethodGet, "/admin/connect", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{
		ID:    "user-1",
		Email: "ADMIN@example.com",
	}))
	w := httptest.NewRecorder()

	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
}
```

- [ ] **Step 6: Run failing admin tests**

Run:

```bash
go test ./apps/portal/internal/admin -count=1
```

Expected: package or `Middleware` missing.

- [ ] **Step 7: Implement admin middleware**

Create `apps/portal/internal/admin/authz.go`:

```go
package admin

import (
	"net/http"
	"strings"

	"github.com/kittypaw-app/kittyportal/internal/auth"
)

func Middleware(adminEmails []string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(adminEmails))
	for _, email := range adminEmails {
		if normalized := normalizeEmail(email); normalized != "" {
			allowed[normalized] = struct{}{}
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := auth.UserFromContext(r.Context())
			if user == nil {
				http.Error(w, "authentication required", http.StatusUnauthorized)
				return
			}
			if _, ok := allowed[normalizeEmail(user.Email)]; !ok {
				http.Error(w, "admin access required", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
```

- [ ] **Step 8: Verify Task 1 tests**

Run:

```bash
go test ./apps/portal/internal/config ./apps/portal/internal/admin -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit Task 1**

Run:

```bash
git add apps/portal/internal/config/config.go apps/portal/internal/config/config_test.go apps/portal/internal/admin
git commit -m "feat(portal): add admin allowlist middleware"
```

---

## Task 2: Connect Admin Schema And Store

**Files:**

- Create: `apps/portal/migrations/009_create_connect_admin.up.sql`
- Create: `apps/portal/migrations/009_create_connect_admin.down.sql`
- Create: `apps/portal/internal/connectadmin/types.go`
- Create: `apps/portal/internal/connectadmin/store.go`
- Create: `apps/portal/internal/connectadmin/store_integration_test.go`

- [ ] **Step 1: Add migrations**

Create `apps/portal/migrations/009_create_connect_admin.up.sql`:

```sql
CREATE TABLE connect_provider_policies (
    provider_id          TEXT PRIMARY KEY,
    enabled              BOOLEAN NOT NULL DEFAULT false,
    default_entitlement  TEXT NOT NULL DEFAULT 'deny'
                         CHECK (default_entitlement IN ('allow', 'deny')),
    requested_scopes     TEXT[] NOT NULL DEFAULT '{}',
    verification_status  TEXT NOT NULL DEFAULT 'unknown'
                         CHECK (verification_status IN ('unknown', 'not_applicable', 'testing', 'submitted', 'verified', 'blocked')),
    cost_mode            TEXT NOT NULL DEFAULT 'none'
                         CHECK (cost_mode IN ('none', 'external_policy', 'kitty_paid')),
    notes                TEXT NOT NULL DEFAULT '',
    updated_by           UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE connect_user_entitlements (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider_id TEXT NOT NULL,
    status      TEXT NOT NULL CHECK (status IN ('allowed', 'blocked', 'revoked')),
    quota_json  JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(quota_json) = 'object'),
    reason      TEXT NOT NULL DEFAULT '',
    granted_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    granted_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at  TIMESTAMPTZ,
    UNIQUE (user_id, provider_id)
);

CREATE INDEX idx_connect_user_entitlements_provider ON connect_user_entitlements(provider_id);
CREATE INDEX idx_connect_user_entitlements_active ON connect_user_entitlements(user_id, provider_id)
    WHERE status = 'allowed' AND revoked_at IS NULL;

CREATE TABLE connect_admin_audit_events (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_user_id  UUID REFERENCES users(id) ON DELETE SET NULL,
    action         TEXT NOT NULL,
    provider_id    TEXT,
    target_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    before_json    JSONB,
    after_json     JSONB,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_connect_admin_audit_created ON connect_admin_audit_events(created_at DESC);
CREATE INDEX idx_connect_admin_audit_target ON connect_admin_audit_events(target_user_id, created_at DESC);
```

Create `apps/portal/migrations/009_create_connect_admin.down.sql`:

```sql
DROP TABLE IF EXISTS connect_admin_audit_events;
DROP TABLE IF EXISTS connect_user_entitlements;
DROP TABLE IF EXISTS connect_provider_policies;
```

- [ ] **Step 2: Add store integration test first**

Create `apps/portal/internal/connectadmin/store_integration_test.go`:

```go
//go:build integration

package connectadmin_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kittypaw-app/kittyportal/internal/connectadmin"
	"github.com/kittypaw-app/kittyportal/internal/model"
)

func TestStorePolicyEntitlementAndAudit(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()
	users := model.NewUserStore(pool)
	adminUser, err := users.CreateOrUpdate(ctx, "google", "admin-1", "admin@example.com", "Admin", "")
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	targetUser, err := users.CreateOrUpdate(ctx, "google", "target-1", "target@example.com", "Target", "")
	if err != nil {
		t.Fatalf("seed target: %v", err)
	}

	store := connectadmin.NewStore(pool)
	policy := connectadmin.ProviderPolicy{
		ProviderID:          "x",
		Enabled:             true,
		DefaultEntitlement:  connectadmin.DefaultEntitlementDeny,
		RequestedScopes:     []string{"tweet.read", "users.read", "offline.access"},
		VerificationStatus:  connectadmin.VerificationNotApplicable,
		CostMode:            connectadmin.CostModeKittyPaid,
		Notes:               "staff only",
		UpdatedBy:           adminUser.ID,
	}
	if err := store.UpsertProviderPolicy(ctx, policy); err != nil {
		t.Fatalf("UpsertProviderPolicy: %v", err)
	}
	gotPolicy, err := store.GetProviderPolicy(ctx, "x")
	if err != nil {
		t.Fatalf("GetProviderPolicy: %v", err)
	}
	if !gotPolicy.Enabled || gotPolicy.DefaultEntitlement != connectadmin.DefaultEntitlementDeny {
		t.Fatalf("policy = %#v", gotPolicy)
	}

	quota := map[string]any{"monthly_post_reads": float64(100)}
	if err := store.UpsertUserEntitlement(ctx, connectadmin.UserEntitlement{
		UserID:     targetUser.ID,
		ProviderID: "x",
		Status:     connectadmin.EntitlementAllowed,
		QuotaJSON:  quota,
		Reason:     "internal beta",
		GrantedBy:  adminUser.ID,
	}); err != nil {
		t.Fatalf("UpsertUserEntitlement: %v", err)
	}
	allowed, err := store.UserAllowed(ctx, targetUser.ID, "x")
	if err != nil {
		t.Fatalf("UserAllowed: %v", err)
	}
	if !allowed {
		t.Fatal("UserAllowed = false, want true")
	}

	if err := store.AppendAuditEvent(ctx, connectadmin.AuditEvent{
		ActorUserID:  adminUser.ID,
		Action:       "entitlement.grant",
		ProviderID:   "x",
		TargetUserID: targetUser.ID,
		After:        map[string]any{"status": "allowed"},
	}); err != nil {
		t.Fatalf("AppendAuditEvent: %v", err)
	}
	events, err := store.ListAuditEvents(ctx, 10)
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(events) != 1 || events[0].Action != "entitlement.grant" {
		t.Fatalf("events = %#v", events)
	}
	if b, _ := json.Marshal(events[0]); string(b) == "" {
		t.Fatal("audit event should be JSON encodable")
	}
}
```

Add this setup helper to `apps/portal/internal/connectadmin/store_integration_test.go` below the test:

```go
func setupTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://kittypaw:kittypaw@localhost:5432/kittypaw_api_test?sslmode=disable"
	}
	if !strings.Contains(dbURL, "_test") {
		t.Fatalf("DATABASE_URL must point at a test DB (must contain \"_test\"); got %q", dbURL)
	}

	m, err := migrate.New("file://../../migrations", "pgx5://"+stripScheme(dbURL))
	if err != nil {
		t.Fatalf("migrate new: %v", err)
	}
	if err := m.Drop(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate drop: %v", err)
	}
	_, _ = m.Close()

	m, err = migrate.New("file://../../migrations", "pgx5://"+stripScheme(dbURL))
	if err != nil {
		t.Fatalf("migrate new post-drop: %v", err)
	}
	if err := m.Up(); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	pool, err := model.NewPool(context.Background(), dbURL)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

func stripScheme(raw string) string {
	for i, c := range raw {
		if c == ':' && i > 0 {
			return raw[i+3:]
		}
	}
	return raw
}
```

Add these imports to the test file:

```go
import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kittypaw-app/kittyportal/internal/connectadmin"
	"github.com/kittypaw-app/kittyportal/internal/model"
)
```

- [ ] **Step 3: Run the failing store test**

Run:

```bash
go test -tags=integration ./apps/portal/internal/connectadmin -run TestStorePolicyEntitlementAndAudit -count=1
```

Expected: package or store types missing.

- [ ] **Step 4: Implement types**

Create `apps/portal/internal/connectadmin/types.go`:

```go
package connectadmin

import "time"

const (
	DefaultEntitlementAllow = "allow"
	DefaultEntitlementDeny  = "deny"

	VerificationUnknown       = "unknown"
	VerificationNotApplicable = "not_applicable"
	VerificationTesting       = "testing"
	VerificationSubmitted     = "submitted"
	VerificationVerified      = "verified"
	VerificationBlocked       = "blocked"

	CostModeNone           = "none"
	CostModeExternalPolicy = "external_policy"
	CostModeKittyPaid      = "kitty_paid"

	EntitlementAllowed = "allowed"
	EntitlementBlocked = "blocked"
	EntitlementRevoked = "revoked"
)

type ProviderPolicy struct {
	ProviderID         string
	Enabled            bool
	DefaultEntitlement string
	RequestedScopes    []string
	VerificationStatus string
	CostMode           string
	Notes              string
	UpdatedBy          string
	UpdatedAt          time.Time
}

type UserEntitlement struct {
	ID         string
	UserID     string
	ProviderID string
	Status     string
	QuotaJSON  map[string]any
	Reason     string
	GrantedBy  string
	GrantedAt  time.Time
	RevokedAt  *time.Time
}

type AuditEvent struct {
	ID           string
	ActorUserID  string
	Action       string
	ProviderID   string
	TargetUserID string
	Before       map[string]any
	After        map[string]any
	CreatedAt    time.Time
}
```

- [ ] **Step 5: Implement store**

Create `apps/portal/internal/connectadmin/store.go` with these public methods:

```go
package connectadmin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store interface {
	ListProviderPolicies(context.Context) ([]ProviderPolicy, error)
	ListAuditEvents(context.Context, int) ([]AuditEvent, error)
	UpsertProviderPolicy(context.Context, ProviderPolicy) error
	GetProviderPolicy(context.Context, string) (ProviderPolicy, error)
	UpsertUserEntitlement(context.Context, UserEntitlement) error
	UserAllowed(context.Context, string, string) (bool, error)
	AppendAuditEvent(context.Context, AuditEvent) error
	EnsureDefaultPolicies(context.Context, ProviderRegistry) error
}

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
		VALUES ($1, $2, $3, $4::jsonb, $5, nullif($6, '')::uuid, now(), null)
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
			return false, nil
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
		var e AuditEvent
		var beforeRaw, afterRaw []byte
		if err := rows.Scan(&e.ID, &e.ActorUserID, &e.Action, &e.ProviderID, &e.TargetUserID, &beforeRaw, &afterRaw, &e.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(beforeRaw, &e.Before)
		_ = json.Unmarshal(afterRaw, &e.After)
		out = append(out, e)
	}
	return out, rows.Err()
}
```

- [ ] **Step 6: Verify store integration test**

Run:

```bash
go test -tags=integration ./apps/portal/internal/connectadmin -run TestStorePolicyEntitlementAndAudit -count=1
```

Expected: PASS with a local test Postgres configured through the existing portal convention.

- [ ] **Step 7: Commit Task 2**

Run:

```bash
git add apps/portal/migrations/009_create_connect_admin.* apps/portal/internal/connectadmin
git commit -m "feat(portal): add connect admin persistence"
```

---

## Task 3: Provider Registry And Policy Seeding

**Files:**

- Create: `apps/portal/internal/connectadmin/provider_registry.go`
- Create: `apps/portal/internal/connectadmin/provider_registry_test.go`
- Modify: `apps/portal/internal/connectadmin/store.go`

- [ ] **Step 1: Write provider registry tests**

Create `apps/portal/internal/connectadmin/provider_registry_test.go`:

```go
package connectadmin

import "testing"

func TestDefaultProviderRegistry(t *testing.T) {
	registry := DefaultProviderRegistry(ProviderRegistryConfig{
		GmailConfigured: true,
		XConfigured:     true,
	})

	gmail, ok := registry.Provider("gmail")
	if !ok {
		t.Fatal("gmail provider missing")
	}
	if gmail.CostBearing {
		t.Fatal("gmail should not be cost-bearing")
	}
	if gmail.DefaultPolicy.DefaultEntitlement != DefaultEntitlementAllow {
		t.Fatalf("gmail default entitlement = %q", gmail.DefaultPolicy.DefaultEntitlement)
	}

	x, ok := registry.Provider("x")
	if !ok {
		t.Fatal("x provider missing")
	}
	if !x.CostBearing {
		t.Fatal("x should be cost-bearing")
	}
	if x.DefaultPolicy.DefaultEntitlement != DefaultEntitlementDeny {
		t.Fatalf("x default entitlement = %q", x.DefaultPolicy.DefaultEntitlement)
	}
	if x.DefaultPolicy.CostMode != CostModeKittyPaid {
		t.Fatalf("x cost mode = %q", x.DefaultPolicy.CostMode)
	}
}
```

- [ ] **Step 2: Run failing registry test**

Run:

```bash
go test ./apps/portal/internal/connectadmin -run TestDefaultProviderRegistry -count=1
```

Expected: registry types missing.

- [ ] **Step 3: Implement registry**

Create `apps/portal/internal/connectadmin/provider_registry.go`:

```go
package connectadmin

import "github.com/kittypaw-app/kittyportal/internal/connect"

type ProviderRegistryConfig struct {
	GmailConfigured bool
	XConfigured     bool
}

type ProviderInfo struct {
	ID            string
	DisplayName   string
	Configured    bool
	Scopes        []string
	WriteCapable  bool
	CostBearing   bool
	DocsURL       string
	DefaultPolicy ProviderPolicy
}

type ProviderRegistry struct {
	providers map[string]ProviderInfo
	order     []string
}

func DefaultProviderRegistry(cfg ProviderRegistryConfig) ProviderRegistry {
	providers := []ProviderInfo{
		{
			ID:           connect.GmailProviderID,
			DisplayName:  "Gmail",
			Configured:   cfg.GmailConfigured,
			Scopes:       []string{"openid", "email", "profile", "https://www.googleapis.com/auth/gmail.readonly"},
			WriteCapable: false,
			CostBearing:  false,
			DocsURL:      "https://developers.google.com/workspace/gmail/api/auth/scopes",
			DefaultPolicy: ProviderPolicy{
				ProviderID:          connect.GmailProviderID,
				Enabled:             true,
				DefaultEntitlement:  DefaultEntitlementAllow,
				RequestedScopes:     []string{"openid", "email", "profile", "https://www.googleapis.com/auth/gmail.readonly"},
				VerificationStatus:  VerificationTesting,
				CostMode:            CostModeNone,
			},
		},
		{
			ID:           connect.XProviderID,
			DisplayName:  "X",
			Configured:   cfg.XConfigured,
			Scopes:       []string{"tweet.read", "users.read", "offline.access"},
			WriteCapable: false,
			CostBearing:  true,
			DocsURL:      "https://docs.x.com/x-api/fundamentals/post-cap",
			DefaultPolicy: ProviderPolicy{
				ProviderID:          connect.XProviderID,
				Enabled:             true,
				DefaultEntitlement:  DefaultEntitlementDeny,
				RequestedScopes:     []string{"tweet.read", "users.read", "offline.access"},
				VerificationStatus:  VerificationNotApplicable,
				CostMode:            CostModeKittyPaid,
			},
		},
	}
	out := ProviderRegistry{
		providers: make(map[string]ProviderInfo, len(providers)),
		order:     make([]string, 0, len(providers)),
	}
	for _, p := range providers {
		out.providers[p.ID] = p
		out.order = append(out.order, p.ID)
	}
	return out
}

func (r ProviderRegistry) Provider(id string) (ProviderInfo, bool) {
	p, ok := r.providers[id]
	return p, ok
}

func (r ProviderRegistry) List() []ProviderInfo {
	out := make([]ProviderInfo, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.providers[id])
	}
	return out
}
```

- [ ] **Step 4: Add seed helper to store**

In `apps/portal/internal/connectadmin/store.go`, add:

```go
func (s *PostgresStore) EnsureDefaultPolicies(ctx context.Context, registry ProviderRegistry) error {
	for _, provider := range registry.List() {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM connect_provider_policies WHERE provider_id = $1)`, provider.ID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		if err := s.UpsertProviderPolicy(ctx, provider.DefaultPolicy); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 5: Verify registry tests**

Run:

```bash
go test ./apps/portal/internal/connectadmin -run TestDefaultProviderRegistry -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit Task 3**

Run:

```bash
git add apps/portal/internal/connectadmin
git commit -m "feat(portal): add connect provider registry"
```

---

## Task 4: Admin Handler And HTML Views

**Files:**

- Create: `apps/portal/internal/connectadmin/handler.go`
- Create: `apps/portal/internal/connectadmin/handler_test.go`

- [ ] **Step 1: Write handler tests with a fake store**

Create `apps/portal/internal/connectadmin/handler_test.go` with tests:

```go
package connectadmin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kittypaw-app/kittyportal/internal/auth"
	"github.com/kittypaw-app/kittyportal/internal/model"
)

func TestHandlerHomeShowsProvidersWithoutSecrets(t *testing.T) {
	h := NewHandler(HandlerOptions{
		Registry: DefaultProviderRegistry(ProviderRegistryConfig{GmailConfigured: true, XConfigured: true}),
		Store: &fakeStore{
			policies: []ProviderPolicy{
				{ProviderID: "gmail", Enabled: true, DefaultEntitlement: DefaultEntitlementAllow, VerificationStatus: VerificationTesting, CostMode: CostModeNone},
				{ProviderID: "x", Enabled: true, DefaultEntitlement: DefaultEntitlementDeny, VerificationStatus: VerificationNotApplicable, CostMode: CostModeKittyPaid},
			},
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/admin/connect", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: "admin-1", Email: "admin@example.com"}))
	w := httptest.NewRecorder()

	h.HandleHome()(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{"KittyPaw Connect Admin", "Gmail", "X", "kitty_paid", "deny"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
	if strings.Contains(strings.ToLower(body), "secret") || strings.Contains(strings.ToLower(body), "access_token") {
		t.Fatalf("admin home leaked secret-shaped text: %s", body)
	}
}
```

Add this fake store to the same test file:

```go
type fakeStore struct {
	policies     []ProviderPolicy
	entitlements []UserEntitlement
	audit        []AuditEvent
}

func (s *fakeStore) ListProviderPolicies(context.Context) ([]ProviderPolicy, error) {
	return s.policies, nil
}

func (s *fakeStore) ListAuditEvents(context.Context, int) ([]AuditEvent, error) {
	return s.audit, nil
}

func (s *fakeStore) UpsertUserEntitlement(_ context.Context, e UserEntitlement) error {
	s.entitlements = append(s.entitlements, e)
	return nil
}

func (s *fakeStore) AppendAuditEvent(_ context.Context, e AuditEvent) error {
	s.audit = append(s.audit, e)
	return nil
}

func (s *fakeStore) UpsertProviderPolicy(_ context.Context, p ProviderPolicy) error {
	for i := range s.policies {
		if s.policies[i].ProviderID == p.ProviderID {
			s.policies[i] = p
			return nil
		}
	}
	s.policies = append(s.policies, p)
	return nil
}

func (s *fakeStore) GetProviderPolicy(_ context.Context, providerID string) (ProviderPolicy, error) {
	for _, p := range s.policies {
		if p.ProviderID == providerID {
			return p, nil
		}
	}
	return ProviderPolicy{}, pgx.ErrNoRows
}

func (s *fakeStore) UserAllowed(_ context.Context, userID, providerID string) (bool, error) {
	for _, e := range s.entitlements {
		if e.UserID == userID && e.ProviderID == providerID {
			return e.Status == EntitlementAllowed && e.RevokedAt == nil, nil
		}
	}
	return false, nil
}

func (s *fakeStore) EnsureDefaultPolicies(_ context.Context, registry ProviderRegistry) error {
	for _, provider := range registry.List() {
		_ = s.UpsertProviderPolicy(context.Background(), provider.DefaultPolicy)
	}
	return nil
}
```

Add `context` and `github.com/jackc/pgx/v5` to the test file imports.

- [ ] **Step 2: Run failing handler test**

Run:

```bash
go test ./apps/portal/internal/connectadmin -run TestHandlerHomeShowsProvidersWithoutSecrets -count=1
```

Expected: `NewHandler` missing.

- [ ] **Step 3: Implement handler interfaces and home view**

Create `apps/portal/internal/connectadmin/handler.go`:

```go
package connectadmin

import (
	"html"
	"net/http"
	"strings"
)

Define `HandlerOptions`, `Handler`, `NewHandler`, and `HandleHome`. `HandleHome` should render:

```html
<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>KittyPaw Connect Admin</title></head>
<body>
<main>
<h1>KittyPaw Connect Admin</h1>
<table>
<thead><tr><th>Provider</th><th>Configured</th><th>Enabled</th><th>Default</th><th>Verification</th><th>Cost</th><th>Scopes</th></tr></thead>
<tbody></tbody>
</table>
</main>
</body>
</html>
```

Render each provider row inside `<tbody>` with `fmt.Fprintf`, escape all provider names, notes, and scopes with `html.EscapeString`, and never render env values.

- [ ] **Step 4: Add grant/revoke handler tests**

In `handler_test.go`, add:

```go
func TestHandlerGrantEntitlementWritesAudit(t *testing.T) {
	store := &fakeStore{}
	h := NewHandler(HandlerOptions{
		Registry: DefaultProviderRegistry(ProviderRegistryConfig{XConfigured: true}),
		Store:    store,
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/connect/users/user-2/providers/x", strings.NewReader("status=allowed&reason=internal+beta&monthly_post_reads=100"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: "admin-1", Email: "admin@example.com"}))
	w := httptest.NewRecorder()

	h.HandleUserProviderUpdate("user-2", "x")(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	if len(store.entitlements) != 1 || store.entitlements[0].Status != EntitlementAllowed {
		t.Fatalf("entitlements = %#v", store.entitlements)
	}
	if len(store.audit) != 1 || store.audit[0].Action != "entitlement.update" {
		t.Fatalf("audit = %#v", store.audit)
	}
}
```

- [ ] **Step 5: Implement grant/revoke handler**

Add `HandleUserProviderUpdate(userID, providerID string) http.HandlerFunc`. It should:

1. require POST;
2. parse form;
3. accept only `allowed`, `blocked`, or `revoked`;
4. parse `monthly_post_reads` as an integer when present;
5. call `UpsertUserEntitlement`;
6. call `AppendAuditEvent`;
7. redirect to `/admin/connect/users`.

Use the `Store` interface from `store.go`; do not define a second handler-local store interface.

- [ ] **Step 6: Verify handler tests**

Run:

```bash
go test ./apps/portal/internal/connectadmin -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit Task 4**

Run:

```bash
git add apps/portal/internal/connectadmin
git commit -m "feat(portal): add connect admin handlers"
```

---

## Task 5: Portal Router Wiring And Host Boundary Tests

**Files:**

- Modify: `apps/portal/cmd/server/main.go`
- Modify: `apps/portal/cmd/server/main_test.go`

- [ ] **Step 1: Write router tests first**

Add tests to `apps/portal/cmd/server/main_test.go`:

```go
type serverUserStore struct {
	users map[string]*model.User
}

func (s *serverUserStore) CreateOrUpdate(_ context.Context, provider, providerID, email, name, avatarURL string) (*model.User, error) {
	u := &model.User{
		ID:         "user-" + providerID,
		Provider:   provider,
		ProviderID: providerID,
		Email:      email,
		Name:       name,
		AvatarURL:  avatarURL,
	}
	if s.users == nil {
		s.users = make(map[string]*model.User)
	}
	s.users[u.ID] = u
	return u, nil
}

func (s *serverUserStore) FindByID(_ context.Context, id string) (*model.User, error) {
	u, ok := s.users[id]
	if !ok {
		return nil, model.ErrNotFound
	}
	return u, nil
}

type serverConnectAdminFakeStore struct {
	policies []connectadmin.ProviderPolicy
}

func (s *serverConnectAdminFakeStore) ListProviderPolicies(context.Context) ([]connectadmin.ProviderPolicy, error) {
	return s.policies, nil
}

func (s *serverConnectAdminFakeStore) ListAuditEvents(context.Context, int) ([]connectadmin.AuditEvent, error) {
	return nil, nil
}

func (s *serverConnectAdminFakeStore) UpsertProviderPolicy(_ context.Context, p connectadmin.ProviderPolicy) error {
	s.policies = append(s.policies, p)
	return nil
}

func (s *serverConnectAdminFakeStore) GetProviderPolicy(_ context.Context, providerID string) (connectadmin.ProviderPolicy, error) {
	for _, p := range s.policies {
		if p.ProviderID == providerID {
			return p, nil
		}
	}
	return connectadmin.ProviderPolicy{}, pgx.ErrNoRows
}

func (s *serverConnectAdminFakeStore) UpsertUserEntitlement(context.Context, connectadmin.UserEntitlement) error {
	return nil
}

func (s *serverConnectAdminFakeStore) UserAllowed(context.Context, string, string) (bool, error) {
	return false, nil
}

func (s *serverConnectAdminFakeStore) AppendAuditEvent(context.Context, connectadmin.AuditEvent) error {
	return nil
}

func (s *serverConnectAdminFakeStore) EnsureDefaultPolicies(_ context.Context, registry connectadmin.ProviderRegistry) error {
	for _, provider := range registry.List() {
		s.policies = append(s.policies, provider.DefaultPolicy)
	}
	return nil
}

func TestConnectAdminOnlyServedOnPortalHost(t *testing.T) {
	cfg := config.LoadForTest()
	cfg.BaseURL = "https://portal.kittypaw.app"
	cfg.APIBaseURL = "https://api.kittypaw.app"
	cfg.ConnectBaseURL = "https://connect.kittypaw.app"
	cfg.PortalAdminEmails = []string{"admin@example.com"}
	users := &serverUserStore{users: make(map[string]*model.User)}
	store := &serverConnectAdminFakeStore{}
	r, cleanup := NewRouter(cfg, users, nil, nil, store)
	t.Cleanup(cleanup)

	req := httptest.NewRequest(http.MethodGet, "/admin/connect", nil)
	req.Host = "connect.kittypaw.app"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("connect host status = %d, want 404", w.Code)
	}
}

func TestConnectAdminAllowsConfiguredAdmin(t *testing.T) {
	cfg := config.LoadForTest()
	cfg.BaseURL = "https://portal.kittypaw.app"
	cfg.APIBaseURL = "https://api.kittypaw.app"
	cfg.ConnectBaseURL = "https://connect.kittypaw.app"
	cfg.PortalAdminEmails = []string{"admin@example.com"}
	users := &serverUserStore{users: map[string]*model.User{
		"user-admin": {ID: "user-admin", Provider: "google", ProviderID: "admin", Email: "admin@example.com", Name: "Admin"},
	}}
	store := &serverConnectAdminFakeStore{policies: []connectadmin.ProviderPolicy{
		{ProviderID: "x", Enabled: true, DefaultEntitlement: connectadmin.DefaultEntitlementDeny, VerificationStatus: connectadmin.VerificationNotApplicable, CostMode: connectadmin.CostModeKittyPaid},
	}}
	r, cleanup := NewRouter(cfg, users, nil, nil, store)
	t.Cleanup(cleanup)

	token := testfixture.IssueTestJWT(t, cfg.JWTPrivateKey, cfg.JWTKID, "user-admin", 15*time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/admin/connect", nil)
	req.Host = "portal.kittypaw.app"
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}

func TestConnectAdminRejectsAuthenticatedNonAdmin(t *testing.T) {
	cfg := config.LoadForTest()
	cfg.BaseURL = "https://portal.kittypaw.app"
	cfg.APIBaseURL = "https://api.kittypaw.app"
	cfg.ConnectBaseURL = "https://connect.kittypaw.app"
	cfg.PortalAdminEmails = []string{"admin@example.com"}
	users := &serverUserStore{users: map[string]*model.User{
		"user-basic": {ID: "user-basic", Provider: "google", ProviderID: "basic", Email: "user@example.com", Name: "User"},
	}}
	r, cleanup := NewRouter(cfg, users, nil, nil, &serverConnectAdminFakeStore{})
	t.Cleanup(cleanup)

	token := testfixture.IssueTestJWT(t, cfg.JWTPrivateKey, cfg.JWTKID, "user-basic", 15*time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/admin/connect", nil)
	req.Host = "portal.kittypaw.app"
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
}
```

Add these imports to `main_test.go`: `context`, `time`, `github.com/jackc/pgx/v5`, `github.com/kittypaw-app/kittyportal/internal/auth/testfixture`, `github.com/kittypaw-app/kittyportal/internal/connectadmin`, and `github.com/kittypaw-app/kittyportal/internal/model`.

- [ ] **Step 2: Run failing router tests**

Run:

```bash
go test ./apps/portal/cmd/server -run 'TestConnectAdmin' -count=1
```

Expected: route missing or wrong status.

- [ ] **Step 3: Wire admin routes**

In `apps/portal/cmd/server/main.go`:

1. Import `github.com/kittypaw-app/kittyportal/internal/admin` and `github.com/kittypaw-app/kittyportal/internal/connectadmin`.
2. Construct the registry:

```go
connectRegistry := connectadmin.DefaultProviderRegistry(connectadmin.ProviderRegistryConfig{
	GmailConfigured: cfg.ConnectGoogleClientID != "" && cfg.ConnectGoogleClientSecret != "",
	XConfigured:     cfg.ConnectXClientID != "" && cfg.ConnectXClientSecret != "",
})
```

3. Change `NewRouter` signature to accept the Connect admin store:

```go
func NewRouter(
	cfg *config.Config,
	userStore model.UserStore,
	refreshStore model.RefreshTokenStore,
	deviceStore model.DeviceStore,
	connectAdminStore connectadmin.Store,
) (*chi.Mux, func()) {
```

4. In `main()`, create and seed the store after the Postgres pool is ready:

```go
connectAdminStore := connectadmin.NewStore(pool)
connectRegistry := connectadmin.DefaultProviderRegistry(connectadmin.ProviderRegistryConfig{
	GmailConfigured: cfg.ConnectGoogleClientID != "" && cfg.ConnectGoogleClientSecret != "",
	XConfigured:     cfg.ConnectXClientID != "" && cfg.ConnectXClientSecret != "",
})
if err := connectAdminStore.EnsureDefaultPolicies(ctx, connectRegistry); err != nil {
	log.Fatalf("seed connect policies: %v", err)
}
router, cleanup := NewRouter(cfg, userStore, refreshStore, deviceStore, connectAdminStore)
```

5. In every existing test call to `NewRouter`, pass `nil` as the fifth argument unless the test is about Connect admin:

```go
r, cleanup := NewRouter(cfg, userStore, refreshStore, deviceStore, nil)
```

6. For Connect admin router tests, define a `serverConnectAdminFakeStore` type in `apps/portal/cmd/server/main_test.go` that implements `connectadmin.Store` with in-memory slices and pass it as the fifth `NewRouter` argument.

7. Add portal-host admin route group inside the identity host group:

```go
r.Route("/admin/connect", func(r chi.Router) {
	r.Use(authMW)
	r.Use(admin.Middleware(cfg.PortalAdminEmails))
	r.Get("/", connectAdminHandler.HandleHome())
	r.Get("", connectAdminHandler.HandleHome())
})
```

Do not put admin routes inside the connect host group.

- [ ] **Step 4: Verify router tests**

Run:

```bash
go test ./apps/portal/cmd/server -run 'TestConnectAdmin|TestConnectRoutesOnlyServedOnConnectHost|TestIdentityRoutesNotServedOnConnectHost' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit Task 5**

Run:

```bash
git add apps/portal/cmd/server/main.go apps/portal/cmd/server/main_test.go
git commit -m "feat(portal): wire connect admin routes"
```

---

## Task 6: X Preauth Session Store And Gated Login

**Files:**

- Create: `apps/portal/internal/connect/preauth_store.go`
- Create: `apps/portal/internal/connect/preauth_store_test.go`
- Modify: `apps/portal/internal/connect/handler.go`
- Modify: `apps/portal/internal/connect/handler_test.go`

- [ ] **Step 1: Write preauth store tests**

Create `apps/portal/internal/connect/preauth_store_test.go`:

```go
package connect

import (
	"testing"
	"time"
)

func TestPreauthStoreConsumesOnce(t *testing.T) {
	now := time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)
	store := NewPreauthStore(PreauthStoreOptions{TTL: time.Minute, Now: func() time.Time { return now }})
	code, err := store.Create(PreauthSession{UserID: "user-1", Provider: XProviderID, Mode: "code"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	session, err := store.Consume(code)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if session.UserID != "user-1" || session.Provider != XProviderID {
		t.Fatalf("session = %#v", session)
	}
	if _, err := store.Consume(code); err == nil {
		t.Fatal("second consume succeeded")
	}
}

func TestPreauthStoreExpires(t *testing.T) {
	now := time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)
	store := NewPreauthStore(PreauthStoreOptions{TTL: time.Minute, Now: func() time.Time { return now }})
	code, err := store.Create(PreauthSession{UserID: "user-1", Provider: XProviderID, Mode: "code"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := store.Consume(code); err == nil {
		t.Fatal("expired consume succeeded")
	}
}
```

- [ ] **Step 2: Run failing preauth tests**

Run:

```bash
go test ./apps/portal/internal/connect -run TestPreauthStore -count=1
```

Expected: preauth store missing.

- [ ] **Step 3: Implement preauth store**

Create `apps/portal/internal/connect/preauth_store.go` with the same locking and random-code pattern as `CodeStore`, but store:

```go
type PreauthSession struct {
	UserID   string
	Provider string
	Mode     string
	Port     string
}
```

Expose:

```go
func NewPreauthStore(opts PreauthStoreOptions) *PreauthStore
func (s *PreauthStore) Create(session PreauthSession) (string, error)
func (s *PreauthStore) Consume(code string) (PreauthSession, error)
```

Use a 5 minute default TTL and consume-once semantics.

- [ ] **Step 4: Add X session route tests**

In `apps/portal/internal/connect/handler_test.go`, add:

```go
func TestHandlerXSessionRequiresEntitlement(t *testing.T) {
	h, _, _, _ := testHandler(t)
	h.PreauthStore = NewPreauthStore(PreauthStoreOptions{TTL: time.Minute})
	h.Entitlements = fakeEntitlementChecker{allowed: false}

	req := httptest.NewRequest(http.MethodPost, "/connect/x/sessions", strings.NewReader(`{"mode":"code"}`))
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: "user-1", Email: "u@example.com"}))
	w := httptest.NewRecorder()
	h.HandleXSession()(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
}
```

Add a second test with `allowed: true`, expecting JSON containing `login_url` with `/connect/x/login?session=`.

- [ ] **Step 5: Implement X session handler**

In `apps/portal/internal/connect/handler.go`:

1. Add to `Handler`:

```go
PreauthStore *PreauthStore
Entitlements EntitlementChecker
```

2. Add interface:

```go
type EntitlementChecker interface {
	UserAllowed(context.Context, string, string) (bool, error)
}
```

3. Implement `HandleXSession()`. It must:

- require authenticated `auth.UserFromContext`;
- parse JSON `{ "mode": "code" | "http", "port": "12345" }`;
- check `Entitlements.UserAllowed(ctx, user.ID, XProviderID)`;
- fail closed with 403 when checker is nil, errors, or returns false;
- create a preauth session;
- return `{ "login_url": "https://connect.kittypaw.app/connect/x/login?session=session-code" }` with the generated session code.

4. Change `HandleXLogin()` to consume `session` and then call `handleLogin`. Direct `/connect/x/login?mode=code` should return 401 or 403.

5. Keep `HandleGmailLogin()` unchanged.

- [ ] **Step 6: Verify connect tests**

Run:

```bash
go test ./apps/portal/internal/connect -run 'TestPreauthStore|TestHandlerXSession|TestHandlerXLogin|TestHandlerGmailLogin' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit Task 6**

Run:

```bash
git add apps/portal/internal/connect
git commit -m "feat(portal): gate x connect with preauth sessions"
```

---

## Task 7: CLI X Connect Uses Portal Login Token

**Files:**

- Modify: `apps/kittypaw/cli/cmd_connect.go`
- Modify: `apps/kittypaw/cli/cmd_connect_test.go`

- [ ] **Step 1: Write CLI test for X session creation**

Add to `apps/kittypaw/cli/cmd_connect_test.go`:

```go
func TestConnectXCreatesPreauthSessionWithPortalToken(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/discovery":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"connect_base_url":%q}`, ts.URL)
		case "/connect/x/sessions":
			gotAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"login_url":%q}`, ts.URL+"/connect/x/login?session=s-1")
		case "/connect/cli/exchange":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"provider":"x","access_token":"x-access","refresh_token":"x-refresh","token_type":"bearer","expires_in":7200,"scope":"tweet.read users.read offline.access","username":"jaypark"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	secrets := testSecretsStore(t)
	apiMgr := core.NewAPITokenManager("", secrets)
	if err := apiMgr.SaveTokens(ts.URL, "access-token", "refresh-token"); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}
	serviceMgr := core.NewServiceTokenManager(secrets)

	loginURL, err := createConnectSession("x", ts.URL, apiMgr, "code", 0)
	if err != nil {
		t.Fatalf("createConnectSession: %v", err)
	}
	if !strings.Contains(loginURL, "session=s-1") {
		t.Fatalf("loginURL = %q", loginURL)
	}
	if gotAuth != "Bearer access-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if err := exchangeConnectCode(ts.URL, "code-1", serviceMgr); err != nil {
		t.Fatalf("exchangeConnectCode: %v", err)
	}
}
```

If the closure needs `ts` before assignment, declare `var ts *httptest.Server` before assignment.

- [ ] **Step 2: Run failing CLI test**

Run:

```bash
go test ./apps/kittypaw/cli -run TestConnectXCreatesPreauthSessionWithPortalToken -count=1
```

Expected: `createConnectSession` missing.

- [ ] **Step 3: Implement CLI session helper**

In `apps/kittypaw/cli/cmd_connect.go`, add:

```go
func createConnectSession(provider, apiURL string, apiMgr *core.APITokenManager, mode string, port int) (string, error) {
	_ = applyDiscovery(apiURL, apiMgr)
	connectBaseURL := apiMgr.ResolveConnectBaseURL(apiURL)
	accessToken, err := apiMgr.LoadAccessToken(apiURL)
	if err != nil {
		return "", err
	}
	if accessToken == "" {
		return "", fmt.Errorf("not logged in — run: kittypaw login --api-url %s", apiURL)
	}
	payload := map[string]any{"mode": mode}
	if mode == "http" {
		payload["port"] = fmt.Sprintf("%d", port)
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(connectBaseURL, "/")+"/connect/"+provider+"/sessions", strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := connectHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("connect session request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("connect session failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var result struct {
		LoginURL string `json:"login_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode connect session: %w", err)
	}
	if result.LoginURL == "" {
		return "", fmt.Errorf("connect session response missing login_url")
	}
	return result.LoginURL, nil
}
```

Change only X paths to use `createConnectSession`. Gmail continues to use `connectServiceLoginURL`.

- [ ] **Step 4: Verify CLI connect tests**

Run:

```bash
go test ./apps/kittypaw/cli -run 'TestConnectX|TestConnectGmail|TestConnectExchange' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit Task 7**

Run:

```bash
git add apps/kittypaw/cli/cmd_connect.go apps/kittypaw/cli/cmd_connect_test.go
git commit -m "feat(cli): require portal login for x connect"
```

---

## Task 8: Docs And Deployment Notes

**Files:**

- Modify: `apps/portal/DEPLOY.md`
- Modify: `docs/operations/connect-x-oauth.md`
- Modify: `docs/operations/connect-gmail-oauth.md`

- [ ] **Step 1: Update portal deploy docs**

In `apps/portal/DEPLOY.md`, add:

```text
PORTAL_ADMIN_EMAILS=admin@example.com
```

State that `/admin/connect` is served only on `portal.kittypaw.app`, requires
normal portal login, and uses the email allowlist. State that `connect.kittypaw.app`
must not serve admin routes.

- [ ] **Step 2: Update X operations docs**

In `docs/operations/connect-x-oauth.md`, add:

```markdown
## Connect Admin

X is cost-bearing for KittyPaw. Do not open X Connect to all users.

Before a user can run `kittypaw connect x`, an admin must:

1. sign in to `https://portal.kittypaw.app`;
2. open `/admin/connect`;
3. grant X entitlement to that user;
4. record a quota note such as `monthly_post_reads=100`.

Phase 0 gates X connection. It does not centrally meter local X API calls after
the token is stored in the local KittyPaw account. Public X access requires a
future server-brokered usage path.
```

- [ ] **Step 3: Update Gmail operations docs**

In `docs/operations/connect-gmail-oauth.md`, add that Gmail remains local-token
and that the admin surface must not be used to inspect mailbox contents.

- [ ] **Step 4: Commit Task 8**

Run:

```bash
git add apps/portal/DEPLOY.md docs/operations/connect-x-oauth.md docs/operations/connect-gmail-oauth.md
git commit -m "docs(connect): document admin entitlement policy"
```

---

## Task 9: Full Verification

**Files:**

- No new files.

- [ ] **Step 1: Run focused portal tests**

Run:

```bash
go test ./apps/portal/internal/admin ./apps/portal/internal/connect ./apps/portal/internal/connectadmin ./apps/portal/cmd/server -count=1
```

Expected: PASS.

- [ ] **Step 2: Run focused CLI tests**

Run:

```bash
go test ./apps/kittypaw/cli -run 'TestConnect' -count=1
```

Expected: PASS.

- [ ] **Step 3: Run portal integration tests**

Run:

```bash
go test -tags=integration ./apps/portal/internal/model ./apps/portal/internal/connectadmin -count=1
```

Expected: PASS when local test Postgres is available. If Postgres is not running, start the repo's local compose stack before retrying.

- [ ] **Step 4: Run complete app test slices**

Run:

```bash
go test ./apps/portal/... ./apps/kittypaw/... -count=1
```

Expected: PASS.

- [ ] **Step 5: Inspect git diff for token leakage**

Run:

```bash
git diff --cached --name-only
git diff HEAD -- apps/portal apps/kittypaw docs | rg -n "access_token|refresh_token|client_secret|bearer token|GOCSPX|AAAAAAAA" || true
```

Expected: no real secrets in tracked files. Literal field names in source code are acceptable; concrete credential values are not.

- [ ] **Step 6: Final commit if verification changed docs or tests**

If verification required doc/test corrections, commit them:

```bash
git add apps/portal apps/kittypaw docs
git commit -m "test(connect): verify admin entitlement flow"
```

If no files changed, do not create an empty commit.

---

## Execution Notes

- Keep commits task-sized.
- Do not push until the user asks.
- Do not add X write scopes.
- Do not store Gmail/X provider tokens in portal.
- Do not expose user mail, X timeline content, DMs, bookmarks, or token values in admin UI.
- If a task reveals that X usage cannot be safely gated without a larger broker change, stop after the entitlement/admin work and record the broker requirement in docs.
