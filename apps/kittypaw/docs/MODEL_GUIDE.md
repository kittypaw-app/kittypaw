# KittyPaw LLM Model Guide

> **Last verified**: 2026-05-04
> **Test machine (local)**: eMac (m3-enuma.local) — MacBook Pro **M3 Pro 36 GB**, 12-core (6P+6E), memory bandwidth 150 GB/s, macOS 26.3.1
> **Runtimes covered**: Ollama 0.23.0 · LM Studio (`lms` 0.0.12 / llmster) · Cloud free tiers
> **KittyPaw provider**: Anthropic Claude (default) + OpenAI Chat Completions wire (Cerebras/Groq/DeepSeek/OpenRouter/Ollama via `provider="<name>"` registry case)

This is a fact-only running log of which models work as a KittyPaw assistant, which fail, and **why**. Each row is reproducible from the [Methodology](#8-methodology) section. Speculation is labeled.

> **로컬에서 직접 `/model` 시연하기**: `make dev-models` (격리된 dev harness — 사용자 `~/.kittypaw/` 무영향). 사용 가이드 → [DEV_MODELS.md](./DEV_MODELS.md).

---

## 1. Decision Matrix (TL;DR)

| Scenario | 1순위 | 2순위 | Notes |
|---|---|---|---|
| **36 GB M3 Pro, local, privacy** (LM Studio OK) | **`Qwen3-30B-A3B-Instruct-2507` 4bit MLX (LM Studio)** ★★★ | qwen2.5:32b-instruct (Ollama) | MLX warm 0.55-0.61s — 30B MoE 즉답+정확성 통합. cold load 30s |
| **36 GB M3 Pro, local, privacy** (Ollama만, 즉답성 우선) | **`gemma4:latest` 8B** ★★★ | `qwen3:latest` 8B (max_tokens=1024) | warm 0.82-9s, multi-section |
| **36 GB M3 Pro, local, privacy** (Ollama만, 정확성·reasoning 우선) | **`qwen2.5:32b-instruct`** ★★★ | gemma4:8B | thinking 없음, 정체성 일관, warm 3-9s |
| **16 GB MBA, local, privacy** | **`phi4-mini:latest`** 3.8B ★★ | `qwen3:latest` 8B (max_tok=1024 thinking 부담) | phi4-mini 즉답 1-3s, 정체성 일관(Phi/Microsoft). granite4.1:8b는 정체성 hallucination(5.1.2) — system prompt 강제 안 하면 비추 |
| **Cloud free, Korean priority** (KittyPaw chat 디폴트) | **Mistral `mistral-medium-latest`** ★★★ (실측 — AI 자칭 일관, 한국어 자연, 128K context, 카드 ❌ phone 인증) | **Groq `qwen/qwen3-32b`** ★★★ (단 KittyPaw 어댑터에 `reasoning_format=parsed` 송신 필수 — § 5.13) | Cerebras 8K cap = KittyPaw chat **디폴트 부적합** (§ 5.3 실측). Mistral large/small은 "개발자 페르소나" SFT (§ 5.11). Mistral magistral은 Native reasoning disable 불가 (§ 5.12) |
| **Cloud free, speed + tool calling** | **Groq Llama 3.3 70B** ★★ | Cerebras Qwen3-235B (보조 phase, 8K cap 짧은 호출 한정) | Groq 한국어는 일본어 mixin 가끔. parallel tool 지원 (gpt-oss-* 제외) |
| **Cloud free, 다양화 후보** | (시도 가능) Mistral `ministral-8b-latest` 256K · `pixtral-large-latest` (vision) | Gemini `gemini-2.5-flash-lite` (단 무료 quota 매우 빡빡) | OpenRouter `:free` 모델 20 RPM · DeepSeek (5M/30일 grant docs 미명시) — 다양화 시 § 4.5-4.8 참조 |

추천 등급: ★★★ 비서 즉답성·정확도·한국어 모두 통과 / ★★ 한 축 약점 / ★ 특정 시나리오 / ✗ 비서 부적합.

---

## 2. Verified Local Models — Ollama (eMac M3 Pro 36 GB · ssh tunnel `:11500 → emac:11434`)

prompt: `안녕? 한 줄로 자기소개 해줘.`

### 2.1 결론 요약

| Model | Quant | size | Capabilities | 비서 결론 |
|---|---|---|---|---|
| **`gemma4:latest`** 8B | Q4_K_M | 9.6 GB | completion + tools | ★★★ 즉답성 1위 |
| **`phi4-mini:latest`** 3.8B | Q4 | 2.5 GB | completion + tools | ★★ 즉답 1-3s, 한국어 살짝 어색. **16GB MBA용 후보** |
| `qwen3:latest` 8B | Q4_K_M | 5.2 GB | completion + tools + **thinking** | ★★ max_tokens=1024 sweet spot |
| `qwen3:14b` | Q4_K_M | 9.3 GB | completion + tools + **thinking** | ✗ 14B+thinking, max_tok=1024에서 57s · 547 thinking 토큰 |
| `qwen3:4b` | Q4 | 2.5 GB | completion + tools + **thinking** | ✗ thinking 폭주 (4B인데 1241 토큰) |
| `qwen3:30b-a3b` | Q4_K_M | 18 GB | qwen3moe + tools + **thinking** | ✗ thinking 1500+ 토큰. instruct variant 필요 |
| `hermes3:8b` | Q4_K_M | 4.7 GB | completion + tools | ★ (영어 function 전용) — 한국어 자기소개에 가공된 이름 매번 다름 |
| `granite4.1:8b` | Q4_K_M | 5.3 GB | completion + tools | ★ (system prompt 강제 시 ★★) — 정체성 hallucination 매번 다름 (ChatGPT/Gemini/Granite 자칭) |
| `mistral-nemo:12b-instruct-2407-q4_K_M` | Q4_K_M | 7.5 GB | completion + tools | ✗ 한국어 안정성 부족 (영어/러시아어 혼용 응답) |
| **`qwen2.5:32b-instruct`** | Q4_K_M | 19 GB | completion + tools (no thinking) | ★★★ — thinking 없음, 정체성 일관, 한국어 자연. **36GB 비서 1순위** |
| `llama3.3:70b` | Q4_K_M | 42 GB | completion + tools | ✗ KittyPaw instruction following 실패 (실측 2026-05-05 — markdown/Go code block 직접 출력 → JS sandbox 3 retry SyntaxError → empty fallback). 36GB UMS swap는 가능 (Apple Metal) but 형식 instruction 무시. § 5.1.4 |

### 2.2 매트릭스 (raw)

| Model | max_tokens | time | finish_reason | completion_tokens | content_len | preview |
|---|---|---|---|---|---|---|
| gemma4:latest | 256 | 4.73s | stop | 27 | 45 | "안녕하세요! 궁금한 점에 대해 깊이 파고들어 답을 찾아드리는 지식 탐험가예요. 💡" |
| gemma4:latest | 1024 | 0.82s ⚠ (캐시) | stop | 21 | 39 | "저는 궁금증을 해결해 드리고 도움을 드리는 대규모 언어 모델이에요. 😊" |
| gemma4:latest | 2048 | 9.00s | stop | 332 | 59 | (multi-section formatted reply) |
| qwen3:latest 8B | 256 | 9.10s | stop | 175 | 102 | "안녕하세요, 저는 톤이 랩에서 개발한 대규모 언어 모델인 Qwen입니다. 질문 답변, 글쓰기, 작업 도움 등" |
| qwen3:latest 8B | 1024 | 10.70s | stop | 275 | 52 | "안녕하세요! 저는 Qwen입니다. 다양한 주제에 대해 도와드릴 수 있는 AI 어시스턴트입니다." |
| qwen3:latest 8B | 2048 | **36.67s** | stop | 943 | 56 | "안녕하세요, 저는 [이름]입니다. 25살 디자이너로 ..." (thinking 폭주 후 짧은 final) |
| qwen3:4b | 256 | 7.04s | **length** | 256 | **0** | (empty — thinking에 모두 소진) |
| qwen3:4b | 1024 | 22.44s | **length** | 1024 | **0** | (empty) |
| qwen3:4b | 2048 | 28.03s | stop | 1271 | 30 | "안녕하세요! 궁금한 점이 있으면 Qwen이 도와드려요." (1241 thinking + 30자 final) |
| qwen3:30b-a3b | 256 | 19.28s | **length** | 256 | **0** | (empty) |
| qwen3:30b-a3b | 1024 | 16.26s | stop | 795 | 51 | "안녕하세요! 저는 알리바바 Tongyi 실험실에서 개발한 초대규모 언어 모델 Qwen입니다." |
| qwen3:30b-a3b | 2048 | 31.95s | stop | 1550 | 44 | "안녕하세요, 저는 Qwen, 알리바바 클라우드에서 개발한 대형 언어 모델입니다." (1506 thinking + 44자 final) |
| **phi4-mini:latest** | 256 | 3.06s | stop | 78 | 142 | "물론이에요! 저는 Phi라는 이름의 디지털 매체의 대표이며, 최신 정보과 지식의 접근성을 보장하고..." |
| **phi4-mini:latest** | 1024 | 1.6s (warm) | stop | 70 | 144 | "좋아, 나는 Phi이고 Microsoft가 개발한 인공지능입니다..." |
| **phi4-mini:latest** | 2048 | 1.69s | stop | 73 | 141 | "물론이야! 저는 Phi, 대중에게 AI 지원하는 창의력을 가진 존재이죠..." |
| qwen3:14b | 256 | 20.54s | stop | 113 | 49 | "안녕하세요! 저는 질문에 답변하고, 글을 작성하며, 대화를 나누는 AI 어시스턴트입니다." |
| qwen3:14b | 1024 | **57.23s** | stop | **547** | 80 | "저는 알리바바 클라우드에서 개발한 대규모 언어 모델 Qwen입니다." (467 thinking + 80자 final) |
| qwen3:14b | 2048 | 9.02s (warm) | stop | 132 | 48 | "안녕하세요, 다양한 질문에 답하고 창의적인 작업을 도와드리는 AI 어시스턴트입니다! 😊" |
| hermes3:8b | 256 | 4.19s | stop | 32 | 50 | "친구들! 여기 당신의 새로운 친구, **에스파이어**입니다." |
| hermes3:8b | 1024 | 1.25s (warm) | stop | 30 | 48 | "친구들, 저는 **가교**라는 이름을 가지고 있고, 인공지능이여서 컴퓨터과학 공부도 좋아해요!" |
| hermes3:8b | 2048 | 0.83s | stop | 19 | 26 | "즐거운 세상에서 너에게 안부인 어린이 **토미**임다!" (자칭 이름이 매번 다름) |
| granite4.1:8b | 256 | 13.73s (cold) | stop | 66 | 77 | "안녕하세요! 저는 **ChatGPT**라는 인공지능 어시스턴트입니다." |
| granite4.1:8b | 1024 | 3.22s (warm) | stop | 73 | 79 | "안녕! 저는 **구글의 인공지능 어시스턴트인 Gemini**입니다." |
| granite4.1:8b | 2048 | 2.27s | stop | 53 | 61 | "안녕하세요! 저는 **구글의 AI 챗봇인 Granite**입니다." (호출마다 회사·정체성 다름) |
| mistral-nemo:12b-instruct-2407-q4_K_M | 256 | 2.41s | stop | 14 | 46 | "**hello, I'm a text-based AI** here to assist you!" (영어 응답) |
| mistral-nemo:12b-instruct-2407-q4_K_M | 1024 | 1.38s | stop | 21 | 51 | "반갑습니다! 저는 코드명으로, **Ваш помощник**(Your Assistant)입니다." (러시아어 mixin) |
| mistral-nemo:12b-instruct-2407-q4_K_M | 2048 | 0.96s | stop | 14 | 24 | "친절한 오픈 소스를 지향하는 AI입니다." (정상 한국어 but 짧음) |
| **qwen2.5:32b-instruct** | 256 | 9.58s (cold) | stop | 32 | 51 | "안녕하세요, 저는 Qwen이라고 합니다, **알리바바클라우드에서 만든 인공지능 어시스턴트**입니다." |
| **qwen2.5:32b-instruct** | 1024 | 5.42s (warm) | stop | 35 | 45 | "안녕! 나는 **알리바바 클라우드에서 개발한** 도움주는 인공지능 챗봇인 **Qwen**이에요." |
| **qwen2.5:32b-instruct** | 2048 | 3.29s | stop | 21 | 32 | "안녕! 저는 Qwen이라는 이름의 인공지능 어시스턴트예요." (정체성 일관, thinking 없음) |

### 2.3 KittyPaw integration test

```bash
# 격리된 KITTYPAW_HOME 셋업 권장 (apps/kittypaw/CLAUDE.md `Testing Isolation` 섹션 참조)
OLLAMA_TEST_MODEL=gemma4:latest \
OLLAMA_BASE_URL=http://localhost:11500/v1/chat/completions \
  go test -tags ollama_integration -v -run TestOllamaLiveSmoke ./llm/
```

### 2.4 KittyPaw harness automated measure (`make dev-models-measure BACKEND=ollama`)

prompt: `안녕? 한 줄로 자기소개 해줘.` (§ 2.2 일관). 측정 2026-05-05. 시스템 프롬프트는 KittyPaw 비서 페르소나 + skill loop의 JS sandbox 형식 강제.

| Model | latency (KittyPaw) | 응답 preview | 비서 결과 |
|---|---|---|---|
| `gemma4:latest` 8B | 1s | "안녕하세요! 나는 KittyPaw입니다. 작업 자동화와 질문 응답을 도와드려요." | ★★★ 즉답 + 페르소나 일관 (§ 2.2 0.82s 캐시 일치) |
| `qwen3:latest` 8B | 1s | "안녕하세요! KittyPaw입니다. 어떤 작업을 도와드릴까요? 😊" | ★★★ 즉답 + 친근 |
| `phi4-mini:latest` 3.8B | <1s | "안녕하세요! KittyPaw입니다. 도와드릴 작업이 있으신가요? 🐱" | ★★★ **최고 즉답** — 16GB MBA 후보 |
| `granite4.1:8b` | 1s | "안녕하세요! KittyPaw입니다. 😊 혹시 궁금한 점이 있나요? 도와드릴 수 있어요!" | ★★★ **KittyPaw 시스템 프롬프트가 정체성 강제 통과** — § 5.1.2 raw ollama 정체성 hallucination을 KittyPaw harness가 system prompt로 가드. 부분 정정 fact |
| `qwen2.5:32b-instruct` Q4_K_M | 5s | "안녕하세요! 나는 KittyPaw입니다. 작업 자동화와 질문 답변을 도와요." + Go FizzBuzz | ★★★ § 2.2 warm 5.42s 일치 (재현) |
| `llama3.3:70b` Q4_K_M | 9s+8.6s (3 retry) | "응답이 비어 있어요. 질문을 다시 한 번 말씀해 주시겠어요?" (KittyPaw fallback) | **✗ instruction following 실패** — markdown/Go code block 직접 출력. § 5.1.4 |

핵심 fact (Round 측정에서 새로 발견):

1. **KittyPaw 시스템 프롬프트가 작은 모델 정체성 hallucination을 강제 통과** — granite4.1:8b raw ollama (§ 5.1.2)에서 ChatGPT/Gemini/Granite 매번 자칭 다름이지만 KittyPaw harness (system prompt "KittyPaw 비서") 통해선 일관. KittyPaw 환경 한정 사용 가능 → ★★★.
2. **모델 크기 ≠ KittyPaw instruction following 품질** — 70B llama가 32B qwen2.5보다 약함 (§ 5.1.4 신규).

---

## 3. Verified Local Models — LM Studio MLX (eMac M3 Pro 36 GB · ssh tunnel `:11600 → emac:1234`)

prompt 동일.

### 3.1 결론 요약

| Model | Quant | size | Capabilities | 비서 결론 |
|---|---|---|---|---|
| **`qwen3-30b-a3b-instruct-2507`** (MLX) | 4bit | 17.2 GB | qwen3_moe + tools (no thinking) | ★★★ — **즉답성·정확성 통합 1순위** (warm 0.55-0.61s · 30B MoE) |

### 3.2 매트릭스 (raw)

| Model | max_tokens | time | finish_reason | completion_tokens | content_len | preview |
|---|---|---|---|---|---|---|
| qwen3-30b-a3b-instruct-2507 (MLX) | (cold load) | **30.57s** | stop | — | 50 | "안녕하세요! 저는 도움을 주는 지능형 어시스턴트예요. 항상 최선을 다해 도와드릴게요! 😊" (KittyPaw integration test) |
| qwen3-30b-a3b-instruct-2507 (MLX) | 256 | **0.61s** (warm) | stop | 25 | 38 | "안녕하세요! 저는 도움을 드리는 능력 있는 AI 어시스턴트입니다. 😊" |
| qwen3-30b-a3b-instruct-2507 (MLX) | 1024 | **0.59s** (warm) | stop | 24 | 34 | "안녕하세요! 저는 도와드릴 수 있는 지능형 어시스턴트예요. 😊" |
| qwen3-30b-a3b-instruct-2507 (MLX) | 2048 | **0.55s** (warm) | stop | 23 | 31 | "안녕하세요! 저는 도움을 주는 지능형 어시스턴트예요. 😊" |

### 3.3 발견 (load-bearing)

- **Instruct variant는 thinking 0** — finish=stop, completion_tokens 23-25 (final 그대로). § 2.2 ollama qwen3:30b-a3b (thinking variant)가 882~1506 thinking tokens 쓰던 것과 대비.
- **MLX 4bit + Apple Metal = 즉답** — warm 0.55-0.61s. ollama qwen2.5:32b-instruct (GGUF Q4_K_M, warm 3-9s) 대비 5-15× 빠름. 같은 30B 클래스 비교에서 MLX가 명확하게 우세.
- **Cold load 30.57s, 그 후 idle unload 까지 warm** — LM Studio 서버가 요청 없을 때 자동 unload. KittyPaw 비서 첫 메시지는 30s 대기 가능, 이후 즉답.
- **정체성 일관** — 3 호출 모두 "AI 어시스턴트"로 자칭. § 5.1.2 granite4.1 hallucination 같은 문제 없음.

### 3.4 다운로드 (lms daemon stall 우회 — § 5.2)

```bash
# lms get은 lmstudio daemon stall 결함 (재현 2회). hf direct 사용:
hf download mlx-community/Qwen3-30B-A3B-Instruct-2507-4bit \
  --local-dir ~/.lmstudio/models/mlx-community/Qwen3-30B-A3B-Instruct-2507-4bit
# 17.2 GB · 49 분 · 평균 22 MB/s (eMac 측 실측)

lms server start --port 1234     # 모델 인덱싱은 자동
lms ls                            # qwen3-30b-a3b-instruct-2507 30B qwen3_moe 17.20 GB Local
```

### 3.5 KittyPaw integration test

```bash
# Option A — go test with ollama integration tag (base_url override만 바꿔 LMS로 재사용)
ssh -fN -L 11600:localhost:1234 emac
OLLAMA_TEST_MODEL=qwen3-30b-a3b-instruct-2507 \
OLLAMA_BASE_URL=http://localhost:11600/v1/chat/completions \
  go test -tags ollama_integration -v -run TestOllamaLiveSmoke ./llm/

# Option B — KittyPaw harness automated measure (recommended)
make dev-models-tunnel-lms
make dev-models-measure BACKEND=lmstudio MODEL=qwen3-30b-a3b-instruct-2507
```

LM Studio는 OpenAI Chat Completions 호환 endpoint 노출. KittyPaw 본 phase에서 `provider="lmstudio"` 신규 case 추가 (`llm/registry.go`, `lmstudioDefaultBaseURL = "http://localhost:11600/v1/chat/completions"`) — `provider="openai" + base_url` 우회보다 telemetry/log clarity 우위 (mistral case 일관 mental model). `make dev-models-measure BACKEND=lmstudio` 는 § 3.6 KittyPaw harness 측정 흐름을 자동화한다.

### 3.6 KittyPaw harness automated measure (`make dev-models-measure BACKEND=lmstudio`)

prompt: `안녕? 한 줄로 자기소개 해줘.` (§ 2.4 일관). 시스템 프롬프트는 KittyPaw 비서 페르소나 + skill loop의 JS sandbox 형식 강제 (§ 2.4와 동일 구조).

| Model | latency (KittyPaw) | 응답 preview | 비서 결과 |
|---|---|---|---|
| `qwen3-30b-a3b-instruct-2507` (MLX 4bit) | _(placeholder — 사용자 측정 후 박힘)_ | _(cold/warm 2회 + 5점 척도)_ | _(KittyPaw harness 재현 → § 3 raw 측정과 대조)_ |

박제 가이드: **cold** = 첫 호출 (모델 GUI에 load 되어 있어도 LM Studio 자체의 idle-unload 가능 — § 3.3 fact), **warm** = 두 번째 호출. quality 5점 척도는 § 2.4와 일관 (한국어 자연스러움 + 페르소나 일관 + 코드 정확도). § 3 raw 측정 (`curl /v1/chat/completions` 직접) 대비 KittyPaw harness 측정은 system prompt + memory + history 누적 영향 포함 — § 8.5 ecological-valid 측정 가이드 일관.

§ 5.1.4 가설 (cloud full precision vs ollama Q4 quantization vs LM Studio MLX) 3-wire 대조에 본 row가 MLX wire 데이터를 박는다.

---

## 4. Verified Cloud Free Tier

prompt 동일.

| Provider | Model | context_window (실측 § 6.4) | Latency (curl) | Latency (KittyPaw) | Korean sample (max_tok=256) | KittyPaw 통합 함정 |
|---|---|---|---|---|---|---|
| **Cerebras** (free) | `qwen-3-235b-a22b-instruct-2507` | **8192** ❌ KittyPaw chat 디폴트 불가 (§ 5.3) | **0.056s** | (별도 세션 측정 — § 5.3) | "안녕! 나는 대화를 통해 도움을 주는 친절한 인공지능이야." | **8K cap = KittyPaw chat 부적합** — paid tier 또는 보조 phase 한정 |
| **Groq** (free) | `llama-3.3-70b-versatile` | **131072 (128K)** ✅ | 0.07s | 0.41s | "안녕하세요, 저는 지식과 TASK들을 수행하는 AIです." (일본어 mixin) | parallel tool 지원, 한국어 어색 |
| **Groq** (free) | `qwen/qwen3-32b` | **131072 (128K)** ✅ | 0.7-0.95s (warm) | (미측정) | **thinking variant** — default content에 `<think>` leak. `reasoning_format=parsed` 옵션 시 클린: "안녕하세요, 저는 대화하고 질문에 답하는 AI 보조자입니다!" | **Groq 비표준 옵션 `reasoning_format=parsed/hidden` 필수** (§ 5.13) — 송신 시 cleansed. console.groq.com/settings/limits에서 enable 필요 (org-gated). `chat_template_kwargs` 불지원 |
| **Groq** (free) | `openai/gpt-oss-120b` | **131072 (128K)** ✅ | — | — | (미측정) | parallel tool 미지원 (§ 5.4) |
| **Groq** (free) | `llama-3.1-8b-instant` | **131072 (128K)** ✅ | — | — | (미측정) | 작은 모델, fallback 후보 |
| **Mistral** (free Experiment) | `mistral-large-latest` | **262144 (256K)** ✅ | 1.50s | (미측정) | "**호기심 많은 개발자**이자 글 쓰는 걸 좋아하는, 끊임없이 배우는 사람입니다!" | **정체성 페르소나 SFT** (3회 재현 — § 5.11). 비서 첫 turn UX 부적합 |
| **Mistral** (free) | `mistral-medium-latest` | **131072 (128K)** ✅ | 1.08-1.30s | (미측정) | "안녕! 🌟 코딩과 글쓰기를 사랑하는 AI 도우미야 — 너의 궁금증을 재밌게 풀어주고…" | **★★★ AI 자칭 일관**, 한국어 자연. 무료 cloud 후보 강함 |
| **Mistral** (free) | `mistral-small-latest` | **262144 (256K)** ✅ | 0.95-2.24s | (미측정) | (max_tok=256) "안녕! 저는 AI 챗봇이에요. 😊" / (max_tok=1024+) "**호기심 많은 개발자**이자, 새로운 도전을 즐기는 사람입니다." | max_tok 길수록 § 5.11 페르소나 노출. `reasoning_effort=none` (Adjustable, 별도 검증) |
| **Mistral** (free) | `magistral-medium-latest` | **131072 (128K)** | 1.0-4.0s | — | (content가 list of blocks 구조 — § 5.12) | **Native reasoning — disable 불가** (Mistral docs 명시). 비서 디폴트 부적합 |
| **Mistral** (free) | `ministral-8b-latest` | **262144 (256K)** ✅ | 1.0s | — | "안녕! AI 도우미로, 다양한 분야에서 효율적인 답변과 창의적인 아이디어를 제공하는 'AI 친구'야!" | ★★ AI 자칭, 한국어 자연. 256K context |
| **Mistral** (free) | `pixtral-large-latest` | **131072 (128K)** | 1.44s | — | "안녕! 나는 AI 도우미야. 다양한 질문에 답해주고 도움이 되고자 노력하고 있어." | ★★ vision 가능 |
| **Gemini** (free) | `gemini-2.5-flash-lite` | 1M (input) / 65K (output) | 5.14s | — | "안녕하세요! 저는 당신의 질문에 답하고 정보를 제공하기 위해 훈련된 대규모 언어 모델입니다." (51자 정상) | finish=stop. 한국어 자연. **무료 한도 docs 미명시 — dashboard 확인** |
| **Gemini** (free) | `gemini-3-flash-preview` | 1M (input) | 2.80s | — | "안녕하세요! 저는 당신의 궁금증을 해결하고…" (max_tok=256은 잘림 — output 65K 풀이 필요) | output 짧으면 잘림. 무료 quota 빡빡 (다른 호출 동시 측정 시 429) |
| **Gemini** (free) | `gemini-3.1-pro-preview` / `gemini-3-pro-preview` / `gemini-2.5-pro` | 1M (input) | — | — | (429 quota) | 무료 한도 매우 작음 |
| OpenRouter (paid) | (model별 `:free` 접미사) | 모델별 | — | — | — | docs 명시: `:free` 모델 **20 RPM / 200 RPD** · <$10 credit → 50 RPD · ≥$10 credit → 1K RPD. provider routing 변동 — production 비추 |
| DeepSeek | `deepseek-v4-flash` / `deepseek-v4-pro` | (catalog) | — | — | — | tool_call이 content 평문으로 새는 버그 ~11% (§ 5.5). **5M tokens/30일 grant docs 미명시** — dashboard 확인 |
| Cohere Trial | `command-r` / `command-r7b` | (catalog) | — | — | — | docs 명시: chat **20 RPM**, **1,000 calls/월**. non-commercial 제한 (별도 ToS) |

### 4.1 Cerebras 무료 한도 (2026-05-05 docs)

- 출처: https://inference-docs.cerebras.ai/support/rate-limits
- 모델별 RPM 10–30 / RPD 100–14,400 / TPM 60K–64K / TPD 1M
- llama-3.1-8b 예시: RPM 30 · RPD 14,400 · TPM 60K · TPD 1M
- **8K context cap (free)**: docs 본문 미명시 — KittyPaw 자체 측정으로 확정 (§ 5.3, median 8001 / 52% 8K 초과)
- **카드 필요 여부**: docs 미명시 — 가입 시 dashboard 확인
- OpenAI 호환: ✅ `https://api.cerebras.ai/v1`

### 4.2 Groq 무료 한도 (2026-05-05 docs)

- 출처: https://console.groq.com/docs/rate-limits
- 모델별 (RPM / RPD / TPM / TPD):
  - `llama-3.3-70b-versatile`: 30 / 1K / 12K / 100K
  - `llama-3.1-8b-instant`: 30 / 14.4K / 6K / 500K
  - `qwen/qwen3-32b`: **60 / 1K / 6K / 500K** (**org-gated** — console.groq.com/settings/limits Allow/Block에서 enable + Save 필수)
  - `openai/gpt-oss-120b` / `openai/gpt-oss-20b`: 30 / 1K / 8K / 200K
  - `meta-llama/llama-4-scout-17b-16e-instruct`: 30 / 1K / 30K / 500K
- parallel tool 지원: Llama 3.3 70B / 3.1 8B / Llama 4 Scout / Qwen3 32B
- 미지원: `openai/gpt-oss-*` (§ 5.4)
- OpenAI 호환: ✅

### 4.3 Mistral La Plateforme — Experiment plan (2026-05-05 docs)

- 출처: https://docs.mistral.ai/deployment/ai-studio/tier · https://help.mistral.ai/en/articles/455206-...
- Plan 이름: **Experiment** ("evaluation and prototyping only")
- **카드 ❌, 전화번호 인증 필수** (1 phone = 1 plan)
- **학습 사용 명시**: "API requests made under the Experiment plan **may be used to train Mistral's models**" — 사용자 동의 토글 권장
- **Experiment의 RPS / RPM / TPM / 월 한도 정확 숫자**: docs **미명시** — Admin panel Limits 페이지에서 확인 (사용자 dashboard)
- 모델 access (실측 — 우리 키 통과): mistral-large/medium/small/ministral-8b/pixtral-large/magistral 모두 동작
- OpenAI 호환: ✅ `https://api.mistral.ai/v1`

### 4.4 Gemini API 무료 (2026-05-05 docs)

- 출처: https://ai.google.dev/gemini-api/docs/rate-limits
- **모델별 정확 한도 docs 본문에 미게시** — `aistudio.google.com/rate-limit` (사용자 dashboard) redirect
- 실측 시점 통과: gemini-2.5-flash-lite (51자 정상), gemini-3-flash-preview / gemini-2.5-flash (max_tok=256은 잘림)
- 실측 quota 빡빡: gemini-3.1-pro-preview / gemini-3-pro-preview / gemini-2.5-pro / gemini-3.1-flash-lite-preview는 다중 호출 시 429
- **학습 사용 여부**: rate-limits 페이지 미명시 — 별도 ToS 확인 권장
- OpenAI 호환: ✅ 별도 endpoint (`/openai`) 존재

### 4.5 Together AI (2026-05-05 docs)

- 출처: https://docs.together.ai/docs/billing-credits · https://www.together.ai/pricing
- **무료 trial 없음, $5 minimum credit purchase 필요** (docs 명시) — 카드 등록 필수
- "Llama-3.3-70B Free" 무료 endpoint 정확 RPM/RPD: docs 미명시
- Rate limit: "dynamic per-model rate limits that scale with sustained traffic"
- KittyPaw 추천 부적합 — **카드 등록 의향 사용자 한정**
- OpenAI 호환: ✅ (별도 page)

### 4.6 OpenRouter 무료 (2026-05-05 docs)

- 출처: https://openrouter.ai/docs/faq · https://openrouter.ai/docs/guides/routing/model-variants/free
- `:free` 접미사 모델: **20 RPM / 200 RPD**
- 무료 모델 RPD: **<$10 credit → 50 RPD**, **≥$10 credit → 1,000 RPD** (영구)
- provider routing 변동 (어떤 backend로 라우팅되는지 가변) — production 비추, 다양화 후보로만
- **실측 통과** (KittyPaw harness 2026-05-05, `meta-llama/llama-3.3-70b-instruct:free`): 1s, 한국어 자연 + KittyPaw 페르소나 + JS sandbox 통과. 카드 ❌, 발급 즉시 사용 가능. dev-models 6번째 entry로 박힘.
- OpenAI 호환: ✅

### 4.7 DeepSeek (2026-05-05 docs)

- 출처: https://api-docs.deepseek.com/quick_start/pricing
- 현 모델: **deepseek-v4-flash / deepseek-v4-pro** (1M context — catalog)
- **deepseek-chat / deepseek-reasoner는 2026-07-24 15:59 UTC retire** 명시
- **5M tokens / 30일 신규 grant 사실 docs 미명시** — third-party 인용. 사용자 dashboard 확인 필요
- OpenAI 호환: ✅ `https://api.deepseek.com` + Anthropic 호환 `/anthropic`
- 함정: tool_call이 content 평문으로 새는 버그 ~11% (§ 5.5)

### 4.8 Cohere Trial (2026-05-05 docs)

- 출처: https://docs.cohere.com/docs/rate-limits
- Trial chat: **20 RPM**, **1,000 calls/월**
- Embed 2K inputs/min, Rerank 10 RPM
- non-commercial 제한 (별도 ToS — rate-limits docs 본문엔 미명시)
- KittyPaw OpenAI 호환 endpoint: 별도 compatibility page 확인 필요

### 4.9 무료 한도 docs 격차 (load-bearing fact)

비판적 발견 (2026-05-05 8개 provider docs 검증):

| 정량 한도 docs 박제 ✅ | dashboard 분산 (정량 미명시) ⚠ |
|---|---|
| Groq · Cerebras · OpenRouter · Cohere | Gemini · Mistral · Together · DeepSeek |

→ 추천 docs에 무료 한도 인용 시 출처 표시 필수. dashboard 분산 provider는 "본인 dashboard 확인" 안내 함께. KittyPaw 회귀 monitor에 docs 페이지 변경 감지 hook 두면 fact rot 방지 가능.

---

## 5. Known Pitfalls (★★★ 핵심)

### 5.1 qwen3 ollama default = thinking variant **전 시리즈**

- 4b / latest(8b) / **14b** / 30b-a3b **모두** thinking 토큰 출력 (2026-05-04 측정 4종 모두 확인)
- 크기와 thinking 양은 비례하지 않음:

  | size | thinking tokens | final chars | max_tok=1024 latency |
  |---|---|---|---|
  | 4B | 1241 | 30 | (length 도달, final 0) |
  | 8B | 175 | 102 | 10.7s |
  | 14B | 467 | 80 | **57.2s** |
  | 30B-A3B | 882~1506 | 28~51 | 16.3s |

- **결론**: KittyPaw 비서 즉답성 부적합. **instruct variant** 또는 **LM Studio MLX 4bit Qwen3-30B-A3B-Instruct-2507**가 답.
- `instruct-2507-q4_K_M` 같은 ollama 라이브러리 태그는 페이지엔 보이지만 manifest fail (ollama 0.20~0.23 모두) — ollama org 정식 push 안 한 상태.

### 5.1.1 Hermes-3 한국어 일반 chat 부적합 (★ 새 발견)

- `hermes3:8b`는 Berkeley FC 91% 영어 function calling 강점. 그러나 한국어 자기소개 prompt에서 **매 호출마다 다른 가공된 이름**(에스파이어 / 가교 / 토미) 자칭 — agent persona/RP 학습이 일반 chat에 새어나옴.
- 한국어 "비서" 용도엔 비추, **영어 function calling 전용**으로만 권장.

### 5.1.2 IBM Granite 4.1 8B 정체성 hallucination (★ 마케팅 ≠ 실측)

- IBM 공식 카탈로그/Ollama 페이지가 "한국어 정식 지원 + tool 강력 + 엔터프라이즈 톤"으로 표기.
- **그러나 실측 (한국어 자기소개 prompt 3 호출)**:
  - 호출 1: "**ChatGPT**라는 인공지능 어시스턴트" (OpenAI 자칭)
  - 호출 2: "**구글의 인공지능 어시스턴트인 Gemini**" (Google 자칭)
  - 호출 3: "**구글의 AI 챗봇인 Granite**" (Google + Granite 혼합)
- 즉 **모델이 자기 회사·이름을 매번 다르게 hallucinate**. 비서 첫 turn UX 무너짐.
- **결론**: system prompt에 정체성 강제 시 ★★, 그렇지 않으면 ★. 외부 카탈로그/마케팅에 의존 X — 측정으로만 판단.

### 5.1.3 Mistral-Nemo 한국어 안정성 부족 — Tekken 토큰 효율 ≠ 한국어 품질

- Mistral-Nemo "Tekken 토크나이저로 한국어 효율 좋다"라는 평판이 광범위 인용됨.
- **실측 (한국어 자기소개 prompt 3 호출)**:
  - 호출 1: 응답 전부 **영어** ("hello, I'm a text-based AI here to assist you!")
  - 호출 2: 한국어 + **러시아어 mixin** ("반갑습니다! 저는 코드명으로, **Ваш помощник**...")
  - 호출 3: 정상 한국어 (단 24자, 매우 짧음)
- **Tekken은 토큰 효율(글자/토큰 비율)이지 한국어 품질 보증 아님**. 비서 비추 — 영어 코드/툴 보조 용도라면 별도.
- 두 번째 "마케팅 ≠ 실측" 사례 (granite4.1 hallucination에 이은).

### 5.1.4 모델 크기 ≠ KittyPaw instruction following 품질 (실측 2026-05-05)

`llama3.3:70b` Q4_K_M (42 GB) vs `qwen2.5:32b-instruct` Q4_K_M (19 GB) — 70B 모델이 32B 모델보다 KittyPaw skill loop의 JS sandbox 형식 instruction following ✗.

`make dev-models-measure BACKEND=ollama MODEL=llama3.3:70b` 결과:
- attempt 0: 자연어 + Go code block (markdown) → SyntaxError (8 errors)
- attempt 1: ` ```go ... ``` ` markdown → ILLEGAL token (2 errors)
- attempt 2: code_len=0 (LLM이 retry budget 소진 후 빈 응답)
- 총 8.6s, retry 3회 한도 도달 후 KittyPaw fallback "응답이 비어 있어요"

대조: `qwen2.5:32b-instruct` 5s + JS sandbox 통과 + 정체성 일관 응답.

추정: Llama 3.3 70B는 자연어 응답 RLHF가 강해서 KittyPaw의 strict "JS code only" 시스템 프롬프트 형식을 무시 + markdown/code block 직접 출력. 모델 크기 ↑ ≠ KittyPaw 호환 ↑. **KittyPaw 비서 부적합 — qwen2.5:32b-instruct 또는 작은 8B 모델 (gemma4/qwen3/phi4-mini/granite4.1)이 정공**.

Apple Metal Unified Memory 효과: 36GB UMS에서 42 GB 모델이 swap heavy 예상이지만 9s 응답 도착 = swap 동작 가능. 다만 응답 quality와 instruction following이 swap 영향 X (raw ollama 형식 차이). § 2.4 매트릭스 row 박제.

**대조 fact (cloud llama3.3:70b vs ollama Q4)**: 같은 모델이 cloud (Groq § 4.2 + OpenRouter `:free` § 4.6)에서는 ★★ 한국어 자연 + KittyPaw harness JS sandbox **통과** (1s warm), ollama Q4_K_M (42 GB)에서는 **✗** instruction following 실패. 가능 원인:
1. **Q4_K_M quantization이 instruction following 품질 떨어뜨림** — cloud는 full precision/bf16, ollama는 Q4. 큰 모델일수록 quantization 영향 ↑ 추정.
2. ollama serve의 시스템 프롬프트 처리 차이 (cloud OpenAI 호환 wire와 다른 message 포맷팅)
3. emac UMS swap 중 partial state로 inference (작은 응답이 swap state로 incomplete)

3개 가능성 모두 측정 가치 있음. 다만 KittyPaw 비서로 llama3.3:70b 사용 = **cloud (Groq/OpenRouter) 권장**, ollama Q4 비추.

### 5.2 LM Studio 다운로드 daemon stall — `Qwen3-30B-A3B-Instruct-2507-MLX-4bit` ✅ resolved via `hf download`

- `lms get https://huggingface.co/mlx-community/Qwen3-30B-A3B-Instruct-2507-4bit --mlx --yes` 두 차례 stall:
  - 첫 시도: 14.24% (2.45 GB / 17.20 GB)에서 1h 15m+ 무진전
  - 재시도: 0.00%에서 17m+ (lms get kill 후 fresh 시작도 동일)
  - lsof: lms get → lmstudio daemon (`llmster` :41343) 로컬 연결만, **외부 HF HTTPS 연결 없음**
- **우회 (실측 성공)**:
  ```bash
  hf download mlx-community/Qwen3-30B-A3B-Instruct-2507-4bit \
    --local-dir ~/.lmstudio/models/mlx-community/Qwen3-30B-A3B-Instruct-2507-4bit
  # 17.2 GB · 49 분 · 평균 22 MB/s
  ```
  완료 후 `lms server start --port 1234` 시 자동 인덱싱. § 3 매트릭스 측정 성공.
- **참고**: `huggingface-cli` (deprecated) 대신 `hf download` 사용. uv 환경에선 `uvx --from huggingface_hub huggingface-cli ...`도 가능하지만 deprecated 경고 출력.

### 5.3 Cerebras free 8K context cap — KittyPaw chat 디폴트 절대 불가능 (실측)

별도 KittyPaw 측정 세션 (2026-05-04, jinto 운영 계정 + 우리 측정 hook으로 `slog "prompt built"` 에 `est_tokens` / `budget` / `window` 박음) 결과:

| 통계 | 값 |
|---|---|
| 측정 호출 수 | 27 (secretary_smoke eval 풀 run, judge LLM 별도) |
| min | 6758 |
| **median** | **8001** |
| max | 9919 |
| mean | 8236 |
| **8K 초과 비율** | **52%** (14/27) |
| 7.5K 초과 비율 | 70% (19/27) |
| 6K 초과 비율 | 100% (27/27) |

**의미**: KittyPaw의 system prompt(11 block) + MCP tools 정의 + profile/memory/observation + recent_window=20 history → 압축 후에도 평균 8.2K. **이미 압축된 결과가 8K 이상이라 압축 미들웨어로 못 살림**.

추가 발견 (stateful daemon 함정): 단발 chat "안녕" 한 번에 `est_tokens=7319` (`message_count=30` history). **single-turn 요청도 stateful daemon은 누적 history 보냄** — 사용자 인터랙션의 single-turn ≠ LLM 입력의 single-turn. 같은 daemon에서 6번 chat 동안 7319 → 7507 단조 증가 관찰됨.

**결론**:
- Cerebras provider 코드 (`feat(llm): add cerebras provider` 5.x)는 유지
- **free tier 8K cap = KittyPaw chat 메인 디폴트 사용 금지**
- paid tier 또는 보조 phase 한정 (mediate/summary 같은 short-context 단발 호출, MoA candidate 등)
- 대안: § 4 의 16K+ free tier provider (Groq qwen3-32b 128K [실측], Gemini 등)

### 5.4 Groq gpt-oss-* parallel tool 미지원

- KittyPaw OpenAI provider에서 multi-tool 시 sequential 직렬화 폴백 필요 (현재 미구현 — Phase 1 plan OOS)

### 5.5 DeepSeek V4 tool_call content 평문 누수 ~11%

- content fallback 정규식 파서 필요 (Phase 1 plan OOS)

### 5.6 Ollama 0.20~0.23 일부 manifest 미등록

- `qwen3:30b-a3b-instruct-2507-q4` / `qwen3:30b-a3b-instruct-2507-q8` 등
- `qwen3:30b-a3b` (thinking) 또는 LM Studio HF 직접 다운로드로 우회 (§ 5.2)

### 5.7 LM Studio CLI search 미구현

- `lms search` unknown command. **GUI Discover** 또는 **HF URL 직접** (`hf download <repo> --local-dir ~/.lmstudio/models/<repo>`)

### 5.8 `/no_think` 트리거 OpenAI compat에서 효과 없음

- `qwen3:30b-a3b`에서 user/system 프롬프트로 시도 — chat_template_kwargs 통과 안 됨
- ollama native `/api/chat` `think:false` 옵션도 ollama 0.21에서 효과 X
- **근본 해법**: instruct variant (예: § 3 MLX Qwen3-30B-A3B-Instruct-2507) 쓰면 thinking 토큰 자체가 없음.

### 5.9 macOS BSD `timeout` 명령 부재

- `coreutils` brew 미설치 환경에서 `timeout` 호출 fail
- Background `nohup &` + `sleep N` + `kill -0` 패턴으로 우회 (이 도구 환경 박제)

### 5.10 Qwen3-Coder family tool calling — 외부 production framework 실패 6+ 보고 (load-bearing 회귀 권장)

R3 production agent framework 채택 조사(2026-05-05)에서 발견 — Qwen3-Coder 30B A3B family는 multiple production framework에서 일관되게 tool calling 실패:

- [RooCode #10780](https://github.com/RooCodeInc/Roo-Code/issues/10780) — llama.cpp tool calls fail
- [Continue #6913](https://github.com/continuedev/continue/issues/6913) — Continue가 Qwen3 Coder 30B tool 인식 못함
- [OpenCode #1809](https://github.com/anomalyco/opencode/issues/1809) — qwen3-coder-30B-A3B tool any 못함
- [LM Studio bug #825](https://github.com/lmstudio-ai/lmstudio-bug-tracker/issues/825) — OpenAI-compat API tool format 비호환 (Cline/Continue/OpenCode/Crush 모두)
- [qwen-code #176](https://github.com/QwenLM/qwen-code/issues/176) — ollama 30b-a3b tool calling 실패
- [Qwen3-Coder #475](https://github.com/QwenLM/Qwen3-Coder/issues/475) — FP8 quant `<tool_call>` 태그 누락
- 별도: [Deveraux-Parker/Qwen3-Coder-30B-A3B-Monkey-Wrenches](https://github.com/Deveraux-Parker/Qwen3-Coder-30B-A3B-Monkey-Wrenches) — "100% 신뢰도 목표" 패치 repo

**KittyPaw 자체 측정 (qwen3:30b-a3b thinking variant)**: 자기소개 단순 prompt 통과. **그러나 multi-step tool call 시나리오는 본 phase에서 미측정** — chat template/parser 행운 가능성 배제 못함.

**권장**: KittyPaw 회귀 테스트 패키지에 multi-step tool call 시나리오(`Calc.add → Search.go → Memory.write` 같은 chain) 추가. build-tag `tool_chain_integration` 또는 `ollama_integration` 확장. v1.7 별도 phase 후보.

### 5.11 Mistral family 정체성 페르소나 SFT — Granite/Nemo와는 다른 패턴 (마케팅 ≠ 실측 4번째 사례)

**측정 (2026-05-05, mistral-large-latest 3회 재현)**:
- 호출 1: "**호기심 많은 개발자**이자, 책과 커피를 사랑하는 평범한 사람입니다!"
- 호출 2: "**호기심 많은 개발자**이자, 책과 커피를 사랑하는 사람입니다!"
- 호출 3: "**호기심 많은 개발자**이자, 책과 커피를 사랑하는 평범한 사람입니다!"

→ **변수 약함, 페르소나 일관**. § 5.1.2 IBM Granite (호출마다 ChatGPT/Gemini/Granite 회사명이 다른 unstable hallucination)와는 **다른 함정**:

| 함정 패턴 | 예 | 처리 |
|---|---|---|
| **Unstable hallucination** | granite4.1:8b — 호출마다 다른 회사 자칭 | system prompt로 안정화 가능 (페르소나 강제 주입) |
| **Stable persona SFT** | mistral-large-latest — "호기심 많은 개발자" 일관 | SFT가 강해서 system prompt도 뚫고 나올 risk (별도 측정 필요) |

**mistral-small-latest의 경계 동작** — `max_tok=256`은 정상 ("AI 챗봇이에요"), `max_tok=1024+`는 페르소나 노출 ("호기심 많은 개발자이자 새로운 도전을 즐기는 사람"). 즉 **응답 길이가 페르소나 가시성을 결정**.

**결론**:
- ★★★ 1순위 KittyPaw 비서 후보: **mistral-medium-latest** (3회 측정 모두 "AI 도우미" 자칭 일관, 한국어 자연)
- ★ mistral-large-latest / mistral-small-latest (max_tok 1024+): "개발자" 페르소나 SFT — 일반 비서 부적합 (단 개발자 도구라면 fit)
- 카탈로그·벤치마크 인용은 약 — 우리 측정 fact가 권위. "마케팅 ≠ 실측" 4번째 사례 (granite4.1, mistral-nemo, Groq qwen3-32b context 16K→128K, Mistral large 페르소나).

### 5.12 Mistral magistral은 "Native reasoning" — disable 불가 (KittyPaw 비서 디폴트 부적합)

**Mistral docs 1차 fact** (https://docs.mistral.ai/capabilities/reasoning/, 2026-05-05):

> Mistral offers two approaches to reasoning:
> - **Adjustable** — `mistral-small-latest` 와 `mistral-medium-3-5` 에 `reasoning_effort` 파라미터 (`"high"` / `"none"`)
> - **Native** — `magistral-small-latest` 와 `magistral-medium-latest`. **항상 reasoning traces 생성, purpose-built for deep reasoning**.

**KittyPaw 측정 (magistral-medium-latest, 2026-05-05)**:
- content가 string이 아닌 **list of blocks** 구조: `[{"type": "thinking", "thinking": [{"type": "text", "text": "Okay, the user wants me to introduce myself..."}]}, ...]`
- KittyPaw OpenAI 어댑터가 `content`를 string으로 가정하면 깨짐 — **별도 파서 필요** (§ 6.7)

**시도 — thinking off (모두 fail)**:

| 시도 | 결과 |
|---|---|
| `reasoning_format=hidden/parsed` (Groq 전용 옵션) | Mistral 호환 X |
| `chat_template_kwargs.enable_thinking=false` | Mistral 호환 X |
| user prompt에 `/no_think` (Qwen3 패턴) | 무효 — list 구조 그대로 |

**결론**: magistral은 KittyPaw 비서 디폴트 부적합. 대안:
- mistral-medium-latest 또는 ministral-8b-latest (Native reasoning 없음)
- 또는 mistral-small-latest / mistral-medium-3-5에 `reasoning_effort=none` (별도 측정 — 본 phase 미검증)

### 5.13 Groq qwen3-32b는 thinking variant — `reasoning_format=parsed/hidden` 우회 가능 (Groq 전용 비표준 옵션)

**측정 (2026-05-05)** — Groq qwen/qwen3-32b는 모델명에 `instruct` 명시 없는 **thinking variant**. default 응답에 `<think>` 토큰이 content에 그대로 노출 — KittyPaw 비서 UX 망가짐 위험.

**시도 결과**:

| 시도 | 결과 |
|---|---|
| default (no option) | ❌ content에 `<think>...` thinking 텍스트 그대로 노출 |
| `chat_template_kwargs.enable_thinking=false` | ❌ "property unsupported" |
| user prompt에 `/no_think` | ⚠ `<think> </think>` 빈 블록 + content 정상 (부분 우회) |
| **`reasoning_format=hidden`** | ✅ content 클린 ("안녕하세요! 저는 알리바바 클라우드에서 개발한 초대규모 언어 모델 Qwen3입니다…"), reasoning 필드 없음 |
| **`reasoning_format=parsed`** | ✅ content 클린, reasoning 별도 필드로 분리 — KittyPaw 측에서 reasoning 활용 가능 |

**load-bearing 결론**:
- **KittyPaw OpenAI 어댑터에 `reasoning_format=parsed` 옵션 송신 권장** (Groq 어댑터 분기). 이 옵션 미송신 시 thinking leak — 비서 UX 망가짐.
- Groq의 비표준 옵션 (`reasoning_format`)은 OpenAI Chat Completions 표준 X — provider 분기 필요.
- 우회 가능 → § 1 Decision Matrix "Cloud free, Korean priority" 1순위 후보로 회복 (Cerebras qwen-3-235b의 8K cap 대안).

### 5.14 무료 한도 공식 docs 격차 — provider 별 정량 박제 vs dashboard 분산

§ 4.9 매트릭스 참조. KittyPaw § 4 박제 시 정량/미명시 라벨링 일관 적용. 사용자 추천 docs는 "본인 dashboard 확인" 안내 추가 권장.

---

## 6. KittyPaw Integration Notes

### 6.1 Provider 등록 (registry.go cases)

| provider name | base_url default | env var | context_window default |
|---|---|---|---|
| `anthropic` / `claude` | api.anthropic.com | `ANTHROPIC_API_KEY` | 200K (대부분 모델) |
| `openai` / `gpt` | api.openai.com (Responses API) | `OPENAI_API_KEY` | 128K |
| `gemini` / `google` | (Gemini API endpoint) | `GEMINI_API_KEY` | 모델별 |
| `ollama` | `http://localhost:11434/v1/chat/completions` | (없음) | 모델별 |
| `cerebras` | `https://api.cerebras.ai/v1/chat/completions` | `CEREBRAS_API_KEY` | **8192** (free cap) |
| `groq` | `https://api.groq.com/openai/v1/chat/completions` | `GROQ_API_KEY` | 모델별 |
| `deepseek` | `https://api.deepseek.com/v1/chat/completions` | `DEEPSEEK_API_KEY` | 모델별 |
| `openrouter` | `https://openrouter.ai/api/v1/chat/completions` | `OPENROUTER_API_KEY` | 모델별 |
| `mistral` | `https://api.mistral.ai/v1/chat/completions` | `MISTRAL_API_KEY` | 모델별 (대부분 128K~256K) |

**Provider identity 분리 원칙**: 같은 OpenAI Chat Completions wire를 공유하더라도 **provider 명시**로 등록 (`provider="mistral"` 등). 그래야 KittyPaw `envAPIKey`가 vendor별 ENV(`MISTRAL_API_KEY` 등)를 정확히 lookup. 우회로 `provider="openai" + base_url=...mistral.ai`를 쓰면 `OPENAI_API_KEY`를 가져와 키 충돌. **Gemini OpenAI-compat endpoint** (`/v1beta/openai/chat/completions`)는 현재 `provider="gemini"` 분기가 자체 wire(GeminiProvider)로 라우팅되므로 사용 시 `provider="openai" + base_url override` 우회만 가능 — 별도 phase에서 `gemini-openai` sub-mode 추가 검토.

### 6.2 max_tokens 권장값

| 모델군 | max_tokens 권장 | 이유 |
|---|---|---|
| Anthropic Claude | 4096 | 표준 |
| qwen3 thinking variants (ollama) | **2048+** | thinking 토큰이 카운팅됨 — 256/1024는 final 못 나올 위험 |
| qwen3 instruct variants (LM Studio MLX) | 1024 | thinking 없으면 1024 충분 |
| gemma4 | 256~1024 | thinking 없음, 짧음 |
| Llama 3.3 70B (cloud) | 1024 | 표준 |

### 6.3 SSH tunnel 패턴 (eMac → MBA)

```bash
ssh -fN -L 11500:localhost:11434 emac     # Ollama
ssh -fN -L 11600:localhost:1234 emac      # LM Studio (server start --port 1234 후)
```

KittyPaw config:
```toml
[llm]
provider = "ollama"
base_url = "http://localhost:11500/v1/chat/completions"
model = "gemma4:latest"
max_tokens = 1024
```

### 6.4 Provider context_window 실측 체크리스트 (load-bearing)

**문제**: docs/marketing/blog 정보는 자주 틀림. 별도 KittyPaw 측정 세션에서 plan 단계에 "Groq qwen3-32b는 16K"로 가정했다가 실측 시 **128K (131072)** — 6× 차이. 이 정도 오차는 provider 채택 의사결정 자체를 뒤집음 (8K cap 우려 → 우려 zero).

**규칙**: provider 추가/wire 시점에 **반드시** `/v1/models` API로 `context_window` 실측. 문서/blog/벤치마크 인용에 의존 X.

```bash
curl -s -H "Authorization: Bearer $KEY" https://api.groq.com/openai/v1/models \
  | jq -r '.data[] | "\(.id)\t\(.context_window)\tactive=\(.active)"'
```

| Provider | Models endpoint |
|---|---|
| OpenAI | `https://api.openai.com/v1/models` |
| Cerebras | `https://api.cerebras.ai/v1/models` |
| Groq | `https://api.groq.com/openai/v1/models` |
| DeepSeek | `https://api.deepseek.com/v1/models` |
| OpenRouter | `https://openrouter.ai/api/v1/models` |
| Ollama | `http://localhost:11434/api/tags` (`context_length` 필드는 `ollama show <model>`) |

**Groq 실측 (2026-05-04, KittyPaw 별도 세션)**:

| 모델 | 실측 context_window |
|---|---|
| `qwen/qwen3-32b` | **131072 (128K)** |
| `llama-3.3-70b-versatile` | 131072 |
| `openai/gpt-oss-120b` | 131072 |
| `llama-3.1-8b-instant` | 131072 |

→ Groq 무료 tier는 Cerebras와 달리 context cap 작게 안 줌. **8K cap 우려 zero**.

**KittyPaw 자체 prompt 분포** (§ 5.3 측정 결과로 박혀있음): median 8001 / max 9919. 16K 이상 cap이면 안전 마진 충분.

### 6.5 결정 기준은 측정 전에 박기 (자기 합리화 방지)

새 provider 후보를 평가할 때, 측정 **전에** 임계값을 명시:

```
모든 입력 < N_safe (cap 대비 2K 버퍼) → 디폴트 GO
N_safe ~ N_warn 분포 → fallback 라우팅 또는 폐기
N_warn 초과 발생 → 디폴트 폐기
```

측정값을 본 뒤 임계를 정하면 "이 정도면 괜찮을 듯"으로 자기 합리화 발생. KittyPaw § 5.3 Cerebras 측정에서 "7.5K 초과 발생 → 폐기"를 측정 전 박았기에 "52% 8K 초과" 결과를 즉각 판정 가능.

### 6.6 Groq thinking variant — `reasoning_format` 옵션 송신 (어댑터 분기 권장)

§ 5.13 fact: Groq의 `qwen/qwen3-32b`(및 다른 thinking-style 모델)는 default 응답에 `<think>` 토큰이 content에 그대로 노출. 비서 UX 망가짐. 우회는 Groq 비표준 옵션 `reasoning_format`:

```jsonc
// KittyPaw OpenAI 어댑터가 provider="groq" 또는 base_url=*.groq.com 인 경우
{
  "model": "qwen/qwen3-32b",
  "messages": [...],
  "reasoning_format": "parsed"   // "hidden"도 가능 — content만 클린, reasoning drop
}
```

- `parsed`: thinking을 별도 `reasoning` 필드로 분리 — KittyPaw 측에서 활용 가능 (debug log, MoA candidate evidence)
- `hidden`: thinking 완전 drop — content만

**구현 권장**: `OpenAIProvider`에 `WithGroqReasoningFormat(string)` option, `provider="groq"` 또는 base_url 호스트 매칭 시 자동 송신. OpenAI 표준 X이므로 다른 provider엔 송신 ❌.

### 6.7 Mistral content list of blocks — string assumption 깨짐 (어댑터 파서 권장)

§ 5.12 fact: Mistral `magistral-*` 모델(Native reasoning) 응답은 `content`가 string이 아니라 list of blocks:

```jsonc
{
  "choices": [{
    "message": {
      "role": "assistant",
      "content": [
        {"type": "thinking", "thinking": [{"type": "text", "text": "..."}]},
        {"type": "text", "text": "최종 답변"}   // 또는 별도 block
      ]
    }
  }]
}
```

KittyPaw `OpenAIProvider`가 `content`를 `string`으로 가정하면 `magistral` 응답 파싱 실패. 권장:
- `content`가 `[]any`이면 `type=="text"` block만 concat해서 final answer 추출
- `type=="thinking"` block은 별도 (`reasoning` 필드 또는 debug log)
- string이면 그대로 통과 (mistral-medium-latest 등 non-reasoning은 string)

대안: magistral 자체를 KittyPaw에서 차단 (`provider="mistral" + model startswith("magistral")` 시 명시적 error). 비서 디폴트 부적합 + 파서 비용 큼 → 차단이 단순함.

---

## 7. Pending Verification (다운로드/설치 중, 2026-05-04)

| Source | Model | 상태 (2026-05-04 스냅샷) | 검증 결과 |
|---|---|---|---|
| Ollama | `qwen3:14b` | ✅ 받음 → 측정 완료 | thinking variant 확정 (5.1) |
| Ollama | `phi4-mini:latest` | ✅ 받음 → 측정 완료 | ★★ — 16GB MBA 후보 |
| Ollama | `hermes3:8b` | ✅ 받음 → 측정 완료 | ★ 영어 function 전용 (5.1.1) |
| Ollama | `granite4.1:8b` | ✅ 받음 → 측정 완료 | ★ 정체성 hallucination (5.1.2) |
| Ollama | `mistral-nemo:12b-instruct-2407-q4_K_M` | ✅ 받음 → 측정 완료 | ✗ 한국어 안정성 부족 (5.1.3) |
| Ollama | `qwen2.5:32b-instruct` | ✅ 받음 → 측정 완료 | ★★★ 36GB 비서 1순위 (정확성·reasoning) |
| LM Studio | `Qwen3-30B-A3B-Instruct-2507-4bit MLX` | ✅ 받음 (hf download 우회 — § 5.2) → 측정 완료 | ★★★ — 36GB 통합 1순위 (§ 3) |

다운로드 끝나는 즉시 § 2.2 / § 3 / § 5 / § 1 갱신.

### 검증 안 한 알려진 후보

- `llama3.3:70b` — Q4 ~40GB로 36 GB swap 위험. M3 Max에서만 권장
- `deepseek-r1:32b` — reasoning 강력하지만 thinking 모드. 비서 즉답성 ✗ 추정
- `command-r7b` (Cohere) — RAG 특화, 일반 비서엔 과함
- `gpt-oss:20b` (OpenAI open) — Groq에서 parallel tool 미지원 측정됨
- `qwen2.5-coder:32b` — 코드 특화 (일반 비서 과함)
- `MFDoom/deepseek-r1-tool-calling` — 커뮤니티 fine-tune

---

## 8. Methodology

### 8.1 매트릭스 prompt

```
안녕? 한 줄로 자기소개 해줘.
```

### 8.2 max_tokens 그리드

`256 / 1024 / 2048` — 하나의 모델이 thinking에 토큰 소진하는지, sweet spot이 어디인지 한 번에 보임.

### 8.3 측정 명령 (ollama, ssh tunnel 통해)

```bash
PROMPT='안녕? 한 줄로 자기소개 해줘.'
for tokens in 256 1024 2048; do
  start=$(python3 -c 'import time;print(time.time())')
  resp=$(curl -sS -X POST http://localhost:11500/v1/chat/completions \
    -H "Content-Type: application/json" \
    -d "{\"model\":\"<MODEL>\",\"max_tokens\":$tokens,\"messages\":[{\"role\":\"user\",\"content\":\"$PROMPT\"}]}")
  elapsed=$(python3 -c "import time;print(round(time.time()-$start,2))")
  echo "$resp" | python3 -c "
import json,sys
d=json.load(sys.stdin)
c=d['choices'][0]
print(f\"max_tokens=$tokens time={$elapsed}s finish={c.get('finish_reason')} \"
      f\"comp_tok={d['usage']['completion_tokens']} content_len={len(c['message'].get('content',''))}\")
"
done
```

### 8.4 캐시 효과 주의

연속 측정 시 모델이 메모리에 로드된 상태(warm) 응답이 cold start보다 5-10초 빠름. § 2.2의 `gemma4 max_tokens=1024 0.82s`는 warm 상태 측정 — **cold start는 +5-10s** 추정.

### 8.5 Methodology limits — 의식적 gap

- **한 prompt만 측정** — 다양한 시나리오(검색 요약, tool 호출 multi-turn, 한국어 long-form)는 별도 phase
- **통계적 유의성 X** (각 1회 측정) — outlier 가능
- **모델 vs 모델 throughput 비교는 같은 max_tokens + warm 상태에서만 의미** 있음
- **Stateful daemon 누적은 미측정** (§ 5.3에서 별도 KittyPaw chat 세션으로만 측정됨). 본 § 2 / § 3 매트릭스는 `curl /v1/chat/completions` 단발 호출이라 KittyPaw daemon의 누적 history(recent_window=20 turn) 미포함 — **best case latency**. 실 KittyPaw 비서 실행에선 system prompt + tools + memory + history 누적되어 prompt 토큰이 더 큼. eval framework(`--features llm-eval`)에서 ecological-valid 측정 진행
- **"마케팅 ≠ 실측" 검증 필수** — 별도 KittyPaw 세션에서 Groq qwen3-32b context_window를 plan 단계 추정값(16K)에서 실측(128K)으로 정정. 6× 차이. § 6.4 체크리스트 참조

---

## 9. Update Log

| Date | Change |
|---|---|
| 2026-05-04 | v1 초판 — Ollama 4 모델 + Cerebras + Groq 매트릭스, 7 known pitfalls |
| 2026-05-04 | v1.1 — phi4-mini / qwen3:14b / hermes3:8b 매트릭스 추가, pitfall 5.1 (qwen3 14b도 thinking 확인), 5.1.1 (Hermes 한국어 페르소나 혼동) 추가, Decision Matrix 16GB 1순위 phi4-mini로 갱신 |
| 2026-05-04 | v1.2 — granite4.1:8b 매트릭스 추가, **5.1.2 새 발견 — IBM Granite 정체성 hallucination (마케팅 ≠ 실측)**. 16GB Decision Matrix 갱신: phi4-mini 1순위 유지, granite4.1 강등 |
| 2026-05-04 | v1.3 — mistral-nemo:12b-instruct-2407 매트릭스 추가, **5.1.3 새 발견 — Tekken 토큰 효율 ≠ 한국어 품질** (영어/러시아어 mixin), § 5.2 LM Studio daemon stall 박제 + § 3 검증 보류 처리 |
| 2026-05-04 | v1.4 — **qwen2.5:32b-instruct ★★★ 매트릭스 추가** (36GB 비서 정확성 1순위, thinking 없음, 정체성 일관), Decision Matrix 36GB 시나리오를 즉답성/정확성 두 갈래로 분리 (gemma4 vs qwen2.5:32b) |
| 2026-05-04 | v1.5 — **§ 3 LM Studio MLX `Qwen3-30B-A3B-Instruct-2507-4bit` 매트릭스 박제** (warm 0.55-0.61s, thinking 0, 정체성 일관). § 5.2 LMS daemon stall은 `hf download` 직접으로 resolved 표시. § 5 numbering 정리(중복 5.2 → 5.2/5.3 분리, 후속 +1 shift). § 1 Decision Matrix에 LM Studio가 가능한 36GB 시나리오 1순위 (MLX) 별도 행 추가 |
| 2026-05-05 | v1.6 — **별도 KittyPaw 세션 측정 결과 통합**: § 5.3 Cerebras 8K cap을 실측 분포(median 8001 / max 9919 / 8K 초과 52%)로 박제 + stateful daemon 누적 함정 명시. § 6.4 신설 — Provider context_window /v1/models API 실측 체크리스트 + Groq qwen3-32b 128K 정정(plan 단계 16K 가정 6× 오류). § 6.5 신설 — 결정 기준 측정 전 박기 원칙. § 4 Cloud 매트릭스 갱신 — 모든 Groq 모델 128K 박제. § 8.5 의식적 gap (stateful daemon, 마케팅 ≠ 실측) 보강 |
| 2026-05-05 | v1.7 — **무료 cloud 매트릭스 대폭 확장 + 비표준 옵션 어댑터 fact**. 측정 신규 11종: Mistral 6 (large/medium/small/magistral/ministral-8b/pixtral-large), Gemini 7 (flash-lite/3-flash/2.5-flash 통과·3.1-pro/3-pro/2.5-pro/3.1-flash-lite quota), Groq qwen3-32b. 신규 함정 § 5.10-5.14: Qwen3-Coder family GH issue 6+ (회귀 권장), Mistral large 정체성 페르소나 SFT (3회 재현), Mistral magistral Native reasoning disable 불가, Groq qwen3-32b thinking variant + `reasoning_format=parsed/hidden` 우회, 무료 한도 docs 격차 (Gemini/Mistral/Together/DeepSeek dashboard 분산). § 6.6 Groq reasoning_format 어댑터 분기 권장. § 6.7 Mistral content list of blocks 파서 권장. § 4.1-4.9 공식 docs 1차 source로 한도 박제 (정량/미명시 라벨링). § 1 Decision Matrix Cloud Korean 1순위 = mistral-medium-latest 확정 (실측 fact 기반, Cerebras qwen-3-235b는 보조 phase) |
| 2026-05-05 | v1.8 — **`provider="lmstudio"` 신규 case + § 3.6 KittyPaw harness placeholder**. `llm/registry.go` 10번째 case (anthropic/openai/gemini/ollama/cerebras/groq/deepseek/openrouter/mistral/lmstudio); `lmstudioDefaultBaseURL = "http://localhost:11600/v1/chat/completions"` (dev-models SSH tunnel `:11600 → emac:1234`). dev-models harness measure script generalize: `make dev-models-measure BACKEND={ollama|lmstudio} MODEL=...` (기존 `dev-models-ollama-measure` rename). tunnel target 분리 — `make dev-models-tunnel-{ollama,lms}-{start,stop,status}` (별도 ControlPath suffix, 동시 운용). dev-models default config 7번째 entry `lmstudio-qwen3-30b-mlx`. § 2.4 헤더 갱신 (BACKEND=ollama). § 3.5 KittyPaw integration test에 harness Option 추가. § 3.6 신설 — qwen3-30b-a3b-instruct-2507 KittyPaw harness 측정 row placeholder (사용자 측정 후 박힘). bats: tunnel.bats 13 cases + measure.bats 11 cases GREEN |

---

## 10. Out of Scope (이 문서에서)

- README.md / CLAUDE.md 통합 — 단일 문서 원칙
- 자동 매트릭스 측정 스크립트 — 별도 phase
- 모델별 KittyPaw config 템플릿 wizard — 별도 phase
- Hermes Berkeley FC 직접 재현 — 외부 인용으로 충분
- 실 비서 워크플로우(검색→요약→tool) 풀 시나리오 — eval framework(`--features llm-eval`)에서 별도
