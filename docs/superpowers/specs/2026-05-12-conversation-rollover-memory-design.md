# Conversation Rollover And Memory Distillation Design

## Goal

KittyPaw should prevent general chat threads from growing without bound. When a
general conversation becomes too long, KittyPaw automatically starts a new
conversation, carries forward only conservative long-term memory, and tells the
user that it has continued in a fresh thread. When the topic appears to change,
KittyPaw should suggest splitting the conversation, but should not auto-split on
semantic judgment in the first version.

## Current Context

The storage foundation already exists:

- `conversations` is the first-class thread index.
- `v2_conversation_turns` stores turns by `conversation_id`.
- Checkpoints and persistent compactions are scoped by `conversation_id`.
- `CompactConversationByID` can summarize older raw turns for prompt use.
- `MemoryContextLines()` injects selected `user_context` rows into prompts.

The missing product layer is the current-channel route and rollover policy:

- A stable Web/CLI/Telegram/Slack channel may keep resolving to the same
  conversation forever.
- There is no durable parent/child link between conversations.
- There is no automatic distillation step that promotes only useful durable
  memory out of an old conversation.
- There is no user-visible explanation that a new conversation has started.

## Product Policy

Length-based rollover is automatic. If a general conversation crosses the
configured threshold, the next user message is handled in a fresh child
conversation and the user is informed in the assistant response.

Topic-shift rollover is advisory. If the system detects a probable topic change,
the assistant may ask whether to split into a new conversation. It must not
automatically switch solely because a semantic detector said the topic changed.

Only general conversations roll over automatically. Project and ticket scoped
conversations are explicit work units and stay under user control.

## Rollover Criteria

Default policy:

```text
enabled: true
max_turns: 80
max_estimated_tokens_ratio: 0.65
min_turns_before_rollover: 20
```

A conversation is eligible for automatic rollover when:

- it is scope type `general`;
- it is not already handling a rollover operation;
- it has at least `min_turns_before_rollover` turns; and
- either its turn count exceeds `max_turns`, or its loaded prompt/history token
  estimate exceeds `context_window * max_estimated_tokens_ratio`.

The first implementation should make these constants internal defaults. A later
iteration can expose them in config once behavior is proven.

## Data Model

Add durable metadata instead of overloading `user_context`.

### conversations

Add nullable/defaulted columns:

```sql
parent_conversation_id TEXT NOT NULL DEFAULT ''
rollover_reason TEXT NOT NULL DEFAULT ''
rollover_from_turn_id INTEGER NOT NULL DEFAULT 0
```

`parent_conversation_id` points to the prior conversation. `rollover_reason`
uses values such as `length_turns`, `length_tokens`, or `manual_topic_split`.
`rollover_from_turn_id` records the last turn in the parent at the time of
rollover.

### conversation_routes

Create a route table:

```sql
CREATE TABLE IF NOT EXISTS conversation_routes (
    route_key TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    source_channel TEXT NOT NULL DEFAULT '',
    source_session_id TEXT NOT NULL DEFAULT '',
    chat_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
```

Route keys are derived from source channel and stable transport identity. For
Web/Desktop/Kakao, prefer `session_id` then `chat_id`. For Telegram/Slack-like
channels, prefer `chat_id` then `session_id`. A route points the inbound channel
to the currently active general conversation.

Project/ticket conversation IDs supplied explicitly by payload bypass this route
table.

## Runtime Flow

Before loading conversation state in `Session.runAgentLoop`:

1. Resolve the current conversation from explicit `conversation_id`, existing
   project/ticket scope, or `conversation_routes`.
2. If there is no route for a general channel, create or reuse the stable
   general conversation and create a route to it.
3. Check whether that conversation is eligible for automatic rollover.
4. If eligible, distill conservative memory from the parent conversation.
5. Create a child general conversation with parent metadata.
6. Update the route to the child conversation.
7. Store the current user turn in the child conversation.
8. Prefix or otherwise include a short user-visible notice in the assistant
   response.

The notice should be concise and visually separate from the carried-forward
state:

```text
* Conversation rolled over

────────────────────────────────

상태를 이어받았습니다. 대화가 길어져 새 대화로 정리해서 이어갑니다.
이전 대화의 중요한 기억만 반영했습니다.
```

The notice is part of the assistant response and is recorded in the child
conversation history.

Same-thread persistent compaction can use the same visual language without
creating a child conversation:

```text
* Context compacted

────────────────────────────────

상태를 이어받았습니다. 오래된 대화 내용을 요약해 현재 맥락에 반영했습니다.
```

The first implementation only needs the rollover notice. The compaction notice
is documented so future `/compact` or automatic compaction UX uses the same
pattern.

## Memory Distillation

The first version stores only conservative memory. It must not store raw tool
results, secrets, large file contents, web page bodies, or arbitrary transcript
summaries.

Allowed categories:

- `preference`: durable user preference.
- `decision`: explicit decision or commitment.
- `ongoing_task`: work still in progress.
- `open_question`: unresolved question that matters later.
- `state`: active project, ticket, staff, or model state when relevant.

Use explicit key prefixes:

```text
memory:preference:<hash>
memory:decision:<hash>
memory:ongoing_task:<hash>
memory:open_question:<hash>
memory:state:<hash>
```

The distiller should write via `SetUserContext(..., source="conversation_rollover")`.

Prompt injection should include these memory rows through `MemoryContextLines()`.
Internal route/control state must be excluded from prompt memory. Exclude at
least:

```text
current_project:*
conversation_route:*
rollover_pending:*
pending_staff_*
active_staff:*
```

## Distillation Engine

Use a bounded LLM call with a structured JSON contract. The input is the parent
conversation's recent and compacted content, capped by token budget. The output
schema:

```json
{
  "memories": [
    {
      "category": "preference",
      "key": "short-stable-key",
      "value": "one concise durable fact",
      "confidence": 0.92,
      "reason": "why this should survive rollover"
    }
  ]
}
```

Store only items with allowed category, non-empty key/value, confidence at or
above the implementation threshold, and values below the configured length cap.
On distillation failure, still create the child conversation and route update;
log the failure and tell the user only that the conversation was continued in a
new thread.

## Topic Shift Advisory

Topic-shift detection is advisory in the first version. The detector can use
simple heuristics first:

- first user message after a long idle gap;
- low lexical overlap with recent user messages;
- explicit phrases such as "다른 얘기인데", "new topic", or "separate question".

When triggered, the assistant may ask whether to split the thread. Approval can
be implemented as a later deterministic command or pipeline branch:

```text
새 대화로 분리할까요?
```

No automatic topic-based route change is allowed in v1.

## User-Facing Diagnostics

Extend `/session` over time to show:

- current `conversation_id`;
- parent conversation ID when present;
- route key/source when present;
- rollover reason for child conversations;
- turn count and latest checkpoint;
- whether the current conversation is near rollover threshold.

Extend `/context` over time to show rollover threshold status alongside current
prompt/context estimates.

## Safety And Failure Modes

- Rollover must be idempotent for one inbound turn. Repeated retries should not
  create multiple child conversations.
- Rollover should not happen for project/ticket scopes.
- Rollover must not delete parent turns.
- Distillation failure must not block the user's message.
- Memory writes must be capped, sanitized, and category-filtered.
- Route updates should be transactional with child conversation creation.
- If route lookup fails, fall back to existing conversation resolution and log a
  warning rather than dropping the message.

## Tests

Store tests:

- creating a child conversation records parent metadata;
- route upsert and lookup select the active conversation;
- route update is scoped to one route key;
- conversation list/info exposes parent metadata.

Engine tests:

- length threshold creates one child conversation and records the current user
  turn in the child;
- project/ticket conversations do not auto-rollover;
- distillation writes only allowed `memory:*` keys;
- distillation failure still rolls over and answers;
- user-visible rollover notice appears once;
- topic-shift detector suggests but does not auto-switch.

Server/API tests:

- conversation info returns parent/rollover metadata;
- chat through the same source route uses the child after rollover.

Prompt/memory tests:

- `MemoryContextLines()` includes `memory:*` rows;
- route/control keys do not leak into prompt memory.
