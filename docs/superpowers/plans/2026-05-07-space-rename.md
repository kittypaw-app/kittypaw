# Space Rename Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rename the hosted Home service to Space across app code, contracts, Portal discovery/audience, Kittypaw discovery consumers, deploy files, and docs.

**Architecture:** Move `apps/home` to `apps/space`, update module/import/config/discovery names mechanically, then repair tests and docs. This is a pre-production breaking rename, so no `home_base_url` compatibility layer is kept.

**Tech Stack:** Go monorepo modules in `go.work`, Portal discovery contracts, bash deploy smoke, Python Fabric deploy files.

---

## File Structure

- Move `apps/home` -> `apps/space`.
- Move `deploy/home` -> `deploy/space`.
- Modify `go.work`.
- Modify `AGENTS.md`.
- Modify `deploy/README.md`, `scripts/smoke-local.sh`, and deployment docs.
- Modify `contracts/discovery/*` and `contracts/auth/*` examples/docs.
- Modify Portal config/auth/discovery tests and implementation.
- Modify Kittypaw discovery/token/login/relay tests and implementation.

## Tasks

### Task 1: Move Space App And Module

- [ ] Move `apps/home` to `apps/space` and update `go.work`.
- [ ] Replace `kittyhome` with `kittyspace`, `KittyHome` with `KittySpace`, `KITTYHOME` with `KITTYSPACE`, and `github.com/kittypaw-app/kittyhome` with `github.com/kittypaw-app/kittyspace` under `apps/space`.
- [ ] Replace `home.kittypaw.app` with `space.kittypaw.app` under `apps/space`.
- [ ] Rename command directories to `cmd/kittyspace`, `cmd/kittyspace-smoke`, and `cmd/kittyspace-cutover-smoke`.
- [ ] Rename deployment files to `kittyspace.service` and `kittyspace.nginx`.
- [ ] Run `cd apps/space && go test ./... -count=1`; expect failures only from cross-app references not yet renamed.
- [ ] Commit with `refactor(space): rename home app`.

### Task 2: Contracts, Portal, And Kittypaw Rename

- [ ] Replace discovery key `home_base_url` with `space_base_url` in contracts and examples.
- [ ] Replace Portal `HOME_BASE_URL` config with `SPACE_BASE_URL`.
- [ ] Rename Portal audience constant `AudienceHome` to `AudienceSpace` and value to `https://space.kittypaw.app`.
- [ ] Replace Kittypaw stored key and discovery field from Home to Space.
- [ ] Ensure hosted relay selection prefers `space_base_url`, then falls back to `chat_relay_url`.
- [ ] Run `make contracts-check`.
- [ ] Run focused tests for Portal and Kittypaw discovery/relay packages.
- [ ] Commit with `refactor(space): rename discovery and audience`.

### Task 3: Docs And Smoke Wiring

- [ ] Move `deploy/home` to `deploy/space` and update root deploy docs.
- [ ] Update `AGENTS.md` app ownership from Home to Space.
- [ ] Update `scripts/smoke-local.sh` to use `apps/space` and Space fabfile/smoke paths.
- [ ] Update all relevant docs under `apps/space`, `deploy/space`, and `docs/superpowers` from Home public naming to Space.
- [ ] Run shell/Python syntax checks for Space deploy assets.
- [ ] Commit with `docs(space): update deployment naming`.

### Task 4: Final Verification And Review

- [ ] Run `cd apps/space && go test ./... -count=1`.
- [ ] Run `cd apps/space && make smoke-local`.
- [ ] Run `cd apps/space && make build && make clean`.
- [ ] Run `cd apps/space && make smoke-cutover`; expect non-zero exit with missing `SPACE_BASE_URL`.
- [ ] Run `cd apps/portal && go test ./... -count=1`.
- [ ] Run `cd apps/kittypaw && go test ./core ./cli ./remote/chatrelay ./server -count=1`.
- [ ] Run `make contracts-check`.
- [ ] Run `bash -n apps/space/deploy/smoke.sh scripts/smoke-local.sh`.
- [ ] Run `PYTHONPYCACHEPREFIX=/private/tmp/kitty-pycache-space python3 -m py_compile apps/space/fabfile.py apps/kittyapi/fabfile.py apps/portal/fabfile.py apps/chat/fabfile.py apps/kakao/fabfile.py`.
- [ ] Run `git diff --check`.
- [ ] Review `git diff main...HEAD` for remaining `apps/home`, `home_base_url`, `HOME_BASE_URL`, `KITTYHOME`, `kittyhome`, and `home.kittypaw.app` references.
