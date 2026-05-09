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

## Plan: Projects Board Replacement ← 현재

> App: `apps/kittypaw`
> Spec: `apps/kittypaw/docs/superpowers/specs/2026-05-09-projects-board-design.md`
> Plan: `apps/kittypaw/docs/superpowers/plans/2026-05-09-projects-board-implementation.md`
> Worktree: `main` worktree에서 진행 (사용자 요청)

- [x] **D1**: Workspace/Kanban 대체 방향 설계 확정.
- [x] **D2**: Project, Ticket, Board, Conversation, Job, Driver, Staff Assignment 개념 정리.
- [x] **T0**: 구현 계획 작성 및 DB/API/UI 영향 범위 점검.
- [x] **T1**: Project/Ticket/Job/Driver/Conversation scope 저장소 기반 구현.
- [x] **T2**: `/api/v1/projects`, `/api/v1/tickets`, `/api/v1/jobs`, `/api/v1/drivers` API 구현.
- [x] **T3**: Projects/Board 중심 웹 UI로 Workspace/Kanban 사용자 표면 대체.
- [x] **T4a**: Engine Projects tool, slash command, Brief Draft 저장/commit 흐름 구현.
- [x] **T4b-1**: Project 생성 직후 kickoff assistant turn 기록 및 API 응답 제공.
- [x] **T4b-2**: project/ticket conversation scope 기반 File 도구 범위 제한.
- [x] **T4b-3**: project/ticket chat UI와 API/WS conversation id 전달 경로 연결.
- [x] **T4b-4**: 비어있지 않은 Project Folder 구조 스캔 후 티켓 초안 제안 흐름 연결.
- [x] **T5**: 회귀, E2E, LLM 호출 포함 전수테스트 경로 정리 및 최종 리뷰 정리 (`8c84f9b`, `5ae4c2f`, `faa1224`, `bbe2669`).

## Follow-up: Product-Wide I18n

> Spec: `docs/superpowers/specs/2026-05-08-product-i18n-design.md`
> Plan: `docs/superpowers/plans/2026-05-08-product-i18n-foundation.md`
> Branch: `feature/product-i18n`
> Local foundation commits: `630b25c` → `8c38d4b`

- [x] **P0**: Create isolated worktree and baseline server/UI tests.
- [x] **T1**: Central catalog, glossary, schema, generator, generated local web asset.
- [x] **T2**: Local account locale preference API.
- [x] **T3**: Local web i18n runtime and globe picker.
- [x] **T4**: Translate local `kittypaw` App/Chat/Settings/Skills/Kanban UI strings.
- [ ] **T5**: Follow-up plans for Space, legacy Chat, Portal, API error codes, and CLI cleanup.

## Candidate: Runner Loop / Channel Safety Backlog

> Input: Hermes / OpenClaw comparison thread. Treat as product-direction
> inspiration only; verify claims against upstream docs/repos before designing.

- [ ] **Session search / recall**: Persist searchable conversation history and
  retrieve relevant past sessions with summarization for current-turn context.
  Useful for "지난번 설정", "전에 만든 스킬", 반복 workflow recall.
- [ ] **Skill promote loop**: Reflection 후보를 곧바로 자동 실행 코드로
  만들기보다, 사용자 승인 후 inspect 가능한 skill draft 로 승격한다.
  Output should include `package.toml`, required config, trigger proposal, and
  manual-run checklist before cron/channel enablement.
- [ ] **Skill risk preview**: Before install or generated-skill enablement,
  summarize package origin, primitives, trigger type, secret requirements, file
  writes, shell/git access, and recommended first-run mode.
- [ ] **Channel access policy**: Add per-channel allowlist / pairing /
  mention-required policy so Telegram, Slack, Discord, Kakao, and WebSocket
  entrypoints do not all share the same trust posture.
- [ ] **카카오톡 선발송 기능 검토**: 현재 OpenBuilder callback relay 는
  사용자 요청에 대한 응답 경로로 유지하고, BizMessage/FriendTalk/Brand
  Message/Channel Message 등 별도 proactive outbound 경로의 정책,
  자격 증명, 수신자 식별자, 과금/동의 요건을 검토한다.

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

## Plan B: Eval Framework — Iteration 2 ✅

> Spec: `.ina/specs/20260506-1630-think-kp-eval-iter2.md` (v2, 3 scope)
> Plan: `.claude/plans/kp-eval-iter2.md`
> 진입: HEAD `552bb95` (Iteration 1 완료) — 첫 실측 7/7 fail → framework 자체 검증 ✓
> 목표: 7→9 entry (ollama×2) + backoff 강화 (60s/180s) + lmstudio readiness graceful fail. 2026-05-06 free API 429/timeout 확인 후 기본 추천은 **local 우선**, API는 paid/credit tier에서 별도 재평가.

- [x] **T1**: `eval/models.toml` 7→9 (ollama-qwen2.5-32b + ollama-gemma4) + `.gitignore` (docs/models.md, eval/runs/, .state/) + bats T1 entry count 7→9 + ollama provider id 검증
- [x] **T2**: `run-models.sh` INTER_MODEL_SLEEP 10→60 + PER_MODEL_TIMEOUT 120→180 + `secretary_smoke/run.sh` `KITTYPAW_EVAL_FIXTURE_LIMIT` env (default 0=no limit, set 시 head -N per category) + bats env defaults + fixture_lines 회귀 0
- [x] **T3**: `run-models.sh` per-model loop 시작 시 ollama tunnel ping (`curl :11500/api/tags`) → fail 시 graceful `record_model fail` + bats `OLLAMA_PROBE_URL` override 케이스
- [x] **T4**: `run-models.sh` per-model loop 시작 시 lmstudio readiness (`ssh emac lms ps | grep model`) → 미로드 시 graceful `record_model fail` (NO auto-load) + bats ssh PATH stub 케이스
- [x] **T5a**: 회귀 (bats 45 GREEN) + server websocket timeout regression (`go test ./server`) + shell syntax 검증
- [x] **T5b**: local 중심 실측 cycle (`RUN_ID=local-wsfix-1778063606 PER_MODEL_TIMEOUT=900 INTER_MODEL_SLEEP=0 make eval-models`) 완료. 결과: pass 0/3 → 추천 없음. lmstudio model 미로드 / qwen2.5 timeout / gemma4 기준 미달 + stale 중 EOF.
- [x] **T5c**: 측정 안정화 — `/ws`를 HTTP middleware timeout(60s) 밖으로 이동해 local long turn 허용 + rate-limit classifier false positive(UUID `429b`) 수정 + local timeout detail provider-aware로 수정.
