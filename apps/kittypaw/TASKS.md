# TASKS

KittyPaw 작업 현황. 완료된 Plan 은 Archive 에 한 줄 요약 + 커밋 해시 로 기록. 상세는 git log 참고.

---

## 🔨 In Progress

### Plan B — eval framework rebuild (예정, ina:think 진입 예정) ← 현재

Plan A (`2bb972d` + `89632ae`)로 짜깁기 cleanup 끝남. 다음 단계는 모델 production fitness 측정 시스템:
- LLM judge 자동화 (haiku-4-5 default)
- `eval/models.toml` source-of-truth + dev-models config generate (사용자 결정 "a", 2026-05-06)
- per-run daemon 격리 (`KITTYPAW_CONFIG_DIR` 강제) vs single shared 결정 필요
- status enum 6 (pass/fail/timeout/judge_unavailable/model_capability_limit/insufficient_samples)
- drift baseline 다축 (latency p95 + cost + judge_score)
- `docs/models.md` use case별 추천 자동 갱신
- latency p50/p95 raw + per-query cost
- judge bias 통제 + rate-limit retry + backoff
- docker compose (KittyPaw daemon + ollama linux container, host SSH tunnel mount)

`/ina:think` 으로 spec 작성 → `/ina:plan` → `/ina:build` 순서.

---

### Plan: dev-models — LM Studio MLX backend (emac SSH tunnel) ✅ 완료
*(plan: `~/.claude/plans/lmstudio-mlx-tunnel.md`, spec: `.ina/specs/20260505-1723-think-dev-models-lmstudio.md`)*

KittyPaw에 `provider="lmstudio"` 신규 case 추가 + dev-models harness에 LM Studio MLX backend 통합 (SSH tunnel `localhost:11600 → emac:1234`). 측정 자동화 generalize (`BACKEND={ollama|lmstudio}` matrix). § 5.1.4 가설 (cloud full precision vs ollama Q4 quantization) MLX wire 추가.

**핵심 결정** (think Round 1-3 Q1-Q4):
- Q1=a: provider="lmstudio" 신규 case (registry.go) — telemetry/log clarity. ollama case 패턴 미러.
- Q2=i: `dev-models-measure.sh BACKEND={ollama|lmstudio}` generalize — DRY + 측정 시스템 일관.
- Q3=a: `tunnel-ollama-{start,stop,status}` rename + `tunnel-lms-{start,stop,status}` 신규. ControlPath 분리.
- Q4=α: 7번째 entry `lmstudio-qwen3-30b-mlx` 자동 추가 (provider=lmstudio, model=qwen3-30b-a3b-instruct-2507).

- [x] **T0 — prereq grep + endpoint validate**: registry.go 9 case 패턴 확인 (line 84-91 ollama 미러). const block (line 11-29). emac LM Studio :1234 200 + qwen3-30b-a3b-instruct-2507 loaded ✓.

- [x] **T1 — RED→GREEN: registry.go case "lmstudio"**: `lmstudioDefaultBaseURL` const + case + 2 tests (ollama 패턴 미러). commit `407964e`.

- [x] **T2 — dev-models.sh tunnel rename + tunnel-lms + 7th entry**: `tunnel-{ollama,lms}-{start,stop,status}` 분리 (ControlPath suffix), heredoc 7th entry `lmstudio-qwen3-30b-mlx`, help text 갱신. tunnel.bats 13 cases (7 ollama + 6 lms) GREEN.

- [x] **T3 — measure script generalize + BACKEND param**: `dev-models-ollama-measure.sh` → `dev-models-measure.sh BACKEND={ollama|lmstudio}`, lmstudio path verifies model loaded via /v1/models (GUI-managed, no auto-pull). measure.bats 11 cases (2 generic + 6 ollama + 3 lmstudio) GREEN.

- [x] **T4 — Makefile target rename + 신규**: `dev-models-tunnel-{ollama,lms}{-stop,-status}` + `dev-models-measure BACKEND=... MODEL=... PROMPT=...`. .PHONY 갱신.

- [x] **T5 — 회귀 검증**: `make build` + `make test-unit` + `make lint` (0 issues) + `go test -race ./engine/... ./llm/...` + bats 24/24 모두 PASS.

- [x] **T6 — docs 갱신**:
  - `docs/DEV_MODELS.md`: SSH 섹션 dual-backend로 refactor (공통 사전 + Quick Start ollama/lms 분리 + measure backend matrix + tunnel fail mode lms 케이스 추가). 등록 모델 6→7 (lmstudio-qwen3-30b-mlx). env vars 4 (groq+mistral+gemini+openrouter). provider case 8→10. 상기 stale Make refs (`dev-models-ollama-measure`, `dev-models-tunnel{,-stop,-status}`) 모두 신 명령으로 갱신.
  - `docs/MODEL_GUIDE.md`:
    - § 2.4 헤더 갱신 (`make dev-models-measure BACKEND=ollama`)
    - § 3.5 KittyPaw integration test에 harness Option B 추가 (`make dev-models-measure BACKEND=lmstudio`) + provider="lmstudio" 신규 case 기록
    - § 3.6 신설 — qwen3-30b-a3b-instruct-2507 KittyPaw harness 측정 row placeholder (T7에서 사용자 측정 후 채움)
    - § 5.1.4 stale `make` 명령 갱신
    - 변경 이력 v1.8 추가

- [x] **T6.5 — `lms` CLI 자동화 (B 갈래 P 진입)**: emac에 `lms` CLI 이미 설치됨 (`~/.lmstudio/bin/lms`, 이전 알려진 "미설치" 정보가 stale). `lms load <modelKey> -y --gpu max --ttl 300` 작동 검증 (cold 9.67s, n=1, 2026-05-05). measure script `BACKEND=lmstudio` path 갱신 — `/v1/models` advertised check 제거, `lms load` 자동 호출로 변경. § 3.4 fact 정정 — stall은 `lms get` (download) 한정, CLI 본체 정상. § 3.3 cold load 30.57s → 9.67s (`--gpu max`, n=1). bats 12 cases GREEN.

- [x] **T7 — 사용자 검증 + § 3.6 기록 (n=3 fact)**: 사용자 1차 + 자동 측정 2차 (`make dev-models-measure BACKEND=lmstudio MODEL=qwen3-30b-a3b-instruct-2507 PROMPT='안녕? 한 줄로 자기소개 해줘.'`). warm chat 0-1s (정수 초 분해능 한계), cold load n=3 분산 6.75-9.67s, 응답 페르소나 일관 (3회 분산: KittyPaw 자칭 1/3, 비서 톤 OK 3/3). § 5.1.4 가설 (cloud full vs ollama Q4 vs LM Studio MLX) **3-wire 대조 확인** — MLX instruct variant가 ollama thinking variant 대비 5-15× warm 빠름.

- [x] **T7-fix — tunnel forward verify bug + lms load idempotent guard**: 본 측정 진행 중 발견된 critical bug 2개:
  - **(a) tunnel-{ollama,lms}-start**: SSH `-fN` exit 0 무관 forward bind 실패 시 거짓 "tunnel up" 보고. stale ssh tunnel (e.g., 사용자가 별도 alias로 띄운 잔재) 시 reproduce. post-spawn `lsof` retry loop (5 × 0.3s) 추가 + bats forward-fail mock 케이스 추가.
  - **(b) lms load non-idempotent**: 2번째 호출 시 LM Studio "Model loading was stopped due to insufficient system resources" trigger (17 GB × 2 = 34 GB > 36 GB emac). measure script `lms ps | grep -qE '^${MODEL}[[:space:]]'` idempotent guard 추가 + bats 케이스. § 3.4 fact 추가 기록.

**합리화 차단**:
- "openai+base_url 우회로 작은 변경" → telemetry/log clarity + future backend 패턴 안정화, 거부
- "T0 spec에 검증됨이라 skip" → plan→T1 사이 GUI 죽음 가능, 매 plan 재검증 (이미 통과)
- "tunnel-status 부수적이라 skip" → T2 lsof + curl 2단계 probe 명시
- "T6 docs 작아 commit 묶어도 OK" → 별도 commit (T7 § 3 기록과 분리)

**Out of Scope** (별도 phase):
- 추가 LM Studio MLX 모델 측정 (Llama 3.3 70B / Mistral Nemo / Gemma)
- llama.cpp 통합 (사용자 명시 다음 phase)
- README 추천 표 LM Studio row 즉시 추가 (T7 § 3 기록 후 별도 commit)
- LM Studio API key 인증 지원 (현재 인증 미사용)
- VPN / autossh / wake-on-LAN
- 자동 lms CLI 호출 (§ 3.4 stall fact 회피)

---

### Plan: KMA Ultra-Short Endpoints + Skill Discovery Contract
*(plan: `.claude/plans/kma-ultra-short-and-discovery.md`)*

KittyAPI 에 KMA 초단기실황 + 초단기예보 endpoint 추가. weather-now 갱신 (실황) +
weather-soon 신규 (초단기예보) + `[discovery]` package.toml field forward-compatible 도입.

- [ ] **T1: RED — `TestNowToUltraShortNowcastBaseDateTime`** (basetime_test.go)
- [ ] **T2: GREEN — `nowToHourlyBase` helper + `NowToUltraShortNowcastBaseDateTime`**
- [ ] **T3: RED — `TestNowToUltraShortForecastBaseDateTime`**
- [ ] **T4: GREEN — `NowToUltraShortForecastBaseDateTime`**
- [ ] **T5: RED → GREEN — `TestUltraShortNowcast_Handler` + `UltraShortNowcast()` handler**
- [ ] **T6: RED → GREEN — `TestUltraShortForecast_Handler` + `UltraShortForecast()` handler**
- [ ] **T7: main.go 2 route 등록 + kittyapi `make build/lint/test`**
- [ ] **T8: skills/weather-now 갱신** (main.js: 실황 호출, package.toml v1.2.0 + `[discovery]`)
- [ ] **T9: skills/weather-soon 신규 생성**
- [ ] **T10: cross-repo harness 갱신** (weather-now 실황 + weather-soon 케이스)
- [ ] **T11: kittypaw `make test` 통과 + kittypaw PackageMeta `[discovery]` silent skip 검증**
- [ ] **T12: live verify** (prod 배포 + endpoint 200 확인 — 별 step)
- [ ] **T13: commit (사용자 허락 후)** — Plan B T5 통합 (test harness + TASKS.md)

---

### Plan B — weather-now KMA primary + Skill Test Harness (✅ 코드 완성, kittypaw commit 미완)
*(plan: `.claude/plans/weather-now-kma-and-skill-test-harness.md` v3, Phase 1+2 합의 통과)*

KittyPaw 의 weather-now skill 이 KR 좌표에서 KittyAPI KMA primary 호출하도록 + skill 단위 test 패턴 (Prove-It) 확립. Plan 5 (KMA proxy) 후속. 직전 즉흥 작업의 TDZ silent fallthrough 회귀 방어가 핵심.

- [x] **T1: helper + skeleton (RED → GREEN)** —
      `engine/cross_repo_skill_integration_test.go` (`package engine`, internal `stripAwait` 접근).
      `runExternalSkill(t, skillRelPath, jsCtx, resolver) *core.ExecutionResult` + `httpRecorder{kmaCalls,wttrCalls,handler}` + `mustHostname(u) string` (panic on empty Hostname).
      File-top load-bearing comment: `IMPORTANT: This helper bypasses 4 of 5 production resolver layers ... 3rd skill add → STOP, switch to PackageManager-based helper.`
      Helper body: wrapping (mirrors `engine/executor.go:1518-1532` — magic comment), `sandbox.New(core.SandboxConfig{TimeoutSecs:5})`, `sb.ExecutePackageOpts(ctx, wrapped, map[string]any{}, rawResolver, sandbox.Options{})`.
      Path missing: `if os.Getenv("CI") != "" { t.Fatalf } else { t.Skipf }`.
      Step 1 끝에 case #1 (KR happy) 가 자동 GREEN (Prove-It).

- [x] **T2: case #2 + #3** —
      table entries 추가. case #2 = non-KR 좌표 (37.77, -122.42), wttr happy. case #3 = KR + KMA throw + wttr happy.
      assertions: 정확한 `kmaCalls` / `wttrCalls` 카운트 + Source line full anchor (`_Source: 기상청 (KMA) · Powered by KittyPaw_` 또는 `_Source: wttr.in · Powered by KittyPaw_`).

- [x] **T3: case #4 + #5 (regression locks)** —
      case #4 NaN config (latitude="abc"): kmaCalls=0, wttrCalls=1 — NaN bypass guard 검증.
      case #5 KMA empty envelope (`items: []` valid envelope, NOT throw): kmaCalls=1, wttrCalls=1 — `extractKMACurrent` null path fallthrough 검증.

- [x] **T4: `make build / lint / test`** —
      gofmt, golangci-lint v2 0 issues, `go build ./...` success, `go test ./engine/...` 5 sub-test green.

- [ ] **T5: commit (사용자 명시 허락 후)** —
      **순서 명시 — skills 먼저, kittypaw 다음** (CI race 방지).
      skills: `feat(weather-now): KMA primary for Korean coordinates` (working tree 2 파일).
      kittypaw: `test(engine): cross-repo skill integration harness + weather-now 5 cases` (1 신규 file).
      Conventional Commits.

**Operational Checklist** (코드 외, 운영자 / 별도 follow-up PR):
- [ ] `.github/workflows/ci.yml` 에 skills repo checkout step 추가 — 안 하면 CI 에서 `t.Fatalf`
- [ ] `engine/executor.go:1518` 위에 reciprocal magic comment `MIRRORED-IN: engine/cross_repo_skill_integration_test.go runExternalSkill` (drift 검출 양방향)
- [ ] 운영 서버에서 weather-now 1.1.0 reinstall

**Rollback trigger**: T1 helper 작성 시 `__context__` wrapping / ExecutePackageOpts / stripAwait 중 production parity 깨지면 즉시 plan 으로 복귀 (resolver-bypass risk #5 발현 신호).

---

### Plan A1 — Sub-plan A: Prompt Reframe + Role Tagging + Eval 인프라 (deferred)
*(통합 plan: `.claude/plans/you-ai-distributed-cerf.md` 의 Sub-plan A)*

목표 (사용자 명시 성공 기준): "엔화는?" 같은 단답에 비서답게 — 되묻기 / 스킬 설치 제안 / 검색 확장 제안 / 비서 시점 응답. 케이스 특화 X 일반화. 자체 검증 loop 통과까지.

- [ ] T1 — Test fixture 5 카테고리 (vague/domain/weak_serp/framing/stale)
- [ ] T2 — LLM judge rubric (Anthropic eval define-success 가이드)
- [ ] T3 — Judge runner test infra (real LLM call, integration build tag)
- [ ] T4 — Unit tests RED (DecisionBlock / EvidenceBlock / CapabilityBlock 구조 + role tagging XML)
- [ ] T5 — engine/prompt.go QualityBlock → DecisionBlock + EvidenceBlock + CapabilityBlock 재구성 GREEN
- [ ] T6 — engine/executor.go buildSubLLMMessages 비서 시점 priming + tool_result XML wrap GREEN
- [ ] T7 — 자체 검증 + iteration (judge LLM 채점, 통과 X 시 autoresearch + 재시도 max 3 iter)
- [ ] T8 — Manual smoke + 사용자 성공 기준 통과 보고
- [ ] T9 — 단일 atomic commit (사용자 명시 허락)

**Rollback trigger**: T7 max iteration 도달 + 통과 X → Sub-plan B/C (Tool contract + Ambiguity audit) 진입 결정 사용자 confirm.

---

---

## 📋 Next Up

### Plan 9: Real-use scenario test expansion
- [ ] **Weather/location skill E2E** — `강남역 날씨`, `강남역에 비오나?`, `서울 날씨` 를 Chat BFF → Kittypaw dispatcher → fake registry → fake KittyAPI geo/weather 경로로 검증. 현재 engine 단위 테스트는 있으나 Chat relay E2E는 보강 필요. Current WIP: `강남역 날씨` + fake geo + installed weather reuse 일부.
- [ ] **Installed skill reuse E2E** — `환율 알려줘` → `네` → `환율` → `원화로 환율` 순서에서 재설치 제안 없이 이미 설치된 `exchange-rate` skill을 바로 실행하는지 검증. Current WIP: `환율 알려줘` → `네` → `원화로 환율 다시 알려줘` 일부.
- [ ] **Kakao local E2E** — fake Kakao webhook → `apps/kakao` relay → Kittypaw `KakaoChannel` WebSocket → runner response → fake Kakao callback. 현재 Kakao relay server test와 Kittypaw channel round-trip test가 분리되어 있으므로 둘을 붙인 진짜 경로 테스트 필요.
- [ ] **Telegram local fixture E2E** — fake Telegram `getUpdates`/`sendMessage` HTTP server → `TelegramChannel` → Kittypaw server dispatch → fake Telegram response 검증. 실제 Telegram 서버는 붙이지 않고 HTTP 캡처 형태 mock 사용.
- [ ] **Staff/reflection through Chat BFF** — `/staff finance`, `@finance 환율 리스크 봐줘`, `재무담당 비서를 고용해` 를 Portal/Chat relay 경유 OpenAI-compatible 요청에서도 engine 테스트와 동일하게 동작하는지 검증. `conversation_turns` 기반 reflection/staff evolution side effect 포함.
- [ ] **Offline/auth/token failure E2E** — device offline 시 Chat BFF 503, 만료 device token refresh 후 재연결, 다른 account/account_id relay 요청 forbidden, browser session은 `Authorization` header 없이 cookie만으로 동작하는지 검증. 일부 browser auth header 차단은 현재 E2E에 있음.
- [ ] **Kakao rich response E2E** — agent가 이미지 포함 응답을 만들었을 때 Kittypaw → Kakao relay → callback body가 `simpleImage` 등 Kakao rich response shape로 나가는지 검증.

### Plan 2: 사용자 추가 명령 + 페어링 흐름
- `kittypaw account add <name>` 기반 별도 페어링 흐름
- per-account secrets/config 인프라 (Plan 1 완료) 위에서 `--user` 플래그 도입

### Plan 3: In-chat staff evolution approval
- CLI staff/reflection surface 제거 이후 `TriggerEvolution` 은
  `evolution:pending:<staff>` 제안 생성까지만 deterministic CI 로 고정됨.
- 다음 단계: 대화/서버 surface 에서 pending evolution 을 확인, 승인, 거절하는 UX와
  테스트 추가. 예: `/staff evolution approve <staff>` 또는 웹 설정 API.
- CI 요구: 승인 시 `staff/<id>/SOUL.md` 적용, 거절 시 rejection 기록,
  pending 제거를 모두 로컬 Go 테스트로 검증.

### Plan 5: Server Auto-start UX
- setup → register → 서버 ready 의 polling timeout 부족 (10s) — 첫 부팅 시 store migration / FTS5 init 무거움
- 단 Plan 1 amendment 후 smoke 에서는 정상 부팅됨 — priority 낮음
- 개선 방향: timeout 30s 또는 명시적 `launchctl kickstart -k`

### Plan 6: 카카오 봇 페어링 컨텍스트 인지
- 페어링 wizard 의 인증코드 발송 단계인데 봇이 "안녕하세요. 무엇을 도와드릴까요?" 일반 인사로 응답 (context-blind)
- 출처: 코드에 hardcoded 안 됨 → LLM staff identity response. wizard ↔ 서버 페어링 mode 신호 + system prompt 분기 필요

### Plan 7: Runner Hallucination 방어 — 검색 결과 신뢰성
- 2026-04-26 smoke 발견: `paw> 환율` → "1,483.50원, 2024년 12월 31일 기준" stale/fabricated. 사용자 의심 없으면 잘못된 정보 판단.
- 검색 결과 timestamp/source 추적, LLM "오늘 정보" claim 가드, stale data 명시

### Plan 8: Proactive Skill Discovery
- 2026-04-26 smoke 발견: "날씨"/"미세먼지"/"환율" 일상 쿼리에 generic 검색만. 기대: 도메인 스킬 추천
- system prompt 에 skill registry awareness + proactive recommendation routing

---

## 🎯 Active Backlog

### 🟢 장기 / 스코프 미확정

- [ ] **Permission Checker** — 에이전트 파일 접근 규칙 엔진. 현재 `isPathAllowed` + `AllowedPaths` 넘어서는 룰 엔진 스코프 미정.
- [ ] **bleve 백엔드 전환** — camelCase 토크나이징, 한국어 형태소, BM25 랭킹 필요 시점에.
- [ ] **시맨틱 검색** — LLM ranking / 임베딩 벡터. 스코프 미정.
- [ ] **Desktop GUI** — Wails vs Fyne 선정, 데몬 자동 실행 + WebView 채팅 UI. CLI + Web API 로 충분한 현 시점 우선순위 낮음.

---

## 📦 Deferred (미착수)

- [ ] **Skill Gallery 웹 UI + `SkillSetting` 저장** — 현재 `kittypaw skill install` CLI 만 존재 (Plan 19). 웹 갤러리 + 동적 settings 폼 + SQLite 암호화 저장이 미구현.
- [ ] **Telegram Pair Code** — `FetchTelegramChatID` 의 서버 race + multi-user 신원 탈취. Kakao pair-code 패턴 이식 예정. account==user 모델 확정 후 재검토 가치 있음.

---

## ✅ Archive

완료 순 (최신 → 과거). 커밋 해시 첨부.

### 2026-05-06

- **Plan A — KittyPaw 짜깁기 cleanup** — `scripts/tunnel.sh` (generic SSH LocalForward helper, lsof 5×1s + curl -fsS + idempotent stop, ControlPath `/tmp/kittypaw-tunnel-${USER}-${name}.sock` single-host scope) 신설로 `dev-models.sh` 의 6개 tunnel 함수 (90% 중복) → 6 one-liner case 위임. `dev-models-measure.sh` + `dev-models-measure.bats` (총 547줄) 삭제 — Plan B의 eval framework rebuild로 대체 예정. CLAUDE.md "Testing Isolation" 섹션의 stale `KITTYPAW_HOME` 표기 → 실제 코드가 읽는 `KITTYPAW_CONFIG_DIR` (`core/config.go:482`) 정정. bats 15→17 (HTTP 5xx + stop idempotent 신규). 10 files, 245 ins / 748 del. `2bb972d` + `89632ae`(.gitignore .ina/)

### 2026-04-26 이후

- **`account add` interactive fallback** — `kittypaw account add <name>` (no flags) 시 4 단계 prompt (telegram token / LLM provider / api-key / model) 진입. Secrets 는 `term.ReadPassword` 로 echo-off (TTY only). `needsAccountPrompt` gating + `*os.File` type assert 로 test fallback path. CI / scripted (flag/env 있음) 호환성 그대로. `cc7dd7e`
- **Secrets + Config per-account Alignment** — OAuth 토큰, Kakao relay URL, api_url, config.toml 모두 글로벌 `~/.kittypaw/` → per-account `~/.kittypaw/accounts/<id>/` 정렬. `core.LoadAccountSecrets(accountID)` + `ConfigPath()` 의미 변경. `ChannelSpawner.Reconcile` 시그니처 보존 (load-bearing sync contract). `Server.secrets` 필드 제거로 stale-cache overwrite (web kakao_register → setup_complete data-loss path) 차단. c1a0c58 OAuth-once-per-host 의도 폐기. 5 회귀 테스트 + 3 fixture rewrite. 마이그레이션 0 — 사용자 wipe + 재설치. `8da0bd3`

### 2026-04-20 이후

- **File.summary + llm_cache** — `File.summary(path, options?)` JS skill: 워크스페이스 파일 LLM 요약 + generic `llm_cache` 테이블 캐시. `engine/summary.go` QuerySummary() + singleflight 미스 중복 제거 + `ON CONFLICT DO UPDATE` UPSERT (force_refresh 가 오염된 row 덮어쓸 수 있음) + prompt-injection 3-layer 방어 (system prompt + fenced markers + `sanitizeBasename`) + charge-after-response 예산 회계 + `RemoveFile` GC 캐스케이드. 19 unit tests. `349d77a`
- **MoA (Multi-Model Aggregation)** — `Moa.query(prompt, options?)` JS skill: `[[models]]` 병렬 fan-out + Default 모델 합성. `engine/moa.go` QueryMoA() + sync.WaitGroup 변형 (partial failure 관용) + per-model ctx timeout + maxModels=5 가드 + 후보 1개 시 합성 skip + `SharedTokenBudget` 회계. 9 unit tests. `513b7ca`
- **Plan 27 Follow-up 2** — Indexer v2 overflow 자동 복구: `fsnotify.ErrEventOverflow` 감지 → 500ms debounce + 30s backoff 로 전 workspace full reindex + `OverflowCount` / `RecoveryCount` atomic 관측. `9cfce19`
- **Plan 27 Follow-up** — Indexer v2 hardening (bundle 1+2): dir-remove FTS cascade + watcher partial-add visibility. `e575f53`
- **Plan 27** — Workspace Indexer v2: fsnotify live filesystem watching + FTS5 incremental update. `8c45a4f`
- **Setup → Chat Auto-Entry** (Plan 26) — `kittypaw setup` 완료 시 TTY 에서 chat REPL 자동 진입 + 서버 hot-reload. `814cc89` + `74acdaf`(/reload validation)
- **Account Remove** — `kittypaw account remove`: LIFO 드레인 → team-space membership/config scrub → `.trash/` 이동 + BotFather 경고. `4ee9c95`
- **Multi-user Blockers** — MB1 account ID regex 완화, MB2/MB3 는 `account==user` 확정으로 revert. `e24cd9e` + `aedf04a`(revert)

### Plan 25 — Family Multi-Account (macOS 단일 서버, 7 personal + 1 family)

- **Plan A** — multi-account routing foundation (Event.AccountID, ChannelSpawner keying, fail-fast 중복 탐지). `8b3860a`
- **Plan B** — team space: cross-account `Share.read` + `Fanout.send/broadcast`. `a62075b` + `26ea597`(gate + dispatch)
- **Plan C** — operations: account health + panic isolation (`57fe75a`), `kittypaw account add` CLI (`4fae3a3`), admin RPC hot-activate (`eb26ec7`), E2E demos (`aa7f9cb`)
- Plan B→C 이월 — account fields wire + legacy migration activation. `83a986b`

### 2026-04-18 이전 (주제별)

- **Package Context Declaration** — Package 에 Context 필드 + UserConfig + event-in-context + locale. `18cac99` + `fd71ab0`
- **Discovery Endpoint Migration** — `/discovery` 로 api_base_url/chat_relay_url/kakao_relay_url/skills_registry_url topology 동적 해석.
- **Relay Rust Rewrite** — KakaoTalk relay TS→Rust (axum + SQLite, self-hosted single binary).
- **Plan 24** — Web Tool Quality (HTML→Markdown, SearchBackend DDG/Tavily) + Runner Observe Loop.
- **Plan 23** — Prompt Quality: SystemPrompt 블록 분리 + QualityBlock + channelHint + 토큰 예산.
- **Plan 22** — Docs site Go rewrite alignment (docs/, docs/en/, docs/ja/ 전면 갱신).
- **Plan 21** — Permission Dialog (Confirmer interface + Telegram inline keyboard + audit log).
- **Plan 20** — GitHub Registry Packages (RegistryConfig + 3파일 다운로드 + SSRF 방어).
- **Plan 19** — Skill Install System (SKILL.md + GitHub resolver + SHA256 + prompt/native 모드).
- **Plan 18** — CLI Command Completion (suggestions / fixes / reflection / staff / memory / channels / reload 17개 메서드).
- **Plan 17** — Thin Client Architecture (CLI → Server HTTP/WS + DaemonConn flock + WebSocket 스트리밍).
- **Plan 16** — Workspace Indexer v1 (FTS5 full-text search + File.search/stats/reindex).
- **Plan 15** — Staff Preset System (`core.Staff` + presets + DetectDirty + preset status).
- **Plan 14** — Channel Hot-Reload (ChannelSpawner + Reconcile + no-drop dispatch).
- **Plan 13** — Vision / Image Skills (Claude/OpenAI/Gemini vision + OpenAI/Gemini image gen).
- **Plan 12** — Workspace Hardening (Workspace CRUD + isPathAllowed + 10MB 제한 + symlink walk).
- **Plan 11** — Package System (core/package.go + secrets.json + registry + cron + CLI).
- **Plan 10** — Reflection System (topic preferences + weekly report + evolution approve/reject).
- **Plan 9** — Runner Delegation (OrchestrateRequest JSON + errgroup fan-out + PM synthesize).
- **Plan 8** — SharedTokenBudget (migration 016). Auto-Fix Loop은 미검증 약속이라 retire (commit `feat!: retire LLM-driven self-healing`).
- **Plan 7** — MCP Registry (connect + listTools + callTool + prompt injection).
- **Plan 6** — Memory Context → LLM Prompt Injection (facts/failures/stats 구조).
- **Plan 5** — Teach Loop (natural language → skill generation + syntax check + approve).
- **Plan 4** — Channel SessionID + Response Retry (user_id → SessionID + pending_responses 재시도).
- **Plan 3** — E2E Runner Loop Test (mock provider + sandbox round-trip + retry).
- **Plan 2** — LLM Provider Resilience (doWithRetry + jitter + SSE scanner buffer + error events).
- **Plan 1** — Skill Scheduler Wiring (sync.Once Stop + in-flight guard + SetLastRun 실패 처리).
- **LLM Test Infra** — HTTP 클라이언트 주입 functional option + OpenAI stream_options + onToken nil-guard.
