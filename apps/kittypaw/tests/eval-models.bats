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
  case "$BATS_TEST_DESCRIPTION" in
    T2:*) _setup_t2 ;;
    T3:*) _setup_t3 ;;
    T4:*) _setup_t4 ;;
    *) return 0 ;;
  esac
}

_setup_t2() {
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

_setup_t3() {
  T3_HOME="$BATS_TEST_TMPDIR/eval-cfg"
  T3_BIN="$BATS_TEST_TMPDIR/bin"
  T3_OUT="$BATS_TEST_TMPDIR/results"
  T3_LOCK="$BATS_TEST_TMPDIR/lock"
  T3_LOG="$BATS_TEST_TMPDIR/kp.log"
  mkdir -p "$T3_HOME/accounts/default" "$T3_BIN"
  cat > "$T3_HOME/accounts/default/config.toml" <<'CFG'
# <!-- GENERATED FROM eval/models.toml — do not edit -->

[llm]
default = "old-model"
CFG
  cat > "$T3_BIN/kittypaw" <<KP
#!/usr/bin/env bash
echo "kittypaw \$*" >> "$T3_LOG"
exit 0
KP
  chmod +x "$T3_BIN/kittypaw"
  export KITTY_BIN="$T3_BIN/kittypaw"
  export OUT_DIR="$T3_OUT"
  export SUMMARY="$T3_OUT/summary.md"
  export LOCK_DIR="$T3_LOCK"
  export KITTYPAW_EVAL_SKIP=1
  RUN_SH="$APP_DIR/eval/secretary_smoke/run.sh"
  T3_CFG="$T3_HOME/accounts/default/config.toml"
}

# ---------- T3: secretary_smoke/run.sh --model opt-in ----------

@test "T3: --model 없음 → default path (RUN_MODEL=configured)" {
  run bash "$RUN_SH"
  [ "$status" -eq 3 ]  # NOT_RUN (KITTYPAW_EVAL_SKIP=1)
  grep -q "^Model: configured$" "$T3_OUT/summary.md"
}

@test "T3: --model groq-qwen + KITTYPAW_CONFIG_DIR 없음 → exit 2 (사용자 보호)" {
  unset KITTYPAW_CONFIG_DIR
  run bash "$RUN_SH" --model groq-qwen
  [ "$status" -eq 2 ]
  echo "$output" | grep -q "KITTYPAW_CONFIG_DIR"
}

@test "T3: --model groq-qwen + KITTYPAW_CONFIG_DIR set → cfg swap + reload + restore" {
  export KITTYPAW_CONFIG_DIR="$T3_HOME"
  run bash "$RUN_SH" --model groq-qwen
  [ "$status" -eq 3 ]  # finish NOT_RUN after swap
  # Restore happened (trap EXIT) — cfg back to old-model.
  grep -q '^default = "old-model"$' "$T3_CFG"
  # Reload was invoked (swap + restore = 2 reload calls).
  reload_count="$(grep -c "server reload" "$T3_LOG")"
  [ "$reload_count" -eq 2 ]
  # Summary shows the swapped model.
  grep -q "^Model: groq-qwen$" "$T3_OUT/summary.md"
}

@test "T3: --model unknown-flag → exit 2" {
  export KITTYPAW_CONFIG_DIR="$T3_HOME"
  run bash "$RUN_SH" --bogus-flag
  [ "$status" -eq 2 ]
  echo "$output" | grep -q "unknown flag"
}

@test "T3: --model with sed-meta chars → exit 2 (regex guard, sed injection 차단)" {
  export KITTYPAW_CONFIG_DIR="$T3_HOME"
  run bash "$RUN_SH" --model 'evil|chars'
  [ "$status" -eq 2 ]
  echo "$output" | grep -q "must match"
  # cfg unchanged — guard rejected before swap_default ran.
  grep -q '^default = "old-model"$' "$T3_CFG"
}

_setup_t4() {
  T4_TMP="$BATS_TEST_TMPDIR/eval-tmp"
  T4_BIN="$BATS_TEST_TMPDIR/bin"
  T4_RUNS="$BATS_TEST_TMPDIR/runs"
  T4_LOG="$BATS_TEST_TMPDIR/kp.log"
  mkdir -p "$T4_TMP" "$T4_BIN" "$T4_RUNS"
  # Mock kittypaw — `server start` exec sleep so background & exits quickly.
  # `chat ping` returns 0 but healthz never listens, so readiness fails.
  cat > "$T4_BIN/kittypaw" <<KP
#!/usr/bin/env bash
echo "kittypaw \$*" >> "$T4_LOG"
# Explicit colon delimiter — \$1 \$2 단어 합치기보다 명확.
case "\$1:\${2:-}" in
  "server:start") exec sleep 1 ;;
  "server:stop")  exit 0 ;;
  "chat:ping")    exit 0 ;;
  *) exit 0 ;;
esac
KP
  chmod +x "$T4_BIN/kittypaw"
  cat > "$T4_BIN/secretary_smoke_mock.sh" <<'MS'
#!/usr/bin/env bash
echo "secretary_smoke $*" >> "$T4_LOG"
exit 0
MS
  chmod +x "$T4_BIN/secretary_smoke_mock.sh"
  export KITTY_BIN="$T4_BIN/kittypaw"
  export SECRETARY_RUN="$T4_BIN/secretary_smoke_mock.sh"
  export EVAL_TMP="$T4_TMP"
  export RUNS_ROOT="$T4_RUNS"
  export READINESS_TIMEOUT=1
  export EVAL_SKIP_JUDGE_CHECK=1
  export GROQ_API_KEY="fake"
  export MISTRAL_API_KEY="fake"
  export GEMINI_API_KEY="fake"
  export OPENROUTER_API_KEY="fake"
  export ANTHROPIC_API_KEY="fake"
  RUN_MODELS="$APP_DIR/eval/run-models.sh"
}

@test "T3: stale .swap_backup → exit 2 + manual recovery 안내 (영구 cfg 오염 차단)" {
  export KITTYPAW_CONFIG_DIR="$T3_HOME"
  # Simulate previous run interrupted: backup exists from earlier swap.
  cp "$T3_CFG" "$T3_CFG.swap_backup"
  echo 'default = "swapped-by-prev-run"' > "$T3_CFG"
  run bash "$RUN_SH" --model groq-qwen
  [ "$status" -eq 2 ]
  echo "$output" | grep -q "stale backup"
  # cfg + backup both untouched (no double-swap).
  grep -q '^default = "swapped-by-prev-run"$' "$T3_CFG"
  [ -f "$T3_CFG.swap_backup" ]
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

# ---------- T4: run-models.sh wrapper (per-run daemon, sequential) ----------

@test "T4: auth missing (MISTRAL_API_KEY) → exit 2" {
  unset MISTRAL_API_KEY
  run bash "$RUN_MODELS"
  [ "$status" -eq 2 ]
  echo "$output" | grep -q "MISTRAL_API_KEY"
}

@test "T4: ANTHROPIC_API_KEY missing (judge) → exit 2" {
  unset ANTHROPIC_API_KEY
  run bash "$RUN_MODELS"
  [ "$status" -eq 2 ]
  echo "$output" | grep -q "ANTHROPIC_API_KEY"
}

@test "T4: TMPDIR 작성 실패 → exit 2" {
  unset EVAL_TMP
  export TMPDIR="/nonexistent/path/no-write"
  run bash "$RUN_MODELS"
  [ "$status" -eq 2 ]
  echo "$output" | grep -q "cannot create"
}

@test "T4: daemon readiness timeout → 7 모델 모두 status=fail + 다음 모델 진행 (whole-run abort 금지)" {
  run bash "$RUN_MODELS"
  [ "$status" -eq 0 ]  # whole-run completes despite per-model failures
  # Single run dir was created.
  run_dir="$(ls "$T4_RUNS")"
  manifest="$T4_RUNS/$run_dir/manifest.json"
  [ -f "$manifest" ]
  count=$(jq '.models | length' "$manifest")
  [ "$count" -eq 7 ]
  fail_count=$(jq '[.models[] | select(.status == "fail")] | length' "$manifest")
  [ "$fail_count" -eq 7 ]
  # secretary_smoke never ran (readiness failed before invocation).
  ! grep -q "secretary_smoke" "$T4_LOG"
}
