# Dev Models Harness — Local `/model` Testing

격리된 KittyPaw 환경에서 채팅 중 `/model` 명령으로 여러 LLM 모델을 turn-level로 swap하며 시연하는 도구. 사용자의 실 `~/.kittypaw/` daemon, secrets, 채팅 기록은 **건드리지 않는다**.

| 격리 항목 | dev-models | 사용자 daemon |
|---|---|---|
| Config dir | `/tmp/kittypaw-dev-models/` (`KITTYPAW_CONFIG_DIR`) | `~/.kittypaw/` |
| Bind | `127.0.0.1:3001` (loopback) | 평소 그대로 |
| Account | `default` (throwaway password) | 실 계정 |
| API key 저장 | env var only (파일 박제 X) | `secrets.json` |

---

## 사전 요구

| 항목 | 비고 |
|---|---|
| Vendor 키 | `GROQ_API_KEY`, `MISTRAL_API_KEY`, `GEMINI_API_KEY` 환경변수. 키 발급: https://console.groq.com/keys, https://console.mistral.ai/api-keys (Mistral Experiment plan 카드 ❌, 전화번호 인증), https://aistudio.google.com/apikey |
| `bin/kittypaw` | `make dev-models`가 자동으로 `make build` 거침 |
| Port `:3001` | 사용자 실 daemon이 `:3000`이면 충돌 X. 다른 환경이면 `KITTYPAW_DEV_PORT=3010 make dev-models`로 override |

---

## 한 명령 시작 (Quick Start)

```bash
cd /Users/jinto/projects/kittypaw/kitty/apps/kittypaw

export GROQ_API_KEY="gsk_..."
export MISTRAL_API_KEY="..."
export GEMINI_API_KEY="AIzaSy..."

make dev-models       # setup → server start → chat REPL 진입
```

채팅 REPL에서:

```
> /model                       # 현재 + 등록된 5 모델 list, "* groq-qwen" 활성 표시
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

## 등록된 5 모델

`scripts/dev-models.sh setup`이 `KITTYPAW_CONFIG_DIR/accounts/default/config.toml`에 박는 기본 5 모델 (cloud OpenAI 호환 wire 4 + Gemini Generative Language API wire 1 — multi-wire 검증 의도):

| ID | provider | model | thinking adapter |
|---|---|---|---|
| `groq-qwen` (default) | groq | `qwen/qwen3-32b` | `reasoning_format=parsed` 자동 송신 (§ 5.13 cleansed) |
| `groq-llama` | groq | `llama-3.3-70b-versatile` | non-thinking |
| `mistral-medium` | mistral | `mistral-medium-latest` | non-thinking, AI 자칭 일관 |
| `ministral-8b` | mistral | `ministral-8b-latest` | non-thinking, 작은 model |
| `gemini-flash-lite` | gemini | `gemini-2.5-flash-lite` | non-thinking, **별도 wire** (Generative Language API, OpenAI 호환 X) |

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

---

## 트러블슈팅

| 증상 | 원인 / 해결 |
|---|---|
| `port :3001 already in use` | 이전 dev-models daemon 잔재. `make dev-models-stop` 또는 `lsof -ti:3001 \| xargs kill` |
| `missing env: GROQ_API_KEY` | 키 export 안 됨. Quick Start의 `export ...` 두 줄 확인 |
| `daemon failed to bind :3001` | log 확인: `tail /tmp/kittypaw-dev-models.log` |
| 채팅 첫 응답 latency 길거나 cold | provider별 cold start. 두 번째 turn부터 warm |
| `/model groq-qwen` 후 응답에 `<think>` 노출 | KittyPaw 어댑터의 `reasoning_format=parsed` 미적용 — 본 commit 이전 binary일 가능성. `make build` 후 재시도 |
| Groq llama 응답에 일본어/태국어 mixin | § 4 매트릭스 박제 fact — Llama 3.3의 한국어 어색. **개선 X** (모델 자체 한계) |
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

setup wizard가 `--password-stdin --no-chat --no-service --force`로 비interactive 호출되어 `account.toml` + `secrets.json`을 박는다. (`auth.json`은 server-wide Web UI credentials이고 별도 — chat WS handshake는 server.toml의 `master_api_key`로 인증되므로 dev harness엔 불필요.) `config.toml`은 wizard 후 dev-models가 5 모델 template으로 overwrite.

`server.toml`에 `bind = "127.0.0.1:3001"` + 무작위 `master_api_key` 박음. chat client (`client/daemon.go:Connect`)가 이 둘을 읽어 BaseURL + APIKey 결정 — 박지 않으면 WS 401 또는 health-check 10초 timeout.

> **CLAUDE.md "Testing Isolation" 섹션의 `KITTYPAW_HOME` 명시는 stale docs**. 코드는 `KITTYPAW_HOME` env var를 안 봄 (검증 2026-05-05). 격리 환경 만들 때는 `KITTYPAW_CONFIG_DIR` 사용 — 본 harness가 그 패턴 박제.

---

## 비판적 함정 박제

1. **`HOME=/tmp/...` redirection 시도는 우회 — `KITTYPAW_CONFIG_DIR`이 정답**. KittyPaw는 `os.UserHomeDir()` 외에 `KITTYPAW_CONFIG_DIR` env var를 우선 lookup. HOME만 바꾸면 `~/.kittypaw/` 하위 layer 한 번 더 필요해 path mismatch.
2. **`--remote http://localhost:3001`는 chat에서 401**. `--remote`는 production server attach용 (auth token 요구). loopback dev daemon은 KITTYPAW_CONFIG_DIR + local discovery로 가야 master_api_key로 인증 통과.
3. **`server.toml` 빈 파일은 401/timeout**. `bind` + `master_api_key` 둘 다 필수.
4. **wizard `--password-stdin` 우회 X**. account.toml + secrets.json이 wizard에서만 만들어짐. 본 harness가 자동화.
5. **사용자 daemon 충돌**: 사용자가 평소 `:3000` 사용하면 dev-models는 `:3001` default라 충돌 없음. 동일 포트 시 `KITTYPAW_DEV_PORT=3010` override.

---

## 다른 모델 추가하기

`scripts/dev-models.sh setup --force` 후 `KITTYPAW_DEV_HOME/accounts/default/config.toml`을 직접 편집하거나, `scripts/dev-models.sh`의 `write_config_if_missing` heredoc을 수정. KittyPaw의 8 provider case (anthropic / openai / gemini / ollama / cerebras / groq / deepseek / openrouter / mistral) 모두 지원.

`provider="openai" + base_url=...` 우회로 mistral / gemini OpenAI-compat endpoint 쓰는 것은 **비추** — `OPENAI_API_KEY` env var와 vendor key 충돌. vendor 명시 case가 정답.

---

## SSH 통한 self-hosted ollama 테스트 (선택)

emac (별도 mac M3 Pro 36GB 등) 같은 별도 머신에 ollama 띄우고 KittyPaw 비서가 사용. 작업 머신 (M1 Air 8GB 등)에서 ollama 못 띄우는 환경 (8GB unified memory + 작업 앱 + 모델 = OOM/freeze) 용.

### 사전 요구

| 항목 | 비고 |
|---|---|
| `ssh emac` alias | `~/.ssh/config` 또는 `known_hosts`. 키 인증 작동 필요 |
| emac에 ollama 설치 | `brew install ollama` 또는 https://ollama.com/download |
| 같은 LAN | SSH tunnel 통해 forward. 외부 SSH도 가능하나 latency ↑ |
| 로컬 도구 | `brew install jq bats-core shellcheck` |

### Quick Start

```bash
make dev-models-tunnel              # SSH tunnel 시작 (background, idempotent)
make dev-models                     # daemon + chat REPL (cloud 모델 swap 가능)

# 다른 터미널에서 측정
make dev-models-ollama-measure MODEL=qwen2.5:7b
make dev-models-ollama-measure MODEL=qwen2.5-coder:7b PROMPT='Go에서 fizzbuzz 함수 한 줄'

make dev-models-tunnel-stop         # 끝
```

### 측정 자동화 흐름 (`scripts/dev-models-ollama-measure.sh`)

```
1. command -v 사전요건 검증 (jq, ssh, ollama, lsof, curl, awk, sed)
2. master_api_key 파싱 (awk -F'"' '/^master_api_key/{print $2}' server.toml)
3. ssh emac true (3s timeout — emac off / sleep / alias 누락 감지)
4. tunnel 2단계 probe (lsof :11500 + curl http://localhost:11500/api/tags — orphan ControlSocket 검출)
5. daemon 헬스 (lsof :3001)
6. ssh emac "ollama pull <model>"  # 이미 받았으면 빠름
7. config.toml 백업 + [[llm.models]] id="ollama-measure" 추가 + [llm].default swap
8. POST /api/v1/reload (Authorization: Bearer master_api_key)
9. POST /api/v1/chat (default = ollama-measure 적용됨, jq -nc로 JSON 빌드)
10. 응답 + latency 출력
11. trap EXIT/INT/TERM → config 원복 + reload (Ctrl-C도 OK)
```

**load-bearing fact**: KittyPaw `core.ChatPayload` (core/types.go:97) 에 model 필드 없음 + `handleChat` (server/api.go:472)이 `nil` RunOptions로 Run 호출 → `POST /api/v1/chat`은 항상 `[llm].default` 사용. 측정 모델 swap 유일한 방법 = config 임시 변경 + `/api/v1/reload`.

### 박제 가이드 — 측정 후 사용자 직접 기록

측정 후 `apps/kittypaw/docs/MODEL_GUIDE.md § 5.15` 표 직접 채우기:

- **quality**: 1=fail / 2=어색 / 3=OK / 4=좋음 / 5=완벽 (한국어 자연스러움 + 코드 정확도 종합)
- **latency**: cold = 첫 호출 (모델 로딩 포함), warm = 두 번째 호출 — 둘 다 측정해서 로딩 영향 분리
- **context_window**: 응답 길이 + 모델 spec 비교 (Q4 양자화 시 줄어들 수 있음)

자동 eval (BLEU 등) ❌ — § 4 매트릭스 패턴은 사용자 직접 박제.

### tunnel fail mode

| 증상 | 원인 / 진단 |
|---|---|
| `ssh emac fail — emac off?` | emac off / sleep / SSH 설정 누락. `ssh emac` 직접 시도 |
| `tunnel down — make dev-models-tunnel` | tunnel 안 띄움 또는 `ssh -O exit` 후 |
| `tunnel orphan (forward unreachable)` | ControlSocket 살았는데 SSH connection reset. `make dev-models-tunnel-stop && make dev-models-tunnel` |
| `kittypaw daemon not listening on :3001` | dev-models 시작 안 됨. `make dev-models-stop && make dev-models` |
| `ollama pull failed` | emac 네트워크 / 디스크 부족 |

### 보안

- SSH tunnel = OpenSSH `ControlMaster=auto` + `ControlPath=/tmp/kittypaw-dev-models-tunnel-%C` (사용자 `~/.ssh/config` 무영향, `-o` inline 옵션만)
- `pkill -f` ❌ — `ssh -O exit`로 해당 tunnel만 정확히 종료 (다른 ssh process 영향 X)
- LAN bind (`OLLAMA_HOST=0.0.0.0`) 회피 — LAN의 다른 기기 노출 risk
- SSH keepalive (`ServerAliveInterval=10 ServerAliveCountMax=3`) — emac sleep 시 hang 회피

### Race 박제 — 단일 사용자 가정

dev-models harness는 **단일 사용자 가정** (사용자 본인). 측정 중 다른 chat 요청 동시 발생 시:
- config swap 진행 중 → 이전 default 호출
- swap 완료 후 → 측정 모델 호출
- trap restore 후 → 원래 default 복귀

측정 중 다른 chat 요청 X 권장. 동시성이 필요한 시나리오는 별도 phase.

### lm-studio 등 다른 self-hosted

별도 phase. `provider="lmstudio"` case 추가 + LM_STUDIO_API_KEY env 처리. 본 phase는 ollama 전용.

---

## 관련 문서

- `apps/kittypaw/docs/MODEL_GUIDE.md` — 측정 fact 박제 (각 모델의 한국어 응답, latency, 함정)
- `apps/kittypaw/CLAUDE.md` — KittyPaw architecture (단 "Testing Isolation" 섹션의 `KITTYPAW_HOME`은 stale fact, 별도 phase에서 fix 예정)
- `scripts/dev-models.sh help` — 짧은 CLI help
