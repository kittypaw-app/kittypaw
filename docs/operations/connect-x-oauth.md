# Connect X OAuth 운영 체크리스트

Last reviewed: 2026-05-07

`connect.kittypaw.app`는 `apps/portal` 서버의 host-routed Connect surface입니다.
X/Twitter는 native read-only OAuth 2.0 PKCE flow로 연결합니다.

## 배포 전

1. X Developer Portal에서 App의 OAuth 2.0을 켭니다.

   - App type: Web App 또는 confidential client
   - Callback URL:

   ```text
   https://connect.kittypaw.app/connect/x/callback
   ```

   - Website URL: `https://kittypaw.app`
   - Scope: `tweet.read users.read offline.access`

2. Portal 환경 변수

   ```text
   CONNECT_BASE_URL=https://connect.kittypaw.app
   CONNECT_X_CLIENT_ID=...
   CONNECT_X_CLIENT_SECRET=...
   CONNECT_TOKEN_ENCRYPTION_KEY=...
   ```

   로컬 fake OAuth 테스트가 아니면 `CONNECT_X_AUTH_URL`,
   `CONNECT_X_TOKEN_URL`, `CONNECT_X_USERINFO_URL`,
   `CONNECT_X_API_BASE_URL`은 비워둡니다.

   `CONNECT_TOKEN_ENCRYPTION_KEY`는 standard base64 32-byte key입니다.

   ```bash
   openssl rand -base64 32
   ```

3. 비용과 quota

   X API는 Developer App/plan 제약을 받습니다. v1 구현은 read-only이며,
   KittyPaw 엔진은 한 호출당 최대 10개 결과로 제한합니다.

## Connect Admin

X is cost-bearing for KittyPaw. Do not open X Connect to all users.

Before a user can run `kittypaw connect x`, an admin must:

1. open `https://portal.kittypaw.app/admin/login`;
2. complete Google login with an email listed in `PORTAL_ADMIN_EMAILS`;
3. open `/admin/connect/users`;
4. grant X entitlement to the user's email address;
5. set a small monthly post-read quota, for example `100`, and record the reason.

X OAuth token은 local KittyPaw 계정에 저장하지 않고 Portal DB에 암호화
저장됩니다. KittyPaw local runtime은 일반 KittyPaw login JWT로
`/connect/x/broker/*`를 호출하고, Portal이 entitlement와
`monthly_post_reads` quota를 확인한 뒤 X API를 호출합니다.

## 배포 직후

1. Host routing smoke

   ```bash
   curl https://connect.kittypaw.app/connect
   curl -i https://portal.kittypaw.app/connect
   curl -i https://connect.kittypaw.app/discovery
   ```

2. OAuth smoke

   ```bash
   kittypaw connect x --api-url https://portal.kittypaw.app --account <account-id>
   ```

   headless 환경:

   ```bash
   kittypaw connect x --api-url https://portal.kittypaw.app --account <account-id> --code
   ```

   확인:

   - `~/.kittypaw/accounts/<account-id>/secrets.json`에 `oauth-x` namespace가 생깁니다.
   - `oauth-x/access_token`, `oauth-x/refresh_token` 값은 없어야 합니다.
   - `oauth-x/token_type`은 `broker`입니다.
   - X token 값은 URL query, 로그, browser callback에 노출되지 않아야 합니다.
   - `connect_base_url`은 `https://connect.kittypaw.app`로 저장됩니다.

3. 기능 smoke

   KittyPaw 내부 도구:

   ```js
   X.searchRecent("kittypaw", {limit: 10})
   X.user("XDevelopers")
   X.userPosts("XDevelopers", {limit: 10})
   X.post("https://x.com/XDevelopers/status/<id>")
   ```

   기존 X direct-token release에서 업그레이드한 사용자는 이 smoke 전에
   `kittypaw connect x`를 다시 실행해야 서버측 broker token이 저장됩니다.

   MCP env source를 쓰는 경우는 legacy compatibility입니다. 신규 X broker
   flow는 local X access token을 저장하지 않으므로 X MCP direct-token
   integration에는 사용할 수 없습니다.

   ```toml
   [mcp_servers.env_from]
   X_ACCESS_TOKEN = "oauth-x/access_token"
   ```

## 운영 중 주의

- write scope는 추가하지 않습니다. `tweet.write`, `like.write`, `dm.write`는 별도 제품 결정과 승인 UX가 필요합니다.
- refresh token 장애가 반복되면 사용자는 `kittypaw connect x`를 다시 실행해야 합니다.
- X API plan, spending cap, per-endpoint quota 변경은 KittyPaw 사용자 경험에 직접 영향을 줍니다.
- quota 조정은 `https://portal.kittypaw.app/admin/connect/users`에서 사용자별
  `monthly_post_reads` 값을 수정합니다.

## 공식 참고

- OAuth 2.0 Authorization Code Flow with PKCE: https://docs.x.com/fundamentals/authentication/oauth-2-0/authorization-code
- Recent search: https://docs.x.com/x-api/posts/search/quickstart/recent-search
- User lookup: https://docs.x.com/x-api/users/lookup/introduction
- Rate limits: https://docs.x.com/x-api/fundamentals/rate-limits
