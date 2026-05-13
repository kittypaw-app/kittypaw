# Memory Safety Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Kittypaw's DB-backed memory prompt-safe, searchable as memory, and minimally user-manageable.

**Architecture:** Add store-level memory classification and query helpers around `user_context`. Reuse those helpers from prompt construction, `Memory.search`, setup completion cleanup, and server memory APIs. Keep execution-history search separate from memory semantics.

**Tech Stack:** Go, SQLite store package, existing engine tool executor, chi server routes, Go unit tests.

---

### Task 1: Store Memory Safety

**Files:**
- Modify: `apps/kittypaw/store/store.go`
- Test: `apps/kittypaw/store/store_test.go`

- [x] Add tests proving `MemoryContextLines` excludes `setup:*` and sensitive key/value rows while keeping `memory:*`, `fact.*`, and `pref.*`.
- [x] Add `SearchUserMemory(query, limit)`, `ListUserMemory(limit)`, `SetUserMemory`, `GetUserMemory`, and safe delete helpers.
- [x] Verify store tests with `go test ./apps/kittypaw/store -run 'TestMemoryContextLines|TestUserMemory' -count=1`.

### Task 2: Engine Memory Search

**Files:**
- Modify: `apps/kittypaw/engine/executor.go`
- Test: `apps/kittypaw/engine/executor_test.go`

- [x] Add a test proving `Memory.search("korean")` returns `user_context` memory and does not return execution history rows.
- [x] Change `executeMemory` search to call `Store.SearchUserMemory`.
- [x] Restrict LLM-facing `Memory.get`, `Memory.set`, and `Memory.delete` to prompt-safe user memory rows.
- [x] Verify with `go test ./apps/kittypaw/engine -run TestExecuteMemorySearchUsesUserMemory -count=1`.

### Task 3: Setup Cleanup And Server APIs

**Files:**
- Modify: `apps/kittypaw/server/api_setup.go`
- Modify: `apps/kittypaw/server/api.go`
- Modify: `apps/kittypaw/server/server.go`
- Test: `apps/kittypaw/server/api_setup_account_test.go`
- Test: `apps/kittypaw/server/api_memory_test.go`

- [x] Add a setup completion test proving `setup:*` rows are deleted after successful completion.
- [x] Add server API tests for memory list, search, delete, export, and forget-all.
- [x] Implement cleanup and API handlers.
- [x] Verify with `go test ./apps/kittypaw/server -run 'TestMemoryAPIManagesPromptSafeUserMemory|TestSetupCompleteRefreshesLoggedInAccountRuntime' -count=1`.

### Task 4: Documentation And Verification

**Files:**
- Modify: `/Users/jinto/projects/kittypaw/candidates.md`

- [x] Update `17-memory` to lower markdown memory agent parity and raise prompt safety, memory search contract, setup cleanup, and user control.
- [x] Run `go test ./apps/kittypaw/...`.
- [x] Run `git diff --check`.
