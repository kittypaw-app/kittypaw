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

@test "T1: 9 entries count" {
  count="$(echo "$JSON" | jq '.model | length')"
  [ "$count" -eq 9 ]
}

@test "T1: expected ids present" {
  for id in groq-qwen groq-llama mistral-medium ministral-8b gemini-flash-lite openrouter-llama-3.3 lmstudio-qwen3-30b-mlx ollama-qwen2.5-32b ollama-gemma4; do
    echo "$JSON" | jq -e --arg id "$id" '.model | map(select(.id == $id)) | length == 1' >/dev/null
  done
}

@test "T1: every entry has id / provider / model" {
  echo "$JSON" | jq -e '.model | all(. | has("id") and has("provider") and has("model"))' >/dev/null
}

@test "T1: api_key_env set for cloud providers, omitted/empty for lmstudio + ollama" {
  for env in GROQ_API_KEY MISTRAL_API_KEY GEMINI_API_KEY OPENROUTER_API_KEY; do
    echo "$JSON" | jq -e --arg env "$env" '.model | map(select(.api_key_env == $env)) | length >= 1' >/dev/null
  done
  for id in lmstudio-qwen3-30b-mlx ollama-qwen2.5-32b ollama-gemma4; do
    echo "$JSON" | jq -e --arg id "$id" '.model | map(select(.id == $id)) | .[0].api_key_env // "" | length == 0' >/dev/null
  done
}

@test "T1: ollama entries use provider=ollama + base_url 11434" {
  for id in ollama-qwen2.5-32b ollama-gemma4; do
    echo "$JSON" | jq -e --arg id "$id" '.model | map(select(.id == $id)) | .[0].provider == "ollama"' >/dev/null
    echo "$JSON" | jq -e --arg id "$id" '.model | map(select(.id == $id)) | .[0].base_url | test("localhost:11434")' >/dev/null
  done
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
    T5:*) _setup_t5 ;;
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
  # Iteration 2: ssh stub for lmstudio readiness probe. Default returns
  # "lms ps" output WITHOUT the lmstudio model id → graceful fail.
  cat > "$T4_BIN/ssh" <<'SSH'
#!/usr/bin/env bash
# T4 stub: simulate lmstudio model NOT loaded on emac
echo "LOADED MODELS"
echo "(none — model not loaded)"
exit 0
SSH
  chmod +x "$T4_BIN/ssh"
  # Prepend T4_BIN so the ssh stub wins over /usr/bin/ssh.
  export PATH="$T4_BIN:$PATH"
  export KITTY_BIN="$T4_BIN/kittypaw"
  export SECRETARY_RUN="$T4_BIN/secretary_smoke_mock.sh"
  export EVAL_TMP="$T4_TMP"
  export RUNS_ROOT="$T4_RUNS"
  export READINESS_TIMEOUT=1
  export INTER_MODEL_SLEEP=0
  # Force ollama tunnel probe to fail fast (port 1 unused) — bats determinism
  # regardless of user's actual ssh tunnel state.
  export OLLAMA_PROBE_URL="http://127.0.0.1:1/api/tags"
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

@test "T2: generator stdout has sentinel + 9 [[llm.models]] + [llm] default" {
  out="$(bash "$GENERATOR")"
  echo "$out" | head -1 | grep -q "GENERATED FROM eval/models.toml"
  blocks="$(echo "$out" | grep -c '^\[\[llm.models\]\]')"
  [ "$blocks" -eq 9 ]
  echo "$out" | grep -q '^\[llm\]'
  echo "$out" | grep -q '^default = "groq-qwen"'
}

@test "T2: setup with no cfg → generates sentinel + 9 entries" {
  run bash "$DEV_MODELS" setup
  [ "$status" -eq 0 ]
  [ -f "$CFG" ]
  head -1 "$CFG" | grep -q "GENERATED FROM eval/models.toml"
  blocks="$(grep -c '^\[\[llm.models\]\]' "$CFG")"
  [ "$blocks" -eq 9 ]
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

_setup_t5() {
  T5_RUNS="$BATS_TEST_TMPDIR/runs"
  T5_DOCS="$BATS_TEST_TMPDIR/models.md"
  mkdir -p "$T5_RUNS"
  RECOMMEND="$APP_DIR/eval/recommend.sh"
  export RUNS_ROOT="$T5_RUNS"
  export DOCS_MD="$T5_DOCS"
}

# Helper for T5: write a manifest with the given (id, status) pairs.
write_manifest() {
  local run_id="$1"; shift
  local dir="$T5_RUNS/$run_id"
  mkdir -p "$dir"
  local entries="[]"
  while [[ $# -gt 0 ]]; do
    local id="$1" status="$2"
    shift 2
    entries=$(echo "$entries" | jq --arg id "$id" --arg status "$status" \
      '. + [{"id": $id, "status": $status, "detail": ""}]')
  done
  jq -n --arg id "$run_id" --argjson m "$entries" \
    '{runID: $id, models: $m}' > "$dir/manifest.json"
  echo "$dir"
}

@test "T4: daemon readiness timeout → 9 모델 모두 status=fail + 다음 모델 진행 (whole-run abort 금지)" {
  run bash "$RUN_MODELS"
  [ "$status" -eq 0 ]  # whole-run completes despite per-model failures
  # Single run dir was created.
  run_dir="$(ls "$T4_RUNS")"
  manifest="$T4_RUNS/$run_dir/manifest.json"
  [ -f "$manifest" ]
  count=$(jq '.models | length' "$manifest")
  [ "$count" -eq 9 ]
  fail_count=$(jq '[.models[] | select(.status == "fail")] | length' "$manifest")
  [ "$fail_count" -eq 9 ]
  # secretary_smoke never ran (readiness failed before invocation).
  ! grep -q "secretary_smoke" "$T4_LOG"
}

# ---------- Iteration 2 T2: backoff defaults + fixture limit env ----------

@test "T2: run-models.sh defaults INTER_MODEL_SLEEP=60 + PER_MODEL_TIMEOUT=180" {
  grep -q 'INTER_MODEL_SLEEP="\${INTER_MODEL_SLEEP:-60}"' "$APP_DIR/eval/run-models.sh"
  grep -q 'PER_MODEL_TIMEOUT="\${PER_MODEL_TIMEOUT:-180}"' "$APP_DIR/eval/run-models.sh"
}

@test "T2: run-models.sh exports KITTYPAW_EVAL_FIXTURE_LIMIT to secretary_smoke (default 0 = no limit, opt-in)" {
  # Default 0 — secretary_smoke threshold logic (hardcoded fixture counts)
  # would auto-FAIL every model if LIMIT < threshold. Opt-in only.
  grep -q 'KITTYPAW_EVAL_FIXTURE_LIMIT="\${KITTYPAW_EVAL_FIXTURE_LIMIT:-0}"' "$APP_DIR/eval/run-models.sh"
  grep -q 'KITTYPAW_EVAL_FIXTURE_LIMIT="\$KITTYPAW_EVAL_FIXTURE_LIMIT"' "$APP_DIR/eval/run-models.sh"
}

@test "T4: lmstudio model not loaded → graceful fail with manual lms load hint (NO auto-load)" {
  run bash "$RUN_MODELS"
  [ "$status" -eq 0 ]
  run_dir="$(ls "$T4_RUNS")"
  manifest="$T4_RUNS/$run_dir/manifest.json"
  lmstudio_status=$(jq -r '.models[] | select(.id == "lmstudio-qwen3-30b-mlx") | .status' "$manifest")
  [ "$lmstudio_status" = "fail" ]
  lmstudio_detail=$(jq -r '.models[] | select(.id == "lmstudio-qwen3-30b-mlx") | .detail' "$manifest")
  echo "$lmstudio_detail" | grep -q "lmstudio model not loaded"
  echo "$lmstudio_detail" | grep -q "lms load qwen3-30b-a3b-instruct-2507"
  echo "$lmstudio_detail" | grep -q "gpu max"
  # NO auto-load: ssh stub was called for `lms ps` only (not `lms load`).
  # ssh stub doesn't track args, but the absence of auto-load is verified by
  # graceful-fail status (would be "pass" if auto-load attempted + succeeded,
  # or "daemon readiness timeout" if it tried and the daemon then failed).
}

@test "T4: ollama tunnel down → ollama 2 entries graceful fail with actionable msg + 다음 모델 진행" {
  run bash "$RUN_MODELS"
  [ "$status" -eq 0 ]
  run_dir="$(ls "$T4_RUNS")"
  manifest="$T4_RUNS/$run_dir/manifest.json"
  ollama_fails=$(jq '[.models[] | select(.id | startswith("ollama-")) | select(.status == "fail")] | length' "$manifest")
  [ "$ollama_fails" -eq 2 ]
  # Detail message contains actionable hint (NOT generic daemon timeout).
  ollama_detail=$(jq -r '.models[] | select(.id == "ollama-qwen2.5-32b") | .detail' "$manifest")
  echo "$ollama_detail" | grep -q "ollama tunnel not ready"
  echo "$ollama_detail" | grep -q "dev-models-tunnel-ollama-start"
  # Other 7 entries (6 cloud + 1 lmstudio) should also have status=fail
  # (daemon readiness fails) — verify whole-run did not abort.
  total_fail=$(jq '[.models[] | select(.status == "fail")] | length' "$manifest")
  [ "$total_fail" -eq 9 ]
}

@test "T2: secretary_smoke fixture_lines respects KITTYPAW_EVAL_FIXTURE_LIMIT" {
  fixture="$BATS_TEST_TMPDIR/test.jsonl"
  printf '{"id":"a"}\n{"id":"b"}\n{"id":"c"}\n' > "$fixture"
  # Extract fixture_lines function from run.sh and source it (avoid running main).
  func=$(awk '/^fixture_lines\(\) \{/,/^\}/' "$APP_DIR/eval/secretary_smoke/run.sh")
  eval "$func"

  # Default (LIMIT unset) — full fixture (3 lines).
  unset KITTYPAW_EVAL_FIXTURE_LIMIT
  count=$(fixture_lines "$fixture" | wc -l | tr -d ' ')
  [ "$count" -eq 3 ]

  # LIMIT=2 — first 2 lines only.
  KITTYPAW_EVAL_FIXTURE_LIMIT=2
  count=$(fixture_lines "$fixture" | wc -l | tr -d ' ')
  [ "$count" -eq 2 ]

  # LIMIT=0 — full fixture (regression: explicit 0 = no limit).
  KITTYPAW_EVAL_FIXTURE_LIMIT=0
  count=$(fixture_lines "$fixture" | wc -l | tr -d ' ')
  [ "$count" -eq 3 ]
}

# ---------- T5: recommend.sh + docs/models.md render ----------

@test "T5: pass entry 있을 때 → 첫 pass id 추천 + status matrix" {
  run_dir=$(write_manifest "1700000000-1234" \
    "groq-qwen" "fail" \
    "groq-llama" "pass" \
    "mistral-medium" "pass")
  run bash "$RECOMMEND"
  [ "$status" -eq 0 ]
  [ -f "$T5_DOCS" ]
  head -1 "$T5_DOCS" | grep -q "GENERATED"
  grep -q '`groq-llama`' "$T5_DOCS"  # first pass entry
  grep -q "추천" "$T5_DOCS"
  grep -q "pass: 2" "$T5_DOCS"
  grep -q "fail: 1" "$T5_DOCS"
}

@test "T5: 모든 fail → '추천 없음' + exit 0 (manifest 보존)" {
  run_dir=$(write_manifest "1700000001-1234" \
    "groq-qwen" "fail" \
    "mistral-medium" "fail")
  run bash "$RECOMMEND"
  [ "$status" -eq 0 ]  # manifest 보존, exit 0
  grep -q "추천 없음" "$T5_DOCS"
  [ -f "$run_dir/manifest.json" ]  # manifest unchanged
}

@test "T5: latest run discovery (여러 run 중 sortable id 마지막)" {
  write_manifest "1700000000-1234" "early" "pass" >/dev/null
  write_manifest "1700000050-9999" "late" "pass" >/dev/null
  run bash "$RECOMMEND"
  [ "$status" -eq 0 ]
  grep -q '`late`' "$T5_DOCS"  # latest selected
  ! grep -q '`early`' "$T5_DOCS"
}

@test "T5: no run found → exit 2" {
  rm -rf "$T5_RUNS"/*
  run bash "$RECOMMEND"
  [ "$status" -eq 2 ]
  echo "$output" | grep -q "no run found"
}
