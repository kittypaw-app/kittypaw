# Home Cutover Smoke Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a manual credentialed Home smoke command that verifies daemon WebSocket connection, route discovery, and chat relay against a Home origin.

**Architecture:** Add a reusable `internal/smoke.RunRemote` runner plus a thin `cmd/kittyhome-cutover-smoke` wrapper. Keep production `deploy/smoke.sh` credential-free and document the new command as a post-deploy cutover check requiring Portal-issued user/device tokens.

**Tech Stack:** Go, `github.com/coder/websocket`, Home relay protocol frames, Home in-process router tests, existing Makefile/deploy docs.

---

## File Structure

- Create `apps/home/internal/smoke/remote.go`: env config, URL derivation, fake remote daemon, route polling, chat completion call.
- Create `apps/home/internal/smoke/remote_test.go`: env validation and in-process end-to-end tests.
- Create `apps/home/cmd/kittyhome-cutover-smoke/main.go`: CLI wrapper.
- Modify `apps/home/Makefile`: add `smoke-cutover` target.
- Modify `apps/home/README.md`: document credentialed cutover smoke.
- Modify `apps/home/DEPLOY.md`: add post-deploy cutover smoke instructions.
- Modify `deploy/home/README.md`: mention manual credentialed cutover smoke.

## Tasks

### Task 1: Remote Smoke Config

- [ ] Write tests in `apps/home/internal/smoke/remote_test.go` for missing env, valid env, timeout parsing, and HTTPS to WSS derivation.
- [ ] Run `cd apps/home && go test ./internal/smoke -run 'TestRemoteConfig|TestRemoteWebSocketURL' -count=1`; expect failure because remote config does not exist.
- [ ] Add `RemoteConfig`, `LoadRemoteConfig`, and `remoteWebSocketURL` in `apps/home/internal/smoke/remote.go`.
- [ ] Run the same tests; expect pass.
- [ ] Commit with `test(home): add remote smoke config`.

### Task 2: Credentialed Remote Runner

- [ ] Add an in-process Home router test in `apps/home/internal/smoke/remote_test.go` that builds a Home router with static user/device credentials, calls `RunRemote`, and expects progress lines for daemon connected, route discovery, and chat completion.
- [ ] Run `cd apps/home && go test ./internal/smoke -run TestRunRemoteCompletesCredentialedRoundTrip -count=1`; expect failure because `RunRemote` does not exist.
- [ ] Implement `RunRemote`, fake daemon WebSocket handling, route polling, relay request validation, and chat completion verification in `apps/home/internal/smoke/remote.go`.
- [ ] Run `cd apps/home && go test ./internal/smoke -count=1`; expect pass.
- [ ] Commit with `feat(home): add credentialed cutover smoke runner`.

### Task 3: CLI And Docs

- [ ] Add `apps/home/cmd/kittyhome-cutover-smoke/main.go` that loads remote config and calls `smoke.RunRemote`.
- [ ] Add `smoke-cutover` to `apps/home/Makefile`.
- [ ] Run `cd apps/home && go test ./cmd/kittyhome-cutover-smoke -count=1`; expect package compile success.
- [ ] Run `cd apps/home && make smoke-cutover`; expect failure with missing `HOME_BASE_URL` because credentials are intentionally required.
- [ ] Update `apps/home/README.md`, `apps/home/DEPLOY.md`, and `deploy/home/README.md` with the env variables and operator flow.
- [ ] Commit with `docs(home): document cutover smoke`.

### Task 4: Final Verification And Review

- [ ] Run `cd apps/home && go test ./... -count=1`.
- [ ] Run `cd apps/home && make smoke-local`.
- [ ] Run `cd apps/home && make build && make clean`.
- [ ] Run `cd apps/home && make smoke-cutover`; expect non-zero exit with a missing env message.
- [ ] Run `bash -n apps/home/deploy/smoke.sh scripts/smoke-local.sh`.
- [ ] Run `git diff --check`.
- [ ] Review `git diff main...HEAD` for credential leakage, accidental production calls in tests, incorrect URL derivation, and accidental `apps/chat` behavior changes.
