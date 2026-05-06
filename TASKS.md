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

## Plan B: Eval Framework Rebuild — Iteration 1 MVP ← 현재

> Plan: `.claude/plans/kp-eval-rebuild.md` (v2)
> Spec: `.ina/specs/20260506-0242-think-kp-eval-rebuild.md`
> Goal: 5 모델 × secretary_smoke 자동 측정 + `apps/kittypaw/docs/models.md` 자동 갱신 (한국어 비서 1줄 추천)
> 진입 commit: `02c0e2d` (Plan A 직후)

- [ ] **T1**: `eval/models.toml` 신설 + 파싱 helper — 5 모델 entry (groq-qwen / mistral-medium / gemini-flash-lite / openrouter-llama-3.3 / lmstudio-qwen3-30b-mlx) + parse helper (`uv run python -c "import tomllib"` 또는 jq). bats: TOML 파싱 정확성 + 5 entry 모두 추출 + `api_key_env` 필드 존재.

- [ ] **T2**: `scripts/dev-models-config-generate.sh` + `dev-models.sh setup` 통합 — generator stdout에 `[[llm.models]]` block + sentinel header. setup이 sentinel 떠서 config.toml 삽입. 사용자 직접 편집 감지 시 abort + diff (recovery는 git checkout). bats: generated 일관 / 편집 감지 abort / idempotent re-run.

- [ ] **T3**: `eval/secretary_smoke/run.sh --model` flag (opt-in) — flag 있을 때만 config swap + reload + trap cleanup. 없을 때 default 회귀 0. bats: --model 없을 때 default path / 있을 때 swap + cleanup.

- [ ] **T4**: `eval/run-models.sh` wrapper (per-run daemon, sequential 5 모델) — runID + EVAL_TMP TMPDIR fallback + readiness probe (`/healthz` AND first chat, 30s timeout). **Daemon start 실패 시 해당 모델 status=fail 기록 + 다음 모델 진행** (전체 abort X). Auth fail-fast (5 키 set 확인 → exit 2). Judge consistency (`s1 == s2`, epsilon=0). raw `[s1, s2]` manifest 기록. bats: 인증 missing exit 2 / TMPDIR 작성 실패 exit 2 / 격리 unchanged / daemon start fail 시 다음 모델 진행 / judge epsilon=0 abort.

- [ ] **T5**: `eval/recommend.sh` + `docs/models.md` render + Makefile target + 회귀 — manifest.json → max korean_score (status=pass 중), 동률 시 warm latency p95. 모든 fail 시 "추천 없음". atomic write (`.tmp` → `mv`). Makefile `eval-models` target. bats: 동률 tiebreaker / 모든 fail 시 "추천 없음" / SIGTERM 시 부분 docs/models.md 미생성. 회귀 (AC #9a): `make build && make test-unit && make lint && go test -race ./engine/... ./llm/...` 통과 (키 의존 0). manual gate (AC #9b): `make smoke` 사용자 환경 1회 실행.
