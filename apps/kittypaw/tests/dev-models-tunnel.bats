#!/usr/bin/env bats
# T2 — tunnel-{ollama,lms}-{start,stop,status} bats unit tests.
# Run: bats apps/kittypaw/tests/dev-models-tunnel.bats
#
# Mocks ssh / lsof / curl by prepending $TEST_DIR/bin to PATH so the harness
# never touches a real SSH host or daemon. Each call is recorded to a log so
# the test can assert option flags (-O exit, -fN, ServerAliveInterval, ports,
# ControlPath suffixes, /api/tags vs /v1/models, etc).

setup() {
  TESTS_DIR="$(cd "$(dirname "${BATS_TEST_FILENAME}")" && pwd)"
  SCRIPT_PATH="$(cd "$TESTS_DIR/../scripts" && pwd)/dev-models.sh"
  export TEST_DIR="$(mktemp -d -t bats-tunnel.XXXXXX)"
  export KITTYPAW_DEV_HOME="$TEST_DIR/dev-models"
  export KITTYPAW_DEV_PORT=13001
  export BATS_SSH_LOG="$TEST_DIR/ssh.log"
  export BATS_CURL_LOG="$TEST_DIR/curl.log"
  mkdir -p "$TEST_DIR/bin" "$KITTYPAW_DEV_HOME"
  : > "$BATS_SSH_LOG"
  : > "$BATS_CURL_LOG"
  export PATH="$TEST_DIR/bin:$PATH"

  # Default mocks — tests override per-case where needed.
  cat > "$TEST_DIR/bin/ssh" <<'SSH'
#!/usr/bin/env bash
echo "ssh $*" >> "$BATS_SSH_LOG"
exit 0
SSH
  chmod +x "$TEST_DIR/bin/ssh"

  cat > "$TEST_DIR/bin/lsof" <<'LSOF'
#!/usr/bin/env bash
exit 1   # default: nothing listening
LSOF
  chmod +x "$TEST_DIR/bin/lsof"

  cat > "$TEST_DIR/bin/curl" <<'CURL'
#!/usr/bin/env bash
echo "curl $*" >> "$BATS_CURL_LOG"
exit 0
CURL
  chmod +x "$TEST_DIR/bin/curl"
}

teardown() {
  rm -rf "$TEST_DIR"
}

# ---------------------------------------------------------------------------
# tunnel-ollama-start
# ---------------------------------------------------------------------------

@test "tunnel-ollama-start: idempotent stop runs before fresh tunnel" {
  run "$SCRIPT_PATH" tunnel-ollama-start
  [ "$status" -eq 0 ]
  # Architect-required: idempotent stop precedes the new -fN.
  grep -q -- "-O exit" "$BATS_SSH_LOG"
  grep -qE "\-fN.*-L 11500:localhost:11434 emac" "$BATS_SSH_LOG"
}

@test "tunnel-ollama-start: ControlMaster + ollama-suffixed ControlPath" {
  run "$SCRIPT_PATH" tunnel-ollama-start
  [ "$status" -eq 0 ]
  grep -q "ControlMaster=auto" "$BATS_SSH_LOG"
  grep -q "ControlPath=/tmp/kittypaw-dev-models-tunnel-ollama-%C" "$BATS_SSH_LOG"
}

@test "tunnel-ollama-start: SSH keepalive options included (Architect spec)" {
  run "$SCRIPT_PATH" tunnel-ollama-start
  [ "$status" -eq 0 ]
  grep -q "ServerAliveInterval=10" "$BATS_SSH_LOG"
  grep -q "ServerAliveCountMax=3" "$BATS_SSH_LOG"
}

# ---------------------------------------------------------------------------
# tunnel-ollama-stop
# ---------------------------------------------------------------------------

@test "tunnel-ollama-stop: invokes ssh -O exit with ollama-suffixed ControlPath" {
  run "$SCRIPT_PATH" tunnel-ollama-stop
  [ "$status" -eq 0 ]
  grep -q -- "-O exit" "$BATS_SSH_LOG"
  grep -q "ControlPath=/tmp/kittypaw-dev-models-tunnel-ollama-%C" "$BATS_SSH_LOG"
}

# ---------------------------------------------------------------------------
# tunnel-ollama-status
# ---------------------------------------------------------------------------

@test "tunnel-ollama-status: probes lsof + curl /api/tags when listening" {
  cat > "$TEST_DIR/bin/lsof" <<'LSOF'
#!/usr/bin/env bash
exit 0   # 11500 listening
LSOF
  chmod +x "$TEST_DIR/bin/lsof"

  run "$SCRIPT_PATH" tunnel-ollama-status
  [ "$status" -eq 0 ]
  grep -qE "curl .*localhost:11500/api/tags" "$BATS_CURL_LOG"
}

@test "tunnel-ollama-status: orphan ControlSocket (lsof OK + curl fail) reported" {
  cat > "$TEST_DIR/bin/lsof" <<'LSOF'
#!/usr/bin/env bash
exit 0
LSOF
  chmod +x "$TEST_DIR/bin/lsof"

  cat > "$TEST_DIR/bin/curl" <<'CURL'
#!/usr/bin/env bash
exit 7   # CURLE_COULDNT_CONNECT
CURL
  chmod +x "$TEST_DIR/bin/curl"

  run "$SCRIPT_PATH" tunnel-ollama-status
  [ "$status" -ne 0 ]
  [[ "$output" == *"orphan"* || "$output" == *"forward"* || "$output" == *"unreachable"* ]]
}

@test "tunnel-ollama-status: tunnel down (lsof empty) reported" {
  # default lsof exits 1 — port not listening
  run "$SCRIPT_PATH" tunnel-ollama-status
  [ "$status" -ne 0 ]
  [[ "$output" == *"down"* || "$output" == *"미가동"* || "$output" == *"not running"* ]]
}

# ---------------------------------------------------------------------------
# tunnel-lms-start (LM Studio MLX, port 11600 → emac:1234)
# ---------------------------------------------------------------------------

@test "tunnel-lms-start: idempotent stop + LocalForward 11600→emac:1234" {
  run "$SCRIPT_PATH" tunnel-lms-start
  [ "$status" -eq 0 ]
  grep -q -- "-O exit" "$BATS_SSH_LOG"
  grep -qE "\-fN.*-L 11600:localhost:1234 emac" "$BATS_SSH_LOG"
}

@test "tunnel-lms-start: ControlMaster + lms-suffixed ControlPath (coexists with ollama)" {
  run "$SCRIPT_PATH" tunnel-lms-start
  [ "$status" -eq 0 ]
  grep -q "ControlMaster=auto" "$BATS_SSH_LOG"
  grep -q "ControlPath=/tmp/kittypaw-dev-models-tunnel-lms-%C" "$BATS_SSH_LOG"
  # Sanity: not the ollama suffix (separate multiplex sessions)
  ! grep -q "ControlPath=/tmp/kittypaw-dev-models-tunnel-ollama-%C" "$BATS_SSH_LOG"
}

# ---------------------------------------------------------------------------
# tunnel-lms-stop
# ---------------------------------------------------------------------------

@test "tunnel-lms-stop: invokes ssh -O exit with lms-suffixed ControlPath" {
  run "$SCRIPT_PATH" tunnel-lms-stop
  [ "$status" -eq 0 ]
  grep -q -- "-O exit" "$BATS_SSH_LOG"
  grep -q "ControlPath=/tmp/kittypaw-dev-models-tunnel-lms-%C" "$BATS_SSH_LOG"
}

# ---------------------------------------------------------------------------
# tunnel-lms-status (LM Studio uses /v1/models, not ollama's /api/tags)
# ---------------------------------------------------------------------------

@test "tunnel-lms-status: probes lsof :11600 + curl /v1/models when listening" {
  cat > "$TEST_DIR/bin/lsof" <<'LSOF'
#!/usr/bin/env bash
exit 0   # 11600 listening
LSOF
  chmod +x "$TEST_DIR/bin/lsof"

  run "$SCRIPT_PATH" tunnel-lms-status
  [ "$status" -eq 0 ]
  grep -qE "curl .*localhost:11600/v1/models" "$BATS_CURL_LOG"
}

@test "tunnel-lms-status: orphan ControlSocket reported (LM Studio HTTP server stopped)" {
  cat > "$TEST_DIR/bin/lsof" <<'LSOF'
#!/usr/bin/env bash
exit 0
LSOF
  chmod +x "$TEST_DIR/bin/lsof"

  cat > "$TEST_DIR/bin/curl" <<'CURL'
#!/usr/bin/env bash
exit 7
CURL
  chmod +x "$TEST_DIR/bin/curl"

  run "$SCRIPT_PATH" tunnel-lms-status
  [ "$status" -ne 0 ]
  [[ "$output" == *"orphan"* || "$output" == *"unreachable"* || "$output" == *"LM Studio"* ]]
}

@test "tunnel-lms-status: tunnel down (lsof empty) reported" {
  run "$SCRIPT_PATH" tunnel-lms-status
  [ "$status" -ne 0 ]
  [[ "$output" == *"down"* ]]
}
