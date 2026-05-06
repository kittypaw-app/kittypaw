# Dev Models Harness — Local `/model` Testing

격리된 KittyPaw 환경에서 채팅 중 `/model` 명령으로 여러 LLM 모델을 turn-level로 swap하며 시연하는 도구. 사용자의 실 `~/.kittypaw/` daemon, secrets, 채팅 기록은 **건드리지 않는다**.

| 격리 항목 | dev-models | 사용자 daemon |
|---|---|---|
| Config dir | `/tmp/kittypaw-dev-models/` (`KITTYPAW_CONFIG_DIR`) | `~/.kittypaw/` |
| Bind | `127.0.0.1:3001` (loopback) | 평소 그대로 |
| Account | `default` (throwaway password) | 실 계정 |
| API key 저장 | env var only (파일 저장 X) | `secrets.json` |

---

## 사전 요구

| 항목 | 비고 |
|---|---|
| Vendor 키 | `GROQ_API_KEY`, `MISTRAL_API_KEY`, `GEMINI_API_KEY`, `OPENROUTER_API_KEY` 환경변수. 키 발급: https://console.groq.com/keys, https://console.mistral.ai/api-keys (Experiment plan 카드 ❌, 전화번호 인증), https://aistudio.google.com/apikey, https://openrouter.ai/keys (free `:free` 모델, 카드 ❌) |
| `bin/kittypaw` | `make dev-models`가 자동으로 `make build` 거침 |
| Port `:3001` | 사용자 실 daemon이 `:3000`이면 충돌 X. 다른 환경이면 `KITTYPAW_DEV_PORT=3010 make dev-models`로 override |

---

## 한 명령 시작 (Quick Start)

```bash
cd /Users/jinto/projects/kittypaw/kitty/apps/kittypaw

export GROQ_API_KEY="gsk_..."
export MISTRAL_API_KEY="..."
export GEMINI_API_KEY="AIzaSy..."
export OPENROUTER_API_KEY="sk-or-v1-..."

make dev-models       # setup → server start → chat REPL 진입
```

채팅 REPL에서:

```
> /model                       # 현재 + 등록된 7 모델 list, "* groq-qwen" 활성 표시
> 안녕? 한 줄 자기소개          # Qwen (default — Groq qwen3-32b cleansed, <think> 부재)
> /model mistral-medium         # turn-level swap
> 안녕? 한 줄 자기소개          # Mistral 응답 ("AI 도우미야" 자칭)
> /model groq-llama             # 다른 swap
> 안녕? 한 줄 자기소개          # Llama 응답 (한국어에 일/태/중 mixin 가능)
> /model groq-qwen              # 첫 모델로 복귀
> /model groq-qwen              # → "이미 groq-qwen를 사용 중입니다." (no-op)
> /model nonsuch                # → "알 수 없는 모델: nonsuch ..." (rejected)
```

`Ctrl-D`로 chat 종료 (daemon은 살아있음 — 다시 `make dev-models-chat`으로 재진입).

---

## 종료 / 정리

```bash
make dev-models-stop      # 격리 daemon 종료 (사용자 실 daemon 영향 X)
make dev-models-clean     # /tmp/kittypaw-dev-models + 로그 삭제 (--yes 자동)
```

API 키 revoke 권장 (시연 후):
- Mistral: https://console.mistral.ai/api-keys
- Groq: https://console.groq.com/keys

---

## 등록된 7 모델

`scripts/dev-models.sh setup`이 `KITTYPAW_CONFIG_DIR/accounts/default/config.toml`에 추가하는 기본 7 모델 (cloud OpenAI 호환 wire 5 + Gemini Generative Language API wire 1 + self-hosted LM Studio MLX 1 — multi-wire 검증 + provider routing 다양화 의도):

| ID | provider | model | thinking adapter |
|---|---|---|---|
| `groq-qwen` (default) | groq | `qwen/qwen3-32b` | `reasoning_format=parsed` 자동 송신 (§ 5.13 cleansed) |
| `groq-llama` | groq | `llama-3.3-70b-versatile` | non-thinking |
| `mistral-medium` | mistral | `mistral-medium-latest` | non-thinking, AI 자칭 일관 |
| `ministral-8b` | mistral | `ministral-8b-latest` | non-thinking, 작은 model |
| `gemini-flash-lite` | gemini | `gemini-2.5-flash-lite` | non-thinking, **별도 wire** (Generative Language API, OpenAI 호환 X) |
| `openrouter-llama-3.3` | openrouter | `meta-llama/llama-3.3-70b-instruct:free` | non-thinking, **provider routing 변동** (§ 4.6) — production 비추, 다양화 후보 |
| `lmstudio-qwen3-30b-mlx` | lmstudio | `qwen3-30b-a3b-instruct-2507` | non-thinking, **self-hosted MLX** (port 11600 → emac:1234, 사전 `make dev-models-tunnel-lms` 필요 — 모델 load는 measure script 자동, 아래 SSH 섹션 참조) |

`magistral-medium-latest` 등 thinking variant는 본 phase 디폴트 X — 직접 추가 시 KittyPaw가 list-of-blocks content 자동 unwrap (§ 6.7 extractContent).

---

## 단계별 명령 (세부 제어)

| Make target | 동작 |
|---|---|
| `make dev-models` | setup + server + chat 한 번에 (recommended) |
| `make dev-models-setup` | 격리 config + wizard 자동 (`--password-stdin`) |
| `make dev-models-server` | daemon만 시작 (interactive chat 없이) |
| `make dev-models-chat` | 떠 있는 daemon에 chat REPL 부착 |
| `make dev-models-stop` | 격리 daemon 종료 |
| `make dev-models-clean` | 격리 home + 로그 삭제 (`--yes` 자동) |
| `make dev-models-status` | 현재 격리 상태 표시 |

스크립트 직접 호출도 가능:

```bash
scripts/dev-models.sh go         # 동일 (make dev-models의 alias)
scripts/dev-models.sh setup --force   # 기존 config overwrite
KITTYPAW_DEV_PORT=3010 scripts/dev-models.sh go
```

---

## 환경 변수

| 변수 | 기본값 | 의미 |
|---|---|---|
| `KITTYPAW_DEV_HOME` | `/tmp/kittypaw-dev-models` | 격리 KITTYPAW_CONFIG_DIR. 다른 dir 원하면 override |
| `KITTYPAW_DEV_PORT` | `3001` | daemon bind port. 사용자 실 daemon이 :3001이면 변경 |
| `KITTYPAW_DEV_BIND` | `127.0.0.1:$KITTYPAW_DEV_PORT` | bind 주소. **loopback default** (vendor 키 보유 daemon이라 LAN 노출 ❌) |
| `KITTYPAW_DEV_LOG` | `/tmp/kittypaw-dev-models.log` | daemon log |
| `GROQ_API_KEY` | (필수) | Groq vendor key |
| `MISTRAL_API_KEY` | (필수) | Mistral vendor key |
| `GEMINI_API_KEY` | (필수) | Gemini vendor key |
| `OPENROUTER_API_KEY` | (필수) | OpenRouter vendor key |

---

## 트러블슈팅

| 증상 | 원인 / 해결 |
|---|---|
| `port :3001 already in use` | 이전 dev-models daemon 잔재. `make dev-models-stop` 또는 `lsof -ti:3001 \| xargs kill` |
| `missing env: GROQ_API_KEY` | 키 export 안 됨. Quick Start의 `export ...` 두 줄 확인 |
| `daemon failed to bind :3001` | log 확인: `tail /tmp/kittypaw-dev-models.log` |
| 채팅 첫 응답 latency 길거나 cold | provider별 cold start. 두 번째 turn부터 warm |
| `/model groq-qwen` 후 응답에 `<think>` 노출 | KittyPaw 어댑터의 `reasoning_format=parsed` 미적용 — 본 commit 이전 binary일 가능성. `make build` 후 재시도 |
| Groq llama 응답에 일본어/태국어 mixin | § 4 매트릭스 기록 fact — Llama 3.3의 한국어 어색. **개선 X** (모델 자체 한계) |
| `/model magistral-...` 추가했는데 응답이 비어있음 | magistral은 list-of-blocks content. KittyPaw extractContent가 unwrap (§ 6.7). non-empty 응답이면 정상 |

---

## 격리 메커니즘 (load-bearing fact)

핵심: **`KITTYPAW_CONFIG_DIR` 환경변수**가 KittyPaw base directory를 직접 override (`core/config.go:481`). HOME redirection 또는 `.kittypaw/` 하위 디렉토리 join은 **불필요**.

```
KITTYPAW_CONFIG_DIR=/tmp/kittypaw-dev-models
├── server.toml              # bind + master_api_key (DaemonConn 의존, 키는 setup마다 무작위 16-byte hex)
├── daemon.pid               # local discovery (server start 후)
└── accounts/default/
    ├── config.toml          # [[llm.models]] 5 entries (wizard 후 overwrite)
    ├── account.toml         # wizard 자동 생성
    └── secrets.json         # wizard 자동 생성
```

setup wizard가 `--password-stdin --no-chat --no-service --force`로 비interactive 호출되어 `account.toml` + `secrets.json`을 작성한다. (`auth.json`은 server-wide Web UI credentials이고 별도 — chat WS handshake는 server.toml의 `master_api_key`로 인증되므로 dev harness엔 불필요.) `config.toml`은 wizard 후 dev-models가 5 모델 template으로 overwrite.

`server.toml`에 `bind = "127.0.0.1:3001"` + 무작위 `master_api_key` 작성. chat client (`client/daemon.go:Connect`)가 이 둘을 읽어 BaseURL + APIKey 결정 — 작성하지 않으면 WS 401 또는 health-check 10초 timeout.

> **격리 메커니즘 fact**: `KITTYPAW_CONFIG_DIR` 만 코드가 읽음 (verified `core/config.go:482`). CLAUDE.md "Testing Isolation" 섹션도 동일 환경변수 정한 후 일관 (Plan A T3, 2026-05-06).

---

## 비판적 함정 기록

1. **`HOME=/tmp/...` redirection 시도는 우회 — `KITTYPAW_CONFIG_DIR`이 정답**. KittyPaw는 `os.UserHomeDir()` 외에 `KITTYPAW_CONFIG_DIR` env var를 우선 lookup. HOME만 바꾸면 `~/.kittypaw/` 하위 layer 한 번 더 필요해 path mismatch.
2. **`--remote http://localhost:3001`는 chat에서 401**. `--remote`는 production server attach용 (auth token 요구). loopback dev daemon은 KITTYPAW_CONFIG_DIR + local discovery로 가야 master_api_key로 인증 통과.
3. **`server.toml` 빈 파일은 401/timeout**. `bind` + `master_api_key` 둘 다 필수.
4. **wizard `--password-stdin` 우회 X**. account.toml + secrets.json이 wizard에서만 만들어짐. 본 harness가 자동화.
5. **사용자 daemon 충돌**: 사용자가 평소 `:3000` 사용하면 dev-models는 `:3001` default라 충돌 없음. 동일 포트 시 `KITTYPAW_DEV_PORT=3010` override.

---

## 다른 모델 추가하기

`scripts/dev-models.sh setup --force` 후 `KITTYPAW_DEV_HOME/accounts/default/config.toml`을 직접 편집하거나, `scripts/dev-models.sh`의 `write_config_if_missing` heredoc을 수정. KittyPaw의 10 provider case (anthropic / openai / gemini / ollama / cerebras / groq / deepseek / openrouter / mistral / lmstudio) 모두 지원.

`provider="openai" + base_url=...` 우회로 mistral / gemini OpenAI-compat endpoint 쓰는 것은 **비추** — `OPENAI_API_KEY` env var와 vendor key 충돌. vendor 명시 case가 정답.

---

## SSH 통한 self-hosted backend 테스트 (선택)

emac (별도 mac M3 Pro 36GB 등) 같은 별도 머신에 ollama 또는 LM Studio (Apple Metal MLX) 띄우고 KittyPaw 비서가 사용. 작업 머신 (M1 Air 8GB 등)에서 로컬 모델 못 띄우는 환경 (8GB unified memory + 작업 앱 + 모델 = OOM/freeze) 용. 두 backend는 **별도 SSH tunnel ControlPath**로 동시 운용 가능 (emac M3 36GB 헤드룸).

| Backend | 로컬 포트 | emac 포트 | KittyPaw provider | 모델 load |
|---|---|---|---|---|
| `ollama` | `:11500` | `:11434` | `provider="ollama"` | `ssh emac ollama pull <model>` (자동) |
| `lmstudio` | `:11600` | `:1234` | `provider="lmstudio"` | LM Studio app GUI에서 수동 load (§ 3.4 lms CLI stall fact 회피) |

### 공통 사전 요구

| 항목 | 비고 |
|---|---|
| `ssh emac` alias | `~/.ssh/config` 또는 `known_hosts`. 키 인증 작동 필요 |
| 같은 LAN | SSH tunnel 통해 forward. 외부 SSH도 가능하나 latency ↑ |
| 로컬 도구 | `brew install jq bats-core shellcheck` |

### Backend별 사전 요구

**ollama** (provider="ollama"):
- emac에 ollama 설치: `brew install ollama` 또는 https://ollama.com/download
- emac에 ollama daemon 가동 (`ollama serve` 또는 launchd)

**LM Studio MLX** (provider="lmstudio"):
- emac에 LM Studio 앱 설치 (https://lmstudio.ai)
- LM Studio에서 HTTP server 활성화 (Settings → Developer → Server, port 1234)
- emac에 `lms` CLI 설치 (LM Studio app → Settings → Developer → "Install lms CLI" 클릭). measure script가 SSH로 자동 호출하므로 PATH 등록은 default home 위치 (`~/.lmstudio/bin/lms`)면 충분. § 3.4 fact 정정 — `lms get` (download)만 daemon stall, CLI 본체 (`lms load/ls/ps`)는 정상 (검증 2026-05-05).
- 측정 대상 모델 download 1회 (e.g., `mlx-community/Qwen3-30B-A3B-Instruct-2507-4bit` 17.2GB). `lms get` 회피 → `hf download` direct (§ 3.4)
- 모델 load는 measure script가 자동 (`lms load <modelKey> -y --gpu max --ttl 300`). 수동 GUI load 불필요.
- API key 인증 미사용 (HTTP server 평문 노출 — emac 로컬에 한정)

### Quick Start — ollama

```bash
make dev-models-tunnel-ollama        # SSH tunnel :11500 → emac:11434 (background, idempotent)
make dev-models                      # daemon + chat REPL (cloud 모델 swap 가능 + /model 명령)
make dev-models-tunnel-ollama-stop   # 끝
```

### Quick Start — LM Studio MLX

```bash
# 사전: LM Studio app GUI에서 HTTP server 켜져 있고 (Settings → Developer → Server),
# 측정 대상 모델 download + GUI load 완료.
make dev-models-tunnel-lms           # SSH tunnel :11600 → emac:1234
make dev-models                      # daemon + chat REPL
# /model lmstudio-qwen3-30b-mlx       # chat REPL 안에서 swap (default config 7번째 entry)
make dev-models-tunnel-lms-stop      # 끝
```

두 backend 동시 운용도 가능 (`make dev-models-tunnel-ollama && make dev-models-tunnel-lms`). ControlPath suffix가 다르고 (`-ollama` / `-lms`) port도 분리되어 race 없음.

### 자동 측정 — Plan B 별도 phase

raw 측정 자동화 (LLM judge + use case별 추천 + drift baseline) 는 **별도 phase**로 처리 — `eval/secretary_smoke` + `eval/user_vision_flows` framework rebuild + `eval/models.toml` source-of-truth (사용자 정한 `a` 결정). 본 dev-models harness는 chat REPL `/model` 명령으로 *수동* 측정 + provider/model swap 추가 entry로 keep — automation은 eval framework로.

**load-bearing fact**: KittyPaw `core.ChatPayload` (core/types.go:97) 에 model 필드 없음 + `handleChat` (server/api.go:472)이 `nil` RunOptions로 Run 호출 → `POST /api/v1/chat`은 항상 `[llm].default` 사용. 측정 모델 swap 유일한 방법 = config 임시 변경 + `/api/v1/reload` (eval framework 가 처리).

### 기록 가이드 — 수동 측정 후 사용자 직접 기록

수동 측정 (chat REPL `/model` swap + 직접 prompt) 후 `apps/kittypaw/docs/MODEL_GUIDE.md` 표 직접 채우기:

- **ollama**: § 2.4 raw 측정 fact 표
- **lmstudio**: § 3.6 raw 측정 fact 표

기록 항목:
- **quality**: 1=fail / 2=어색 / 3=OK / 4=좋음 / 5=완벽 (한국어 자연스러움 + 코드 정확도 종합)
- **latency**: warm chat (cold load는 부수적 — 띄워두고 쓰는 가정)
- **context_window**: 응답 길이 + 모델 spec 비교 (Q4 양자화 시 줄어들 수 있음. MLX 4bit는 thinking 없는 instruct variant 우세 — § 3.3)

자동 eval (LLM judge + drift baseline) 은 Plan B에서 처리 — 본 기록 가이드는 수동 측정 entry로 keep.

### tunnel fail mode

| 증상 | 원인 / 진단 |
|---|---|
| `ssh emac fail — emac off?` | emac off / sleep / SSH 설정 누락. `ssh emac` 직접 시도 |
| `tunnel down — make dev-models-tunnel-ollama` (또는 `-lms`) | tunnel 안 띄움 또는 `ssh -O exit` 후 |
| `tunnel orphan (forward unreachable)` | ControlSocket 살았는데 SSH connection reset. `make dev-models-tunnel-{ollama|lms}-stop && make dev-models-tunnel-{ollama|lms}` |
| `kittypaw daemon not listening on :3001` | dev-models 시작 안 됨. `make dev-models-stop && make dev-models` |
| `ollama pull failed` (ollama only) | emac 네트워크 / 디스크 부족 |
| `lms CLI not found on emac` (lmstudio only) | emac에 lms CLI 미설치. LM Studio app → Settings → Developer → "Install lms CLI" 클릭 후 재시도. (~/.lmstudio/bin/lms 가 default 위치) |
| `lms load failed for <model>` (lmstudio only) | modelKey 잘못 (path 줬는지 확인 — `ssh emac '~/.lmstudio/bin/lms ls'` 결과의 modelKey 컬럼 사용). `--exact` 안 쓰면 fuzzy 매칭이지만 다중 매칭 시 -y가 첫 매치 선택 |
| `tunnel orphan (forward unreachable — LM Studio HTTP server stopped?)` (lmstudio) | LM Studio app은 떠있으나 HTTP server 비활성화. Settings → Developer → Server toggle 확인 |

### 보안

- SSH tunnel = OpenSSH `ControlMaster=auto` + `ControlPath=/tmp/kittypaw-tunnel-{ollama|lms}.sock` (사용자 `~/.ssh/config` 무영향, `-o` inline 옵션만). 단일-host scope (emac 한정) — multi-host 확장 시 host 접미사 추가 (Plan B).
- `pkill -f` ❌ — `ssh -O exit`로 해당 tunnel만 정확히 종료 (다른 ssh process 영향 X)
- LAN bind (`OLLAMA_HOST=0.0.0.0` / LM Studio Server "Network Visible") 회피 — LAN의 다른 기기 노출 risk. SSH tunnel은 SSH 인증으로 가려져 안전.
- SSH keepalive (`ServerAliveInterval=10 ServerAliveCountMax=3`) — emac sleep 시 hang 회피
- LM Studio HTTP API는 인증 없음 — emac에서 `lsof -i :1234`로 LISTEN 주소 확인 (`127.0.0.1:1234`만 OK; `*:1234` 공개 시 LAN 차단 필요)

### Race 기록 — 단일 사용자 가정

dev-models harness는 **단일 사용자 가정** (사용자 본인). 측정 중 다른 chat 요청 동시 발생 시:
- config swap 진행 중 → 이전 default 호출
- swap 완료 후 → 측정 모델 호출
- trap restore 후 → 원래 default 복귀

측정 중 다른 chat 요청 X 권장. 동시성이 필요한 시나리오는 별도 phase.

두 backend 동시 측정 (e.g., ollama + lmstudio 병렬)도 같은 race 패턴 — config 단일 swap 지점 (`[llm].default`) 공유 → 순차 실행 권장.

### llama.cpp 등 다른 self-hosted

별도 phase. llama.cpp는 OpenAI Chat Completions wire 호환 — `provider="lmstudio"` 또는 `provider="openai" + base_url` 우회 가능성 있으나, 본 phase는 ollama + LM Studio MLX 두 backend 한정.

---

## 관련 문서

- `apps/kittypaw/docs/MODEL_GUIDE.md` — 측정 fact 기록 (각 모델의 한국어 응답, latency, 함정)
- `apps/kittypaw/CLAUDE.md` — KittyPaw architecture ("Testing Isolation" 섹션은 `KITTYPAW_CONFIG_DIR` load-bearing fact 박힘, Plan A T3에서 정정)
- `scripts/dev-models.sh help` — 짧은 CLI help
