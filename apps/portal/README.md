# Kitty Portal

Identity and discovery service for KittyPaw. Portal owns OAuth login, access
and refresh token issuance, device credentials, JWKS publication, and the
service discovery document consumed by clients.

```text
portal.kittypaw.app
  /auth/*                  OAuth, token refresh, /me, device credentials
  /.well-known/jwks.json   JWT verification keys
  /discovery               API, chat, Kakao, and skills service locations
```

`/v1/*` data routes are intentionally not served here; they belong to
`apps/kittyapi`.

## API

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check with version and commit hash |
| `GET` | `/discovery` | Service URL discovery document |
| `GET` | `/.well-known/jwks.json` | Public JWK Set for RS256 JWTs |
| `GET` | `/auth/google` | Google OAuth login |
| `GET` | `/auth/google/callback` | Google OAuth callback; also completes web login states |
| `GET` | `/auth/github` | GitHub OAuth login |
| `GET` | `/auth/github/callback` | GitHub OAuth callback |
| `POST` | `/auth/token/refresh` | Refresh user access token |
| `GET` | `/auth/me` | Current user |
| `GET` | `/auth/cli/{provider}` | CLI OAuth login |
| `GET` | `/auth/cli/callback` | CLI OAuth callback |
| `POST` | `/auth/cli/exchange` | Exchange CLI one-time code |
| `GET` | `/auth/web/google` | Web OAuth login for chat callback |
| `POST` | `/auth/web/exchange` | Exchange web OAuth code |
| `POST` | `/auth/devices/pair` | Pair chat relay device |
| `POST` | `/auth/devices/refresh` | Rotate device credential |
| `GET` | `/auth/devices` | List paired devices |
| `DELETE` | `/auth/devices/{id}` | Revoke paired device |

## Configuration

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | Server port; production deploy env currently overrides this to `9714` |
| `UNIX_SOCKET` | unset | Optional Unix socket path; when set, nginx should proxy to this socket instead of a TCP port |
| `BASE_URL` | `http://localhost:8080` | Portal public origin |
| `API_BASE_URL` | `BASE_URL` | KittyAPI resource origin |
| `DATABASE_URL` | required | PostgreSQL connection string |
| `JWT_PRIVATE_KEY_PEM_B64` | required | Base64 PEM RSA private key for RS256 JWT signing |
| `GOOGLE_CLIENT_ID` | | Google OAuth client ID |
| `GOOGLE_CLIENT_SECRET` | | Google OAuth client secret |
| `GOOGLE_AUTH_URL` | Google authorization URL | Override only for local E2E/fake OAuth |
| `GOOGLE_TOKEN_URL` | Google token URL | Override only for local E2E/fake OAuth |
| `GOOGLE_USERINFO_URL` | Google userinfo URL | Override only for local E2E/fake OAuth |
| `CONNECT_BASE_URL` | unset | Connect public origin; enables Connect routes when set |
| `CONNECT_GOOGLE_CLIENT_ID` | | Connect Gmail OAuth client ID |
| `CONNECT_GOOGLE_CLIENT_SECRET` | | Connect Gmail OAuth client secret |
| `CONNECT_X_CLIENT_ID` | | Connect X OAuth client ID |
| `CONNECT_X_CLIENT_SECRET` | | Connect X OAuth client secret |
| `CONNECT_TOKEN_ENCRYPTION_KEY` | required for X | Standard base64 of 32 random bytes for server-side X token encryption |
| `CONNECT_X_API_BASE_URL` | X API origin | Override only for local fake X API |
| `GITHUB_CLIENT_ID` | | GitHub OAuth client ID |
| `GITHUB_CLIENT_SECRET` | | GitHub OAuth client secret |
| `CORS_ORIGINS` | `BASE_URL` | Comma-separated allowed origins |
| `WEB_REDIRECT_URI_ALLOWLIST` | empty | Exact-match chat web OAuth redirect allowlist |
| `KAKAO_RELAY_URL` | | Optional Kakao relay URL for discovery |
| `CHAT_RELAY_URL` | | Optional chat relay URL for discovery |
| `SKILLS_REGISTRY_URL` | `https://github.com/kittypaw-app/skills` | Skills registry URL |

## Development

```bash
cp .env.example .env
make test
make run
bash deploy/smoke.sh
```
