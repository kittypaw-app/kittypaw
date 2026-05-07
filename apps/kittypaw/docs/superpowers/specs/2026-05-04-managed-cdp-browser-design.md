# Managed CDP Browser Control Design

## Status

Approved direction: built-in browser control with managed Chrome as the primary
and MVP path. Attach-only is explicitly rejected for product UX. External MCP
browser servers remain out of scope for this feature.

## Goal

Give KittyPaw runners a built-in `Browser` tool that can launch and control a
local Chrome/Chromium instance through the Chrome DevTools Protocol (CDP).
The user should not need to start Chrome with debugging flags by hand.

The first release should support common browser automation tasks:

- open a managed browser
- navigate to pages
- inspect page title, URL, text, and actionable elements
- click and type into page elements
- run bounded JavaScript evaluation
- capture screenshots to account-local files
- list, activate, and close tabs

## Non-Goals

- No attach-only MVP.
- No raw `Cdp.send(method, params)` public tool.
- No cross-account browser/session sharing.
- No hosted browser service.
- No persistent background web crawler.
- No download/file-upload automation in the first release.
- No attempt to bypass bot protection, login challenges, CAPTCHAs, or site
  policies.

## User Experience

The assistant can call:

```js
const tab = Browser.open("https://example.com");
const page = Browser.snapshot();
Browser.click(page.elements[0].ref);
Browser.type("#search", "kittypaw");
const shot = Browser.screenshot();
return `Captured ${shot.path}`;
```

If Chrome is missing or cannot be launched, `Browser.status()` returns a
diagnostic object with the attempted executable paths and the last startup
error. Other browser methods return structured errors instead of panicking.

## Configuration

Add a new account-local config section:

```toml
[browser]
enabled = true
headless = false
chrome_path = ""
allowed_hosts = []
timeout_seconds = 15
```

Field meanings:

- `enabled`: when false, `Browser.*` returns `browser disabled`.
- `headless`: launch managed Chrome headless when true.
- `chrome_path`: optional explicit executable path. Empty means auto-detect.
- `allowed_hosts`: optional navigation allowlist. Empty uses the safe default:
  public `http` and `https` URLs only, with private/internal hosts blocked.
- `timeout_seconds`: per-browser-operation default timeout.

No `debug_url` is part of the user-facing MVP. A test-only or internal attach
hook may exist behind Go interfaces, but it must not become the default UX.

## Architecture

Add a new local package, tentatively `browser/`, that owns CDP details:

- `Controller`: high-level account-scoped browser API used by the engine.
- `Launcher`: finds and starts Chrome/Chromium in managed mode.
- `Client`: minimal CDP JSON-RPC client over `nhooyr.io/websocket`.
- `TargetSession`: active page/tab session using CDP flat session IDs.
- `Snapshotter`: converts DOM/page state into compact text and element refs.

Wire it into the existing app:

- `core.Config` gains `Browser BrowserConfig`.
- `core.SkillRegistry` gains the `Browser` global and method signatures.
- `sandbox` exposes the global automatically from `SkillRegistry`.
- `engine.Session` gains `BrowserController`.
- `engine.resolveSkillCall` dispatches `Browser.*`.
- `server.OpenAccountDeps` constructs one controller per account.
- `AccountDeps.Close` closes the controller before the store closes.

## Browser Lifecycle

Managed launch uses an account-local user data:

```text
<account BaseDir>/data/browser/user-data/
<account BaseDir>/data/browser/screenshots/
```

The launcher starts Chrome with:

- `--remote-debugging-port=0`
- `--remote-debugging-address=127.0.0.1`
- `--user-data-dir=<account user-data dir>`
- `--no-first-run`
- `--no-default-browser-check`
- `--headless=new` when configured

After launch, the controller reads Chrome's `DevToolsActivePort` file from the
user-data directory, calls `/json/version`, and connects to the browser
`webSocketDebuggerUrl`.

The controller creates tabs with `Target.createTarget`, attaches with
`Target.attachToTarget` using `flatten=true`, activates tabs with
`Target.activateTarget`, and sends page-scoped commands with the returned
`sessionId`.

## Browser Skill API

### `Browser.status()`

Returns:

```json
{
  "enabled": true,
  "running": true,
  "managed": true,
  "chrome_path": "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  "candidate_paths": ["..."],
  "browser": "Chrome/...",
  "active_target_id": "...",
  "last_error": ""
}
```

### `Browser.open(url?)`

Starts managed Chrome if needed, creates a tab, optionally navigates, and makes
that tab active. Returns `{target_id, url, title}`.

### `Browser.tabs()`

Returns page targets visible to the controller:

```json
{"tabs":[{"target_id":"...","url":"...","title":"...","active":true}]}
```

### `Browser.use(targetId)`

Sets the active target and brings it to front.

### `Browser.navigate(url)`

Navigates the active tab after URL validation. Returns CDP navigation result
plus normalized `{url}`.

### `Browser.snapshot(options?)`

Returns a compact page view:

```json
{
  "target_id": "...",
  "url": "https://example.com",
  "title": "Example",
  "text": "visible text capped to 12000 chars",
  "elements": [
    {"ref":"e1","role":"link","text":"Docs","selector":"a:nth-of-type(1)"}
  ]
}
```

`elements` includes clickable links/buttons and text inputs/selects/textareas.
The result is capped so it can safely enter the next LLM turn.

### `Browser.click(refOrSelector)`

Resolves an element ref from the most recent snapshot or accepts a CSS selector.
It computes the element center with `Runtime.evaluate`, then sends
`Input.dispatchMouseEvent` `mousePressed` and `mouseReleased`.

### `Browser.type(refOrSelector, text)`

Focuses the element, clears it if appropriate, and inserts text. The text length
is capped.

### `Browser.evaluate(js)`

Runs bounded JavaScript in the active page via `Runtime.evaluate` with
`returnByValue=true`. The result is JSON-capped. This method is powerful and
must be approval-gated by default in supervised mode.

### `Browser.screenshot(options?)`

Calls `Page.captureScreenshot`, decodes the base64 PNG/JPEG/WebP data, writes it
under `<BaseDir>/data/browser/screenshots/`, and returns:

```json
{"path":".../shot-20260504-120102.png","mime":"image/png","bytes":12345}
```

The tool does not return image bytes directly.

### `Browser.close(targetId?)`

Closes the given target or the active tab with `Target.closeTarget`.

## Security Model

Browser control is treated as a side-effecting tool surface.

- `Browser.navigate` and `Browser.open` allow only `http` and `https`.
- Private/internal hosts are blocked unless explicitly listed in
  `[browser].allowed_hosts`.
- CDP discovery and WebSocket URLs must resolve to loopback for managed mode.
- `Browser.open`, `Browser.navigate`, `Browser.click`, `Browser.type`,
  `Browser.evaluate`, and `Browser.close` are added to the default approval
  list for supervised mode.
- Snapshot and evaluate outputs are truncated before entering LLM context.
- Screenshots are saved under the account base directory and never written
  outside it.
- Each account gets its own Chrome user-data directory, avoiding cookie/session
  leakage across accounts.
- The controller must recover gracefully from Chrome exits and stale sessions.

## Error Handling

Every public method returns JSON with either a result or an `error` field.
Common errors:

- `browser disabled`
- `chrome executable not found`
- `browser launch timed out`
- `browser connection failed`
- `no active tab`
- `target not found`
- `navigation blocked: host ...`
- `element not found`
- `cdp error: ...`

The engine should not panic on CDP failures. Account panic isolation remains a
backstop, not the normal error path.

## Testing Strategy

Unit tests:

- config parsing/defaults for `[browser]`
- Chrome executable detection with injected filesystem/env
- launch command construction
- `DevToolsActivePort` parsing
- CDP JSON-RPC request/response matching
- concurrent CDP calls return to the right waiter
- event messages are ignored or routed without breaking waiters
- URL validation blocks private/internal hosts by default
- snapshot truncation and stable element refs
- executor dispatch for `Browser.*`
- supervised permission gating covers side-effecting Browser methods

Integration tests:

- gated by build tag or environment, skipped when Chrome is unavailable
- managed launch creates a real tab
- navigate to an `httptest.Server`
- snapshot sees expected text and controls
- click/type mutate a test page
- screenshot writes a non-empty file under the account directory
- close tears down tabs and account close terminates the managed Chrome process

Required verification for implementation:

- `go test ./core ./browser ./engine ./server`
- browser integration test command when Chrome exists locally
- `gofmt -w` on touched Go files

## References

- Chrome DevTools Protocol overview:
  https://chromedevtools.github.io/devtools-protocol/
- Target domain:
  https://chromedevtools.github.io/devtools-protocol/tot/Target/
- Page domain:
  https://chromedevtools.github.io/devtools-protocol/tot/Page/
- Runtime domain:
  https://chromedevtools.github.io/devtools-protocol/tot/Runtime/
- Input domain:
  https://chromedevtools.github.io/devtools-protocol/tot/Input/
