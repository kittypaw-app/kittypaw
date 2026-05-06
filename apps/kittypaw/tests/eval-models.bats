#!/usr/bin/env bats
# Plan B Iteration 1 — eval framework rebuild bats.
# Run: bats apps/kittypaw/tests/eval-models.bats

setup_file() {
  APP_DIR="$(cd "$(dirname "${BATS_TEST_FILENAME}")/.." && pwd)"
  export APP_DIR
  export MODELS_TOML="$APP_DIR/eval/models.toml"
  export PARSE="$APP_DIR/eval/parse-models.py"
  if [ -f "$MODELS_TOML" ]; then
    JSON="$(uv run python "$PARSE" "$MODELS_TOML")"
    export JSON
  fi
}

# ---------- T1: eval/models.toml + parse helper ----------

@test "T1: eval/models.toml exists" {
  [ -f "$MODELS_TOML" ]
}

@test "T1: parse-models.py emits valid JSON" {
  echo "$JSON" | jq -e '.' >/dev/null
}

@test "T1: 7 entries count" {
  count="$(echo "$JSON" | jq '.model | length')"
  [ "$count" -eq 7 ]
}

@test "T1: expected ids present" {
  for id in groq-qwen groq-llama mistral-medium ministral-8b gemini-flash-lite openrouter-llama-3.3 lmstudio-qwen3-30b-mlx; do
    echo "$JSON" | jq -e --arg id "$id" '.model | map(select(.id == $id)) | length == 1' >/dev/null
  done
}

@test "T1: every entry has id / provider / model" {
  echo "$JSON" | jq -e '.model | all(. | has("id") and has("provider") and has("model"))' >/dev/null
}

@test "T1: api_key_env set for cloud providers, omitted/empty for lmstudio" {
  for env in GROQ_API_KEY MISTRAL_API_KEY GEMINI_API_KEY OPENROUTER_API_KEY; do
    echo "$JSON" | jq -e --arg env "$env" '.model | map(select(.api_key_env == $env)) | length >= 1' >/dev/null
  done
  echo "$JSON" | jq -e '.model | map(select(.id == "lmstudio-qwen3-30b-mlx")) | .[0].api_key_env // "" | length == 0' >/dev/null
}

@test "T1: parse-models.py exits 2 when toml missing" {
  run uv run python "$PARSE" /nonexistent/models.toml
  [ "$status" -eq 2 ]
}

# ---------- T2: dev-models config generator + sentinel guard ----------

setup() {
  # T1 케이스는 setup_file의 export로 충분 — T2 mock 환경 cost 회피.
  [[ "$BATS_TEST_DESCRIPTION" == T2:* ]] || return 0
  T2_TMP="$BATS_TEST_TMPDIR/dev-models"
  T2_BIN="$BATS_TEST_TMPDIR/bin"
  mkdir -p "$T2_TMP" "$T2_BIN"
  cat > "$T2_BIN/kittypaw" <<'KP'
#!/usr/bin/env bash
home="${KITTYPAW_CONFIG_DIR:-/tmp/kp-mock}"
mkdir -p "$home/accounts/default"
touch "$home/accounts/default/config.toml"
exit 0
KP
  chmod +x "$T2_BIN/kittypaw"
  export KITTYPAW_DEV_HOME="$T2_TMP"
  export KP_BIN="$T2_BIN/kittypaw"
  DEV_MODELS="$APP_DIR/scripts/dev-models.sh"
  GENERATOR="$APP_DIR/scripts/dev-models-config-generate.sh"
  CFG="$T2_TMP/accounts/default/config.toml"
}

@test "T2: generator stdout has sentinel + 7 [[llm.models]] + [llm] default" {
  out="$(bash "$GENERATOR")"
  echo "$out" | head -1 | grep -q "GENERATED FROM eval/models.toml"
  blocks="$(echo "$out" | grep -c '^\[\[llm.models\]\]')"
  [ "$blocks" -eq 7 ]
  echo "$out" | grep -q '^\[llm\]'
  echo "$out" | grep -q '^default = "groq-qwen"'
}

@test "T2: setup with no cfg → generates sentinel + 7 entries" {
  run bash "$DEV_MODELS" setup
  [ "$status" -eq 0 ]
  [ -f "$CFG" ]
  head -1 "$CFG" | grep -q "GENERATED FROM eval/models.toml"
  blocks="$(grep -c '^\[\[llm.models\]\]' "$CFG")"
  [ "$blocks" -eq 7 ]
}

@test "T2: setup with sentinel cfg + no --force → skip (exit 0)" {
  bash "$DEV_MODELS" setup >/dev/null
  run bash "$DEV_MODELS" setup
  [ "$status" -eq 0 ]
  echo "$output" | grep -q "sentinel managed"
}

@test "T2: setup with non-sentinel cfg + no --force → abort exit 3 + diff stderr" {
  mkdir -p "$(dirname "$CFG")"
  echo "# user-edited config without sentinel" > "$CFG"
  echo "[llm]" >> "$CFG"
  run bash "$DEV_MODELS" setup
  [ "$status" -eq 3 ]
  echo "$output" | grep -q "no sentinel header"
  echo "$output" | grep -q "diff vs generated"
}

@test "T2: setup with non-sentinel cfg + --force → overwrites (no validation)" {
  mkdir -p "$(dirname "$CFG")"
  echo "# user-edited" > "$CFG"
  run bash "$DEV_MODELS" setup --force
  [ "$status" -eq 0 ]
  head -1 "$CFG" | grep -q "GENERATED FROM eval/models.toml"
}
