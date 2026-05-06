#!/usr/bin/env bash
# Plan B recommendation rendering.
#
# Reads manifest.json from latest run (or RUN_DIR override) and writes
# docs/models.md. Recommendation rule is intentionally small:
#   1. status=pass only
#   2. higher korean_score wins
#   3. tie-breaker: lower latency_p95_ms wins
#   4. separate Cloud / Local recommendations use the same ranking
#
# Baseline is derived from existing eval/runs/*/manifest.json files. No DB.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EVAL_DIR="$ROOT_DIR/eval"
RUNS_ROOT="${RUNS_ROOT:-$EVAL_DIR/runs}"
DOCS_MD="${DOCS_MD:-$ROOT_DIR/docs/models.md}"
RUN_DIR="${RUN_DIR:-}"

if [[ -z "$RUN_DIR" ]]; then
  RUN_DIR="$(ls -1d "$RUNS_ROOT"/*/ 2>/dev/null | sort | tail -1 || true)"
  RUN_DIR="${RUN_DIR%/}"
fi

if [[ -z "$RUN_DIR" || ! -f "$RUN_DIR/manifest.json" ]]; then
  echo "no run found in $RUNS_ROOT (override with RUN_DIR=...)" >&2
  exit 2
fi

MANIFEST="$RUN_DIR/manifest.json"
RUN_ID="$(basename "$RUN_DIR")"

pass_count=$(jq '[.models[] | select(.status == "pass")] | length' "$MANIFEST")
fail_count=$(jq '[.models[] | select(.status == "fail")] | length' "$MANIFEST")
total_count=$(jq '.models | length' "$MANIFEST")

rank_expr='
  def rank:
    sort_by([((.korean_score // 0) * -1), (.latency_p95_ms // 999999999), .id]);
  [.models[] | select(.status == "pass")] | rank | .[0].id // ""
'
recommend_id=$(jq -r "$rank_expr" "$MANIFEST")
cloud_id=$(jq -r '
  def rank:
    sort_by([((.korean_score // 0) * -1), (.latency_p95_ms // 999999999), .id]);
  [.models[] | select(.status == "pass" and ((.provider // "") != "ollama") and ((.provider // "") != "lmstudio"))] | rank | .[0].id // ""
' "$MANIFEST")
local_id=$(jq -r '
  def rank:
    sort_by([((.korean_score // 0) * -1), (.latency_p95_ms // 999999999), .id]);
  [.models[] | select(.status == "pass" and (((.provider // "") == "ollama") or ((.provider // "") == "lmstudio")))] | rank | .[0].id // ""
' "$MANIFEST")

metric_for() {
  local id="$1" key="$2"
  jq -r --arg id "$id" --arg key "$key" '.models[] | select(.id == $id) | .[$key] // 0' "$MANIFEST"
}

fmt_score() {
  awk -v v="${1:-0}" 'BEGIN { printf "%.2f", v + 0 }'
}

emit_recommendation_line() {
  local label="$1" id="$2"
  if [[ -z "$id" ]]; then
    echo "**$label**: 추천 없음."
    return
  fi
  local score latency
  score="$(metric_for "$id" korean_score)"
  latency="$(metric_for "$id" latency_p95_ms)"
  echo "**$label**: \`$id\` — korean_score $(fmt_score "$score"), latency_p95_ms ${latency}."
}

collect_manifest_files() {
  find "$RUNS_ROOT" -mindepth 2 -maxdepth 2 -name manifest.json -print 2>/dev/null | sort
}

baseline_rows() {
  local files=()
  while IFS= read -r f; do
    [[ -n "$f" ]] && files+=("$f")
  done < <(collect_manifest_files)

  if (( ${#files[@]} == 0 )); then
    return 0
  fi

  jq -sr '
    [.[].models[]? | select(.korean_score != null) | {id, score: .korean_score}]
    | group_by(.id)
    | map({id: .[0].id, n: length, mean: (map(.score) | add / length)})
    | sort_by(.id)
    | .[]
    | [.id, .n, .mean]
    | @tsv
  ' "${files[@]}" | while IFS=$'\t' read -r id n mean; do
    printf '| `%s` | %s | %.3f |\n' "$id" "$n" "$mean"
  done
}

mkdir -p "$(dirname "$DOCS_MD")"
{
  echo "<!-- GENERATED — do not edit. source: eval/runs/$RUN_ID/manifest.json + eval/recommend.sh -->"
  echo
  echo "# KittyPaw Model 측정 (자동)"
  echo
  echo "Run: \`$RUN_ID\`"
  echo
  echo "## Status Matrix"
  echo
  echo "| id | provider | status | korean_score | latency_p95_ms | detail |"
  echo "|---|---|---|---:|---:|---|"
  jq -r '.models[] |
    "| `\(.id)` | \(.provider // "") | \(.status) | \((.korean_score // 0)) | \((.latency_p95_ms // 0)) | \(.detail // "") |"
  ' "$MANIFEST"
  echo
  echo "## 추천 (한국어 비서)"
  echo
  if [[ -n "$recommend_id" ]]; then
    emit_recommendation_line "추천" "$recommend_id"
    echo
    emit_recommendation_line "API 추천" "$cloud_id"
    echo
    emit_recommendation_line "Local 추천" "$local_id"
  else
    echo "**추천 없음** ($total_count 모델 모두 status=fail — 로그 확인 필요)."
    echo
    echo "_run dir: \`$RUN_DIR\`_"
  fi
  echo
  echo "> Note: free API tier candidates are excluded from default eval after 2026-05-06 rate-limit/timeout failures. Use \`EVAL_INCLUDE_DISABLED=1\` only for explicit API re-tests."
  echo
  echo "## Run History Baseline"
  echo
  echo "| id | n | mean_korean_score |"
  echo "|---|---:|---:|"
  baseline_rows
  echo
  echo "## 통계"
  echo
  echo "- pass: $pass_count"
  echo "- fail: $fail_count"
  echo "- total: $total_count"
} > "$DOCS_MD.tmp"
mv "$DOCS_MD.tmp" "$DOCS_MD"

echo "wrote $DOCS_MD"
