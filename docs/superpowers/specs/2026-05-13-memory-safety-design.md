# Memory Safety Design

## Goal

Make Kittypaw's existing SQLite-backed memory safer and clearer without adding an OpenClaw-style markdown memory agent.

## Scope

- Only prompt-injected long-term memory rows should appear in `## User Memory`.
- Setup staging values, tokens, keys, secrets, credentials, and control rows must never be injected into prompts.
- `Memory.search` and `/api/v1/memory/search` should search user memory, not execution history.
- LLM-facing `Memory.get`, `Memory.set`, and `Memory.delete` should not expose setup/control/sensitive rows.
- `/setup/complete` should delete temporary `setup:*` rows after the generated config has been applied.
- Add a minimal memory management API for list, delete, export, and forget-all.
- Update `candidates.md` 17-memory to reflect this product direction.

## Out Of Scope

- Markdown memory tree as the primary store.
- Cookie-style dedicated memory agent.
- UI screens for memory management.
- Scoped memory schema migration with `kind`, `scope`, `sensitive`, or `expires_at` columns.

## Design

The existing `user_context` table remains the primary store. Prompt injection becomes opt-in by key shape: `memory:*`, `fact.*`, `pref.*`, `preference:*`, `identity:*`, and `user:*` are prompt-eligible unless the key or value looks sensitive. Setup/control prefixes and exact internal keys such as `onboarding_completed` remain queryable through lower-level store APIs but do not enter the prompt or memory tool surface.

`Store.SearchUserMemory(query, limit)` searches key and value in `user_context`, applies the same prompt-safe classification, and returns key/value pairs ordered by recency. `Memory.search` calls this method. `/api/v1/memory/search` also uses this method. Execution history search should later move to a separate history endpoint/tool.

`Memory.get`, `Memory.set`, and `Memory.delete` use the same prompt-safe boundary. This keeps stale setup rows or guessed sensitive keys from being exposed through tool calls even if old databases still contain them.

Server memory management endpoints expose only prompt-safe memory by default:

- `GET /api/v1/memory`
- `GET /api/v1/memory/search?q=...`
- `DELETE /api/v1/memory/{key}`
- `POST /api/v1/memory/forget-all`
- `GET /api/v1/memory/export`

`forget-all` deletes prompt-safe memory rows only, leaving setup/control state alone. `/chat/forget` remains conversation-only.
