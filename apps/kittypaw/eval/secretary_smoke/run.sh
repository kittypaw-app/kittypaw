#!/usr/bin/env bash
# Secretary Smoke runner.
#
# Reads each fixture jsonl, sends the input to KittyPaw via `kittypaw chat`,
# captures the response, then asks a small judge LLM to score each expected
# behavior. Antipattern substrings are matched deterministically (no LLM).
#
# Output: eval/secretary_smoke/results/{category}.jsonl + summary.md
#
# Required env:
#   ANTHROPIC_API_KEY (or read from default tenant config.toml)

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
EVAL_DIR="$ROOT_DIR/eval/secretary_smoke"
FIX_DIR="$EVAL_DIR/fixtures"
OUT_DIR="${OUT_DIR:-$EVAL_DIR/results}"
SUMMARY="${SUMMARY:-$OUT_DIR/summary.md}"
SUMMARY_JSON="${SUMMARY_JSON:-$OUT_DIR/summary.json}"
LOCK_DIR="${LOCK_DIR:-$EVAL_DIR/.runner.lock}"
KITTY_BIN="${KITTY_BIN:-$ROOT_DIR/bin/kittypaw}"
JUDGE_MODEL="${JUDGE_MODEL:-claude-haiku-4-5-20251001}"
RUN_ACCOUNT="${KITTYPAW_ACCOUNT:-auto}"
RUN_PROVIDER="${KITTYPAW_EVAL_PROVIDER:-configured}"
RUN_MODEL="${KITTYPAW_EVAL_MODEL:-configured}"
RUN_SERVER="${KITTYPAW_EVAL_SERVER:-${KITTYPAW_EVAL_DAEMON:-local}}"
FINISHED=0

# T3: --model <id> opt-in flag — config swap + reload + trap restore.
# Default path (flag absent) keeps the existing "configured" behavior intact
# (AC #3, make smoke 회귀 0).
SWAP_MODEL=""
SWAP_BACKUP=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --model)
      [[ -z "${2:-}" ]] && { echo "--model requires <id>" >&2; exit 2; }
      SWAP_MODEL="$2"
      shift 2
      ;;
    --model=*)
      SWAP_MODEL="${1#*=}"
      shift
      ;;
    *)
      echo "unknown flag: $1" >&2
      exit 2
      ;;
  esac
done

if [[ -n "$SWAP_MODEL" ]]; then
  # Protect the user's ~/.kittypaw — --model only swaps inside an isolated cfg dir.
  if [[ -z "${KITTYPAW_CONFIG_DIR:-}" ]]; then
    echo "--model requires KITTYPAW_CONFIG_DIR (사용자 ~/.kittypaw 보호)" >&2
    exit 2
  fi
  # Reject sed-meta / shell-special characters in SWAP_MODEL — defense in depth
  # against arbitrary config injection. eval/models.toml ids are alphanumeric +
  # `._-` only, so this is the canonical id shape.
  if [[ ! "$SWAP_MODEL" =~ ^[A-Za-z0-9._-]+$ ]]; then
    echo "--model id must match ^[A-Za-z0-9._-]+\$ (got: $SWAP_MODEL)" >&2
    exit 2
  fi
  SWAP_CFG="$KITTYPAW_CONFIG_DIR/accounts/default/config.toml"
  if [[ ! -f "$SWAP_CFG" ]]; then
    echo "config.toml not found: $SWAP_CFG" >&2
    exit 2
  fi
  RUN_MODEL="$SWAP_MODEL"
fi

mkdir -p "$OUT_DIR"

write_summary_header() {
  {
    echo "# Secretary Smoke Results"
    echo
    echo "Date: $(date -u +'%Y-%m-%d %H:%M UTC')"
    echo "State: RUNNING"
    echo "Provider: $RUN_PROVIDER"
    echo "Model: $RUN_MODEL"
    echo "Judge model: $JUDGE_MODEL"
    echo "Account: $RUN_ACCOUNT"
    echo "Server: $RUN_SERVER"
    echo
  } > "$SUMMARY"
}

now_ms() {
  python3 -c 'import time; print(int(time.time() * 1000))'
}

cleanup() {
  rmdir "$LOCK_DIR" 2>/dev/null || true
}

# T3: restore [llm].default after --model swap. Reload daemon so it sees the
# pre-swap cfg again. No-op when --model absent. Also clears any .tmp leak
# from an interrupted swap_default.
restore_swap() {
  if [[ -n "${SWAP_CFG:-}" ]]; then
    rm -f "$SWAP_CFG.tmp" 2>/dev/null || true
  fi
  if [[ -n "$SWAP_BACKUP" && -f "$SWAP_BACKUP" ]]; then
    cp "$SWAP_BACKUP" "$SWAP_CFG"
    rm -f "$SWAP_BACKUP"
    "$KITTY_BIN" server reload >/dev/null 2>&1 || true
  fi
}

# T3: swap [llm].default to SWAP_MODEL and reload daemon. Backup retained
# until restore_swap runs (trap EXIT). Refuses to overwrite a stale backup
# from a previously interrupted run — protects against permanent cfg 오염
# when SIGKILL/power-loss skipped restore_swap.
swap_default() {
  SWAP_BACKUP="$SWAP_CFG.swap_backup"
  if [[ -e "$SWAP_BACKUP" ]]; then
    echo "stale backup found: $SWAP_BACKUP — previous run did not restore." >&2
    echo "manual recovery: cp '$SWAP_BACKUP' '$SWAP_CFG' && rm '$SWAP_BACKUP'" >&2
    SWAP_BACKUP=""  # don't let restore_swap touch a stale backup we didn't create
    return 2
  fi
  cp "$SWAP_CFG" "$SWAP_BACKUP"
  # POSIX-portable in-place edit: write to .tmp then mv.
  sed "s|^default = .*|default = \"$SWAP_MODEL\"|" "$SWAP_CFG" > "$SWAP_CFG.tmp"
  mv "$SWAP_CFG.tmp" "$SWAP_CFG"
  if ! "$KITTY_BIN" server reload >/dev/null 2>&1; then
    echo "kittypaw server reload failed after swap" >&2
    return 2
  fi
}

finish() {
  local state="$1"
  local code="$2"
  local detail="${3:-}"
  FINISHED=1
  {
    echo
    echo "State: $state"
    [[ -n "$detail" ]] && echo "Detail: $detail"
  } >> "$SUMMARY"
  cleanup
  echo "STATE: $state"
  [[ -n "$detail" ]] && echo "$detail" >&2
  exit "$code"
}

trap 'rc=$?; restore_swap; if (( rc != 0 && FINISHED == 0 )); then cleanup; echo "STATE: INFRA"; echo "runner aborted with exit $rc" >&2; exit 2; fi; cleanup' EXIT

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    finish INFRA 2 "required command not found: $1"
  fi
}

write_summary_header

# T3: perform swap before KITTYPAW_EVAL_SKIP check so swap+restore runs even
# in skip mode (bats can verify the swap mechanism without running fixtures).
if [[ -n "$SWAP_MODEL" ]]; then
  if ! swap_default; then
    finish INFRA 2 "swap to model=$SWAP_MODEL failed"
  fi
fi

if [[ "${KITTYPAW_EVAL_SKIP:-}" == "1" ]]; then
  finish NOT_RUN 3 "KITTYPAW_EVAL_SKIP=1"
fi

need_cmd jq
need_cmd curl
need_cmd python3

# Single-instance lock (prevents the multi-runner race we hit during the
# first eval pass — multiple bash run.sh writing to results/ simultaneously
# corrupted the per-category jsonl and the summary).
#
# `mkdir` is atomic across POSIX, macOS-friendly (flock is Linux-only).
if ! mkdir "$LOCK_DIR" 2>/dev/null; then
  finish INFRA 2 "Another runner already holds $LOCK_DIR"
fi

# Resolve Anthropic key.
if [[ -z "${ANTHROPIC_API_KEY:-}" ]]; then
  ANTHROPIC_API_KEY=$(grep -m1 'api_key = "sk-ant' ~/.kittypaw/accounts/default/config.toml 2>/dev/null | sed 's/.*"\(sk-ant[^"]*\)".*/\1/')
fi
if [[ -z "$ANTHROPIC_API_KEY" ]]; then
  finish INFRA 2 "ANTHROPIC_API_KEY not set and not found in accounts/default/config.toml"
fi

# Strip ANSI + spinner glyphs + REPL prompts from `kittypaw chat <text>`
# output, leaving only the model's reply. The script is in a sibling file
# because heredoc-as-stdin and pipe-as-stdin collide for `python3 - <<EOF`.
CLEAN_CHAT_PY="$EVAL_DIR/clean_chat.py"
strip_chat_output() {
  python3 "$CLEAN_CHAT_PY"
}

# Substring antipattern check. Returns 0 if any antipattern matched.
check_antipatterns() {
  local response="$1"
  shift
  local hit=0
  for pat in "$@"; do
    if [[ "$response" == *"$pat"* ]]; then
      hit=1
      echo "  antipattern_hit=$pat" >&2
    fi
  done
  return $hit
}

# Behavior definitions — short prompt for the judge LLM.
behavior_def() {
  case "$1" in
    clarify_intent) echo "The response acknowledges the query is ambiguous and either asks a clarifying question or explicitly states a working interpretation before proceeding." ;;
    enumerate_interpretations) echo "The response lists two or more possible interpretations of the user query." ;;
    suggest_skill) echo "The response surfaces that a domain-specific tool/skill could give better information (mentions install, recommend, or a specific skill name)." ;;
    expand_query) echo "The response proposes search refinement: a different keyword, a specific source/site, or asks the user for guidance on where to look next." ;;
    honest_uncertainty) echo "The response explicitly acknowledges uncertainty or limitation in the data/source instead of fabricating a confident answer." ;;
    source_timestamp) echo "The response indicates the time-frame or source of the information (mentions 'as of', 'today', or that data may be outdated)." ;;
    first_person) echo "The response speaks from the assistant's perspective (e.g. 'I checked', '찾아본 결과로는') without framing the tool/search output as something the user provided." ;;
    *) echo "Unknown behavior" ;;
  esac
}

# Ask the judge LLM whether `response` exhibits `behavior` for `query`.
# Echoes one of: PASS / PARTIAL / FAIL.
judge_behavior() {
  local query="$1"
  local response="$2"
  local behavior="$3"
  local def
  def=$(behavior_def "$behavior")

  local body
  body=$(jq -n \
    --arg model "$JUDGE_MODEL" \
    --arg q "$query" \
    --arg r "$response" \
    --arg b "$behavior" \
    --arg d "$def" \
    '{
      model: $model,
      max_tokens: 80,
      messages: [{
        role: "user",
        content: ("You evaluate whether an AI assistant response exhibits a target behavior.\n\nUser query: " + $q + "\n\nAssistant response:\n" + $r + "\n\nBehavior name: " + $b + "\nBehavior definition: " + $d + "\n\nAnswer with strictly one token (PASS / PARTIAL / FAIL) on the first line, then a one-sentence reason.")
      }]
    }')

  local api_response
  if ! api_response=$(curl -fsS https://api.anthropic.com/v1/messages \
    -H "x-api-key: $ANTHROPIC_API_KEY" \
    -H "anthropic-version: 2023-06-01" \
    -H "content-type: application/json" \
    -d "$body"); then
    return 2
  fi
  if echo "$api_response" | jq -e '.error' >/dev/null 2>&1; then
    return 2
  fi

  local result
  result=$(echo "$api_response" | jq -r '.content[0].text // empty' | head -n1 | tr -d '[:space:]' | tr '[:lower:]' '[:upper:]')
  if [[ -z "$result" ]]; then
    return 2
  fi

  case "$result" in
    PASS) echo "PASS" ;;
    PARTIAL) echo "PARTIAL" ;;
    *) echo "FAIL" ;;
  esac
}

# Iteration 2: optional per-category fixture limit. Set to 0 (or unset) for full
# scoring. Set to N>0 to process only first N entries — used by run-models.sh
# wrapper (default 2) to cut cycle time for first iteration 2 measurement.
fixture_lines() {
  local file="$1"
  local limit="${KITTYPAW_EVAL_FIXTURE_LIMIT:-0}"
  if (( limit > 0 )); then
    head -n "$limit" "$file"
  else
    cat "$file"
  fi
}

turn_sleep() {
  local seconds="${KITTYPAW_EVAL_TURN_SLEEP:-0}"
  awk -v s="$seconds" 'BEGIN { exit (s > 0) ? 0 : 1 }' || return 0
  "${SLEEP_BIN:-sleep}" "$seconds"
}

# Score a single fixture file. Echoes an aggregate JSON line.
score_category() {
  local fixture="$1"
  local category
  category=$(basename "$fixture" .jsonl)
  local out="$OUT_DIR/${category}.jsonl"
  : > "$out"

  local total_q=0
  local pass_q=0  # queries with score >= 1.5
  local antipattern_hits=0

  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    total_q=$((total_q + 1))

    local id input expected antipatterns
    id=$(echo "$line" | jq -r '.id')
    input=$(echo "$line" | jq -r '.input')
    expected=$(echo "$line" | jq -r '.expected_behaviors | join(",")')
    antipatterns=$(echo "$line" | jq -r '.antipatterns[]')

    echo "[$category] $id: $input" >&2

    # Run KittyPaw.
    local raw_response started_ms ended_ms latency_ms
    started_ms=$(now_ms)
    if ! raw_response=$("$KITTY_BIN" chat "$input" 2>&1); then
      echo "chat command failed for $category/$id:" >&2
      echo "$raw_response" >&2
      return 2
    fi
    ended_ms=$(now_ms)
    latency_ms=$((ended_ms - started_ms))
    (( latency_ms < 0 )) && latency_ms=0
    local response
    response=$(echo "$raw_response" | strip_chat_output)

    # Antipattern check.
    local antipattern_penalty=0
    while IFS= read -r pat; do
      [[ -z "$pat" ]] && continue
      if [[ "$response" == *"$pat"* ]]; then
        antipattern_penalty=1
        antipattern_hits=$((antipattern_hits + 1))
        echo "    ANTIPATTERN: $pat" >&2
        break
      fi
    done <<< "$antipatterns"

    # Behavior judge calls.
    local points=0
    local total_b=0
    local behavior_results="["
    IFS=',' read -ra bs <<< "$expected"
    for b in "${bs[@]}"; do
      total_b=$((total_b + 1))
      local verdict
      if ! verdict=$(judge_behavior "$input" "$response" "$b"); then
        echo "judge request failed for $category/$id behavior=$b" >&2
        return 2
      fi
      [[ "$verdict" == "PASS" ]] && points=$((points + 2))
      [[ "$verdict" == "PARTIAL" ]] && points=$((points + 1))
      behavior_results+="{\"behavior\":\"$b\",\"verdict\":\"$verdict\"},"
      echo "    $b -> $verdict" >&2
    done
    behavior_results="${behavior_results%,}]"

    # Compute score: (passed / total) * 2 - penalty (0 or 1).
    local score
    if (( total_b > 0 )); then
      score=$(awk "BEGIN { s = ($points / $total_b) - $antipattern_penalty * 0.5; if (s < 0) s = 0; printf \"%.2f\", s }")
    else
      score="0.00"
    fi

    # Track pass count.
    awk -v s="$score" 'BEGIN { exit (s >= 1.5) ? 0 : 1 }' && pass_q=$((pass_q + 1)) || true

    jq -cn \
      --arg id "$id" \
      --arg input "$input" \
      --arg category "$category" \
      --arg response "$response" \
      --argjson behaviors "$behavior_results" \
      --argjson penalty "$antipattern_penalty" \
      --arg score "$score" \
      --argjson latency "$latency_ms" \
      '{id: $id, input: $input, category: $category, response: $response, behaviors: $behaviors, antipattern_penalty: $penalty, score: ($score | tonumber), latency_ms: $latency}' >> "$out"
    turn_sleep
  done < <(fixture_lines "$fixture")

  jq -cn \
    --arg category "$category" \
    --argjson total "$total_q" \
    --argjson pass "$pass_q" \
    --argjson antihit "$antipattern_hits" \
    '{category: $category, total: $total, pass: $pass, antipattern_hits: $antihit}'
}

# Threshold check per category. Returns 0 if passes, 1 if fails.
check_threshold() {
  local agg_json="$1"
  local category total pass antihit
  category=$(echo "$agg_json" | jq -r '.category')
  total=$(echo "$agg_json" | jq -r '.total')
  pass=$(echo "$agg_json" | jq -r '.pass')
  antihit=$(echo "$agg_json" | jq -r '.antipattern_hits')

  local threshold_msg=""
  case "$category" in
    vague)       (( pass >= 6 )) && threshold_msg="PASS" || threshold_msg="FAIL: $pass/8 (need 6+)" ;;
    domain)      (( pass >= 3 )) && threshold_msg="PASS" || threshold_msg="FAIL: $pass/5 (need 3+)" ;;
    weak_serp)   (( pass >= 3 )) && threshold_msg="PASS" || threshold_msg="FAIL: $pass/5 (need 3+)" ;;
    framing)     (( antihit < 2 )) && threshold_msg="PASS" || threshold_msg="FAIL: $antihit antipattern hits (need <2)" ;;
    stale)       (( pass >= 8 )) && threshold_msg="PASS" || threshold_msg="FAIL: $pass/10 (need 8+)" ;;
    *)           threshold_msg="UNKNOWN" ;;
  esac

  echo "$threshold_msg"
}

{
  echo "| Category | Total | Pass (≥1.5) | Antipattern hits | Threshold |"
  echo "|---|---|---|---|---|"
} >> "$SUMMARY"

CATEGORY_JSONL="$OUT_DIR/.summary-categories.jsonl"
: > "$CATEGORY_JSONL"

categories=(vague domain weak_serp framing stale)
overall_pass=0
overall_categories=0

for cat in "${categories[@]}"; do
  fixture="$FIX_DIR/${cat}.jsonl"
  [[ ! -f "$fixture" ]] && { echo "Skipping (no fixture): $cat"; continue; }
  overall_categories=$((overall_categories + 1))
  echo "==== $cat ====" >&2

  if ! agg=$(score_category "$fixture"); then
    finish INFRA 2 "score category failed: $cat"
  fi
  threshold=$(check_threshold "$agg")
  total=$(echo "$agg" | jq -r '.total')
  pass=$(echo "$agg" | jq -r '.pass')
  antihit=$(echo "$agg" | jq -r '.antipattern_hits')

  echo "| $cat | $total | $pass | $antihit | $threshold |" >> "$SUMMARY"
  threshold_pass=false
  if [[ "$threshold" == PASS* ]]; then
    threshold_pass=true
    overall_pass=$((overall_pass + 1))
  fi
  echo "$agg" | jq -c \
    --arg threshold "$threshold" \
    --argjson threshold_pass "$threshold_pass" \
    '. + {threshold: $threshold, threshold_pass: $threshold_pass}' >> "$CATEGORY_JSONL"
done

total_questions=$(jq -s 'map(.total) | add // 0' "$CATEGORY_JSONL")
total_pass=$(jq -s 'map(.pass) | add // 0' "$CATEGORY_JSONL")
korean_score=$(jq -n --argjson pass "$total_pass" --argjson total "$total_questions" \
  'if $total == 0 then 0 else ($pass / $total) end')
latency_p95_ms=$(jq -s '
  [.[].latency_ms? // empty] | sort |
  if length == 0 then 0 else .[((length - 1) * 95 / 100 | floor)] end
' "$OUT_DIR"/*.jsonl)
run_status="fail"
(( overall_pass >= 4 )) && run_status="pass"

jq -n \
  --arg status "$run_status" \
  --argjson korean_score "$korean_score" \
  --argjson latency_p95_ms "$latency_p95_ms" \
  --argjson overall_pass "$overall_pass" \
  --argjson overall_categories "$overall_categories" \
  --slurpfile categories "$CATEGORY_JSONL" \
  '{
    status: $status,
    korean_score: $korean_score,
    latency_p95_ms: $latency_p95_ms,
    overall_pass: $overall_pass,
    overall_categories: $overall_categories,
    categories: $categories
  }' > "$SUMMARY_JSON"
rm -f "$CATEGORY_JSONL"

{
  echo
  echo "**Overall: $overall_pass / $overall_categories categories passed.**"
  echo
  if (( overall_pass >= 4 )); then
    echo "Sub-plan A pass criterion (4/5 categories) MET ✅"
  else
    echo "Sub-plan A pass criterion (4/5 categories) NOT MET ❌"
  fi
} >> "$SUMMARY"

cat "$SUMMARY"
if (( overall_pass >= 4 )); then
  finish PASS 0
else
  finish FAIL 1 "Sub-plan A pass criterion not met: $overall_pass / $overall_categories categories"
fi
