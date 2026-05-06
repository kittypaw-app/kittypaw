#!/usr/bin/env bash
set -euo pipefail

HOST="${KITTYAPI_ENV_HOST:-second}"
REMOTE_ENV="${KITTYAPI_REMOTE_ENV:-/home/jinto/kittyapi/.env}"
ALLOWLIST_RE='^(AIRKOREA_API_KEY|WEATHER_API_KEY|HOLIDAY_API_KEY)='

usage() {
  cat >&2 <<'EOF'
Usage:
  scripts/with-kittyapi-public-env.sh -- <command> [args...]

Environment:
  KITTYAPI_ENV_HOST    SSH host alias. Default: second
  KITTYAPI_REMOTE_ENV  Remote kittyapi .env path. Default: /home/jinto/kittyapi/.env

Only AIRKOREA_API_KEY, WEATHER_API_KEY, and HOLIDAY_API_KEY are imported.
EOF
}

if [[ "${1:-}" != "--" ]]; then
  usage
  exit 2
fi
shift

if [[ "$#" -eq 0 ]]; then
  usage
  exit 2
fi

env_dump="$(
  ssh "$HOST" "test -f '$REMOTE_ENV' && grep -E '$ALLOWLIST_RE' '$REMOTE_ENV' || true"
)"

if [[ -z "$env_dump" ]]; then
  echo "No public kittyapi API keys found in $HOST:$REMOTE_ENV" >&2
  exit 1
fi

while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  key="${line%%=*}"
  value="${line#*=}"
  case "$value" in
    \"*\") value="${value#\"}"; value="${value%\"}" ;;
    \'*\') value="${value#\'}"; value="${value%\'}" ;;
  esac
  export "$key=$value"
done <<<"$env_dump"

exec "$@"
