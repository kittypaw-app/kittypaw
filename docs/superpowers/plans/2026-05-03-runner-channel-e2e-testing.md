# Runner Channel E2E Testing Implementation Plan

> Historical plan snapshot. This document records the implementation plan or design state at the time it was written; use repository README, ARCHITECTURE.md, and app README/DEPLOY docs for the current live shape.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add deterministic local tests for chat-driven skill installation and channel delivery, then connect them to the monorepo smoke/E2E test tiers.

**Architecture:** Keep fast tests in Go packages with fake registry/API/channel fixtures. Keep full service wiring behind opt-in root targets so unit and integration failures stay easy to diagnose before Docker compose enters the loop.

**Tech Stack:** Go tests, `httptest`, existing Kittypaw engine/server/channel packages, root `Makefile`, existing Docker compose E2E harness.

**Status 2026-05-03:** Fast deterministic coverage is in place for registry
seams, chat-driven exchange-rate/weather install flows, Telegram/Kakao captured
fixtures, `/staff`, assistant mention routing, chat-created staff identities, and
reflection/evolution over `conversation_turns`. The Docker-backed
`make e2e-local` harness now also covers Portal fake-Google login, device
pairing, browser cookie session auth, Chat relay routing to a real Kittypaw
dispatcher, fake registry exchange-rate install consent, installed exchange-rate
reuse with KRW conversion, weather-now install consent with fake KittyAPI geo
resolution for Gangnam Station, and installed weather skill reuse through the
Chat BFF. The remaining real-use scenario backlog is tracked in
`apps/kittypaw/TASKS.md` under "Plan 9: Real-use scenario test expansion".

---

### Task 1: Local Registry Test Seam

**Files:**
- Modify: `apps/kittypaw/core/registry.go`
- Test: `apps/kittypaw/core/registry_test.go`

- [ ] Add a failing test proving a loopback `httptest` registry can be used only when an explicit test/local override is present.
- [ ] Implement the smallest registry client seam needed by engine integration tests without weakening the default HTTPS requirement.
- [ ] Run `go test ./core -run 'TestNewRegistryClient|TestRegistryClient' -count=1`.
- [ ] Commit as `test(kittypaw): allow explicit local registry tests`.

### Task 2: Deterministic Runner Skill Install Flows

**Files:**
- Create or modify: `apps/kittypaw/engine/install_flow_integration_test.go`

- [ ] Add fake registry helpers serving `exchange-rate` and `weather-now` package files.
- [ ] Add `TestInstallConsentInstallsAndRunsExchangeRateFromRegistry`.
- [ ] Add `TestInstallConsentInstallsAndRunsWeatherNowWithStructuredLocation`.
- [ ] Add `TestInstalledExchangeRateFollowupRunsWithoutReinstallOffer`.
- [ ] Run `go test ./engine -run 'TestInstallConsent|TestInstalledExchangeRate' -count=1`.
- [ ] Commit as `test(kittypaw): cover chat skill install flows`.

### Task 3: Channel Fixture Tests

**Files:**
- Create: `apps/kittypaw/channel/testdata/telegram/text_update.json`
- Create: `apps/kittypaw/channel/testdata/kakao/ws_incoming.json`
- Modify: `apps/kittypaw/channel/channel_test.go`
- Modify: `apps/kittypaw/server/account_isolation_test.go` or a new focused server integration test

- [ ] Add captured-shape Telegram/Kakao fixtures with sanitized IDs and tokens.
- [ ] Assert Telegram fixture parsing produces the intended `core.Event`.
- [ ] Assert Kakao relay-shaped event reaches the correct account and returns a channel response.
- [ ] Run `go test ./channel ./server -run 'Test.*(Telegram|Kakao|SkillInstall)' -count=1`.
- [ ] Commit as `test(kittypaw): add channel fixture coverage`.

### Task 4: Local Test Tier Wiring

**Files:**
- Modify: `scripts/smoke-local.sh`
- Modify: `scripts/README.md`
- Modify: `apps/kittypaw/eval/user_vision_flows/README.md`

- [ ] Add a fast deterministic Kittypaw install-flow command to `smoke-local`.
- [ ] Document which tests are deterministic and which remain LLM-judged manual/nightly evals.
- [ ] Run `bash -n scripts/smoke-local.sh` and the added Go test commands.
- [ ] Commit as `test: wire runner install flows into smoke tier`.

### Task 5: Reflection and Staff Conversation Flows

**Files:**
- Modify: `apps/kittypaw/engine/session_test.go`
- Modify: `apps/kittypaw/engine/commands_test.go`
- Modify: `apps/kittypaw/engine/reflection_test.go`
- Modify: `apps/kittypaw/engine/evolution_test.go`
- Modify: `apps/kittypaw/TASKS.md`

- [ ] Add an in-chat `/staff <staff-id>` regression test and implementation.
- [ ] Add a `@staff` assistant-mention test proving the mention is stripped from stored `conversation_turns` and the mentioned staff member's SOUL reaches the prompt.
- [ ] Add a conversation request test proving `Staff.create` can create a server-side staff identity from chat.
- [ ] Add reflection and evolution tests over `v2_conversation_turns`.
- [ ] If evolution approval/reject surface is absent, keep CI on pending proposal creation and add the missing approval UX to `apps/kittypaw/TASKS.md`.
- [ ] Commit as `test(kittypaw): cover staff and reflection conversation flows`.

### Task 6: Compose E2E Follow-up Design

**Files:**
- Create or modify: `docs/superpowers/specs/2026-05-03-compose-runner-channel-e2e-design.md`

- [ ] Document the next heavier phase: Portal, Chat, Kittyapi, Kakao relay, fake Kakao, fake registry, and Kittypaw daemon runner.
- [ ] Keep this out of the first implementation unless the deterministic tests uncover service-level seams that must be fixed now.
- [ ] Commit as `docs(test): plan compose runner channel e2e`.

### Final Verification

- [ ] Run `go test ./apps/kittypaw/core ./apps/kittypaw/engine ./apps/kittypaw/channel ./apps/kittypaw/server -count=1`.
- [ ] Run `bash -n scripts/smoke-local.sh`.
- [ ] Review `git diff --stat` and `git diff`.
- [ ] Commit remaining changes with scoped messages.
