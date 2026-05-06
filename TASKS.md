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

## Plan B: Eval Framework — Iteration 2 ← 현재

> Spec: `.ina/specs/20260506-1630-think-kp-eval-iter2.md` (v2, 3 scope)
> Plan: `.claude/plans/kp-eval-iter2.md`
> 진입: HEAD `552bb95` (Iteration 1 완료) — 첫 실측 7/7 fail → framework 자체 검증 ✓
> 목표: 7→9 entry (ollama×2) + backoff 강화 (60s/180s/fixture 2) + lmstudio readiness graceful fail. **2nd 실측에서 1 local + 1 cloud 각각 status=pass**.

- [x] **T1**: `eval/models.toml` 7→9 (ollama-qwen2.5-32b + ollama-gemma4) + `.gitignore` (docs/models.md, eval/runs/, .state/) + bats T1 entry count 7→9 + ollama provider id 검증
- [x] **T2**: `run-models.sh` INTER_MODEL_SLEEP 10→60 + PER_MODEL_TIMEOUT 120→180 + `secretary_smoke/run.sh` `KITTYPAW_EVAL_FIXTURE_LIMIT` env (default 0=no limit, set 시 head -N per category) + bats env defaults + fixture_lines 회귀 0
- [x] **T3**: `run-models.sh` per-model loop 시작 시 ollama tunnel ping (`curl :11500/api/tags`) → fail 시 graceful `record_model fail` + bats `OLLAMA_PROBE_URL` override 케이스
- [x] **T4**: `run-models.sh` per-model loop 시작 시 lmstudio readiness (`ssh emac lms ps | grep model`) → 미로드 시 graceful `record_model fail` (NO auto-load) + bats ssh PATH stub 케이스
- [x] **T5a**: 회귀 (bats 32 GREEN — 26 기존 + 6 신규) + AC #9a (`make build && test-unit && lint && go test -race`) 통과
- [ ] **T5b**: 두 번째 실측 cycle (`make eval-models`) — manual gate (vendor keys + emac state) — AC #2 (1 local + 1 cloud pass) 확인
