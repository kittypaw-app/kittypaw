# Home Chat Smoke Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Home smoke catch browser BFF regressions before chat cutover.

**Architecture:** Extend `apps/home/internal/smoke` with a fake Portal auth server and a Home webapp handler wired into the in-process router. Keep production smoke credential-free but make it check browser assets, anonymous API behavior, and login redirect shape.

**Tech Stack:** Go 1.25, chi, `httptest`, `github.com/coder/websocket`, shell smoke with `curl`, `jq`, and `grep`.

---

## File Structure

- Modify `apps/home/internal/smoke/local.go`: add fake Portal exchange, BFF session flow, and JSON/SSE daemon responses.
- Modify `apps/home/internal/smoke/local_test.go`: assert BFF progress and add focused BFF failure tests.
- Modify `apps/home/deploy/smoke.sh`: add credential-free production checks.
- Modify `apps/home/README.md`: document local and production smoke guarantees.
- Modify `apps/home/DEPLOY.md`: document production smoke checks and required tools.

## Tasks

### Task 1: Add Local BFF Smoke Coverage

- [ ] Write a failing test in `apps/home/internal/smoke/local_test.go` that expects `RunLocal` output to include `bff login` and `bff chat completion`.
- [ ] Run `cd apps/home && go test ./internal/smoke -run TestRunLocalCompletesChatRoundTrip -count=1`; expected failure because BFF smoke is not implemented.
- [ ] Update `apps/home/internal/smoke/local.go`:
  - `localRouter` returns the Home router plus a fake Portal server cleanup.
  - Add a `webapp.New` handler configured with the fake Portal `/auth` base.
  - Add `runBFFLogin`, `runBFFRoutes`, and `runBFFChatCompletion`.
  - Teach the fake daemon to serve two requests: direct stream and BFF JSON.
- [ ] Run `cd apps/home && go test ./internal/smoke -count=1`; expected pass.
- [ ] Commit as `test(home): extend local smoke through chat bff`.

### Task 2: Strengthen Production Smoke And Docs

- [ ] Update `apps/home/deploy/smoke.sh` to assert:
  - `/health` returns `status == healthy`.
  - `/chat/` contains `home-chat-root`.
  - `/assets/chat.js` contains `/chat/api/routes`.
  - anonymous `/chat/api/session` returns `401`.
  - `/auth/login/google` returns a `302` with a `Location` containing `/web/google`.
- [ ] Run `bash -n apps/home/deploy/smoke.sh`; expected pass.
- [ ] Update `apps/home/README.md` and `apps/home/DEPLOY.md` with local/prod smoke guarantees.
- [ ] Run `cd apps/home && make smoke-local`; expected direct and BFF progress lines.
- [ ] Commit as `docs(home): document smoke hardening`.

### Task 3: Final Verification And Review

- [ ] Run `gofmt -w apps/home/internal/smoke`.
- [ ] Run `cd apps/home && go test ./... -count=1`.
- [ ] Run `cd apps/portal && go test ./... -count=1`.
- [ ] Run `cd apps/kittypaw && go test ./core ./cli ./remote/chatrelay ./server -count=1`.
- [ ] Run `make contracts-check`.
- [ ] Run `cd apps/home && make build && make smoke-local && make clean`.
- [ ] Run `bash -n apps/home/deploy/smoke.sh apps/portal/deploy/smoke.sh`.
- [ ] Run `git diff --check`.
- [ ] Perform a review pass over the final diff, fix any issues, and rerun affected tests.
