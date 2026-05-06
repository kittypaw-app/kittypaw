#!/usr/bin/env bash
# Plan B Iteration 1 — per-run isolated daemon, 7 models sequential.
#
# Pipeline per model:
#   write isolated cfg (KITTYPAW_CONFIG_DIR=$EVAL_TMP) → server start →
#   readiness probe (/healthz AND first chat) → secretary_smoke --model $id →
#   record manifest → server stop → cfg cleanup.
#
# Daemon start fail / readiness timeout → record status=fail + 다음 모델 진행
# (whole-run abort 금지 — 1개 모델 실패가 7×N 무효화하면 안 됨).
#
# Auth fail-fast: api_key_env 필드 있는 entry의 env var set 확인.
# omit entry (lmstudio) 자동 skip.
#
# Judge consistency sanity (epsilon=0) — same prompt 2회 호출 → s1==s2 검증.
# EVAL_SKIP_JUDGE_CHECK=1 로 skip 가능 (bats fixture).
#
# Output: eval/runs/<runID>/manifest.json (per-model status + judge_consistency).

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EVAL_DIR="$ROOT_DIR/eval"
MODELS_TOML="${MODELS_TOML:-$EVAL_DIR/models.toml}"
PARSE="${PARSE:-$EVAL_DIR/parse-models.py}"
SECRETARY_RUN="${SECRETARY_RUN:-$EVAL_DIR/secretary_smoke/run.sh}"
KITTY_BIN="${KITTY_BIN:-$ROOT_DIR/bin/kittypaw}"
EVAL_PORT="${EVAL_PORT:-13007}"
EVAL_BIND="${EVAL_BIND:-127.0.0.1:$EVAL_PORT}"
DAEMON_PID=""
JUDGE_MODEL="${JUDGE_MODEL:-claude-haiku-4-5-20251001}"
READINESS_TIMEOUT="${READINESS_TIMEOUT:-30}"

RUN_ID="${RUN_ID:-$(date +%s)-$$}"
EVAL_TMP="${EVAL_TMP:-${TMPDIR:-/tmp}/kittypaw-eval-$RUN_ID}"
RUNS_ROOT="${RUNS_ROOT:-$EVAL_DIR/runs}"
RUN_DIR="$RUNS_ROOT/$RUN_ID"
MANIFEST="$RUN_DIR/manifest.json"

# ---------- preflight ----------

# Parse models.toml.
if ! MODELS_JSON="$(uv run python "$PARSE" "$MODELS_TOML" 2>/dev/null)"; then
  echo "failed to parse $MODELS_TOML" >&2
  exit 2
fi

# Auth fail-fast — env var present for entries with non-empty api_key_env.
missing=()
while IFS= read -r env; do
  [[ -z "$env" ]] && continue
  if [[ -z "${!env:-}" ]]; then
    missing+=("$env")
  fi
done < <(echo "$MODELS_JSON" | jq -r '.model[].api_key_env // empty')
if (( ${#missing[@]} > 0 )); then
  echo "missing env: ${missing[*]}" >&2
  exit 2
fi

# Anthropic key (judge) — separate.
if [[ -z "${ANTHROPIC_API_KEY:-}" ]]; then
  echo "missing env: ANTHROPIC_API_KEY (judge)" >&2
  exit 2
fi

# Port pre-flight — confused-deputy 차단 (사용자 live daemon이 :EVAL_PORT 잡고
# 있으면 우리 server start가 silent fail + readiness probe가 user daemon에
# chat/healthz 보내서 ~/.kittypaw 오염 가능).
if lsof -nP -iTCP:"$EVAL_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
  echo "port :$EVAL_PORT already in use — set EVAL_PORT to a free port (확인: lsof -iTCP:$EVAL_PORT)" >&2
  exit 2
fi

# TMPDIR write — fail-fast with message.
if ! mkdir -p "$EVAL_TMP/accounts/default" 2>/dev/null; then
  echo "cannot create $EVAL_TMP — check write perms or override TMPDIR" >&2
  exit 2
fi
# trap EXIT — RUN_ID 포함된 EVAL_TMP만 wipe (hostile EVAL_TMP=/etc 등 override
# 차단). DAEMON_PID 캡처되어 있으면 kill + wait (좀비 daemon 누수 차단).
trap 'on_exit' EXIT
on_exit() {
  if [[ -n "$DAEMON_PID" ]]; then
    kill "$DAEMON_PID" 2>/dev/null || true
    wait "$DAEMON_PID" 2>/dev/null || true
  fi
  if [[ -n "$RUN_ID" && "$EVAL_TMP" == *"$RUN_ID"* ]]; then
    rm -rf "$EVAL_TMP"
  fi
}

mkdir -p "$RUN_DIR"
echo '{"runID": "'"$RUN_ID"'", "models": []}' > "$MANIFEST"

# ---------- judge consistency sanity ----------

judge_call() {
  local prompt="$1"
  # ANTHROPIC_API_KEY를 curl argv에 직접 넣으면 ps/proc로 노출됨. config file
  # (mode 0600 via umask 077)로 우회 — secret이 argv에 들어가지 않는다.
  local cfg="$EVAL_TMP/.curl-judge.cfg"
  umask 077
  cat > "$cfg" <<HDR
header = "x-api-key: $ANTHROPIC_API_KEY"
header = "anthropic-version: 2023-06-01"
header = "content-type: application/json"
HDR
  umask 022
  curl -fsS --config "$cfg" https://api.anthropic.com/v1/messages \
    -d "$(jq -n --arg model "$JUDGE_MODEL" --arg p "$prompt" \
      '{model: $model, max_tokens: 10, messages: [{role: "user", content: $p}]}')" \
    | jq -r '.content[0].text // empty' | tr -d '[:space:]' | tr '[:lower:]' '[:upper:]'
}

judge_consistency_check() {
  local prompt="Reply with exactly one word: PING"
  local r1 r2
  if ! r1=$(judge_call "$prompt"); then return 2; fi
  if ! r2=$(judge_call "$prompt"); then return 2; fi
  jq --arg s1 "$r1" --arg s2 "$r2" '.judge_consistency = [$s1, $s2]' \
    "$MANIFEST" > "$MANIFEST.tmp" && mv "$MANIFEST.tmp" "$MANIFEST"
  if [[ "$r1" != "$r2" ]]; then
    echo "judge consistency check failed (epsilon=0): '$r1' != '$r2'" >&2
    return 2
  fi
}

if [[ "${EVAL_SKIP_JUDGE_CHECK:-}" != "1" ]]; then
  if ! judge_consistency_check; then exit 2; fi
fi

# ---------- per-model pipeline ----------

write_model_cfg() {
  local id="$1" provider="$2" model="$3"
  mkdir -p "$EVAL_TMP/accounts/default"
  cat > "$EVAL_TMP/accounts/default/config.toml" <<CFG
# <!-- GENERATED FROM eval/run-models.sh -->

[llm]
default = "$id"

[[llm.models]]
id = "$id"
provider = "$provider"
model = "$model"
max_tokens = 1024
CFG
  local master_key
  master_key="$(head -c 16 /dev/urandom | xxd -p -c 32)"
  umask 077
  cat > "$EVAL_TMP/server.toml" <<CFG
bind = "$EVAL_BIND"
master_api_key = "$master_key"
CFG
  umask 022
}

start_daemon() {
  local log="$EVAL_TMP/daemon.log"
  KITTYPAW_CONFIG_DIR="$EVAL_TMP" "$KITTY_BIN" server start --bind "$EVAL_BIND" > "$log" 2>&1 &
  DAEMON_PID=$!
  local elapsed=0
  while (( elapsed < READINESS_TIMEOUT )); do
    if curl -fsS "http://$EVAL_BIND/healthz" >/dev/null 2>&1 \
       && KITTYPAW_CONFIG_DIR="$EVAL_TMP" "$KITTY_BIN" chat "ping" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
    elapsed=$((elapsed + 1))
  done
  return 2
}

stop_daemon() {
  KITTYPAW_CONFIG_DIR="$EVAL_TMP" "$KITTY_BIN" server stop >/dev/null 2>&1 || true
  if [[ -n "$DAEMON_PID" ]]; then
    kill "$DAEMON_PID" 2>/dev/null || true
    wait "$DAEMON_PID" 2>/dev/null || true
    DAEMON_PID=""
  fi
  rm -rf "$EVAL_TMP/accounts/default" 2>/dev/null || true
}

record_model() {
  local id="$1" status="$2" detail="${3:-}"
  jq --arg id "$id" --arg status "$status" --arg detail "$detail" \
    '.models += [{"id": $id, "status": $status, "detail": $detail}]' \
    "$MANIFEST" > "$MANIFEST.tmp" && mv "$MANIFEST.tmp" "$MANIFEST"
}

# Sequential per-model. Process substitution avoids subshell variable scoping
# issues that a `jq | while` pipe would introduce.
while IFS=$'\t' read -r id provider model; do
  echo "[$id] starting daemon..." >&2
  write_model_cfg "$id" "$provider" "$model"

  if ! start_daemon; then
    echo "[$id] daemon start / readiness failed" >&2
    record_model "$id" "fail" "daemon readiness timeout"
    stop_daemon
    continue
  fi

  echo "[$id] running secretary_smoke..." >&2
  if KITTYPAW_CONFIG_DIR="$EVAL_TMP" "$SECRETARY_RUN" --model "$id" >/dev/null 2>&1; then
    record_model "$id" "pass" ""
  else
    record_model "$id" "fail" "secretary_smoke failed"
  fi

  stop_daemon
done < <(echo "$MODELS_JSON" | jq -r '.model[] | [.id, .provider, .model] | @tsv')

echo "manifest: $MANIFEST"
