# Home Deployment

The active Home deployment files live in `apps/home/deploy/`, with Fabric
automation in `apps/home/fabfile.py`.

Home is the hosted user-facing surface at `home.kittypaw.app`. It replaces the
legacy chat relay after the migration checklist in the Home chat design passes.

Common operator commands:

```bash
cd apps/home
DEPLOY_DOMAIN=home.kittypaw.app uv run fab setup
uv run fab deploy
uv run fab smoke
uv run fab rollback
uv run fab status
uv run fab logs
```

After deploy, run `apps/home`'s credentialed cutover smoke with Portal-issued
user and device tokens before considering the legacy chat relay removable:

```bash
cd apps/home
HOME_BASE_URL=https://home.kittypaw.app \
HOME_USER_TOKEN=<user-access-token> \
HOME_DEVICE_TOKEN=<device-token> \
HOME_DEVICE_ID=<device-id> \
HOME_LOCAL_ACCOUNT_ID=<local-account-id> \
make smoke-cutover
```
