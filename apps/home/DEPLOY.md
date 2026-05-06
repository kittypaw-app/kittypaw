# KittyHome Deploy

Deploy `apps/home` behind nginx/systemd.

```bash
make -C apps/home build
```

Production smoke:

```bash
BASE_URL=https://home.kittypaw.app bash apps/home/deploy/smoke.sh
```

Portal must include:

```text
HOME_BASE_URL=https://home.kittypaw.app
WEB_REDIRECT_URI_ALLOWLIST=https://home.kittypaw.app/auth/callback
CORS_ORIGINS=https://portal.kittypaw.app,https://home.kittypaw.app
```
