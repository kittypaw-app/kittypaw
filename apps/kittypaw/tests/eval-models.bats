#!/usr/bin/env bats
# Plan B Iteration 1 — eval framework rebuild bats.
# Run: bats apps/kittypaw/tests/eval-models.bats

setup_file() {
  APP_DIR="$(cd "$(dirname "${BATS_TEST_FILENAME}")/.." && pwd)"
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

@test "T1: 5 entries count" {
  count="$(echo "$JSON" | jq '.model | length')"
  [ "$count" -eq 5 ]
}

@test "T1: expected ids present" {
  for id in groq-qwen mistral-medium gemini-flash-lite openrouter-llama-3.3 lmstudio-qwen3-30b-mlx; do
    echo "$JSON" | jq -e --arg id "$id" '.model | map(select(.id == $id)) | length == 1' >/dev/null
  done
}

@test "T1: every entry has id / provider / model" {
  echo "$JSON" | jq -e '.model | all(. | has("id") and has("provider") and has("model"))' >/dev/null
}

@test "T1: api_key_env set for cloud providers, omitted/empty for lmstudio" {
  for env in GROQ_API_KEY MISTRAL_API_KEY GEMINI_API_KEY OPENROUTER_API_KEY; do
    echo "$JSON" | jq -e --arg env "$env" '.model | map(select(.api_key_env == $env)) | length == 1' >/dev/null
  done
  echo "$JSON" | jq -e '.model | map(select(.id == "lmstudio-qwen3-30b-mlx")) | .[0].api_key_env // "" | length == 0' >/dev/null
}

@test "T1: parse-models.py exits 2 when toml missing" {
  run uv run python "$PARSE" /nonexistent/models.toml
  [ "$status" -eq 2 ]
}
