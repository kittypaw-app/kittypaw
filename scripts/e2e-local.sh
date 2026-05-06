#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT/docker-compose.e2e.yml"

export GOCACHE="${GOCACHE:-/private/tmp/kitty-go-build}"
export DATABASE_URL="${DATABASE_URL:-postgres://kittypaw:kittypaw@localhost:15433/kitty_e2e_test?sslmode=disable}"

cleanup() {
  local status=$?
  if [[ "${KITTY_E2E_SKIP_COMPOSE:-}" != "1" && "${KITTY_E2E_KEEP_DB:-}" != "1" ]]; then
    docker compose -f "$COMPOSE_FILE" down -v >/dev/null
  fi
  exit "$status"
}
trap cleanup EXIT

echo "==> Local auth/space E2E"
echo "DATABASE_URL=$DATABASE_URL"

if [[ "${KITTY_E2E_SKIP_COMPOSE:-}" != "1" ]]; then
  echo "==> Starting disposable Postgres"
  docker compose -f "$COMPOSE_FILE" up -d postgres-e2e
  for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30; do
    if docker compose -f "$COMPOSE_FILE" exec -T postgres-e2e pg_isready -U kittypaw -d kitty_e2e_test >/dev/null; then
      break
    fi
    if [[ "$attempt" == "30" ]]; then
      echo "Postgres did not become ready" >&2
      exit 1
    fi
    sleep 1
  done
fi

echo "==> Running Go E2E harness"
(
  cd "$ROOT/testkit/e2e"
  go test ./... -count=1 -timeout=90s -v
)
