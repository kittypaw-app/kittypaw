# KittyPaw

AI runner framework with JavaScript sandbox execution, multi-channel messaging, and skill learning.

## Architecture

```
cli/           CLI binary (Cobra)
core/          Types, config, skill management, staff identities/presets, account isolation, WebSocket protocol, setup wizard shared logic
llm/           LLM provider abstraction (Claude, OpenAI, Ollama)
mcp/           MCP client registry (external tool server connections)
sandbox/       JavaScript execution sandbox (in-process goja VM, Runner.observe interrupts)
store/         SQLite persistence with 21 migrations (WAL mode)
engine/        Runner loop (observe + retry), skill executor, HTML-to-Markdown, SearchBackend, compaction, scheduling
channel/       Messaging channels (Telegram, Slack, Discord, Kakao, WebSocket)
server/        HTTP API (Chi) + WebSocket streaming + ChannelSpawner (hot-reload)
client/        REST/WS client + DaemonConn (thin client: auto server discovery/start)
remote/        Hosted-service connectors such as Chat relay
```

## Key Design Decisions (vs Rust original)

- **No CGO**: Uses `modernc.org/sqlite` (pure Go) instead of sqlite3
- **In-process sandbox**: Uses `goja` (pure Go JS engine) instead of fork+Seatbelt+QuickJS
- **Official SDKs**: Raw HTTP for Anthropic/OpenAI APIs (with SSE streaming)
- **Goroutines**: Replace tokio async with goroutines + channels
- **Chi router**: Replaces Axum for HTTP routing
- **Cobra CLI**: Replaces Clap for command-line parsing
- **Multi-account BaseDir**: All filesystem operations use `Session.BaseDir` via `*From(baseDir, ...)` function variants, enabling per-account data isolation without engine/handler changes
- **Account routing**: Single server serves N personal accounts + optional team-space account. The persisted flag remains `is_shared=true` for config compatibility, but public copy and operator docs should say team space / 팀공간. `AccountRouter` fans inbound events to the right `Session` by `Event.AccountID`; `ChannelSpawner` keys by `(accountID, channelType)` — each personal account hosts at most one channel per type (one Telegram bot, one Kakao relay, etc). Mental model: personal account == one human user; team-space account == team-space coordinator and data owner. Multi-channel-per-account (e.g. two Telegram bots under one account) is out of scope. `ValidateAccountID` also accepts leading-underscore IDs like `_default_`/`_shared_` for future reserved-form use — no boot logic auto-creates them. Provisioning via `kittypaw account add <name>` (`cli/cmd_account.go`, `core/account_setup.go`) stages the directory under `.<id>.staging/` and renames atomically; duplicate bot-token validation runs pre-commit so collisions fail before any filesystem write. Team-space accounts are created directly with `kittypaw account add <name> --is-shared`; there is no separate wizard. When a server is already running, the CLI auto-activates via `POST /api/v1/admin/accounts` (`server/admin.go:AddAccount`) — no restart required (AC-U3). `AddAccount` serializes under `accountMu` (shared with `RemoveAccount`), re-runs the bot-token collision check against the live account snapshot, opens per-account deps via `OpenAccountDeps` (same path as boot), stores them in `Server.accountDeps` keyed by account ID, and uses an LIFO rollback stack so a failed channel reconcile fully unwinds registry/router/accountList/accountDeps side effects. `--no-activate` stages files only. Decommissioning uses `kittypaw account remove <name>`: (1) if the server is running, `POST /api/v1/admin/accounts/{id}/delete` drains channels via `Reconcile(id, nil)` and tears down registry entries in exact LIFO order (`accounts.Remove` → `accountList` pop → `accountRegistry.Unregister` → `delete(accountDeps,id)` → `AccountDeps.Close`); (2) server-off or server-missing-config paths skip the RPC silently; (3) if the removed account is not itself a team-space account, the CLI scrubs membership from `[team_space].members` and removes any legacy `[share.<removed>]` stanza from team-space config via `WriteConfigAtomic`; (4) the account directory is moved to `.trash/<id>-<YYYYMMDDHHMMSS>/` (collision-suffixed) so operators can recover it; (5) the CLI prints reminders to revoke the BotFather token and re-pair Kakao manually — the server never touches external credentials. See `core/account.go`, `server/account_router.go`, `server/admin.go`, `cli/cmd_account.go`.
- **Local account identity**: Fresh setup creates `~/.kittypaw/accounts/<accountID>/`, not `accounts/default/`, when invoked with `kittypaw setup --account <id>`; upgraded installs with `accounts/default/` remain valid. Local Web UI credentials live in server-wide `~/.kittypaw/auth.json` and are created during `setup` / `account add` (`--password-stdin` for scripts). Once auth users exist, `/api/auth/login` issues a signed session cookie and setup/WebSocket chat are routed to the logged-in account. Non-default browser bootstrap returns a WS URL without a default-account API key; `/api/v1` remains default-bound until a later account-scoped HTTP API pass.
- **Account panic isolation (AC-T8)**: Every worker goroutine wraps work in `engine.RecoverAccountPanic` (or the `engine.runWithAccountRecover` helper). A panic marks only the owning account `Degraded` via `core.HealthState` (atomic state machine: Ready / Degraded / Stopped-terminal) and never propagates to siblings; clean completion promotes back to `Ready`. Chokepoints: scheduler `tickOnce` / `reflectionTick` / skill goroutine / package goroutine, and `server.dispatchLoop` per-event. Health state is nil-safe for bare-struct test fixtures.

## Team Space (cross-account read + fanout)

`Config.IsShared = true` marks an account as the team-space coordinator. The TOML key is intentionally still `is_shared = true` for compatibility; code may also retain legacy aliases such as `IsFamily`, `ValidateFamilyAccounts`, and `EventFamilyPush` while comments identify them as aliases. Two JS skills are conditionally exposed:

- **`Share.read(accountID, path)`** — personal member accounts can read shareable data from a team-space account listed in `[team_space].members`. Paths are limited to `memory/...` and configured `workspace/<alias>/...` roots; legacy `[share.<reader>]` per-path allowlists still parse for compatibility but do not grant membership by themselves. Defense in depth: (1) `executeShare` rejects any target whose `Config.IsTeamSpaceAccount() != true`; (2) `sandbox.Options.ExposeShare = !Config.IsTeamSpaceAccount()` — the team-space account itself never sees the `Share` JS global (`typeof Share === "undefined"`), so a compromised team-space skill can't loop back to read personal accounts. Every call emits a `cross_account_read` / `cross_account_read_rejected` audit log; the externally-visible error string is unified across "unknown account" and "not team space" branches to prevent account ID enumeration via error oracle. `core.ValidateSharedReadPath` blocks `..`, absolute paths, symlinks, and hardlink escapes; `ValidateAccountID` rejects hostile IDs before any log/registry touch. Size-capped at `maxFileReadSize` (10 MB).
- **`Fanout.send(accountID, {text, channel_hint})` / `Fanout.broadcast({...})`** — emits `Event{Type: EventTeamSpacePush, AccountID: target}` onto the server eventCh for configured team-space members. `EventFamilyPush` remains a compatibility alias. `Server.dispatchLoop` branches on this event type *before* `event.ParsePayload()` and routes via `deliverTeamSpacePush` — the message is already-composed outbound text, so the runner loop is bypassed (re-invoking the LLM would paraphrase or drop it). Channel selection: `ChannelHint` exact match wins, otherwise the first non-`web_chat` channel (`web_chat` is per-WebSocket, no durable destination); destination is the target account's `AdminChatIDs[0]`. If the channel isn't currently running (hot-reload window), the push lands in `pending_responses` for the retry loop — except Kakao, whose action IDs are ephemeral. Personal accounts never see the `Fanout` JS global (`typeof Fanout === "undefined"`), gated by `sandbox.Options.ExposeFanout` threaded through `ExecuteWithResolverOpts`/`ExecutePackageOpts`. Both flags are produced by `Session.sandboxOptions()` so callsites stay consistent.

Personal accounts cannot invoke `Share.read` against each other — only a team-space account with `[team_space].members` grants member-scoped reads, and only the team-space account's Session has a non-nil `Fanout`. Channel configs on team-space accounts are rejected at config load (`ValidateTeamSpaceAccounts`; `ValidateFamilyAccounts` is the legacy alias). Account-aware schedulers run per account, and removing an account scrubs team-space membership plus legacy share stanzas.

## Skill Install Internals

Supports two source formats:
- **SKILL.md** (agentskills.io standard) — YAML frontmatter + markdown body. Installed in prompt mode (LLM executes with permission-scoped tools) by default, or `--mode native` for JS conversion via teach pipeline.
- **Native** (`package.toml` + `main.js`) — installed directly via PackageManager.

Provenance tracked via `SourceURL`, `SourceHash`, `SourceText` fields on Skill. SHA256 verification for registry packages.

Config fields support `source = "namespace/key"` binding to resolve values from shared `secrets.json` (e.g. `source = "telegram/bot_token"`). Resolution order: package-scoped → source binding → default. Secrets file auto-migrates from flat/mixed formats to canonical nested JSON on first load.

**Official skill boundary (load-bearing)**: official packages are deterministic executors, not natural-language parsers. Intent classification, slot extraction, disambiguation, and multi-skill chaining belong to the engine/LLM layer. Official JS packages may read structured `__context__.params`, structured `__context__.user`, and package `__context__.config`; they must not maintain stop-word lists, Korean particle stripping, regex grammars, or other raw utterance parsing over `ctx.message.text` / `ctx.input`. If a required structured slot is missing, the engine should clarify or resolve it before calling the package; the package should not silently fall back to an unrelated default. Caller-facing LLM requirements belong in `package.toml` (`[discovery]`, `[capabilities.*]`, `[invocation]`, `[[invocation.inputs]]`), not in package JS. See `html/skills/official-skill-contract.md`.

## API Token Management

`kittypaw login` authenticates against a kittypaw-api server via OAuth (Google). Two modes:
- **HTTP callback** (default): local server on `127.0.0.1:0`, browser-based OAuth flow.
- **Code-paste** (`--code`): for SSH/remote sessions, enter a one-time code from the browser.

Tokens stored **per-account** in `~/.kittypaw/accounts/<accountID>/secrets.json` under namespace `kittypaw-api/{host}` (e.g. `kittypaw-api/localhost:8080`). Setup and account-scoped Web UI flows resolve the account from `--account`, `KITTYPAW_ACCOUNT`, or the browser session before writing secrets. `kittypaw login` is still legacy/default-bound until a follow-up adds explicit login account selection. The earlier "OAuth-once-per-host" global model (commit `c1a0c58`) was retired so each account on a shared host can have its own credentials. `APITokenManager` (`core/api_token.go`) handles auto-refresh with single-flight mutex pattern. JWT expiry checked client-side via base64 payload decode (no signature verification) with 30-second grace window.

Before OAuth, `loginHTTP`/`loginCode` call unauthenticated `GET {apiURL}/discovery` (see `core/discovery.go`) to resolve service topology. The response persists service URLs per-host under the portal namespace — `api_base_url`, `chat_relay_url`, `kakao_relay_url`, `skills_registry_url` — with empty-string-deletes semantics so stale URLs don't survive a relay migration. Discovery failures log a warning and fall back to the user-supplied `apiURL` so login works in collapsed deployments.

KakaoTalk relay pairing skips OAuth entirely: `wizardKakao` (CLI) and `handleSetupKakaoRegister` (web) hit `/discovery` anonymously, `POST {kakao_relay_url}/register` directly, then persist the full `wss://…/ws/{token}` under `kittypaw-api/{host}` in the same per-account store. `InjectKakaoWSURL(accountID, channels)` (invoked by `ChannelSpawner.Reconcile` as the single injection site) opens the named account's secrets via `LoadAccountSecrets`, reads the saved relay URL, and populates `KakaoWSURL` on the runtime channel config — the token is never written to `config.toml`, so rotations don't require a config edit. The `Reconcile` signature itself is unchanged (load-bearing sync contract per `TestHandleReload_WaitsForReconcile`); only `InjectKakaoWSURL` learned the account context.

Packages with `source = "kittypaw-api/access_token"` config fields get auto-refreshed tokens at execution time (`engine/executor.go:runSkillOrPackage`).

## HTTP Sandbox Security

`Http.get/post/put/delete/patch/head` support an optional `options` argument: `Http.get(url, {headers: {key: value}})`.
Hop-by-hop headers (`Host`, `Connection`, `Transfer-Encoding`, `Upgrade`, `TE`, `Trailer`) are blocked.

SSRF validation (`validateHTTPTarget`): explicit `allowed_hosts` in `package.toml` takes priority over private IP blocking — packages can declare `allowed_hosts = ["localhost"]` to reach local API servers. The package resolver validates URLs against the package's AllowedHosts and stores the validated hostname in context; `executeHTTP` verifies the actual request hostname matches.

## MoA (Multi-Model Aggregation)

`Moa.query(prompt, options?)` JS skill runs parallel fan-out to multiple named models from config `[[models]]`, then synthesizes the responses via the Default model. Implemented in `engine/moa.go`. Public core: `QueryMoA(ctx, MoARequest, ProviderResolver, *SharedTokenBudget)`; the JS adapter `executeMoA` in the same file fills `MoARequest.Models` from `s.Config.Models` and `SynthesizerModel` from the `Default=true` entry when the caller omits them.

**Design decisions (load-bearing)**:
- **Partial failure tolerant** — uses `sync.WaitGroup` + indexed slots (NOT `errgroup`, whose fail-fast cancel-siblings-on-first-error semantics are wrong for MoA). A single flaky/slow model lands as `candidates[i].Error` without failing the run.
- **Per-model timeout** — each candidate gets `context.WithTimeout` (default `moaDefaultTimeout=30s`, override via `options.per_model_timeout_ms`). Independent of the outer ctx so one slow model can't stall the rest.
- **Hard guardrails** — `len(models) > moaMaxModels (5)` → error (prevents accidental cost explosion from `[[models]]` table growth); `len(models) == 0` → error; `len(models) == 1` at the JS adapter → skip fan-out, degrade to a plain `Llm.generate`-style call + `slog.Info("moa: single-model config, degrading")`.
- **Synthesis skip** — ≤1 successful candidate means synthesis would just paraphrase; `QueryMoA` returns the sole candidate with `synthesized=false` and `model=<candidate model>`. Saves an LLM call and preserves original phrasing.
- **Synthesizer context protection** — per-candidate text is truncated to `moaCandidateCharLimit (8000 chars ≈ 2000 tokens)` before being fed to the synthesizer, bounded by `moaTruncate`. Even 5 candidates stay within 10K chars of synthesizer input.
- **Budget accounting** — `SharedTokenBudget.TrySpendFromUsage` is called for each successful candidate AND the synthesizer response. On exhaustion mid-flight, the in-flight goroutine calls `cancelAll()` to kill pending siblings; already-returned responses are kept (the API cost was paid) to maximize the value of completed work. Synthesizer overshoot is logged warn but not rejected (advisory post-hoc).
- **Synthesizer fallback chain** — if `resolver(SynthesizerModel)` returns nil, fall back to the first successful candidate's model. If that is also unresolvable, return `firstSuccess` with `synthesized=false`. MoA never errors purely due to a misconfigured synthesizer when real candidate data exists.
- **slog key hygiene** — MoA-level log fields use `moa_model` (not `model`) to avoid collision with provider-level slog context.

**JS API** (registered in `core/skillmeta.go` under `Moa`):
```js
Moa.query(prompt, {
  models: ["sonnet", "gpt-4"],    // optional, defaults to all [[models]] names
  synthesizer: "sonnet",            // optional, defaults to Default=true model
  per_model_timeout_ms: 30000       // optional, 30s default
}) → {
  text, model,                       // synthesizer output (or sole candidate when synthesized=false)
  usage: {input_tokens, output_tokens},
  candidates: [{model, text, usage, error?, latency_ms}],
  synthesized: boolean               // false when ≤1 candidate or synthesizer fallback hit
}
```

**Out of scope** (intentionally, to keep scope bounded): streaming synthesis, debate/critique mode, response caching, weighted voting. Each is a separate future plan.

## File.summary + llm_cache

`File.summary(path, options?)` JS skill returns an LLM-generated summary of a workspace file, cached in a generic `llm_cache` SQLite table so repeat calls on unchanged content are free. `options.force_refresh=true` bypasses the lookup and UPSERTs the fresh result (`ON CONFLICT DO UPDATE` under the compound UNIQUE), so a stale or poisoned summary can actually be replaced; `options.model` overrides `[llm].model`. Implemented in `engine/summary.go` (public core `QuerySummary`) + `engine/executor.go:executeFileSummary` + `store/migrations/019_llm_cache.sql`.

**Design decisions (load-bearing)**:
- **Generic `llm_cache` table, not `file_summaries`** — the `kind` column (`"file.summary"` for this feature) lets future LLM-caching features (e.g. diff-summary, commit-summary) share the same table + GC machinery. Compound UNIQUE `(kind, key_hash, input_hash, model, prompt_hash)` makes collision impossible across kinds.
- **key_hash = first16Hex(workspace_id || \x00 || abs_path)** — a NUL separator avoids `ws/a` + `/b` == `ws/a/b` collision. `first16Hex` = lowercase hex of `sha256(b)[:8]` (64 bits, enough inside a compound UNIQUE).
- **prompt_hash auto-invalidation** — `currentPromptHash = first16Hex(summarySystemPrompt + summaryUserTemplate)` is computed at `init()`. Editing either constant yields a new hash, so every existing row becomes a miss on next lookup. This replaces an error-prone manually-bumped `prompt_version int`.
- **Path security mirrors File.read** — `filepath.Abs` + `resolveForValidation` (EvalSymlinks) + `isPathAllowedResolved` in `executeFileSummary`, before the file is even opened. Prevents symlink escape into another account's BaseDir or outside AllowedPaths.
- **Singleflight dedup** — `Session.SummaryFlight *singleflight.Group` collapses concurrent misses for the same `(kind, key_hash, input_hash, model, prompt_hash)` into one provider call. Wired in `server.buildAccountSession` (per-account, nil-safe at callsite). `force_refresh` is deliberately NOT in the flight key: a force arriving during a normal miss's flight is happy with the same response.
- **Budget charge-after-response** (MoA pattern) — `SharedTokenBudget.TrySpendFromUsage(resp.Usage)` is called AFTER `provider.Generate` returns. On exhaustion the row is NOT persisted and the caller gets an error (retry must re-invoke the provider). Nil-safe via `engine.usageInput/usageOutput`.
- **Insert failure graceful degradation (AC-18)** — a DB write failure does NOT mask the computed summary. `slog.Warn("summary: cache insert failed", ...)` and return the result anyway. Tests inject failure via the package-level `summaryInsertOverride` hook.
- **Guard-rails before DB/provider traffic** — `!utf8.Valid(content)` → `"binary content not supported for summary"`; `EstimateTokens > 150_000` → `"file too large for summary: ~N tokens (max M); use File.read with offset/limit instead"`. Both errors are returned with zero I/O so a malicious account cannot burn budget by submitting binary/huge files.
- **Prompt-injection defense in 3 layers** — (1) system prompt tells the model to ignore instructions inside the file content and output only a factual summary; (2) the user template wraps the file payload in explicit `--- FILE CONTENT --- … --- END FILE CONTENT ---` fenced markers so the model can tell prompt from payload even when the file itself tries to impersonate one; (3) the runner loop treats the returned `tool_result` as untrusted input (existing contract, unchanged by this feature).
- **GC on file removal (load-bearing)** — `FTS5Indexer.RemoveFile(workspaceID, absPath)` calls `store.DeleteLLMCacheByKeyHash("file.summary", key_hash)` *after* `DeleteWorkspaceFilesByPrefix` succeeds. The GC is best-effort (log-warn on error, don't fail the primary delete) because the FTS index is the load-bearing contract; stale summary rows are harmless and self-correct on re-index with a different `input_hash`. For directory removes the (kind, key_hash) lookup only purges the row for the directory path itself — rows for files *under* the removed directory stay until those files are re-removed individually (acceptable: the paths no longer exist on disk, so no future lookup hits them). Pinned by `TestRemoveFile_PurgesSummaryCache` (engine/indexer_test.go).
- **Cross-account isolation** — `key_hash` folds in `workspace_id`, so two accounts with the same abs_path land in different rows. A GC triggered on one account cannot touch the other's cache. Pinned by the cross-workspace assertion inside `TestRemoveFile_PurgesSummaryCache`.
- **:memory: SQLite requires SetMaxOpenConns(1)** — an in-memory connection pool gives each connection a fresh un-migrated DB, so tests that touch `llm_cache` from concurrent goroutines would fail with `no such table: llm_cache`. `store.Open` pins pool size to 1 when `path == ":memory:"`; production always opens a file path and is unaffected.

**JS API** (registered in `core/skillmeta.go` under `File`):
```js
File.summary("src/main.go", {
  model: "sonnet",          // optional, defaults to [llm].model
  force_refresh: false      // optional, bypass cache lookup
}) → {
  summary, model,
  cached: boolean,          // true when served from llm_cache
  usage: {input_tokens, output_tokens, model},
  content_hash              // hex short-hash of the source content
}
```

**Out of scope** (deferred to future plans): streaming summaries, incremental / chunked summaries for > 150k-token files, cross-workspace summary sharing (`Share.read` does the file, the consumer runs their own `File.summary`), summary eviction by age (only the prompt-hash and file-change paths invalidate today).

## Permission System

Destructive operations (Shell.exec, Git.push, etc.) require user approval in `supervised` autonomy mode.
Chat channels that implement `channel.Confirmer` (currently Telegram) show an inline keyboard for approve/deny.
Config: `[permissions]` section in `config.toml` — `require_approval` (operation list) + `timeout_seconds`.
Callback responses route through channel-internal `sync.Map` (not `eventCh`) to prevent dispatchLoop deadlock.

## Config Internals

Account TOML config lives at `~/.kittypaw/accounts/<accountID>/config.toml`; `core.ConfigPath()` is a legacy wrapper for `accounts/default/config.toml`. Server-wide settings (bind, master API key, accounts) go in `~/.kittypaw/server.toml`, and local Web UI credentials live in `~/.kittypaw/auth.json`. See `core/config.go:TopLevelServerConfig` and `core/local_auth.go`.

## Live Workspace Indexing

Workspace files are indexed into FTS5 incrementally via an fsnotify pipeline: `engine.Watcher` (recursive Add, excludedDirs, editor-temp-file filter, drains both `Events` and `Errors`) → `engine.Debouncer` (500 ms coalesce, 2 s cap, fake-clock-driven tests) → `engine.LiveIndexer.IndexFile`/`RemoveFile` on the existing `FTS5Indexer`. One `LiveIndexer` per account, constructed in `server.buildAccountSession` and stored on `AccountDeps.LiveIndexer`. **Startup order is watch-before-bulk-walk**: the startup goroutine calls `live.Start()` + `AddWorkspace` for every registered workspace *before* firing the bulk `Indexer.Index` walk — a filesystem change during the walk would otherwise land after the walker passed and before fsnotify was listening, leaving FTS permanently out-of-sync. `IndexFile` is idempotent so overlap between the initial scan and live events is safe. `handleWorkspacesCreate` reuses the same order (watch first, walk second); `handleWorkspacesDelete` calls `RemoveWorkspace` *before* `DeleteWorkspace` so no stray event lands in `workspace_files` after truncation.

**Symlink defense in depth**: `FTS5Indexer.IndexFile` runs `os.Lstat` before `os.Stat` and skips symlink entries silently — an account cannot plant a symlink inside its workspace that points at `/etc/passwd` or another account's BaseDir and have it auto-indexed. `store.SeedWorkspacesFromConfig` and API-driven workspace create both canonicalise paths via `filepath.EvalSymlinks` so prefix matching against fsnotify-emitted (symlink-resolved) paths stays consistent on macOS.

Opt-out via `[workspace] live_index = false` — `DefaultConfig` has `LiveIndex: true` (pinned by `TestWorkspaceConfig_DefaultsOn`), and when `LiveIndex=false` the field stays `nil` so v1 behavior (manual `File.reindex`) is preserved (pinned by `TestBuildAccountSession_LiveIndexDisabled`).

**Shutdown order is load-bearing**: `AccountDeps.Close` tears down **LiveIndexer before Store**, and `LiveIndexer.Close` itself runs `ctx.cancel → watcher.Close → consumer.Wait → debouncer.Close`. `Debouncer.Close` waits on an in-flight `sync.WaitGroup` covering fire callbacks currently inside `IndexFile`; without that wait the store would close while an `IndexFile` transaction was still open and log `sql: database is closed`. `LiveIndexer.Start` is serialized with `Close` under `l.mu` so an account torn down before its startup goroutine finishes can't race `watcher.Start` against `watcher.Close`. Goroutine leaks are guarded by `TestAccountDeps_Close_NoGoroutineLeak` (3× create/close cycle, ±3 goroutine slack); `go test -race ./engine ./server` is required green. Integration coverage lives under `//go:build integration` in `engine/live_indexer_integration_test.go` — macOS tempdir symlink (`/var/` → `/private/var/`) must be resolved via `filepath.EvalSymlinks` before `AddWorkspace`, otherwise kqueue emits `/private/var/...` paths that don't match the registered workspace root.

**Directory deletes cascade via prefix match**: `FTS5Indexer.RemoveFile` delegates to `store.DeleteWorkspaceFilesByPrefix`, which deletes the exact `abs_path` row *and* every row under it as a subtree (BINARY range `p+"/"` ≤ x < `p+"0"` in a single tx, FTS kept in sync). At fsnotify-Remove time the caller cannot stat the vanished path, so file-vs-dir is resolved server-side — exact match covers the file case, the range covers the dir case, LIKE-metacharacter paths are safe because parameters are bound literally. Trailing slashes on the prefix are normalized. An empty prefix is a no-op (callers wanting a full wipe use `DeleteWorkspaceIndex`).

**Subtree-unwatched visibility**: `Watcher.partialAdds` (atomic int64) counts non-root `fs.Add` / walk failures across `initial_walk` / `initial_subdir` / `runtime_create` phases; each increment emits `slog.Warn` with a `phase` key, and the count is exposed via `Watcher.PartialAddFailures()` + `LiveIndexer.PartialFailures()` (both safe before `Start` / after `Close`). Root Add failures remain terminal errors returned to the caller so the workspace can still enter lazy mode. The counter is cumulative (no reset) and detail-free — detailed path/error forensics stay in the Warn logs.

**Overflow auto-recovery**: `fsnotify.ErrEventOverflow` (Linux `IN_Q_OVERFLOW`, Windows 버퍼 오버런) 감지 시 — 커널 큐가 넘쳐 어떤 watch가 영향 받았는지 알 수 없으므로 — 해당 `Watcher` 가 소유한 모든 workspace 를 `500ms` debounce + `30s` backoff 로 자동 `Reindex`. 전체 walk + upsert + `DeleteStaleWorkspaceFiles` 로 blackout 동안의 create/delete 양방향이 수렴된다. 급격한 오버플로 버스트는 debounce 로 1회에 coalesce 되고, 지속적으로 오버플로하는 커널은 backoff 로 reindex 루프에 빠지지 않는다. `Watcher.OverflowCount()` / `LiveIndexer.RecoveryCount()` atomic API 로 관측 (둘 다 프로세스 시작 이후 누적, `Start` 전·`Close` 후 안전). `LiveIndexer.Close` 는 `ctx.cancel → watcher.Close → consume.Wait → debouncer.Close → overflow.Close` 순서로 진행해 in-flight `Reindex` 가 Close 보다 오래 살지 않는다. `TestLiveIndexer_Close_DuringRecovery_CtxCancelled` / `TestLiveIndexer_Close_NoGoroutineLeak_WithOverflow` 로 고정.

## Onboarding → Chat Auto-Entry

After `kittypaw setup` completes, the CLI drops the user straight into the `kittypaw chat` REPL when four conditions all hold (`cli/cmd_setup.go:autoChatEligible`): stdin is a TTY, stdout is a TTY, no `--provider` flag was passed (that path is non-interactive/CI), and `--no-chat` was not passed. Any one of those false → setup exits normally, preserving CI/scripted paths. The prompt wording (`setupPromptAutoChat` etc.) is golden-string tested — an incidental rewording must be a deliberate test update. Setup also calls `maybeReloadServer` before printing the completion box: if a server is running it POSTs `/api/v1/reload` so the subsequent chat REPL connects to a server that already sees the new channels; server-off prints a hint and returns (never fatal). `maybeReloadServer` returns a 3-state `reloadOutcome` (`ServerOff` / `Reloaded` / `Failed`); if the running server **rejects** Reload, `runSetup` prints `setupMsgAutoChatBlocked` and skips auto-entry — attaching chat to a server still holding the previous config would silently run with stale LLM keys or channel tokens. `ServerOff` and `Reloaded` both allow auto-entry (a fresh server reads the new config on spawn). The CLI deliberately does NOT write `user_context.onboarding_completed` to the DB — `server.isOnboardingCompleted` falls back to `cfg.LLM.APIKey != ""` and that fallback is load-bearing (pinned by `TestIsOnboardingCompleted_FallbackToLLMKey`).

**Load-bearing sync contract in `handleReload`**: the handler calls `spawner.Reconcile` synchronously under `accountMu` and returns only after it completes. `cli/cmd_setup.maybeReloadServer` → `runChat` depends on this — if Reconcile ran async, chat would connect before the new channel set was wired up. Pinned by `TestHandleReload_WaitsForReconcile` (barrier-blocking stub) and `TestAutoEntryNoRace` (`-race -count 50` happens-before loop). Converting Reconcile to a goroutine requires updating both the CLI wiring AND those tests.

**Validation contract in `handleReload`**: the proposed cfg is checked with `core.ValidateAccountChannels` (bot_token / Kakao URL dedup against live peers) and `core.ValidateTeamSpaceAccounts` (no channels on `is_shared=true`) **before** any state mutation — symmetric with `StartChannels` (boot) and `AddAccount` (hot-add). A rejected reload returns `409 Conflict` with an `slog.Error` (`reason=channel_duplicate` or `reason=team_space_account_with_channels`); `s.config` and the spawner are untouched so the server keeps running on the last-good config. The CLI surface `maybeReloadServer` already maps a Reload failure to `setupMsgAutoChatBlocked`, so a bad config.toml edit is caught and reported through the existing path. The entire validate→swap→reconcile sequence runs under `accountMu` — releasing the lock mid-sequence would let a concurrent `AddAccount` validate against the stale default-account channel list and spawn a bot that duplicates the token this reload is about to introduce. Pinned by `TestHandleReload_DuplicateTelegramToken_Rejects`, the team-space-with-channels rejection coverage, `TestHandleReload_SerializesWithAddAccount` (TOCTOU guard), and the `TestHandleReload_ValidConfig_SwapsAndReconciles` happy-path baseline.

## Development

```bash
make build          # Build binary
make test           # All tests (verbose, no cache)
make test-unit      # Short tests only
make lint           # golangci-lint (errcheck, staticcheck, revive, misspell, ...)
make fmt            # gofmt + goimports
```

### Commit Conventions

Conventional Commits enforced by [lefthook](https://github.com/evilmartians/lefthook):

```
type(scope): description

types: feat, fix, refactor, perf, docs, chore, test, ci, build, merge
```

Pre-commit hooks run `gofmt` and `golangci-lint` automatically. Install with:

```bash
lefthook install
```

### CI

The imported app-local workflow files under `apps/kittypaw/.github/` are not
active from the monorepo root. Active monorepo workflows live under
`.github/workflows/`; currently the root owns Pages and `kittypaw/v*` release
automation. Use root `make smoke-local` plus app-local `make test` / `make lint`
for local verification.

## Release

Version is injected via ldflags (`-X main.version`). `kittypaw --version` prints it.

새버전 릴리즈는 반드시 사용자의 동의를 얻어서 진행한다. Do not create or
push release tags, publish GitHub releases, or otherwise trigger release
automation unless the user has explicitly approved that release.

Binary release and stable auto-update promotion are two separate approvals:

- `kittypaw/vX.Y.Z` binary releases require an explicit user instruction before
  tagging or pushing.
- `stable.json` updates require a separate explicit user instruction after the
  released binary has been downloaded and tested locally. Treat this as the
  "allow installed servers to auto-download" switch, not as part of ordinary
  release automation.
- Do not auto-update `stable.json` from CI or from the release workflow.
- The default installer (`curl .../install-kittypaw.sh | sh`) follows
  `apps/kittypaw/stable.json`, not the newest GitHub release. Test candidate
  releases with `VERSION=X.Y.Z curl .../install-kittypaw.sh | sh`.
- Before creating a new binary release, check the current `stable.json`. If it
  still points at an older version and the previous binary release was never
  promoted to stable, warn the user once before proceeding. This may be
  intentional, but it can also indicate repeated binary releases without stable
  promotion.

## Testing

```bash
make test           # All tests
make test-unit      # Unit tests only (fast)
go test ./store/... # Single package
```

### Testing Isolation (KITTYPAW_CONFIG_DIR) — load-bearing

Smoke / eval / 실 LLM endpoint 검증을 돌릴 때 **반드시** `KITTYPAW_CONFIG_DIR=/tmp/kittypaw-<purpose>`를
설정해 사용자의 `~/.kittypaw/` (live daemon, 실 계정 시크릿, 채널 토큰 보유)와
격리한다. 이 디렉토리는 `accounts/`, `cache/`, `daemon.pid` 등의 루트가 된다 — `core/config.go:482`이
값을 verbatim base directory로 사용 (no `.kittypaw/` join, no `os.UserHomeDir` lookup).

```bash
export KITTYPAW_CONFIG_DIR=/tmp/kittypaw-smoke
mkdir -p "$KITTYPAW_CONFIG_DIR/accounts/default"
cat > "$KITTYPAW_CONFIG_DIR/accounts/default/config.toml" <<'EOF'
[llm]
provider = "cerebras"   # or anthropic / openai / ollama / groq / ...
model = "qwen-3-235b-a22b-instruct-2507"
max_tokens = 1024
EOF
touch "$KITTYPAW_CONFIG_DIR/server.toml"

./bin/kittypaw chat "메시지" --account default
```

사용자의 live daemon(`~/.kittypaw/daemon.pid`)은 영향 없음 — 격리된 실행은
`~/.kittypaw/` 아래 어떤 흔적도 남기지 않는다. eval framework
(`--features llm-eval`)도 동일 패턴 — `KITTYPAW_CONFIG_DIR`을 sandbox 디렉토리로
설정하고 끝나면 제거한다. 격리 없이 실 endpoint를 호출하면 daemon에 저장된
대화 기록·secrets에 오염이 들어가고 채널 메시지가 실제로 발송될 수 있다.
