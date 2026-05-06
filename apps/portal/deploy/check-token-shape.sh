#!/usr/bin/env bash
# portal JWT wire-format guard.
# Verifies a portal-issued access token matches the contract pinned in
# docs/specs/kittychat-credential-foundation.md:
#   - iss == "https://portal.kittypaw.app/auth"
#   - v   == 2
#   - sub is a non-empty string
#   - "uid" key absent (legacy artifact must not reappear)
#   - aud contains API, Chat, and Home URL-form audiences during migration
#   - scope contains both "chat:relay" and "models:read"
#
# Usage:
#   bash deploy/check-token-shape.sh <jwt-token>
#   echo "$ACCESS_TOKEN" | bash deploy/check-token-shape.sh -
#
# Exit: 0 on PASS, 1 on FAIL.

set -uo pipefail

if [[ -t 1 ]]; then
    G='\033[32m'; R='\033[31m'; N='\033[0m'
else
    G=''; R=''; N=''
fi

if ! command -v jq >/dev/null 2>&1; then
    printf "${R}✗${N} jq not found — install with 'brew install jq' or 'apt install jq'\n" >&2
    exit 1
fi

if [[ $# -lt 1 ]]; then
    echo "usage: $0 <jwt-token> | $0 -" >&2
    exit 2
fi

if [[ "$1" == "-" ]]; then
    TOKEN=$(cat)
else
    TOKEN="$1"
fi
TOKEN="${TOKEN// /}"   # strip whitespace

# Split JWT and decode middle (payload) segment. base64url → base64 (tr).
IFS='.' read -r -a PARTS <<< "$TOKEN"
if [[ ${#PARTS[@]} -ne 3 ]]; then
    printf "${R}✗${N} expected 3 JWT segments, got %d\n" "${#PARTS[@]}"
    exit 1
fi

# Pad base64url to multiple of 4 then decode.
PAYLOAD_B64="${PARTS[1]}"
PAD=$(( (4 - ${#PAYLOAD_B64} % 4) % 4 ))
PAYLOAD_B64="${PAYLOAD_B64}$(printf '=%.0s' $(seq 1 $PAD))"
PAYLOAD=$(printf '%s' "$PAYLOAD_B64" | tr '_-' '/+' | base64 -d 2>/dev/null) || {
    printf "${R}✗${N} failed to base64-decode payload\n"
    exit 1
}

# Single jq expression — short-circuits on first violation.
if printf '%s' "$PAYLOAD" | jq -e '
    .iss == "https://portal.kittypaw.app/auth"
    and .v  == 2
    and (.sub | type == "string" and length > 0)
    and (has("uid") | not)
    and ((.aud   // []) | index("https://api.kittypaw.app") != null and index("https://chat.kittypaw.app") != null and index("https://home.kittypaw.app") != null)
    and ((.scope // []) | index("chat:relay") != null and index("models:read") != null)
' >/dev/null 2>&1; then
    printf "${G}✓${N} PASS — wire-format matches portal contract\n"
    printf '%s\n' "$PAYLOAD" | jq '{iss, sub, aud, scope, v, exp}'
    exit 0
else
    printf "${R}✗${N} FAIL — wire-format violates portal contract\n"
    printf "payload:\n"
    printf '%s\n' "$PAYLOAD" | jq .
    exit 1
fi
