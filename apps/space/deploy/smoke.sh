#!/usr/bin/env bash
set -euo pipefail

BASE="${BASE_URL:-https://space.kittypaw.app}"
HEADERS_FILE="$(mktemp)"
trap 'rm -f "$HEADERS_FILE"' EXIT

curl -fsS "$BASE/health" | jq -e '.status == "healthy"' >/dev/null
printf 'ok health\n'

curl -fsS "$BASE/chat/" | grep -q 'space-chat-root'
printf 'ok chat html\n'

curl -fsS "$BASE/assets/chat.js" | grep -q '/chat/api/routes'
printf 'ok chat js bff route\n'

SESSION_CODE="$(curl -sS -o /dev/null -w '%{http_code}' "$BASE/chat/api/session")"
if [[ "$SESSION_CODE" != "401" ]]; then
    echo "expected /chat/api/session to return 401 for anonymous caller, got $SESSION_CODE" >&2
    exit 1
fi
printf 'ok anonymous session rejected\n'

LOGIN_CODE="$(curl -sS -o /dev/null -D "$HEADERS_FILE" -w '%{http_code}' "$BASE/auth/login/google")"
if [[ "$LOGIN_CODE" != "302" ]]; then
    echo "expected /auth/login/google to return 302, got $LOGIN_CODE" >&2
    exit 1
fi
if ! grep -iq '^location: .*\/web\/google' "$HEADERS_FILE"; then
    echo "expected login redirect Location to contain /web/google" >&2
    exit 1
fi
printf 'ok google login redirect\n'
