# KittyAPI Proxy 카탈로그 — 외부 API 통합 조사

> Historical research snapshot. This document captures the code and service
> shape observed at the time of the research; use repository README,
> ARCHITECTURE.md, and app README/DEPLOY docs for the current live shape.

> **조사 시점**: 2026-04-30
> **조사 범위**: KittyPaw 내부 (`../skills` 14개 + 현재 KittyAPI proxy) + 경쟁사 (hermes-agent, openclaw) + AI automation 일반 카탈로그 + proxy 적합성 framework
> **산출 목적**: KittyAPI proxy 가 *추가로* 지원할 외부 API 결정의 근거 자료

---

## Executive Summary

1. **현재 KittyAPI 가 proxy 하는 API는 2개**: AirKorea (대기질) + Holiday (공휴일).
2. **KittyPaw 의 14개 skill 중 KittyAPI 경유는 1개 (air-quality)뿐** — 나머지 13개는 외부 API 직접 호출. 즉 *proxy 의 정당성 자체가 데이터로 약함*. 굳이 proxy 가 필요한 API (key 보호 / rate limit 흡수 / schema 안정화) 만 추가해야 ROI 양수.
3. **OpenClaw 는 LLM provider 카탈로그가 압도적** (47개 vendor) + **메시징 채널이 21개** (LINE/Zalo/QQ/Feishu 까지 포함). KittyPaw 의 *Kakao* 는 두 경쟁사 어디에도 없음 — KittyAPI 가 잘 만들면 차별화 포인트.
4. **Hermes 는 longtail (research/blockchain/health/bioscience) 무료 통합이 강함** — Polymarket / arXiv / ChEMBL+OpenFDA / OSM Overpass / CoinGecko / CT logs / RSS 등. KittyAPI 가 "key 없이 free quota proxy" 를 first-class 로 노출하려면 hermes 카탈로그가 빠른 win.
5. **양쪽 다 약한 영역 (KittyAPI 신규 진입 기회)**: 한국 specific (Kakao/Naver/Toss/data.go.kr 통합), 주식·환율 ticker (Alpha Vantage / Polygon / Yahoo Finance), 일반 뉴스 API, 번역 (DeepL/Papago), CRM (Salesforce/HubSpot), 결제 (Stripe/Toss/카카오페이).

---

## P1 — KittyPaw 내부 현재 상태

### 1.1 KittyAPI 현재 proxy (2개)

| Endpoint | Upstream | 캐시 | 메모 |
|---|---|---|---|
| `/airkorea/*` (5개 sub-endpoint) | AirKorea (대기질 측정소) | ✅ | 공공데이터포털 인증 필요 |
| `/holidays/*` (3개 sub-endpoint) | data.go.kr 공휴일/기념일/24절기 | ✅ | 공공데이터포털 인증 |

소스: `kittyapi/internal/proxy/{airkorea,holiday}.go`

인프라: chi router + cache + JWT + rate limit (anon 5/min, auth 60/min) + OAuth (Google/GitHub) + `/discovery` endpoint. 즉 *추가 endpoint 만 붙이면 되는 baseline 이 이미 잘 갖춰져 있음*.

### 1.2 KittyPaw skills 의 외부 API 의존도 (14개)

| Skill | 외부 API | KittyAPI 경유? | API key |
|---|---|---|---|
| **air-quality** | api.kittypaw.app (AirKorea proxy) | ✅ | OAuth |
| delivery-tracker | info.sweettracker.co.kr | ❌ 직접 | 필요 |
| exchange-rate | api.frankfurter.dev | ❌ 직접 | 불필요 |
| lotto-check | www.dhlottery.co.kr | ❌ 직접 | 불필요 |
| macro-economy-report | query{1,2}.finance.yahoo.com | ❌ 직접 | 불필요 |
| news-digest-kr | news.google.com | ❌ 직접 | 불필요 |
| reminder | (외부 API 없음) | – | – |
| rss-digest | * (any RSS) | ❌ 직접 | 불필요 |
| stock-alert | m.stock.naver.com | ❌ 직접 | 불필요 |
| stock-quote | www.alphavantage.co | ❌ 직접 | **필요** |
| url-monitor | * (any URL) | ❌ 직접 | – |
| weather-briefing | api.open-meteo.com | ❌ 직접 | 불필요 |
| weather-now | wttr.in | ❌ 직접 | 불필요 |
| world-time | timeapi.io | ❌ 직접 | 불필요 |

**핵심 발견**: 13개 skill 중 *key 가 필요한 외부 API* 는 **2개** (delivery-tracker → sweettracker, stock-quote → alphavantage). 이 두 개가 KittyAPI proxy ROI 가 가장 높은 즉시 후보. 나머지 11개는 *proxy hop 만 비용* 이 될 가능성이 높음.

---

## P2 — Hermes-Agent + OpenClaw 통합 카탈로그

> 소스: `competitors/hermes-agent/` + `competitors/openclaw/extensions/*/openclaw.plugin.json` (직접 read).

### 2.1 Hermes 핵심 카테고리

| 카테고리 | 대표 통합 (free) | 대표 통합 (paid) |
|---|---|---|
| Search/Research | duckduckgo, arxiv, polymarket, blogwatcher (RSS), find-nearby (OSM Overpass) | parallel-cli |
| Productivity | google-workspace (OAuth), notion (key), linear (key) | – |
| Email/Telephony | himalaya (IMAP/SMTP) | agentmail, twilio + bland.ai + vapi |
| Apple ecosystem | apple-notes/reminders/imessage/findmy (macOS) | – |
| Maps/Local | OSM Overpass (free) | – |
| Media | youtube-content, songsee | gif-search (Tenor key) |
| Blockchain | solana RPC + CoinGecko, base RPC + CoinGecko | – |
| Health/Science | ChEMBL + OpenFDA, USDA FoodData, wger | – |
| Smart Home | openhue (Philips Hue LAN) | – |

LLM 카탈로그 (코어): Anthropic, OpenAI, Google Gemini, xAI Grok, MiniMax, Xiaomi MiMo, Ollama, OpenRouter, Nous Portal, OpenCode Go, Z.AI, Fireworks, Mistral, Supermemory.

채널: CLI / Telegram / Discord / Slack / Matrix / Signal / Mattermost / Gateway. **Kakao 없음**.

### 2.2 OpenClaw 핵심 카탈로그

**LLM Provider plugin: 47개**. Anthropic / OpenAI / Google / xAI / Bedrock / Alibaba / Qwen / Byteplus / Moonshot Kimi / Volcengine / Xiaomi / Z.AI / MiniMax / Stepfun / Qianfan / Chutes / Cloudflare AI Gateway / Vercel AI Gateway / OpenRouter / LiteLLM / GitHub Copilot / Groq / Fireworks / Mistral / DeepSeek / NVIDIA / etc.

**Search Provider plugin: 11개**. Brave / DuckDuckGo / Exa / Firecrawl / Perplexity / Tavily / SearXNG / Ollama / Gemini / Kimi / Grok.

**Speech/TTS/Transcription: 6개**. ElevenLabs / Deepgram / OpenAI / Microsoft / MiniMax / Vydra.

**Image/Video/Music Generation: 8+개**. OpenAI / Google / Fal / Comfy / MiniMax / Vydra / Runway / Together / xAI.

**메시징 채널: 21개** — Discord / Telegram / Slack / MS Teams / Matrix / Mattermost / Signal / IRC / WhatsApp / iMessage / BlueBubbles / Google Chat / **Feishu** / **LINE** / Nostr / Nextcloud Talk / **QQBot** / Synology Chat / Tlon / Twitch / **Zalo** / Zalouser. **Kakao 없음**.

**Voice/Telephony plugin**: Telnyx + Twilio + Plivo (3 vendors).

**User-facing skills (53개)** 중 외부 API 의존: weather (wttr.in/Open-Meteo), trello, notion, github, xurl (Twitter), goplaces (Google Places), gog (Google Workspace OAuth), spotify-player, sonoscli, openhue, ordercli (Foodora), eightctl (Eight Sleep), 1password, openai-whisper-api, sag (ElevenLabs).

### 2.3 양쪽 비교

| 영역 | Hermes 강점 | OpenClaw 강점 | 양쪽 약점 |
|---|---|---|---|
| LLM provider | 14개 | 47개 (특히 중국 vendor) | – |
| 메시징 채널 | 8개 | 21개 (LINE/Feishu/Zalo/QQ) | **Kakao** 없음 |
| Search | DDG, parallel-cli | 11개 search provider | – |
| Speech/Image/Video/Music | inference.sh aggregator 1개 | 8+ vendor 깊이 | – |
| Research/Bio/Blockchain | Polymarket, ChEMBL+OpenFDA, USDA, Solana RPC, Base RPC, ArXiv | – | – |
| Maps/Geocoding | OSM only | Google Places only | Mapbox/HERE/Kakao Maps 통합 없음 |
| Stock/Forex 실시간 | – | – | **둘 다 없음** |
| 일반 News API | RSS only | – | NewsAPI/GNews 없음 |
| Translation 전용 | – | – | DeepL/Papago plugin 없음 (LLM 으로 대체) |
| Calendar (구체) | Google Calendar (gws) | Google Calendar (gog) | iCal/Outlook/Calendly 없음 |
| CRM | – | – | Salesforce/HubSpot/Zendesk 없음 |
| Banking/Payments | – | – | Stripe/Plaid/Toss/카카오페이 없음 |

**KittyAPI 차별화 후보**: (a) 한국 messenger 깊이 (Kakao + 향후 LINE/Zalo), (b) 한국 financial/govt 통합 (KIS/data.go.kr/한국은행 ECOS), (c) 결제 통합 (토스/카카오페이/Stripe).

---

## P3 — AI Automation 일반 외부 API 카탈로그

> 18 카테고리 × 카테고리별 top 3 추천 (자세한 표는 Appendix A).

| 카테고리 | top 3 추천 (KittyAPI proxy ROI 관점) |
|---|---|
| 1. 웹 검색 | SearXNG self-host + Brave Search ($5/1K) + Google CSE (2027.1 EOL 대비 마이그레이션) |
| 2. 날씨 | Open-Meteo (글로벌·무료) + OpenWeatherMap (key proxy ROI) + 기상청 단기예보 (KR 정확도) |
| 3. 주식 | Finnhub (60/min 가장 관대) + yfinance (무료 backup) + 네이버 금융 (KR 종목) |
| 4. 암호화폐 | CoinGecko Demo (30/min 안정) + Binance public + CoinMarketCap (key ROI) |
| 5. 환율 | Frankfurter (무제한·무료) + ExchangeRate-API (key ROI) + 한국은행 ECOS (KR) |
| 6. 뉴스 | Google News RSS (무료) + GNews (key ROI) + RSS feeds (다양성) |
| 7. 지도 | Nominatim public (1/sec) + HERE (250K/월 무료) + **Kakao Maps (KR 핵심)** |
| 8. 시간/달력 | TimeAPI.io (무료) + Nager.Date (공휴일·무료) + Calendarific (key ROI) |
| 9. 번역 | Microsoft Translator (2M chars/월) + **Papago (KR 품질)** + DeepL Free (유럽어) |
| 10. 택배 | Sweet Tracker (KR 메인) + AfterShip (글로벌) + EasyPost (USPS/UPS) |
| 11. 항공/교통 | AviationStack (key ROI) + Amadeus Self-Service (test 무제한) + **KR TAGO (대중교통)** |
| 12. 레시피 | TheMealDB (무료) + Edamam + Spoonacular |
| 13. 이미지/미디어 | Unsplash + Giphy + TMDB |
| 14. 위키/지식 | Wikipedia REST + Wikidata + MediaWiki Action |
| 15. 개발자 | GitHub auth (5K/hr) + Stack Exchange (10K/일 with key) + PyPI |
| 16. 이메일/통신 | Resend (3K/월 영구) + Mailgun (백업) + Twilio SMS (PIN/2FA) |
| 17. AI/LLM 외부 | Together AI (오픈소스) + Replicate (이미지 생성) + Groq (속도) |
| 18. **한국 특화** | data.go.kr 통합 wrapper (기상청+AirKorea+TAGO) + 한국은행 ECOS + Naver Open API |

### 3.1 마이그레이션·리스크 모니터링 필요

- **Brave Search**: 2025.5 무료 tier 폐지 → 비용 발생.
- **Google CSE**: 2027.1 EOL → SerpAPI/Brave/Serper 등 대안 준비.
- **WorldTimeAPI**: 이미 sunset (2024) → TimeAPI.io 마이그레이션. *KittyPaw `world-time` skill 이 `timeapi.io` 쓰는지 점검 필요*.
- **SendGrid**: 영구 무료 폐지 → Resend/Mailgun.
- **Wikimedia**: 2026.3-5 신규 rate limit → 캐시 전략 강화.
- **Yahoo Finance/yfinance**: 비공식, 갑작스런 차단 risk → KittyPaw `macro-economy-report` 가 이걸 직접 씀, proxy 화 우선순위 ↑.

---

## P4 — KittyAPI Proxy 적합성 평가 Framework

### 4.1 정량 Scorecard (12 criteria)

각 외부 API 후보를 채점. **합계 ≥ +6 → strong fit; +3~5 → conditional; ≤ +2 → reject.**

| # | 질문 | Yes | No |
|---|---|---|---|
| 1 | API key 가 필요한가? | +2 | 0 |
| 2 | 무료 tier rate limit 이 사용자 1명당 < 60/hr 인가? | +2 | 0 |
| 3 | 응답이 캐시 가능한가 (TTL ≥ 60s 효용)? | +2 | 0 |
| 4 | LLM 입력으로 schema 안정성/normalization 이 필요한가? | +2 | 0 |
| 5 | 응답이 PII/IP 노출 risk 있나 (사용자 query 가 sensitive)? | +1 | 0 |
| 6 | upstream 가 종종 다운되는가 (uptime < 99.9%)? | +1 | 0 |
| 7 | observability/cost-tracking 이 필요한가? | +1 | 0 |
| 8 | **사용자별 OAuth 가 필요한가?** (Gmail/Calendar/Slack) | **−3** | 0 |
| 9 | streaming/binary/대용량 페이로드인가? | −2 | 0 |
| 10 | 매우 빠르게 변하는 실시간 데이터인가 (TTL < 5s)? | −1 | 0 |
| 11 | **upstream ToS 가 중계/redistribution 을 금지하는가?** | **−5 (즉시 reject)** | 0 |
| 12 | **결제/의료 등 책임 경계가 모호한가?** | **−5 (즉시 reject)** | 0 |

### 4.2 운영 체크리스트 (proxy 추가 전)

- [ ] ToS 검토 — 중계/캐싱/재배포 허용 명시
- [ ] attribution 요구사항
- [ ] 인증 방식 (key / OAuth / mTLS)
- [ ] rate limit 명세 (분/시/일, IP/key 기준)
- [ ] 캐시 정책 (TTL, ETag/Last-Modified)
- [ ] fail-open vs fail-closed 명시
- [ ] schema versioning (upstream major version up 시)
- [ ] PII 정책 (redaction 필요?)
- [ ] payload 크기 limit
- [ ] observability hook
- [ ] deprecation 정책 (우리 측 schema 변경)
- [ ] eval 테스트 (LLM 회귀)

### 4.3 Reference Patterns 차용

| 카테고리 | 차용 대상 |
|---|---|
| 캐시 backend | Helicone Rust gateway (Apache 2.0, P95<5ms, S3/Redis cache) |
| BYOK / virtual key | Cloudflare AI Gateway, Portkey virtual key |
| stale-while-revalidate / stale-if-error | RFC 5861 + AWS REL05-BP01 graceful degradation |
| schema normalization | Anthropic "Writing Tools for Agents" + OpenAI structured outputs `strict: true` |
| PII redaction | Microsoft Presidio + FastAPI middleware |
| retry + fallback + circuit breaker | Portkey gateway patterns |

---

## 종합 권장 — KittyAPI 추가 우선순위 Top 10

### Tier 1: 즉시 추가 (high ROI, 12-criteria score ≥ +6)

1. **OpenWeatherMap** — 일상 #1, 60/min key proxy + 캐시 ROI 큼. (KittyPaw `weather-briefing` 이 Open-Meteo 쓰지만 OWM 도 사용자 선호 많음.)
2. **GitHub authenticated** — unauth 60/hr → auth 5K/hr (~83x). 개발자 사용자 핵심.
3. **Naver Open API + Papago** — KR 검색·번역 핵심. KittyPaw 의 KR 우위 유지.
4. **data.go.kr 통합 wrapper** — 기상청 + AirKorea + TAGO + 공휴일 + 통계청 단일 인증. 기존 AirKorea/Holiday 의 자연스러운 확장.
5. **Alpha Vantage** — KittyPaw `stock-quote` 가 이미 사용 중, 25 calls/일 매우 빡빡 → proxy + 캐시로 사용자별 quota 분배.

### Tier 2: 중기 추가 (conditional, score +3~5)

6. **Sweet Tracker (KR 택배)** — KittyPaw `delivery-tracker` 가 이미 사용. key proxy ROI.
7. **한국은행 ECOS** — 환율/금리/거시 통계, KR 사용자 차별화.
8. **Frankfurter (환율)** — 무료지만 *cache CDN edge ROI*. KittyPaw `exchange-rate` 이미 사용.
9. **Finnhub (주식)** — 60/min, 글로벌 stock proxy 의 baseline.
10. **Resend (이메일 발송)** — 향후 알림/리포트 발송 인프라.

### Tier 3: 신규 진입 차별화 (양쪽 경쟁사 약함, score 가변)

- **KakaoTalk channel 자체** — KittyPaw 의 unique strength. KittyAPI 가 Kakao API key 보호 + relay 통합.
- **Kakao Maps + Naver Maps** — KR 지도 (글로벌 Maps API 와 별도).
- **Toss / 카카오페이 결제** — 가능성, 단 책임 경계 모호로 12-criteria #12 reject 위험.
- **KIS API (한국투자증권)** — KR 종목 실시간, 단 사용자별 계정 OAuth 필요 (#8 −3).

### Reject (proxy 부적합)

- Open-Meteo / wttr.in / TimeAPI.io / Frankfurter (무료·무제한·무키) — proxy hop 만 비용. 단 *cache CDN 가치* 만 살리려면 Tier 2 conditional.
- Google Workspace OAuth 류 (Gmail/Calendar/Drive) — 사용자별 OAuth 필요 (#8 −3). token-forward 모드만 가능.
- 실시간 시세 streaming / 대용량 미디어 — bandwidth/streaming 부적합.

---

## Appendix A — P3 자세한 18 카테고리 표

(생략. 자세한 free tier / rate limit / API key / ROI 정보는 P3 research report 원본 참고.)

## Appendix B — Hermes/OpenClaw 통합 전체 표

(생략. P2 research report 원본 참고.)

---

## Sources

### Industry references
- Anthropic — [Writing Effective Tools for AI Agents](https://www.anthropic.com/engineering/writing-tools-for-agents)
- OpenAI — [Structured Outputs](https://openai.com/index/introducing-structured-outputs-in-the-api/)
- Cloudflare — [AI Gateway Caching](https://developers.cloudflare.com/ai-gateway/features/caching/), [Privacy Gateway](https://blog.cloudflare.com/building-privacy-into-internet-standards-and-how-to-make-your-app-more-private-today/)
- Helicone — [AI Gateway (GitHub)](https://github.com/Helicone/ai-gateway)
- Portkey — [Gateway (GitHub)](https://github.com/Portkey-AI/gateway)
- LangChain — [Improving Core Tool Interfaces](https://blog.langchain.com/improving-core-tool-interfaces-and-docs-in-langchain/)
- RFC 5861 — [HTTP Cache-Control Extensions for Stale Content](https://datatracker.ietf.org/doc/html/rfc5861)
- AWS — [REL05-BP01 Graceful Degradation](https://docs.aws.amazon.com/wellarchitected/latest/reliability-pillar/rel_mitigate_interaction_failure_graceful_degradation.html)

### Public API list references
- [public-apis (GitHub)](https://github.com/public-apis/public-apis)
- [Mixed Analytics — Free Open Public APIs (No Auth) 2026](https://mixedanalytics.com/blog/list-actually-free-open-no-auth-needed-apis/)
- [Public APIs for Korean Services (GitHub)](https://github.com/yybmion/public-apis-4Kr)

### Pricing / rate limit references (2026-04 snapshot)
- Open-Meteo, OpenWeatherMap, Brave Search, SerpAPI, Google Custom Search, Frankfurter, ExchangeRate.host, NewsAPI, GNews, Mediastack, Mapbox, HERE, Nominatim, TimeAPI.io, Nager.Date, DeepL Free, Microsoft Translator, Papago, Spoonacular, Edamam, Unsplash, Pexels, Pixabay, Giphy, TMDB, OMDB, GitHub REST, Stack Exchange, Resend, SendGrid, Twilio, Hugging Face Inference, AfterShip, EasyPost, AviationStack, Amadeus, OpenSky, CoinGecko, CoinMarketCap, Binance, Kraken, Alpha Vantage, Finnhub, Polygon.io, IEX Cloud, Tiingo, yfinance, data.go.kr, ECOS, KOSIS, AirKorea, KIS API, Naver Open API, Kakao API.

(URLs 전부는 P3 research report sources 섹션 참고.)

### Vendored sources
- `competitors/hermes-agent/skills/**/SKILL.md` (default-on skills)
- `competitors/hermes-agent/optional-skills/**/SKILL.md` (opt-in skills)
- `competitors/hermes-agent/RELEASE_v0.8.0.md` (channel/provider catalog)
- `competitors/openclaw/extensions/*/openclaw.plugin.json` (100 plugin manifests)
- `competitors/openclaw/skills/*/SKILL.md` (53 user-facing skills)
- `kittyapi/internal/proxy/*.go` (현재 KittyAPI proxy)
- `~/projects/kittypaw/skills/packages/*/package.toml` (KittyPaw skill 14개)
