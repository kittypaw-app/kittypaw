#!/usr/bin/env bats
# T3 RED — dev-models-ollama-measure.sh bats unit tests.
# Run: bats apps/kittypaw/tests/dev-models-ollama-measure.bats
#
# Mocks ssh / lsof / curl / ollama via PATH override. Every external command
# the script touches is shimmed so the harness stays hermetic — no real SSH
# connection, no real ollama pull, no real KittyPaw daemon. Each shim records
# its invocation to a per-test log file so assertions can probe argument flags.

setup() {
  TESTS_DIR="$(cd "$(dirname "${BATS_TEST_FILENAME}")" && pwd)"
  SCRIPT_PATH="$(cd "$TESTS_DIR/../scripts" && pwd)/dev-models-ollama-measure.sh"
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

  # ssh: success by default; arg log for assertions.
  cat > "$TEST_DIR/bin/ssh" <<'SSH'
#!/usr/bin/env bash
echo "ssh $*" >> "$TEST_DIR/ssh.log"
exit 0
SSH
  chmod +x "$TEST_DIR/bin/ssh"

  # lsof: success — both 11500 (tunnel) and 13001 (daemon) appear listening.
  cat > "$TEST_DIR/bin/lsof" <<'LSOF'
#!/usr/bin/env bash
exit 0
LSOF
  chmod +x "$TEST_DIR/bin/lsof"

  # curl: route by URL, return realistic shape. /api/v1/chat echoes a stub
  # response so the script's jq pipeline has something to print.
  cat > "$TEST_DIR/bin/curl" <<'CURL'
#!/usr/bin/env bash
echo "curl $*" >> "$TEST_DIR/curl.log"
case "$*" in
  *"/api/v1/reload"*) echo '{"reloaded":true}'; exit 0 ;;
  *"/api/v1/chat"*)   echo '{"response":"hello from mock"}'; exit 0 ;;
  *"/api/tags"*)      echo '{"models":[]}'; exit 0 ;;
  *)                  exit 0 ;;
esac
CURL
  chmod +x "$TEST_DIR/bin/curl"

  # ollama presence — `command -v ollama` must succeed even though we won't
  # actually pull anything (real pulls happen via ssh emac in production).
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
# Pre-flight failures
# ---------------------------------------------------------------------------

@test "fail when master_api_key empty in server.toml" {
  : > "$KITTYPAW_DEV_HOME/server.toml"   # zero-byte file
  run "$SCRIPT_PATH" qwen2.5:7b
  [ "$status" -ne 0 ]
  [[ "$output" == *"master_api_key"* ]]
}

@test "fail when ssh emac unreachable" {
  cat > "$TEST_DIR/bin/ssh" <<'SSH'
#!/usr/bin/env bash
echo "ssh $*" >> "$TEST_DIR/ssh.log"
exit 255    # OpenSSH "could not resolve hostname"
SSH
  chmod +x "$TEST_DIR/bin/ssh"

  run "$SCRIPT_PATH" qwen2.5:7b
  [ "$status" -ne 0 ]
  [[ "$output" == *"emac"* ]]
}

@test "fail when tunnel down (lsof :11500 empty)" {
  # 11500 down, 13001 (daemon) still up — surface the right diagnosis.
  cat > "$TEST_DIR/bin/lsof" <<'LSOF'
#!/usr/bin/env bash
case "$*" in
  *":11500"*)  exit 1 ;;
  *)           exit 0 ;;
esac
LSOF
  chmod +x "$TEST_DIR/bin/lsof"

  run "$SCRIPT_PATH" qwen2.5:7b
  [ "$status" -ne 0 ]
  [[ "$output" == *"tunnel"* ]]
}

@test "fail when tunnel orphan (lsof :11500 OK but curl /api/tags fails)" {
  cat > "$TEST_DIR/bin/curl" <<'CURL'
#!/usr/bin/env bash
echo "curl $*" >> "$TEST_DIR/curl.log"
case "$*" in
  *"/api/tags"*) exit 7 ;;   # CURLE_COULDNT_CONNECT
  *)             exit 0 ;;
esac
CURL
  chmod +x "$TEST_DIR/bin/curl"

  run "$SCRIPT_PATH" qwen2.5:7b
  [ "$status" -ne 0 ]
  [[ "$output" == *"orphan"* || "$output" == *"unreachable"* ]]
}

# ---------------------------------------------------------------------------
# Happy path + Critic-obligatory JSON escape
# ---------------------------------------------------------------------------

@test "happy path: reload + chat + trap restores config" {
  ORIG_CFG="$(cat "$KITTYPAW_DEV_HOME/accounts/default/config.toml")"

  run "$SCRIPT_PATH" qwen2.5:7b "안녕"
  [ "$status" -eq 0 ]

  grep -q "/api/v1/reload" "$TEST_DIR/curl.log"
  grep -q "/api/v1/chat" "$TEST_DIR/curl.log"
  grep -qE "Authorization: Bearer deadbeef" "$TEST_DIR/curl.log"

  RESTORED="$(cat "$KITTYPAW_DEV_HOME/accounts/default/config.toml")"
  [ "$RESTORED" = "$ORIG_CFG" ]
}

@test "JSON escape: special chars in prompt survive jq encoding (Critic obligatory)" {
  # Hostile prompt: quote, newline, dollar — naive sed/printf would corrupt.
  PROMPT='He said "hello"
and $env got expanded'

  run "$SCRIPT_PATH" qwen2.5:7b "$PROMPT"
  [ "$status" -eq 0 ]

  # The chat endpoint must still be reached — i.e., jq did not error out.
  grep -q "/api/v1/chat" "$TEST_DIR/curl.log"
  # And the prompt body actually shipped (escaped form), not a corrupted one.
  grep -q "He said" "$TEST_DIR/curl.log"
}
