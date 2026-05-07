# Managed CDP Browser Control Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add built-in managed Chrome CDP browser control to the local KittyPaw binary through a new `Browser` skill global.

**Architecture:** Add a focused `browser` package that owns Chrome launch, CDP transport, target/tab state, snapshots, actions, screenshots, and JSON method dispatch. Wire one controller per account through `server.AccountDeps` into `engine.Session`, keeping engine integration thin and testable without real Chrome.

**Tech Stack:** Go 1.25, `nhooyr.io/websocket`, Chrome DevTools Protocol JSON-RPC, existing `core.SkillRegistry`, existing sandbox skill stubs, existing supervised permission gate.

---

## File Structure

- Create `browser/types.go`: public config-independent result structs, errors, constants, `ControllerOptions`, and small JSON helpers.
- Create `browser/url.go`: URL validation for browser navigation using the same private-host rule as HTTP tools.
- Create `browser/launcher.go`: Chrome executable detection, managed launch command construction, `DevToolsActivePort` parsing, and process shutdown.
- Create `browser/cdp.go`: minimal CDP JSON-RPC client over an injected WebSocket connection.
- Create `browser/controller.go`: high-level managed browser lifecycle, tab state, CDP domain operations, and `Execute(ctx, core.SkillCall)`.
- Create `browser/snapshot.go`: page snapshot JavaScript, element ref cache, click/type helpers, and result truncation.
- Create `browser/screenshot.go`: screenshot capture, base64 decode, account-local file writes.
- Create tests beside each new package file.
- Modify `core/config.go`: add `BrowserConfig`, add field to `Config`, add defaults.
- Modify `core/config_test.go`: config/default tests for `[browser]`.
- Modify `core/skillmeta.go`: add `Browser` tool signatures.
- Modify `engine/session.go`: add `BrowserController` interface field.
- Modify `engine/executor.go`: dispatch `Browser.*`.
- Modify `engine/executor_test.go`: permission-gate and dispatch tests.
- Modify `server/account_deps.go`: construct and close the account browser controller.
- Modify `server/live_indexer_wiring_test.go` or add `server/browser_wiring_test.go`: verify controller wiring and close order with nil-safe account deps.
- Modify `docs/superpowers/specs/2026-05-04-managed-cdp-browser-design.md`: keep security list aligned with implementation.

## Task 1: Config, Skill Registry, And Permission Surface

**Files:**
- Modify: `core/config.go`
- Modify: `core/config_test.go`
- Modify: `core/skillmeta.go`
- Modify: `engine/teach_test.go`
- Modify: `engine/executor_test.go`

- [ ] **Step 1: Write failing config and permission tests**

Add to `core/config_test.go`:

```go
func TestBrowserConfigDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Browser.Enabled {
		t.Fatal("browser should be enabled by default")
	}
	if cfg.Browser.Headless {
		t.Fatal("browser should default to visible managed Chrome")
	}
	if cfg.Browser.ChromePath != "" {
		t.Fatalf("ChromePath = %q, want empty auto-detect", cfg.Browser.ChromePath)
	}
	if cfg.Browser.TimeoutSeconds != 15 {
		t.Fatalf("TimeoutSeconds = %d, want 15", cfg.Browser.TimeoutSeconds)
	}
	if cfg.Browser.AllowedHosts != nil {
		t.Fatalf("AllowedHosts = %#v, want nil default", cfg.Browser.AllowedHosts)
	}
}

func TestBrowserConfigParsing(t *testing.T) {
	tomlContent := `
[browser]
enabled = false
headless = true
chrome_path = "/opt/chrome"
allowed_hosts = ["localhost", "127.0.0.1"]
timeout_seconds = 9
`
	var cfg Config
	if _, err := toml.Decode(tomlContent, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.Browser.Enabled {
		t.Fatal("enabled should parse false")
	}
	if !cfg.Browser.Headless {
		t.Fatal("headless should parse true")
	}
	if cfg.Browser.ChromePath != "/opt/chrome" {
		t.Fatalf("ChromePath = %q", cfg.Browser.ChromePath)
	}
	if cfg.Browser.TimeoutSeconds != 9 {
		t.Fatalf("TimeoutSeconds = %d", cfg.Browser.TimeoutSeconds)
	}
	if got := cfg.Browser.AllowedHosts; len(got) != 2 || got[0] != "localhost" || got[1] != "127.0.0.1" {
		t.Fatalf("AllowedHosts = %#v", got)
	}
}
```

Extend `TestPermissionPolicyDefaults` in `core/config_test.go`:

```go
for _, want := range []string{
	"Browser.open",
	"Browser.navigate",
	"Browser.click",
	"Browser.type",
	"Browser.evaluate",
	"Browser.close",
} {
	if !slices.Contains(DefaultRequireApproval, want) {
		t.Fatalf("DefaultRequireApproval missing %s: %v", want, DefaultRequireApproval)
	}
}
```

Add `slices` to the test imports.

Extend `TestDetectPermissions` in `engine/teach_test.go`:

```go
{
	name: "browser global",
	code: `Browser.open("https://example.com"); Browser.click("e1");`,
	want: []string{"Browser"},
},
```

Extend the `TestNeedsPermission` table in `engine/executor_test.go`:

```go
{"supervised_browser_open", "Browser", "open", core.AutonomySupervised, nil, true},
{"supervised_browser_snapshot", "Browser", "snapshot", core.AutonomySupervised, nil, false},
{"supervised_browser_evaluate", "Browser", "evaluate", core.AutonomySupervised, nil, true},
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./core ./engine -run 'TestBrowserConfig|TestPermissionPolicyDefaults|TestDetectPermissions|TestNeedsPermission' -count=1
```

Expected: failures for missing `Browser` config fields, missing default approvals, and missing `Browser` skill registry entry.

- [ ] **Step 3: Add minimal config and registry implementation**

In `core/config.go`, add:

```go
type BrowserConfig struct {
	Enabled        bool     `toml:"enabled"`
	Headless       bool     `toml:"headless"`
	ChromePath     string   `toml:"chrome_path"`
	AllowedHosts   []string `toml:"allowed_hosts"`
	TimeoutSeconds int      `toml:"timeout_seconds"`
}
```

Add to `Config`:

```go
Browser BrowserConfig `toml:"browser"`
```

Add to `DefaultConfig()`:

```go
Browser: BrowserConfig{
	Enabled:        true,
	TimeoutSeconds: 15,
},
```

Extend `DefaultRequireApproval`:

```go
"Browser.open", "Browser.navigate", "Browser.click", "Browser.type", "Browser.evaluate", "Browser.close",
```

In `core/skillmeta.go`, add:

```go
{Name: "Browser", Methods: []SkillMethodMeta{
	{Name: "status", Signature: "Browser.status() — returns managed Chrome status and diagnostics"},
	{Name: "open", Signature: "Browser.open(url?) — starts managed Chrome if needed, creates an active tab, and optionally navigates"},
	{Name: "tabs", Signature: "Browser.tabs() — lists controlled tabs"},
	{Name: "use", Signature: "Browser.use(targetId) — activates a controlled tab"},
	{Name: "navigate", Signature: "Browser.navigate(url) — navigates the active tab"},
	{Name: "snapshot", Signature: "Browser.snapshot(options?) — returns title, URL, visible text, and actionable element refs"},
	{Name: "click", Signature: "Browser.click(refOrSelector) — clicks an element ref from the latest snapshot or a CSS selector"},
	{Name: "type", Signature: "Browser.type(refOrSelector, text) — focuses an element and types text"},
	{Name: "evaluate", Signature: "Browser.evaluate(js) — runs bounded JavaScript in the active page and returns JSON-capped result"},
	{Name: "screenshot", Signature: "Browser.screenshot(options?) — saves a screenshot under the account data dir and returns {path, mime, bytes}"},
	{Name: "close", Signature: "Browser.close(targetId?) — closes a tab"},
}},
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
go test ./core ./engine -run 'TestBrowserConfig|TestPermissionPolicyDefaults|TestDetectPermissions|TestNeedsPermission' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add core/config.go core/config_test.go core/skillmeta.go engine/teach_test.go engine/executor_test.go docs/superpowers/specs/2026-05-04-managed-cdp-browser-design.md
git commit -m "feat: expose browser tool config"
```

## Task 2: Browser URL Validation And Account Paths

**Files:**
- Create: `browser/types.go`
- Create: `browser/url.go`
- Test: `browser/url_test.go`

- [ ] **Step 1: Write failing URL validation tests**

Create `browser/url_test.go`:

```go
package browser

import "testing"

func TestValidateNavigationURL(t *testing.T) {
	tests := []struct {
		name    string
		rawURL  string
		allowed []string
		wantErr bool
	}{
		{"public_https", "https://example.com/path", nil, false},
		{"public_http", "http://example.com/path", nil, false},
		{"missing_scheme", "example.com", nil, true},
		{"javascript_scheme", "javascript:alert(1)", nil, true},
		{"file_scheme", "file:///etc/passwd", nil, true},
		{"loopback_blocked", "http://127.0.0.1:8080", nil, true},
		{"localhost_blocked", "http://localhost:8080", nil, true},
		{"private_blocked", "http://192.168.1.1", nil, true},
		{"allow_localhost", "http://localhost:8080", []string{"localhost"}, false},
		{"allow_loopback", "http://127.0.0.1:8080", []string{"127.0.0.1"}, false},
		{"allow_wildcard", "http://10.0.0.2", []string{"*"}, false},
		{"reject_unlisted_when_allowlist_present", "https://example.com", []string{"kittypaw.local"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateNavigationURL(tt.rawURL, tt.allowed)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && got == "" {
				t.Fatal("normalized URL is empty")
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./browser -run TestValidateNavigationURL -count=1
```

Expected: build failure because package `browser` and `validateNavigationURL` do not exist.

- [ ] **Step 3: Add minimal types and URL validation**

Create `browser/types.go`:

```go
package browser

import (
	"encoding/json"
	"time"

	"github.com/jinto/kittypaw/core"
)

const (
	defaultTextLimit      = 12000
	defaultElementsLimit  = 80
	defaultEvaluateLimit  = 8000
	defaultTypeTextLimit  = 4000
	defaultStartupTimeout = 15 * time.Second
)

type ControllerOptions struct {
	Config  core.BrowserConfig
	BaseDir string
}

func jsonResult(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func errorResult(msg string) (string, error) {
	return jsonResult(map[string]any{"error": msg})
}
```

Create `browser/url.go`:

```go
package browser

import (
	"fmt"
	"net/url"

	"github.com/jinto/kittypaw/core"
)

func validateNavigationURL(rawURL string, allowedHosts []string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("unsupported URL scheme %q; only http and https are allowed", parsed.Scheme)
	}
	host := parsed.Hostname()
	if host == "" {
		return "", fmt.Errorf("URL host is required")
	}
	if len(allowedHosts) > 0 {
		for _, h := range allowedHosts {
			if h == "*" || h == host {
				return parsed.String(), nil
			}
		}
		return "", fmt.Errorf("host %q not in browser allowed hosts", host)
	}
	if core.IsPrivateIP(host) {
		return "", fmt.Errorf("navigation to private/internal address %q is blocked", host)
	}
	return parsed.String(), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run:

```bash
go test ./browser -run TestValidateNavigationURL -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add browser/types.go browser/url.go browser/url_test.go
git commit -m "feat: add browser URL validation"
```

## Task 3: Managed Chrome Launcher

**Files:**
- Create: `browser/launcher.go`
- Test: `browser/launcher_test.go`

- [ ] **Step 1: Write failing launcher tests**

Create `browser/launcher_test.go`:

```go
package browser

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseDevToolsActivePort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "DevToolsActivePort")
	if err := os.WriteFile(path, []byte("49231\n/devtools/browser/abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	port, browserPath, err := parseDevToolsActivePort(path)
	if err != nil {
		t.Fatalf("parseDevToolsActivePort: %v", err)
	}
	if port != "49231" || browserPath != "/devtools/browser/abc" {
		t.Fatalf("port/path = %q/%q", port, browserPath)
	}
}

func TestBuildChromeArgs(t *testing.T) {
	args := buildChromeArgs("/tmp/chrome-user-data", true)
	joined := strings.Join(args, "\n")
	for _, want := range []string{
		"--remote-debugging-port=0",
		"--remote-debugging-address=127.0.0.1",
		"--user-data-dir=/tmp/chrome-user-data",
		"--no-first-run",
		"--no-default-browser-check",
		"--headless=new",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args missing %s: %v", want, args)
		}
	}
}

func TestDefaultChromeCandidates(t *testing.T) {
	candidates := defaultChromeCandidates()
	if len(candidates) == 0 {
		t.Fatal("expected candidates")
	}
	switch runtime.GOOS {
	case "darwin":
		if candidates[0] != "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" {
			t.Fatalf("first darwin candidate = %q", candidates[0])
		}
	case "linux":
		if candidates[0] == "" {
			t.Fatal("first linux candidate empty")
		}
	case "windows":
		if candidates[0] == "" {
			t.Fatal("first windows candidate empty")
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./browser -run 'TestParseDevToolsActivePort|TestBuildChromeArgs|TestDefaultChromeCandidates' -count=1
```

Expected: build failure for missing launcher helpers.

- [ ] **Step 3: Add launcher helpers and process wrapper**

Create `browser/launcher.go` with:

```go
package browser

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type chromeProcess struct {
	cmd *exec.Cmd
}

func (p *chromeProcess) Close() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	_ = p.cmd.Process.Signal(os.Interrupt)
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		_ = p.cmd.Process.Kill()
		return <-done
	}
}

func defaultChromeCandidates() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
	case "windows":
		return []string{
			filepath.Join(os.Getenv("PROGRAMFILES"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("PROGRAMFILES(X86)"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Google", "Chrome", "Application", "chrome.exe"),
		}
	default:
		return []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"}
	}
}

func findChrome(explicit string) (string, []string, error) {
	candidates := defaultChromeCandidates()
	if explicit != "" {
		candidates = append([]string{explicit}, candidates...)
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if filepath.IsAbs(c) {
			if info, err := os.Stat(c); err == nil && !info.IsDir() {
				return c, candidates, nil
			}
			continue
		}
		if path, err := exec.LookPath(c); err == nil {
			return path, candidates, nil
		}
	}
	return "", candidates, fmt.Errorf("chrome executable not found")
}

func buildChromeArgs(userDataDir string, headless bool) []string {
	args := []string{
		"--remote-debugging-port=0",
		"--remote-debugging-address=127.0.0.1",
		"--user-data-dir=" + userDataDir,
		"--no-first-run",
		"--no-default-browser-check",
		"about:blank",
	}
	if headless {
		args = append(args[:len(args)-1], "--headless=new", "about:blank")
	}
	return args
}

func parseDevToolsActivePort(path string) (string, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return "", "", fmt.Errorf("DevToolsActivePort missing port")
	}
	port := strings.TrimSpace(sc.Text())
	if !sc.Scan() {
		return "", "", fmt.Errorf("DevToolsActivePort missing browser path")
	}
	browserPath := strings.TrimSpace(sc.Text())
	if err := sc.Err(); err != nil {
		return "", "", err
	}
	if port == "" || browserPath == "" {
		return "", "", fmt.Errorf("DevToolsActivePort incomplete")
	}
	return port, browserPath, nil
}

func waitForDevToolsActivePort(ctx context.Context, path string) (string, string, error) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		port, browserPath, err := parseDevToolsActivePort(path)
		if err == nil {
			return port, browserPath, nil
		}
		select {
		case <-ctx.Done():
			return "", "", fmt.Errorf("browser launch timed out")
		case <-ticker.C:
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
go test ./browser -run 'TestParseDevToolsActivePort|TestBuildChromeArgs|TestDefaultChromeCandidates' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add browser/launcher.go browser/launcher_test.go
git commit -m "feat: add managed chrome launcher helpers"
```

## Task 4: CDP JSON-RPC Client

**Files:**
- Create: `browser/cdp.go`
- Test: `browser/cdp_test.go`

- [ ] **Step 1: Write failing CDP client tests**

Create `browser/cdp_test.go`:

```go
package browser

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type fakeCDPConn struct {
	writes chan []byte
	reads  chan []byte
}

func newFakeCDPConn() *fakeCDPConn {
	return &fakeCDPConn{writes: make(chan []byte, 8), reads: make(chan []byte, 8)}
}

func (f *fakeCDPConn) Write(ctx context.Context, b []byte) error {
	select {
	case f.writes <- append([]byte(nil), b...):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *fakeCDPConn) Read(ctx context.Context) ([]byte, error) {
	select {
	case b := <-f.reads:
		return b, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (f *fakeCDPConn) Close() error { return nil }

func TestCDPClientCallMatchesResponseByID(t *testing.T) {
	conn := newFakeCDPConn()
	client := newCDPClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	defer client.Close()

	done := make(chan map[string]any, 1)
	go func() {
		var out map[string]any
		if err := client.Call(ctx, "Runtime.evaluate", map[string]any{"expression": "1+1"}, &out); err != nil {
			t.Errorf("Call: %v", err)
			return
		}
		done <- out
	}()

	var req cdpRequest
	if err := json.Unmarshal(<-conn.writes, &req); err != nil {
		t.Fatalf("request json: %v", err)
	}
	if req.ID == 0 || req.Method != "Runtime.evaluate" {
		t.Fatalf("request = %#v", req)
	}
	conn.reads <- []byte(`{"id":1,"result":{"value":2}}`)

	got := <-done
	if got["value"].(float64) != 2 {
		t.Fatalf("result = %#v", got)
	}
}

func TestCDPClientIgnoresEventsWhileWaiting(t *testing.T) {
	conn := newFakeCDPConn()
	client := newCDPClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	defer client.Close()

	done := make(chan error, 1)
	go func() {
		var out map[string]any
		done <- client.Call(ctx, "Page.navigate", nil, &out)
	}()
	var req cdpRequest
	_ = json.Unmarshal(<-conn.writes, &req)
	conn.reads <- []byte(`{"method":"Page.loadEventFired","params":{}}`)
	conn.reads <- []byte(`{"id":1,"result":{"frameId":"f1"}}`)
	if err := <-done; err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
}

func TestCDPClientReturnsCDPError(t *testing.T) {
	conn := newFakeCDPConn()
	client := newCDPClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	defer client.Close()

	done := make(chan error, 1)
	go func() {
		done <- client.Call(ctx, "Bad.method", nil, nil)
	}()
	<-conn.writes
	conn.reads <- []byte(`{"id":1,"error":{"code":-32601,"message":"method not found"}}`)
	if err := <-done; err == nil || err.Error() != "cdp error -32601: method not found" {
		t.Fatalf("error = %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./browser -run 'TestCDPClient' -count=1
```

Expected: build failure for missing CDP client types.

- [ ] **Step 3: Add CDP client implementation**

Create `browser/cdp.go` with a `cdpConn` interface, `newCDPClient`, a read loop, pending response map, and `Call`:

```go
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
)

type cdpConn interface {
	Write(context.Context, []byte) error
	Read(context.Context) ([]byte, error)
	Close() error
}

type cdpRequest struct {
	ID        int64          `json:"id"`
	Method    string         `json:"method"`
	Params    any            `json:"params,omitempty"`
	SessionID string         `json:"sessionId,omitempty"`
}

type cdpResponse struct {
	ID     int64           `json:"id,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *cdpError       `json:"error,omitempty"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cdpClient struct {
	conn    cdpConn
	nextID  atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan cdpResponse
	closed  bool
}

func newCDPClient(conn cdpConn) *cdpClient {
	c := &cdpClient{conn: conn, pending: make(map[int64]chan cdpResponse)}
	go c.readLoop()
	return c
}

func (c *cdpClient) readLoop() {
	for {
		b, err := c.conn.Read(context.Background())
		if err != nil {
			c.failAll()
			return
		}
		var resp cdpResponse
		if err := json.Unmarshal(b, &resp); err != nil || resp.ID == 0 {
			continue
		}
		c.mu.Lock()
		ch := c.pending[resp.ID]
		delete(c.pending, resp.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- resp
			close(ch)
		}
	}
}

func (c *cdpClient) Call(ctx context.Context, method string, params any, out any) error {
	return c.CallSession(ctx, "", method, params, out)
}

func (c *cdpClient) CallSession(ctx context.Context, sessionID, method string, params any, out any) error {
	id := c.nextID.Add(1)
	ch := make(chan cdpResponse, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return fmt.Errorf("cdp client closed")
	}
	c.pending[id] = ch
	c.mu.Unlock()

	req := cdpRequest{ID: id, Method: method, Params: params, SessionID: sessionID}
	data, err := json.Marshal(req)
	if err != nil {
		c.removePending(id)
		return err
	}
	if err := c.conn.Write(ctx, data); err != nil {
		c.removePending(id)
		return err
	}
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return fmt.Errorf("cdp error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		if out != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, out)
		}
		return nil
	case <-ctx.Done():
		c.removePending(id)
		return ctx.Err()
	}
}

func (c *cdpClient) removePending(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *cdpClient) failAll() {
	c.mu.Lock()
	c.closed = true
	for id, ch := range c.pending {
		delete(c.pending, id)
		close(ch)
	}
	c.mu.Unlock()
}

func (c *cdpClient) Close() error {
	c.failAll()
	return c.conn.Close()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
go test ./browser -run 'TestCDPClient' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add browser/cdp.go browser/cdp_test.go
git commit -m "feat: add cdp json rpc client"
```

## Task 5: Controller Method Dispatch And Tab Operations

**Files:**
- Create: `browser/controller.go`
- Test: `browser/controller_test.go`

- [ ] **Step 1: Write failing controller dispatch tests**

Create `browser/controller_test.go`:

```go
package browser

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
)

type fakeBackend struct {
	calls []string
	tab   tabInfo
}

func (f *fakeBackend) status(context.Context) (StatusResult, error) {
	return StatusResult{Enabled: true, Running: true, Managed: true, ActiveTargetID: f.tab.TargetID}, nil
}
func (f *fakeBackend) open(ctx context.Context, rawURL string) (tabInfo, error) {
	f.calls = append(f.calls, "open:"+rawURL)
	f.tab = tabInfo{TargetID: "target-1", URL: rawURL, Title: "Title", Active: true}
	return f.tab, nil
}
func (f *fakeBackend) tabs(context.Context) ([]tabInfo, error) { return []tabInfo{f.tab}, nil }
func (f *fakeBackend) use(ctx context.Context, targetID string) (tabInfo, error) {
	f.calls = append(f.calls, "use:"+targetID)
	f.tab.TargetID = targetID
	f.tab.Active = true
	return f.tab, nil
}
func (f *fakeBackend) navigate(ctx context.Context, rawURL string) (map[string]any, error) {
	f.calls = append(f.calls, "navigate:"+rawURL)
	f.tab.URL = rawURL
	return map[string]any{"url": rawURL}, nil
}
func (f *fakeBackend) close(ctx context.Context, targetID string) error {
	f.calls = append(f.calls, "close:"+targetID)
	return nil
}

func TestControllerExecuteStatusDisabled(t *testing.T) {
	c := NewController(ControllerOptions{
		Config: core.BrowserConfig{Enabled: false},
		BaseDir: t.TempDir(),
	})
	got, err := c.Execute(context.Background(), core.SkillCall{SkillName: "Browser", Method: "status"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"enabled":false`) {
		t.Fatalf("status = %s", got)
	}
}

func TestControllerExecuteOpenValidatesURL(t *testing.T) {
	fake := &fakeBackend{}
	c := newControllerWithBackend(core.BrowserConfig{Enabled: true}, t.TempDir(), fake)
	got, err := c.Execute(context.Background(), core.SkillCall{
		SkillName: "Browser",
		Method: "open",
		Args: []json.RawMessage{json.RawMessage(`"javascript:alert(1)"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "unsupported URL scheme") {
		t.Fatalf("got %s", got)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("backend should not be called: %v", fake.calls)
	}
}

func TestControllerExecuteOpen(t *testing.T) {
	fake := &fakeBackend{}
	c := newControllerWithBackend(core.BrowserConfig{Enabled: true}, t.TempDir(), fake)
	got, err := c.Execute(context.Background(), core.SkillCall{
		SkillName: "Browser",
		Method: "open",
		Args: []json.RawMessage{json.RawMessage(`"https://example.com"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"target_id":"target-1"`) {
		t.Fatalf("got %s", got)
	}
	if fake.calls[0] != "open:https://example.com" {
		t.Fatalf("calls = %v", fake.calls)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./browser -run 'TestControllerExecute' -count=1
```

Expected: build failure for missing `Controller`, `StatusResult`, and dispatch helpers.

- [ ] **Step 3: Add controller dispatch skeleton**

Create `browser/controller.go` with:

```go
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/jinto/kittypaw/core"
)

type StatusResult struct {
	Enabled        bool     `json:"enabled"`
	Running        bool     `json:"running"`
	Managed        bool     `json:"managed"`
	ChromePath     string   `json:"chrome_path,omitempty"`
	CandidatePaths []string `json:"candidate_paths,omitempty"`
	Browser        string   `json:"browser,omitempty"`
	ActiveTargetID string   `json:"active_target_id,omitempty"`
	LastError      string   `json:"last_error,omitempty"`
}

type tabInfo struct {
	TargetID string `json:"target_id"`
	URL      string `json:"url"`
	Title    string `json:"title"`
	Active   bool   `json:"active"`
}

type backend interface {
	status(context.Context) (StatusResult, error)
	open(context.Context, string) (tabInfo, error)
	tabs(context.Context) ([]tabInfo, error)
	use(context.Context, string) (tabInfo, error)
	navigate(context.Context, string) (map[string]any, error)
	close(context.Context, string) error
}

type Controller struct {
	cfg       core.BrowserConfig
	baseDir   string
	dataDir   string
	backend   backend
	lastError string
	mu        sync.Mutex
}

func NewController(opts ControllerOptions) *Controller {
	c := &Controller{
		cfg:     opts.Config,
		baseDir: opts.BaseDir,
		dataDir: filepath.Join(opts.BaseDir, "data", "browser"),
	}
	c.backend = c
	return c
}

func newControllerWithBackend(cfg core.BrowserConfig, baseDir string, b backend) *Controller {
	return &Controller{cfg: cfg, baseDir: baseDir, dataDir: filepath.Join(baseDir, "data", "browser"), backend: b}
}

func (c *Controller) Execute(ctx context.Context, call core.SkillCall) (string, error) {
	if call.SkillName != "Browser" {
		return errorResult("invalid browser skill")
	}
	if call.Method == "status" {
		return c.executeStatus(ctx)
	}
	if !c.cfg.Enabled {
		return errorResult("browser disabled")
	}
	timeout := time.Duration(c.cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultStartupTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	switch call.Method {
	case "open":
		rawURL, err := optionalStringArg(call.Args, 0)
		if err != nil {
			return errorResult(err.Error())
		}
		if rawURL != "" {
			var validErr error
			rawURL, validErr = validateNavigationURL(rawURL, c.cfg.AllowedHosts)
			if validErr != nil {
				return errorResult(validErr.Error())
			}
		}
		tab, err := c.backend.open(callCtx, rawURL)
		return c.resultOrError(tab, err)
	case "tabs":
		tabs, err := c.backend.tabs(callCtx)
		return c.resultOrError(map[string]any{"tabs": tabs}, err)
	case "use":
		targetID, err := requiredStringArg(call.Args, 0, "targetId argument required")
		if err != nil {
			return errorResult(err.Error())
		}
		tab, err := c.backend.use(callCtx, targetID)
		return c.resultOrError(tab, err)
	case "navigate":
		rawURL, err := requiredStringArg(call.Args, 0, "url argument required")
		if err != nil {
			return errorResult(err.Error())
		}
		rawURL, err = validateNavigationURL(rawURL, c.cfg.AllowedHosts)
		if err != nil {
			return errorResult(err.Error())
		}
		out, err := c.backend.navigate(callCtx, rawURL)
		return c.resultOrError(out, err)
	case "close":
		targetID, err := optionalStringArg(call.Args, 0)
		if err != nil {
			return errorResult(err.Error())
		}
		err = c.backend.close(callCtx, targetID)
		return c.resultOrError(map[string]any{"success": err == nil}, err)
	default:
		return errorResult(fmt.Sprintf("unknown Browser method: %s", call.Method))
	}
}

func (c *Controller) executeStatus(ctx context.Context) (string, error) {
	if !c.cfg.Enabled {
		return jsonResult(StatusResult{Enabled: false, LastError: c.lastError})
	}
	status, err := c.backend.status(ctx)
	if err != nil {
		c.recordError(err)
		status = StatusResult{Enabled: true, LastError: err.Error()}
	}
	return jsonResult(status)
}

func (c *Controller) resultOrError(v any, err error) (string, error) {
	if err != nil {
		c.recordError(err)
		return errorResult(err.Error())
	}
	return jsonResult(v)
}

func (c *Controller) recordError(err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	c.lastError = err.Error()
	c.mu.Unlock()
}

func optionalStringArg(args []json.RawMessage, idx int) (string, error) {
	if len(args) <= idx {
		return "", nil
	}
	var out string
	if err := json.Unmarshal(args[idx], &out); err != nil {
		return "", fmt.Errorf("invalid string argument")
	}
	return out, nil
}

func requiredStringArg(args []json.RawMessage, idx int, msg string) (string, error) {
	out, err := optionalStringArg(args, idx)
	if err != nil {
		return "", err
	}
	if out == "" {
		return "", fmt.Errorf("%s", msg)
	}
	return out, nil
}

func (c *Controller) Close() error { return nil }
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
go test ./browser -run 'TestControllerExecute' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add browser/controller.go browser/controller_test.go
git commit -m "feat: add browser controller dispatch"
```

## Task 6: Real Managed CDP Backend

**Files:**
- Modify: `browser/controller.go`
- Modify: `browser/launcher.go`
- Modify: `browser/cdp.go`
- Test: `browser/controller_backend_test.go`

- [ ] **Step 1: Write failing backend tests with fake CDP**

Create `browser/controller_backend_test.go`:

```go
package browser

import (
	"context"
	"testing"
)

func TestControllerBackendStatusBeforeLaunch(t *testing.T) {
	c := NewController(ControllerOptions{Config: testBrowserConfig(), BaseDir: t.TempDir()})
	status, err := c.status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Enabled || status.Running {
		t.Fatalf("status = %#v", status)
	}
}

func testBrowserConfig() core.BrowserConfig {
	return core.BrowserConfig{Enabled: true, TimeoutSeconds: 1}
}
```

Add `github.com/jinto/kittypaw/core` import. This first test forces `Controller` to implement its own backend methods.

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./browser -run TestControllerBackendStatusBeforeLaunch -count=1
```

Expected: compile failure because `Controller.status` has not been implemented.

- [ ] **Step 3: Add real backend state and status implementation**

Extend `Controller`:

```go
chromePath     string
candidatePaths []string
proc           *chromeProcess
client         *cdpClient
browserVersion string
activeTargetID string
targets        map[string]string
```

Add methods:

```go
func (c *Controller) status(ctx context.Context) (StatusResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return StatusResult{
		Enabled:        c.cfg.Enabled,
		Running:        c.client != nil,
		Managed:        true,
		ChromePath:     c.chromePath,
		CandidatePaths: append([]string(nil), c.candidatePaths...),
		Browser:        c.browserVersion,
		ActiveTargetID: c.activeTargetID,
		LastError:      c.lastError,
	}, nil
}
```

- [ ] **Step 4: Add launch and CDP connect implementation**

Add a WebSocket adapter in `browser/cdp.go`:

```go
type websocketConn struct{ conn *websocket.Conn }

func (w *websocketConn) Write(ctx context.Context, b []byte) error {
	return w.conn.Write(ctx, websocket.MessageText, b)
}
func (w *websocketConn) Read(ctx context.Context) ([]byte, error) {
	_, b, err := w.conn.Read(ctx)
	return b, err
}
func (w *websocketConn) Close() error { return w.conn.Close(websocket.StatusNormalClosure, "") }
```

Add `nhooyr.io/websocket` to imports.

Add `ensureStarted(ctx)` in `browser/controller.go`:

```go
func (c *Controller) ensureStarted(ctx context.Context) error {
	c.mu.Lock()
	if c.client != nil {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	if err := os.MkdirAll(filepath.Join(c.dataDir, "user-data"), 0o700); err != nil {
		return err
	}
	chromePath, candidates, err := findChrome(c.cfg.ChromePath)
	c.mu.Lock()
	c.chromePath = chromePath
	c.candidatePaths = candidates
	c.mu.Unlock()
	if err != nil {
		return err
	}

	userDataDir := filepath.Join(c.dataDir, "user-data")
	cmd := exec.CommandContext(ctx, chromePath, buildChromeArgs(userDataDir, c.cfg.Headless)...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("browser launch failed: %w", err)
	}
	proc := &chromeProcess{cmd: cmd}
	port, browserPath, err := waitForDevToolsActivePort(ctx, filepath.Join(userDataDir, "DevToolsActivePort"))
	if err != nil {
		_ = proc.Close()
		return err
	}
	versionURL := "http://127.0.0.1:" + port + "/json/version"
	var version struct {
		Browser              string `json:"Browser"`
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, versionURL, nil)
	if err != nil {
		_ = proc.Close()
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		_ = proc.Close()
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&version); err != nil {
		_ = proc.Close()
		return err
	}
	if version.WebSocketDebuggerURL == "" || !strings.Contains(version.WebSocketDebuggerURL, "127.0.0.1:"+port+browserPath) {
		_ = proc.Close()
		return fmt.Errorf("browser websocket URL failed loopback validation")
	}
	ws, _, err := websocket.Dial(ctx, version.WebSocketDebuggerURL, nil)
	if err != nil {
		_ = proc.Close()
		return err
	}
	c.mu.Lock()
	c.proc = proc
	c.client = newCDPClient(&websocketConn{conn: ws})
	c.browserVersion = version.Browser
	c.targets = make(map[string]string)
	c.mu.Unlock()
	return nil
}
```

Keep imports exact: `encoding/json`, `fmt`, `io`, `net/http`, `os`, `os/exec`, `path/filepath`, `strings`, `nhooyr.io/websocket`.

- [ ] **Step 5: Add real tab operations**

Implement `open`, `tabs`, `use`, `navigate`, and `close` with CDP:

```go
func (c *Controller) open(ctx context.Context, rawURL string) (tabInfo, error) {
	if err := c.ensureStarted(ctx); err != nil {
		return tabInfo{}, err
	}
	url := rawURL
	if url == "" {
		url = "about:blank"
	}
	var created struct{ TargetID string `json:"targetId"` }
	if err := c.client.Call(ctx, "Target.createTarget", map[string]any{"url": url}, &created); err != nil {
		return tabInfo{}, err
	}
	var attached struct{ SessionID string `json:"sessionId"` }
	if err := c.client.Call(ctx, "Target.attachToTarget", map[string]any{"targetId": created.TargetID, "flatten": true}, &attached); err != nil {
		return tabInfo{}, err
	}
	_ = c.client.Call(ctx, "Target.activateTarget", map[string]any{"targetId": created.TargetID}, nil)
	c.mu.Lock()
	c.activeTargetID = created.TargetID
	c.targets[created.TargetID] = attached.SessionID
	c.mu.Unlock()
	return c.currentTabInfo(ctx, created.TargetID)
}
```

Use `Target.getTargets` for `tabs`, `Target.activateTarget` for `use`, `Page.navigate` with `CallSession` for `navigate`, and `Target.closeTarget` for `close`.

- [ ] **Step 6: Run focused browser package tests**

Run:

```bash
go test ./browser -count=1
```

Expected: PASS without launching Chrome in unit tests.

- [ ] **Step 7: Commit**

```bash
git add browser/controller.go browser/launcher.go browser/cdp.go browser/controller_backend_test.go
git commit -m "feat: add managed cdp browser backend"
```

## Task 7: Snapshot, Click, Type, Evaluate, And Screenshot

**Files:**
- Create: `browser/snapshot.go`
- Create: `browser/screenshot.go`
- Modify: `browser/controller.go`
- Test: `browser/snapshot_test.go`
- Test: `browser/screenshot_test.go`

- [ ] **Step 1: Write failing snapshot truncation tests**

Create `browser/snapshot_test.go`:

```go
package browser

import (
	"strings"
	"testing"
)

func TestTruncateRunes(t *testing.T) {
	got := truncateRunes(strings.Repeat("가", 13000), 12000)
	if len([]rune(got)) != 12014 {
		t.Fatalf("rune len = %d", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "...(truncated)") {
		t.Fatalf("missing suffix: %q", got[len(got)-20:])
	}
}

func TestElementRefsAreStable(t *testing.T) {
	elements := []snapshotElement{{Role: "link", Text: "Docs", Selector: "a:nth-of-type(1)"}}
	assignRefs(elements)
	if elements[0].Ref != "e1" {
		t.Fatalf("ref = %q", elements[0].Ref)
	}
}
```

- [ ] **Step 2: Write failing screenshot file test**

Create `browser/screenshot_test.go`:

```go
package browser

import (
	"encoding/base64"
	"os"
	"testing"
)

func TestWriteScreenshot(t *testing.T) {
	c := NewController(ControllerOptions{Config: testBrowserConfig(), BaseDir: t.TempDir()})
	out, err := c.writeScreenshot(base64.StdEncoding.EncodeToString([]byte("png-bytes")), "png")
	if err != nil {
		t.Fatal(err)
	}
	if out.Bytes != len("png-bytes") {
		t.Fatalf("bytes = %d", out.Bytes)
	}
	data, err := os.ReadFile(out.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "png-bytes" {
		t.Fatalf("data = %q", data)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run:

```bash
go test ./browser -run 'TestTruncateRunes|TestElementRefsAreStable|TestWriteScreenshot' -count=1
```

Expected: build failure for missing snapshot and screenshot helpers.

- [ ] **Step 4: Add snapshot helpers**

Create `browser/snapshot.go`:

```go
package browser

import "strings"

type SnapshotResult struct {
	TargetID string            `json:"target_id"`
	URL      string            `json:"url"`
	Title    string            `json:"title"`
	Text     string            `json:"text"`
	Elements []snapshotElement `json:"elements"`
}

type snapshotElement struct {
	Ref      string `json:"ref"`
	Role     string `json:"role"`
	Text     string `json:"text,omitempty"`
	Selector string `json:"selector"`
}

func truncateRunes(s string, limit int) string {
	r := []rune(s)
	if limit <= 0 || len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "...(truncated)"
}

func assignRefs(elements []snapshotElement) {
	for i := range elements {
		elements[i].Ref = "e" + strconv.Itoa(i+1)
	}
}
```

Add `strconv` import.

Add `snapshotScript` constant that returns `{url,title,text,elements}` from the active page using `document.body.innerText` and selectors for `a,button,input,textarea,select,[role=button],[onclick]`.

- [ ] **Step 5: Add screenshot helper**

Create `browser/screenshot.go`:

```go
package browser

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type ScreenshotResult struct {
	Path  string `json:"path"`
	Mime  string `json:"mime"`
	Bytes int    `json:"bytes"`
}

func (c *Controller) writeScreenshot(encoded, format string) (ScreenshotResult, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return ScreenshotResult{}, err
	}
	if format == "" {
		format = "png"
	}
	dir := filepath.Join(c.dataDir, "screenshots")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ScreenshotResult{}, err
	}
	path := filepath.Join(dir, fmt.Sprintf("shot-%s.%s", time.Now().Format("20060102-150405"), format))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return ScreenshotResult{}, err
	}
	return ScreenshotResult{Path: path, Mime: "image/" + format, Bytes: len(data)}, nil
}
```

- [ ] **Step 6: Extend controller dispatch and backend methods**

Add `snapshot`, `click`, `typeText`, `evaluate`, and `screenshot` to the `backend` interface and `Execute` switch.

Implement CDP methods:

- `snapshot`: `Runtime.evaluate` `snapshotScript`, store `ref -> selector` in controller.
- `click`: resolve ref or selector, evaluate bounding rect, send `Input.dispatchMouseEvent` pressed and released.
- `typeText`: cap text to `defaultTypeTextLimit`, focus selector, set value for inputs/textareas, dispatch input/change events.
- `evaluate`: `Runtime.evaluate` with `returnByValue=true`, cap marshaled result to `defaultEvaluateLimit`.
- `screenshot`: `Page.captureScreenshot`, call `writeScreenshot`.

- [ ] **Step 7: Run focused tests**

Run:

```bash
go test ./browser -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add browser/snapshot.go browser/screenshot.go browser/controller.go browser/snapshot_test.go browser/screenshot_test.go
git commit -m "feat: add browser page actions"
```

## Task 8: Engine And Server Wiring

**Files:**
- Modify: `engine/session.go`
- Modify: `engine/executor.go`
- Modify: `engine/executor_test.go`
- Modify: `server/account_deps.go`
- Create: `server/browser_wiring_test.go`

- [ ] **Step 1: Write failing engine dispatch test**

Add to `engine/executor_test.go`:

```go
type fakeBrowserController struct{ calls []core.SkillCall }

func (f *fakeBrowserController) Execute(ctx context.Context, call core.SkillCall) (string, error) {
	f.calls = append(f.calls, call)
	return `{"ok":true}`, nil
}
func (f *fakeBrowserController) Close() error { return nil }

func TestResolveSkillCallBrowserDispatch(t *testing.T) {
	fake := &fakeBrowserController{}
	s := &Session{Config: &core.Config{AutonomyLevel: core.AutonomyFull}, BrowserController: fake}
	got, err := resolveSkillCall(context.Background(), core.SkillCall{SkillName: "Browser", Method: "status"}, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"ok":true}` {
		t.Fatalf("got %s", got)
	}
	if len(fake.calls) != 1 || fake.calls[0].Method != "status" {
		t.Fatalf("calls = %#v", fake.calls)
	}
}

func TestResolveSkillCallBrowserNotConfigured(t *testing.T) {
	s := &Session{Config: &core.Config{AutonomyLevel: core.AutonomyFull}}
	got, err := resolveSkillCall(context.Background(), core.SkillCall{SkillName: "Browser", Method: "status"}, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "browser not configured") {
		t.Fatalf("got %s", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./engine -run 'TestResolveSkillCallBrowser' -count=1
```

Expected: build failure for missing `BrowserController` field and dispatch.

- [ ] **Step 3: Add engine browser interface and dispatch**

In `engine/session.go`, add:

```go
type BrowserController interface {
	Execute(context.Context, core.SkillCall) (string, error)
	Close() error
}
```

Add field to `Session`:

```go
BrowserController BrowserController
```

In `engine/executor.go`, add switch case:

```go
case "Browser":
	if s.BrowserController == nil {
		return jsonResult(map[string]any{"error": "browser not configured"})
	}
	return s.BrowserController.Execute(ctx, call)
```

- [ ] **Step 4: Write server wiring test**

Create `server/browser_wiring_test.go`:

```go
package server

import (
	"testing"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/store"
)

func openBrowserWiringTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestBuildAccountSessionWiresBrowserController(t *testing.T) {
	td := &AccountDeps{
		Account: &core.Account{ID: "alice", BaseDir: t.TempDir(), Config: &core.Config{
			Browser: core.BrowserConfig{Enabled: true, TimeoutSeconds: 1},
			Workspace: core.WorkspaceConfig{LiveIndex: false},
		}},
		Store: openBrowserWiringTestStore(t),
	}
	sess := buildAccountSession(td, core.NewAccountRegistry(t.TempDir(), "alice"), nil)
	if sess.BrowserController == nil {
		t.Fatal("BrowserController not wired")
	}
}
```

- [ ] **Step 5: Run server test to verify it fails**

Run:

```bash
go test ./server -run TestBuildAccountSessionWiresBrowserController -count=1
```

Expected: failure because account deps do not include browser controller.

- [ ] **Step 6: Wire browser controller in server**

In `server/account_deps.go`, import `github.com/jinto/kittypaw/browser`.

Add to `AccountDeps`:

```go
BrowserController *browser.Controller
```

In `OpenAccountDeps`, after `apiTokenMgr`:

```go
browserController := browser.NewController(browser.ControllerOptions{
	Config:  t.Config.Browser,
	BaseDir: t.BaseDir,
})
```

Add to returned `AccountDeps`:

```go
BrowserController: browserController,
```

In `Close`, close browser before MCP and store:

```go
if td.BrowserController != nil {
	if err := td.BrowserController.Close(); err != nil {
		slog.Warn("close browser controller", "account", td.Account.ID, "error", err)
	}
}
```

In `buildAccountSession`, set:

```go
BrowserController: td.BrowserController,
```

- [ ] **Step 7: Run focused tests**

Run:

```bash
go test ./engine ./server -run 'TestResolveSkillCallBrowser|TestBuildAccountSessionWiresBrowserController|TestNeedsPermission' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add engine/session.go engine/executor.go engine/executor_test.go server/account_deps.go server/browser_wiring_test.go
git commit -m "feat: wire browser controller into engine"
```

## Task 9: Chrome Integration Test

**Files:**
- Create: `browser/integration_test.go`

- [ ] **Step 1: Write integration test gated by environment**

Create `browser/integration_test.go`:

```go
package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
)

func TestManagedChromeIntegration(t *testing.T) {
	if os.Getenv("KITTYPAW_BROWSER_INTEGRATION") != "1" {
		t.Skip("set KITTYPAW_BROWSER_INTEGRATION=1 to run managed Chrome integration")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><title>KP Test</title><body><input id="q"><button id="b" onclick="document.body.dataset.clicked='yes'">Go</button><p>Hello Browser</p></body></html>`))
	}))
	defer srv.Close()

	c := NewController(ControllerOptions{
		Config: core.BrowserConfig{Enabled: true, Headless: true, AllowedHosts: []string{"127.0.0.1"}, TimeoutSeconds: 10},
		BaseDir: t.TempDir(),
	})
	defer c.Close()

	ctx := context.Background()
	if _, err := c.open(ctx, srv.URL); err != nil {
		t.Fatalf("open: %v", err)
	}
	snap, err := c.snapshot(ctx, nil)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snap.Title != "KP Test" || !strings.Contains(snap.Text, "Hello Browser") {
		t.Fatalf("snapshot = %#v", snap)
	}
	if _, err := c.typeText(ctx, "#q", "kittypaw"); err != nil {
		t.Fatalf("typeText: %v", err)
	}
	if _, err := c.click(ctx, "#b"); err != nil {
		t.Fatalf("click: %v", err)
	}
	eval, err := c.evaluate(ctx, `document.querySelector("#q").value + ":" + document.body.dataset.clicked`)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !strings.Contains(eval, "kittypaw:yes") {
		t.Fatalf("evaluate = %s", eval)
	}
	shot, err := c.screenshot(ctx, "png")
	if err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	if shot.Bytes == 0 {
		t.Fatal("screenshot empty")
	}
}
```

- [ ] **Step 2: Run integration test without env**

Run:

```bash
go test ./browser -run TestManagedChromeIntegration -count=1
```

Expected: SKIP.

- [ ] **Step 3: Run integration test with env when Chrome exists**

Run:

```bash
KITTYPAW_BROWSER_INTEGRATION=1 go test ./browser -run TestManagedChromeIntegration -count=1 -v
```

Expected: PASS when Chrome/Chromium is installed; SKIP or FAIL with clear Chrome detection error is acceptable only if the local machine has no Chrome binary.

- [ ] **Step 4: Commit**

```bash
git add browser/integration_test.go
git commit -m "test: add managed chrome integration coverage"
```

## Task 10: Final Verification

**Files:**
- All touched files

- [ ] **Step 1: Format Go files**

Run:

```bash
gofmt -w browser core/config.go core/config_test.go core/skillmeta.go engine/session.go engine/executor.go engine/executor_test.go engine/teach_test.go server/account_deps.go server/browser_wiring_test.go
```

Expected: no output.

- [ ] **Step 2: Run focused unit tests**

Run:

```bash
go test ./core ./browser ./engine ./server -count=1
```

Expected: PASS.

- [ ] **Step 3: Run broader app tests if focused tests pass**

Run:

```bash
make test-unit
```

Expected: PASS.

- [ ] **Step 4: Inspect worktree**

Run:

```bash
git status --short
git diff --stat
```

Expected: only intentional files changed, no generated clutter.

- [ ] **Step 5: Commit final adjustments**

If formatting or test fixes changed files after the previous commits:

```bash
git add -A
git commit -m "fix: stabilize browser control tests"
```

Expected: commit created only when files changed.

## Self-Review Notes

- Spec coverage: managed Chrome, account-local user data, Browser methods, URL validation, screenshots, supervised approvals, engine/server wiring, and integration testing each have a task.
- Scope boundary: hosted apps, contracts, MCP server integration, file upload/download automation, and attach-only UX are not included.
- Type consistency: `core.BrowserConfig`, `browser.Controller`, `Controller.Execute(ctx, core.SkillCall)`, `engine.BrowserController`, and `server.AccountDeps.BrowserController` are the same names across tasks.
