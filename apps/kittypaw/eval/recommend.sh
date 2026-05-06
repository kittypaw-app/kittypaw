#!/usr/bin/env bash
# Plan B Iteration 1 — recommendation rendering.
#
# Reads manifest.json from latest run (or RUN_DIR override) → emits raw matrix
# + recommendation 1-line to docs/models.md (atomic write).
#
# Iteration 1 decision rule (yagni — score/latency aggregate parser는 iteration 2):
#   pass 모델 중 첫 entry. 동률 tiebreaker · korean_score · latency p95는
#   secretary_smoke summary.md format 확정 + 데이터 누적 후 도입.
#
# 모든 모델 fail → "추천 없음 (N 모델 모두 실패 — 로그 확인 필요)" + exit 0.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EVAL_DIR="$ROOT_DIR/eval"
RUNS_ROOT="${RUNS_ROOT:-$EVAL_DIR/runs}"
DOCS_MD="${DOCS_MD:-$ROOT_DIR/docs/models.md}"
RUN_DIR="${RUN_DIR:-}"

# Latest run discovery (sortable runID = epoch-pid).
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

# Recommendation: first pass entry (deterministic). Iteration 2 → korean_score.
recommend_id=""
if (( pass_count > 0 )); then
  recommend_id=$(jq -r '[.models[] | select(.status == "pass")] | .[0].id' "$MANIFEST")
fi

# Atomic write — partial docs/models.md 차단 (SIGTERM 시 .tmp만 leak).
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
  echo "| id | status | detail |"
  echo "|---|---|---|"
  jq -r '.models[] | "| `\(.id)` | \(.status) | \(.detail // "") |"' "$MANIFEST"
  echo
  echo "## 추천 (한국어 비서)"
  echo
  if [[ -n "$recommend_id" ]]; then
    echo "**추천**: \`$recommend_id\` — pass 모델 중 첫 entry (iteration 1 deterministic)."
    echo
    echo "_iteration 2 도입 예정: korean_score 정렬, 동률 시 warm latency p95 tiebreaker._"
  else
    echo "**추천 없음** ($total_count 모델 모두 status=fail — 로그 확인 필요)."
    echo
    echo "_run dir: \`$RUN_DIR\`_"
  fi
  echo
  echo "## 통계"
  echo
  echo "- pass: $pass_count"
  echo "- fail: $fail_count"
  echo "- total: $total_count"
} > "$DOCS_MD.tmp"
mv "$DOCS_MD.tmp" "$DOCS_MD"

echo "wrote $DOCS_MD"
