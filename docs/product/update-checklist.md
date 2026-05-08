# Product Concept Documentation Checklist

Use this checklist whenever a change adds or changes a user-visible KittyPaw
concept, command, tool, route, or surface.

## Quick Rule

If a user can say "KittyPaw has X now", update the product concept docs.

Required docs:

- `docs/product/concepts.md`
- `docs/product/surfaces.md`
- `docs/product/architecture-map.md` when the flow crosses app/runtime
  boundaries
- this checklist when a new update rule is discovered

## Change Types

### New Concept

Examples: Staff, Team Space, Browser/CDP, hosted Space route.

Checklist:

- [ ] Add the concept to `concepts.md`.
- [ ] Name the source of truth.
- [ ] Name the owning app.
- [ ] Define lifecycle states.
- [ ] Define what counts as "created" or "available".
- [ ] Add surface exposure to `surfaces.md`.
- [ ] Add an architecture flow if it crosses daemon, hosted service, channel,
      or account boundaries.
- [ ] Add at least one scenario showing how a user reaches it.

### New CLI Command

Checklist:

- [ ] Add or update the relevant concept row in `surfaces.md`.
- [ ] Decide whether CLI is primary or supported.
- [ ] Document whether the command mutates durable state.
- [ ] Document account selection behavior if account state is touched.
- [ ] Add tests for false success on missing state.

### New Web Surface

Examples: `/_settings`, `/chat`, `/kanban`, a future staff editor.

Checklist:

- [ ] Add the surface to `surfaces.md` if it is a new surface type.
- [ ] Update concept rows for concepts shown or mutated there.
- [ ] Document the local route and hosted route separately.
- [ ] Document auth/session behavior.
- [ ] Document whether it calls `/api/settings`, `/api/chat`, or `/api/v1`.
- [ ] Add smoke coverage for local route.
- [ ] Add hosted smoke coverage before marking hosted support.

### New In-Chat Slash Command

Checklist:

- [ ] Add the command to the concept-specific policy section in `surfaces.md`.
- [ ] State why it is deterministic instead of natural-language only.
- [ ] Document the exact durable state it can mutate.
- [ ] Add missing-state tests.
- [ ] Add ambiguous-input tests if it resolves aliases/names.
- [ ] Keep command output aligned with actual source of truth.

### New Natural-Language Chat Intent

Checklist:

- [ ] Add the intent to `surfaces.md`.
- [ ] State whether it creates a draft, asks consent, or performs direct action.
- [ ] Define the confirmation phrase/state required before durable mutation.
- [ ] Add tests for "assistant claims success but state does not exist".
- [ ] Add tests for cancellation and stale pending state if a draft is involved.
- [ ] Update prompt/tool guidance if the LLM can reach the concept.

### New Tool Global Or Method

Checklist:

- [ ] Update `core.SkillRegistry`.
- [ ] Add concept/surface docs explaining the product meaning.
- [ ] Add executor dispatch tests.
- [ ] Add prompt guidance when the tool has easy-to-confuse boundaries.
- [ ] Document whether it is read-only, write, destructive, or external-cost.
- [ ] If it creates durable state, ensure there is a deterministic confirmation
      path or explicit user request requirement.

### New Skill/Package Behavior

Checklist:

- [ ] Distinguish built-in tool global vs installed skill/package.
- [ ] Update `surfaces.md` if users can install/run/configure it in a new way.
- [ ] Document registry consent requirements.
- [ ] Document scheduler delivery requirements if it can run proactively.
- [ ] Add package contract or fixture tests if it affects wire behavior.

### New Channel Behavior

Checklist:

- [ ] Update the channel delivery table in `concepts.md`.
- [ ] Update channel policy in `surfaces.md`.
- [ ] State inbound, current-reply, and proactive-outbound capability.
- [ ] State what identity is stable, if any (`chat_id`, callback action ID,
      user key, channel ID).
- [ ] Add tests for scheduled delivery if proactive outbound is claimed.
- [ ] If third-party policy or pricing matters, add an operations note.

### New Hosted Space Capability

Checklist:

- [ ] Document local route and hosted route separately.
- [ ] Document relay/API path.
- [ ] Document auth/session requirements.
- [ ] Add hosted route to `architecture-map.md`.
- [ ] Add local smoke and hosted smoke coverage.
- [ ] Do not mark a concept as hosted just because the local daemon supports it.

## Scenario Template

Use this shape for use case scenarios:

```text
Title:
  One sentence.

Surface:
  CLI | setup | web control | web chat | web kanban | slash | natural chat | hosted space

Concepts:
  Account, Staff, Skill, Workspace, Project, ...

User says/does:
  Exact command, click path, or natural-language request.

Expected system path:
  Deterministic command, pipeline intent, LLM tool, API route, or relay path.

Source of truth after success:
  Files, DB rows, config, external token, relay registration, etc.

Failure modes:
  Missing state, ambiguous alias, no auth, no proactive channel, stale draft,
  external quota, hosted route missing.
```

## Review Questions

Ask these before merging a concept/surface change:

- Can a user tell where to configure it?
- Can a user tell how to use it from chat?
- Is the source of truth explicit?
- Is hosted support separate from local support?
- Does the assistant ever claim success before durable state exists?
- Does the feature behave differently in CLI, web, slash command, and natural
  language? If yes, is that difference documented?
- Is a use case scenario now easier to write from the docs alone?

