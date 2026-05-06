#!/usr/bin/env bash
# portal end-to-end device contract verifier.
#
# Exercises the full pair → refresh → list → delete flow against a real
# portal instance (prod by default), using a user JWT supplied by the
# caller (Google OAuth is interactive — caller goes through it once
# manually). Pins the wire-format invariants daemon + chat-team verifier
# both depend on, so a regression here surfaces before either of them
# debug it.
#
# Usage:
#   USER_JWT=<token> bash deploy/e2e_devices.sh
#   USER_JWT=<token> PORTAL_BASE_URL=http://localhost:9714 bash deploy/e2e_devices.sh
#
# Obtaining USER_JWT (one-time, manual):
#   1. open https://portal.kittypaw.app/auth/google in a browser
#   2. complete Google OAuth — final redirect lands on a localhost URL
#      with access_token=... in the query string
#   3. copy that access_token value into USER_JWT
#
# Exit: 0 on all-pass, 1 on any failure.
# Requires: bash, curl, jq, python3 (header/payload base64 decode).

set -uo pipefail

BASE="${PORTAL_BASE_URL:-${BASE_URL:-https://portal.kittypaw.app}}"
USER_JWT="${USER_JWT:-}"

if [[ -z "$USER_JWT" ]]; then
    echo "ERROR: USER_JWT required (Google OAuth access_token)" >&2
    echo "  see deploy/e2e_devices.sh header for the manual step" >&2
    exit 1
fi

if [[ -t 1 ]]; then
    G='\033[32m'; R='\033[31m'; Y='\033[33m'; B='\033[34m'; N='\033[0m'
else
    G=''; R=''; Y=''; B=''; N=''
fi

PASS=0
FAIL=0
FAIL_LIST=()

pass() { PASS=$((PASS + 1)); printf "${G}✓${N} %s\n" "$1"; }
fail() { FAIL=$((FAIL + 1)); FAIL_LIST+=("$1"); printf "${R}✗${N} %s\n" "$1"; }
info() { printf "${B}ℹ${N} %s\n" "$1"; }

# decode_jwt_segment <jwt> <0|1>  →  decoded JSON to stdout
# 0 = header, 1 = payload. base64url decode, no signature verification.
decode_jwt_segment() {
    local jwt="$1"
    local idx="$2"
    local seg
    seg=$(echo "$jwt" | cut -d. -f"$((idx + 1))")
    # base64url → base64: replace -_ with +/, then pad with =.
    python3 -c "
import base64, sys
seg = sys.argv[1]
seg = seg.replace('-', '+').replace('_', '/')
seg += '=' * (-len(seg) % 4)
sys.stdout.write(base64.b64decode(seg).decode())
" "$seg"
}

# req_with_status <method> <path> <body?> <auth-header?>
# Sets RESP and CODE.
req() {
    local method="$1"
    local path="$2"
    local body="${3:-}"
    local auth="${4:-}"
    local args=(-sS -X "$method" -w '\n%{http_code}')
    if [[ -n "$auth" ]]; then args+=(-H "Authorization: Bearer $auth"); fi
    if [[ -n "$body" ]]; then args+=(-H "Content-Type: application/json" -d "$body"); fi
    local raw
    raw=$(curl "${args[@]}" "${BASE}${path}" 2>/dev/null || printf '\n000')
    RESP=$(printf '%s' "$raw" | sed '$d')
    CODE=$(printf '%s' "$raw" | tail -n1)
}

echo "portal E2E device contract verifier"
echo "  BASE = $BASE"
echo

# --- Step 1: JWKS endpoint ---------------------------------------------------
echo "--- 1. JWKS endpoint ---"
req GET "/.well-known/jwks.json"
if [[ "$CODE" == "200" ]]; then
    KID=$(echo "$RESP" | jq -r '.keys[0].kid')
    ALG=$(echo "$RESP" | jq -r '.keys[0].alg')
    if [[ "$ALG" == "RS256" && -n "$KID" && "$KID" != "null" ]]; then
        pass "JWKS reachable [alg=$ALG, kid=${KID:0:16}...]"
    else
        fail "JWKS shape unexpected (alg=$ALG, kid=$KID)"
    fi
else
    fail "JWKS GET status $CODE"
fi

# --- Step 2: pair ------------------------------------------------------------
echo
echo "--- 2. POST /auth/devices/pair ---"
PAIR_BODY='{"name":"e2e-test","capabilities":{"v":"e2e-0.1"}}'
req POST "/auth/devices/pair" "$PAIR_BODY" "$USER_JWT"
if [[ "$CODE" != "200" ]]; then
    fail "pair status $CODE: $RESP"
    echo "  Cannot continue — fix auth or network issue and retry."
    exit 1
fi

DEVICE_ID=$(echo "$RESP" | jq -r '.device_id')
DEVICE_ACCESS=$(echo "$RESP" | jq -r '.device_access_token')
DEVICE_REFRESH=$(echo "$RESP" | jq -r '.device_refresh_token')
EXPIRES_IN=$(echo "$RESP" | jq -r '.expires_in')

if [[ -z "$DEVICE_ID" || "$DEVICE_ID" == "null" ]]; then
    fail "pair response missing device_id"
    exit 1
fi
if [[ "$EXPIRES_IN" != "900" ]]; then
    fail "expires_in = $EXPIRES_IN, want 900"
else
    pass "pair returned device_id=${DEVICE_ID:0:8}..., expires_in=900"
fi

# --- Step 3: device JWT wire format -----------------------------------------
echo
echo "--- 3. Device JWT wire format ---"
HEADER=$(decode_jwt_segment "$DEVICE_ACCESS" 0)
PAYLOAD=$(decode_jwt_segment "$DEVICE_ACCESS" 1)

ALG=$(echo "$HEADER" | jq -r '.alg')
HDR_KID=$(echo "$HEADER" | jq -r '.kid')
SUB=$(echo "$PAYLOAD" | jq -r '.sub')
USER_ID=$(echo "$PAYLOAD" | jq -r '.user_id')
AUD_CHAT=$(echo "$PAYLOAD" | jq -r '(.aud // []) | index("https://chat.kittypaw.app")')
AUD_SPACE=$(echo "$PAYLOAD" | jq -r '(.aud // []) | index("https://space.kittypaw.app")')
SCOPE=$(echo "$PAYLOAD" | jq -r '.scope[0]')
V=$(echo "$PAYLOAD" | jq -r '.v')
ISS=$(echo "$PAYLOAD" | jq -r '.iss')

[[ "$ALG" == "RS256" ]]                            && pass "header.alg = RS256"     || fail "alg = $ALG"
[[ -n "$HDR_KID" && "$HDR_KID" != "null" ]]        && pass "header.kid present"     || fail "kid missing"
[[ "$HDR_KID" == "$KID" ]]                         && pass "header.kid matches JWKS" || fail "kid mismatch"
[[ "$SUB" == "device:$DEVICE_ID" ]]                && pass "sub = device:<id>"      || fail "sub = $SUB"
[[ -n "$USER_ID" && "$USER_ID" != "null" ]]        && pass "user_id present"        || fail "user_id missing"
[[ "$AUD_CHAT" != "null" ]]                        && pass "aud includes chat"      || fail "aud missing chat"
[[ "$AUD_SPACE" != "null" ]]                       && pass "aud includes space"     || fail "aud missing space"
[[ "$SCOPE" == "daemon:connect" ]]                 && pass "scope = daemon:connect" || fail "scope = $SCOPE"
[[ "$V" == "2" ]]                                  && pass "v = 2"                  || fail "v = $V"
[[ "$ISS" == "https://portal.kittypaw.app/auth" ]] && pass "iss correct"            || fail "iss = $ISS"

# --- Step 4: pair response Cache-Control ------------------------------------
echo
echo "--- 4. RFC 6749 §5.1 — Cache-Control on pair response ---"
HDRS=$(curl -sS -D - -o /dev/null -X POST \
    -H "Authorization: Bearer $USER_JWT" \
    -H "Content-Type: application/json" \
    -d '{"name":"hdr-check"}' \
    "${BASE}/auth/devices/pair" 2>/dev/null || true)
CACHE=$(echo "$HDRS" | grep -i '^cache-control:' | head -1 | tr -d '\r' | awk -F': ' '{print $2}')
[[ "$CACHE" == "no-store" ]] && pass "Cache-Control: no-store" || fail "Cache-Control = '$CACHE'"

# Clean up the throw-away device from step 4 so it doesn't pollute list.
HDR_DEV_ID=$(echo "$HDRS" | sed -n '/^$/,$p' | tail -n +2 | jq -r '.device_id // empty' 2>/dev/null || true)
if [[ -n "$HDR_DEV_ID" ]]; then
    req DELETE "/auth/devices/$HDR_DEV_ID" "" "$USER_JWT"
fi

# --- Step 5: refresh rotation ------------------------------------------------
echo
echo "--- 5. POST /auth/devices/refresh — rotation ---"
REFRESH_BODY=$(jq -nc --arg t "$DEVICE_REFRESH" '{refresh_token: $t}')
req POST "/auth/devices/refresh" "$REFRESH_BODY"
if [[ "$CODE" != "200" ]]; then
    fail "refresh status $CODE: $RESP"
else
    NEW_REFRESH=$(echo "$RESP" | jq -r '.device_refresh_token')
    NEW_DEVICE_ID=$(echo "$RESP" | jq -r '.device_id')
    [[ "$NEW_REFRESH" != "$DEVICE_REFRESH" ]] && pass "refresh token rotated" || fail "refresh token unchanged"
    [[ "$NEW_DEVICE_ID" == "$DEVICE_ID" ]]    && pass "device_id stable on rotation" || fail "device_id changed"
fi

# --- Step 6: reuse detection -------------------------------------------------
echo
echo "--- 6. Reuse detection — replay original refresh token ---"
req POST "/auth/devices/refresh" "$REFRESH_BODY"
if [[ "$CODE" == "401" ]]; then
    pass "reused (already-revoked) refresh → 401"
else
    fail "reuse status $CODE, want 401"
fi

# --- Step 7: list ------------------------------------------------------------
echo
echo "--- 7. GET /auth/devices ---"
req GET "/auth/devices" "" "$USER_JWT"
if [[ "$CODE" == "200" ]]; then
    COUNT=$(echo "$RESP" | jq 'length')
    HAS_DEV=$(echo "$RESP" | jq --arg id "$DEVICE_ID" 'any(.device_id == $id)')
    [[ "$HAS_DEV" == "true" ]] && pass "list includes paired device (count=$COUNT)" || fail "paired device not in list"
else
    fail "list status $CODE"
fi

# --- Step 8: delete + verify gone --------------------------------------------
echo
echo "--- 8. DELETE /auth/devices/{id} ---"
req DELETE "/auth/devices/$DEVICE_ID" "" "$USER_JWT"
if [[ "$CODE" == "200" ]]; then
    pass "delete returned 200"
else
    fail "delete status $CODE"
fi

req GET "/auth/devices" "" "$USER_JWT"
HAS_DEV=$(echo "$RESP" | jq --arg id "$DEVICE_ID" 'any(.device_id == $id)')
[[ "$HAS_DEV" == "false" ]] && pass "device removed from list after delete" || fail "device still present"

# --- Step 9: post-delete refresh — revoked-device guard ---------------------
echo
echo "--- 9. Post-delete refresh — revoked-device guard ---"
NEW_REFRESH_BODY=$(jq -nc --arg t "$NEW_REFRESH" '{refresh_token: $t}')
req POST "/auth/devices/refresh" "$NEW_REFRESH_BODY"
if [[ "$CODE" == "401" ]]; then
    pass "refresh after device-delete → 401"
else
    fail "post-delete refresh status $CODE, want 401"
fi

# --- Step 10: cross-audience leak guard --------------------------------------
echo
echo "--- 10. Cross-audience leak guard — device JWT against /auth/me ---"
# Re-pair briefly to obtain a fresh device JWT (the previous one is still
# valid until exp, but we want a clean throw-away).
req POST "/auth/devices/pair" '{"name":"x-aud-check"}' "$USER_JWT"
XAUD_DEV_ID=$(echo "$RESP" | jq -r '.device_id')
XAUD_DEV_JWT=$(echo "$RESP" | jq -r '.device_access_token')

req GET "/auth/me" "" "$XAUD_DEV_JWT"
if [[ "$CODE" == "401" ]]; then
    pass "device JWT (aud=chat) on user MW (aud=API) → 401"
else
    fail "cross-aud leak — device JWT got $CODE on /auth/me"
fi

req DELETE "/auth/devices/$XAUD_DEV_ID" "" "$USER_JWT"

# --- Summary -----------------------------------------------------------------
TOTAL=$((PASS + FAIL))
echo
echo "=== Summary ==="
printf "Passed: ${G}%d${N}/%d\n" "$PASS" "$TOTAL"
if (( FAIL > 0 )); then
    printf "Failed: ${R}%d${N}\n" "$FAIL"
    for f in "${FAIL_LIST[@]}"; do
        printf "  ${R}-${N} %s\n" "$f"
    done
    exit 1
fi
echo "All E2E checks passed."
