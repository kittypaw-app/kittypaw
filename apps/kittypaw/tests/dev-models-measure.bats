#!/usr/bin/env bats
# T3 — dev-models-measure.sh BACKEND={ollama|lmstudio} bats unit tests.
# Run: bats apps/kittypaw/tests/dev-models-measure.bats
#
# Mocks ssh / lsof / curl / ollama via PATH override. Every external command
# the script touches is shimmed so the harness stays hermetic — no real SSH
# connection, no real ollama pull, no real LM Studio app, no real KittyPaw
# daemon. Each shim records its invocation to a per-test log file so
# assertions can probe argument flags + URL paths.

setup() {
  TESTS_DIR="$(cd "$(dirname "${BATS_TEST_FILENAME}")" && pwd)"
  SCRIPT_PATH="$(cd "$TESTS_DIR/../scripts" && pwd)/dev-models-measure.sh"
  export TEST_DIR="$(mktemp -d -t bats-measure.XXXXXX)"
  export KITTYPAW_DEV_HOME="$TEST_DIR/dev-models"
  export KITTYPAW_DEV_PORT=13001
  mkdir -p "$KITTYPAW_DEV_HOME/accounts/default" "$TEST_DIR/bin"

  cat > "$KITTYPAW_DEV_HOME/server.toml" <<EOF
bind = "127.0.0.1:13001"
master_api_key = "deadbeef0badc0de1234567890abcdef"
EOF

  cat > "$KITTYPAW_DEV_HOME/accounts/default/config.toml" <<'CFG'
[llm]
default = "groq-qwen"

[[llm.models]]
id = "groq-qwen"
provider = "groq"
model = "qwen/qwen3-32b"
max_tokens = 1024
CFG

  export PATH="$TEST_DIR/bin:$PATH"

  # ssh: success by default. The ollama pull path captures stdout via
  # `EMAC_OLLAMA=$(ssh emac '...')`, so when the script asks `command -v
  # ollama || for p in ...` we need to print a plausible path; otherwise
  # the script fails-fast with "ollama not found on emac".
  cat > "$TEST_DIR/bin/ssh" <<'SSH'
#!/usr/bin/env bash
echo "ssh $*" >> "$TEST_DIR/ssh.log"
case "$*" in
  *"command -v ollama"*) echo "/usr/local/bin/ollama" ;;
  *) ;;
esac
exit 0
SSH
  chmod +x "$TEST_DIR/bin/ssh"

  # lsof: success — both 11500/11600 (tunnels) and 13001 (daemon) listening.
  cat > "$TEST_DIR/bin/lsof" <<'LSOF'
#!/usr/bin/env bash
exit 0
LSOF
  chmod +x "$TEST_DIR/bin/lsof"

  # curl: route by URL. /v1/models advertises the LM Studio test model so
  # the lmstudio "verify model loaded" probe (jq -e .data[].id) passes.
  cat > "$TEST_DIR/bin/curl" <<'CURL'
#!/usr/bin/env bash
echo "curl $*" >> "$TEST_DIR/curl.log"
case "$*" in
  *"/v1/models"*)     echo '{"data":[{"id":"qwen3-30b-a3b-instruct-2507","object":"model"}]}'; exit 0 ;;
  *"/api/v1/reload"*) echo '{"reloaded":true}'; exit 0 ;;
  *"/api/v1/chat"*)   echo '{"response":"hello from mock"}'; exit 0 ;;
  *"/api/tags"*)      echo '{"models":[]}'; exit 0 ;;
  *)                  exit 0 ;;
esac
CURL
  chmod +x "$TEST_DIR/bin/curl"

  # ollama presence — `command -v ollama` must succeed even though no real
  # pull happens (the production pull goes through ssh emac). Only the
  # ollama backend prereq check needs this; lmstudio backend skips it.
  cat > "$TEST_DIR/bin/ollama" <<'OLL'
#!/usr/bin/env bash
exit 0
OLL
  chmod +x "$TEST_DIR/bin/ollama"
}

teardown() {
  rm -rf "$TEST_DIR"
}

# ---------------------------------------------------------------------------
# Argument validation
# ---------------------------------------------------------------------------

@test "fail when BACKEND missing (no args)" {
  run "$SCRIPT_PATH"
  [ "$status" -ne 0 ]
  [[ "$output" == *"usage"* ]]
}

@test "fail when BACKEND unsupported" {
  run "$SCRIPT_PATH" llamacpp qwen3
  [ "$status" -ne 0 ]
  [[ "$output" == *"unsupported"* || "$output" == *"backend"* ]]
}

# ---------------------------------------------------------------------------
# Pre-flight failures (ollama path representative; logic is generic)
# ---------------------------------------------------------------------------

@test "ollama: fail when master_api_key empty in server.toml" {
  : > "$KITTYPAW_DEV_HOME/server.toml"
  run "$SCRIPT_PATH" ollama qwen2.5:7b
  [ "$status" -ne 0 ]
  [[ "$output" == *"master_api_key"* ]]
}

@test "ollama: fail when ssh emac unreachable" {
  cat > "$TEST_DIR/bin/ssh" <<'SSH'
#!/usr/bin/env bash
echo "ssh $*" >> "$TEST_DIR/ssh.log"
exit 255
SSH
  chmod +x "$TEST_DIR/bin/ssh"

  run "$SCRIPT_PATH" ollama qwen2.5:7b
  [ "$status" -ne 0 ]
  [[ "$output" == *"emac"* ]]
}

@test "ollama: fail when tunnel down (lsof :11500 empty)" {
  cat > "$TEST_DIR/bin/lsof" <<'LSOF'
#!/usr/bin/env bash
case "$*" in
  *":11500"*) exit 1 ;;
  *)          exit 0 ;;
esac
LSOF
  chmod +x "$TEST_DIR/bin/lsof"

  run "$SCRIPT_PATH" ollama qwen2.5:7b
  [ "$status" -ne 0 ]
  [[ "$output" == *"tunnel"* ]]
}

@test "ollama: fail when tunnel orphan (curl /api/tags fails)" {
  cat > "$TEST_DIR/bin/curl" <<'CURL'
#!/usr/bin/env bash
echo "curl $*" >> "$TEST_DIR/curl.log"
case "$*" in
  *"/api/tags"*) exit 7 ;;
  *)             exit 0 ;;
esac
CURL
  chmod +x "$TEST_DIR/bin/curl"

  run "$SCRIPT_PATH" ollama qwen2.5:7b
  [ "$status" -ne 0 ]
  [[ "$output" == *"orphan"* || "$output" == *"unreachable"* ]]
}

# ---------------------------------------------------------------------------
# Happy paths + JSON escape contract
# ---------------------------------------------------------------------------

@test "ollama: happy path — reload + chat + trap restores config" {
  ORIG_CFG="$(cat "$KITTYPAW_DEV_HOME/accounts/default/config.toml")"

  run "$SCRIPT_PATH" ollama qwen2.5:7b "안녕"
  [ "$status" -eq 0 ]

  grep -q "/api/v1/reload" "$TEST_DIR/curl.log"
  grep -q "/api/v1/chat" "$TEST_DIR/curl.log"
  grep -qE "Authorization: Bearer deadbeef" "$TEST_DIR/curl.log"

  RESTORED="$(cat "$KITTYPAW_DEV_HOME/accounts/default/config.toml")"
  [ "$RESTORED" = "$ORIG_CFG" ]
}

@test "ollama: JSON escape — special chars in prompt survive jq encoding (Critic obligatory)" {
  PROMPT='He said "hello"
and $env got expanded'

  run "$SCRIPT_PATH" ollama qwen2.5:7b "$PROMPT"
  [ "$status" -eq 0 ]

  grep -q "/api/v1/chat" "$TEST_DIR/curl.log"
  grep -q "He said" "$TEST_DIR/curl.log"
}

# ---------------------------------------------------------------------------
# lmstudio backend (port 11600 → emac:1234, no pull, GUI-managed model load)
# ---------------------------------------------------------------------------

@test "lmstudio: happy path — verify model loaded + reload + chat + trap restore" {
  ORIG_CFG="$(cat "$KITTYPAW_DEV_HOME/accounts/default/config.toml")"

  run "$SCRIPT_PATH" lmstudio qwen3-30b-a3b-instruct-2507 "ping"
  [ "$status" -eq 0 ]

  # Probe must hit /v1/models (not /api/tags) and chat must be reached.
  grep -qE "curl .*localhost:11600/v1/models" "$TEST_DIR/curl.log"
  grep -q "/api/v1/reload" "$TEST_DIR/curl.log"
  grep -q "/api/v1/chat" "$TEST_DIR/curl.log"

  # No `ollama pull` — lmstudio path skips emac model fetch. The ssh.log
  # may contain the SSH health probe (`ssh ... emac true`), but should not
  # contain any `pull` invocation.
  ! grep -qE "ssh .*pull " "$TEST_DIR/ssh.log" 2>/dev/null

  # Trap restored config.
  RESTORED="$(cat "$KITTYPAW_DEV_HOME/accounts/default/config.toml")"
  [ "$RESTORED" = "$ORIG_CFG" ]
}

@test "lmstudio: fail when target model not loaded in /v1/models" {
  # /v1/models returns a snapshot without the requested model — script
  # must fail-fast with a clear "load via app GUI" hint.
  cat > "$TEST_DIR/bin/curl" <<'CURL'
#!/usr/bin/env bash
echo "curl $*" >> "$TEST_DIR/curl.log"
case "$*" in
  *"/v1/models"*) echo '{"data":[{"id":"some-other-model","object":"model"}]}'; exit 0 ;;
  *"/api/v1/reload"*) echo '{"reloaded":true}'; exit 0 ;;
  *"/api/v1/chat"*)   echo '{"response":"hello"}'; exit 0 ;;
  *)                  exit 0 ;;
esac
CURL
  chmod +x "$TEST_DIR/bin/curl"

  run "$SCRIPT_PATH" lmstudio qwen3-30b-a3b-instruct-2507
  [ "$status" -ne 0 ]
  [[ "$output" == *"not loaded"* || "$output" == *"GUI"* ]]
  # Must NOT have reached the chat endpoint.
  ! grep -q "/api/v1/chat" "$TEST_DIR/curl.log"
}

@test "lmstudio: fail when tunnel down (lsof :11600 empty)" {
  cat > "$TEST_DIR/bin/lsof" <<'LSOF'
#!/usr/bin/env bash
case "$*" in
  *":11600"*) exit 1 ;;
  *)          exit 0 ;;
esac
LSOF
  chmod +x "$TEST_DIR/bin/lsof"

  run "$SCRIPT_PATH" lmstudio qwen3-30b-a3b-instruct-2507
  [ "$status" -ne 0 ]
  [[ "$output" == *"tunnel"* ]]
  [[ "$output" == *"lmstudio"* ]]
}
