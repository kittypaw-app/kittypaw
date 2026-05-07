# In-Chat Staff Design

## Purpose

KittyPaw staff creation from chat must make the system state match what the
assistant says. A staff member is not "created" until the persistent metadata
and `staff/<id>/SOUL.md` both exist.

This design adds a guided in-chat flow for staff discovery, creation, and
switching while preventing ghost staff identities caused by fallback SOUL
loading or low-level LLM tool calls.

## Current Problems

- `/staff <id>` can appear to switch to a staff ID even when no real staff
  exists, because missing `SOUL.md` falls back to the built-in
  `default-assistant` preset.
- `Staff.create(id, desc)` creates metadata only. It does not create
  `staff/<id>/SOUL.md`, so an LLM can claim a role was created without a real
  identity file.
- Natural-language requests such as "개발PM 만들어줘" are currently handled by
  normal LLM behavior, not a deterministic product flow.
- `@mention` routing only works for active `staff_meta` rows, while default
  fallback staff can exist on disk but remain invisible in list surfaces.

## Product Principles

- The assistant may only say a staff was created after durable creation
  succeeds.
- Natural-language creation requests require explicit opt-in to KittyPaw Staff.
- Staff creation requires a visible draft and user approval before persistence.
- Creating a staff does not automatically switch the current conversation.
- The official staff ID is a single canonical ASCII-safe identifier.
- Korean and other friendly aliases may help natural-language resolution, but
  the UI should not present multiple official names for the same staff.

## In-Chat Commands

The MVP command set is:

```text
/staff
/staff current
/staff list
/staff show <id>
/staff use <id>
/staff hire <role>
/staff cancel
```

`/staff` shows current staff, available staff, and concise usage.

`/staff current` shows the active staff for the current conversation and whether
it came from a command override, channel binding, or default config.

`/staff list` lists real active staff only. A listed staff must have metadata;
the response should show whether `SOUL.md` exists.

`/staff show <id>` shows metadata, display name, canonical ID, alias summary,
and SOUL status. It should not dump the full `SOUL.md` unless a later command
explicitly asks for it.

`/staff use <id>` switches the current conversation only if the ID or alias
resolves to a real active staff member. Missing staff must fail clearly.

`/staff hire <role>` skips the natural-language opt-in question and immediately
creates a draft for review.

`/staff cancel` cancels the current conversation's pending staff draft.

## Natural-Language Creation Flow

When a user says something like "개발PM 한 명 만들어줘", the assistant should not
create anything immediately. It should ask:

```text
KittyPaw Staff 기능으로 새 역할을 만들까요?
```

If the user agrees, the system creates a pending draft and shows it:

```text
개발 PM staff 초안입니다.

시스템 이름: dev-pm
표시 이름: 개발 PM
역할: 요구사항 정리, 일정 관리, 우선순위 조율, 진행상황 추적

SOUL.md:
...

이대로 생성할까요?
```

The user may approve, cancel, or ask for changes. Only approval commits the
staff. After successful creation, the assistant asks whether to use it in the
current conversation:

```text
개발 PM staff를 만들었어요.

시스템 이름은 dev-pm 입니다.
지금 이 대화에서 사용할까요?
```

If the user agrees, the conversation's active staff override is set. If not,
the new staff remains available but the current staff stays unchanged.

## Draft State

Each conversation may have at most one pending staff draft.

Draft key:

```text
pending_staff_draft:<conversation_id>
```

Draft payload:

```json
{
  "id": "dev-pm",
  "display_name": "개발 PM",
  "description": "요구사항 정리, 일정 관리, 우선순위 조율, 진행상황 추적",
  "aliases": ["개발PM", "개발 PM", "PM"],
  "soul": "...",
  "source": "natural_language",
  "created_at": "2026-05-08T00:00:00Z",
  "expires_at": "2026-05-09T00:00:00Z"
}
```

If another staff draft is requested while one is pending, the assistant asks
whether to replace the existing draft before generating a new one.

Drafts expire after 24 hours. Expiration avoids old approvals applying to stale
context.

## Naming And Aliases

Each staff has one canonical ID. The ID is used for files, database references,
commands, and official mentions.

Rules:

- Canonical ID uses the existing safe staff ID rules: ASCII letters, numbers,
  `_`, and `-`.
- Display name is user-facing text and may be Korean.
- Aliases may include Korean and natural phrases for resolver use.
- Aliases with spaces are allowed for natural-language resolution, but only
  space-free aliases can participate in `@mention` parsing.
- Public completion messages should show the canonical ID, not list every alias.
- Alias lookup must not allow ambiguous matches. If multiple staff match, the
  assistant asks the user to choose.
- Canonical IDs and aliases share a collision namespace for resolution. A new
  alias cannot shadow another staff's canonical ID.

Example:

```text
canonical ID: dev-pm
display name: 개발 PM
aliases: 개발PM, 개발 PM, PM
```

## Tool Surface

The chat LLM should not have a direct "create real staff now" tool.

Preferred chat-facing surface:

```text
Staff.list()
Staff.switch(id)
Staff.draft(roleOrSpec)
Staff.updateDraft(patch)
Staff.cancelDraft()
Staff.commitDraft()
```

`Staff.commitDraft()` must only succeed when the current conversation has a
pending draft and the latest user turn expresses approval.

Low-level server/store APIs may still expose direct create/update operations,
but chat should use the draft/commit flow so user-facing claims stay aligned
with durable state.

## Persistence

Committing a draft must persist both:

- `staff_meta` row for the canonical ID.
- `staff/<id>/SOUL.md` containing the approved SOUL.

Alias persistence is needed for natural-language resolution. Prefer a separate
`staff_aliases` table over embedding aliases in `staff_meta` because lookup and
collision checks are clearer.

Table shape:

```text
staff_aliases
- alias TEXT PRIMARY KEY
- staff_id TEXT NOT NULL
- created_at TEXT NOT NULL
```

Add `display_name TEXT NOT NULL DEFAULT ''` to `staff_meta`. Existing rows keep
an empty display name and continue to use their canonical ID as the visible
label when no display name is set.

## Error Handling

- `/staff use <id>` fails if the ID or alias does not resolve to an active real
  staff member.
- Draft approval fails if the draft expired or was replaced.
- Creation fails cleanly on ID collision, alias collision, filesystem write
  failure, or database write failure.
- The commit path should avoid partial creation. If one side fails, it should
  roll back or clearly report that no usable staff was created.
- If an alias is ambiguous, the assistant asks the user to pick a canonical ID.

## Testing

Focused tests should cover:

- `/staff use missing-id` does not succeed through SOUL fallback.
- `/staff hire 개발PM` creates a pending draft but no staff files or metadata.
- Approving a draft creates both `staff_meta` and `staff/<id>/SOUL.md`.
- After creation, the conversation does not switch until the user agrees.
- Natural-language creation first asks whether to use KittyPaw Staff.
- Alias resolution maps Korean aliases to a canonical ID without exposing
  multiple official names in completion copy.
- Existing default staff fallback continues to work for normal chat when no
  explicit staff is selected.

## MVP Scope

Included:

- Deterministic `/staff` command family.
- Natural-language staff creation opt-in.
- One pending staff draft per conversation.
- Approved creation of metadata plus `SOUL.md`.
- Post-creation prompt asking whether to switch.
- Korean alias resolution for natural-language matching.
- Ghost staff prevention for explicit staff switching.

Excluded:

- Staff evolution approval.
- Web UI staff editor.
- Multiple simultaneous pending drafts.
- Team approval workflows.
- Staff-specific skill equipment UX.
