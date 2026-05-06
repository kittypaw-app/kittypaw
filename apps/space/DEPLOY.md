# KittySpace Deploy

Deploy `apps/space` behind nginx/systemd.

## Initial Setup

```bash
cd apps/space
DEPLOY_DOMAIN=space.kittypaw.app uv run fab setup
```

The setup task installs rendered nginx/systemd templates and creates
`/home/jinto/kittyspace/.env` from `deploy/env.example` only when it does not
already exist.

Current production templates bind the process to a Unix socket owned by systemd:

```env
KITTYSPACE_BIND_ADDR=unix:/run/kittyspace/kittyspace.sock
```

## Deploy

```bash
cd apps/space
uv run fab deploy
uv run fab status
uv run fab logs
```

`fab deploy` builds a linux/amd64 `kittyspace` binary, uploads it, restarts the
`kittyspace` service, and runs production smoke.

Rollback restores the previous binary:

```bash
cd apps/space
uv run fab rollback
```

## Verify

```bash
BASE_URL=https://space.kittypaw.app bash apps/space/deploy/smoke.sh
cd apps/space && uv run fab smoke
```

This credential-free smoke checks `/health`, `/chat/`, `/assets/chat.js`,
anonymous `/chat/api/session` rejection, and the Portal Google login redirect.
Run `make -C apps/space smoke-local` before deploy to verify the full local BFF
round-trip with fake Portal and fake daemon services.

After deploying Space and provisioning real Portal credentials, run the
credentialed cutover smoke:

```bash
cd apps/space
SPACE_BASE_URL=https://space.kittypaw.app \
SPACE_USER_TOKEN=<user-access-token> \
SPACE_DEVICE_TOKEN=<device-token> \
SPACE_DEVICE_ID=<device-id> \
SPACE_LOCAL_ACCOUNT_ID=<local-account-id> \
make smoke-cutover
```

This starts a fake daemon with the device token, waits for `/v1/routes` to show
the device/account, and verifies a chat completion round-trip through Space. It
must pass before `apps/chat` is considered safe to stop.

Portal must include:

```text
SPACE_BASE_URL=https://space.kittypaw.app
WEB_REDIRECT_URI_ALLOWLIST=https://chat.kittypaw.app/auth/callback,https://space.kittypaw.app/auth/callback
CORS_ORIGINS=https://kittypaw.app,https://portal.kittypaw.app,https://connect.kittypaw.app,https://chat.kittypaw.app,https://space.kittypaw.app
```

Keep the legacy chat callback/origin during migration; remove it only after the
cutover checklist passes.
