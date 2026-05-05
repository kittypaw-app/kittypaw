#!/usr/bin/env bash
# Measure an {ollama|lmstudio} model running on emac via the dev-models
# harness. Pulls the model on emac (ollama only — LM Studio app GUI handles
# model load), temporarily swaps `[llm].default` in the dev-models config to
# point at the target model, hits POST /api/v1/reload so the daemon picks up
# the new default, fires one POST /api/v1/chat round, prints latency +
# response, then a trap restores the original config (and reloads again) on
# any EXIT / INT / TERM.
#
# Usage:
#   scripts/dev-models-measure.sh ollama   <model> [prompt]
#   scripts/dev-models-measure.sh lmstudio <model> [prompt]
#
# Architectural notes (load-bearing):
# - core.ChatPayload (core/types.go:97) has no `model` field, and the
#   handler at server/api.go:472 calls Session.Run with `nil` RunOptions —
#   so POST /api/v1/chat always uses `[llm].default`. The only swap path
#   that doesn't touch KittyPaw core is config edit + /api/v1/reload.
# - Reload reconciles channels too (server/api.go:603), but dev-models
#   never wires a [[channels]] block, so the reconcile is a noop.
# - jq -n --arg builds the JSON body, so a hostile prompt with quotes,
#   newlines, or dollar signs cannot break the payload. Naive printf
#   interpolation would.
# - awk parses master_api_key from server.toml. More resilient than
#   `grep | cut -d'"' -f2`: tolerates leading whitespace, doesn't
#   mis-match a future `default_account = "..."` line, robust to format
#   tweaks.
# - The 5-second curl --max-time on probes (/api/tags, /v1/models,
#   /api/v1/reload) is intentionally shorter than the chat's 120s — a
#   slow probe is itself a symptom worth surfacing fast.
# - LM Studio HTTP server runs unauthenticated on emac:1234 and exposes
#   the OpenAI Chat Completions wire shape (see MODEL_GUIDE § 3). The
#   `lms` CLI is intentionally unused per § 3.4 daemon-stall fact —
#   models are loaded via app GUI before measurement.

set -euo pipefail

usage() {
  echo "usage: $0 <backend> <model> [prompt]" >&2
  echo "  backend: ollama | lmstudio" >&2
  exit 1
}

[[ $# -ge 2 ]] || usage
BACKEND="$1"
MODEL="$2"
PROMPT="${3:-한국어로 자기소개 한 줄 + Go로 fizzbuzz 함수 한 줄}"

case "$BACKEND" in
  ollama)
    PROBE_PORT=11500
    PROBE_PATH="/api/tags"
    BASE_URL="http://localhost:11500/v1/chat/completions"
    PROVIDER="ollama"
    CONFIG_ID="ollama-measure"
    CONTROL_PATH="/tmp/kittypaw-dev-models-tunnel-ollama-%C"
    NEEDS_PULL=1
    ;;
  lmstudio)
    PROBE_PORT=11600
    PROBE_PATH="/v1/models"
    BASE_URL="http://localhost:11600/v1/chat/completions"
    PROVIDER="lmstudio"
    CONFIG_ID="lmstudio-measure"
    CONTROL_PATH="/tmp/kittypaw-dev-models-tunnel-lms-%C"
    NEEDS_PULL=0
    ;;
  *)
    echo "unsupported backend: $BACKEND (must be ollama | lmstudio)" >&2
    usage
    ;;
esac

# T0 prereq — fail fast if any dependency is missing. `ollama` is only
# required for the ollama backend; LM Studio is GUI-managed.
PREREQ=(ssh jq lsof curl awk sed)
[[ "$NEEDS_PULL" == "1" ]] && PREREQ+=(ollama)
for cmd in "${PREREQ[@]}"; do
  command -v "$cmd" >/dev/null 2>&1 \
    || { echo "missing prerequisite: $cmd" >&2; exit 1; }
done

TEST_HOME="${KITTYPAW_DEV_HOME:-/tmp/kittypaw-dev-models}"
TEST_PORT="${KITTYPAW_DEV_PORT:-3001}"
CFG="$TEST_HOME/accounts/default/config.toml"
BACKUP="$(mktemp -t dev-models-cfg.XXXXXX)"

SSH_OPTS=(-o ServerAliveInterval=10 -o ServerAliveCountMax=3
          -o ConnectTimeout=3
          -o "ControlPath=$CONTROL_PATH")

MASTER_KEY=$(awk -F'"' '/^master_api_key/{print $2}' "$TEST_HOME/server.toml" 2>/dev/null || true)
if [[ -z "${MASTER_KEY:-}" ]]; then
  echo "master_api_key not found in $TEST_HOME/server.toml — run: make dev-models-setup" >&2
  exit 1
fi

# Cleanup trap: restore config + reload daemon on EXIT/INT/TERM. The trap
# must be installed AFTER MASTER_KEY is captured (restore needs it for the
# reload call) but BEFORE the first ssh probe so an early failure path
# still cleans up if config was already partially mutated.
restore() {
  rc=$?
  if [ -f "$BACKUP" ]; then
    mv "$BACKUP" "$CFG" 2>/dev/null || true
    curl -fsS --max-time 5 -X POST "http://127.0.0.1:$TEST_PORT/api/v1/reload" \
      -H "Authorization: Bearer $MASTER_KEY" >/dev/null 2>&1 || true
  fi
  exit $rc
}
trap restore EXIT INT TERM

# 1. ssh emac health
ssh "${SSH_OPTS[@]}" emac true 2>/dev/null \
  || { echo "ssh emac fail — emac off, sleep, or alias missing in ~/.ssh/config" >&2; exit 1; }

# 2. tunnel two-stage probe (Architect): lsof catches the local bind, curl
#    catches an "orphan" ControlSocket where the SSH session was reset but
#    the local LocalForward bind survives. ollama uses /api/tags, LM Studio
#    uses /v1/models — both are unauthenticated GET endpoints.
lsof -nP -iTCP:"$PROBE_PORT" -sTCP:LISTEN >/dev/null 2>&1 \
  || { echo "tunnel down — make dev-models-tunnel-$BACKEND" >&2; exit 1; }
curl -fsS --max-time 5 "http://localhost:$PROBE_PORT$PROBE_PATH" >/dev/null 2>&1 \
  || { echo "tunnel orphan (forward unreachable) — restart: make dev-models-tunnel-$BACKEND-stop && make dev-models-tunnel-$BACKEND" >&2; exit 1; }

# 3. daemon health
lsof -nP -iTCP:"$TEST_PORT" -sTCP:LISTEN >/dev/null 2>&1 \
  || { echo "kittypaw daemon not listening on :$TEST_PORT — make dev-models" >&2; exit 1; }

# 4. backend-specific model load
if [[ "$NEEDS_PULL" == "1" ]]; then
  # emac SSH non-interactive PATH frequently lacks /usr/local/bin and
  # /opt/homebrew/bin (only ~/.cargo/bin:/usr/bin:/bin:/usr/sbin:/sbin),
  # so `ollama` lookup fails even when the binary is installed and the
  # server is running. Probe explicit install paths so the user does not
  # have to edit ~/.zprofile or ~/.ssh/environment.
  echo "[1/4] ollama pull $MODEL on emac..."
  EMAC_OLLAMA=$(ssh "${SSH_OPTS[@]}" emac \
    'command -v ollama 2>/dev/null || \
     for p in /usr/local/bin/ollama /opt/homebrew/bin/ollama; do \
       [ -x "$p" ] && echo "$p" && break; \
     done' 2>/dev/null)
  if [[ -z "$EMAC_OLLAMA" ]]; then
    echo "ollama not found on emac (checked PATH, /usr/local/bin, /opt/homebrew/bin)" >&2
    echo "  install: ssh emac 'brew install ollama' or https://ollama.com/download" >&2
    exit 1
  fi
  ssh "${SSH_OPTS[@]}" emac "$EMAC_OLLAMA pull $MODEL" \
    || { echo "ollama pull failed — check network / disk on emac" >&2; exit 1; }
else
  # LM Studio: load $MODEL via `lms` CLI on emac. The earlier § 3.4 fact
  # ("lms daemon stall, re-produced 2x") was scoped to `lms get` (model
  # download); the CLI itself (load/ls/ps) is fine — verified 2026-05-05
  # against LM Studio 0.4.12+1 + lms commit 0b2a176. `-y` auto-approves
  # the disambiguation prompt that would otherwise hang non-interactive
  # SSH (first match wins; modelKey, not the full mlx-community/... path).
  # `--gpu max` forces full Apple Metal offload (cold load ~9.67s vs
  # ~30s on auto, n=1). `--ttl 300` auto-unloads after 5 min idle so a
  # measurement run doesn't leak 17 GB indefinitely.
  echo "[1/4] LM Studio: load $MODEL via lms CLI on emac..."
  EMAC_LMS=$(ssh "${SSH_OPTS[@]}" emac \
    'command -v lms 2>/dev/null || \
     for p in ~/.lmstudio/bin/lms ~/.cache/lm-studio/bin/lms /usr/local/bin/lms /opt/homebrew/bin/lms; do \
       [ -x "$p" ] && echo "$p" && break; \
     done' 2>/dev/null)
  if [[ -z "$EMAC_LMS" ]]; then
    echo "lms CLI not found on emac (checked PATH, ~/.lmstudio/bin, ~/.cache/lm-studio/bin, /usr/local/bin, /opt/homebrew/bin)" >&2
    echo "  install: LM Studio app on emac → Settings → Developer → 'Install lms CLI'" >&2
    exit 1
  fi
  ssh "${SSH_OPTS[@]}" emac "$EMAC_LMS load \"$MODEL\" -y --gpu max --ttl 300" \
    || { echo "lms load failed for $MODEL — verify modelKey: ssh emac '$EMAC_LMS ls'" >&2; exit 1; }
fi

# 5. config swap + reload
echo "[2/4] config swap + reload..."
cp "$CFG" "$BACKUP"
{
  echo
  echo '[[llm.models]]'
  printf 'id = "%s"\n' "$CONFIG_ID"
  printf 'provider = "%s"\n' "$PROVIDER"
  printf 'model = "%s"\n' "$MODEL"
  echo 'max_tokens = 1024'
  # Backend-specific base_url targets the appropriate SSH tunnel local
  # bind (ollama :11500, lmstudio :11600). Override the registry default
  # so the daemon dials the tunnel, not localhost:11434 / localhost:11600
  # directly (the registry defaults assume a tunnel the daemon doesn't
  # open itself).
  printf 'base_url = "%s"\n' "$BASE_URL"
} >> "$CFG"
case "$(uname -s)" in
  Darwin) sed -i '' "s/^default = \".*\"/default = \"$CONFIG_ID\"/" "$CFG" ;;
  *)      sed -i "s/^default = \".*\"/default = \"$CONFIG_ID\"/" "$CFG" ;;
esac
curl -fsS --max-time 5 -X POST "http://127.0.0.1:$TEST_PORT/api/v1/reload" \
  -H "Authorization: Bearer $MASTER_KEY" >/dev/null

# 6. chat
echo "[3/4] POST /api/v1/chat..."
MODEL_SAFE=$(printf '%s' "$MODEL" | tr -c 'a-zA-Z0-9._-' '-')
SESSION_ID="measure-${BACKEND}-${MODEL_SAFE}-$$"
JSON=$(jq -nc --arg t "$PROMPT" --arg s "$SESSION_ID" '{text:$t, session_id:$s}')
START=$(date +%s)
RESP=$(curl -fsS --max-time 120 -X POST "http://127.0.0.1:$TEST_PORT/api/v1/chat" \
  -H "Authorization: Bearer $MASTER_KEY" \
  -H "Content-Type: application/json" \
  -d "$JSON")
END=$(date +%s)
LATENCY_S=$((END - START))

# 7. result
echo "[4/4] response (${LATENCY_S}s):"
echo "$RESP" | jq -r '.response // .error // .'
echo
echo "User to record in MODEL_GUIDE (§ 3 for LM Studio MLX, § 5.x for ollama):"
echo "  - quality (1=fail .. 5=perfect)"
echo "  - latency cold/warm — run twice to capture model loading effect"
echo "  - context_window observed in actual reply"

# trap EXIT will restore + reload
