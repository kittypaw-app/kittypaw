#!/usr/bin/env bash
# portal + kittyapi prod smoke test.
# Verifies HTTP 200 + JSON envelope `response.header.resultCode == "00"` for
# every API-backed endpoint, plus API /health and portal identity routes.
#
# Usage:
#   bash deploy/smoke.sh
#   BASE_URL=http://localhost:9712 PORTAL_BASE_URL=http://localhost:9714 bash deploy/smoke.sh
#   make smoke
#
# Exit: 0 on all-pass, 1 on any failure (rate-limit warnings don't fail).
# Throttled by `sleep 0.5` between calls to stay under anon 5rpm/IP gate.

set -uo pipefail

BASE="${BASE_URL:-https://api.kittypaw.app}"
PORTAL_BASE="${PORTAL_BASE_URL:-https://portal.kittypaw.app}"
THROTTLE="${SMOKE_THROTTLE:-0.5}"

PASS=0
FAIL=0
WARN=0
FAIL_LIST=()

if [[ -t 1 ]]; then
    G='\033[32m'; R='\033[31m'; Y='\033[33m'; N='\033[0m'
else
    G=''; R=''; Y=''; N=''
fi

# Split body and trailing HTTP code from a single curl response.
_split_body_code() {
    local raw="$1"
    BODY=$(printf '%s' "$raw" | sed '$d')
    CODE=$(printf '%s' "$raw" | tail -n1)
}

# do_curl URL [METHOD=GET] → sets BODY + CODE. Auto-recovers from 429 by
# waiting one full anon rate-limit window (60s + 1s margin) and retrying once.
do_curl() {
    local url="$1"
    local method="${2:-GET}"
    local raw
    raw=$(curl -sS -X "$method" -w $'\n%{http_code}' "$url" 2>/dev/null || printf '\n000')
    _split_body_code "$raw"
    if [[ "$CODE" == "429" ]]; then
        printf "${Y}⚠${N} 429 rate-limit hit — waiting 61s for window reset...\n" >&2
        sleep 61
        raw=$(curl -sS -X "$method" -w $'\n%{http_code}' "$url" 2>/dev/null || printf '\n000')
        _split_body_code "$raw"
    fi
}

check_status() {
    check_status_at "$BASE" "$@"
}

check_portal_status() {
    check_status_at "$PORTAL_BASE" "$@"
}

check_status_at() {
    local base="$1"
    shift
    local path="$1"
    local expected="$2"
    local desc="${3:-$path}"
    local method="${4:-GET}"
    do_curl "${base}${path}" "$method"
    if [[ "$CODE" == "$expected" ]]; then
        printf "${G}✓${N} %s [%s]\n" "$desc" "$CODE"
        PASS=$((PASS + 1))
    else
        printf "${R}✗${N} %s [expected %s, got %s]\n" "$desc" "$expected" "$CODE"
        FAIL=$((FAIL + 1))
        FAIL_LIST+=("$desc")
    fi
    sleep "$THROTTLE"
}

check_health_at() {
    local base="$1"
    local desc="$2"
    do_curl "${base}/health"
    if [[ "$CODE" != "200" ]]; then
        printf "${R}✗${N} %s health [expected 200, got %s]\n" "$desc" "$CODE"
        FAIL=$((FAIL + 1))
        FAIL_LIST+=("$desc health")
        sleep "$THROTTLE"
        return
    fi
    local status version commit
    status=$(printf '%s' "$BODY" | jq -r '.status // ""' 2>/dev/null || echo "")
    version=$(printf '%s' "$BODY" | jq -r '.version // "unknown"' 2>/dev/null || echo "unknown")
    commit=$(printf '%s' "$BODY" | jq -r '.commit // "unknown"' 2>/dev/null || echo "unknown")
    if [[ "$status" != "healthy" ]]; then
        printf "${R}✗${N} %s health [unexpected body: %s]\n" "$desc" "$BODY"
        FAIL=$((FAIL + 1))
        FAIL_LIST+=("$desc health")
    else
        printf "${G}✓${N} %s health [%s %s]\n" "$desc" "$version" "$commit"
        PASS=$((PASS + 1))
    fi
    sleep "$THROTTLE"
}

check_envelope() {
    local path="$1"
    local desc="${2:-$path}"
    do_curl "${BASE}${path}"

    if [[ "$CODE" != "200" ]]; then
        printf "${R}✗${N} %s [HTTP %s]\n" "$desc" "$CODE"
        FAIL=$((FAIL + 1))
        FAIL_LIST+=("$desc")
        sleep "$THROTTLE"
        return
    fi

    local rc
    rc=$(printf '%s' "$BODY" | jq -r '.response.header.resultCode // "MISSING"' 2>/dev/null || echo "PARSE_ERR")
    : "${rc:=PARSE_ERR}"

    case "$rc" in
        "00")
            printf "${G}✓${N} %s [200 + resultCode=00]\n" "$desc"
            PASS=$((PASS + 1))
            ;;
        "22" | "99" | "LIMITED_NUMBER_OF_SERVICE_REQUESTS_EXCEEDS_ERROR")
            printf "${Y}⚠${N} %s [rate-limited resultCode=%s, skipping]\n" "$desc" "$rc"
            WARN=$((WARN + 1))
            ;;
        "MISSING" | "PARSE_ERR")
            printf "${R}✗${N} %s [200 but malformed envelope: %s]\n" "$desc" "$rc"
            FAIL=$((FAIL + 1))
            FAIL_LIST+=("$desc")
            ;;
        *)
            printf "${R}✗${N} %s [resultCode=%s]\n" "$desc" "$rc"
            FAIL=$((FAIL + 1))
            FAIL_LIST+=("$desc")
            ;;
    esac
    sleep "$THROTTLE"
}

# /v1/geo/resolve has its own JSON shape (no `response.header` envelope).
# curl -G with --data-urlencode handles Hangul query safely.
#
# Args: <query> <desc> [expected_status_class]
#   expected_status_class: "200" (default — also asserts lat/lon/name_matched
#   fields) or "4xx" (passes on any 4XX — used for unsupported_input cases).
check_geo() {
    local query="$1"
    local desc="$2"
    local expected="${3:-200}"
    local raw
    raw=$(curl -sS -w $'\n%{http_code}' "${BASE}/v1/geo/resolve" --data-urlencode "q=${query}" -G 2>/dev/null || printf '\n000')
    _split_body_code "$raw"
    if [[ "$CODE" == "429" ]]; then
        printf "${Y}⚠${N} 429 rate-limit hit — waiting 61s for window reset...\n" >&2
        sleep 61
        raw=$(curl -sS -w $'\n%{http_code}' "${BASE}/v1/geo/resolve" --data-urlencode "q=${query}" -G 2>/dev/null || printf '\n000')
        _split_body_code "$raw"
    fi

    if [[ "$expected" == "4xx" ]]; then
        if [[ "$CODE" =~ ^4[0-9]{2}$ ]]; then
            printf "${G}✓${N} %s [%s — 4xx as expected]\n" "$desc" "$CODE"
            PASS=$((PASS + 1))
        else
            printf "${R}✗${N} %s [HTTP %s, expected 4xx]\n" "$desc" "$CODE"
            FAIL=$((FAIL + 1))
            FAIL_LIST+=("$desc")
        fi
        sleep "$THROTTLE"
        return
    fi

    # default: expect 200 + lat/lon/name_matched fields
    if [[ "$CODE" != "200" ]]; then
        printf "${R}✗${N} %s [HTTP %s]\n" "$desc" "$CODE"
        FAIL=$((FAIL + 1))
        FAIL_LIST+=("$desc")
        sleep "$THROTTLE"
        return
    fi
    if printf '%s' "$BODY" | jq -e '.lat and .lon and .name_matched' >/dev/null 2>&1; then
        printf "${G}✓${N} %s [200 + lat/lon/name_matched]\n" "$desc"
        PASS=$((PASS + 1))
    else
        printf "${R}✗${N} %s [200 but missing fields: %s]\n" "$desc" "$BODY"
        FAIL=$((FAIL + 1))
        FAIL_LIST+=("$desc")
    fi
    sleep "$THROTTLE"
}

# Verify /discovery returns 200 + all expected non-empty string keys. Catches
# contract drift (e.g. envvar typo, key rename without deploy sync) at the
# layer integration tests can't see.
check_discovery_keys() {
    local raw
    raw=$(curl -sS -w $'\n%{http_code}' "${PORTAL_BASE}/discovery" 2>/dev/null || printf '\n000')
    _split_body_code "$raw"
    if [[ "$CODE" != "200" ]]; then
        printf "${R}✗${N} discovery [HTTP %s]\n" "$CODE"
        FAIL=$((FAIL + 1))
        FAIL_LIST+=("discovery")
        sleep "$THROTTLE"
        return
    fi
    local missing=()
    for key in api_base_url auth_base_url skills_registry_url space_base_url kakao_relay_url chat_relay_url; do
        if ! printf '%s' "$BODY" | jq -e --arg k "$key" 'has($k) and (.[$k] | type == "string") and (.[$k] | length > 0)' >/dev/null 2>&1; then
            missing+=("$key")
        fi
    done
    if [[ ${#missing[@]} -eq 0 ]]; then
        printf "${G}✓${N} discovery [200 + 6 keys: api_base_url, auth_base_url, skills_registry_url, space_base_url, kakao_relay_url, chat_relay_url]\n"
        PASS=$((PASS + 1))
    else
        printf "${R}✗${N} discovery [200 but missing/empty: %s]\n" "${missing[*]}"
        FAIL=$((FAIL + 1))
        FAIL_LIST+=("discovery: ${missing[*]}")
    fi
    sleep "$THROTTLE"
}

if ! command -v jq >/dev/null 2>&1; then
    printf "${R}✗${N} jq not found — install with 'brew install jq' or 'apt install jq'\n"
    exit 1
fi

echo "=== kittyapi resource smoke: ${BASE} ==="
echo "=== portal identity smoke: ${PORTAL_BASE} ==="

echo
echo "--- Infrastructure ---"
check_health_at "$BASE" "api"
check_health_at "$PORTAL_BASE" "portal"
check_discovery_keys
if [[ "$BASE" != "$PORTAL_BASE" ]]; then
    check_status "/discovery" "404" "api/discovery closed on resource host"
    check_status "/.well-known/jwks.json" "404" "api/jwks closed on resource host"
fi

echo
echo "--- Calendar (KASI SpcdeInfoService) ---"
check_envelope "/v1/calendar/holidays?solYear=2025" "calendar/holidays"
check_envelope "/v1/calendar/anniversaries?solYear=2025" "calendar/anniversaries"
check_envelope "/v1/calendar/solar-terms?solYear=2025" "calendar/solar-terms"

echo
echo "--- Almanac (KASI LrsrCld + RiseSet) ---"
check_envelope "/v1/almanac/lunar-date?solYear=2026&solMonth=05&solDay=01" "almanac/lunar-date"
check_envelope "/v1/almanac/solar-date?lunYear=2026&lunMonth=03&lunDay=15" "almanac/solar-date"
check_envelope "/v1/almanac/sun?locdate=20260501&latitude=37.5665&longitude=126.9780" "almanac/sun"

echo
echo "--- Weather (KMA) ---"
check_envelope "/v1/weather/kma/village-fcst?lat=37.5665&lon=126.978" "weather/village-fcst"
check_envelope "/v1/weather/kma/ultra-srt-ncst?lat=37.5665&lon=126.978" "weather/ultra-srt-ncst"
check_envelope "/v1/weather/kma/ultra-srt-fcst?lat=37.5665&lon=126.978" "weather/ultra-srt-fcst"

echo
echo "--- Air (한국환경공단) ---"
check_envelope "/v1/air/airkorea/realtime/city?sidoName=%EC%84%9C%EC%9A%B8" "air/airkorea/realtime/city (서울)"
check_envelope "/v1/air/airkorea/realtime/station?stationName=%EC%A2%85%EB%A1%9C%EA%B5%AC&dataTerm=DAILY" "air/airkorea/realtime/station (종로구)"
check_envelope "/v1/air/airkorea/forecast?informCode=PM10" "air/airkorea/forecast (PM10)"
check_envelope "/v1/air/airkorea/forecast/weekly" "air/airkorea/forecast/weekly"
check_envelope "/v1/air/airkorea/unhealthy" "air/airkorea/unhealthy"

echo
echo "--- Geo (places DB + addresses fallthrough) ---"
check_geo "강남역" "geo/resolve (강남역, exact)"
check_geo "서울대입구역" "geo/resolve (서울대입구역, alias_override or exact)"
check_geo "강남" "geo/resolve (강남, fuzzy)"
check_geo "Tokyo" "geo/resolve (Tokyo, out-of-korea)" "4xx"

# OAuth: endpoint-level GET smoke (routing/handler liveness).
# Login 동작 자체는 별도 plan (Playwright/headless browser flow).
# 302 = OAuth provider redirect (PKCE state 자동 생성).
# 400 = missing required params — handler reachable + correct error path.
# 401 = auth required without token — middleware reachable.
echo
echo "--- Auth (OAuth + CLI, endpoint liveness only) ---"
check_portal_status "/auth/google" "302" "auth/google (Google redirect)"
check_portal_status "/auth/github" "302" "auth/github (GitHub redirect)"
check_portal_status "/auth/me" "401" "auth/me (no token)"
check_portal_status "/auth/google/callback" "400" "auth/google/callback (no params)"
check_portal_status "/auth/github/callback" "400" "auth/github/callback (no params)"
check_portal_status "/auth/cli/google" "400" "auth/cli/google (no params)"
check_portal_status "/auth/cli/callback" "400" "auth/cli/callback (no params)"
check_portal_status "/auth/token/refresh" "400" "auth/token/refresh POST (no body)" "POST"
check_portal_status "/auth/cli/exchange" "400" "auth/cli/exchange POST (no body)" "POST"

# Plan 23 PR-D — device endpoints. Auth-required routes return 401 to
# anonymous probes; refresh sits OUTSIDE authMW so it returns 400 (no body)
# instead of 401 — that asymmetry is the wire test for 결정 3.
echo
echo "--- Auth (device endpoints, Plan 23 PR-D) ---"
check_portal_status "/auth/devices/pair" "401" "auth/devices/pair (no auth)" "POST"
check_portal_status "/auth/devices/refresh" "400" "auth/devices/refresh (no body, no auth required)" "POST"
check_portal_status "/auth/devices" "401" "auth/devices GET (no auth)"
check_portal_status "/auth/devices/00000000-0000-0000-0000-000000000000" "401" "auth/devices/{id} DELETE (no auth)" "DELETE"

# Plan 25 — web OAuth flow (PKCE + code exchange).
# /auth/web/google: no-params → 400 (handler reachable, validation path).
# /auth/web/exchange: no-body → 400; with Origin header → 403 (BFF boundary
# enforced server-side, independent of cors.AllowedOrigins config).
check_origin_rejected() {
    local path="$1"
    local desc="$2"
    local raw
    raw=$(curl -sS -X POST -H "Origin: https://example.com" -w $'\n%{http_code}' "${PORTAL_BASE}${path}" 2>/dev/null || printf '\n000')
    _split_body_code "$raw"
    if [[ "$CODE" == "403" ]]; then
        printf "${G}✓${N} %s [403, Origin blocked]\n" "$desc"
        PASS=$((PASS + 1))
    else
        printf "${R}✗${N} %s [expected 403, got %s]\n" "$desc" "$CODE"
        FAIL=$((FAIL + 1))
        FAIL_LIST+=("$desc")
    fi
    sleep "$THROTTLE"
}

echo
echo "--- Auth (web OAuth flow, Plan 25) ---"
check_portal_status "/auth/web/google" "400" "auth/web/google (no params)"
check_portal_status "/auth/web/exchange" "400" "auth/web/exchange POST (no body)" "POST"
check_origin_rejected "/auth/web/exchange" "auth/web/exchange (browser Origin blocked)"

TOTAL=$((PASS + FAIL))
echo
echo "=== Summary ==="
printf "Passed: ${G}%d${N}/%d\n" "$PASS" "$TOTAL"
if (( WARN > 0 )); then
    printf "Warned: ${Y}%d${N} (rate-limited, not failed)\n" "$WARN"
fi
if (( FAIL > 0 )); then
    printf "Failed: ${R}%d${N}\n" "$FAIL"
    for f in "${FAIL_LIST[@]}"; do
        printf "  ${R}✗${N} %s\n" "$f"
    done
    exit 1
fi
echo "All smoke checks passed."
