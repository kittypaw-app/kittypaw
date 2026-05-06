# Tasks

## Skeleton

- [x] Create initial monorepo directory shape.
- [x] Document service ownership and architecture constraints.
- [x] Add first contract placeholders and example fixtures.

## Next

- [x] Decide whether existing repositories will be imported with history or as
  snapshots.
- [x] Import existing repositories with history.
- [x] Add root-level contract verification commands.
- [x] Add migration plan for current `kittypaw`, `kittyapi`, `kittychat`, and
  `kittykakao` repositories.
- [x] Add `apps/kittypaw` release workflow plan for `kittypaw/v*` tags.
- [ ] Add initial CI path-filter strategy.

## Plan: OpenAI Function Calling ✅

> Branch `feat/openai-tool-calling` — 커밋 `00d4e48`
> Plan: `.claude/plans/openai-tool-calling.md`

- [x] **T1**: Tool 정의 직렬화 — `convertToolsToOpenAI` + `buildChatRequestBodyWithTools` + AC-10 회귀 단정
- [x] **T2a**: assistant 메시지 변환 — text-only / tool_use only / mixed / parallel / 빈 인자
- [x] **T2b**: user 메시지 변환 — tool_result 단독 / 멀티 순서 보존 / mixed text+tool_result + slog.Warn
- [x] **T3**: 응답 파싱 — tool_calls 디코드 + arguments 타입 분기 + finish_reason 매핑 + usage nil-safe
- [x] **T4**: `GenerateWithTools` end-to-end + multi-turn round-trip + parallel 응답
- [x] **T5**: 회귀 + 3-lane review fix (Marshal panic / empty id error / slog redaction note) + 커밋 완료

## Plan B: Eval Framework Rebuild — Iteration 1 MVP ✅

> Plan: `.claude/plans/kp-eval-rebuild.md` (v2)
> Spec: `.ina/specs/20260506-0242-think-kp-eval-rebuild.md`
> 7 commit: `2417219` T1 → `fe59c9d` T1.1 (5→7) → `6d579df` T2 → `c4b4e30` T3 → `4e2d3e0` T4 → `ed03fed` T5 → `eada162` cleanup
> bats 26 GREEN, AC #9a CI green, ina:review --full 2회 (T3·T4 security fix)

- [x] **T1**: `eval/models.toml` SoT + parse-models.py + bats 7
- [x] **T1.1**: 5 → 7 (groq-llama + ministral-8b 추가, dev-models.sh와 SoT 통일)
- [x] **T2**: `scripts/dev-models-config-generate.sh` + sentinel guard
- [x] **T3**: `eval/secretary_smoke/run.sh --model` opt-in + sed injection regex 가드
- [x] **T4**: `eval/run-models.sh` wrapper (per-run daemon, 7 sequential, port pre-flight, daemon-fail graceful, curl --config)
- [x] **T5**: `eval/recommend.sh` (iteration 1 deterministic) + `docs/models.md` atomic write + Makefile `eval-models` target

**Iteration 2 entry**: korean_score aggregate parser (secretary_smoke summary.md format 확정 후), drift baseline N≥3, GC trap (leak 발견 시), flock (race 발견 시), Cerebras paid tier 채택 시 § 1.1 재검토.

**User manual gates** (vendor keys + emac SSH 환경 필요):
- AC #1/#5: `make eval-models` 1회 실행 → `apps/kittypaw/docs/models.md` 자동 생성
- AC #9b: `make smoke` 사용자 환경 1회
