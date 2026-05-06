#!/usr/bin/env bash
set -euo pipefail

BASE="${BASE_URL:-https://home.kittypaw.app}"

curl -fsS "$BASE/health" | jq -e '.status == "healthy"' >/dev/null
curl -fsS "$BASE/chat/" | grep -q 'home-chat-root'
