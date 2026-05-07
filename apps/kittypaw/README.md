# KittyPaw

Experimental Go framework for local AI agents. Single binary, goja JS sandbox,
5 channel adapters, skill registry. **Alpha** — honest status over polish.

## Status

- ✅ **Working** — CLI + local server, registry install, sandbox + permission, 5 channel adapters (Telegram/Slack/Discord/Kakao/WS)
- ✅ **Working** — Telegram/Kakao inbound media metadata; attached images are available to the runner through `Vision.analyzeAttachment(...)`
- 🚧 **Partial** — Reflection candidate surface (verified), `skill create` syntax (5/5 measured), Web search source quality
- 🔬 **Experimental** — Team space, MoA, live workspace indexing
- ❌ **Not / retired** — Windows GUI signing, "learns the more you use it" auto-adaptation, self-healing (retired)

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/kittypaw-app/kitty/main/install-kittypaw.sh | sh
```

Without `VERSION`, the installer follows `apps/kittypaw/stable.json`, not the
newest GitHub release. Use `VERSION` to install a specific candidate release
for testing:

```bash
VERSION=0.4.9 curl -fsSL https://raw.githubusercontent.com/kittypaw-app/kitty/main/install-kittypaw.sh | sh
```

Installer overrides for local forks or nonstandard install locations:

```bash
KITTYPAW_INSTALL_REPO=owner/repo \
INSTALL_DIR="$HOME/.local/bin" \
curl -fsSL https://raw.githubusercontent.com/kittypaw-app/kitty/main/install-kittypaw.sh | sh

KITTYPAW_INSTALL_SCRIPT_URL=https://example.com/install-kittypaw.sh \
curl -fsSL https://raw.githubusercontent.com/kittypaw-app/kitty/main/install-kittypaw.sh | sh
```

## Quick Start

```bash
kittypaw setup --account alice            # interactive setup (account login, LLM, channels); auto-enters chat on TTY
kittypaw skill install weather-briefing   # install a skill from registry
kittypaw chat "오늘 날씨 알려줘"            # one-shot chat (auto-starts local server)
```

Inspect what you get: a local server, one installed skill, an LLM-backed chat. Skill runtime behaviour depends on its package, the configured APIs, and the LLM provider.

```bash
kittypaw chat          # interactive REPL mode
kittypaw server start  # start as HTTP/WebSocket server
```

## Recommended Models

Verified 2026-05-04 to 2026-05-05. Detailed matrix + raw measurements: [`docs/MODEL_GUIDE.md`](docs/MODEL_GUIDE.md).

### Cloud (KittyPaw chat default — Korean priority)

| Rank | Provider / Model | Free quota | Notes |
|---|---|---|---|
| ★★★ 1 | **Mistral `mistral-medium-latest`** | docs unspecified — dashboard. Card ❌ phone auth + ⚠️ training-on-by-default | Korean natural, AI-self-aware, 128K context |
| ★★★ 2 | **Groq `qwen/qwen3-32b`** | 60 RPM / 1K RPD / 6K TPM / 500K TPD. **org-gated** + `reasoning_format=parsed` required | Cleansed thinking variant, 128K context |
| ★★ 3 | Mistral `ministral-8b-latest` | (Mistral Experiment plan) | 256K context, faster |
| ★★ 4 | Groq `llama-3.3-70b-versatile` | 30 RPM / 1K RPD / 12K TPM / 100K TPD | Parallel tools ✅, occasional Korean/Japanese mix |
| ★★ 5 | Gemini `gemini-2.5-flash-lite` | docs unspecified — `aistudio.google.com/rate-limit` dashboard | 1M input / 65K output |
| ★★ 6 | **OpenRouter `meta-llama/llama-3.3-70b-instruct:free`** | 20 RPM / 200 RPD, card ❌ | Verified KittyPaw harness 2026-05-05 (1s warm, Korean natural). Provider routing varies — § 4.6 |

**Skipped**: Cerebras (8K context cap → KittyPaw chat unfit), Together AI ($5 card required), Cohere Trial (non-commercial license).

**Untested (key not held)**: DeepSeek (5M tokens / 30-day grant — tool_call leak ~11%).

### Local (verified on eMac M3 Pro 36 GB · ssh tunnel `:11500 → emac:11434`)

| Rank | Backend / Model | Memory (Q4) | Warm latency | Notes |
|---|---|---|---|---|
| ★★★ 1 | **LM Studio MLX `qwen3-30b-a3b-instruct-2507`** 4bit | 17.2 GB | **0.55-0.61s** (cold 30s) | MoE, no thinking |
| ★★★ 2 | **Ollama `qwen2.5:32b-instruct`** Q4_K_M | 19 GB | 3-9s | Thinking-free, identity stable |
| ★★★ 3 | Ollama `gemma4:latest` 8B | 9.6 GB | 0.82s | Fastest 8B |
| ★★ 4 | Ollama `qwen3:latest` 8B | 5.2 GB | 10.7s @ max_tok=1024 | Sweet spot |
| ★★★ 5 | Ollama `phi4-mini:latest` 3.8B | 2.5 GB | <1s (KittyPaw harness) | **16 GB MBA fit** |
| ★★ 6 | Ollama `granite4.1:8b` Q4_K_M | 5.4 GB | 1s (KittyPaw harness) | Raw ollama identity hallucination (§ 5.1.2) — **KittyPaw system prompt가 강제 통과** |

**Avoid**: qwen3:14b/4b/30b-a3b (thinking explosion), hermes3:8b (English-only), mistral-nemo:12b (Korean unstable), **`llama3.3:70b`** (KittyPaw instruction following 실패 — 모델 크기 ≠ 호환, § 5.1.4).

**Sources** (verified 2026-05-05): [Cerebras](https://inference-docs.cerebras.ai/support/rate-limits) · [Groq](https://console.groq.com/docs/rate-limits) · [Mistral](https://docs.mistral.ai/deployment/ai-studio/tier) · [Gemini](https://ai.google.dev/gemini-api/docs/rate-limits) · [OpenRouter](https://openrouter.ai/docs/faq) · [Together](https://docs.together.ai/docs/billing-credits)

## In-Chat Commands

These commands are entered inside `kittypaw chat`, Telegram, Kakao, or another
connected chat channel:

```text
/help                 show command help
/status               show today's local execution stats
/skills               list local user-created skills
/run <name>           run an installed skill or package by id/name
/teach <description>  create and save a draft skill from chat
/staff <staff-id> set the default staff identity for this account
```

## Accounts

Fresh installs create named local accounts under `~/.kittypaw/accounts/<accountID>/`.
The legacy `~/.kittypaw/accounts/default/` layout still works for upgraded installs.

```bash
printf '%s\n' "$LOCAL_WEB_PASSWORD" | kittypaw setup --account alice --password-stdin
printf '%s\n' "$BOB_WEB_PASSWORD" | kittypaw account add bob --password-stdin

KITTYPAW_ACCOUNT=bob kittypaw chat
kittypaw chat --account bob
```

If multiple accounts exist, CLI commands that read or write account config require
`--account <id>` or `KITTYPAW_ACCOUNT=<id>`. The local Web UI requires login once
`~/.kittypaw/auth.json` has local users; each account has its own Web UI password.

## Skills

```bash
kittypaw skill install weather-briefing           # install from registry
kittypaw skill install https://github.com/owner/repo   # install from GitHub
kittypaw skill install /path/to/local/skill       # install from local directory
kittypaw skill search <keyword>                   # search skill registry
kittypaw skill list                               # list installed skills
kittypaw skill create <description>               # generate a draft skill from natural language
```

## Config

Account TOML config lives at `~/.kittypaw/accounts/<accountID>/config.toml`.
Server-wide settings live at `~/.kittypaw/server.toml`, and local Web UI login
metadata lives at `~/.kittypaw/auth.json`.

```toml
[registry]
url = "https://raw.githubusercontent.com/kittypaw-app/skills/main"
```

Operational environment variables:

| Variable | Purpose |
|---|---|
| `KITTYPAW_ACCOUNT` | Select the local account for CLI commands |
| `KITTYPAW_TELEGRAM_BOT_TOKEN` | Seed `kittypaw account add` / setup with a Telegram bot token |
| `KITTYPAW_ALLOW_INSECURE_REGISTRY=1` | Test/local override that permits non-HTTPS skill registries |
| `INSTALL_DIR` | Install destination for `apps/kittypaw/install-kittypaw.sh` |
| `KITTYPAW_INSTALL_REPO` | Root installer repository override, e.g. `owner/repo` |
| `KITTYPAW_INSTALL_SCRIPT_URL` | Root installer script URL override |
| `KITTYPAW_CHANNEL=latest` | Installer override that follows the newest GitHub release instead of stable |
| `VERSION` | Installer override for a specific release, e.g. `0.4.9` |

## Build from Source

```bash
make build    # Build binary
make test     # Run tests
make lint     # Lint (requires golangci-lint)
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for development workflow and conventions.

## Release

KittyPaw releases are built from the monorepo root workflow
`.github/workflows/release-kittypaw.yml`. Product tags are namespaced:

```bash
git tag kittypaw/vX.Y.Z
git push origin kittypaw/vX.Y.Z
```

The workflow builds archives directly with `go build`, signs and notarizes the
macOS binaries, and updates release checksums. Do not use plain `vX.Y.Z` tags
for monorepo product releases.

Binary releases are candidates until `apps/kittypaw/stable.json` is manually
promoted. The default install command follows stable; use `VERSION=X.Y.Z` to
test a candidate before promotion.

## Stop / Uninstall

```bash
kittypaw server stop            # stop the running server
```

```bash
kittypaw server stop
rm /usr/local/bin/kittypaw      # remove binary
rm -rf ~/.kittypaw              # remove config and data
```

## License

[Elastic License 2.0](LICENSE)
