# Space Chat, X Tool, and Kanban Surface Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stabilize hosted Space chat diagnostics, prevent X/Twitter requests from drifting into Gmail context, and make Kanban a first-class direct surface.

**Architecture:** Keep app boundaries explicit. `apps/space` remains a capability relay, `apps/kittypaw` owns local engine/UI/daemon behavior, and hosted Kanban is planned as a follow-up relay capability rather than a generic localhost tunnel.

**Tech Stack:** Go, chi HTTP routers, local embedded JavaScript/CSS assets, existing Go unit tests.

---

### Task 1: Space Relay Diagnostics

**Files:**
- Modify: `apps/space/internal/broker/broker.go`
- Modify: `apps/space/internal/openai/handler.go`
- Test: `apps/space/internal/openai/handler_test.go`

- [ ] Add a failing test proving a daemon `response_headers` status of 502 is passed through as a downstream response instead of being labeled as a Space origin failure.
- [ ] Add broker/openai structured logs at device register, replace, unregister, request reject, request send failure, daemon error frame, and non-2xx daemon response headers.
- [ ] Verify with `go test ./internal/broker ./internal/openai` from `apps/space`.

### Task 2: Local Chat Relay Dispatch Diagnostics

**Files:**
- Modify: `apps/kittypaw/server/chat_relay_dispatcher.go`
- Test: `apps/kittypaw/server/chat_relay_dispatcher_test.go`

- [ ] Add a failing test proving provider/engine failure returns an OpenAI-style 500 JSON body with a stable error shape.
- [ ] Add request-scoped logs for account, operation, request id, status, and internal error class without leaking tokens or full prompt content.
- [ ] Verify with `go test ./server` from `apps/kittypaw`.

### Task 3: X/Twitter Tool Guard

**Files:**
- Modify: `apps/kittypaw/engine/prompt.go`
- Test: `apps/kittypaw/engine/prompt_test.go`

- [ ] Add a failing prompt test proving the system prompt instructs Twitter/X requests to use X tools only and not substitute Gmail results.
- [ ] Add concise tool-selection guidance for explicit X/Twitter wording, including the known limitation that `X.homeTimeline` is reverse chronological and not For You.
- [ ] Verify with `go test ./engine` from `apps/kittypaw`.

### Task 4: Kanban Direct Surface Separation

**Files:**
- Modify: `apps/kittypaw/server/web/app.js`
- Modify: `apps/kittypaw/server/web/kanban.js`
- Modify: `apps/kittypaw/server/web/style.css`
- Test: `apps/kittypaw/server/web_app_test.go`
- Test: `apps/kittypaw/server/web_kanban_test.go`

- [ ] Add failing tests proving `/kanban` mounts without the settings sidebar and `/_settings` points users to the standalone Kanban surface.
- [ ] Keep settings as control/config UI and make Kanban a dedicated surface with its own shell.
- [ ] Verify with `go test ./server` from `apps/kittypaw`.

### Task 5: Final Verification

**Files:**
- Run checks only.

- [ ] Run `go test ./...` in `apps/space`.
- [ ] Run `go test ./server ./engine ./remote/chatrelay ./core` in `apps/kittypaw`.
- [ ] Run `git diff --check`.
- [ ] Review changed files before commit.
