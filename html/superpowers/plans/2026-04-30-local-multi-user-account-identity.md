# Local Multi-User Account Identity Implementation Plan

> Historical plan snapshot. This document records the implementation plan or design state at the time it was written; use repository README, ARCHITECTURE.md, and app README/DEPLOY docs for the current live shape.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make fresh setup create `~/.kittypaw/accounts/<username>/` with local login credentials, and route Web UI/API access by authenticated account instead of implicit `default`.

**Architecture:** Add a server-wide local auth store, account-aware setup/account-add flows, explicit CLI account selection, and request-scoped account resolution in the server. Keep `default` only for legacy migration and compatibility.

**Tech Stack:** Go, Cobra CLI, chi middleware, existing `core.Account`, `server.AccountRouter`, JSON auth store, Argon2id password hashing.

---

## File Structure

- Create: `core/local_auth.go`  
  Server-wide local auth store at `~/.kittypaw/auth.json`, password hashing, credential CRUD, verification.

- Create: `core/local_auth_test.go`  
  Hash verification, duplicate prevention, disabled users, corrupt file handling, atomic writes.

- Modify: `core/config.go`  
  Add `ConfigPathForAccount(accountID string)` and keep `ConfigPath()` as legacy/default wrapper.

- Modify: `core/secrets.go`  
  Keep `LoadAccountSecrets(accountID string)` account-explicit and update callers that still pass `core.DefaultAccountID`.

- Modify: `core/account_setup.go`  
  Let account provisioning register credentials transactionally.

- Modify: `cli/init_wizard.go`  
  Collect account ID/password before LLM/channel setup.

- Modify: `cli/main.go`  
  Add setup flags, route setup writes to selected account, add active-account resolution helper, stop bare multi-account writes.

- Modify: `cli/cmd_account.go`  
  Add `--password-stdin` and interactive password prompts for `account add`.

- Modify: `client/daemon.go`  
  Stop assuming `accounts/default/config.toml` when resolving server endpoint.

- Modify: `server/server.go`  
  Store deps by account ID, choose default only for compatibility, add request account helpers.

- Modify: `server/api_auth.go`  
  New local Web UI auth endpoints and session cookie handling.

- Modify: `server/api_setup.go`  
  Make setup status/complete account-aware and prevent unauthenticated setup mutation once local users exist.

- Modify: `server/ws.go`  
  Route WebSocket chat to the authenticated account session.

- Modify: `server/web/app.js`, `server/web/onboarding.js`, `server/web/chat.js`  
  Add login gate and stop relying on unauthenticated bootstrap for account access.

- Modify tests under `cli/`, `client/`, `server/`, `core/` matching each behavior.

---

### Task 1: Core Local Auth Store

**Files:**
- Create: `core/local_auth.go`
- Create: `core/local_auth_test.go`

- [ ] **Step 1: Write failing tests for credential lifecycle**

Create `core/local_auth_test.go` with tests covering:

```go
func TestLocalAuthStoreCreateAndVerify(t *testing.T) {
    root := t.TempDir()
    st := NewLocalAuthStore(filepath.Join(root, "auth.json"))

    if err := st.CreateUser("alice", "correct horse battery staple"); err != nil {
        t.Fatalf("CreateUser: %v", err)
    }
    if ok, err := st.VerifyPassword("alice", "correct horse battery staple"); err != nil || !ok {
        t.Fatalf("VerifyPassword good = (%v, %v), want true nil", ok, err)
    }
    if ok, err := st.VerifyPassword("alice", "wrong"); err != nil || ok {
        t.Fatalf("VerifyPassword bad = (%v, %v), want false nil", ok, err)
    }
}

func TestLocalAuthStoreRejectsDuplicateAndInvalidID(t *testing.T) {
    root := t.TempDir()
    st := NewLocalAuthStore(filepath.Join(root, "auth.json"))

    if err := st.CreateUser("alice", "pw"); err != nil {
        t.Fatalf("CreateUser alice: %v", err)
    }
    if err := st.CreateUser("alice", "pw2"); !errors.Is(err, ErrLocalUserExists) {
        t.Fatalf("duplicate err = %v, want ErrLocalUserExists", err)
    }
    if err := st.CreateUser("../bad", "pw"); err == nil {
        t.Fatal("CreateUser accepted invalid account id")
    }
}

func TestLocalAuthStoreDisabledUserCannotLogin(t *testing.T) {
    root := t.TempDir()
    st := NewLocalAuthStore(filepath.Join(root, "auth.json"))
    if err := st.CreateUser("alice", "pw"); err != nil {
        t.Fatalf("CreateUser: %v", err)
    }
    if err := st.SetDisabled("alice", true); err != nil {
        t.Fatalf("SetDisabled: %v", err)
    }
    if ok, err := st.VerifyPassword("alice", "pw"); err != nil || ok {
        t.Fatalf("disabled VerifyPassword = (%v, %v), want false nil", ok, err)
    }
}
```

- [ ] **Step 2: Run tests to verify failure**

Run:

```bash
go test ./core -run 'TestLocalAuthStore' -count=1
```

Expected: compile fails because `NewLocalAuthStore` and `ErrLocalUserExists` do not exist.

- [ ] **Step 3: Implement auth store**

Create `core/local_auth.go` with:

```go
package core

import (
    "crypto/rand"
    "crypto/subtle"
    "encoding/base64"
    "encoding/json"
    "errors"
    "fmt"
    "os"
    "path/filepath"
    "time"

    "golang.org/x/crypto/argon2"
)

var ErrLocalUserExists = errors.New("local user already exists")

type LocalAuthStore struct {
    path string
}

type LocalAuthFile struct {
    Version int                    `json:"version"`
    Users   map[string]LocalUser   `json:"users"`
}

type LocalUser struct {
    AccountID    string    `json:"account_id"`
    PasswordHash string    `json:"password_hash"`
    Disabled     bool      `json:"disabled,omitempty"`
    CreatedAt    time.Time `json:"created_at"`
    UpdatedAt    time.Time `json:"updated_at"`
}

func NewLocalAuthStore(path string) *LocalAuthStore {
    return &LocalAuthStore{path: path}
}

func LocalAuthPath() (string, error) {
    dir, err := ConfigDir()
    if err != nil {
        return "", err
    }
    return filepath.Join(dir, "auth.json"), nil
}

func (s *LocalAuthStore) CreateUser(accountID, password string) error {
    if err := ValidateAccountID(accountID); err != nil {
        return err
    }
    if password == "" {
        return errors.New("password is required")
    }
    f, err := s.load()
    if err != nil {
        return err
    }
    if _, ok := f.Users[accountID]; ok {
        return fmt.Errorf("%w: %s", ErrLocalUserExists, accountID)
    }
    h, err := hashPassword(password)
    if err != nil {
        return err
    }
    now := time.Now().UTC()
    f.Users[accountID] = LocalUser{
        AccountID: accountID,
        PasswordHash: h,
        CreatedAt: now,
        UpdatedAt: now,
    }
    return s.save(f)
}

func (s *LocalAuthStore) VerifyPassword(accountID, password string) (bool, error) {
    f, err := s.load()
    if err != nil {
        return false, err
    }
    u, ok := f.Users[accountID]
    if !ok || u.Disabled {
        return false, nil
    }
    return verifyPassword(u.PasswordHash, password)
}

func (s *LocalAuthStore) SetDisabled(accountID string, disabled bool) error {
    f, err := s.load()
    if err != nil {
        return err
    }
    u, ok := f.Users[accountID]
    if !ok {
        return fmt.Errorf("local user %q not found", accountID)
    }
    u.Disabled = disabled
    u.UpdatedAt = time.Now().UTC()
    f.Users[accountID] = u
    return s.save(f)
}

func (s *LocalAuthStore) load() (*LocalAuthFile, error) {
    b, err := os.ReadFile(s.path)
    if os.IsNotExist(err) {
        return &LocalAuthFile{Version: 1, Users: map[string]LocalUser{}}, nil
    }
    if err != nil {
        return nil, err
    }
    var f LocalAuthFile
    if err := json.Unmarshal(b, &f); err != nil {
        return nil, fmt.Errorf("parse local auth store: %w", err)
    }
    if f.Users == nil {
        f.Users = map[string]LocalUser{}
    }
    return &f, nil
}

func (s *LocalAuthStore) save(f *LocalAuthFile) error {
    if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
        return err
    }
    b, err := json.MarshalIndent(f, "", "  ")
    if err != nil {
        return err
    }
    tmp := s.path + ".tmp"
    if err := os.WriteFile(tmp, b, 0o600); err != nil {
        return err
    }
    return os.Rename(tmp, s.path)
}

func hashPassword(password string) (string, error) {
    salt := make([]byte, 16)
    if _, err := rand.Read(salt); err != nil {
        return "", err
    }
    sum := argon2.IDKey([]byte(password), salt, 3, 64*1024, 4, 32)
    return "argon2id$v=1$m=65536,t=3,p=4$" +
        base64.RawStdEncoding.EncodeToString(salt) + "$" +
        base64.RawStdEncoding.EncodeToString(sum), nil
}

func verifyPassword(encoded, password string) (bool, error) {
    params, salt, expected, err := parsePasswordHash(encoded)
    if err != nil {
        return false, err
    }
    actual := argon2.IDKey([]byte(password), salt, params.time, params.memory, params.threads, uint32(len(expected)))
    return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}
```

Also implement the small `parsePasswordHash` helper in the same file. Keep the encoded format exact so future migrations can parse `argon2id$v=1$m=65536,t=3,p=4$...$...`.

- [ ] **Step 4: Run tests**

Run:

```bash
go test ./core -run 'TestLocalAuthStore' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add core/local_auth.go core/local_auth_test.go go.mod go.sum
git commit -m "feat: add local auth store"
```

---

### Task 2: Account-Scoped Config And CLI Account Selection

**Files:**
- Modify: `core/config.go`
- Modify: `core/secrets.go`
- Modify: `cli/main.go`
- Modify: `core/config_test.go`
- Modify: `cli/main_test.go`

- [ ] **Step 1: Write failing tests**

Add tests asserting:

```go
func TestConfigPathForAccount(t *testing.T) {
    t.Setenv("KITTYPAW_CONFIG_DIR", t.TempDir())
    got, err := ConfigPathForAccount("alice")
    if err != nil {
        t.Fatal(err)
    }
    if !strings.HasSuffix(got, filepath.Join("accounts", "alice", "config.toml")) {
        t.Fatalf("ConfigPathForAccount = %q", got)
    }
}

func TestConfigPathForAccountRejectsInvalidID(t *testing.T) {
    t.Setenv("KITTYPAW_CONFIG_DIR", t.TempDir())
    if _, err := ConfigPathForAccount("../bad"); err == nil {
        t.Fatal("expected invalid account id error")
    }
}
```

Add CLI tests for active account selection:

```go
func TestResolveCLIAccountSingleAccount(t *testing.T) {
    root := t.TempDir()
    t.Setenv("KITTYPAW_CONFIG_DIR", root)
    mustWriteConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))
    got, err := resolveCLIAccount("")
    if err != nil || got != "alice" {
        t.Fatalf("resolveCLIAccount = %q, %v; want alice nil", got, err)
    }
}

func TestResolveCLIAccountMultipleRequiresExplicit(t *testing.T) {
    root := t.TempDir()
    t.Setenv("KITTYPAW_CONFIG_DIR", root)
    mustWriteConfig(t, filepath.Join(root, "accounts", "alice", "config.toml"))
    mustWriteConfig(t, filepath.Join(root, "accounts", "bob", "config.toml"))
    if _, err := resolveCLIAccount(""); err == nil {
        t.Fatal("expected multiple account error")
    }
}
```

- [ ] **Step 2: Run tests to verify failure**

```bash
go test ./core -run 'TestConfigPathForAccount' -count=1
go test ./cli -run 'TestResolveCLIAccount' -count=1
```

Expected: compile failures for missing functions.

- [ ] **Step 3: Implement path helpers**

In `core/config.go` add:

```go
func ConfigPathForAccount(accountID string) (string, error) {
    if err := ValidateAccountID(accountID); err != nil {
        return "", err
    }
    dir, err := ConfigDir()
    if err != nil {
        return "", err
    }
    return filepath.Join(dir, "accounts", accountID, "config.toml"), nil
}

func ConfigPath() (string, error) {
    return ConfigPathForAccount(DefaultAccountID)
}
```

Keep existing callers compiling initially.

- [ ] **Step 4: Implement CLI account resolver**

In `cli/main.go` add a helper near server/config helpers:

```go
func resolveCLIAccount(explicit string) (string, error) {
    if explicit != "" {
        if err := core.ValidateAccountID(explicit); err != nil {
            return "", err
        }
        return explicit, nil
    }
    if env := strings.TrimSpace(os.Getenv("KITTYPAW_ACCOUNT")); env != "" {
        if err := core.ValidateAccountID(env); err != nil {
            return "", err
        }
        return env, nil
    }
    cfgDir, err := core.ConfigDir()
    if err != nil {
        return "", err
    }
    accounts, err := core.DiscoverAccounts(filepath.Join(cfgDir, "accounts"))
    if err != nil {
        return "", err
    }
    if len(accounts) == 1 {
        return accounts[0].ID, nil
    }
    if len(accounts) == 0 {
        return "", errors.New("no accounts found; run `kittypaw setup` first")
    }
    ids := make([]string, 0, len(accounts))
    for _, a := range accounts {
        ids = append(ids, a.ID)
    }
    sort.Strings(ids)
    return "", fmt.Errorf("multiple accounts found (%s); pass --account or set KITTYPAW_ACCOUNT", strings.Join(ids, ", "))
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./core -run 'TestConfigPathForAccount' -count=1
go test ./cli -run 'TestResolveCLIAccount' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add core/config.go core/config_test.go cli/main.go cli/main_test.go
git commit -m "feat: resolve active local account explicitly"
```

---

### Task 3: Fresh Setup Creates Named Account And Credential

**Files:**
- Modify: `cli/main.go`
- Modify: `cli/init_wizard.go`
- Modify: `cli/cmd_setup_test.go`
- Modify: `cli/cmd_setup_e2e_test.go`
- Modify: `core/account_setup.go`

- [ ] **Step 1: Write failing setup tests**

Add tests for:

```go
func TestSetupWritesNamedAccount(t *testing.T) {
    root := t.TempDir()
    t.Setenv("KITTYPAW_CONFIG_DIR", root)

    flags := &setupFlags{
        accountID: "alice",
        provider: "local",
        localURL: "http://localhost:11434/v1",
        localModel: "llama3",
        noChat: true,
        noService: true,
    }
    stdin := strings.NewReader("secret-password\n")
    cmd := newSetupCmd()
    cmd.SetIn(stdin)

    if err := runSetup(cmd, flags); err != nil {
        t.Fatalf("runSetup: %v", err)
    }
    mustExist(t, filepath.Join(root, "accounts", "alice", "config.toml"))
    mustNotExist(t, filepath.Join(root, "accounts", "default", "config.toml"))
    if ok, err := core.NewLocalAuthStore(filepath.Join(root, "auth.json")).VerifyPassword("alice", "secret-password"); err != nil || !ok {
        t.Fatalf("VerifyPassword = %v, %v; want true nil", ok, err)
    }
}
```

Also add a test that bare setup with multiple existing accounts fails before writing.

- [ ] **Step 2: Run tests to verify failure**

```bash
go test ./cli -run 'TestSetupWritesNamedAccount|TestSetupMultipleAccounts' -count=1
```

Expected: compile failure for missing `setupFlags.accountID` and password handling.

- [ ] **Step 3: Add setup flags**

Extend `setupFlags` in `cli/init_wizard.go`:

```go
type setupFlags struct {
    accountID string
    passwordStdin bool
    ...
}
```

Register flags in `newSetupCmd`:

```go
cmd.Flags().StringVar(&flags.accountID, "account", "", "Local account ID to create or configure")
cmd.Flags().BoolVar(&flags.passwordStdin, "password-stdin", false, "Read local Web UI password from stdin")
```

- [ ] **Step 4: Implement credential collection**

Add helpers in `cli/init_wizard.go`:

```go
func promptAccountID(scanner *bufio.Scanner, stdout io.Writer) (string, error)
func promptPassword(confirm bool) (string, error)
func resolveSetupPassword(flags setupFlags, stdin io.Reader) (string, error)
```

Rules:

- Interactive fresh setup prompts account ID/password.
- Non-interactive fresh setup requires `--account` and `--password-stdin`.
- Existing account setup only requires password if no auth user exists for that account.

- [ ] **Step 5: Route setup writes to account path**

In `runSetup`:

1. Determine account ID before `core.ConfigPath`.
2. Use `core.ConfigPathForAccount(accountID)`.
3. Create `~/.kittypaw/accounts/<accountID>/`.
4. Create local auth user in `auth.json` if missing.
5. Save API server URL with `core.LoadAccountSecrets(accountID)`.
6. Ensure staff directory under `accounts/<accountID>/`.

Do not create top-level `data/` or `skills/` for fresh setup.

- [ ] **Step 6: Run tests**

```bash
go test ./cli -run 'TestSetupWritesNamedAccount|TestSetupMultipleAccounts' -count=1
go test ./cli -run 'TestSetup' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add cli/main.go cli/init_wizard.go cli/cmd_setup_test.go cli/cmd_setup_e2e_test.go core/account_setup.go
git commit -m "feat: create named account during setup"
```

---

### Task 4: Account Add Creates Credential

**Files:**
- Modify: `core/account_setup.go`
- Modify: `core/account_test.go`
- Modify: `cli/cmd_account.go`
- Modify: `cli/cmd_account_test.go`

- [ ] **Step 1: Write failing tests**

Add CLI tests:

```go
func TestAccountAddCreatesAuthUser(t *testing.T) {
    root := t.TempDir()
    t.Setenv("KITTYPAW_CONFIG_DIR", root)
    f := &accountAddFlags{
        isFamily: true,
        passwordStdin: true,
        noActivate: true,
    }
    in := strings.NewReader("pw123\n")
    var out, errOut bytes.Buffer
    if err := runAccountAdd("alice", f, in, &out, &errOut); err != nil {
        t.Fatalf("runAccountAdd: %v", err)
    }
    if ok, err := core.NewLocalAuthStore(filepath.Join(root, "auth.json")).VerifyPassword("alice", "pw123"); err != nil || !ok {
        t.Fatalf("VerifyPassword = %v, %v; want true nil", ok, err)
    }
}
```

Add a rollback test that simulates duplicate auth user and confirms no account directory is committed.

- [ ] **Step 2: Run tests to verify failure**

```bash
go test ./cli -run 'TestAccountAddCreatesAuthUser|TestAccountAddAuthRollback' -count=1
```

Expected: compile failure for `passwordStdin`.

- [ ] **Step 3: Add flags and password resolution**

In `accountAddFlags`:

```go
passwordStdin bool
```

Register:

```go
cmd.Flags().BoolVar(&f.passwordStdin, "password-stdin", false, "Read local Web UI password from stdin")
```

Implement:

```go
func resolveAccountPassword(f *accountAddFlags, stdin io.Reader) (string, error)
```

Interactive path prompts password and confirmation when stdin is a TTY.

- [ ] **Step 4: Make provisioning transactional**

Add an option to `core.AccountOpts`:

```go
LocalPassword string
```

In `InitAccount`, after staging config but before final rename, create the auth user in `auth.json` only after validating no duplicate account directory exists. If auth creation fails, remove staging and return.

Implementation detail: because `auth.json` is outside the staging dir, prefer creating auth immediately before final rename and deleting the auth entry if final rename fails. Add:

```go
func (s *LocalAuthStore) DeleteUser(accountID string) error
```

Use it only for rollback.

- [ ] **Step 5: Run tests**

```bash
go test ./core -run 'TestInitAccount' -count=1
go test ./cli -run 'TestAccountAdd' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add core/account_setup.go core/account_test.go core/local_auth.go core/local_auth_test.go cli/cmd_account.go cli/cmd_account_test.go
git commit -m "feat: create local credentials for added accounts"
```

---

### Task 5: Server Boot And Client Discovery Stop Assuming Default

**Files:**
- Modify: `core/config.go`
- Modify: `client/daemon.go`
- Modify: `client/daemon_test.go`
- Modify: `server/server.go`
- Modify: `server/account_session_test.go`
- Modify: `cli/main.go`

- [ ] **Step 1: Write failing tests**

Add tests for:

```go
func TestDaemonConnSingleNonDefaultAccount(t *testing.T) {
    dir := t.TempDir()
    t.Setenv("KITTYPAW_CONFIG_DIR", dir)
    writeFile(t, filepath.Join(dir, "accounts", "alice", "config.toml"),
        "[server]\nbind = \"127.0.0.1:4567\"\napi_key = \"account-key\"\n")
    d, err := NewDaemonConn("")
    if err != nil {
        t.Fatalf("NewDaemonConn: %v", err)
    }
    if d.BaseURL != "http://127.0.0.1:4567" {
        t.Fatalf("BaseURL = %q", d.BaseURL)
    }
}

func TestDaemonConnMultipleAccountsNeedsServerToml(t *testing.T) {
    dir := t.TempDir()
    t.Setenv("KITTYPAW_CONFIG_DIR", dir)
    writeFile(t, filepath.Join(dir, "accounts", "alice", "config.toml"), "[server]\nbind = \"127.0.0.1:4567\"\n")
    writeFile(t, filepath.Join(dir, "accounts", "bob", "config.toml"), "[server]\nbind = \"127.0.0.1:4568\"\n")
    if _, err := NewDaemonConn(""); err == nil {
        t.Fatal("expected ambiguity error")
    }
}
```

- [ ] **Step 2: Run tests to verify failure**

```bash
go test ./client -run 'TestDaemonConnSingleNonDefaultAccount|TestDaemonConnMultipleAccountsNeedsServerToml' -count=1
```

Expected: failure because discovery checks `accounts/default`.

- [ ] **Step 3: Update server endpoint resolution**

In `client/daemon.go`:

1. Keep `server.toml` as highest priority.
2. If exactly one account exists, use that account config.
3. If multiple accounts exist and `server.toml` is incomplete, fail with an actionable message.
4. Keep legacy root config fallback for pre-start state only when accounts are absent.

- [ ] **Step 4: Update server default selection**

In `server.New`, select the default account as:

1. `server.toml.default_account`, after `TopLevelServerConfig.DefaultAccount` is wired into bootstrap.
2. `default`, if present, for legacy compatibility.
3. First account, for single-account/non-default installs.

Make comments explicit that `DefaultAccountID` is legacy compatibility, not fresh setup behavior.

- [ ] **Step 5: Run tests**

```bash
go test ./client ./server -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add client/daemon.go client/daemon_test.go server/server.go server/account_session_test.go cli/main.go core/config.go
git commit -m "fix: discover non-default local accounts"
```

---

### Task 6: Local Web UI Login And Sessions

**Files:**
- Create: `server/api_auth.go`
- Create: `server/api_auth_test.go`
- Modify: `server/server.go`
- Modify: `server/web/app.js`
- Modify: `server/web/style.css`

- [ ] **Step 1: Write failing auth endpoint tests**

Add `server/api_auth_test.go`:

```go
func TestAuthLoginSetsSessionCookie(t *testing.T) {
    srv := newServerWithLocalUser(t, "alice", "pw")
    req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"account_id":"alice","password":"pw"}`))
    req.Header.Set("Content-Type", "application/json")
    rr := httptest.NewRecorder()
    srv.setupRoutes().ServeHTTP(rr, req)
    if rr.Code != http.StatusOK {
        t.Fatalf("login code = %d body=%s", rr.Code, rr.Body.String())
    }
    if len(rr.Result().Cookies()) == 0 {
        t.Fatal("expected session cookie")
    }
}

func TestAuthLoginRejectsWrongPassword(t *testing.T) {
    srv := newServerWithLocalUser(t, "alice", "pw")
    req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"account_id":"alice","password":"bad"}`))
    req.Header.Set("Content-Type", "application/json")
    rr := httptest.NewRecorder()
    srv.setupRoutes().ServeHTTP(rr, req)
    if rr.Code != http.StatusUnauthorized {
        t.Fatalf("login code = %d, want 401", rr.Code)
    }
}
```

- [ ] **Step 2: Run tests to verify failure**

```bash
go test ./server -run 'TestAuthLogin' -count=1
```

Expected: 404 for missing route.

- [ ] **Step 3: Implement auth routes**

In `server/setupRoutes` add before setup/API routes:

```go
r.Route("/api/auth", func(r chi.Router) {
    r.Post("/login", s.handleAuthLogin)
    r.Post("/logout", s.handleAuthLogout)
    r.Get("/me", s.handleAuthMe)
})
```

Create `server/api_auth.go` with:

- login body `{account_id,password}`
- `LocalAuthStore.VerifyPassword`
- signed opaque session cookie
- session expiry
- logout cookie clear
- `me` returns `{account_id}`

For MVP, an HMAC-signed cookie is acceptable. If a server-side session DB is added later, keep the handler contract unchanged.

- [ ] **Step 4: Add Web UI login gate**

In `server/web/app.js`:

1. On startup, call `/api/auth/me`.
2. If 401, render login form.
3. On login success, proceed to setup/status/bootstrap.
4. Never call `/api/bootstrap` before login when auth users exist.

- [ ] **Step 5: Run tests**

```bash
go test ./server -run 'TestAuth' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add server/api_auth.go server/api_auth_test.go server/server.go server/web/app.js server/web/style.css
git commit -m "feat: add local web login"
```

---

### Task 7: Request-Scoped Account Routing For WebSocket Chat

**Files:**
- Modify: `server/server.go`
- Modify: `server/ws.go`
- Modify: `server/ws_validate_test.go`
- Modify: `server/account_session_test.go`
- Modify: `server/web/chat.js`

- [ ] **Step 1: Write failing cross-account WebSocket test**

Add a server test that creates `alice` and `bob`, logs in as `bob`, opens `/ws`, sends a chat frame, and asserts the resulting event/session uses `bob`, not `default` or `alice`.

The test should fail before implementation because `handleWebSocket` uses `s.session`.

- [ ] **Step 2: Run test to verify failure**

```bash
go test ./server -run 'TestWebSocketUsesAuthenticatedAccount' -count=1
```

Expected: FAIL showing wrong account/session.

- [ ] **Step 3: Add request account resolution helper**

In `server/server.go` add:

```go
type requestAccount struct {
    ID string
    Deps *AccountDeps
    Session *engine.Session
}

func (s *Server) requestAccount(r *http.Request) (*requestAccount, error)
```

Resolution rules:

1. Browser session cookie account ID.
2. Account-scoped legacy API key, resolved by scanning active account configs for a matching key.
3. If no auth users exist and only one account exists, compatibility fallback.
4. Otherwise reject.

- [ ] **Step 4: Use request account in WebSocket**

In `handleWebSocket`, replace `s.session` references with `acct.Session` after auth/session resolution.

Keep existing API key token behavior for compatibility, but if more than one account is active, require session or account-scoped token.

- [ ] **Step 5: Update frontend**

In `server/web/chat.js`, stop appending the account config API key from `/api/bootstrap` for logged-in browser sessions. Use cookie session auth for `/ws`. Keep token support only for legacy/API clients.

- [ ] **Step 6: Run tests**

```bash
go test ./server -run 'TestWebSocket|TestAuth' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add server/server.go server/ws.go server/ws_validate_test.go server/account_session_test.go server/web/chat.js
git commit -m "feat: route websocket chat by logged-in account"
```

---

### Task 8: Account-Scoped Setup And Bootstrap

**Files:**
- Modify: `server/api_setup.go`
- Modify: `server/api_setup_fallback_test.go`
- Modify: `server/web/onboarding.js`
- Modify: `server/web/app.js`

- [ ] **Step 1: Write failing tests**

Add tests:

```go
func TestSetupStatusUsesLoggedInAccount(t *testing.T) {
    srv := newTwoAccountServer(t)
    cookie := loginCookie(t, srv, "bob", "pw")
    req := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
    req.AddCookie(cookie)
    rr := httptest.NewRecorder()
    srv.setupRoutes().ServeHTTP(rr, req)
    if rr.Code != http.StatusOK {
        t.Fatalf("status code = %d", rr.Code)
    }
    // Assert response reflects bob's config, not alice/default.
}
```

Add a test that mutating setup endpoints reject unauthenticated requests once `auth.json` has users.

- [ ] **Step 2: Run tests to verify failure**

```bash
go test ./server -run 'TestSetupStatusUsesLoggedInAccount|TestSetupRejectsUnauthenticated' -count=1
```

Expected: FAIL because setup handlers use `s.config`/`s.store`.

- [ ] **Step 3: Make setup account-aware**

For setup handlers:

- `handleSetupStatus` reads account config/store from request account when auth users exist.
- Mutating setup routes require either localhost first-run with no local users, or authenticated account session.
- `handleSetupComplete` writes `accounts/<accountID>/config.toml`.
- Kakao/API URL secrets use the logged-in account ID.
- Reconcile uses the logged-in account ID.

- [ ] **Step 4: Update web onboarding**

In `server/web/onboarding.js`, include account creation fields only for first-run setup with no users. For existing logged-in users, setup configures that user's account.

- [ ] **Step 5: Run tests**

```bash
go test ./server -run 'TestSetup' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add server/api_setup.go server/api_setup_fallback_test.go server/web/onboarding.js server/web/app.js
git commit -m "feat: scope setup to local account"
```

---

### Task 9: Documentation And Compatibility Sweep

**Files:**
- Modify: `CLAUDE.md`
- Modify: `docs/deployment.md`
- Modify: `README.md`
- Modify tests mentioning `accounts/default`

- [ ] **Step 1: Update docs**

Document:

```text
kittypaw setup --account alice --password-stdin
kittypaw account add bob --password-stdin
KITTYPAW_ACCOUNT=bob kittypaw chat
kittypaw chat --account bob
```

State explicitly:

- Fresh installs create named accounts.
- Existing installs may still have `accounts/default`.
- Multiple accounts require explicit CLI account selection.
- Local Web UI requires login once local users exist.

- [ ] **Step 2: Run full tests**

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md docs/deployment.md README.md cli core server client
git commit -m "docs: document local multi-user accounts"
```

---

## Self-Review Notes

- This plan intentionally does not implement hosted relay yet. It creates the account/auth foundation that hosted relay requires.
- The major blast radius is account-scoping HTTP handlers. The plan handles WebSocket chat and setup first because those are the most visible Web UI overlap points.
- Legacy `default` remains valid for migration. Fresh setup stops creating it.
- Local password and hosted cloud password remain separate. Remote pairing later maps a cloud user to a local account without sending the local password to the cloud.
