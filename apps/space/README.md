# KittySpace

KittySpace is the hosted Space surface for local KittyPaw daemons. The first
surface is hosted chat at `https://space.kittypaw.app/chat`.

KittySpace is not a generic localhost tunnel. It accepts daemon outbound
WebSocket connections and relays only supported capability operations.

## Routes

- `GET /health`
- `GET /chat`
- `GET /chat/`
- `GET /auth/login/google`
- `GET /auth/callback`
- `POST /auth/logout`
- `GET /chat/api/session`
- `GET /chat/api/routes`
- `GET /chat/api/nodes/*`
- `POST /chat/api/nodes/*`
- `GET /kanban`
- `GET /kanban/`
- `GET /kanban/api/session`
- `GET /kanban/api/routes`
- `GET /kanban/api/nodes/*`
- `POST /kanban/api/nodes/*`
- `PATCH /kanban/api/nodes/*`
- `GET /daemon/connect`
- `GET /v1/routes`
- `GET /nodes/{device_id}/accounts/{account_id}/v1/models`
- `POST /nodes/{device_id}/accounts/{account_id}/v1/chat/completions`
- `GET|POST|PATCH /nodes/{device_id}/accounts/{account_id}/api/*` for the
  allowlisted Kanban/workspace local API surface

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `KITTYSPACE_JWKS_URL` | unset | Portal JWKS endpoint for RS256 access tokens and daemon credentials |
| `KITTYSPACE_JWT_SECRET` | unset | Legacy HS256 fallback for local testing |
| `KITTYSPACE_API_TOKEN` | unset | Static API token fallback when no JWT verifier is configured |
| `KITTYSPACE_DEVICE_TOKEN` | unset | Static daemon token fallback when no JWT verifier is configured |
| `KITTYSPACE_USER_ID` | unset | Static fallback user id |
| `KITTYSPACE_DEVICE_ID` | unset | Static fallback device id |
| `KITTYSPACE_LOCAL_ACCOUNT_ID` | unset | Static fallback local account id |
| `KITTYSPACE_BIND_ADDR` | `:$PORT` or `:8080` | TCP bind address or Unix socket path |
| `KITTYSPACE_PUBLIC_BASE_URL` | `https://space.kittypaw.app` | Public Space origin |
| `KITTYSPACE_API_AUTH_BASE_URL` | `https://portal.kittypaw.app/auth` | Portal auth base URL |
| `KITTYSPACE_VERSION` | `dev` | Health version |
| `KITTYSPACE_COMMIT` | unset | Health commit |

## Smoke Tests

`make smoke-local` starts an in-process Space router, fake Portal auth exchange,
and fake daemon. It verifies direct relay routes plus the browser BFF path:
login callback, session cookie, `/chat/api/routes`, and `/chat/api` chat
completion.

`bash deploy/smoke.sh` is safe against production without credentials. It checks
health, `/chat/`, `/kanban/`, app JS wiring to the BFF routes, anonymous session
rejection, and the Google login redirect shape.

`make smoke-cutover` is a manual credentialed smoke for the Space cutover path.
It requires Portal-issued user and device credentials:

```bash
SPACE_BASE_URL=https://space.kittypaw.app \
SPACE_USER_TOKEN=<user-access-token> \
SPACE_DEVICE_TOKEN=<device-token> \
SPACE_DEVICE_ID=<device-id> \
SPACE_LOCAL_ACCOUNT_ID=<local-account-id> \
make smoke-cutover
```

The cutover smoke starts a fake daemon WebSocket client, waits for Space route
discovery, and verifies a chat completion is relayed through that daemon.
