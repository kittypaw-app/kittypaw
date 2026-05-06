# KittyHome

KittyHome is the hosted Home surface for local KittyPaw daemons. The first
surface is hosted chat at `https://home.kittypaw.app/chat`.

KittyHome is not a generic localhost tunnel. It accepts daemon outbound
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
- `GET /daemon/connect`
- `GET /v1/routes`
- `GET /nodes/{device_id}/accounts/{account_id}/v1/models`
- `POST /nodes/{device_id}/accounts/{account_id}/v1/chat/completions`

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `KITTYHOME_JWKS_URL` | unset | Portal JWKS endpoint for RS256 access tokens and daemon credentials |
| `KITTYHOME_JWT_SECRET` | unset | Legacy HS256 fallback for local testing |
| `KITTYHOME_API_TOKEN` | unset | Static API token fallback when no JWT verifier is configured |
| `KITTYHOME_DEVICE_TOKEN` | unset | Static daemon token fallback when no JWT verifier is configured |
| `KITTYHOME_USER_ID` | unset | Static fallback user id |
| `KITTYHOME_DEVICE_ID` | unset | Static fallback device id |
| `KITTYHOME_LOCAL_ACCOUNT_ID` | unset | Static fallback local account id |
| `KITTYHOME_BIND_ADDR` | `:$PORT` or `:8080` | TCP bind address or Unix socket path |
| `KITTYHOME_PUBLIC_BASE_URL` | `https://home.kittypaw.app` | Public Home origin |
| `KITTYHOME_API_AUTH_BASE_URL` | `https://portal.kittypaw.app/auth` | Portal auth base URL |
| `KITTYHOME_VERSION` | `dev` | Health version |
| `KITTYHOME_COMMIT` | unset | Health commit |

## Smoke Tests

`make smoke-local` starts an in-process Home router, fake Portal auth exchange,
and fake daemon. It verifies direct relay routes plus the browser BFF path:
login callback, session cookie, `/chat/api/routes`, and `/chat/api` chat
completion.

`bash deploy/smoke.sh` is safe against production without credentials. It checks
health, `/chat/`, chat JS wiring to `/chat/api`, anonymous session rejection, and
the Google login redirect shape.
