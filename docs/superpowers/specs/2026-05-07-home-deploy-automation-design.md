# Home Deploy Automation Design

Date: 2026-05-07
Status: Approved for implementation planning

## Purpose

Make `apps/home` deployable with the same local operator workflow as the other
hosted KittyPaw services. The Home chat surface now has service code, deployment
templates, and smoke coverage, but it does not yet have the Fabric automation
needed to install, deploy, smoke, inspect, and roll back the production service.

This slice closes that deployment gap before any `apps/chat` cutover work. It
does not deploy to a server and does not stop or modify `apps/chat`.

## Scope

Implement Home deployment automation for:

- Initial server setup with nginx and systemd templates.
- Linux/amd64 static binary build with version and commit metadata.
- Binary upload, restart, active-service check, and production smoke.
- Rollback to the previous binary.
- Status and logs commands.
- Local syntax and template tests so deployment drift is caught in CI/local
  smoke.

Out of scope:

- Running Fabric against production.
- Changing Portal credentials, DNS, TLS, or CORS configuration.
- Removing, redirecting, or decommissioning `apps/chat`.
- Adding Kanban endpoints to Home.

## Architecture

Add `apps/home/fabfile.py` modeled on the existing Go hosted services while
using Home-specific names:

```text
SERVICE=kittyhome
BINARY=kittyhome
REMOTE_DIR=/home/jinto/kittyhome
DEFAULT_DOMAIN=home.kittypaw.app
```

The Fabric tasks are `setup`, `deploy`, `smoke`, `rollback`, `status`, and
`logs`. `setup` renders template placeholders into temporary remote files and
creates a `.env` from `apps/home/deploy/env.example` only when one does not
already exist. `deploy` builds `./cmd/kittyhome` for linux/amd64 with
`main.version` and `main.commit` linker values, uploads it through a `.new`
staging filename, preserves `.prev`, restarts `kittyhome`, then runs
`deploy/smoke.sh`.

The systemd template should be a real template rather than hardcoded to one
operator path. It uses `{{REMOTE_DIR}}`, `{{SERVICE_USER}}`, and
`{{SERVICE_GROUP}}`. It also owns `/run/kittyhome` through `RuntimeDirectory`
because the current production env binds to `unix:/run/kittyhome/kittyhome.sock`.

The nginx template should be rendered through `{{DOMAIN}}`. It keeps the daemon
WebSocket path explicit with upgrade headers and long read timeout, then proxies
all other Home routes to the same unix socket.

## Testing

Tests should verify deployment assets without contacting a server:

- Python syntax for `apps/home/fabfile.py`.
- Shell syntax for `apps/home/deploy/smoke.sh`.
- Go tests for `apps/home`.
- Local Home smoke.
- Template regression tests proving the service/nginx files still contain the
  placeholders and operational directives that `fab setup` depends on.
- Root `scripts/smoke-local.sh` includes Home deploy syntax checks so future
  repository smoke runs cover the new service.

## Risks And Mitigations

- A hardcoded service template can silently install to the wrong path. Template
  regression tests pin placeholders for `REMOTE_DIR`, `SERVICE_USER`, and
  `SERVICE_GROUP`.
- `/run/kittyhome` can disappear after reboot. systemd owns it with
  `RuntimeDirectory=kittyhome`.
- WebSockets can fail through nginx if upgrade headers drift. nginx template
  tests pin the `/daemon/` location and upgrade headers.
- A broken fabfile can escape Go tests. Python compile checks are added to both
  feature verification and root local smoke.
