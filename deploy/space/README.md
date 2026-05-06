# Space Deployment

The active Space deployment files live in `apps/space/deploy/`, with Fabric
automation in `apps/space/fabfile.py`.

Space is the hosted user-facing surface at `space.kittypaw.app`. It replaces the
legacy chat relay after the migration checklist in the Space chat design passes.

Common operator commands:

```bash
cd apps/space
DEPLOY_DOMAIN=space.kittypaw.app uv run fab setup
uv run fab deploy
uv run fab smoke
uv run fab rollback
uv run fab status
uv run fab logs
```

After deploy, run `apps/space`'s credentialed cutover smoke with Portal-issued
user and device tokens before considering the legacy chat relay removable:

```bash
cd apps/space
SPACE_BASE_URL=https://space.kittypaw.app \
SPACE_USER_TOKEN=<user-access-token> \
SPACE_DEVICE_TOKEN=<device-token> \
SPACE_DEVICE_ID=<device-id> \
SPACE_LOCAL_ACCOUNT_ID=<local-account-id> \
make smoke-cutover
```
