//go:build integration

package proxy_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kittypaw-app/kittyapi/internal/model"
	"github.com/kittypaw-app/kittyapi/internal/proxy"
)

// Plan 12 — L1.D handler-layer integration tests.
// docs/specs/test-coverage-completion.md (γ Plan A) +
// .claude/plans/plan-12-l1d-l3-geo.md (full spec).
//
// Each test self-seeds places / alias_overrides rows under a
// `_p12_<scope>_` prefix and registers a t.Cleanup that DELETEs by prefix.
// pg_advisory_lock(12) at setup time prevents the model package's
// setupPlacesTestDB TRUNCATE from racing our seed (Phase 2 ITERATE).

const (
	planPrefix     = "_p12_"
	advisoryLockID = 12 // matches Plan 12 magic — used by setupGeoIntegration
)

type geoSetup struct {
	pool   *pgxpool.Pool
	server *httptest.Server
}

func setupGeoIntegration(t *testing.T) *geoSetup {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	// Refuse to run against anything that doesn't look like a test DB. Prevents
	// a misexported prod DATABASE_URL from causing the seed/cleanup logic
	// below to touch real rows. The docker-compose target uses
	// `kittypaw_api_test`; CI similarly conventions a `_test` suffix.
	if !strings.Contains(dsn, "_test") {
		t.Fatalf("DATABASE_URL must point at a test DB (must contain \"_test\"); got %q", dsn)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}

	if _, err := pool.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLockID); err != nil {
		pool.Close()
		t.Fatalf("pg_advisory_lock(%d): %v", advisoryLockID, err)
	}

	if err := migrateGeoIntegrationDB(dsn); err != nil {
		pool.Close()
		t.Fatalf("migrate geo integration db: %v", err)
	}

	store := model.NewPlaceStore(pool)
	h := &proxy.PlacesHandler{Store: store}
	server := httptest.NewServer(h.Resolve())

	t.Cleanup(func() {
		bg := context.Background()
		// Best-effort cleanup. Errors here only matter for the next run; the
		// prefix is deterministic so re-runs converge. (Postgres also
		// auto-releases advisory locks on connection close — this unlock is
		// belt-and-suspenders for graceful shutdown.)
		_, _ = pool.Exec(bg, "DELETE FROM places WHERE name_ko LIKE $1 OR source_ref LIKE $1", planPrefix+"%")
		_, _ = pool.Exec(bg, "DELETE FROM alias_overrides WHERE alias LIKE $1", planPrefix+"%")
		_, _ = pool.Exec(bg, "SELECT pg_advisory_unlock($1)", advisoryLockID)
		server.Close()
		pool.Close()
	})

	return &geoSetup{pool: pool, server: server}
}

func migrateGeoIntegrationDB(dsn string) error {
	m, err := migrate.New("file://../../migrations", "pgx5://"+stripPostgresScheme(dsn))
	if err != nil {
		return err
	}
	defer func() {
		_, _ = m.Close()
	}()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

func stripPostgresScheme(dsn string) string {
	dsn = strings.TrimPrefix(dsn, "postgres://")
	dsn = strings.TrimPrefix(dsn, "postgresql://")
	return dsn
}

func seedPlace(t *testing.T, pool *pgxpool.Pool, nameKo string, lat, lon float64, typ, source, sourceRef string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO places (name_ko, aliases, lat, lon, type, source, source_ref, source_priority)
		VALUES ($1, '{}'::text[], $2, $3, $4, $5, $6, $7)
		ON CONFLICT (source, source_ref) DO UPDATE SET
			name_ko = EXCLUDED.name_ko,
			lat = EXCLUDED.lat,
			lon = EXCLUDED.lon,
			type = EXCLUDED.type,
			source_priority = EXCLUDED.source_priority,
			updated_at = now()
	`, nameKo, lat, lon, typ, source, sourceRef, priorityFor(source))
	if err != nil {
		t.Fatalf("seedPlace(%q): %v", nameKo, err)
	}
}

func priorityFor(source string) int {
	switch source {
	case model.SourceSeoulMetro:
		return model.PrioritySeoulMetro
	default:
		return model.PriorityWikidata
	}
}

func seedAliasOverride(t *testing.T, pool *pgxpool.Pool, alias string, lat, lon float64, targetName string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO alias_overrides (alias, target_lat, target_lon, target_name, note)
		VALUES ($1, $2, $3, $4, 'plan-12 self-seed')
		ON CONFLICT (alias) DO NOTHING
	`, alias, lat, lon, targetName)
	if err != nil {
		t.Fatalf("seedAliasOverride(%q): %v", alias, err)
	}
}

type resolveResp struct {
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	Source      string  `json:"source"`
	Type        string  `json:"type"`
	NameMatched string  `json:"name_matched"`
}

func resolveQuery(t *testing.T, server *httptest.Server, q string) (*http.Response, []byte) {
	t.Helper()
	u := server.URL + "/?q=" + url.QueryEscape(q)
	resp, err := http.Get(u)
	if err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, body
}

func decodeOK(t *testing.T, body []byte) *resolveResp {
	t.Helper()
	var r resolveResp
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, string(body))
	}
	return &r
}

func approxEqual(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 0.001
}

// --- T1: exact match ---------------------------------------------------------

func TestResolve_Integration_Exact(t *testing.T) {
	s := setupGeoIntegration(t)

	const (
		nameKo = planPrefix + "exact_강남역"
		lat    = 37.4979
		lon    = 127.0276
	)
	seedPlace(t, s.pool, nameKo, lat, lon, model.TypeLandmark, model.SourceWikidata, planPrefix+"exact_ref")

	resp, body := resolveQuery(t, s.server, nameKo)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, string(body))
	}
	got := decodeOK(t, body)
	if !approxEqual(got.Lat, lat) || !approxEqual(got.Lon, lon) {
		t.Fatalf("lat/lon = (%f, %f), want (%f, %f)", got.Lat, got.Lon, lat, lon)
	}
	if got.NameMatched != nameKo {
		t.Fatalf("name_matched = %q, want %q", got.NameMatched, nameKo)
	}
	if got.Source != model.SourceWikidata {
		t.Fatalf("source = %q, want %q", got.Source, model.SourceWikidata)
	}
	if got.Type != model.TypeLandmark {
		t.Fatalf("type = %q, want %q", got.Type, model.TypeLandmark)
	}
}

// --- T2.a: alias_override defeats places.exact -------------------------------

func TestResolve_Integration_AliasOverridePriority(t *testing.T) {
	s := setupGeoIntegration(t)

	const (
		alias       = planPrefix + "aop_target"
		placeLat    = 37.0000
		placeLon    = 127.0000
		overrideLat = 35.5555
		overrideLon = 128.5555
		overrideTo  = planPrefix + "aop_override"
	)
	// places row: same name as alias key, but DIFFERENT coords. If
	// alias_override is not honored first, response would point here.
	seedPlace(t, s.pool, alias, placeLat, placeLon, model.TypeLandmark, model.SourceWikidata, planPrefix+"aop_ref")
	seedAliasOverride(t, s.pool, alias, overrideLat, overrideLon, overrideTo)

	resp, body := resolveQuery(t, s.server, alias)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, string(body))
	}
	got := decodeOK(t, body)
	if !approxEqual(got.Lat, overrideLat) || !approxEqual(got.Lon, overrideLon) {
		t.Fatalf("lat/lon = (%f, %f), want override (%f, %f) — places.exact must not win", got.Lat, got.Lon, overrideLat, overrideLon)
	}
	if got.Source != model.SourceKittypawAlias {
		t.Fatalf("source = %q, want %q", got.Source, model.SourceKittypawAlias)
	}
	if got.Type != model.TypeAliasOverride {
		t.Fatalf("type = %q, want %q", got.Type, model.TypeAliasOverride)
	}
	if got.NameMatched != overrideTo {
		t.Fatalf("name_matched = %q, want %q (target_name)", got.NameMatched, overrideTo)
	}
}

// --- T2.b: fuzzy fallback when q does not exact-match ------------------------

func TestResolve_Integration_FuzzyFallback(t *testing.T) {
	s := setupGeoIntegration(t)

	const (
		seeded = planPrefix + "fuzzy_강남구청역"
		query  = planPrefix + "fuzzy_강남구청" // no 역 suffix — exact miss, fuzzy hit
		lat    = 37.5172
		lon    = 127.0473
	)
	seedPlace(t, s.pool, seeded, lat, lon, model.TypeLandmark, model.SourceWikidata, planPrefix+"fuzzy_ref")

	resp, body := resolveQuery(t, s.server, query)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, string(body))
	}
	got := decodeOK(t, body)
	if got.NameMatched != seeded {
		t.Fatalf("name_matched = %q, want %q (fuzzy match)", got.NameMatched, seeded)
	}
	if !approxEqual(got.Lat, lat) || !approxEqual(got.Lon, lon) {
		t.Fatalf("lat/lon = (%f, %f), want (%f, %f)", got.Lat, got.Lon, lat, lon)
	}
	if got.Source != model.SourceWikidata {
		t.Fatalf("source = %q, want %q", got.Source, model.SourceWikidata)
	}
	if got.Type != model.TypeLandmark {
		t.Fatalf("type = %q, want %q", got.Type, model.TypeLandmark)
	}
}

// --- T2.c: typeHint via 역 suffix promotes subway_station over landmark -----

func TestResolve_Integration_TypeHintSubwayWins(t *testing.T) {
	s := setupGeoIntegration(t)

	// Two rows with the SAME name_ko but different types. Without a typeHint
	// the orderByClause prefers landmark (default). With 역 suffix the handler
	// passes typeHint='subway_station' and the CASE clause floats the
	// subway_station row to the top.
	const sameName = planPrefix + "th_연결역" // ends in 역 → reSubwayStation hits → typeHint='subway_station'
	seedPlace(t, s.pool, sameName, 37.1111, 127.1111, model.TypeLandmark, model.SourceWikidata, planPrefix+"th_landmark_ref")
	seedPlace(t, s.pool, sameName, 37.2222, 127.2222, model.TypeSubwayStation, model.SourceWikidata, planPrefix+"th_subway_ref")

	resp, body := resolveQuery(t, s.server, sameName)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, string(body))
	}
	got := decodeOK(t, body)
	if got.Type != model.TypeSubwayStation {
		t.Fatalf("type = %q, want %q (역 suffix → subway_station priority)", got.Type, model.TypeSubwayStation)
	}
	if !approxEqual(got.Lat, 37.2222) {
		t.Fatalf("lat = %f, want 37.2222 (subway row)", got.Lat)
	}
}

// --- T3: negative cases (status + error code pinned) -------------------------

func decodeError(t *testing.T, body []byte) string {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode error body: %v (body=%s)", err, string(body))
	}
	code, _ := raw["error"].(string)
	return code
}

func TestResolve_Integration_OutOfKorea(t *testing.T) {
	s := setupGeoIntegration(t)

	// Use a prefixed unmappable string instead of "Tokyo" — the latter is a
	// short ASCII token that can fuzzy-match (similarity > 0.7) against rows
	// seeded by other packages running in parallel (e.g. internal/model
	// place_integration_test.go's 8 cases). The _p12_oof_ prefix isolates
	// this test from cross-package fixtures and our own cleanup wipes any
	// stale rows under it.
	const q = planPrefix + "oof_unmappable_xyz_0xff"
	resp, body := resolveQuery(t, s.server, q)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", resp.StatusCode, string(body))
	}
	if code := decodeError(t, body); code != "unsupported_input" {
		t.Fatalf("error = %q, want %q", code, "unsupported_input")
	}
}

func TestResolve_Integration_MissingQ(t *testing.T) {
	s := setupGeoIntegration(t)

	resp, err := http.Get(s.server.URL + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if code := decodeError(t, body); code != "missing_q" {
		t.Fatalf("error = %q, want %q", code, "missing_q")
	}
}

func TestResolve_Integration_InputTooLong(t *testing.T) {
	s := setupGeoIntegration(t)

	// 201 runes of Hangul: easily exceeds the 200-rune cap, well under the
	// 6× byte cap so we hit the rune check (414 + input_too_long) cleanly.
	long := ""
	for i := 0; i < 201; i++ {
		long += "가"
	}

	resp, body := resolveQuery(t, s.server, long)
	if resp.StatusCode != http.StatusRequestURITooLong {
		t.Fatalf("status = %d, want 414; body=%s", resp.StatusCode, string(body))
	}
	if code := decodeError(t, body); code != "input_too_long" {
		t.Fatalf("error = %q, want %q", code, "input_too_long")
	}
}
