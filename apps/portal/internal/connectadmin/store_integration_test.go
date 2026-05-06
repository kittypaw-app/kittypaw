//go:build integration

package connectadmin_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
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
		ProviderID:         "x",
		Enabled:            true,
		DefaultEntitlement: connectadmin.DefaultEntitlementDeny,
		RequestedScopes:    []string{"tweet.read", "users.read", "offline.access"},
		VerificationStatus: connectadmin.VerificationNotApplicable,
		CostMode:           connectadmin.CostModeKittyPaid,
		Notes:              "staff only",
		UpdatedBy:          adminUser.ID,
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
	var monthlyPostReads float64
	err = pool.QueryRow(ctx, `
		SELECT (quota_json->>'monthly_post_reads')::float8
		FROM connect_user_entitlements
		WHERE user_id = $1 AND provider_id = $2
	`, targetUser.ID, "x").Scan(&monthlyPostReads)
	if err != nil {
		t.Fatalf("query quota_json: %v", err)
	}
	if monthlyPostReads != 100 {
		t.Fatalf("monthly_post_reads = %v, want 100", monthlyPostReads)
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
	if events[0].ProviderID != "x" || events[0].TargetUserID != targetUser.ID {
		t.Fatalf("event target = provider %q user %q, want provider %q user %q", events[0].ProviderID, events[0].TargetUserID, "x", targetUser.ID)
	}
	if events[0].After["status"] != "allowed" {
		t.Fatalf("event after status = %#v, want allowed", events[0].After["status"])
	}
	if b, _ := json.Marshal(events[0]); string(b) == "" {
		t.Fatal("audit event should be JSON encodable")
	}
}

func TestNilRequestedScopesStoresEmptyArray(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()

	store := connectadmin.NewStore(pool)
	if err := store.UpsertProviderPolicy(ctx, connectadmin.ProviderPolicy{
		ProviderID:         "nil-scopes",
		Enabled:            true,
		DefaultEntitlement: connectadmin.DefaultEntitlementDeny,
		RequestedScopes:    nil,
		VerificationStatus: connectadmin.VerificationNotApplicable,
		CostMode:           connectadmin.CostModeNone,
	}); err != nil {
		t.Fatalf("UpsertProviderPolicy nil scopes: %v", err)
	}

	got, err := store.GetProviderPolicy(ctx, "nil-scopes")
	if err != nil {
		t.Fatalf("GetProviderPolicy nil scopes: %v", err)
	}
	if len(got.RequestedScopes) != 0 {
		t.Fatalf("requested scopes = %#v, want empty", got.RequestedScopes)
	}
}

func TestUserAllowedFallsBackToProviderDefault(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()
	users := model.NewUserStore(pool)
	targetUser, err := users.CreateOrUpdate(ctx, "google", "target-defaults", "target-defaults@example.com", "Target", "")
	if err != nil {
		t.Fatalf("seed target: %v", err)
	}

	store := connectadmin.NewStore(pool)
	if err := store.UpsertProviderPolicy(ctx, connectadmin.ProviderPolicy{
		ProviderID:         "default-allow",
		Enabled:            true,
		DefaultEntitlement: connectadmin.DefaultEntitlementAllow,
		RequestedScopes:    []string{},
		VerificationStatus: connectadmin.VerificationNotApplicable,
		CostMode:           connectadmin.CostModeNone,
	}); err != nil {
		t.Fatalf("upsert default allow policy: %v", err)
	}
	if err := store.UpsertProviderPolicy(ctx, connectadmin.ProviderPolicy{
		ProviderID:         "default-deny",
		Enabled:            true,
		DefaultEntitlement: connectadmin.DefaultEntitlementDeny,
		RequestedScopes:    []string{},
		VerificationStatus: connectadmin.VerificationNotApplicable,
		CostMode:           connectadmin.CostModeNone,
	}); err != nil {
		t.Fatalf("upsert default deny policy: %v", err)
	}
	if err := store.UpsertProviderPolicy(ctx, connectadmin.ProviderPolicy{
		ProviderID:         "disabled-default-allow",
		Enabled:            false,
		DefaultEntitlement: connectadmin.DefaultEntitlementAllow,
		RequestedScopes:    []string{},
		VerificationStatus: connectadmin.VerificationNotApplicable,
		CostMode:           connectadmin.CostModeNone,
	}); err != nil {
		t.Fatalf("upsert disabled default allow policy: %v", err)
	}

	allowed, err := store.UserAllowed(ctx, targetUser.ID, "default-allow")
	if err != nil {
		t.Fatalf("UserAllowed default allow: %v", err)
	}
	if !allowed {
		t.Fatal("UserAllowed default allow = false, want true")
	}

	allowed, err = store.UserAllowed(ctx, targetUser.ID, "default-deny")
	if err != nil {
		t.Fatalf("UserAllowed default deny: %v", err)
	}
	if allowed {
		t.Fatal("UserAllowed default deny = true, want false")
	}

	allowed, err = store.UserAllowed(ctx, targetUser.ID, "disabled-default-allow")
	if err != nil {
		t.Fatalf("UserAllowed disabled default allow: %v", err)
	}
	if allowed {
		t.Fatal("UserAllowed disabled default allow = true, want false")
	}

	allowed, err = store.UserAllowed(ctx, targetUser.ID, "missing-provider")
	if err != nil {
		t.Fatalf("UserAllowed missing provider: %v", err)
	}
	if allowed {
		t.Fatal("UserAllowed missing provider = true, want false")
	}
}

func TestExplicitBlockedEntitlementOverridesDefaultAllow(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()
	users := model.NewUserStore(pool)
	targetUser, err := users.CreateOrUpdate(ctx, "google", "target-blocked", "target-blocked@example.com", "Target", "")
	if err != nil {
		t.Fatalf("seed target: %v", err)
	}

	store := connectadmin.NewStore(pool)
	if err := store.UpsertProviderPolicy(ctx, connectadmin.ProviderPolicy{
		ProviderID:         "blocked-provider",
		Enabled:            true,
		DefaultEntitlement: connectadmin.DefaultEntitlementAllow,
		RequestedScopes:    []string{},
		VerificationStatus: connectadmin.VerificationNotApplicable,
		CostMode:           connectadmin.CostModeNone,
	}); err != nil {
		t.Fatalf("upsert policy: %v", err)
	}
	if err := store.UpsertUserEntitlement(ctx, connectadmin.UserEntitlement{
		UserID:     targetUser.ID,
		ProviderID: "blocked-provider",
		Status:     connectadmin.EntitlementBlocked,
		QuotaJSON:  map[string]any{},
		Reason:     "blocked override",
	}); err != nil {
		t.Fatalf("UpsertUserEntitlement blocked: %v", err)
	}

	allowed, err := store.UserAllowed(ctx, targetUser.ID, "blocked-provider")
	if err != nil {
		t.Fatalf("UserAllowed blocked: %v", err)
	}
	if allowed {
		t.Fatal("UserAllowed blocked = true, want false")
	}
}

func TestExplicitAllowedEntitlementRequiresEnabledProvider(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()
	users := model.NewUserStore(pool)
	targetUser, err := users.CreateOrUpdate(ctx, "google", "target-kill-switch", "target-kill-switch@example.com", "Target", "")
	if err != nil {
		t.Fatalf("seed target: %v", err)
	}

	store := connectadmin.NewStore(pool)
	if err := store.UpsertProviderPolicy(ctx, connectadmin.ProviderPolicy{
		ProviderID:         "disabled-provider",
		Enabled:            false,
		DefaultEntitlement: connectadmin.DefaultEntitlementAllow,
		RequestedScopes:    []string{},
		VerificationStatus: connectadmin.VerificationNotApplicable,
		CostMode:           connectadmin.CostModeNone,
	}); err != nil {
		t.Fatalf("upsert disabled policy: %v", err)
	}
	if err := store.UpsertUserEntitlement(ctx, connectadmin.UserEntitlement{
		UserID:     targetUser.ID,
		ProviderID: "disabled-provider",
		Status:     connectadmin.EntitlementAllowed,
		QuotaJSON:  map[string]any{},
		Reason:     "explicit allow",
	}); err != nil {
		t.Fatalf("upsert disabled provider entitlement: %v", err)
	}
	if err := store.UpsertUserEntitlement(ctx, connectadmin.UserEntitlement{
		UserID:     targetUser.ID,
		ProviderID: "missing-provider",
		Status:     connectadmin.EntitlementAllowed,
		QuotaJSON:  map[string]any{},
		Reason:     "explicit allow",
	}); err != nil {
		t.Fatalf("upsert missing provider entitlement: %v", err)
	}

	allowed, err := store.UserAllowed(ctx, targetUser.ID, "disabled-provider")
	if err != nil {
		t.Fatalf("UserAllowed disabled provider: %v", err)
	}
	if allowed {
		t.Fatal("UserAllowed disabled provider = true, want false")
	}

	allowed, err = store.UserAllowed(ctx, targetUser.ID, "missing-provider")
	if err != nil {
		t.Fatalf("UserAllowed missing provider: %v", err)
	}
	if allowed {
		t.Fatal("UserAllowed missing provider = true, want false")
	}
}

func TestNilQuotaStoresEmptyObject(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()
	users := model.NewUserStore(pool)
	adminUser, err := users.CreateOrUpdate(ctx, "google", "admin-nil-quota", "admin-nil-quota@example.com", "Admin", "")
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	targetUser, err := users.CreateOrUpdate(ctx, "google", "target-nil-quota", "target-nil-quota@example.com", "Target", "")
	if err != nil {
		t.Fatalf("seed target: %v", err)
	}

	store := connectadmin.NewStore(pool)
	if err := store.UpsertUserEntitlement(ctx, connectadmin.UserEntitlement{
		UserID:     targetUser.ID,
		ProviderID: "nil-quota-provider",
		Status:     connectadmin.EntitlementAllowed,
		Reason:     "no quota",
		GrantedBy:  adminUser.ID,
	}); err != nil {
		t.Fatalf("UpsertUserEntitlement nil quota: %v", err)
	}

	var quotaIsEmptyObject bool
	err = pool.QueryRow(ctx, `
		SELECT quota_json = '{}'::jsonb
		FROM connect_user_entitlements
		WHERE user_id = $1 AND provider_id = $2
	`, targetUser.ID, "nil-quota-provider").Scan(&quotaIsEmptyObject)
	if err != nil {
		t.Fatalf("query quota_json: %v", err)
	}
	if !quotaIsEmptyObject {
		t.Fatal("quota_json is not empty object")
	}
}

func TestRevokedEntitlementStoresRevokedAtOnFirstInsert(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()
	users := model.NewUserStore(pool)
	adminUser, err := users.CreateOrUpdate(ctx, "google", "admin-revoke", "admin-revoke@example.com", "Admin", "")
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	targetUser, err := users.CreateOrUpdate(ctx, "google", "target-revoke", "target-revoke@example.com", "Target", "")
	if err != nil {
		t.Fatalf("seed target: %v", err)
	}

	store := connectadmin.NewStore(pool)
	if err := store.UpsertProviderPolicy(ctx, connectadmin.ProviderPolicy{
		ProviderID:         "revoked-provider",
		Enabled:            true,
		DefaultEntitlement: connectadmin.DefaultEntitlementAllow,
		RequestedScopes:    []string{},
		VerificationStatus: connectadmin.VerificationNotApplicable,
		CostMode:           connectadmin.CostModeNone,
	}); err != nil {
		t.Fatalf("upsert policy: %v", err)
	}
	if err := store.UpsertUserEntitlement(ctx, connectadmin.UserEntitlement{
		UserID:     targetUser.ID,
		ProviderID: "revoked-provider",
		Status:     connectadmin.EntitlementRevoked,
		QuotaJSON:  map[string]any{},
		Reason:     "manual revoke",
		GrantedBy:  adminUser.ID,
	}); err != nil {
		t.Fatalf("UpsertUserEntitlement revoked: %v", err)
	}

	allowed, err := store.UserAllowed(ctx, targetUser.ID, "revoked-provider")
	if err != nil {
		t.Fatalf("UserAllowed revoked: %v", err)
	}
	if allowed {
		t.Fatal("UserAllowed revoked = true, want false")
	}

	var revokedAtSet bool
	err = pool.QueryRow(ctx, `
		SELECT revoked_at IS NOT NULL
		FROM connect_user_entitlements
		WHERE user_id = $1 AND provider_id = $2
	`, targetUser.ID, "revoked-provider").Scan(&revokedAtSet)
	if err != nil {
		t.Fatalf("query revoked_at: %v", err)
	}
	if !revokedAtSet {
		t.Fatal("revoked_at is null, want timestamp")
	}
}

func TestRepeatedRevokedEntitlementPreservesRevokedAt(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()
	users := model.NewUserStore(pool)
	adminUser, err := users.CreateOrUpdate(ctx, "google", "admin-repeat-revoke", "admin-repeat-revoke@example.com", "Admin", "")
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	targetUser, err := users.CreateOrUpdate(ctx, "google", "target-repeat-revoke", "target-repeat-revoke@example.com", "Target", "")
	if err != nil {
		t.Fatalf("seed target: %v", err)
	}

	store := connectadmin.NewStore(pool)
	entitlement := connectadmin.UserEntitlement{
		UserID:     targetUser.ID,
		ProviderID: "repeat-revoke-provider",
		Status:     connectadmin.EntitlementRevoked,
		QuotaJSON:  map[string]any{},
		Reason:     "manual revoke",
		GrantedBy:  adminUser.ID,
	}
	if err := store.UpsertUserEntitlement(ctx, entitlement); err != nil {
		t.Fatalf("initial revoked upsert: %v", err)
	}
	var firstRevokedAt time.Time
	err = pool.QueryRow(ctx, `
		SELECT revoked_at
		FROM connect_user_entitlements
		WHERE user_id = $1 AND provider_id = $2
	`, targetUser.ID, "repeat-revoke-provider").Scan(&firstRevokedAt)
	if err != nil {
		t.Fatalf("query first revoked_at: %v", err)
	}

	time.Sleep(10 * time.Millisecond)
	if err := store.UpsertUserEntitlement(ctx, entitlement); err != nil {
		t.Fatalf("repeated revoked upsert: %v", err)
	}
	var secondRevokedAt time.Time
	err = pool.QueryRow(ctx, `
		SELECT revoked_at
		FROM connect_user_entitlements
		WHERE user_id = $1 AND provider_id = $2
	`, targetUser.ID, "repeat-revoke-provider").Scan(&secondRevokedAt)
	if err != nil {
		t.Fatalf("query second revoked_at: %v", err)
	}
	if !secondRevokedAt.Equal(firstRevokedAt) {
		t.Fatalf("revoked_at changed from %s to %s", firstRevokedAt.Format(time.RFC3339Nano), secondRevokedAt.Format(time.RFC3339Nano))
	}
}

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
