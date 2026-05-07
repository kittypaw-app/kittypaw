# User-vision multi-turn regression smoke

LLM-in-the-loop scenarios are not unit-testable — the same query
routes through different prompts/code each run. This script pins the
canonical multi-turn flows the user actually walked through during the
"진짜 비서답게" assistant-quality work, so a future change can be
sanity-checked with one command.

Deterministic regressions for the same product surface live in Go tests and run
from the repository smoke tier first. Those tests exercise engine/channel
behavior, not removed standalone CLI staff/reflection commands: skill
install/run, installed-skill reuse, assistant mention routing, in-chat
`/staff`, reflection over `conversation_turns`, staff identity evolution pending
proposals, and Telegram/Kakao fixture conversion. This eval remains the slower
behavior-quality check with an LLM judge.

## Why a separate path from `eval/secretary_smoke/`

`secretary_smoke` is single-turn fixtures with an LLM judge — designed
for behavior-class breadth (vague / domain / weak_serp / framing /
stale). This directory is for **multi-turn flows tied to specific
user-visible commits** (clarify → install → browse → chitchat).
The runner now uses behavior-level LLM judging rather than exact
substring checks, so equivalent Anthropic/OpenAI/Gemini phrasing can
pass as long as the user-visible outcome is right.

## Run

Build first, then:

```bash
./eval/user_vision_flows/run.sh                # all flows
FLOW=clarify ./eval/user_vision_flows/run.sh   # just one
KITTYPAW_EVAL_PROVIDER=openai ./eval/user_vision_flows/run.sh
```

Each flow stops the server, wipes installed packages/skills, then
pipes a multi-turn input into `kittypaw chat`. The cleaned transcript
is judged with `JUDGE_MODEL` (default Claude Haiku) using the behavior
baselines in `provider_baselines.json`.

## Flows

| Flow | Sequence | Validates |
|---|---|---|
| `clarify` | "엔화는?" | Clarifies ambiguity and avoids unsupported numeric exchange-rate fabrication. |
| `install_chitchat` | 환율 알려줘 → 네 → 오 잘하네! | Provides exchange-rate data, acknowledges install/readiness, and handles praise naturally. |
| `install_explicit_request` | 엔화는? → 네 → 설치해줘요. | Follows through on the explicit install request and avoids a repeated "which skill?" loop. |
| `installed_dispatch` | 환율 알려줘 → 네 → 환율 | Uses the already-installed capability instead of offering installation again. |
| `intent_aligned` | 환율 알려줘 → 네 → 원화로 환율 | Reframes the exchange-rate answer around KRW/Korean won. |
| `browse` | 어떤 스킬들이 있어요? | Lists available skills/categories without auto-installing. |
| `multimatch` | 뉴스 관련 스킬 있어요? | Presents multiple news-related choices without auto-installing one. |

## Provider Baselines

`provider_baselines.json` has a `default` baseline plus override sections
for `anthropic`, `openai`, and `gemini`. Select the family with
`KITTYPAW_EVAL_PROVIDER`. Provider-specific differences should be encoded
as behavior/threshold overrides, not as exact wording expectations.

## When to add a flow

Whenever a commit has the shape "fixed user-visible chat regression
that took N tries to land": add the canonical sequence here so the
next refactor catches a re-break before the user does.

## When to update assertions

Prefer adding or adjusting behavior definitions/baselines over matching
literal text. Keep deterministic substring checks only for hard
anti-patterns that should never appear.
