# Deploy

Deployment assets are grouped by deployable unit.

```text
deploy/
  kittyapi/
  portal/
  chat/
  space/
  kakao/
```

The active deployment scripts and service templates live with each app under
`apps/<name>/deploy/`. The root `deploy/` directory is only for repository-level
notes and shared deployment material.

Hosted Go services are deployed behind nginx/systemd. Current production
templates bind them to Unix socket files where supported (`UNIX_SOCKET`,
`KITTYCHAT_BIND_ADDR`, `KITTYSPACE_BIND_ADDR`, or `BIND_ADDR`) and let nginx
proxy public HTTPS traffic to the socket.

`apps/kittypaw` release automation is GitHub-release based rather than a hosted
service deployment. Its active workflow is `.github/workflows/release-kittypaw.yml`
and it is triggered by `kittypaw/v*` tags.
