# kittypaw-api v1

> Historical task log for the pre-portal-split KittyAPI repository. Runtime
> identity, OAuth, users, devices, discovery, and JWKS moved to `apps/portal`.
> Current KittyAPI ownership is `/v1/*` public data APIs and resource-data
> storage. Treat older auth/JWKS entries below as archive context unless they
> are explicitly promoted in a new monorepo plan.

## Plan 1: Project Scaffolding ✅

- [x] **T1: Go module + health endpoint** — `go mod init`, `cmd/server/main.go` (chi + /health), `internal/config/config.go`, 테스트 통과
- [x] **T2: Makefile + .gitignore + .env.example** — build/test/lint/run 타겟
- [x] **T3: golangci-lint** — `.golangci.yml` v2, `make lint` 통과
- [x] **T4: lefthook** — `lefthook.yml` (pre-commit: fmt + lint, commit-msg: conventional commit)
- [x] **T6: GitHub Actions CI + CLAUDE.md** — `.github/workflows/ci.yml` (lint → test)

## Plan 2: Auth ✅

- [x] **T1: Database foundation** — migrations (users, refresh_tokens) + pgx pool + `UserStore` interface + `PostgresUserStore`
- [x] **T2: JWT package** — `Sign`, `Verify` (HS256, 15min TTL)
- [x] **T3: OAuth infra** — `StateStore` (10K cap, 10min TTL, lazy eviction) + PKCE helpers
- [x] **T4: OAuth Google** — `HandleGoogleLogin` + `HandleGoogleCallback` (PKCE + code exchange + upsert + tokens)
- [x] **T5: OAuth GitHub** — `HandleGitHubLogin` + `HandleGitHubCallback`
- [x] **T6: Refresh token rotation** — `RefreshTokenStore` + `HandleTokenRefresh` (7-day expiry, reuse detection)
- [x] **T7: Auth middleware + /auth/me + CORS + route wiring** — JWT middleware, context helpers, CORS, full route wiring

## Plan 3: Data Proxy ✅

- [x] **T1: In-memory cache** — `Cache` (Get/Set/GetStale, TTL, stale-while-revalidate, background cleanup)
- [x] **T2: Rate limiting** — fixed window counter + daily 10K cap + Retry-After header + middleware
- [x] **T3: /v1/air endpoint** — 에어코리아 프록시 (15s timeout, cache, Warning header on stale, 502 on failure)
- [x] **T4: Route wiring** — cache + ratelimit + proxy integrated into main.go

## Plan 4: Calendar API (특일정보) ✅

- [x] **T1: Config** — `HOLIDAY_API_KEY` env var + `.env.example`
- [x] **T2: HolidayHandler** — 한국천문연구원 특일정보 프록시 (공휴일, 기념일, 24절기) + 테스트 10개
- [x] **T3: Route wiring** — `/v1/calendar/*` 라우트 등록
- [x] **T4: 검증** — 전체 테스트 65개 통과, lint 0 issues

## Plan 5: KMA Village Forecast Wrapper + KittyPaw fallback wiring ✅

> Spec: `.claude/plans/data-go-kr-wrappers.md` (v3, Phase 1+2 합의 통과)
> Goal: `/v1/weather/kma/village-fcst` proxy + `weather-briefing` skill 의 KMA fallback hook
> Delivery model: M1 (master key + cache + redistribution OK — data.go.kr 공공데이터)
> Atomic single commit (사용자 명시 허락 후)

- [x] **T1: `internal/proxy/kma/` sub-package + grid** —
      RED: `kma/grid_test.go` 11 case (5 도시: 서울 60,127 / 부산 98,76 / 대구 89,90 / 인천 55,124 / 제주 53,38 + 경계 ±0.001° 5쌍 + 한반도 외 lat=0/lon=0 + lat=43.5 → ErrOutOfKoreaPeninsula). `make test ./internal/proxy/kma/...` fail 확인.
      GREEN: `kma/grid.go` LCC 변환 (RE=6371.00877, GRID=5.0, SLAT1=30°, SLAT2=60°, OLON=126°, OLAT=38°, XO=43, YO=136) + range check (lat∈[33,39], lon∈[124,132]) + ErrOutOfKoreaPeninsula sentinel. `kma/doc.go` package doc. all green.

- [x] **T2: `internal/proxy/kma/basetime`** —
      RED: `basetime_test.go` 10 case (정상 05:30 → today 0500 / 경계 직전 05:09 → today 0200 / 직후 05:11 → today 0500 / 정확 05:10 → today 0500 / 자정 직후 00:30 → yesterday 2300 / 02:09 → yesterday 2300 / 02:11 → today 0200 / 비-슬롯 13:30 → today 1100 / 23:09 → today 2000 / 23:11 → today 2300). 시그니처 강제: `func NowToBaseDateTime(now time.Time) (baseDate, baseTime string)`. fail 확인.
      GREEN: `basetime.go` 구현. 8 슬롯 + 발표 후 10분 지연 + 날짜 경계. all green.

- [x] **T3: weather handler RED** —
      `internal/config/config.go` 에 `WeatherAPIKey` 필드 + `.env.example` 갱신.
      `internal/proxy/weather.go` stub: `WeatherHandler{Cache, HTTPClient(Timeout=15s), APIKey, BaseURL, Now func()time.Time}` + `VillageForecast() http.HandlerFunc` (빈 구현).
      `internal/proxy/weather_test.go` 6 sub-test:
        - happy + cache hit counter (`atomic.Int32` upstream call counter — 1번째 1, 2번째 *여전히* 1)
        - resultCode "03" → 502, log, 캐시 X
        - upstream 503 → cache.GetStale → 200 + `Warning: 110`
        - timeout (HTTPClient.Timeout=100ms test override + mock 200ms sleep) → 502 + context cancel
        - lat/lon 누락 → 400
        - 한반도 외 (lat=0) → 400
      fail 확인.

- [x] **T4: weather handler GREEN** —
      `weather.go` 본체 — VillageForecast 의 ⓛ–⑨ 흐름. inline `parseKMAError(body) (resultCode, resultMsg, isError)`. `kma.LatLngToGrid` + `kma.NowToBaseDateTime(h.Now())`. cacheKey prefix `"kma:village:"`. `http.NewRequestWithContext` 패턴. 6 sub-test all green.

- [x] **T5: route + integration test** —
      `cmd/server/main.go` `NewRouter` 에 `/v1/weather/kma/village-fcst` 라우트.
      `weather_integration_test.go` (`//go:build integration`): env 없으면 `t.Skipf("WEATHER_API_KEY not set")`, 있으면 서울 좌표 1회 실 KMA 호출 + `response.body` 존재 확인.
      Router-level test: 동일 IP 6회 anon 호출 → 6번째 429 (AC #7).

- [x] **T6: KittyPaw `weather-briefing` skill fallback wiring** —
      `../skills/packages/weather-briefing/main.js` 수정 (~20줄).
      `tryKMAFallback(lat, lon)` 추가 — KR 좌표 (lat∈[33,39], lon∈[124,132]) 한정 KittyAPI `/v1/weather/kma/village-fcst` 호출 (Http.get + timeout_ms=5000).
      기존 `fetchWeather()` 에 hook — Open-Meteo 실패/timeout 시 fallback.

- [x] **T7: build + lint + test + commit** —
      `make build / make lint (golangci-lint v2) / make test` 모두 pass.
      Conventional Commits — `feat(proxy): KMA village forecast wrapper + skill fallback`.
      **사용자 명시 허락 후** atomic single commit.

**Operational Checklist** (코드 외, 운영자 작업):
- [ ] data.go.kr 기상청 단기예보 (`15084084`) 인증키 신청 + 승인 (1-3일)
- [ ] 운영 서버 환경변수 `WEATHER_API_KEY` 등록
- [ ] 프로덕션 smoke: 서울 좌표 1회 curl → 200

## Plan 6: Places DB + /v1/geo/resolve

> Spec: `.claude/plans/geo-address-coords.md` (v9, PR-2 의사결정 박제)
> Goal: 자체 통합 places DB로 LLM 자연어 위치 입력 → 좌표 변환. **외부 API 의존 0**
> 데이터: Wikidata(CC0) + 서울교통공사 1~8호선(제한없음) + 별칭 50 + 행안부 도로명주소(PR-2, 제한없음)
> PR-1: Wikidata + 서울교통공사 + 별칭 + 벤치마크 ✅
> PR-2: 행안부 도로명주소 (EPSG:5179 → WGS84 pure-Go 변환 + 별도 addresses 테이블) ← **현재 (build target)**

### PR-1 ✅ (8 태스크 — TDD 사이클)

- [x] **T1: migration** —
      RED: `migrations/00X_create_places.up.sql` + `down.sql`. `make migrate` → places + alias_overrides + pg_trgm + 인덱스 생성. `SELECT 1 FROM places LIMIT 0` 통합 테스트.
      GREEN: SQL 작성. `UNIQUE (source, source_ref)` + GIN 인덱스. pg_trgm 권한 부재 시 명시적 에러.

- [x] **T2: model/place.go** —
      RED: `internal/model/place_test.go` 5 함수 테이블 테스트 — `FindExact`, `FindByAlias`, `FindByFuzzy`, `FindAliasOverride`, `Upsert`. fixture INSERT 후 검증.
      GREEN: pgx raw SQL 5 함수. ORDER BY `similarity DESC, (CASE type WHEN 'landmark' THEN 0 ELSE 1 END) ASC, source_priority DESC, id ASC`.

- [x] **T3: proxy/places.go + places_errors.go** —
      RED: `internal/proxy/places_test.go` Resolve 통합 테스트 — NFC 정규화·길이 검증·typeHint·chain 5단계 + 400/414/422 응답.
      GREEN: `Resolve` chain (alias_override → exact → alias → fuzzy → 422). 에러 enum const.

- [x] **T4: cmd/seed-wikidata** —
      RED: `cmd/seed-wikidata/main_test.go` fakeUpstream으로 SPARQL mock — 페이징·재시도·swap·체크포인트.
      GREEN: SPARQL 클라이언트 (offset+limit 1000, max retry 3, exponential backoff). transactional swap (places_import_<run_id>). 체크포인트 `places_import_state.json`.

- [x] **T5: cmd/seed-seoul-metro** —
      RED: 작은 CSV 입력 → places에 정확 INSERT 검증.
      GREEN: CSV 파서 + COPY FROM ON CONFLICT.

- [x] **T6a: 별칭 50개 + 골든 17건** —
      RED: `migrations/00Y_seed_alias_overrides.up.sql` (§10 정책 준수). `testdata/golden_cases.json` 12 positive + 5 negative. `internal/proxy/places_golden_test.go`.
      GREEN: 50개 SQL 시드 + 골든 테스트 통과 (코엑스/광화문/강남역/63빌딩/잠실역/장한평역/롯데월드타워/경복궁/DDP/코엑스몰 + 422 케이스).

- [x] **T6b: corpus 인프라 + 벤치마크 cmd** (24건 bootstrap, 100건 확장은 운영 후 follow-up) —
      RED: `testdata/korean_corpus.json` 100건 (50 시나리오 + 50 변형 NFC/NFD/한자/오타). `cmd/benchmark-resolve/main.go`. `make benchmark-resolve` 타겟.
      GREEN: corpus 작성 + 측정 + **precision ≥ 0.85 게이트**. 미달 시 alias 보강 또는 threshold 조정.

- [x] **T7: 라우트 등록 + README + Makefile + docs/maintenance.md** —
      RED: `cmd/server/main.go` `/v1/geo/resolve` 라우트. 통합 테스트.
      GREEN: 라우트 1줄 + README LLM normalize 가이드 섹션 + Makefile (`seed-wikidata`, `seed-seoul-metro`, `benchmark-resolve`). `make build/lint/test` pass. **사용자 명시 허락 후** atomic commit.

**Operational Checklist**:
- [ ] PostgreSQL pg_trgm superuser 1회 설치 (RDS 시 `rds.extensions = pg_trgm` 파라미터 그룹)
- [ ] `make seed-wikidata` 첫 임포트 (~10k row, 수 분)
- [ ] `make seed-seoul-metro` 첫 임포트 (~280 row, 수 초)
- [ ] cron: Wikidata 주간, 서울교통공사 연 1회

**Follow-up Issues** (PR-1 범위 외, Phase 2 리뷰 결과 포함):
- [ ] **Anon rate limit 재검토** — 현재 5 rpm/IP는 LLM 사용에 부족 (Security Lane #2). 옵션: (a) /v1/geo만 별도 한도, (b) 전체 anon 한도 상향, (c) auth 강제. 트래픽 데이터 후 결정
- [ ] **Integration test harness** — PostgreSQL + pg_trgm 실제 SQL 동작 검증 `//go:build integration` (Adversarial #9). docker-compose 권고
- [ ] **Fuzzy threshold 튜닝** — 0.7은 한국어 짧은 토큰("강남" → "강남역" similarity ≈ 0.67)에서 미달. corpus benchmark 결과로 0.45~0.5로 조정 검토 (Adversarial #5)
- [ ] **Curated alias 좌표 검증** — `잠실` 등 round-numbered 좌표는 placeholder 가능성. corpus benchmark에서 ±200m 게이트로 사후 검증 (Adversarial #6)
- [ ] **alias_overrides 우선순위 메타데이터** — `disabled BOOLEAN` / `defeat_exact BOOLEAN` 등 운영 중 큐레이터 실수 보호 컬럼 (Adversarial #6)
- [ ] **cron 실패 알림 채널** (Slack/email) — 30일 stale 운영자 무인지 방지
- [ ] **6개월 정확도 측정 KPI 대시보드** — Steelman 잔여 우려 대응
- [ ] **PR-2 EPSG 라이브러리 PoC 스파이크** — `go-proj` 등 후보 1개 확정 (PR-2 첫 태스크)
- [ ] **down.sql 위험성 강화** — `migrate down 003`이 운영 데이터 즉시 삭제. maintenance.md 경고 강화 (Security #6)

### PR-2 ← 현재 (build target — 8 태스크 TDD 사이클)

> 의사결정 4건 (geo-address-coords.md §15):
> - **D1**: EPSG:5179 → WGS84 = pure-Go LCC + datum-shift 무시 (CGO 0)
> - **D2**: 데이터 소스 = 행안부 도로명주소 전체 DB txt
> - **D3**: cron 주기 = 매월 5일 KST 03:00 (월간)
> - **D4**: 부분 주소 = 422 + format hint (보수적)

- [ ] **T1: migration 005 — addresses 테이블** —
      RED: `migrations/005_create_addresses.up.sql` + `down.sql`. `make migrate` → addresses 테이블 + 인덱스 생성. `SELECT 1 FROM addresses LIMIT 0` 통합 테스트.
      GREEN: 스키마 (`pnu UNIQUE`, `road_address_normalized`, `region_sido/sigungu`). gin_trgm_ops on normalized + building. region (sido, sigungu) 복합 인덱스.

- [ ] **T2: internal/geo/epsg5179.go — LCC inverse** ⏸️ 보류 — 행안부 "제공하는 주소 (도형, 좌표)" 자료 도착 후 재개. **이유**: tmp/ 사물주소.zip 의 좌표 (광양 X=224711, 강릉 X=73807) 가 EPSG:5179 X 범위 (80K~1.4M) 와 불일치 → plan v9 의 EPSG:5179 가정 자체 검증 필요. 별도 신청 자료의 좌표계 (EPSG:5179 / 5181 / 5186) 결정 후 LCC 파라미터 확정.
      RED: `internal/geo/epsg5179_test.go` 6 case (서울/부산/대구/인천/제주/대전 시청 알려진 좌표 → WGS84, ±5m 게이트). bbox 외 → ErrOutOfKorea.
      GREEN: LCC inverse (EPSG:5179 가정 파라미터: lat_0=38, lon_0=127.5, lat_1=30, lat_2=60, x_0=1000000, y_0=2000000, GRS80 a=6378137 b=6356752.3141). 좌표계 확정 후 파라미터 교체.

- [ ] **T3: internal/model/address.go (5 함수 + integration test)** —
      RED: `address_integration_test.go` (`//go:build integration`) — FindByRoadExact / FindByRoadFuzzy / FindByBuilding / FindByPNU / Upsert. fixture INSERT + truncate isolation.
      GREEN: pgx raw SQL 5 함수. ORDER BY similarity DESC, region_sido ASC, id ASC. road_address_normalized = NFC + 시도 약어 통일 (서울특별시 ↔ 서울).

- [ ] **T4: cmd/seed-juso — 행안부 txt parser + EPSG + COPY FROM** —
      RED: `cmd/seed-juso/main_test.go` mini fixture (10 row pipe-delimited txt) → addresses INSERT 정확 검증. EPSG 변환 후 좌표 ±5m.
      GREEN: 시도별 분할 입력 (17 파일), per-시도 transactional swap, 청크 단위 COPY FROM 10k row, 체크포인트 `.juso_import_state.json`. NULLIF 빈 문자열 → NULL.

- [ ] **T5: internal/proxy/places.go 확장 — addresses fallthrough** —
      RED: golden case 추가 — "서울 강남구 테헤란로 152" → 200 (source="juso") / "테헤란로 152" → 422 (부분 주소) / "역삼동 825-22" → 200 (지번).
      GREEN: `isAddressLikely(q)` 패턴 (시도 토큰 + 도로명/번지 정규식). chain 5단계로 추가 (alias_override → exact → alias → fuzzy → addresses → 422).

- [ ] **T6: docs/maintenance.md PR-2 갱신** —
      juso.go.kr 회원가입 + 다운로드 절차 (24h URL 만료 명시) + 매월 5일 KST 03:00 운영자 수동 다운로드 + `make seed-juso` 실행 + 실패 rollback 절차.

- [ ] **T7: testdata 확장 + benchmark 갱신** —
      RED: `testdata/korean_corpus.json` 100 → 130건 (도로명 20 + 지번 10). `cmd/benchmark-resolve` 측정.
      GREEN: corpus 작성 + **precision ≥ 0.85 게이트 유지**. 미달 시 alias 보강 또는 normalize 패턴 추가.

- [ ] **T8: 라우트 확장 + Makefile + atomic commit** —
      RED: `cmd/server/main.go` integration test ("서울 강남구 테헤란로 152" → 200 + 좌표). `make build/lint/test` pass.
      GREEN: Makefile `seed-juso` 타겟 1줄 + Conventional Commits — `feat(geo): 행안부 도로명주소 (EPSG:5179 pure-Go 변환)`. **사용자 명시 허락 후** atomic commit.

**Operational Checklist** (PR-2 머지 후):
- [ ] juso.go.kr 회원가입 + 다운로드 권한 신청
- [ ] 첫 다운로드 + `make seed-juso` (~30분-1시간, ~1천만 row)
- [ ] 백업 사이즈 측정 (addresses 인덱스 포함 ~3-5GB)
- [ ] cron 등록 (매월 5일 KST 03:00)
- [ ] production smoke: `curl '/v1/geo/resolve?q=서울 강남구 테헤란로 152'` → 200

## Plan 7: Almanac (KASI) — Phase A ✅ (`09fa12b` + `1c509c6` push to main)

> Spec: `.claude/plans/almanac-kasi-phase-a.md` (v3, T0 검증 + 3-reviewer 다관점 검증 통과)
> 상위 로드맵: `~/.claude/plans/majestic-percolating-cray.md`
> Goal: `/v1/almanac/lunar-date` (양→음) + `/v1/almanac/solar-date` (음→양) + `/v1/almanac/sun` (좌표/지역)
> Reuse: `holiday.go` 패턴 미러 (단 `_type=json`, `serviceName` 동적). `kma.ErrOutOfKoreaPeninsula` 가드 재사용
> Atomic single commit (사용자 명시 허락 후)

- [x] **T1: AlmanacHandler scaffold + LunarDate (양→음)** — 7 sub-test (plan v3 6 + `_type=json` 검증 1) all pass.
- [x] **T2: SolarDate (음→양) + stale/502 대칭 보강** — 7 sub-test (윤달 passthrough 포함) all pass.
- [x] **T3+T4: Sun (좌표/지역 통합) + 한반도 가드 (D9)** — 9 sub-test (OutOfPeninsula + DnYnSilentlyDropped + InvalidCoords 포함) all pass. `/sun` 단일 endpoint, `latitude+longitude` vs `location` 분기.
- [x] **T5: 라우트 등록 + router-level rate limit test** — `TestAlmanacRouteWiredWithRateLimit` (anon 5+1=429) pass. main.go 에 `/v1/almanac/{lunar-date,solar-date,sun}` 3 라우트 등록.
- [x] **T6: Integration test + build/lint/test** — `TestAlmanac_LiveKASI` 3 골든 케이스 pass (양력 2026-05-01 ↔ 음력 2026-03-15 평달 / 서울 sunrise=0537 sunset=1922 / round-trip). `make build / make lint (0 issues) / make test` 모두 pass.
- [x] **T7: Conventional Commit** — `09fa12b feat(almanac): 음력 변환 + 일출/일몰 (KASI)` push to main. Smoke test 7/7 통과 (port 28080).

**Operational Checklist**:
- [x] data.go.kr 활용 신청 (LrsrCldInfoService + RiseSetInfoService) — 2026-05-01 자동 승인 완료
- [ ] **L4 — kittypaw 스킬 패키지 측 통합 (별도 PR, 별도 레포 `../skills/packages/`)** — Plan 5 T6 선례. 본 PR 머지 ≠ 사용자 도달. 본 PR 끝난 후 별도 진행.
- [ ] **Phase C 키 신청 발의** (서울교통공사 OpenAPI) — 1~3일 리드타임. 본 plan 진행과 병렬 발의 권장 (상위 로드맵 명시 결정).
- [ ] (P1 follow-up) D10 — 입력범위(1391~2050) 검증 — 별도 issue.
- [ ] (P1 follow-up) **D4 — KASI helper 통합 refactor** — Phase B (KMA UV) 추가 시점에 holiday/almanac/weather/UV 4개 ServiceName 11 endpoint 를 한 번에 통합. plan v2 박제: `.claude/plans/d4-kasi-helper-refactor.md`. 3 reviewer (Architect/Critic/CEO) Phase 2 ITERATE — 옵션 3 (UV 동시 통합) 채택. **재개 트리거**: Phase B UV endpoint production 추가 시점.
- [x] **holiday.go envelope 검증** — `parseKMAError` 재사용으로 `resultCode != "00"` 응답이 24h 캐시되지 않도록 fix. `fetch()` 의 200 OK 분기에서 검증 → fetch error → stale fallback → 502.

## Plan 8: Smoke 3-Layer L1.A — Holiday Integration Test ✅ (`3c28f6a` push to main)

> Spec: `.claude/plans/smoke-3-layer.md` (v2, Architect/Critic 14 finding ITERATE 후 재작성. CEO 메타 비판 dispatch — 사용자 명시 결정)
> Goal: `internal/proxy/holiday_integration_test.go` 신규 + `Makefile` 분리 (DB 의존 vs API 의존 build tag split)
> Reuse: `almanac_integration_test.go` 패턴 미러 (in-process httptest + `HOLIDAY_API_KEY` env + `t.Skipf` if missing)
> 직접 동기: `3688453 fix(holiday): _type=json` (prod ~4-day 502 회귀 — 외부 KASI 실 동작 grounded 검증 layer 부재)
> 결정 D1~D7: plan v2 §"핵심 결정 7개" 모두 사용자 합의 박제. D4 = (A) bash+curl (v1 (C) 에서 다운그레이드 — Architect F1 critical)

**TDD 변형**: production code 무변경. strict RED→GREEN 사이클 N/A. **RED** = test 자체 fail (env 미설정 ∨ envelope mismatch ∨ 골든 불일치), **GREEN** = `.env` KEY 주입 후 envelope OK + 골든 일치.

**L1.A 수락 기준 (AC1~AC5)**:
- **AC1**: HTTP 200 (요청 자체 fail 시 `t.Fatalf`)
- **AC2**: `parseKMAError(body)` → `isError == false` (envelope `response.header.resultCode == "00"`)
- **AC3**: JSON unmarshal 성공 + `body.items.item` 길이 ≥ 1 (NO_DATA 와 구분)
- **AC4**: (holidays 만, 골든) `2025-01-01` `dateName` = `1월1일` 또는 `신정`
- **AC5**: `HOLIDAY_API_KEY` 부재 시 `t.Skipf("HOLIDAY_API_KEY not set")` (기존 weather/almanac 패턴 동일)

**Retry 정책** (D2):
- upstream 502/timeout → 1회 재시도 (15s timeout, 1s backoff). 두 번째도 502 → `t.Fatalf` (실 회귀)
- envelope `resultCode=22/99/SERVICETIME_OUT` (limit hit) → `t.Skipf("daily limit reached")` + CI annotation
- envelope `resultCode=03` (NO_DATA) → endpoint-specific. holidays = `t.Fatalf`, anniversaries = `t.Skipf` 허용

- [x] **T1: `internal/proxy/holiday_integration_test.go` 신규 (3 sub-test + AC1~AC5 + 골든)** — Holidays/Anniversaries/SolarTerms 3 sub-test all PASS. `fmt.Sprint(float64)` 지수 표기 micro-bug RED 발견 → `%.0f` 수정. retry closure + rate-limit Skipf 분기.

- [x] **T2: `Makefile` 분리 + build tag 격리** — 신규 `test-integration-calendar` target + `test-integration-all` umbrella alias. plan v2 D1 1단계 충실 — 기존 `integration` 태그는 model+weather+almanac 통합 유지 (L1.B/C/D 시점 분리). `make build / make lint (0 issues)` 회귀 0.

**Operational Checklist** (L1.A 머지 후):
- [ ] **L2 plan trigger by 2026-05-16 (D7 SLA, L1.A=`3c28f6a` 2026-05-02 머지 + 14일)** — `ina:plan` 으로 L2 (CI integration job, GitHub Actions secrets + fork PR silent-green 차단) plan 작성. 미이행 시 L1.A 가치 ≈ 0 (로컬 한정 검증).
- [x] **L3 prod smoke ✅ (Plan 10)** — `deploy/smoke.sh` 신규. **26/26 endpoint 100% cover** (Plan 10 확장 후). `make smoke` / `fab smoke` / `fab deploy` 종결부 자동 호출. 두 routing 회귀 sequential catch (Air `/v1/air/airkorea/...`, OAuth `/auth/*` 두 prefix 가정 실패) — integration test 가 못 잡는 layer 증명.
- [ ] **T0 spike** — `data.go.kr` 5 service key 별 daily limit 확인 (HOLIDAY/WEATHER/AIRKOREA + KASI 음력/일출). 결과를 plan v2 §D3 표에 record. L2 plan prerequisite.
- [x] **L1.B (airkorea 5 endpoint) + L1.C (weather UltraShort 2)** — Plan 9 ✅ (이번 세션). 외부 API 의존 endpoint cover 100%.
- [ ] **L1.D (geo HTTP layer)** ⏸️ 별도 plan — DB+API hybrid 재설계 필요. plan v2 §D6 박제. 행안부 좌표 + PR-2 T2/T3 머지 후 trigger.
- [ ] **dual-mode test harness** (L3 의 in-process httptest + HTTP client BASE_URL 분기) — L3 sibling plan 시점에 재검토 (현재 비범위).

## Plan 9: Smoke 3-Layer L1.B + L1.C — AirKorea + Weather UltraShort ✅

> Spec: Plan 8 (`smoke-3-layer.md` v2) sibling. ina:plan 생략 + 직접 ina:build (CEO 비판 학습 — template 정착됨).
> Reuse: Plan 8 L1.A `holiday_integration_test.go` 패턴 미러
> 외부 API 의존 endpoint cover 율: 50% (7/14) → **100% (14/14)**

- [x] **L1.B: `internal/proxy/airkorea_integration_test.go` 신규** — 5 sub-test (RealtimeByCity/RealtimeByStation/Forecast/WeeklyForecast/UnhealthyStations) all PASS. AirKorea 도 `returnType=json` 사용 (holiday 와 동일 quirk class) 이지만 현재 정상 수신 — silent XML fallback 회귀 catch mechanism 박제. build tag `air_integration` + `make test-integration-air` target.

- [x] **L1.C: `internal/proxy/weather_integration_test.go` 확장** — `TestUltraShort_LiveKMA` 함수 추가 (Nowcast + Forecast 2 sub-test) all PASS. 기존 `TestVillageForecast_LiveKMA` 유지 (build tag `integration` Plan 8 D1 phased 충실).

- [x] **Makefile umbrella** — `test-integration-all` = `test-integration` + `test-integration-calendar` + `test-integration-air`.

**Operational Checklist (L1.B/C 머지 후)**:
- [ ] **L1.D (Geo HTTP layer)** ⏸️ 별도 plan — Plan 8 v2 D6 ⚠ DB+API hybrid. 행안부 좌표 자료 도착 + PR-2 T2/T3 머지 후 trigger.
- [ ] **OAuth integration (10 endpoint)** ⏸️ 별도 plan — browser flow (Playwright/headless). 사용자 영향 큼.
- [x] **/health + /discovery** ✅ L3 smoke (Plan 10) cover.

## Plan 10: L3 Prod Smoke — `deploy/smoke.sh` ✅

> Spec: Plan 8 v2 §D4(A) bash + curl + jq 박제대로
> Goal: prod URL (`api.kittypaw.app`) 대상 17 endpoint 자동 smoke
> 직접 동기: integration test (in-process httptest) 가 routing/배포 회귀 못 catch — Plan 9 까지의 100% integration cover 도 prefix 잘못은 무방비

- [x] **`deploy/smoke.sh` 신규 (확장 후 26 endpoint)** — /health + /discovery (2) + Calendar (3) + Almanac (3) + Weather (3) + Air (5) + Geo (1) + OAuth endpoint-level (9) = 26. HTTP 200 + envelope resultCode=00 (KASI/KMA/AirKorea) + lat/lon/name_matched (geo) + status code (OAuth: 302/400/401). anon 5rpm/IP rate-limit auto-retry (429 → 61s wait → retry once).

- [x] **Makefile `smoke` target** — `make smoke` 단독 호출.

- [x] **fabfile.py 통합** — `fab deploy` 종결부 자동 smoke + 별도 `fab smoke` task.

- [x] **검증** — 26/26 PASS against api.kittypaw.app (확장 후). 두 routing 회귀 sequential catch — Air prefix (`/v1/air/airkorea/...`), OAuth prefix (`/auth/*` not `/v1/auth/*`). 둘 다 integration test (in-process httptest) 가 못 잡은 layer.

**비범위**:
- **OAuth full browser flow** (Playwright/headless, login 성공까지 검증) — 별도 plan. 현재 endpoint-level liveness 만 cover.
- prod 자동 smoke 의 CI 통합 — L2 plan (D7 SLA 2026-05-16) 영역
- Cloudflare 우회 / 운영자 IP 화이트리스트 — 운영 시 검토

## Plan 11: B0 — testfixture-only (γ option) ✅

> Spec: `.claude/plans/test-coverage-completion.md` (γ compromise, 사용자 결정 2026-05-02)
> Goal: `internal/auth/testfixture/` 신규 sub-package — `IssueTestJWT` + `SeedTestUser`. Plan 12·13·14 가 모두 의존하는 helper 분리 PR.
> Critic 권고 기반 — 머지 후 12·13·14 병렬 가능. 본 plan 자체는 production 코드 변경 0.

- [x] **T1: package skeleton + RED** — `fixture.go` stub + `fixture_test.go` (3 case: round-trip + DefaultTTL + CustomTTL). 3개 모두 fail 확인.

- [x] **T2: GREEN — `IssueTestJWT`** — `auth.Sign` 재사용. ttl=0 시 `15*time.Minute` 기본값. 3 case pass. **시그니처 정정** (plan 박제 vs 실 코드): `secret string`, `userID string` (실 `auth.Sign` + `User.ID` 가 string).

- [x] **T3: GREEN — `SeedTestUser`** — `store.CreateOrUpdate` 호출 (실 시그니처). `fixture_pg_integration_test.go` (`//go:build integration`) — `DATABASE_URL` skip + LiveDB seed + Idempotent (UnixNano provider_id 로 collision X). teardown 은 `UserStore.Delete` 부재로 omit, `doc.go` 에 명시.

- [x] **T4: doc + commit gate** — `doc.go` package doc 작성. `make build` ✓ / `make lint` 0 issues / `make test` PASS. 사용자 허락 후 commit (이 commit).

## Plan 12: A — L1.D + L3 geo 보강 ✅

> **Spec**: `.claude/plans/plan-12-l1d-l3-geo.md`. Phase 1 Architect/Critic + Phase 2 종합 ITERATE 모두 반영.

- [x] **T1**: scaffold `internal/proxy/places_integration_test.go` (`//go:build integration`) + `setupGeoIntegration(t)` (DATABASE_URL skip + pgxpool + `pg_advisory_lock(12)` + prefix-DELETE teardown) + `seedPlace`/`seedAliasOverride` helper + `TestResolve_Integration_Exact` PASS.
- [x] **T2**: 3 case — `AliasOverridePriority` (places vs alias_overrides 다른 좌표 → response.source=`kittypaw_alias`/type=`alias_override`) + `FuzzyFallback` (q=`_p12_fuzzy_강남구청` → seed `_p12_fuzzy_강남구청역` trgm match) + `TypeHintSubwayWins` (동일 name_ko 두 row, 역 suffix → subway_station 우선). 모두 PASS.
- [x] **T3**: 3 negative — `OutOfKorea` (q=`_p12_oof_unmappable_*` → 422 `unsupported_input`. *cross-package row 와 fuzzy 충돌 회피 위해 prefix 박제 unmappable query 사용* — Tokyo 같은 ASCII short token 은 model 패키지 fixture 와 trgm match 가능) + `MissingQ` (400) + `InputTooLong` (201자 → 414).
- [x] **T4**: `check_geo` 3rd arg `expected_status_class` default `200`, `4xx` regex 박제. 4 case (강남역/서울대입구역/강남/Tokyo `4xx`). 기존 호출 BC 보존. **`make smoke` 29/29 PASS** (26 → 29, 3 추가).
- [x] **T5**: TASKS.md ✅ + Plan 13 promote. commit gate.

**검증**: `make test-integration` 전 패키지 PASS (회귀 0) / `make build` ✓ / `make lint` 0 issues / `make smoke` 29/29 PASS. production 코드 변경 0.

## Plan 13: B1 — auth /me + refresh rotation + contract revision ✅

> **Spec**: `.claude/plans/plan-13-auth-me-refresh-contract-revision.md` (β 묶음, Phase 1 + Phase 2 ITERATE 모두 반영)
> **사용자 결정 2026-05-02**: D1 issuer = `"https://api.kittypaw.app/auth"` (path-based) / D2 audience = `["https://api.kittypaw.app", "https://chat.kittypaw.app"]` (URL form). Superseded by portal split: issuer = `"https://portal.kittypaw.app/auth"`.

- [x] **T1**: contract revision. `scopes.go` const 정정 (`Issuer`/`AudienceAPI`/`AudienceChat` URL form, 기존 `IssuerKittyAPI` 등 이름 변경) + `jwt.go` 참조 정정 + `main.go` discovery `auth_base_url` derive (`strings.TrimRight(cfg.BaseURL, "/") + "/auth"`, R6 trailing slash) + `main_test.go` `TestDiscoveryReturnsAuthBaseURL` 신규 + `google_test.go` wire-format URL form + `jwt_test.go` 의 Plan 17 박제 갱신 + `docs/specs/kittychat-credential-foundation.md` D2 + 새 D8.
- [x] **T2**: `internal/auth/me_integration_test.go` 신규. `setupAuthIntegration(t)` Plan 12 패턴 (`_test` guard + pgxpool + middleware-wrapped httptest, advisory_lock 불필요). Plan 11 testfixture 활용. 3 case (NoToken 401 / ValidJWT 200 + body / ExpiredJWT 401) PASS.
- [x] **T3**: `internal/auth/refresh_rotation_integration_test.go` 신규. `setupRefreshIntegration(t)` (UserStore + RefreshTokenStore). 2 case PASS — Happy (rotation + 이전 revoked DB 검증) + ReuseDetect (DB query `activeRefreshCount == 0` 검증, Critic ITERATE C2).
- [x] **T4**: `deploy/smoke.sh` `check_discovery_keys` 4-key → 5-key (`auth_base_url` 추가).
- [x] **T5**: TASKS.md ✅ + Plan 14 promote. 2 commit (contract revision + integration test) + push + fab deploy + smoke 검증 + cross-team 알림.

**검증**: `make test-integration` 전 패키지 PASS (회귀 0 + 새 5 case PASS) / `make build` / `make lint` (0 issues) / `make smoke` 회귀 0 + auth_base_url 추가.

**BC**: prod deploy 직후 active old token (iss=`kittyapi`) → 다음 호출 401 → client refresh 자동 → 새 shape. AccessTokenTTL=15min. 1인 + private 환경 영향 ≈ 0.

- [ ] kickoff 시 ina:plan trigger

## Plan 14: C′ — L1.F cross-cutting 축소판 (deferred)

> Spec: `.claude/plans/test-coverage-completion.md` Plan C′ 섹션
> kickoff: B0 머지 직후 별도 `ina:plan` 호출.
> 핵심: cache_stale Warning:110 (airkorea 1 case) + ratelimit (anon 5rpm 429 + auth 60rpm 패스 + RealIP/XFF 격리 4 case). fakeClock window reset 은 deferred.

- [ ] kickoff 시 ina:plan trigger

## Plan 15: B2 — OAuth e2e mock provider (deferred)

> γ deferred — Google/GitHub OAuth provider spec drift 빈도 낮음 + mock provider 영구 부채 회피.
> 재개 트리거: prod OAuth 회귀 발생 또는 다중 provider 추가 시.

## Plan 16: D — L2 staging (deferred + 14d SLA 폐기)

> γ deferred — 1인 메인테이너 + private 환경에서 라우터 wiring 회귀 발생률 ≈ 0.
> `smoke-3-layer.md` 14d SLA 박제 명시 폐기 (CEO ITERATE 채택, sunk cost fallacy).
> 재개 트리거: 라우터 wiring 회귀 발생 또는 외부 운영 인력 추가 시.

## Plan 17: kittychat credential foundation ✅

> Spec: `docs/specs/kittychat-credential-foundation.md` (cross-team contract — track 필요. multi-aud + claims schema + scope vocab + version policy 박제, 사용자 결정 2026-05-02)
> 외부 의존: kittychat 측 implementer unblock. 그쪽이 우리 spec 위에서 `CredentialVerifier`/`APIClientClaims`/`DeviceClaims` 정의 후 env-seeded verifier 진행.
> 본 commit 은 **spec only** — 실 구현 (T1~T5) 은 다음 slice (별도 `ina:plan` kickoff).

- [x] **T1**: `Claims` struct 확장 — `Scope []string` + `V int` (Aud 는 RegisteredClaims.Audience 재사용 = RFC 7519 standard). RED 2 case (`TestSignForAudiences_RoundTrip` + `TestVerify_LegacyTokenWithoutAudOrScope` BC) fail 확인.
- [x] **T2**: `auth.SignForAudiences(userID, audiences, scopes, secret, ttl)` helper. 기존 `Sign` 은 thin wrapper (BC + DRY). `v=1` 박제 (audiences/scopes 둘 다 빈 경우만 v=0 — legacy path). 6 case PASS.
- [x] **T3**: `cli.go:27` 의 `issueTokenPair` 한 줄 변경 — Google/GitHub/Refresh/CLI 모두 *single choke point* 경유라 한 줄로 모든 발급 path cover. `SignForAudiences(user.ID, DefaultAPIClientAudiences, DefaultAPIClientScopes, ...)`.
- [x] **T4**: `internal/auth/scopes.go` 신규 — `ScopeChatRelay/ModelsRead/DaemonConnect`, `AudienceKittyAPI/KittyChat`, `ClaimsVersion=1`, `DefaultAPIClientScopes`, `DefaultAPIClientAudiences`.
- [x] **T5**: README.md / README.ko.md 의 JWT 항목에 spec link 박제.
- [x] **T6**: wire-format guard (post-merge follow-up). RFC 7519 sub/iss 정정 (`abf8c16`) + uid reject test (`c8435e2`) 후 추가 박제. `internal/auth/google_test.go`의 `TestGoogleCallbackSuccess` 끝에 access_token decode + sub/iss/aud/scope/v=1 + uid 키 부재 assertion 추가. 회귀 시뮬레이션 (cli.go:27 SignForAudiences→Sign) 결과 fail message *"wire-format regression in issueTokenPair: v = <nil>, want 1"* 정확 catch 검증. `deploy/check-token-shape.sh` 신설 — 사용자 manual decode script (paste-and-verify).

**다음 slice (Plan 17 머지 후, 별도 plan)**:
- device schema migration (users → devices 1:N)
- device credential 발급 endpoint (`POST /auth/devices/pair`, `POST /auth/devices/{id}/credential`)
- opaque API key + introspection endpoint
- JWKS public endpoint + RS256 마이그레이션
- pairing flow (registration code)

## Plan 18: 코드 리뷰 — Phase 1 보안 즉시 처치 ✅

> Spec: `~/.claude/plans/silly-wiggling-balloon.md` (전체 7 phase 코드 리뷰 종합 계획, 사용자 결정 2026-05-02 ina:build 진입)
> Goal: rate-limit 헤더 우회 차단 + /auth/token/refresh body size cap. Phase 2-7은 별도 PR.

- [x] **P1-1**: `cmd/server/main.go:51`의 `chi.middleware.RealIP` 제거. `internal/ratelimit/middleware.go`의 `realIP()`가 X-Real-IP 헤더만 신뢰 (nginx canonical override) + fallback `r.RemoteAddr` host. 이유: chi RealIP는 True-Client-IP / X-Real-IP / X-Forwarded-For 순으로 신뢰하나, 표준 nginx `proxy_params`는 X-Real-IP만 override (True-Client-IP 미터치, X-Forwarded-For append) → 공격자가 헤더 회전으로 ratelimit 키 우회 가능했음.
  - 신규 router-level 테스트 3건 (cmd/server/main_test.go): TrueClientIP/XForwardedFor 우회 시도 → 6번째 429 (RED→GREEN), X-Real-IP 정상 작동 (regression guard).
  - `deploy/kittyapi.nginx`: defense-in-depth — `proxy_set_header True-Client-IP "";` + `proxy_set_header X-Forwarded-For $remote_addr;` (코드 측 잠금 + nginx 측 잠금 양쪽).
- [x] **P1-3**: `internal/auth/refresh.go`의 `HandleTokenRefresh()`에 `MaxBytesReader(maxAuthBodyBytes=1024)` 추가. `/auth/cli/exchange` (cli.go:194)와 공유 const 추출 (`internal/auth/handler.go`).
  - 신규 테스트 1건 (refresh_test.go): 10 KiB body → 400 (`MaxBytesReader` reject), NOT 401 (FindByHash miss).
- [x] 리뷰: Lane B (Security OWASP) + Lane C (Simplify) 병렬. 적용된 fix-first — 주석 DRY (main.go ↔ middleware.go 중복 압축), `1024` 상수 추출 (`maxAuthBodyBytes`).

**검증**: `make build` ✓ / `make lint` 0 issues / `make test` 전 패키지 PASS (회귀 0).

**Operational Checklist** (이 PR 머지 후):
- [ ] **fab deploy** — nginx 변경 반영. 미배포 상태에선 nginx defense-in-depth 미적용 (코드 측 잠금만 활성).
- [ ] **`make smoke`** — 26/26 회귀 0 확인.
- [ ] **수동 검증** — `curl -H 'True-Client-IP: 1.2.3.4' https://api.kittypaw.app/v1/almanac/lunar-date?solYear=2026&solMonth=05&solDay=01` 6회 → 6번째 429.

**Follow-up (silly-wiggling-balloon Phase 2-7)**:
- [ ] **Phase 2** (graceful shutdown + slog 도입) — 별도 ina:build kickoff
- [ ] **Phase 3** (JWT claims에 email/name 박제 → middleware DB 조회 제거)
- [ ] **Phase 4** (인메모리 store 통합: cache + state + cli_code → generic ttl.Store)
- [ ] **Phase 5** (5개 proxy handler 통합 — TASKS.md D4 follow-up과 정합, KMA + AirKorea까지 확장)
- [ ] **Phase 6** (OAuth Provider interface 추출 — google + github 통합)
- [ ] **Phase 7** (운영 강화: JWT secret rotation, cache/ratelimit max-entries, CLI exchange 401 burst cool-down, seed-wikidata 분할)
- [ ] **마이크로 follow-up**: IPv6 형태 r.RemoteAddr fallback 보강 (`net.ParseIP` + bracket strip) — 운영 영향 극미하나 깔끔함
- [ ] **테스트 인프라 flake**: `make test-integration` 이 패키지 병렬 실행 (`go test -p`)으로 인해 model 패키지 마이그레이션과 proxy/places_integration_test 가 race. `setupGeoIntegration` 이 마이그레이션을 적용하지 않고 model 테스트가 적용한 결과에 의존. 임시 회피: `-p 1` 또는 model 먼저 실행. 본질 fix: `setupGeoIntegration` 자체 마이그레이션 적용 또는 packages 간 dependency 명시.

## Plan 19: 코드 리뷰 — Phase 2 graceful shutdown + slog ✅

> Spec: `~/.claude/plans/silly-wiggling-balloon.md` Phase 2.
> Goal: SIGINT/SIGTERM 시 inflight 요청 drain + 4 store sweep goroutine 정리. slog 도입 (init/shutdown 한정).

- [x] **P2-4**: `main()` → `run() error` 분리. `signal.NotifyContext(ctx, SIGINT, SIGTERM)` + `srv.Shutdown(grace=30s)` + `defer cleanup()`. `http.Server` 도입 — `ReadHeaderTimeout=10s` (slowloris), `WriteTimeout=30s` (slow reader), `IdleTimeout=120s` (keep-alive 좀비). `NewRouter` signature `(*chi.Mux, func())` — cleanup이 cache + state + cli_code + limiter 4개 store Close 호출. 4 store에 `sync.Once` 적용 (멱등성, 두 번 호출 시 panic 방지).
- [x] **P2-5(시작)**: `initLogging()` — JSON handler to stderr, `LOG_LEVEL` env (debug/info/warn/error). unknown 값은 `slog.Warn` + info fallback (silent 방지). main lifecycle만 slog (listening / shutdown signal / exited). 기존 handler 내부 `log.Printf`는 점진 follow-up.
- [x] 리뷰 적용 (5건): WriteTimeout/IdleTimeout, LOG_LEVEL 검증, 변수명 `stop`→`stopSignals`, defer 순서 주석, sync.Once. 임계 미달 또는 plan 외 3건 (slog secret filter, log.Printf 혼재, shutdown race edge) → 별도 follow-up.

**검증**: `make build` ✓ / `make lint` 0 issues / `make test` + `test-integration -p 1` 전 패키지 PASS.

**테스트**: `TestNewRouter_CleanupReleasesStores` 신규 — cleanup 두 번 호출 시 panic 없음 (sync.Once 회귀 가드). `testRouter` 시그니처 `(t *testing.T) http.Handler`로 변경, 모든 호출자에 `t.Cleanup` cascade.

**Operational Checklist**:
- [ ] **`fab deploy`** — 새 timeout 설정 반영. systemd unit에 `LOG_LEVEL` 환경변수 추가 (옵션, 기본 info).
- [ ] **`make smoke`** — 26/26 회귀 0 확인.
- [ ] **수동 SIGTERM 검증**: `systemctl reload kittyapi` 또는 `kill -TERM <pid>` 후 journal에서 "shutdown signal received" + "server exited" 로그 확인.

**Follow-up (별도 PR)**:
- [ ] **handler 내부 `log.Printf` → slog 일괄 마이그레이션** — `internal/auth/{refresh,google,github,cli}.go`, `internal/proxy/*.go` 등 ~16곳. structured fields + level 분류.
- [ ] **slog secret redaction** — custom `slog.Handler`로 `password`/`token`/`secret` 키 자동 마스킹. Lane B HIGH/0.90.
- [ ] **shutdown race edge** — `signal.NotifyContext` cancel이 goroutine 시작 전 도착 시 select가 ctx.Done()으로 빠짐. 현재 코드 정상 작동하지만 명시적 ordering 보강 가치. Lane B Medium/0.70.

## Plan 20: RS256/JWKS + device credential — PR-A (keys + JWKS infra) ✅ (`c75c238`)

> Spec: `~/.claude/plans/silly-wiggling-balloon.md` (전체 4 PR — A/B/C/D — 중 첫 PR)
> Goal: RSA key 인프라 + JWKS endpoint 노출. 회귀 0 (발급은 여전히 HS256). 채팅팀 verifier 통합 테스트 unblock.
> 후속: PR-A 머지 후 별도 ina:plan kickoff — PR-B (RS256 cutover) → PR-C (devices DB) → PR-D (endpoints).
> Cross-team: 채팅팀 + daemon팀 spec 합의 완료. 회신 보낸 message 박제는 plan §결정된 spec 참고.

- [x] **T1: `internal/auth/keystore.go` — RSA load + JWK Set + RFC 7638 thumbprint + JWKSProvider**
      RED: `keystore_test.go` 단위 4건 — (a) RFC 7638 §3.1 example key의 thumbprint 알려진 값 매칭, (b) JWK modulus N의 leading-zero 패딩 검증 (modulus byte 길이 = `(BitLen+7)/8`), (c) 잘못된 PEM bytes → error, (d) `JWKSProvider.Lookup(kid)` known kid → public key, unknown kid → error.
      GREEN: `LoadPrivateKeyPEM([]byte) (*rsa.PrivateKey, string, error)` (key + kid), `BuildJWKSet(*rsa.PublicKey, kid string) JWKSet` (n/e/kty/alg/use/kid 박제), `Thumbprint(JWK) string` (RFC 7638 canonical JSON SHA-256 base64url), `JWKSProvider` interface + `singleKeyProvider` 구현. stdlib only (crypto/rsa, encoding/base64, encoding/json, math/big, crypto/sha256).

- [x] **T2: `internal/auth/jwks.go` — HandleJWKS handler**
      RED: `jwks_test.go` 단위 2건 — (a) GET 응답 200 + Content-Type=application/json + body가 `{"keys":[{...}]}` shape, (b) `Cache-Control: public, max-age=600` header 박제.
      GREEN: `HandleJWKS(provider JWKSProvider) http.HandlerFunc` — provider에서 keys 추출 → JSON encode + Cache-Control set.

- [x] **T3: `internal/config/config.go` — `JWT_PRIVATE_KEY_PEM_B64` env + fail-fast**
      RED: `config_test.go` 단위 4건 — (a) 정상 base64 PEM → key load 성공 + kid 비어있지 않음, (b) base64 디코딩 실패 → error, (c) PEM parse 실패 → error, (d) RSA 비트 < 2048 → error. `JWT_SECRET`은 일단 기존 검증 유지 (PR-B에서 제거).
      GREEN: Config struct에 `JWTPrivateKey *rsa.PrivateKey`, `JWTKID string` 필드 추가. `Load()`가 env 디코딩 → `keystore.LoadPrivateKeyPEM` 호출 → 비트 검증 → fail-fast error 반환. `LoadForTest()`는 fixture key 박제 (메모리만).

- [x] **T4: `cmd/server/main.go` — JWKS 라우트 등록 + integration**
      구현됨: `cfg.JWTPrivateKey + cfg.JWTKID` 으로 `auth.NewSingleKeyProvider` 생성 → `r.Get("/.well-known/jwks.json", auth.HandleJWKS(provider))` `/health` 옆 등록. `LoadForTest()`에 sync.Once 캐시된 RSA fixture key 박제 (~50ms × 1회). `testRouter(t)`가 fixture 자동 wire. 회귀 0 — 모든 기존 main_test.go 케이스 PASS.

- [x] **T5: `internal/auth/testfixture/jwt.go` — IssueDeviceJWT helper (정적 fixture 미포함)**
      변경 사유: 정적 `.jwt` 파일 commit 비포함. RSA private key commit 시 gitleaks 운영 부채 + 정적 JWKS와 동적 키 매칭 보증 어려움. helper-only 패턴으로 단순화 — chat 측이 helper(또는 동등 코드)로 자기 verifier 테스트 fixture 동적 생성.
      구현됨: `DeviceClaims` struct + `IssueDeviceJWT(*rsa.PrivateKey, kid, DeviceClaims) (string, error)` (RS256 + kid header). 단위 테스트 `TestIssueDeviceJWT_RoundTrip` — alg=RS256, kid set, sub=`device:<id>`, user_id, aud, scope, v=2 wire에서 회복 검증. PASS.

- [x] **T6: docs/spec 갱신 (gitleaks allowlist는 정적 fixture 미포함으로 불필요)**
      구현됨: `docs/specs/kittychat-credential-foundation.md` D5 섹션 갱신 — "HS256 첫 slice → RS256 다음 slice" 박제를 "Plan 20 PR-A에서 RS256+JWKS 직진" 으로 교체. RFC 7638 thumbprint 박제, JWKS endpoint URL, Cache-Control max-age=600, key rotation contract (old key overlap 30분, 양측 알림), kittychat fail-mode (stale cache + backoff), Verify invariants (downgrade + cross-aud + leeway), JWKSProvider interface, 사용자 0명 cutover 배경 모두 박제.

**검증**: `make build` ✓ / `make lint` 0 issues / `make test` (T1~T5 단위 14건 + 통합 3건) PASS / `make test-integration -p 1` 회귀 0 / fixture key의 thumbprint = T1 expected 매칭.

**완료 신호**: 채팅팀이 `https://portal.kittypaw.app/.well-known/jwks.json` fetch + fixture private key로 동적 mock JWT 생성하여 verifier 통합 테스트 시작 가능.

**Operational Checklist** (이 PR 머지 후):
- [ ] secret manager에 prod RSA private key 등록 (test fixture key는 prod 사용 절대 금지)
- [ ] systemd EnvironmentFile에 `JWT_PRIVATE_KEY_PEM_B64` 추가
- [ ] `fab deploy` (build + upload + restart + smoke)
- [ ] `curl https://portal.kittypaw.app/.well-known/jwks.json` 200 + JSON 응답 검증
- [ ] 채팅팀에 PR-A 머지 신호 (verifier 통합 테스트 unblock)
- [ ] PR-B (RS256 cutover) ina:plan kickoff

## Plan 21: RS256/JWKS + device credential — PR-B (RS256 cutover) ✅ (`eca6e42`)

> Spec: `~/.claude/plans/silly-wiggling-balloon.md` PR-B
> Goal: user JWT 발급/검증 HS256 → RS256 cutover. ClaimsVersion v=2 bump. JWKSProvider 와이어링. 사용자 0명 윈도우 활용 (BC 부담 0).
> 직전 머지: PR-A `c75c238` (JWKS 인프라).
> 후속: PR-C (devices DB) → PR-D (endpoints) — 본 PR 머지 후 별도 ina:plan.

**검증 결과** (Plan review ITERATE 4 high concerns 박제 반영):
- fixture helper 시그니처: `IssueTestJWT(t, key, kid, userID, ttl)` — testfixture가 config import 회피 (cycle)
- `Verify(token, jwks, audience)` — caller가 audience 지정
- JWT_SECRET dead path 동시 제거 체크리스트 (silly-wiggling-balloon.md PR-B step 14 참고)
- wire-format guard에 header 검증 추가 (alg=RS256, kid 존재)

- [x] **T1: `SignForAudiences` RS256 + `ClaimsVersion=2` (단위)**
      RED: `internal/auth/jwt_test.go`의 `TestSignForAudiences_RoundTrip`을 RSA fixture로 갱신 — `cfg := config.LoadForTest(); SignForAudiences(userID, auds, scopes, cfg.JWTPrivateKey, cfg.JWTKID, ttl)`. 결과 token decode → header `alg=="RS256"`, `kid==cfg.JWTKID`, payload `v==2` 검증. 컴파일 실패 + assertion fail 확인.
      GREEN: `internal/auth/scopes.go` `ClaimsVersion = 2`. `internal/auth/jwt.go` `SignForAudiences(userID string, audiences, scopes []string, key *rsa.PrivateKey, kid string, ttl time.Duration)` — `jwt.SigningMethodRS256` + `token.Header["kid"] = kid` + `token.SignedString(key)`.

- [x] **T2: `Verify` JWKSProvider + leeway + downgrade guard (단위)**
      RED: `jwt_test.go`에 4건 신규 — (a) `TestVerify_RejectsHS256_Downgrade` (alg=HS256 위조 토큰 → 401), (b) `TestVerify_LeewayBoundary_30sPass`, (c) `TestVerify_LeewayBoundary_90sFail`, (d) `TestVerify_RejectsUnknownKID`. 기존 `TestSignVerifyRoundtrip`/`TestVerifyExpired`/`TestVerifyWrongSecret`/`TestVerifyMalformed` RSA로 갱신. `TestVerifyWrongSecret` → `TestVerify_RejectsForeignKey` (다른 키로 서명 → JWKS lookup 미스).
      GREEN: `Verify(tokenString string, jwks JWKSProvider, audience string) (*Claims, error)`. KeyFunc에서 `kid := token.Header["kid"].(string)` → `jwks.Lookup(kid)`. `jwt.WithLeeway(60*time.Second)` + `jwt.WithIssuer(Issuer)` + `jwt.WithAudience(audience)` + `jwt.WithValidMethods([]string{"RS256"})`.

- [x] **T3: `IssueTestJWT` helper RS256 + cycle 회피 (단위)**
      RED: `internal/auth/testfixture/fixture_test.go`의 3건 시그니처 변경 — `IssueTestJWT(t, cfg.JWTPrivateKey, cfg.JWTKID, "user-abc", 0)`. `auth.Verify(token, provider, auth.AudienceAPI)`. 시그니처 mismatch.
      GREEN: `internal/auth/testfixture/fixture.go` `IssueTestJWT(t *testing.T, key *rsa.PrivateKey, kid, userID string, ttl time.Duration) string` — 내부에서 `auth.SignForAudiences(...)` 호출. testfixture가 `config` import 안 함. `doc.go` 코멘트 RS256/key-injection으로 갱신.

- [x] **T4: middleware JWKS provider + audience strict + cross-aud guard (단위)**
      RED: `internal/auth/middleware_test.go` 4 호출 시그니처 변경 (`auth.Middleware(jwks, auth.AudienceAPI, userStore)`). 신규 테스트 `TestMiddleware_RejectsCrossAudienceLeak` — device JWT (aud=AudienceChat, scope=daemon:connect)를 user middleware에 던짐 → 401 기대. `testfixture.IssueDeviceJWT` 사용 (PR-A 박제).
      GREEN: `internal/auth/middleware.go` `Middleware(jwks JWKSProvider, audience string, users model.UserStore)`. 호출 `auth.Verify(parts[1], jwks, audience)`. `internal/auth/handler.go` `OAuthHandler.JWTSecret` 제거 + `JWTPrivateKey *rsa.PrivateKey, JWTKID string` 추가. `internal/auth/cli.go:27` `SignForAudiences(user.ID, ..., h.JWTPrivateKey, h.JWTKID, AccessTokenTTL)`.

- [x] **T5: handler 레벨 테스트 cascade + wire-format guard (단위)**
      RED: 4 파일 시그니처 cascade — `google_test.go:100`, `github_test.go:22`, `refresh_test.go:75`의 `JWTSecret: testSecret` → `JWTPrivateKey: cfg.JWTPrivateKey, JWTKID: cfg.JWTKID`. wire-format guard 강화 — `google_test.go:215` `v != 1` → `v != 2`. `L197` 직후 header 검증 5줄 추가:
      ```go
      hdrSeg, _ := base64.RawURLEncoding.DecodeString(parts[0])
      var hdr map[string]any
      _ = json.Unmarshal(hdrSeg, &hdr)
      if hdr["alg"] != "RS256" { t.Fatalf("wire-format regression: alg=%v", hdr["alg"]) }
      if hdr["kid"] == "" { t.Fatal("wire-format regression: missing kid") }
      ```
      GREEN: 위 cascade가 자체로 GREEN. production 변경 없음 (T1~T4에서 완료).

- [x] **T6: integration 테스트 cascade (build tag integration)**
      RED: `me_integration_test.go` (3 호출) + `refresh_rotation_integration_test.go` 시그니처 cascade — `auth.Middleware(jwksProvider, auth.AudienceAPI, store)` + `IssueTestJWT(t, key, kid, ...)` + `JWTPrivateKey/JWTKID` 두 필드.
      GREEN: 시그니처 변경. 통합 테스트 회귀 0 (`make test-integration -p 1`).

- [x] **T7: cmd/server/main 와이어링 + JWT_SECRET dead path 일괄 제거**
      RED: `cmd/server/main_test.go`의 `cfg.JWTSecret = "test-secret"` 5줄 제거 → 컴파일/lint fail (unused field).
      GREEN:
      - `cmd/server/main.go`: `auth.Middleware(jwksProvider, auth.AudienceAPI, userStore)` + oauthHandler에 `JWTPrivateKey: cfg.JWTPrivateKey, JWTKID: cfg.JWTKID`
      - `internal/config/config.go`: `JWTSecret string` 필드 + Load의 required map + len ≥ 32 check 제거
      - `internal/config/config_test.go`: `loadWithEnv` base map에서 `JWT_SECRET` 제거
      - `cmd/server/main_test.go`: `cfg.JWTSecret = "test-secret"` 5줄 제거
      - 각 _test.go의 `testSecret` 상수 (5+곳) 정리 — RSA fixture로 의미 변경 또는 제거
      - `.env.example`: `JWT_SECRET` 제거 (PR-A에서 박제된 신규 `JWT_PRIVATE_KEY_PEM_B64`만 유지)

**검증**: `make build` ✓ / `make lint` 0 issues / `make test` (T1~T5 단위 + T6 integration) PASS / `make test-integration -p 1` 회귀 0.

**완료 신호**: 기존 user 흐름 동일 (Google/GitHub/CLI/Refresh OAuth flow), 단지 발급 토큰이 RS256/v=2로 변경. middleware가 JWKSProvider로 검증. cross-audience leak 차단. downgrade attack 방어.

**Operational Checklist** (PR-B 머지 후):
- [ ] `fab deploy` — RS256 발급 prod 활성. `JWT_SECRET` env 운영 서버에서 제거 가능 (선택).
- [ ] `make smoke` 회귀 0 + Google OAuth 직접 1회 (사용자 0명이라 회귀 risk 거의 없음).
- [ ] 채팅팀에 user JWT가 RS256/v=2로 발급 시작 알림 (kittychat이 user JWT 검증 안 하므로 무영향이지만 contract 변경).

## Plan 22: RS256/JWKS + device credential — PR-C (devices DB + store layer) ✅ (`d275a86`)

> Spec: `.claude/plans/plan-22-pr-c-devices-store.md` (Architect/Critic/CEO 3관점 ITERATE 후 합의)
> Goal: `devices` 테이블 + `refresh_tokens.device_id` 컬럼 + DeviceStore/RefreshTokenStore 확장. **endpoint 미노출** — 그건 PR-D scope.
> 직전: PR-B `eca6e42` (RS256 cutover) · 후속: PR-D (endpoints + device JWT shape) — 본 PR 머지 후 별도 ina:plan.

**검증 결과** (3관점 합의):
- Architect APPROVED (7 concerns 박제 후): pgx/v5 jsonb는 `[]byte` round-trip (`pgtype.JSONB` 없음), capabilities CHECK 제약, `device_id` non-NULL DEFAULT 차단 주석, dirty-state 복구 runbook, "3rd device-only column" revisit trigger.
- Critic APPROVED (8 concerns 박제 후): T1 schema-only로 좁힘 (interface design은 T3에서 driving), setupTestDB refactor 명시, Error contracts 박제 (FindByID/Revoke ErrNotFound + revoked 정상 반환 + Revoke 멱등), capabilities CHECK rejection test, 006/007 down 비대칭 의도 박제.
- CEO APPROVED (cuts 반영 후): T6 (CASCADE characterization) 컷 — PostgreSQL 동작 테스트는 PostgreSQL 테스트. T8 (down-abort integration) 컷 — 0명 사용자 윈도우 시나리오 발생 불가. `UpdateLastUsed` interface에서 제외 — PR-D scope 밖.

- [x] **T1: 마이그레이션 006 + 007 SQL + reversibility CI 테스트**
      RED: `internal/model/migration_reversibility_test.go` 신규 — `TestMigrationReversibility_006_007` 작성. up 7 → down 2 → up 7 reversibility + dirty=false 단언. RED.
      GREEN: `migrations/006_create_devices.up.sql` (CHECK jsonb_typeof = 'object' 포함), `006_create_devices.down.sql` (DROP TABLE), `007_add_device_id_to_refresh_tokens.up.sql` (non-NULL DEFAULT 차단 주석 + partial index), `007.down.sql` (RAISE EXCEPTION abort guard). `Makefile`에 `test-migration` target 추가.

- [x] **T2: setupTestDB refactor — `*pgxpool.Pool` 반환 + DELETE FROM devices**
      RED: `internal/model/setup_test.go` 신규에서 `setupTestDB(t) *pgxpool.Pool` 정의 → `user_test.go`의 3 호출부 컴파일 실패.
      GREEN: setup_test.go로 setupTestDB + stripScheme 이동, cleanup에 devices 추가 (`refresh_tokens → devices → users` 순서). `user_test.go` 3 호출부 cascade — `pool := setupTestDB(t); store := model.NewUserStore(pool)`.

- [x] **T3: DeviceStore interface 정의 (test-driven)**
      RED: `internal/model/device_test.go` 신규 — `TestDeviceStore_CreateAndFindByID_Integration` 작성 → `model.NewDeviceStore` 미존재로 컴파일 실패.
      GREEN: `internal/model/device.go` (`Device` struct + `DeviceStore` interface 4 메서드 — UpdateLastUsed 제외). `internal/model/device_pg.go` (Create + FindByID 두 메서드만, capabilities marshalling은 `json.Marshal(map) → []byte` 패턴 — pgx/v5는 jsonb 컬럼을 []byte로 round-trip).

- [x] **T4: DeviceStore CRUD 완성 + Error contracts**
      RED: 6 테스트 신규 — FindByID NotFound + revoked 정상 반환, ListActiveForUser revoked filter, Revoke NotFound, Revoke 멱등, capabilities CHECK rejection (23514).
      GREEN: device_pg.go에 ListActiveForUser, Revoke (idempotent SQL: `UPDATE ... WHERE id=$1 AND revoked_at IS NULL` + EXISTS 사전 체크) 구현.

- [x] **T5: RefreshTokenStore 확장 — CreateForDevice + RevokeAllForDevice + 의미 분리**
      RED: `internal/model/refresh_token_test.go` 신규 (build tag integration) — 6 테스트: CreateForDevice round-trip, FK violation NotFound (23503 매핑), RevokeAllForDevice 정상/missing-noop, RevokeAllForDevice user 보존 (characterization), RevokeAllForUser 둘 다 revoke (characterization).
      GREEN: `refresh_token.go` interface에 두 메서드 + `RefreshToken` struct에 `DeviceID *string` 추가. `refresh_token_pg.go` 두 메서드 구현 + `Create` SQL 무수정 + FindByHash SELECT에 device_id Scan 추가 + 23503 → ErrNotFound 매핑.

- [x] **T6: 회귀 검증 — 기존 user 흐름**
      RED: `make test-integration -p 1` 전체 실행. 기존 `TestRefresh_Integration_HappyRotation` 등이 GREEN 유지 확인.
      GREEN: 회귀 0. RefreshToken struct DeviceID 필드 추가가 기존 user refresh path 영향 없음 단언.

**검증**: `make build` ✓ / `make lint` 0 issues / `make test` PASS / `make test-integration -p 1` PASS / `make test-migration` PASS.

**완료 신호**: schema + store layer ready. PR-D unblock — `/auth/devices/{pair, refresh, list, delete}` endpoints + device JWT shape 작업 시작 가능.

**Operational Checklist** (PR-C 머지 후): ✅ 모두 완료
- [x] prod DB `migrate up 7` 실행 — `fab migrate` 6/u + 7/u 적용 완료.
- [x] dirty-state 복구 runbook 박제 — Plan 22 결정 9에 박제됨.
- [x] `silly-wiggling-balloon.md` revisit trigger sync — `b8bc9d7` follow-up commit.
- [x] PR-D ina:plan kickoff — Plan 23 박제 (`.claude/plans/plan-23-pr-d-devices-endpoints.md`).
- [ ] 채팅팀에 PR-D 머지 ETA 알림 (PR-D 머지 후).

## Plan 23: RS256/JWKS + device credential — PR-D (devices endpoints + device JWT shape) ← 현재

> Spec: `.claude/plans/plan-23-pr-d-devices-endpoints.md` (Architect/Critic/CEO 3관점 ITERATE 후 합의)
> Goal: `/auth/devices/{pair, refresh, list, delete}` 4 endpoints + `SignDeviceJWT`. silly-wiggling-balloon.md 4단계 cutover의 종착.
> 직전: PR-C `d275a86` (devices DB + store layer)
> 채팅팀 cross-repo verifier가 PR-D 머지 후 첫 device JWT로 E2E 검증 가능.

**3관점 합의 결과**:
- Architect APPROVED (7 concerns 박제 후): pair atomicity, refresh middleware, D1/D2 split 재검토, wire-format specificity
- Critic APPROVED (10 concerns 박제 후): T6 sub-step, mock 전략, mockDeviceStore, error mapping table, edge cases
- CEO 4 cuts 반영: T6 collapse 단일, T4/T5 integration-only, sequential explicit revoke (defer+bool 대신), refresh route을 authMW 밖 chi.Group으로 분리 (pull forward)

- [x] **T1: `SignDeviceJWT` 단위 + wire-format guard**
      RED: `internal/auth/devices_test.go` 신규 — `TestSignDeviceJWT_RoundTrip` (verify 통과 + sub=device:<id>, user_id, aud=chat, scope=daemon:connect, v=2 단언) + `TestSignDeviceJWT_WireFormatMatchesIssueDeviceJWT` (claim 구조 비교 — header alg/typ/kid + payload key set + sub prefix + iss + aud[0] + scope[0] + v=2; iat/exp는 type만).
      GREEN: `internal/auth/devices.go` 신규 + `SignDeviceJWT(userID, deviceID, key *rsa.PrivateKey, kid string, ttl time.Duration) (string, error)` 구현. `deviceClaimsPayload` (testfixture/jwt.go:25-30 패턴 동일) + `jwt.SigningMethodRS256` + header kid set.

- [x] **T2: Pair handler + atomicity (compensating revoke)**
      RED: `devices_test.go` (mock) + `devices_integration_test.go` (real DB) — Happy/Anonymous/BodyTooLarge/MalformedJSON/EmptyName/CapabilitiesArray/RefreshCreateFails_RevokesDevice (mock RefreshTokenStore 강제 에러 → device.RevokedAt 단언).
      GREEN: `OAuthHandler.DeviceStore` 필드 추가 + `HandlePair()` 구현. body decode (4KB cap), name validation, sequential explicit revoke (CEO 권고 — defer+bool 대신).

- [x] **T3: Refresh handler**
      RED: Happy_Rotation/ReuseDetection/Expired/UnknownHash/UserScopedRefresh_401/RevokedDevice/RevokeRace/AuthorizationHeaderIgnored.
      GREEN: `HandleDeviceRefresh()` 구현. body decode (1KB cap), `FindByHash` → 분기 (DeviceID nil / RevokedAt non-nil → reuse + RevokeAllForDevice / Expired) → `RevokeIfActive` (race check) → `DeviceStore.FindByID` (revoked check) → `SignDeviceJWT` + `CreateForDevice`.

- [x] **T4: List handler (integration only)**
      RED: `devices_integration_test.go` — Happy_2Devices (paired_at DESC) / RevokedFiltered / Anonymous_401 / OtherUserDevicesHidden / ZeroDevices_EmptyArray (`[]` not `null`).
      GREEN: `HandleDevicesList()` 구현. `auth.UserFromContext` nil → 401, `DeviceStore.ListActiveForUser(user.ID)` → JSON array (nil slice → `[]*model.Device{}` 강제).

- [x] **T5: Delete handler (integration only)**
      RED: Happy_200 / NotFound_404 / DifferentUser_404 / AlreadyRevoked_404 / Anonymous_401 / InvalidUUID_404.
      GREEN: `HandleDeviceDelete()` 구현. URL param 추출, `FindByID` 분기 모두 404, `RefreshTokenStore.RevokeAllForDevice` → `DeviceStore.Revoke` → `200 {}`.

- [x] **T6: 라우팅 wiring + spec + smoke (단일)**
      RED: `cmd/server/main_test.go`에 `TestDevicesRoutesWired_*` (4 routes liveness).
      GREEN:
      - `cmd/server/main.go` chi.Group 분리 (refresh authMW 밖) + 4 라우트 등록 + DeviceStore 와이어링
      - `internal/auth/handler.go` `OAuthHandler.DeviceStore` 필드 추가
      - `docs/specs/kittychat-credential-foundation.md` D5에 production issue 경로 + Error mapping table 박제
      - `deploy/smoke.sh`에 4 endpoints liveness check 추가

**검증**: `make build` ✓ / `make lint` 0 issues / `make test` PASS / `make test-integration -p 1` PASS / `make test-migration` PASS.

**완료 신호**: silly-wiggling-balloon.md 4단계 cutover (PR-A/B/C/D) 종착. daemon E2E 가능. 채팅팀 cross-repo verifier가 첫 device JWT로 검증 시작.

**Operational Checklist** (PR-D 머지 후):
- [ ] `fab deploy` (binary upload + restart) — 마이그레이션 불필요 (PR-C에서 schema 7 완료)
- [ ] `make smoke` 회귀 0 + auth/devices/* 4 endpoints liveness PASS
- [ ] 채팅팀에 머지 신호 — cross-repo verifier가 첫 device JWT E2E 검증 가능
- [ ] daemon 첫 pairing 수동 검증 (kittypaw CLI ready 시점)

## Plan 24: Credential Lifecycle Janitor ← 현재

> Goal: device + refresh_token 자동 GC. 사용자는 "설치 → 로그인 → 채팅"만 알면 되고, idle/expired/revoked credential은 서버가 알아서 정리.
> 의사결정 (사용자 confirm 완료):
> - **Idle threshold**: 60일 (last_used_at 기준, soft revoke)
> - **Revoked retention**: 90일 (revoked_at 기준, hard delete — refresh_tokens CASCADE)
> - **Expired refresh retention**: 30일 (expires_at 기준, hard delete)
> - **Cadence**: 매일 KST 04:00 (Go ticker, in-process, graceful-shutdown 연동)
> - **last_used_at touch**: refresh 호출 성공 시 1번 (best-effort, transaction 외부)
> - **per-user device cap**: 별도 일감 (이 PR 범위 밖)

- [ ] **T1: device.Touch + last_used_at refresh wiring + index**
      RED: `device_test.go` mockDeviceStore.Touch — refresh 성공 시 last_used_at non-nil. `device_integration_test.go` Touch round-trip.
      GREEN:
      - `internal/model/device.go` DeviceStore 인터페이스에 `Touch(ctx, id) error` 추가
      - `internal/model/device_pg.go` `UPDATE devices SET last_used_at = now() WHERE id = $1 AND revoked_at IS NULL`
      - `internal/auth/devices.go` `HandleDeviceRefresh` 성공 경로 끝에 `_ = h.DeviceStore.Touch(ctx, dev.ID)` (best-effort, 실패 시 logStoreErr Warn)
      - `internal/auth/devices_test.go` mockDeviceStore.Touch in-memory 갱신
      - migration 008: `idx_devices_last_used ON devices(last_used_at) WHERE revoked_at IS NULL`, `idx_refresh_tokens_expires ON refresh_tokens(expires_at) WHERE revoked_at IS NULL`

- [ ] **T2: cleanup 메소드 (DeviceStore + RefreshTokenStore) + janitor 패키지**
      RED: `device_integration_test.go` ReapIdle / DeleteRevokedOlderThan; `refresh_token_integration_test.go` DeleteExpiredOlderThan; `janitor/janitor_test.go` mock 기반 1tick 시 호출 verify + clock injection.
      GREEN:
      - `internal/model/device.go` `ReapIdle(ctx, olderThan) (int64, error)`, `DeleteRevokedOlderThan(ctx, olderThan) (int64, error)`
      - `internal/model/refresh_token.go` `DeleteExpiredOlderThan(ctx, olderThan) (int64, error)`
      - 각 구현: LIMIT 1000 LOOP 패턴 (autovacuum 압력 + lock 시간 제한)
      - mockDeviceStore / mockRefreshTokenStore stub 메소드 (test 갱신)
      - `internal/janitor/janitor.go` 신규: `New(devices, refresh, policy, clock) *Janitor` + `Run(ctx)` (24h ticker + KST 04:00 첫 alignment)
      - `cmd/server/main.go` janitor goroutine wire (graceful-shutdown 연동, 이미 Plan 19에서 패턴 박힘)

- [ ] **T3: 통합 검증 + Operational Checklist 갱신**
      RED: `janitor_integration_test.go` (`//go:build integration`) — fixture insert (idle device 60일+, revoked 90일+, expired 30일+) → janitor 1tick → row count 검증.
      GREEN: `make build / lint / test / test-integration / test-migration` 모두 pass. **사용자 명시 허락 후** atomic commit.

**Operational Checklist** (PR 머지 후, 정기):
- [ ] `fab deploy` (binary upload + restart) — migration 008 자동 적용
- [ ] 매주: slog `janitor.tick` 라인에서 reaped/deleted count 확인 (0이 7일+ = 정책 이상 신호)
- [ ] 분기: idle threshold 60일이 적정한지 검증 (사용자 churn / 분실 device 메트릭)
- [ ] per-user device cap 일감 (Plan 25 후보) — device-stuffing 방어

## Plan 25: KittyChat Web OAuth Flow (PKCE + Code Exchange) ← 현재

> Spec: chat 팀과 합의된 contract — Authorization Code with PKCE, BFF 패턴.
> Goal: chat.kittypaw.app가 server-to-server로 OAuth 처리. browser에는 HttpOnly session cookie만, token은 chat 서버 session-only.
> 의사결정 (확정):
> - **Refresh**: 기존 `/auth/token/refresh` 그대로 사용 (모든 user OAuth token이 동일 multi-aud `[api, chat]` 발급, audience 컬럼 추가는 YAGNI)
> - **Audience**: 기존 multi-aud 재사용 (`scopes.go` `DefaultAPIClientAudiences`). web user JWT는 `aud=[api, chat]`, `scope=[chat:relay, models:read]`, `sub=<user_id>` — daemon JWT (`sub=device:<id>`, `scope=[daemon:connect]`)와 sub/scope으로 명확히 구분.
> - **Code TTL**: 60s (chat → API 왕복 충분, browser 외부 노출 시 빠른 만료)
> - **redirect_uri allowlist**: env `WEB_REDIRECT_URI_ALLOWLIST` (CSV exact match)
> - **CORS**: `/auth/web/exchange`는 server-to-server. `cors.AllowedOrigins`에 chat.kittypaw.app 미포함 → browser 자동 차단.
> - **code_challenge_method**: S256만 허용 (plain 거부)

**Contract 요약**:
```
1. chat → API:
   GET /auth/web/google?redirect_uri=...&state=<chat_state>
       &code_challenge=<S256(verifier)>&code_challenge_method=S256

2. API → chat callback:
   302 {redirect_uri}?code=<one-time>&state=<echoed-chat_state>

3. chat 서버 → API (server-to-server):
   POST /auth/web/exchange
   { "code", "code_verifier", "redirect_uri" }

   → { access_token, refresh_token, token_type, expires_in }
```

- [ ] **T1: WebCodeStore (1회용 60s TTL store)**
      RED: `web_code_store_test.go` — Create/Consume happy / unknown code → error / expired → error / one-time use (두 번 Consume → 두 번째 fail).
      GREEN: `internal/auth/web_code_store.go` — `WebCodeEntry { UserID, RedirectURI, CodeChallenge }`. CLICodeStore 미러 (mu + map + sweep goroutine, 60s TTL).

- [ ] **T2: HandleWebGoogleLogin + Google callback web 분기**
      RED: `web_test.go` — allowlist 외 redirect_uri → 400 / S256 외 method → 400 / missing chat_state → 400 / happy path → 302 to Google with state metadata 박음 / callback web 분기 → redirect to {redirect_uri}?code=&state=
      GREEN:
      - `internal/auth/web.go` `HandleWebGoogleLogin` — redirect_uri allowlist 검증 → state.CreateWithMeta({mode=web, redirect_uri, chat_state, code_challenge}) → Google redirect.
      - `google.go` `HandleGoogleCallback` web 분기: state.metadata["mode"]=="web" → token 발급 안 함 → WebCodeStore.Create → 302 redirect_uri.

- [ ] **T3: HandleWebExchange (PKCE 검증 + token 발급)**
      RED: `web_test.go` — unknown code → 401 / expired code → 401 / verifier mismatch → 401 / redirect_uri mismatch → 400 / happy → 200 with TokenResponse (multi-aud token).
      GREEN: `web.go` `HandleWebExchange` — body decode (1KB cap) → WebCodeStore.Consume → redirect_uri 매치 검증 → ChallengeS256(verifier) == stored_challenge → UserStore.FindByID → issueTokenPair (재사용).

- [ ] **T4: config + main.go wire**
      `internal/config/config.go` — `WebRedirectURIAllowlist []string` (CSV parse).
      `cmd/server/main.go` — WebCodeStore 인스턴스 + cleanup wire / 라우트 등록 (`/auth/web/google`, `/auth/web/exchange`) / cors AllowedOrigins에 chat.kittypaw.app 미포함 검증.

- [ ] **T5: 통합 검증 + chat 팀 알림**
      `make build / lint / test / test-integration -p 1` 모두 pass. **사용자 명시 허락 후** atomic commit.
      chat 팀 알림: scope-based authorization 구현 명시 (device endpoint는 `daemon:connect` scope만, user endpoint는 `chat:relay` 만).

**Operational Checklist** (배포 후):
- [ ] `.env` 갱신: `WEB_REDIRECT_URI_ALLOWLIST=https://chat.kittypaw.app/auth/callback`
- [ ] `fab deploy`
- [ ] chat 팀 BFF 구현 시작 신호

## Follow-up 일감 (별도 PR / 별도 plan 권장)



- [ ] **L4 — 신규 kittypaw skill 작성** (`../skills/packages/`) — 음력 변환 + 일출/일몰. spec 7가지 (묶음 단위 / trigger / config / 응답 포맷 / 에러 / 인증 / allowed_hosts) 결정 필요. `ina:think` → `ina:plan` 워크플로우 권장. Plan 5 T6 (weather-briefing → KMA fallback) 선례 참고.
- [ ] **Phase C — 서울교통공사 OpenAPI 활용 신청** (사용자 직접 작업) — data.go.kr 카탈로그에서 "지하철 실시간 도착정보" 검색 → 활용신청 → 자동/수동 승인 (1~3일) → `.env` `SEOUL_METRO_API_KEY` 등록. Phase B(KMA 확장) 작업과 병행 발의 권장.
- [ ] **D4 trigger 발동 — KASI helper 통합 refactor** — KASI endpoint 7개 (>5 trigger) 도달. `internal/proxy/kasi/endpoint.go` 공통 helper 추출 + `holiday.go` / `almanac.go` thin wrapper 화. 별도 `refactor(proxy):` PR. 회귀 검증 위해 기존 unit test 전부 그대로 통과해야 함.
- [ ] **Phase B 첫 endpoint — KMA 자외선 (UV)** — plan v1 박제 (`.claude/plans/kma-uv-index.md`). 3 reviewer Phase 2 ITERATE — **옵션 2 (PR-2 우선, UV 보류)** 채택. 재개 트리거: PR-2 머지 + KMA UV 키 활성화. 그 시점에 plan v2 박제 (Phase 2 ITERATE 항목 must-fix 5 + should-fix 5 반영) → ina:build.
