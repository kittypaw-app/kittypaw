#!/usr/bin/env bash
# Local /model command demo harness — isolated KittyPaw home so the user's
# real ~/.kittypaw/ daemon, secrets, and chat history are untouched.
#
# Subcommands:
#   setup   — create $TEST_HOME with config.toml registering 4 free models.
#             Idempotent: existing files are NOT overwritten unless --force.
#   server  — start an isolated daemon on $TEST_PORT (default :3001),
#             logging to /tmp/kittypaw-dev-models.log.
#   chat    — attach the chat REPL to the isolated daemon.
#   stop    — stop the isolated daemon.
#   clean   — remove $TEST_HOME entirely (user-initiated, never automatic).
#   status  — print the current isolation state.
#
# Vendor keys come from the environment — never embedded here, never logged.
# The script refuses to start the daemon if no key is exported (silent
# failure on the first chat turn would be a worse UX).

set -euo pipefail

TEST_HOME="${KITTYPAW_DEV_HOME:-/tmp/kittypaw-dev-models}"
# core/config.go:482: when KITTYPAW_CONFIG_DIR is set it is used verbatim as
# the base directory (no .kittypaw/ join, no os.UserHomeDir lookup), so
# accounts/ and server.toml live directly under $TEST_HOME. This is the
# load-bearing isolation knob — CLAUDE.md "Testing Isolation" 섹션 일관 (Plan
# A T3, 2026-05-06). The daemon and the chat client both honor
# KITTYPAW_CONFIG_DIR via the same code path, so auto-discovery (daemon.pid)
# routes the client to our isolated daemon without explicit base-url plumbing.
TEST_PORT="${KITTYPAW_DEV_PORT:-3001}"
# Loopback by default — the daemon holds vendor API keys and binding to all
# interfaces (0.0.0.0) would expose them to the LAN. Override with
# KITTYPAW_DEV_BIND=0.0.0.0:3001 only when intentional.
TEST_BIND="${KITTYPAW_DEV_BIND:-127.0.0.1:$TEST_PORT}"
LOG_FILE="${KITTYPAW_DEV_LOG:-/tmp/kittypaw-dev-models.log}"

# bin/kittypaw is a sibling of this script's apps/kittypaw root.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APP_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
KP_BIN="${KP_BIN:-$APP_ROOT/bin/kittypaw}"

# Auto-load vendor keys from sibling .env.dev-models if present.
# .env.* is git-ignored (apps/kittypaw/.gitignore) so this never enters the
# repo. Lets `make dev-models-server` work without re-exporting keys in
# every shell. Override with explicit env vars wins (set -a not used).
ENV_FILE="$APP_ROOT/.env.dev-models"
if [[ -f "$ENV_FILE" ]]; then
  # shellcheck disable=SC1090
  source "$ENV_FILE"
fi

cmd="${1:-help}"
shift || true

ensure_binary() {
  if [[ ! -x "$KP_BIN" ]]; then
    echo "bin/kittypaw not built. run: make build" >&2
    exit 1
  fi
}

write_config_if_missing() {
  local force="${1:-no}"
  local cfg="$TEST_HOME/accounts/default/config.toml"
  local sentinel_marker="GENERATED FROM eval/models.toml"
  if [[ -f "$cfg" ]]; then
    if [[ "$force" != "force" ]]; then
      if head -1 "$cfg" | grep -q "$sentinel_marker"; then
        echo "config exists: $cfg (sentinel managed — use --force to regenerate)"
        return
      fi
      # Pre-T2 cfg or user-edited (sentinel missing) — abort to protect edits.
      echo "config exists but unmanaged (no sentinel header): $cfg" >&2
      echo "likely pre-T2 cfg or user-edited. backup before overwrite:" >&2
      echo "  cp '$cfg' '$cfg.bak' && $0 setup --force" >&2
      echo "diff vs generated (-: current, +: would-be generated):" >&2
      diff "$cfg" <(bash "$SCRIPT_DIR/dev-models-config-generate.sh") >&2 || true
      exit 3
    fi
  fi
  ensure_binary
  # Run the setup wizard non-interactively to create account.toml +
  # secrets.json under accounts/default/. (auth.json is server-wide Web
  # UI credentials and is NOT produced by --password-stdin — the chat
  # WS handshake authenticates via server.toml's master_api_key, written
  # below.) The provider/model values here are placeholders — the generator
  # below overwrites config.toml with sentinel-managed eval/models.toml SoT.
  echo 'devpw' | KITTYPAW_CONFIG_DIR="$TEST_HOME" "$KP_BIN" setup \
    --account default \
    --provider local \
    --local-model "placeholder" \
    --local-url "http://localhost:11434/v1" \
    --password-stdin \
    --no-chat \
    --no-service \
    --force >/dev/null
  bash "$SCRIPT_DIR/dev-models-config-generate.sh" > "$cfg"
  # server.toml carries bind + master_api_key (CLAUDE.md "Server-wide
  # settings"). The chat client's DaemonConn reads these to derive
  # BaseURL + APIKey for both health-check and WS auth — without them
  # we get the 401 / 10-second-timeout dance.
  #
  # master_api_key is randomized per setup (16 bytes hex). A literal
  # constant would let any local user on a multi-user host reach the
  # vendor-key-holding daemon, and concurrent dev-models instances on
  # different ports/dirs would share auth. /dev/urandom is portable
  # across macOS / Linux without depending on openssl.
  local master_key
  master_key="$(head -c 16 /dev/urandom | xxd -p -c 32)"
  umask 077
  cat > "$TEST_HOME/server.toml" <<TOML
bind = "$TEST_BIND"
master_api_key = "$master_key"
TOML
  umask 022
  echo "wrote $cfg (generated from eval/models.toml — sentinel managed)"
}

require_keys() {
  local missing=()
  if [[ -z "${GROQ_API_KEY:-}" ]]; then missing+=("GROQ_API_KEY"); fi
  if [[ -z "${MISTRAL_API_KEY:-}" ]]; then missing+=("MISTRAL_API_KEY"); fi
  if [[ -z "${GEMINI_API_KEY:-}" ]]; then missing+=("GEMINI_API_KEY"); fi
  if [[ -z "${OPENROUTER_API_KEY:-}" ]]; then missing+=("OPENROUTER_API_KEY"); fi
  if (( ${#missing[@]} > 0 )); then
    echo "missing env: ${missing[*]}" >&2
    echo "  Groq:       https://console.groq.com/keys" >&2
    echo "  Mistral:    https://console.mistral.ai/api-keys (Experiment plan, no card)" >&2
    echo "  Gemini:     https://aistudio.google.com/apikey" >&2
    echo "  OpenRouter: https://openrouter.ai/keys (free :free models, no card)" >&2
    return 1
  fi
}

case "$cmd" in
setup)
  force="no"
  if [[ "${1:-}" == "--force" ]]; then force="force"; fi
  write_config_if_missing "$force"
  echo "TEST_HOME=$TEST_HOME"
  ;;

server)
  ensure_binary
  require_keys
  if [[ ! -f "$TEST_HOME/accounts/default/config.toml" ]]; then
    write_config_if_missing
  fi
  if lsof -nP -iTCP:"$TEST_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "port :$TEST_PORT already in use — stop the existing daemon first or set KITTYPAW_DEV_PORT" >&2
    exit 1
  fi
  echo "starting daemon on $TEST_BIND (KITTYPAW_CONFIG_DIR=$TEST_HOME, log=$LOG_FILE)"
  KITTYPAW_CONFIG_DIR="$TEST_HOME" "$KP_BIN" server start --bind "$TEST_BIND" > "$LOG_FILE" 2>&1 &
  sleep 2
  if ! lsof -nP -iTCP:"$TEST_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "daemon failed to bind :$TEST_PORT — see $LOG_FILE" >&2
    tail -20 "$LOG_FILE" >&2 || true
    exit 1
  fi
  echo "ready: KITTYPAW_CONFIG_DIR=$TEST_HOME chat → make dev-models-chat"
  ;;

chat)
  ensure_binary
  if ! lsof -nP -iTCP:"$TEST_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "no daemon on :$TEST_PORT — run: make dev-models-server" >&2
    exit 1
  fi
  # KITTYPAW_CONFIG_DIR=$TEST_HOME makes the client's local-discovery
  # path read the isolated daemon.pid we wrote in setup. --remote would
  # bypass discovery but require an auth token (401 on plain WS), so
  # we go through the loopback discovery path instead.
  KITTYPAW_CONFIG_DIR="$TEST_HOME" "$KP_BIN" chat --account default
  ;;

stop)
  ensure_binary
  KITTYPAW_CONFIG_DIR="$TEST_HOME" "$KP_BIN" server stop || true
  ;;

clean)
  if [[ "${1:-}" != "--yes" ]]; then
    echo "this removes $TEST_HOME and $LOG_FILE — re-run with: $0 clean --yes" >&2
    exit 1
  fi
  KITTYPAW_CONFIG_DIR="$TEST_HOME" "$KP_BIN" server stop 2>/dev/null || true
  rm -rf "$TEST_HOME" "$LOG_FILE"
  echo "removed $TEST_HOME and $LOG_FILE"
  ;;

status)
  echo "KITTYPAW_CONFIG_DIR=$TEST_HOME"
  echo "TEST_BIND=$TEST_BIND"
  echo "LOG_FILE=$LOG_FILE"
  if [[ -f "$TEST_HOME/accounts/default/config.toml" ]]; then
    echo "config: present"
  else
    echo "config: missing (run: make dev-models-setup)"
  fi
  if lsof -nP -iTCP:"$TEST_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "daemon: running on :$TEST_PORT"
  else
    echo "daemon: not running"
  fi
  for v in GROQ_API_KEY MISTRAL_API_KEY GEMINI_API_KEY OPENROUTER_API_KEY; do
    if [[ -n "${!v:-}" ]]; then
      echo "$v: set"
    else
      echo "$v: missing"
    fi
  done
  ;;

tunnel-ollama-start)   "$SCRIPT_DIR/tunnel.sh" start  ollama 11500 11434 /api/tags ;;
tunnel-ollama-stop)    "$SCRIPT_DIR/tunnel.sh" stop   ollama ;;
tunnel-ollama-status)  "$SCRIPT_DIR/tunnel.sh" status ollama 11500 /api/tags ;;
tunnel-lms-start)      "$SCRIPT_DIR/tunnel.sh" start  lms    11600 1234  /v1/models ;;
tunnel-lms-stop)       "$SCRIPT_DIR/tunnel.sh" stop   lms ;;
tunnel-lms-status)     "$SCRIPT_DIR/tunnel.sh" status lms    11600 /v1/models ;;

go)
  # One-shot: setup → server → chat. Stops nothing on exit so the user
  # can re-attach with `make dev-models-chat`. Run `make dev-models-stop`
  # when finished. Existing daemon on the same port is reused.
  ensure_binary
  require_keys
  if [[ ! -f "$TEST_HOME/accounts/default/config.toml" ]]; then
    write_config_if_missing
  fi
  if ! lsof -nP -iTCP:"$TEST_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "starting daemon on $TEST_BIND (KITTYPAW_CONFIG_DIR=$TEST_HOME)"
    KITTYPAW_CONFIG_DIR="$TEST_HOME" "$KP_BIN" server start --bind "$TEST_BIND" > "$LOG_FILE" 2>&1 &
    sleep 2
    if ! lsof -nP -iTCP:"$TEST_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
      echo "daemon failed — see $LOG_FILE" >&2
      tail -20 "$LOG_FILE" >&2 || true
      exit 1
    fi
  else
    echo "reusing daemon on :$TEST_PORT"
  fi
  echo "attaching chat (Ctrl-D to exit; server stays up — run 'make dev-models-stop' to kill)"
  KITTYPAW_CONFIG_DIR="$TEST_HOME" "$KP_BIN" chat --account default
  ;;

help|*)
  cat <<USAGE
$(basename "$0") — local /model demo harness

Usage:
  $0 go                       setup + server + chat in one shot (recommended)
  $0 setup [--force]          create isolated config (idempotent)
  $0 server                   start daemon on :$TEST_PORT
  $0 chat                     attach chat REPL
  $0 stop                     stop daemon
  $0 clean --yes              remove TEST_HOME (explicit confirmation)
  $0 status                   print state

  $0 tunnel-ollama-start      SSH tunnel localhost:11500 → emac:11434
  $0 tunnel-ollama-stop       close ollama tunnel via -O exit
  $0 tunnel-ollama-status     2-stage probe (lsof + /api/tags)
  $0 tunnel-lms-start         SSH tunnel localhost:11600 → emac:1234
  $0 tunnel-lms-stop          close lms tunnel via -O exit
  $0 tunnel-lms-status        2-stage probe (lsof + /v1/models)

Env:
  KITTYPAW_DEV_HOME           isolation home (default: /tmp/kittypaw-dev-models)
  KITTYPAW_DEV_PORT           daemon port    (default: 3001)
  KITTYPAW_DEV_BIND           bind address   (default: 127.0.0.1:\$KITTYPAW_DEV_PORT;
                                              loopback only — vendor keys live in
                                              this daemon, override only when LAN
                                              access is intentional)
  GROQ_API_KEY                required for server
  MISTRAL_API_KEY             required for server
  GEMINI_API_KEY              required for server
  OPENROUTER_API_KEY          required for server

Typical flow (Makefile aliases shown):
  export GROQ_API_KEY=gsk_...
  export MISTRAL_API_KEY=...
  export GEMINI_API_KEY=...
  export OPENROUTER_API_KEY=...
  make dev-models                              # one-shot: setup + server + chat
  # or step-by-step:
  make dev-models-setup
  make dev-models-server
  make dev-models-chat                         # in REPL: /model, /model mistral-medium, ...
  make dev-models-stop

Local backend on emac (M3 Pro 36GB, ssh emac alias required):
  make dev-models-tunnel-ollama                # ollama serve  → :11500
  make dev-models-tunnel-lms                   # LM Studio app → :11600 (load model via GUI)
USAGE
  ;;
esac
