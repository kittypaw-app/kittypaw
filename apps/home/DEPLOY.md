# KittyHome Deploy

Deploy `apps/home` behind nginx/systemd.

## Initial Setup

```bash
cd apps/home
DEPLOY_DOMAIN=home.kittypaw.app uv run fab setup
```

The setup task installs rendered nginx/systemd templates and creates
`/home/jinto/kittyhome/.env` from `deploy/env.example` only when it does not
already exist.

Current production templates bind the process to a Unix socket owned by systemd:

```env
KITTYHOME_BIND_ADDR=unix:/run/kittyhome/kittyhome.sock
```

## Deploy

```bash
cd apps/home
uv run fab deploy
uv run fab status
uv run fab logs
```

`fab deploy` builds a linux/amd64 `kittyhome` binary, uploads it, restarts the
`kittyhome` service, and runs production smoke.

Rollback restores the previous binary:

```bash
cd apps/home
uv run fab rollback
```

## Verify

```bash
BASE_URL=https://home.kittypaw.app bash apps/home/deploy/smoke.sh
cd apps/home && uv run fab smoke
```

This credential-free smoke checks `/health`, `/chat/`, `/assets/chat.js`,
anonymous `/chat/api/session` rejection, and the Portal Google login redirect.
Run `make -C apps/home smoke-local` before deploy to verify the full local BFF
round-trip with fake Portal and fake daemon services.

Portal must include:

```text
HOME_BASE_URL=https://home.kittypaw.app
WEB_REDIRECT_URI_ALLOWLIST=https://chat.kittypaw.app/auth/callback,https://home.kittypaw.app/auth/callback
CORS_ORIGINS=https://kittypaw.app,https://portal.kittypaw.app,https://connect.kittypaw.app,https://chat.kittypaw.app,https://home.kittypaw.app
```

Keep the legacy chat callback/origin during migration; remove it only after the
cutover checklist passes.
