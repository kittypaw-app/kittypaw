# X Broker Quota Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move X/Twitter reads behind a Portal-owned broker that stores X tokens server-side and enforces per-user monthly post-read quotas.

**Architecture:** `apps/portal` owns X OAuth token storage, broker endpoints, token refresh, entitlement checks, and usage accounting. `apps/kittypaw` stops calling `api.x.com` directly and calls `connect.kittypaw.app/connect/x/broker/*` with the normal KittyPaw login JWT. Gmail remains unchanged.

**Tech Stack:** Go, chi, pgx/PostgreSQL migrations, AES-GCM, existing Portal auth middleware, existing `core.XClient` JSON shapes.

---

## File Structure

- Create `apps/portal/migrations/010_create_connect_broker.up.sql`: token and usage tables.
- Create `apps/portal/migrations/010_create_connect_broker.down.sql`: rollback for the new tables.
- Create `apps/portal/internal/connect/token_crypto.go`: AES-GCM token encryption.
- Create `apps/portal/internal/connect/token_store.go`: server-side provider token persistence and usage accounting.
- Create `apps/portal/internal/connect/broker.go`: authenticated X broker handlers.
- Modify `apps/portal/internal/config/config.go`: `CONNECT_TOKEN_ENCRYPTION_KEY`.
- Modify `apps/portal/internal/connect/handler.go`: X callback stores server token and returns broker marker.
- Modify `apps/portal/cmd/server/main.go`: wire token store and broker routes.
- Modify `apps/kittypaw/core/x_client.go`: add tweet lookup and broker client.
- Modify `apps/kittypaw/engine/x.go`: call broker instead of direct X API.
- Modify `apps/kittypaw/cli/cmd_connect.go`: store X broker marker without access/refresh tokens.
- Update tests near each changed file.

---

### Task 1: Portal Schema, Crypto, And Store

**Files:**
- Create: `apps/portal/migrations/010_create_connect_broker.up.sql`
- Create: `apps/portal/migrations/010_create_connect_broker.down.sql`
- Create: `apps/portal/internal/connect/token_crypto.go`
- Create: `apps/portal/internal/connect/token_store.go`
- Test: `apps/portal/internal/connect/token_store_test.go`

- [ ] **Step 1: Write failing token crypto/store tests**

Create `apps/portal/internal/connect/token_store_test.go` with tests for AES-GCM round trip, invalid key rejection, token upsert/load, and usage quota.

```go
func TestTokenCipherRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	c, err := NewTokenCipher(key)
	if err != nil {
		t.Fatalf("NewTokenCipher: %v", err)
	}
	enc, err := c.Encrypt("secret-token")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := c.Decrypt(enc)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != "secret-token" {
		t.Fatalf("Decrypt = %q, want secret-token", got)
	}
}

func TestTokenCipherRejectsBadKey(t *testing.T) {
	if _, err := NewTokenCipher([]byte("short")); err == nil {
		t.Fatal("expected bad key error")
	}
}

func TestMemoryConnectTokenStoreUsageQuota(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	store := NewMemoryTokenStore(now)
	if allowed, err := store.RecordUsage(context.Background(), UsageRecord{
		UserID: "user-1", ProviderID: XProviderID, Operation: "search_recent",
		Quantity: 3, MonthlyLimit: 5, Now: now,
	}); err != nil || !allowed {
		t.Fatalf("first RecordUsage allowed=%v err=%v", allowed, err)
	}
	if allowed, err := store.RecordUsage(context.Background(), UsageRecord{
		UserID: "user-1", ProviderID: XProviderID, Operation: "search_recent",
		Quantity: 3, MonthlyLimit: 5, Now: now,
	}); err != nil || allowed {
		t.Fatalf("second RecordUsage allowed=%v err=%v, want over quota", allowed, err)
	}
}
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
cd apps/portal
go test ./internal/connect -run 'TestTokenCipher|TestMemoryConnectTokenStoreUsageQuota'
```

Expected: compile failure for undefined `NewTokenCipher`, `NewMemoryTokenStore`, and `UsageRecord`.

- [ ] **Step 3: Add migrations**

Create `010_create_connect_broker.up.sql`:

```sql
CREATE TABLE connect_provider_tokens (
    user_id                  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider_id              TEXT NOT NULL,
    access_token_ciphertext  BYTEA NOT NULL,
    refresh_token_ciphertext BYTEA,
    token_type               TEXT NOT NULL DEFAULT 'Bearer',
    scope                    TEXT NOT NULL DEFAULT '',
    username                 TEXT NOT NULL DEFAULT '',
    expires_at               TIMESTAMPTZ,
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, provider_id)
);

CREATE TABLE connect_provider_usage_events (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider_id   TEXT NOT NULL,
    operation     TEXT NOT NULL,
    quantity      INTEGER NOT NULL CHECK (quantity >= 0),
    quota_key     TEXT NOT NULL DEFAULT 'post_reads',
    window_start  TIMESTAMPTZ NOT NULL,
    metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata_json) = 'object'),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_connect_provider_usage_window
    ON connect_provider_usage_events(user_id, provider_id, quota_key, window_start);
```

Create `010_create_connect_broker.down.sql`:

```sql
DROP TABLE IF EXISTS connect_provider_usage_events;
DROP TABLE IF EXISTS connect_provider_tokens;
```

- [ ] **Step 4: Implement crypto and in-memory store**

Create `token_crypto.go` with `TokenCipher` using AES-GCM and nonce-prefixed ciphertext. Create `token_store.go` with:

```go
type ProviderTokenRecord struct {
	UserID, ProviderID, AccessToken, RefreshToken, TokenType, Scope, Username string
	ExpiresAt *time.Time
}

type UsageRecord struct {
	UserID, ProviderID, Operation string
	Quantity int
	MonthlyLimit int
	Now time.Time
	Metadata map[string]any
}

type BrokerTokenStore interface {
	SaveProviderToken(context.Context, ProviderTokenRecord) error
	LoadProviderToken(context.Context, string, string) (ProviderTokenRecord, error)
	RecordUsage(context.Context, UsageRecord) (bool, error)
}
```

Implement `MemoryTokenStore` for unit tests.

- [ ] **Step 5: Run tests and verify GREEN**

Run:

```bash
cd apps/portal
go test ./internal/connect -run 'TestTokenCipher|TestMemoryConnectTokenStoreUsageQuota'
```

Expected: PASS.

- [ ] **Step 6: Add Postgres store methods**

Extend `token_store.go` with `PostgresTokenStore` using pgxpool. Encrypt access/refresh tokens before insert and decrypt when loading. `RecordUsage` must run a transaction:

```sql
SELECT COALESCE(SUM(quantity), 0)
FROM connect_provider_usage_events
WHERE user_id = $1 AND provider_id = $2 AND quota_key = 'post_reads' AND window_start = $3
```

If `used + quantity > monthly_limit`, return `false` without insert. Otherwise insert the usage event and return `true`.

- [ ] **Step 7: Commit**

```bash
git add apps/portal/migrations/010_create_connect_broker.* apps/portal/internal/connect/token_crypto.go apps/portal/internal/connect/token_store.go apps/portal/internal/connect/token_store_test.go
git commit -m "feat(portal): add connect broker token store"
```

---

### Task 2: X OAuth Callback Stores Server Tokens

**Files:**
- Modify: `apps/portal/internal/connect/handler.go`
- Modify: `apps/portal/internal/connect/handler_test.go`
- Modify: `apps/kittypaw/cli/cmd_connect.go`
- Modify: `apps/kittypaw/cli/cmd_connect_test.go`

- [ ] **Step 1: Write failing Portal callback test**

Add a test showing X callback stores tokens server-side and returns only a broker marker.

```go
func TestHandlerXCallbackStoresServerTokenAndReturnsBrokerMarker(t *testing.T) {
	h, states, _, _ := testHandler(t)
	tokenStore := NewMemoryTokenStore(time.Now())
	h.TokenStore = tokenStore
	state, err := states.CreateWithMeta("verifier-1", map[string]string{
		"mode": "code", "provider": XProviderID, "user_id": "user-1",
	})
	if err != nil {
		t.Fatalf("CreateWithMeta: %v", err)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/connect/x/callback?code=x-code&state="+url.QueryEscape(state), nil)
	h.HandleXCallback()(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	stored, err := tokenStore.LoadProviderToken(context.Background(), "user-1", XProviderID)
	if err != nil {
		t.Fatalf("LoadProviderToken: %v", err)
	}
	if stored.AccessToken != "x-access" || stored.RefreshToken != "x-refresh" {
		t.Fatalf("stored token = %#v", stored)
	}
	displayCode := extractDisplayCode(t, w.Body.String())
	exchange := httptest.NewRecorder()
	h.HandleCLIExchange()(exchange, httptest.NewRequest(http.MethodPost, "/connect/cli/exchange", strings.NewReader(fmt.Sprintf(`{"code":%q}`, displayCode))))
	if strings.Contains(exchange.Body.String(), "x-access") || strings.Contains(exchange.Body.String(), "x-refresh") {
		t.Fatalf("exchange leaked X token: %s", exchange.Body.String())
	}
}
```

- [ ] **Step 2: Run test and verify RED**

Run:

```bash
cd apps/portal
go test ./internal/connect -run TestHandlerXCallbackStoresServerTokenAndReturnsBrokerMarker
```

Expected: compile failure for `Handler.TokenStore` or failure because tokens still leak.

- [ ] **Step 3: Implement callback changes**

Add `TokenStore BrokerTokenStore` to `Handler`. Add `user_id` to X OAuth state in `startOAuthLogin` by passing the preauth session user. On X callback:

```go
if provider == XProviderID && h.TokenStore != nil {
	err := h.TokenStore.SaveProviderToken(r.Context(), ProviderTokenRecord{
		UserID: meta["user_id"], ProviderID: XProviderID,
		AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken,
		TokenType: tokens.TokenType, Scope: tokens.Scope, Username: tokens.Username,
		ExpiresAt: expiresAtFrom(tokens),
	})
	tokens.AccessToken = ""
	tokens.RefreshToken = ""
	tokens.TokenType = "broker"
}
```

Gmail must keep the existing local-token path.

- [ ] **Step 4: Update CLI test for broker marker**

Change X connect tests so the fake exchange response is:

```json
{"provider":"x","token_type":"broker","scope":"tweet.read users.read offline.access","username":"jaypark"}
```

Assert local secrets do not contain `oauth-x/access_token` or `oauth-x/refresh_token`, but do contain `oauth-x/connect_base_url` and `oauth-x/token_type=broker`.

- [ ] **Step 5: Implement CLI save behavior**

In `exchangeConnectCode`, if `provider == "x"` and `token_type == "broker"`, save provider, token_type, scope, username, and connect_base_url. Do not save access or refresh tokens.

- [ ] **Step 6: Run tests and commit**

Run:

```bash
cd apps/portal && go test ./internal/connect
cd ../kittypaw && go test ./cli -run 'TestConnectX'
```

Commit:

```bash
git add apps/portal/internal/connect apps/kittypaw/cli
git commit -m "feat(connect): store x tokens server-side"
```

---

### Task 3: Portal X Broker Endpoints And Quota

**Files:**
- Create: `apps/portal/internal/connect/broker.go`
- Modify: `apps/portal/internal/connect/x.go`
- Modify: `apps/portal/internal/connect/handler_test.go`
- Modify: `apps/portal/cmd/server/main.go`
- Modify: `apps/portal/cmd/server/main_test.go`

- [ ] **Step 1: Write failing broker tests**

Add tests for search recent, quota exceeded, and unauthenticated request.

```go
func TestXBrokerSearchRecentRecordsUsage(t *testing.T) {
	tokenStore := NewMemoryTokenStore(time.Now())
	_ = tokenStore.SaveProviderToken(context.Background(), ProviderTokenRecord{
		UserID: "user-1", ProviderID: XProviderID, AccessToken: "x-access", TokenType: "Bearer",
	})
	h := &Handler{X: testXProviderWithFakeClient(t), TokenStore: tokenStore, Entitlements: fakeQuotaEntitlements{allowed: true, monthlyPostReads: 5}}
	req := httptest.NewRequest(http.MethodGet, "/connect/x/broker/search/recent?query=kittypaw&limit=10", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: "user-1", Email: "a@example.com"}))
	w := httptest.NewRecorder()
	h.HandleXBrokerSearchRecent()(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
}
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
cd apps/portal
go test ./internal/connect -run 'TestXBroker'
```

Expected: compile failure for broker handlers.

- [ ] **Step 3: Implement broker handlers**

Create `broker.go` handlers:

- `HandleXBrokerSearchRecent`
- `HandleXBrokerUserByUsername`
- `HandleXBrokerUserPostsByUsername`
- `HandleXBrokerTweetByID`

Each handler gets `auth.UserFromContext`, checks entitlement/quota, loads server token, calls `XProvider`/`XClient`, then records usage before returning JSON.

- [ ] **Step 4: Add X client tweet lookup in portal provider**

Add `TweetByID(ctx, accessToken, id)` to the reusable X client path or provider helper so broker can call `GET /2/tweets/{id}` with the same fields as search/user posts.

- [ ] **Step 5: Wire routes**

In `apps/portal/cmd/server/main.go`, add authenticated connect-host routes:

```go
r.With(authMW).Get("/connect/x/broker/search/recent", connectHandler.HandleXBrokerSearchRecent())
r.With(authMW).Get("/connect/x/broker/users/by/username/{username}", connectHandler.HandleXBrokerUserByUsername())
r.With(authMW).Get("/connect/x/broker/users/by/username/{username}/tweets", connectHandler.HandleXBrokerUserPostsByUsername())
r.With(authMW).Get("/connect/x/broker/tweets/{id}", connectHandler.HandleXBrokerTweetByID())
```

- [ ] **Step 6: Run tests and commit**

Run:

```bash
cd apps/portal
go test ./internal/connect ./cmd/server
```

Commit:

```bash
git add apps/portal/internal/connect apps/portal/cmd/server
git commit -m "feat(portal): broker x reads with quotas"
```

---

### Task 4: Local KittyPaw Uses Broker

**Files:**
- Modify: `apps/kittypaw/core/x_client.go`
- Modify: `apps/kittypaw/core/x_client_test.go`
- Modify: `apps/kittypaw/engine/x.go`
- Modify: `apps/kittypaw/engine/x_test.go`

- [ ] **Step 1: Write failing core broker client tests**

Add tests that broker requests use `Authorization: Bearer <kittypaw-jwt>` and broker paths.

```go
func TestXBrokerClientSearchRecentUsesBroker(t *testing.T) {
	var gotAuth, gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"posts":[{"id":"p1","text":"hello"}]}`)
	}))
	defer ts.Close()
	client := NewXBrokerClient(ts.URL, nil)
	result, err := client.SearchRecent(context.Background(), "kp-token", "kittypaw", 10)
	if err != nil {
		t.Fatalf("SearchRecent: %v", err)
	}
	if gotAuth != "Bearer kp-token" || gotPath != "/connect/x/broker/search/recent" {
		t.Fatalf("auth/path = %q %q", gotAuth, gotPath)
	}
	if len(result.Posts) != 1 {
		t.Fatalf("posts = %#v", result.Posts)
	}
}
```

- [ ] **Step 2: Run test and verify RED**

Run:

```bash
cd apps/kittypaw
go test ./core -run TestXBrokerClient
```

Expected: compile failure for `NewXBrokerClient`.

- [ ] **Step 3: Implement `XBrokerClient`**

In `core/x_client.go`, add a broker client that returns existing `XUser`,
`XPost`, and `XPostsResult` types. Add methods:

- `SearchRecent(ctx, jwt, query string, maxResults int)`
- `UserByUsername(ctx, jwt, username string)`
- `UserPostsByUsername(ctx, jwt, username string, maxResults int)`
- `TweetByID(ctx, jwt, id string)`

Map 401, 403, and 429 to clear errors.

- [ ] **Step 4: Write failing engine test**

Add an engine test where `Session.APITokenMgr` has a login token and connect base URL, and `executeX` calls the fake broker. Assert the fake direct `api.x.com` client is not used.

- [ ] **Step 5: Implement engine broker path**

Replace `xClientForSession` with `xBrokerClientForSession` using:

```go
accessToken, err := s.APITokenMgr.LoadAccessToken(core.DefaultAPIServerURL)
base := s.APITokenMgr.ResolveConnectBaseURL(core.DefaultAPIServerURL)
client := core.NewXBrokerClient(base, nil)
```

Keep guidance:

- no API token -> `kittypaw login --account <account>`
- broker 403 -> `kittypaw connect x --account <account>`

- [ ] **Step 6: Run tests and commit**

Run:

```bash
cd apps/kittypaw
go test ./core ./engine
```

Commit:

```bash
git add apps/kittypaw/core apps/kittypaw/engine
git commit -m "feat(kittypaw): route x tools through broker"
```

---

### Task 5: Config, Docs, And Full Verification

**Files:**
- Modify: `apps/portal/internal/config/config.go`
- Modify: `apps/portal/internal/config/config_test.go`
- Modify: `apps/portal/deploy/env.example`
- Modify: `apps/portal/DEPLOY.md`
- Modify: `docs/operations/connect-x-oauth.md`

- [ ] **Step 1: Add config tests**

Add a config test that `CONNECT_TOKEN_ENCRYPTION_KEY` decodes to 32 bytes when X broker is configured, and fails when malformed.

- [ ] **Step 2: Implement config field**

Add `ConnectTokenEncryptionKey []byte` to config. Decode standard base64. In tests, `LoadForTest` uses `bytes.Repeat([]byte{1}, 32)`.

- [ ] **Step 3: Wire PostgresTokenStore**

In `main.go`, if `connectAdminStore != nil`, `connectHandler.TokenStore = connect.NewPostgresTokenStore(pool, cipher)`.

- [ ] **Step 4: Update docs/env**

Document:

```text
CONNECT_TOKEN_ENCRYPTION_KEY=<base64 32-byte key>
```

and the operational requirement that X users must reconnect once after rollout.

- [ ] **Step 5: Run focused verification**

Run:

```bash
cd apps/portal && go test ./...
cd ../kittypaw && go test ./...
```

Expected: all pass.

- [ ] **Step 6: Run repo smoke**

Run:

```bash
make smoke-local
```

Expected: all smoke checks pass.

- [ ] **Step 7: Final commit**

```bash
git add apps/portal apps/kittypaw docs/operations/connect-x-oauth.md
git commit -m "docs(connect): document x broker quota rollout"
```

