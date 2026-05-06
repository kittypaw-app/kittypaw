# Home Deploy Automation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add production-ready Fabric deployment automation for `apps/home`.

**Architecture:** Add a Home-specific `fabfile.py`, convert hardcoded Home nginx/systemd files into rendered templates, and pin the deployment contract with local tests. Keep this slice limited to deployment automation; no production deployment or `apps/chat` cutover happens here.

**Tech Stack:** Python Fabric task file, Go unit tests for deployment templates, bash smoke scripts, existing Go Home service.

---

## File Structure

- Create `apps/home/fabfile.py`: Home `setup`, `deploy`, `smoke`, `rollback`, `status`, and `logs` tasks.
- Create `apps/home/deploy/template_test.go`: regression tests for service and nginx templates.
- Modify `apps/home/deploy/kittyhome.service`: renderable systemd template with user/group/runtime directory.
- Modify `apps/home/deploy/kittyhome.nginx`: renderable nginx template with explicit daemon WebSocket location.
- Modify `apps/home/DEPLOY.md`: operator workflow using `uv run fab`.
- Modify `deploy/home/README.md`: root deployment summary.
- Modify `scripts/smoke-local.sh`: include Home deploy shell and Python syntax checks.

## Tasks

### Task 1: Pin Deployment Template Contract

- [ ] Write failing Go tests in `apps/home/deploy/template_test.go` that read `kittyhome.service` and `kittyhome.nginx`.
- [ ] Assert the service template contains `User={{SERVICE_USER}}`, `Group={{SERVICE_GROUP}}`, `WorkingDirectory={{REMOTE_DIR}}`, `EnvironmentFile={{REMOTE_DIR}}/.env`, `ExecStart={{REMOTE_DIR}}/kittyhome`, and `RuntimeDirectory=kittyhome`.
- [ ] Assert the nginx template contains `server_name {{DOMAIN}}`, an upstream named `kittyhome`, a `/daemon/` location, `proxy_set_header Upgrade $http_upgrade`, `proxy_set_header Connection "upgrade"`, and `proxy_read_timeout 86400s`.
- [ ] Run `cd apps/home && go test ./deploy -count=1`; expect failure against the current hardcoded templates.
- [ ] Update the service and nginx templates to satisfy the tests.
- [ ] Run `cd apps/home && go test ./deploy -count=1`; expect pass.
- [ ] Commit with `test(home): pin deploy templates`.

### Task 2: Add Home Fabric Tasks

- [ ] Create `apps/home/fabfile.py` with constants and helpers matching Home naming and paths.
- [ ] Implement `_local_build()` using `GOOS=linux`, `GOARCH=amd64`, `CGO_ENABLED=0`, output `kittyhome-linux`, and linker variables `main.version` and `main.commit`.
- [ ] Implement `setup()` to require or default `DEPLOY_DOMAIN=home.kittypaw.app`, render `{{DOMAIN}}`, `{{REMOTE_DIR}}`, `{{SERVICE_USER}}`, and `{{SERVICE_GROUP}}`, install nginx/systemd files, enable the service, reload nginx, and create `.env` only when absent.
- [ ] Implement `deploy()` with `.prev` backup, `.new` upload, restart, active check, and `smoke(ctx)`.
- [ ] Implement `smoke()`, `rollback()`, `status()`, and `logs()` following existing hosted service behavior.
- [ ] Run `python3 -m py_compile apps/home/fabfile.py`; expect pass.
- [ ] Run `cd apps/home && make build && make clean`; expect pass and no retained binary.
- [ ] Commit with `feat(home): add fabric deploy automation`.

### Task 3: Document And Wire Local Smoke

- [ ] Update `apps/home/DEPLOY.md` with `uv run fab setup`, `uv run fab deploy`, `uv run fab smoke`, `uv run fab rollback`, `uv run fab status`, and `uv run fab logs`.
- [ ] Update `deploy/home/README.md` to mention `apps/home/fabfile.py` and production Home service ownership.
- [ ] Update `scripts/smoke-local.sh` so deploy shell syntax includes `apps/home/deploy/smoke.sh`, deploy Python syntax includes `apps/home/fabfile.py`, Go tests include `./apps/home/...`, and hosted relay in-process smoke includes `apps/home`.
- [ ] Run `bash -n apps/home/deploy/smoke.sh`.
- [ ] Run `python3 -m py_compile apps/home/fabfile.py`.
- [ ] Run `cd apps/home && make smoke-local`.
- [ ] Commit with `docs(home): document deploy automation`.

### Task 4: Final Verification And Review

- [ ] Run `cd apps/home && go test ./... -count=1`.
- [ ] Run `cd apps/home && make smoke-local`.
- [ ] Run `cd apps/home && make build && make clean`.
- [ ] Run `bash -n apps/home/deploy/smoke.sh apps/portal/deploy/smoke.sh`.
- [ ] Run `python3 -m py_compile apps/home/fabfile.py apps/kittyapi/fabfile.py apps/portal/fabfile.py apps/chat/fabfile.py apps/kakao/fabfile.py`.
- [ ] Run `git diff --check`.
- [ ] Review `git diff main...HEAD` for deployment safety issues, especially hardcoded paths, missing placeholders, untracked binaries, and accidental `apps/chat` behavior changes.
