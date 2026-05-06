# Kitty Portal 배포 메모

Portal은 `portal.kittypaw.app`에서 auth authority와 service discovery를
제공하고, 같은 `kittyportal` 서버가 `connect.kittypaw.app`에서 KittyPaw
Connect account-linking surface를 제공합니다. `/v1/*` 데이터 API는
`apps/kittyapi`가 담당합니다.

## 환경 변수

`deploy/env.example`를 기준으로 서버의 EnvironmentFile을 구성합니다.

```text
PORT=9714
UNIX_SOCKET=/home/jinto/kittyportal/kittyportal.sock
BASE_URL=https://portal.kittypaw.app
CONNECT_BASE_URL=https://connect.kittypaw.app
API_BASE_URL=https://api.kittypaw.app
DATABASE_URL=postgres://...
JWT_PRIVATE_KEY_PEM_B64=
GOOGLE_CLIENT_ID=
GOOGLE_CLIENT_SECRET=
# GOOGLE_AUTH_URL=        # unset in prod; local fake OAuth only
# GOOGLE_TOKEN_URL=       # unset in prod; local fake OAuth only
# GOOGLE_USERINFO_URL=    # unset in prod; local fake OAuth only
CONNECT_GOOGLE_CLIENT_ID=
CONNECT_GOOGLE_CLIENT_SECRET=
# CONNECT_GOOGLE_AUTH_URL=       # unset in prod; local fake OAuth only
# CONNECT_GOOGLE_TOKEN_URL=      # unset in prod; local fake OAuth only
# CONNECT_GOOGLE_USERINFO_URL=   # unset in prod; local fake OAuth only
CONNECT_X_CLIENT_ID=
CONNECT_X_CLIENT_SECRET=
# CONNECT_X_AUTH_URL=            # unset in prod; local fake OAuth only
# CONNECT_X_TOKEN_URL=           # unset in prod; local fake OAuth only
# CONNECT_X_USERINFO_URL=        # unset in prod; local fake OAuth only
GITHUB_CLIENT_ID=
GITHUB_CLIENT_SECRET=
WEB_REDIRECT_URI_ALLOWLIST=https://chat.kittypaw.app/auth/callback
```

`GOOGLE_CLIENT_ID`/`GOOGLE_CLIENT_SECRET`은 KittyPaw identity login용입니다.
Gmail restricted scopes는 `CONNECT_GOOGLE_CLIENT_ID`/
`CONNECT_GOOGLE_CLIENT_SECRET`에 별도 OAuth client를 설정합니다.
Gmail OAuth 배포 전/후 체크리스트는
`docs/operations/connect-gmail-oauth.md`를 따릅니다.

X Connect는 `CONNECT_X_CLIENT_ID`/`CONNECT_X_CLIENT_SECRET`을 사용합니다.
X Developer Portal의 OAuth 2.0 callback URL은
`https://connect.kittypaw.app/connect/x/callback`로 정확히 등록합니다.
초기 scope는 read-only `tweet.read users.read offline.access`입니다.

nginx의 `server_name`에는 portal과 connect host를 모두 넣습니다:

```bash
DEPLOY_DOMAIN="portal.kittypaw.app connect.kittypaw.app" uv run fab setup
```

## RS256 서명 키

JWT는 RS256으로 서명되며 공개 키는
`https://portal.kittypaw.app/.well-known/jwks.json`으로 노출됩니다.

```bash
# Linux
openssl genrsa 2048 | base64 -w0

# macOS
openssl genrsa 2048 | base64 | tr -d '\n'
```

출력값은 `JWT_PRIVATE_KEY_PEM_B64`에만 설정하고 git에 커밋하지 않습니다.

## 검증

```bash
curl https://portal.kittypaw.app/health
curl https://portal.kittypaw.app/discovery
curl https://portal.kittypaw.app/.well-known/jwks.json
curl https://portal.kittypaw.app/v1/geo/resolve       # 404
curl https://connect.kittypaw.app/
curl https://connect.kittypaw.app/connect
curl https://connect.kittypaw.app/discovery           # 404
curl https://portal.kittypaw.app/connect              # 404
bash deploy/smoke.sh
```

## Fabric 작업

```bash
uv run fab setup
uv run fab deploy
uv run fab smoke
uv run fab migrate
uv run fab rollback
uv run fab status
uv run fab logs
```

## DB

Portal은 users, refresh_tokens, devices 테이블을 소유합니다. 현재 production
DB 물리는 기존 `kittypaw_api` 데이터베이스를 공유하며, 별도 DB로의 물리
분리는 후속 cutover에서 다룹니다.
