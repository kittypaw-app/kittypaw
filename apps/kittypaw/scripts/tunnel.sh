#!/usr/bin/env bash
#
# Generic SSH LocalForward helper for KittyPaw dev-models harness.
#
# Each tunnel is identified by <name> and bound to a unique ControlPath
# (/tmp/kittypaw-tunnel-<name>.sock) so multiple backends (ollama, lms,
# llamacpp) can coexist without multiplex collision. Single-host scope:
# all tunnels target the `emac` ssh alias. Multi-host extension would
# need a host-keyed ControlPath suffix.
#
# Why this exists:
#   - dev-models.sh used to inline 6 near-identical tunnel-{ollama,lms}-{
#     start,stop,status} functions. Each backend (and any future llamacpp)
#     adds another 3 functions of 90% boilerplate. Generic helper folds
#     that into 4 args + a single implementation.
#   - `ssh -fN` exits 0 even when the LocalForward bind fails ("Address
#     already in use" lands on stderr but the process still forks the
#     ControlMaster). Without an explicit post-spawn lsof probe a stale
#     manual tunnel silently masks the new bind. Verified 2026-05-05.
#
# Usage:
#   tunnel.sh start  <name> <local-port> <remote-port> <probe-path>
#   tunnel.sh stop   <name>
#   tunnel.sh status <name> <local-port> <probe-path>
#
# probe-path examples: /api/tags (ollama), /v1/models (lmstudio).

set -euo pipefail

control_path() {
  # USER suffix prevents collision when multiple devs share a host;
  # /tmp has the sticky bit, but the socket name is otherwise predictable.
  echo "/tmp/kittypaw-tunnel-${USER:-$(id -un)}-$1.sock"
}

ssh_opts_for() {
  local name="$1"
  cat <<EOF
-o ServerAliveInterval=10
-o ServerAliveCountMax=3
-o ConnectTimeout=3
-o ControlPath=$(control_path "$name")
EOF
}

cmd_start() {
  local name="$1" local_port="$2" remote_port="$3" probe_path="$4"
  local opts=()
  while IFS= read -r line; do opts+=("$line"); done < <(ssh_opts_for "$name")

  # Idempotent stop before fresh tunnel — `|| true` because no Master is
  # the normal case after a clean previous run.
  ssh "${opts[@]}" -O exit emac >/dev/null 2>&1 || true
  ssh "${opts[@]}" -fN -o ControlMaster=auto \
      -L "${local_port}:localhost:${remote_port}" emac

  for _ in 1 2 3 4 5; do
    if lsof -nP -iTCP:"$local_port" -sTCP:LISTEN >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done
  if ! lsof -nP -iTCP:"$local_port" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "tunnel-${name}-start: forward bind failed (lsof :${local_port} empty after spawn)" >&2
    echo "  diagnose: lsof -nP -iTCP:${local_port}   # which process holds the port?" >&2
    echo "  if it is a stale ssh: kill the PID, then re-run this target." >&2
    exit 1
  fi
  echo "tunnel up: localhost:${local_port} → emac:${remote_port}  (stop: tunnel.sh stop ${name})"
}

cmd_stop() {
  local name="$1"
  local opts=()
  while IFS= read -r line; do opts+=("$line"); done < <(ssh_opts_for "$name")

  # Idempotent: no Master = nothing to stop = exit 0. Single failure
  # case (ssh -O exit fails when no Master exists) becomes a no-op.
  ssh "${opts[@]}" -O exit emac >/dev/null 2>&1 || true
}

cmd_status() {
  local name="$1" local_port="$2" probe_path="$3"
  if ! lsof -nP -iTCP:"$local_port" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "tunnel down — tunnel.sh start ${name} ..." >&2
    exit 1
  fi
  if ! curl -fsS --max-time 5 "http://localhost:${local_port}${probe_path}" >/dev/null 2>&1; then
    echo "tunnel orphan (forward unreachable) — restart: tunnel.sh stop ${name} && tunnel.sh start ${name} ..." >&2
    exit 1
  fi
  echo "tunnel ok: localhost:${local_port} → emac (probe ${probe_path} responding)"
}

case "${1:-}" in
  start)  shift; cmd_start  "$@" ;;
  stop)   shift; cmd_stop   "$@" ;;
  status) shift; cmd_status "$@" ;;
  *)
    echo "usage: tunnel.sh start|stop|status <name> [<local-port> <remote-port> <probe-path>]" >&2
    exit 2
    ;;
esac
