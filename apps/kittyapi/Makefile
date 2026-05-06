BINARY := kittypaw-api
PKG    := ./cmd/server
VERSION ?= $(shell git -C ../.. describe --tags --always 2>/dev/null || echo dev)
COMMIT  ?= $(shell git -C ../.. rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT)

.PHONY: build test test-integration test-integration-all test-integration-public test-integration-calendar test-integration-air test-integration-weather-almanac test-integration-down test-migration lint run clean seed-wikidata seed-seoul-metro benchmark-resolve smoke

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

test:
	go test ./... -v -count=1

test-unit:
	go test ./... -v -count=1 -short

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .
	goimports -w .

run: build
	@if [ -f .env ]; then set -a && . ./.env && set +a; fi && ./$(BINARY)

clean:
	rm -f $(BINARY)

# --- Geo Resolve (Plan 6) -----------------------------------------------------
# Wikidata SPARQL → places (CC0). Idempotent transactional swap, safe to re-run.
seed-wikidata:
	@if [ -f .env ]; then set -a && . ./.env && set +a; fi && go run ./cmd/seed-wikidata

# Seoul Metro 1-8호선 CSV → places. Manual download required (see docs/maintenance.md).
seed-seoul-metro:
	@if [ -f .env ]; then set -a && . ./.env && set +a; fi && go run ./cmd/seed-seoul-metro --csv=testdata/seoul_metro.csv

# Benchmark resolve corpus against a running server. Exits 1 if precision < gate.
benchmark-resolve:
	go run ./cmd/benchmark-resolve --base=$${KITTYAPI_BASE:-http://localhost:8080}

# --- Integration test groups (Plan 8 / smoke-3-layer L1.A) ---------------------
# Build tag split (1단계): 신규 `calendar_integration` (외부 KASI 그룹) 만 분리.
# 기존 `integration` 태그는 model + weather + almanac 통합 유지 — 추가 분리는
# L1.B/C/D sibling plan 시점.

# Backward-compatible umbrella — 모든 integration 그룹 sequential 실행.
test-integration-all: test-integration test-integration-public
	@echo "all integration test groups passed"

test-integration-public: test-integration-calendar test-integration-air test-integration-weather-almanac
	@echo "public data integration test groups passed"

# Calendar 그룹 (holiday_integration_test.go) — 외부 KASI 호출, DB 무관.
# `HOLIDAY_API_KEY` 누락 시 t.Skipf 패턴 (weather/almanac 과 동일).
test-integration-calendar:
	@if [ -f .env ]; then set -a && . ./.env && set +a; fi && \
		go test -tags=calendar_integration -count=1 ./internal/proxy/... -run TestHoliday

# Air 그룹 (airkorea_integration_test.go) — 외부 한국환경공단 호출, DB 무관.
# `AIRKOREA_API_KEY` 누락 시 t.Skipf. L1.B (sibling of Plan 8 L1.A).
test-integration-air:
	@if [ -f .env ]; then set -a && . ./.env && set +a; fi && \
		go test -tags=air_integration -count=1 ./internal/proxy/... -run TestAirKorea

test-integration-weather-almanac:
	@if [ -f .env ]; then set -a && . ./.env && set +a; fi && \
		go test -tags=integration -count=1 ./internal/proxy -run 'Test(VillageForecast_LiveKMA|UltraShort_LiveKMA|Almanac_LiveKASI)'

# DB-backed integration tests — docker-compose 기반.
# Spins up postgres:17-alpine on port 15432 with pg_trgm pre-installed,
# applies migrations, then runs DB integration packages sequentially.
# Container persists between runs for fast iteration; tear down with
# `make test-integration-down`.
test-integration:
	@docker-compose -f docker-compose.test.yml up -d
	@echo "waiting for postgres-test to be healthy..."
	@for i in $$(seq 1 30); do \
		if docker exec kittypaw-postgres-test pg_isready -U kittypaw -d kittypaw_api_test >/dev/null 2>&1; then \
			echo "postgres-test ready"; break; \
		fi; \
		sleep 1; \
	done
	DATABASE_URL="postgres://kittypaw:kittypaw@localhost:15432/kittypaw_api_test?sslmode=disable" \
		go test -tags=integration -count=1 ./internal/model
	DATABASE_URL="postgres://kittypaw:kittypaw@localhost:15432/kittypaw_api_test?sslmode=disable" \
		go test -tags=integration -count=1 ./internal/proxy -run 'TestResolve_Integration'

test-integration-down:
	@docker-compose -f docker-compose.test.yml down -v

# Migration reversibility (Plan 22 PR-C). Pins `up → down → up` cycle
# across the latest migrations and asserts dirty=false at each boundary.
# Failing here means a down-path RAISE EXCEPTION or a non-recoverable
# DDL — block the merge until fixed.
test-migration:
	@docker-compose -f docker-compose.test.yml up -d
	@echo "waiting for postgres-test to be healthy..."
	@for i in $$(seq 1 30); do \
		if docker exec kittypaw-postgres-test pg_isready -U kittypaw -d kittypaw_api_test >/dev/null 2>&1; then \
			echo "postgres-test ready"; break; \
		fi; \
		sleep 1; \
	done
	DATABASE_URL="postgres://kittypaw:kittypaw@localhost:15432/kittypaw_api_test?sslmode=disable" \
		go test -tags=integration -count=1 -run TestMigrationReversibility ./internal/model/...

# --- Prod smoke (Plan 8 v2 D4 / L3) -------------------------------------------
# Verifies every external-API endpoint against prod (default) or a custom URL.
# Throttled to stay under anon 5rpm/IP rate-limit. Requires `jq`.
smoke:
	@bash deploy/smoke.sh
