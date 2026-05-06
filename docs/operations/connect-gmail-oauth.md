# Connect Gmail OAuth 운영 체크리스트

Last reviewed: 2026-05-04

`connect.kittypaw.app`는 별도 binary가 아니라 `apps/portal` 서버의 host-routed
surface입니다. `portal.kittypaw.app`는 identity/discovery/JWKS를 담당하고,
`connect.kittypaw.app`는 외부 계정 연결 OAuth를 담당합니다.

## 배포 전

1. DNS와 TLS

   - `connect.kittypaw.app`이 portal 서버를 가리키도록 DNS를 추가합니다.
   - nginx 또는 reverse proxy의 `server_name`에 `portal.kittypaw.app`와
     `connect.kittypaw.app`를 모두 넣습니다.
   - 인증서는 두 host를 모두 포함해야 합니다.

2. Portal 환경 변수

   ```text
   BASE_URL=https://portal.kittypaw.app
   CONNECT_BASE_URL=https://connect.kittypaw.app
   CONNECT_GOOGLE_CLIENT_ID=...
   CONNECT_GOOGLE_CLIENT_SECRET=...
   ```

   `GOOGLE_CLIENT_ID`/`GOOGLE_CLIENT_SECRET`은 KittyPaw identity login용입니다.
   Gmail OAuth에는 `CONNECT_GOOGLE_CLIENT_ID`/`CONNECT_GOOGLE_CLIENT_SECRET`을
   별도로 사용합니다.

   Gmail Connect는 local-token 모델입니다. Portal은 OAuth callback과
   one-time code exchange만 처리하고, Gmail access/refresh token의 장기 저장
   위치는 각 사용자의 local KittyPaw account secrets입니다.

3. Google Cloud Console

   - Gmail API를 enable 합니다.
   - OAuth consent screen의 authorized domain에 `kittypaw.app`를 등록합니다.
   - Web OAuth client redirect URI에 아래 값을 추가합니다.

   ```text
   https://connect.kittypaw.app/connect/gmail/callback
   ```

   - 현재 구현 scope는 `https://www.googleapis.com/auth/gmail.readonly`입니다.
     아직 구현하지 않은 `gmail.send`, `gmail.modify`는 추가하지 않습니다.
   - 베타 기간에는 test users로 내부 계정을 등록해서 먼저 검증합니다.

4. Verification 준비

   `gmail.readonly`는 Google restricted scope입니다. 공개 사용자에게 열기 전
   OAuth verification과 restricted scope 심사를 준비해야 합니다. Google 문서상
   development/testing 또는 personal-use 수준의 소규모 테스트는 unverified warning과
   100-user cap 아래에서 진행할 수 있지만, production 공개에는 검증이 필요합니다.

   준비물:

   - Privacy Policy URL
   - Terms of Service URL
   - support/developer contact email
   - demo video: `kittypaw connect gmail` OAuth flow와 실제 Gmail read-only 사용 장면
   - scope justification: "최근 메일을 읽고 사용자의 로컬 KittyPaw daemon에서 요약/분류하기 위해 read-only 접근만 사용한다"
   - reviewer용 테스트 계정과 실행 안내

## 배포 직후

1. Host routing smoke

   ```bash
   curl https://portal.kittypaw.app/health
   curl https://portal.kittypaw.app/discovery
   curl https://connect.kittypaw.app/
   curl https://connect.kittypaw.app/connect
   curl -i https://connect.kittypaw.app/discovery
   curl -i https://portal.kittypaw.app/connect
   ```

   기대값:

   - portal `/discovery` 응답에 `connect_base_url`이 있습니다.
   - connect host의 `/connect`가 200입니다.
   - connect host의 `/discovery`는 404입니다.
   - portal host의 `/connect`는 404입니다.

2. OAuth smoke

   ```bash
   kittypaw login --api-url https://portal.kittypaw.app
   kittypaw connect gmail --api-url https://portal.kittypaw.app --account <account-id>
   ```

   headless 환경에서는 code-paste mode를 사용합니다.

   ```bash
   kittypaw connect gmail --api-url https://portal.kittypaw.app --account <account-id> --code
   ```

   확인:

   - `~/.kittypaw/accounts/<account-id>/secrets.json`에 `oauth-gmail` namespace가 생깁니다.
   - token 값은 로그, URL query, browser callback에 노출되지 않아야 합니다.
   - `connect_base_url`은 `https://connect.kittypaw.app`로 저장됩니다.

3. MCP config smoke

   Gmail MCP server가 준비되면 account config에 source-bound env를 사용합니다.

   ```toml
   [[mcp_servers]]
   name = "gmail"
   command = "gmail-mcp"

   [mcp_servers.env_from]
   GMAIL_ACCESS_TOKEN = "oauth-gmail/access_token"
   ```

   토큰이 없거나 refresh에 실패하면 local server log에 `kittypaw connect gmail`
   안내가 보여야 하고, subprocess는 토큰 없이 시작되면 안 됩니다.

## 운영 중 주의

- Google OAuth client owner/editor 이메일을 실제로 확인 가능한 주소로 유지합니다.
- restricted scope 관련 Google 이메일은 project owner/editor에게 갑니다.
- refresh token 장애가 반복되면 사용자는 `kittypaw connect gmail`을 다시 실행해야 합니다.
- scope 추가는 코드에서 실제 기능이 들어간 뒤에만 합니다. Google verification은
  "미래 기능을 위한 scope" 요청을 거절할 수 있습니다.
- portal 서버는 OAuth token을 장기 저장하지 않습니다. local KittyPaw account secrets가
  refresh token의 장기 저장 위치입니다.
- `/admin/connect`는 provider 정책과 entitlement만 관리합니다. Gmail mailbox
  contents, Gmail token 값, message metadata를 조회하거나 검사하는 용도로
  사용하면 안 됩니다.

## 공식 참고

- Gmail API scopes: https://developers.google.com/workspace/gmail/api/auth/scopes
- Google restricted scopes: https://support.google.com/cloud/answer/13464325
- When verification is not needed: https://support.google.com/cloud/answer/13464323
- Submitting an app for verification: https://support.google.com/cloud/answer/13461325
- OAuth app branding and authorized domains: https://support.google.com/cloud/answer/10311615
