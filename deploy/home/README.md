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
