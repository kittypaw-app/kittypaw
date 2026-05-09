package engine

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dop251/goja"
	"golang.org/x/sync/singleflight"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/store"
)

type contextKey string

const ctxKeyConversationID contextKey = "conversationID"
const ctxKeyEvent contextKey = "event"
const ctxKeyPackageParams contextKey = "packageParams"

// ContextWithConversationID stores the conversation ID in context for use by skill handlers.
func ContextWithConversationID(ctx context.Context, conversationID string) context.Context {
	return context.WithValue(ctx, ctxKeyConversationID, conversationID)
}

// ConversationIDFromContext retrieves the conversation ID from context.
func ConversationIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyConversationID).(string); ok {
		return v
	}
	return ""
}

// ContextWithEvent stores the inbound event in context so downstream handlers
// (e.g. runSkillOrPackage) can access channel/user info without signature changes.
func ContextWithEvent(ctx context.Context, event *core.Event) context.Context {
	return context.WithValue(ctx, ctxKeyEvent, event)
}

// EventFromContext retrieves the event from context. Returns nil for scheduler paths.
func EventFromContext(ctx context.Context) *core.Event {
	if v, ok := ctx.Value(ctxKeyEvent).(*core.Event); ok {
		return v
	}
	return nil
}

// ContextWithPackageParams stores structured, engine-resolved package inputs.
// Official packages may consume these values through __context__.params; raw
// natural-language parsing stays in the engine/LLM layer.
func ContextWithPackageParams(ctx context.Context, params map[string]any) context.Context {
	return context.WithValue(ctx, ctxKeyPackageParams, params)
}

func PackageParamsFromContext(ctx context.Context) map[string]any {
	if v, ok := ctx.Value(ctxKeyPackageParams).(map[string]any); ok {
		return v
	}
	return nil
}

// needsPermission checks whether a skill call requires explicit user approval
// based on the config's permission policy. Returns false for AutonomyFull
// (auto-approve) and AutonomyReadonly (execution blocked elsewhere).
func needsPermission(skillName, method string, cfg *core.Config) bool {
	if cfg.AutonomyLevel == core.AutonomyFull {
		return false
	}
	if cfg.AutonomyLevel == core.AutonomyReadonly {
		return false
	}
	key := skillName + "." + method
	list := cfg.Permissions.RequireApproval
	if list == nil {
		list = core.DefaultRequireApproval
	}
	return slices.Contains(list, key)
}

// resolveSkillCall dispatches a single skill call to the appropriate handler.
func resolveSkillCall(ctx context.Context, call core.SkillCall, s *Session, permFn PermissionCallback) (string, error) {
	slog.Debug("resolving skill call", "skill", call.SkillName, "method", call.Method)

	// Central permission gate — applies to ALL skills uniformly.
	if needsPermission(call.SkillName, call.Method, s.Config) {
		desc := fmt.Sprintf("%s.%s", call.SkillName, call.Method)
		// Include the first argument for context (e.g., the shell command or file path).
		if len(call.Args) > 0 {
			var arg string
			if json.Unmarshal(call.Args[0], &arg) == nil && arg != "" {
				const maxArgLen = 200
				if len(arg) > maxArgLen {
					// Avoid splitting a multi-byte UTF-8 character.
					arg = arg[:maxArgLen]
					for len(arg) > 0 && arg[len(arg)-1]&0xC0 == 0x80 {
						arg = arg[:len(arg)-1]
					}
					if len(arg) > 0 && arg[len(arg)-1]&0xC0 == 0xC0 {
						arg = arg[:len(arg)-1]
					}
					arg += "..."
				}
				desc += ": " + arg
			}
		}
		if permFn != nil {
			ok, err := permFn(ctx, desc, call.SkillName)
			if err != nil || !ok {
				return jsonResult(map[string]any{"error": call.SkillName + "." + call.Method + " permission denied"})
			}
		} else {
			return jsonResult(map[string]any{"error": call.SkillName + "." + call.Method + " requires permission approval"})
		}
	}

	switch call.SkillName {
	case "Http", "Web":
		return executeHTTP(ctx, call, s)
	case "File":
		return executeFile(ctx, call, s)
	case "Storage":
		return executeStorage(ctx, call, s)
	case "Shell":
		return executeShell(ctx, call, s)
	case "Git":
		return executeGit(ctx, call, s)
	case "Llm":
		return executeLLM(ctx, call, s)
	case "Moa":
		return executeMoA(ctx, call, s)
	case "Memory":
		return executeMemory(ctx, call, s)
	case "Todo":
		return executeTodo(ctx, call, s)
	case "Projects":
		return executeProjects(ctx, call, s)
	case "Env":
		return executeEnv(call)
	case "Telegram":
		return executeTelegram(ctx, call, s)
	case "Slack":
		return executeSlack(ctx, call, s)
	case "Discord":
		return executeDiscord(ctx, call, s)
	case "Gmail":
		return executeGmail(ctx, call, s)
	case "X":
		return executeX(ctx, call, s)
	case "Skill":
		return executeSkillMgmt(ctx, call, s)
	case "Staff":
		return executeStaff(ctx, call, s)
	case "Tts":
		return executeTTS(ctx, call, s)
	case "Image":
		return executeImage(ctx, call, s)
	case "Vision":
		return executeVision(ctx, call, s)
	case "Mcp":
		return executeMCP(ctx, call, s)
	case "Browser":
		if s.BrowserController == nil {
			return jsonResult(map[string]any{"error": "browser not configured"})
		}
		return s.BrowserController.Execute(ctx, call)
	case "Runner":
		return executeRunner(ctx, call, s)
	case "Share":
		return executeShare(ctx, call, s)
	case "Fanout":
		return executeFanout(ctx, call, s)
	case "Code":
		return executeCode(ctx, call, s)
	default:
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown skill: %s", call.SkillName)})
	}
}

// codeExecTimeout caps a single Code.exec call so a runaway loop in
// LLM-generated arithmetic cannot stall the runner loop. 1s is far more
// than any honest unit-conversion / scope-filter / fmt task needs.
const codeExecTimeout = 1 * time.Second

// codeExecMaxOutputBytes truncates Code.exec result + logs so a large
// computed payload (huge array, deeply nested object) cannot bloat the
// next-turn prompt. The cap matches moaCandidateCharLimit conceptually
// — the result is fed back into an LLM context which already has limits.
const codeExecMaxOutputBytes = 8000

// executeCode runs LLM-supplied JavaScript inside an isolated
// pure-compute sandbox: a fresh goja runtime with no Http/Storage/
// Skill/Llm/etc. globals registered. The intent is to give the LLM an
// explicit affordance for ad-hoc numeric / shape transforms — unit
// conversion, base reframe, scope filter, JSON re-arrangement — that
// are too one-off to live in a permanent JS skill but too error-prone
// to entrust to LLM paraphrase.
//
// Functionally close to the main runner loop's JS-as-response contract
// (every LLM reply already runs in goja). The marginal value is the
// affordance signal — a named tool tells the model "you can self-trigger
// computation when uncertain about a number" — and the lockdown: no IO
// surface, so a Code.exec call is guaranteed side-effect-free even if
// the model writes adversarial code by mistake.
func executeCode(_ context.Context, call core.SkillCall, _ *Session) (string, error) {
	if call.Method != "exec" {
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown Code method: %s", call.Method)})
	}
	if len(call.Args) == 0 {
		return jsonResult(map[string]any{"error": "code required"})
	}
	var code string
	_ = json.Unmarshal(call.Args[0], &code)
	if strings.TrimSpace(code) == "" {
		return jsonResult(map[string]any{"error": "empty code"})
	}

	vm := goja.New()

	// console.log capture — useful for the LLM to debug its own snippets
	// (the captured logs come back in the result envelope).
	var logs []string
	console := vm.NewObject()
	_ = console.Set("log", func(fc goja.FunctionCall) goja.Value {
		parts := make([]string, len(fc.Arguments))
		for i, a := range fc.Arguments {
			parts[i] = a.String()
		}
		logs = append(logs, strings.Join(parts, " "))
		if len(logs) > 100 {
			logs = logs[len(logs)-100:]
		}
		return goja.Undefined()
	})
	_ = vm.Set("console", console)

	timer := time.AfterFunc(codeExecTimeout, func() {
		vm.Interrupt("code.exec timed out")
	})
	defer timer.Stop()

	val, err := vm.RunString(code)
	if err != nil {
		return jsonResult(map[string]any{
			"error": err.Error(),
			"logs":  truncateLogs(logs),
		})
	}

	var result any
	if val != nil && !goja.IsUndefined(val) && !goja.IsNull(val) {
		result = val.Export()
	}
	out := map[string]any{
		"result": result,
		"logs":   truncateLogs(logs),
	}
	// Soft-cap the marshaled envelope.
	encoded, _ := json.Marshal(out)
	if len(encoded) > codeExecMaxOutputBytes {
		out["result"] = fmt.Sprintf("%v", result)
		if len(out["result"].(string)) > codeExecMaxOutputBytes {
			out["result"] = out["result"].(string)[:codeExecMaxOutputBytes] + "…(truncated)"
		}
	}
	return jsonResult(out)
}

func truncateLogs(logs []string) []string {
	if len(logs) <= 20 {
		return logs
	}
	// Keep first 5 + last 15 so both setup and final state are visible.
	out := make([]string, 0, 21)
	out = append(out, logs[:5]...)
	out = append(out, "…(truncated)")
	out = append(out, logs[len(logs)-15:]...)
	return out
}

// --- HTTP / Web ---

// httpValidatedHostKey stores the hostname that buildPackageResolver already validated
// against the package's AllowedHosts. executeHTTP compares the actual request hostname
// against this value instead of a blanket bypass, preventing URL-swap attacks.
const httpValidatedHostKey contextKey = "httpValidatedHost"

func executeHTTP(ctx context.Context, call core.SkillCall, s *Session) (string, error) {
	switch call.Method {
	case "get", "post", "put", "delete", "patch", "head", "search", "fetch":
	default:
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown Http method: %s", call.Method)})
	}

	if len(call.Args) == 0 {
		return jsonResult(map[string]any{"error": "url argument required"})
	}

	var urlStr string
	if err := json.Unmarshal(call.Args[0], &urlStr); err != nil {
		return jsonResult(map[string]any{"error": "invalid url"})
	}

	// SSRF prevention: skip only if the package resolver already validated this exact host.
	parsedURL, parseErr := url.Parse(urlStr)
	if parseErr != nil {
		return jsonResult(map[string]any{"error": "invalid url"})
	}
	if validatedHost, ok := ctx.Value(httpValidatedHostKey).(string); !ok || validatedHost != parsedURL.Hostname() {
		if err := validateHTTPTarget(urlStr, s.Config.Sandbox.AllowedHosts); err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
	}

	// Web.search uses a search API
	if call.Method == "search" {
		return webSearch(ctx, urlStr, s.Config)
	}
	// Web.fetch gets page text
	if call.Method == "fetch" {
		return webFetch(ctx, urlStr)
	}

	method := strings.ToUpper(call.Method)
	var body io.Reader
	if len(call.Args) > 1 && (method == "POST" || method == "PUT" || method == "PATCH") {
		body = strings.NewReader(string(call.Args[1]))
	}

	req, err := http.NewRequestWithContext(ctx, method, urlStr, body)
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// Apply optional headers from the last argument.
	// Non-body methods (GET/DELETE/HEAD): options at Args[1].
	// Body methods (POST/PUT/PATCH): options at Args[2].
	optIdx := 1
	if method == "POST" || method == "PUT" || method == "PATCH" {
		optIdx = 2
	}
	if len(call.Args) > optIdx {
		var opts struct {
			Headers map[string]string `json:"headers"`
		}
		if json.Unmarshal(call.Args[optIdx], &opts) == nil && opts.Headers != nil {
			for k, v := range opts.Headers {
				if isBlockedHeader(k) {
					continue
				}
				req.Header.Set(k, v)
			}
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 100_000))
	return jsonResult(map[string]any{
		"status": resp.StatusCode,
		"body":   string(respBody),
	})
}

// isBlockedHeader returns true for hop-by-hop and security-sensitive headers
// that sandbox code must not override.
func isBlockedHeader(name string) bool {
	switch strings.ToLower(name) {
	case "host", "transfer-encoding", "connection", "upgrade", "te", "trailer":
		return true
	}
	return false
}

// validateHTTPTarget enforces AllowedHosts and blocks requests to private/internal addresses.
// When an explicit allowlist is provided, it takes priority — listed hosts (including private
// IPs like localhost) are permitted. This enables packages to declare allowed_hosts = ["localhost"]
// for local API servers.
func validateHTTPTarget(urlStr string, allowedHosts []string) error {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	host := parsed.Hostname()

	// Explicit allowlist takes priority (permits private IPs if listed).
	if len(allowedHosts) > 0 {
		for _, h := range allowedHosts {
			if h == "*" || h == host {
				return nil
			}
		}
		return fmt.Errorf("host %q not in allowed hosts", host)
	}

	// Default (no allowlist): block private IPs.
	if core.IsPrivateIP(host) {
		return fmt.Errorf("requests to private/internal address %q are blocked", host)
	}
	return nil
}

func webSearch(ctx context.Context, query string, cfg *core.Config) (string, error) {
	backend, err := NewSearchBackend(&cfg.Web)
	if err != nil {
		return jsonResult(map[string]any{"results": []any{}, "error": err.Error()})
	}
	results, err := backend.Search(ctx, query, 10)
	if err != nil {
		// Fallback to DuckDuckGo when the primary backend fails (e.g. credits exhausted).
		if _, isDDG := backend.(*DuckDuckGoBackend); !isDDG {
			slog.Warn("search backend failed, falling back to DuckDuckGo", "error", err)
			ddg := &DuckDuckGoBackend{}
			if fallbackResults, fbErr := ddg.Search(ctx, query, 10); fbErr == nil {
				warning := fmt.Sprintf("검색 백엔드(%s) 오류: %s — DuckDuckGo로 대체 검색했습니다.", backendName(backend), err.Error())
				return jsonResult(map[string]any{"results": fallbackResults, "warning": warning})
			}
		}
		return jsonResult(map[string]any{"results": []any{}, "error": err.Error()})
	}
	return jsonResult(map[string]any{"results": results})
}

// backendName returns a human-readable name for a SearchBackend.
func backendName(b SearchBackend) string {
	switch b.(type) {
	case *FirecrawlBackend:
		return "Firecrawl"
	case *TavilyBackend:
		return "Tavily"
	case *DuckDuckGoBackend:
		return "DuckDuckGo"
	default:
		return "unknown"
	}
}

func webFetch(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	req.Header.Set("User-Agent", "KittyPaw/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 500_000))
	rawHTML := string(body)

	// Existing: strip HTML tags for plain text (backward compatible)
	text := stripHTMLTags(rawHTML)
	text = truncate(text, 10000)

	// New: structured markdown conversion
	md := htmlToMarkdown(rawHTML)
	md = truncate(md, 10000)

	// New: extract page title
	title := extractTitle(rawHTML)

	return jsonResult(map[string]any{
		"text":     text,
		"markdown": md,
		"title":    title,
		"status":   resp.StatusCode,
	})
}

// --- File ---

const maxFileReadSize = 10 * 1024 * 1024 // 10MB — protects LLM context from huge files.

type fileToolScope struct {
	allowedPaths []string
	workspaceIDs []string
}

func currentFileToolScope(ctx context.Context, s *Session) (fileToolScope, error) {
	scope := fileToolScope{}
	if s == nil {
		return scope, nil
	}
	scope.allowedPaths = s.AllowedPaths()
	if scope.allowedPaths != nil {
		allowed, err := normalizeFileToolAllowedPaths(scope.allowedPaths)
		if err != nil {
			return fileToolScope{}, err
		}
		scope.allowedPaths = allowed
	}
	if s.Store == nil {
		return scope, nil
	}

	conversationID := strings.TrimSpace(ConversationIDFromContext(ctx))
	if conversationID == "" {
		conversationID = conversationKey(s)
	}
	project, ok, err := projectForConversationScope(s.Store, conversationID)
	if err != nil {
		return fileToolScope{}, err
	}
	if ok {
		allowed, err := normalizeFileToolAllowedPaths([]string{project.RootPath})
		if err != nil {
			return fileToolScope{}, err
		}
		return fileToolScope{
			allowedPaths: allowed,
			workspaceIDs: []string{project.ID},
		}, nil
	}

	projects, err := s.Store.ListProjects(false)
	if err != nil {
		return fileToolScope{}, fmt.Errorf("list projects: %w", err)
	}
	if len(projects) > 0 {
		return fileToolScope{}, fmt.Errorf("project를 선택하세요")
	}
	return scope, nil
}

func projectForConversationScope(st *store.Store, conversationID string) (*store.Project, bool, error) {
	if st == nil || strings.TrimSpace(conversationID) == "" {
		return nil, false, nil
	}
	scope, ok, err := st.ConversationScope(conversationID)
	if err != nil || !ok {
		return nil, false, err
	}
	switch scope.ScopeType {
	case "project":
		project, err := st.GetProject(scope.ScopeID)
		if err != nil {
			return nil, true, fmt.Errorf("project conversation scope: %w", err)
		}
		return project, true, nil
	case "ticket":
		ticket, err := st.GetTicket(scope.ScopeID)
		if err != nil {
			return nil, true, fmt.Errorf("ticket conversation scope: %w", err)
		}
		project, err := st.GetProject(ticket.ProjectID)
		if err != nil {
			return nil, true, fmt.Errorf("ticket project scope: %w", err)
		}
		return project, true, nil
	default:
		return nil, false, nil
	}
}

func normalizeFileToolAllowedPaths(paths []string) ([]string, error) {
	allowed := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("path not allowed")
		}
		allowed = append(allowed, resolveForValidation(abs))
	}
	return allowed, nil
}

func executeFile(ctx context.Context, call core.SkillCall, s *Session) (string, error) {
	// Index-based methods dispatch early — they don't take a file path.
	switch call.Method {
	case "search":
		return executeFileSearch(ctx, call, s)
	case "stats":
		return executeFileStats(ctx, call, s)
	case "reindex":
		return executeFileReindex(ctx, call, s)
	case "summary":
		return executeFileSummary(ctx, call, s)
	}

	if len(call.Args) == 0 {
		return "", fmt.Errorf("path argument required")
	}
	var rawPath string
	if err := json.Unmarshal(call.Args[0], &rawPath); err != nil {
		return "", fmt.Errorf("invalid path argument")
	}
	scope, err := currentFileToolScope(ctx, s)
	if err != nil {
		return "", err
	}

	// Resolve the path once and use it for both validation and all file operations.
	// Relative paths are interpreted inside the account's default workspace, not
	// the server's current working directory.
	resolvedPath, err := resolveFileToolPath(rawPath, scope.allowedPaths)
	if err != nil {
		return "", err
	}

	switch call.Method {
	case "read":
		// Open + fstat + limited read on the same fd to prevent TOCTOU size bypass.
		f, err := os.Open(resolvedPath)
		if err != nil {
			return "", fmt.Errorf("file read: %w", err)
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return "", fmt.Errorf("file read: %w", err)
		}
		if info.Size() > maxFileReadSize {
			return "", fmt.Errorf("file too large: %d bytes (max %d)", info.Size(), maxFileReadSize)
		}
		data, err := io.ReadAll(io.LimitReader(f, maxFileReadSize+1))
		if err != nil {
			return "", fmt.Errorf("file read: %w", err)
		}
		return jsonResult(map[string]any{"content": string(data)})

	case "write":
		if len(call.Args) < 2 {
			return "", fmt.Errorf("content argument required")
		}
		var content string
		if err := json.Unmarshal(call.Args[1], &content); err != nil {
			return "", fmt.Errorf("invalid content argument")
		}
		if err := os.WriteFile(resolvedPath, []byte(content), 0o644); err != nil {
			return "", fmt.Errorf("file write: %w", err)
		}
		return jsonResult(map[string]any{"success": true})

	case "append":
		if len(call.Args) < 2 {
			return "", fmt.Errorf("content argument required")
		}
		var content string
		if err := json.Unmarshal(call.Args[1], &content); err != nil {
			return "", fmt.Errorf("invalid content argument")
		}
		f, err := os.OpenFile(resolvedPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return "", fmt.Errorf("file append: %w", err)
		}
		defer f.Close()
		if _, err := f.WriteString(content); err != nil {
			return "", fmt.Errorf("file append: %w", err)
		}
		return jsonResult(map[string]any{"success": true})

	case "delete":
		if err := os.Remove(resolvedPath); err != nil {
			return "", fmt.Errorf("file delete: %w", err)
		}
		return jsonResult(map[string]any{"success": true})

	case "list":
		entries, err := os.ReadDir(resolvedPath)
		if err != nil {
			return "", fmt.Errorf("file list: %w", err)
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return jsonResult(map[string]any{"files": names})

	case "exists":
		_, err := os.Stat(resolvedPath)
		return jsonResult(map[string]any{"exists": err == nil})

	case "mkdir":
		if err := os.MkdirAll(resolvedPath, 0o755); err != nil {
			return "", fmt.Errorf("file mkdir: %w", err)
		}
		return jsonResult(map[string]any{"success": true})

	default:
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown File method: %s", call.Method)})
	}
}

// --- File index methods ---

func executeFileSearch(ctx context.Context, call core.SkillCall, s *Session) (string, error) {
	if s.Indexer == nil {
		return "", fmt.Errorf("workspace indexer not available")
	}
	if len(call.Args) == 0 {
		return "", fmt.Errorf("query argument required")
	}
	var query string
	if err := json.Unmarshal(call.Args[0], &query); err != nil {
		return "", fmt.Errorf("invalid query argument")
	}
	if query == "" {
		return "", fmt.Errorf("empty search query")
	}

	var opts SearchOptions
	if len(call.Args) > 1 {
		_ = json.Unmarshal(call.Args[1], &opts)
	}
	scope, err := currentFileToolScope(ctx, s)
	if err != nil {
		return "", err
	}
	opts.WorkspaceIDs = scope.workspaceIDs

	result, err := s.Indexer.Search(ctx, query, opts)
	if err != nil {
		return "", fmt.Errorf("file search: %w", err)
	}

	// Post-filter by AllowedPaths (defense-in-depth).
	allowed := scope.allowedPaths
	if allowed != nil {
		filtered := make([]SearchHit, 0, len(result.Files))
		for _, hit := range result.Files {
			resolved := resolveForValidation(hit.Path)
			if isPathAllowedResolved(resolved, allowed) {
				filtered = append(filtered, hit)
			}
		}
		if len(filtered) != len(result.Files) {
			result.Total = len(filtered)
		}
		result.Files = filtered
	}

	return jsonResult(result)
}

func executeFileStats(ctx context.Context, call core.SkillCall, s *Session) (string, error) {
	if s.Indexer == nil {
		return "", fmt.Errorf("workspace indexer not available")
	}
	var opts StatsOptions
	if len(call.Args) > 0 {
		var path string
		if err := json.Unmarshal(call.Args[0], &path); err == nil {
			opts.Path = path
		}
	}
	scope, err := currentFileToolScope(ctx, s)
	if err != nil {
		return "", err
	}
	opts.WorkspaceIDs = scope.workspaceIDs
	result, err := s.Indexer.Stats(ctx, opts)
	if err != nil {
		return "", fmt.Errorf("file stats: %w", err)
	}
	return jsonResult(result)
}

func executeFileReindex(ctx context.Context, call core.SkillCall, s *Session) (string, error) {
	if s.Indexer == nil {
		return "", fmt.Errorf("workspace indexer not available")
	}

	// Determine which workspace(s) to reindex.
	var targetPath string
	if len(call.Args) > 0 {
		_ = json.Unmarshal(call.Args[0], &targetPath)
	}

	roots, err := s.Store.ListFileIndexRoots()
	if err != nil {
		return "", fmt.Errorf("list file index roots: %w", err)
	}
	scope, err := currentFileToolScope(ctx, s)
	if err != nil {
		return "", err
	}
	scopedWorkspaceIDs := map[string]bool{}
	for _, id := range scope.workspaceIDs {
		scopedWorkspaceIDs[id] = true
	}

	var totalResult IndexResult
	for _, root := range roots {
		if len(scopedWorkspaceIDs) > 0 && !scopedWorkspaceIDs[root.ID] {
			continue
		}
		if len(scopedWorkspaceIDs) == 0 && scope.allowedPaths != nil && !isPathAllowed(root.RootPath, scope.allowedPaths) {
			continue
		}
		// If a path is given, only reindex matching root.
		if targetPath != "" {
			absTarget, _ := filepath.Abs(targetPath)
			if !strings.HasPrefix(absTarget, root.RootPath) {
				continue
			}
		}
		result, reErr := s.Indexer.Reindex(ctx, root.ID, root.RootPath)
		if reErr != nil {
			slog.Warn("reindex failed", "root", root.ID, "error", reErr)
			totalResult.Errors++
			continue
		}
		totalResult.Indexed += result.Indexed
		totalResult.Skipped += result.Skipped
		totalResult.Errors += result.Errors
		totalResult.DurationMs += result.DurationMs
	}

	return jsonResult(totalResult)
}

// executeFileSummary is the File.summary(path, options?) dispatch.
// Performs Phase A validation (same as File.read) — filepath.Abs →
// resolveForValidation → isPathAllowedResolved → size cap — then loads
// the file bytes and delegates to QuerySummary, which owns caching,
// singleflight dedup, guard-rails, and provider calls.
func executeFileSummary(ctx context.Context, call core.SkillCall, s *Session) (string, error) {
	if len(call.Args) == 0 {
		return "", fmt.Errorf("path argument required")
	}
	var rawPath string
	if err := json.Unmarshal(call.Args[0], &rawPath); err != nil {
		return "", fmt.Errorf("invalid path argument")
	}
	scope, err := currentFileToolScope(ctx, s)
	if err != nil {
		return "", err
	}

	resolvedPath, err := resolveFileToolPath(rawPath, scope.allowedPaths)
	if err != nil {
		return "", err
	}

	// Parse optional second arg {model, force_refresh}.
	var opts struct {
		Model        string `json:"model"`
		ForceRefresh bool   `json:"force_refresh"`
	}
	if len(call.Args) >= 2 {
		if err := json.Unmarshal(call.Args[1], &opts); err != nil {
			return "", fmt.Errorf("invalid options argument")
		}
	}

	// Open + stat on the same fd eliminates TOCTOU between size check and
	// read. QuerySummary adds a token cap (150K) on top.
	f, err := os.Open(resolvedPath)
	if err != nil {
		return "", fmt.Errorf("file read: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("file read: %w", err)
	}
	if info.Size() > maxFileReadSize {
		return "", fmt.Errorf("file too large: %d bytes (max %d)", info.Size(), maxFileReadSize)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFileReadSize+1))
	if err != nil {
		return "", fmt.Errorf("file read: %w", err)
	}

	// Resolve which workspace owns resolvedPath so the cache key stays
	// consistent with RemoveFile's GC key (both hash workspace_id || path).
	// AllowedPaths already passed, so a matching workspace is guaranteed
	// under normal operation; a missing match signals a config/DB drift.
	workspaceID, err := resolveWorkspaceID(s, resolvedPath)
	if err != nil {
		return "", err
	}

	flight := s.SummaryFlight
	if flight == nil {
		flight = &singleflight.Group{}
	}
	budget := s.Budget
	if budget == nil {
		budget = NewSharedBudget(0)
	}
	model := opts.Model
	if model == "" {
		model = s.Config.LLM.Model
	}

	req := SummaryRequest{
		WorkspaceID: workspaceID,
		AbsPath:     resolvedPath,
		Content:     data,
		Model:       model,
		Force:       opts.ForceRefresh,
	}
	res, err := QuerySummary(ctx, req, s.Store, s.resolveProvider, budget, flight)
	if err != nil {
		return "", err
	}
	return jsonResult(res)
}

// resolveWorkspaceID finds the file index root whose root contains resolvedPath.
// Roots are stored symlink-resolved where possible, so a simple prefix match
// against the already-resolved input is sufficient.
func resolveWorkspaceID(s *Session, resolvedPath string) (string, error) {
	roots, err := s.Store.ListFileIndexRoots()
	if err != nil {
		return "", fmt.Errorf("list file index roots: %w", err)
	}
	sep := string(filepath.Separator)
	for _, root := range roots {
		if resolvedPath == root.RootPath || strings.HasPrefix(resolvedPath, root.RootPath+sep) {
			return root.ID, nil
		}
	}
	return "", fmt.Errorf("file index root not found for path")
}

func resolveFileToolPath(rawPath string, allowedPaths []string) (string, error) {
	targetPath := rawPath
	if !filepath.IsAbs(targetPath) {
		if len(allowedPaths) == 0 {
			return "", fmt.Errorf("path not allowed")
		}
		targetPath = filepath.Join(allowedPaths[0], targetPath)
	}

	absPath, err := filepath.Abs(targetPath)
	if err != nil {
		return "", fmt.Errorf("path not allowed")
	}
	resolvedPath := resolveForValidation(absPath)
	if !isPathAllowedResolved(resolvedPath, allowedPaths) {
		return "", fmt.Errorf("path not allowed")
	}
	return resolvedPath, nil
}

// --- Storage ---

func executeStorage(_ context.Context, call core.SkillCall, s *Session) (string, error) {
	switch call.Method {
	case "get":
		if len(call.Args) == 0 {
			return jsonResult(map[string]any{"error": "key required"})
		}
		var key string
		_ = json.Unmarshal(call.Args[0], &key)
		val, ok, err := s.Store.StorageGet("default", key)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		if !ok {
			return jsonResult(nil)
		}
		return jsonResult(map[string]any{"value": val})

	case "set":
		if len(call.Args) < 2 {
			return jsonResult(map[string]any{"error": "key and value required"})
		}
		var key string
		_ = json.Unmarshal(call.Args[0], &key)
		val := string(call.Args[1])
		if err := s.Store.StorageSet("default", key, val); err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"success": true})

	case "delete":
		if len(call.Args) == 0 {
			return jsonResult(map[string]any{"error": "key required"})
		}
		var key string
		_ = json.Unmarshal(call.Args[0], &key)
		if err := s.Store.StorageDelete("default", key); err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"success": true})

	case "list":
		keys, err := s.Store.StorageList("default")
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"keys": keys})

	default:
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown Storage method: %s", call.Method)})
	}
}

// --- Shell ---

func executeShell(ctx context.Context, call core.SkillCall, s *Session) (string, error) {
	if call.Method != "exec" {
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown Shell method: %s", call.Method)})
	}
	if len(call.Args) == 0 {
		return jsonResult(map[string]any{"error": "command required"})
	}
	var command string
	_ = json.Unmarshal(call.Args[0], &command)

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	output, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return jsonResult(map[string]any{"error": err.Error()})
		}
	}
	return jsonResult(map[string]any{
		"output":    string(output),
		"exit_code": exitCode,
	})
}

// --- Git ---

func executeGit(ctx context.Context, call core.SkillCall, s *Session) (string, error) {
	var args []string

	switch call.Method {
	case "status":
		args = []string{"status", "--short"}
	case "log":
		n := "10"
		if len(call.Args) > 0 {
			_ = json.Unmarshal(call.Args[0], &n)
		}
		args = []string{"log", "--oneline", "-n", n}
	case "diff":
		args = []string{"diff"}
	case "add":
		if len(call.Args) == 0 {
			args = []string{"add", "."}
		} else {
			var path string
			_ = json.Unmarshal(call.Args[0], &path)
			args = []string{"add", path}
		}
	case "commit":
		if len(call.Args) == 0 {
			return jsonResult(map[string]any{"error": "commit message required"})
		}
		var msg string
		_ = json.Unmarshal(call.Args[0], &msg)
		args = []string{"commit", "-m", msg}
	case "push":
		args = []string{"push"}
	case "pull":
		args = []string{"pull"}
	default:
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown Git method: %s", call.Method)})
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return jsonResult(map[string]any{"output": string(output), "error": err.Error()})
	}
	return jsonResult(map[string]any{"output": string(output)})
}

// --- LLM ---

// subLLMToolName is the synthetic tool identifier exposed to the model in the
// fake tool_use block. The literal "framework_context" reads as an obviously
// internal source so the model treats the paired tool_result as observation
// data rather than user-provided text.
const subLLMToolName = "framework_context"

// subLLMPrimingUser is the leading user message — opens the conversation
// (Anthropic requires first message role=user) and encodes the assistant
// behavior contract for sub-LLM calls. Phrased as a *general principle* (do
// not enumerate forbidden phrases case-by-case) so it does not collide with
// LLM priors the way specific phrase blocklists do (R-MVP failure mode).
//
// External grounding:
//   - Anthropic Claude 4 system prompt: "Search results aren't from the human
//   - do not thank the user for results."
//   - OpenAI Model Spec (2025-10-27): tool output is untrusted relative to
//     user intent; honest uncertainty preferred over confident fabrication.
const subLLMPrimingUser = `당신은 사용자의 비서입니다. 아래에 오는 도구 결과는 사용자가 보낸 메시지가 아니라 비서인 당신이 호출한 도구의 응답입니다.

원칙:
- 비서 시점으로 응답하세요 (first person). "찾아본 결과로는…", "확인해보니…", "I checked and…" 처럼.
- 도구 결과를 사용자가 제공한 것으로 취급하지 마세요. 사용자에게 결과를 받은 듯 표현하는 모든 한국어/영어 phrasing 금지 — 도구 출력은 당신의 호출 결과이지 사용자의 입력이 아닙니다.
- 결과가 부족하면 솔직히 인정하고 다음 행동을 제안하세요 (다른 키워드 검색, 더 구체적 source, 도메인 스킬 설치 권유 등). 정보가 없다고 단순 거부하지 마세요.

위 원칙을 지키며 사용자에게 자연스러운 비서 응답을 작성하세요.`

// subLLMRoleTagOpen / Close wrap the tool_result payload in an XML role tag
// so the model has an additional structural signal beyond the Anthropic
// content block — defense-in-depth against mis-attribution. The source
// attribute is generic ("framework_context") because the sub-LLM call site
// does not know which downstream tool produced the prompt.
const (
	subLLMRoleTagOpen  = "<tool_result source=\"framework_context\">\n"
	subLLMRoleTagClose = "\n</tool_result>"
)

// wrapSubLLMToolResult applies the XML role tag around the prompt that lands
// in the tool_result content. Splitting the wrap into a helper makes the
// behavior testable without exporting the constants individually.
func wrapSubLLMToolResult(prompt string) string {
	return subLLMRoleTagOpen + prompt + subLLMRoleTagClose
}

// buildSubLLMMessages wraps a single sub-LLM prompt in a synthetic
// tool_use + tool_result pair so the model sees the embedded content as
// framework-provided observation rather than user input. The XML role tag
// inside the tool_result content is a second layer (the Anthropic block
// itself is the first) that survives even when the model partially ignores
// the protocol-level signal — common with strong language priors.
func buildSubLLMMessages(prompt string) []core.LlmMessage {
	toolUseID := newSubLLMToolUseID()
	return []core.LlmMessage{
		{Role: core.RoleUser, Content: subLLMPrimingUser},
		{
			Role: core.RoleAssistant,
			ContentBlocks: []core.ContentBlock{
				{
					Type:  core.BlockTypeToolUse,
					ID:    toolUseID,
					Name:  subLLMToolName,
					Input: map[string]any{},
				},
			},
		},
		{
			Role: core.RoleUser,
			ContentBlocks: []core.ContentBlock{
				{
					Type:      core.BlockTypeToolResult,
					ToolUseID: toolUseID,
					Content:   wrapSubLLMToolResult(prompt),
				},
			},
		},
	}
}

// newSubLLMToolUseID returns an Anthropic-style tool_use_id (toolu_<hex>).
// Length and prefix mirror what the API itself emits so logs and any future
// LLM-side debugging stay readable. Uniqueness across concurrent skill calls
// avoids a stale tool_result from one call being attributed to another.
func newSubLLMToolUseID() string {
	var b [12]byte
	_, _ = cryptorand.Read(b[:])
	return "toolu_" + hex.EncodeToString(b[:])
}

func executeLLM(ctx context.Context, call core.SkillCall, s *Session) (string, error) {
	if call.Method != "generate" {
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown Llm method: %s", call.Method)})
	}
	if len(call.Args) == 0 {
		return jsonResult(map[string]any{"error": "prompt required"})
	}
	var prompt string
	_ = json.Unmarshal(call.Args[0], &prompt)

	messages := buildSubLLMMessages(prompt)
	resp, err := s.Provider.Generate(WithLLMCallKind(ctx, "tool.llm"), messages)
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}

	result := map[string]any{"text": resp.Content}
	if resp.Usage != nil {
		result["model"] = resp.Usage.Model
		result["usage"] = map[string]any{
			"input_tokens":  resp.Usage.InputTokens,
			"output_tokens": resp.Usage.OutputTokens,
		}
	}
	return jsonResult(result)
}

// --- Memory ---

func executeMemory(_ context.Context, call core.SkillCall, s *Session) (string, error) {
	switch call.Method {
	case "search":
		if len(call.Args) == 0 {
			return jsonResult(map[string]any{"error": "query required"})
		}
		var query string
		_ = json.Unmarshal(call.Args[0], &query)
		results, err := s.Store.SearchExecutions(query, 10)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"results": results})

	case "user", "set":
		if len(call.Args) < 2 {
			return jsonResult(map[string]any{"error": "key and value required"})
		}
		var key string
		_ = json.Unmarshal(call.Args[0], &key)
		var value string
		_ = json.Unmarshal(call.Args[1], &value)
		if err := s.Store.SetUserContext(key, value, "runner"); err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"success": true})

	case "get":
		if len(call.Args) == 0 {
			return jsonResult(map[string]any{"error": "key required"})
		}
		var key string
		_ = json.Unmarshal(call.Args[0], &key)
		val, ok, err := s.Store.GetUserContext(key)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		if !ok {
			return jsonResult(nil)
		}
		return jsonResult(map[string]any{"value": val})

	case "delete":
		if len(call.Args) == 0 {
			return jsonResult(map[string]any{"error": "key required"})
		}
		var key string
		_ = json.Unmarshal(call.Args[0], &key)
		ok, err := s.Store.DeleteUserContext(key)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"deleted": ok})

	default:
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown Memory method: %s", call.Method)})
	}
}

// --- Todo ---

func executeTodo(_ context.Context, call core.SkillCall, s *Session) (string, error) {
	ns := "todo"
	switch call.Method {
	case "list":
		keys, err := s.Store.StorageList(ns)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		var items []map[string]string
		for _, k := range keys {
			val, _, _ := s.Store.StorageGet(ns, k)
			items = append(items, map[string]string{"id": k, "text": val})
		}
		return jsonResult(map[string]any{"items": items})

	case "add":
		if len(call.Args) == 0 {
			return jsonResult(map[string]any{"error": "text required"})
		}
		var text string
		_ = json.Unmarshal(call.Args[0], &text)
		id := fmt.Sprintf("todo-%d", time.Now().UnixNano())
		if err := s.Store.StorageSet(ns, id, text); err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"id": id, "success": true})

	case "update":
		if len(call.Args) < 2 {
			return jsonResult(map[string]any{"error": "id and text required"})
		}
		var id, text string
		_ = json.Unmarshal(call.Args[0], &id)
		_ = json.Unmarshal(call.Args[1], &text)
		if err := s.Store.StorageSet(ns, id, text); err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"success": true})

	case "delete":
		if len(call.Args) == 0 {
			return jsonResult(map[string]any{"error": "id required"})
		}
		var id string
		_ = json.Unmarshal(call.Args[0], &id)
		if err := s.Store.StorageDelete(ns, id); err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"success": true})

	default:
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown Todo method: %s", call.Method)})
	}
}

// --- Projects ---

type projectsTicketOptions struct {
	Project   string   `json:"project"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Status    string   `json:"status"`
	Priority  int      `json:"priority"`
	Labels    []string `json:"labels"`
	CreatedBy string   `json:"created_by"`
	ActorID   string   `json:"actor_id"`
	Message   string   `json:"message"`
	AuthorID  string   `json:"author_id"`
}

type projectsBriefOptions struct {
	Project             string `json:"project"`
	Title               string `json:"title"`
	BriefJSON           string `json:"brief_json"`
	ProposedTicketsJSON string `json:"proposed_tickets_json"`
	CreatedBy           string `json:"created_by"`
	ActorID             string `json:"actor_id"`
}

type projectsJobOptions struct {
	DriverID      string `json:"driver_id"`
	Mode          string `json:"mode"`
	WorktreePath  string `json:"worktree_path"`
	BranchName    string `json:"branch_name"`
	PromptSummary string `json:"prompt_summary"`
	PromptText    string `json:"prompt_text"`
	CreatedBy     string `json:"created_by"`
	ActorID       string `json:"actor_id"`
	Reason        string `json:"reason"`
	Text          string `json:"text"`
}

func executeProjects(_ context.Context, call core.SkillCall, s *Session) (string, error) {
	if s.Store == nil {
		return jsonResult(map[string]any{"error": "projects store not configured"})
	}
	switch call.Method {
	case "list":
		projects, err := s.Store.ListProjects(false)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"projects": projects})
	case "current":
		projects, err := s.Store.ListProjects(false)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		if len(projects) == 0 {
			return jsonResult(map[string]any{"project": nil})
		}
		return jsonResult(map[string]any{"project": projects[0]})
	case "show":
		projectID, err := projectsToolStringArg(call, 0, "project")
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		project, err := s.Store.GetProject(projectID)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		board, err := s.Store.ProjectBoard(project.ID)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"project": project, "board": board})
	case "listTickets":
		projectID, err := projectsToolStringArg(call, 0, "project")
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		project, err := s.Store.GetProject(projectID)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		tickets, err := s.Store.ListTickets(store.TicketListFilter{ProjectID: project.ID})
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"tickets": tickets})
	case "createTicket":
		return executeProjectsCreateTicket(call, s)
	case "showTicket":
		ticketID, err := projectsToolStringArg(call, 0, "ticket")
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		ticket, err := s.Store.GetTicket(ticketID)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		actions, err := s.Store.ListTicketActions(ticket.ID)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"ticket": ticket, "actions": actions})
	case "moveTicket":
		return executeProjectsMoveTicket(call, s)
	case "commentTicket":
		return executeProjectsCommentTicket(call, s)
	case "createBriefDraft":
		return executeProjectsCreateBriefDraft(call, s)
	case "updateBriefDraft":
		return executeProjectsUpdateBriefDraft(call, s)
	case "commitBriefDraft":
		draftID, err := projectsToolStringArg(call, 0, "draft")
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		opts := projectsBriefOptionsArg(call, 1)
		result, err := s.Store.CommitProjectBriefDraft(draftID, strings.TrimSpace(opts.ActorID))
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"result": result})
	case "planJob":
		return executeProjectsPlanJob(call, s)
	case "showJob":
		jobID, err := projectsToolStringArg(call, 0, "job")
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		job, err := s.Store.GetJob(jobID)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"job": job})
	case "cancelJob":
		jobID, err := projectsToolStringArg(call, 0, "job")
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		opts := projectsJobOptionsArg(call, 1)
		job, err := s.Store.CancelJob(jobID, strings.TrimSpace(opts.ActorID), strings.TrimSpace(opts.Reason))
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"job": job})
	case "appendJobInput":
		jobID, err := projectsToolStringArg(call, 0, "job")
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		opts := projectsJobOptionsArg(call, 1)
		event, err := s.Store.AddJobEvent(store.AddJobEventRequest{JobID: jobID, Type: "input", ActorID: opts.ActorID, Message: opts.Text})
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"event": event})
	default:
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown Projects method: %s", call.Method)})
	}
}

func executeProjectsCreateTicket(call core.SkillCall, s *Session) (string, error) {
	opts := projectsTicketOptionsArg(call, 0)
	if strings.TrimSpace(opts.Project) == "" {
		return jsonResult(map[string]any{"error": "project required"})
	}
	project, err := s.Store.GetProject(opts.Project)
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	ticket, err := s.Store.CreateTicket(store.CreateTicketRequest{
		ProjectID: project.ID,
		Title:     strings.TrimSpace(opts.Title),
		Body:      strings.TrimSpace(opts.Body),
		Status:    strings.TrimSpace(opts.Status),
		Priority:  opts.Priority,
		Labels:    opts.Labels,
		CreatedBy: strings.TrimSpace(opts.CreatedBy),
	})
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	return jsonResult(map[string]any{"ticket": ticket})
}

func executeProjectsMoveTicket(call core.SkillCall, s *Session) (string, error) {
	ticketID, err := projectsToolStringArg(call, 0, "ticket")
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	opts := projectsTicketOptionsArg(call, 1)
	ticket, err := s.Store.MoveTicket(ticketID, store.MoveTicketRequest{
		ActorID: strings.TrimSpace(opts.ActorID),
		Status:  strings.TrimSpace(opts.Status),
		Message: strings.TrimSpace(opts.Message),
	})
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	return jsonResult(map[string]any{"ticket": ticket})
}

func executeProjectsCommentTicket(call core.SkillCall, s *Session) (string, error) {
	ticketID, err := projectsToolStringArg(call, 0, "ticket")
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	opts := projectsTicketOptionsArg(call, 1)
	msg, err := s.Store.AddTicketMessage(store.AddTicketMessageRequest{
		TicketID: ticketID,
		AuthorID: strings.TrimSpace(opts.AuthorID),
		Body:     strings.TrimSpace(opts.Body),
	})
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	return jsonResult(map[string]any{"message": msg})
}

func executeProjectsCreateBriefDraft(call core.SkillCall, s *Session) (string, error) {
	projectID, err := projectsToolStringArg(call, 0, "project")
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	project, err := s.Store.GetProject(projectID)
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	opts := projectsBriefOptionsArg(call, 1)
	draft, err := s.Store.CreateProjectBriefDraft(store.CreateProjectBriefDraftRequest{
		ProjectID:           project.ID,
		Title:               strings.TrimSpace(opts.Title),
		BriefJSON:           strings.TrimSpace(opts.BriefJSON),
		ProposedTicketsJSON: strings.TrimSpace(opts.ProposedTicketsJSON),
		CreatedBy:           strings.TrimSpace(opts.CreatedBy),
	})
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	return jsonResult(map[string]any{"draft": draft})
}

func executeProjectsUpdateBriefDraft(call core.SkillCall, s *Session) (string, error) {
	draftID, err := projectsToolStringArg(call, 0, "draft")
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	opts := projectsBriefOptionsArg(call, 1)
	var title *string
	if strings.TrimSpace(opts.Title) != "" {
		v := strings.TrimSpace(opts.Title)
		title = &v
	}
	var briefJSON *string
	if strings.TrimSpace(opts.BriefJSON) != "" {
		v := strings.TrimSpace(opts.BriefJSON)
		briefJSON = &v
	}
	var proposedJSON *string
	if strings.TrimSpace(opts.ProposedTicketsJSON) != "" {
		v := strings.TrimSpace(opts.ProposedTicketsJSON)
		proposedJSON = &v
	}
	draft, err := s.Store.UpdateProjectBriefDraft(draftID, store.UpdateProjectBriefDraftRequest{
		Title:               title,
		BriefJSON:           briefJSON,
		ProposedTicketsJSON: proposedJSON,
	})
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	return jsonResult(map[string]any{"draft": draft})
}

func executeProjectsPlanJob(call core.SkillCall, s *Session) (string, error) {
	ticketID, err := projectsToolStringArg(call, 0, "ticket")
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	ticket, err := s.Store.GetTicket(ticketID)
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	if err := s.Store.EnsureDefaultDrivers(); err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	opts := projectsJobOptionsArg(call, 1)
	driverID := strings.TrimSpace(opts.DriverID)
	if driverID == "" {
		driverID = "codex"
	}
	job, err := s.Store.PlanJob(store.PlanJobRequest{
		ProjectID:     ticket.ProjectID,
		TicketID:      ticket.ID,
		DriverID:      driverID,
		Mode:          strings.TrimSpace(opts.Mode),
		WorktreePath:  strings.TrimSpace(opts.WorktreePath),
		BranchName:    strings.TrimSpace(opts.BranchName),
		PromptSummary: strings.TrimSpace(opts.PromptSummary),
		PromptText:    strings.TrimSpace(opts.PromptText),
		CreatedBy:     strings.TrimSpace(opts.CreatedBy),
	})
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	return jsonResult(map[string]any{"job": job})
}

func projectsToolStringArg(call core.SkillCall, index int, label string) (string, error) {
	if len(call.Args) <= index {
		return "", fmt.Errorf("%s required", label)
	}
	var value string
	if err := json.Unmarshal(call.Args[index], &value); err != nil {
		return "", fmt.Errorf("invalid %s argument", label)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s required", label)
	}
	return value, nil
}

func projectsTicketOptionsArg(call core.SkillCall, index int) projectsTicketOptions {
	if len(call.Args) <= index {
		return projectsTicketOptions{}
	}
	var opts projectsTicketOptions
	_ = json.Unmarshal(call.Args[index], &opts)
	return opts
}

func projectsBriefOptionsArg(call core.SkillCall, index int) projectsBriefOptions {
	if len(call.Args) <= index {
		return projectsBriefOptions{}
	}
	var opts projectsBriefOptions
	_ = json.Unmarshal(call.Args[index], &opts)
	return opts
}

func projectsJobOptionsArg(call core.SkillCall, index int) projectsJobOptions {
	if len(call.Args) <= index {
		return projectsJobOptions{}
	}
	var opts projectsJobOptions
	_ = json.Unmarshal(call.Args[index], &opts)
	return opts
}

// --- Env ---

func executeEnv(call core.SkillCall) (string, error) {
	if call.Method != "get" {
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown Env method: %s", call.Method)})
	}
	if len(call.Args) == 0 {
		return jsonResult(map[string]any{"error": "name required"})
	}
	var name string
	_ = json.Unmarshal(call.Args[0], &name)
	if core.IsSecretEnvVar(name) {
		return jsonResult(map[string]any{"error": fmt.Sprintf("access to secret env var %q is blocked", name)})
	}
	return jsonResult(map[string]any{"value": os.Getenv(name)})
}

// --- Channel sends (Telegram, Slack, Discord) ---

func executeTelegram(ctx context.Context, call core.SkillCall, s *Session) (string, error) {
	if call.Method != "sendMessage" && call.Method != "send" && call.Method != "sendVoice" {
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown Telegram method: %s", call.Method)})
	}
	// Find telegram token from config
	var token string
	for _, ch := range s.Config.Channels {
		if ch.ChannelType == core.ChannelTelegram {
			token = ch.Token
			break
		}
	}
	if token == "" {
		return jsonResult(map[string]any{"error": "telegram not configured"})
	}
	if len(call.Args) == 0 {
		return jsonResult(map[string]any{"error": "text required"})
	}
	var text string
	_ = json.Unmarshal(call.Args[0], &text)

	// Send via Telegram Bot API
	return sendTelegramMessage(ctx, token, text)
}

func executeSlack(ctx context.Context, call core.SkillCall, s *Session) (string, error) {
	if call.Method != "send" {
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown Slack method: %s", call.Method)})
	}
	_ = ctx
	_ = s
	if len(call.Args) == 0 {
		return jsonResult(map[string]any{"error": "text required"})
	}
	// TODO: implement Slack send
	return jsonResult(map[string]any{"success": true, "note": "slack send not yet implemented"})
}

func executeDiscord(ctx context.Context, call core.SkillCall, s *Session) (string, error) {
	if call.Method != "send" {
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown Discord method: %s", call.Method)})
	}
	_ = ctx
	_ = s
	if len(call.Args) == 0 {
		return jsonResult(map[string]any{"error": "text required"})
	}
	// TODO: implement Discord send
	return jsonResult(map[string]any{"success": true, "note": "discord send not yet implemented"})
}

// --- Skill Management ---

func executeSkillMgmt(ctx context.Context, call core.SkillCall, s *Session) (string, error) {
	switch call.Method {
	case "list":
		skills, err := core.LoadAllSkillsFrom(s.BaseDir)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		var items []map[string]any
		for _, sk := range skills {
			items = append(items, map[string]any{
				"name":        sk.Skill.Name,
				"description": sk.Skill.Description,
				"enabled":     sk.Skill.Enabled,
				"trigger":     sk.Skill.Trigger.Type,
			})
		}
		// Include installed packages.
		if s.PackageManager != nil {
			packages, pkgErr := s.PackageManager.ListInstalled()
			if pkgErr == nil {
				for _, pkg := range packages {
					items = append(items, map[string]any{
						"name":        pkg.Meta.ID,
						"description": pkg.Meta.Description,
						"enabled":     true,
						"trigger":     "package",
					})
				}
			}
		}
		return jsonResult(map[string]any{"skills": items})

	case "run":
		if len(call.Args) == 0 {
			return jsonResult(map[string]any{"error": "name required"})
		}
		var name string
		_ = json.Unmarshal(call.Args[0], &name)
		var params map[string]any
		if len(call.Args) > 1 && len(call.Args[1]) > 0 && string(call.Args[1]) != "null" {
			if err := json.Unmarshal(call.Args[1], &params); err != nil {
				return jsonResult(map[string]any{"error": fmt.Sprintf("Skill.run params must be an object: %v", err)})
			}
		}
		return runSkillOrPackageWithParams(ctx, name, s, params)

	case "create":
		if len(call.Args) < 3 {
			return jsonResult(map[string]any{"error": "name, description, and code required"})
		}
		var name, desc, code string
		_ = json.Unmarshal(call.Args[0], &name)
		_ = json.Unmarshal(call.Args[1], &desc)
		_ = json.Unmarshal(call.Args[2], &code)

		// Guard: reject if a package with the same ID is already installed.
		if s.PackageManager != nil {
			if pkg, _, loadErr := s.PackageManager.LoadPackage(name); loadErr == nil && pkg != nil {
				return jsonResult(map[string]any{
					"error": fmt.Sprintf(
						"package %q (%s, v%s) is already installed. Use Skill.run(%q) to execute it instead of creating a duplicate skill.",
						pkg.Meta.ID, pkg.Meta.Name, pkg.Meta.Version, pkg.Meta.ID),
					"installed_package": map[string]any{
						"id":          pkg.Meta.ID,
						"name":        pkg.Meta.Name,
						"version":     pkg.Meta.Version,
						"description": pkg.Meta.Description,
					},
				})
			}
		}

		triggerType := "manual"
		if len(call.Args) > 3 {
			_ = json.Unmarshal(call.Args[3], &triggerType)
		}
		schedule := ""
		if len(call.Args) > 4 {
			_ = json.Unmarshal(call.Args[4], &schedule)
		}

		skill := &core.Skill{
			Name:        name,
			Version:     1,
			Description: desc,
			Enabled:     true,
			Format:      core.SkillFormatNative,
			Trigger: core.SkillTrigger{
				Type: triggerType,
			},
		}
		if triggerType == "schedule" {
			skill.Trigger.Cron = schedule
		}

		if err := core.SaveSkillTo(s.BaseDir, skill, code); err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"success": true, "name": name})

	case "disable":
		if len(call.Args) == 0 {
			return jsonResult(map[string]any{"error": "name required"})
		}
		var name string
		_ = json.Unmarshal(call.Args[0], &name)
		if err := core.DisableSkillFrom(s.BaseDir, name); err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"success": true})

	case "uninstall":
		if len(call.Args) == 0 {
			return jsonResult(map[string]any{"error": "name required"})
		}
		var name string
		_ = json.Unmarshal(call.Args[0], &name)
		name = strings.TrimSpace(name)
		if name == "" {
			return jsonResult(map[string]any{"error": "name required"})
		}
		if s.PackageManager != nil {
			if err := s.PackageManager.Uninstall(name); err == nil {
				return jsonResult(map[string]any{"success": true, "name": name, "kind": "package"})
			} else if !strings.Contains(err.Error(), "not installed") && !strings.Contains(err.Error(), "package ID contains invalid") {
				return jsonResult(map[string]any{"error": err.Error()})
			}
			if pkgID, err := resolveInstalledPackageID(s.PackageManager, name); err == nil {
				if err := s.PackageManager.Uninstall(pkgID); err != nil {
					return jsonResult(map[string]any{"error": err.Error()})
				}
				return jsonResult(map[string]any{"success": true, "name": pkgID, "kind": "package"})
			} else if !strings.Contains(err.Error(), "not installed") && !strings.Contains(err.Error(), "package ID contains invalid") {
				return jsonResult(map[string]any{"error": err.Error()})
			}
		}
		skill, _, err := core.LoadSkillFrom(s.BaseDir, name)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		if skill == nil {
			return jsonResult(map[string]any{"error": fmt.Sprintf("skill or package %q not installed", name)})
		}
		if err := core.DeleteSkillFrom(s.BaseDir, name); err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"success": true, "name": name, "kind": "skill"})

	case "rollback":
		if len(call.Args) == 0 {
			return jsonResult(map[string]any{"error": "name required"})
		}
		var name string
		_ = json.Unmarshal(call.Args[0], &name)
		if err := core.RollbackSkillFrom(s.BaseDir, name); err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"success": true})

	case "search":
		if len(call.Args) == 0 {
			return jsonResult(map[string]any{"error": "query required"})
		}
		var query string
		_ = json.Unmarshal(call.Args[0], &query)
		return executeSkillSearch(query, s)

	case "installFromRegistry":
		if len(call.Args) == 0 {
			return jsonResult(map[string]any{"error": "id required"})
		}
		var id string
		_ = json.Unmarshal(call.Args[0], &id)
		return executeSkillInstallRegistry(ctx, id, s)

	default:
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown Skill method: %s", call.Method)})
	}
}

func resolveInstalledPackageID(pm *core.PackageManager, name string) (string, error) {
	packages, err := pm.ListInstalled()
	if err != nil {
		return "", err
	}
	for _, pkg := range packages {
		if pkg.Meta.ID == name || pkg.Meta.Name == name {
			return pkg.Meta.ID, nil
		}
	}
	if err := core.ValidatePackageID(name); err != nil {
		return "", err
	}
	return "", fmt.Errorf("package %q not installed", name)
}

// executeSkillSearch wraps RegistryClient.SearchEntries; empty query browses
// the full index, keyword query narrows for the suffix-offer flow.
func executeSkillSearch(query string, s *Session) (string, error) {
	const (
		maxSuffixEntries = 5
		maxBrowseEntries = 30
	)
	rc, err := newRegistryClient(s.Config)
	if err != nil {
		return jsonResult(map[string]any{"error": fmt.Sprintf("registry: %v", err)})
	}
	entries, err := rc.SearchEntries(query)
	if err != nil {
		return jsonResult(map[string]any{"error": fmt.Sprintf("search: %v", err)})
	}
	limit := maxSuffixEntries
	if query == "" {
		limit = maxBrowseEntries
	}
	if len(entries) > limit {
		entries = entries[:limit]
	}
	// Capture for the deterministic InstallConsentBranch — when the user
	// replies "네"/"설치해줘요"/etc. on the next turn, the classifier
	// reads this slice to drive a deterministic install instead of
	// asking the LLM to recall the id (which truncates names).
	s.Pipeline.RecordSkillSearch(entries)
	results := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		results = append(results, map[string]any{
			"id":          e.ID,
			"name":        e.Name,
			"version":     e.Version,
			"description": e.Description,
			"author":      e.Author,
		})
	}
	return jsonResult(map[string]any{"results": results})
}

// executeSkillInstallRegistry installs a skill from the registry. First-touch
// consent is owned by the LLM-level confirm flow (CapabilityBlock suffix +
// auto-discovery). System-level gating is opt-in via config.toml — callers
// who want a hard gate must list "Skill.installFromRegistry" under
// [permissions] require_approval and use a channel that implements
// channel.Confirmer (currently Telegram).
func executeSkillInstallRegistry(_ context.Context, id string, s *Session) (string, error) {
	if s.PackageManager == nil {
		return jsonResult(map[string]any{"error": "package manager not configured"})
	}
	rc, err := newRegistryClient(s.Config)
	if err != nil {
		return jsonResult(map[string]any{"error": fmt.Sprintf("registry: %v", err)})
	}
	entry, err := rc.FindEntry(id)
	if err != nil {
		return jsonResult(map[string]any{"error": fmt.Sprintf("find: %v", err)})
	}
	if entry == nil {
		return jsonResult(map[string]any{"error": fmt.Sprintf("skill %q not found in registry", id)})
	}
	pkg, err := s.PackageManager.InstallFromRegistry(rc, *entry)
	if err != nil {
		return jsonResult(map[string]any{"error": fmt.Sprintf("install: %v", err)})
	}
	return jsonResult(map[string]any{
		"success":     true,
		"id":          pkg.Meta.ID,
		"name":        pkg.Meta.Name,
		"version":     pkg.Meta.Version,
		"description": pkg.Meta.Description,
	})
}

// newRegistryClient constructs a RegistryClient from the session config,
// falling back to DefaultRegistryURL when no override is set.
func newRegistryClient(cfg *core.Config) (*core.RegistryClient, error) {
	url := core.DefaultRegistryURL
	if cfg != nil && cfg.Registry.URL != "" {
		url = cfg.Registry.URL
	}
	return core.NewRegistryClient(url)
}

// runSkillOrPackage executes a user-created skill or installed package by name.
// User skills take priority over packages with the same name.
func runSkillOrPackage(ctx context.Context, name string, s *Session) (string, error) {
	return runSkillOrPackageWithParams(ctx, name, s, nil)
}

func runSkillOrPackageWithParams(ctx context.Context, name string, s *Session, params map[string]any) (string, error) {
	if len(params) > 0 {
		ctx = ContextWithPackageParams(ctx, params)
	}
	// Try user-created skill first.
	skill, code, err := core.LoadSkillFrom(s.BaseDir, name)
	if err == nil && skill != nil && code != "" {
		if !skill.Enabled {
			return jsonResult(map[string]any{"error": fmt.Sprintf("skill %q is disabled", name)})
		}
		resolver := func(ctx context.Context, call core.SkillCall) (string, error) {
			return resolveSkillCall(ctx, call, s, nil)
		}
		jsContext := map[string]any{}
		result, execErr := s.Sandbox.ExecuteWithResolverOpts(ctx, code, jsContext, resolver, s.sandboxOptions())
		if execErr != nil {
			return jsonResult(map[string]any{"error": fmt.Sprintf("skill %q execution failed: %v", name, execErr)})
		}
		if !result.Success {
			return jsonResult(map[string]any{"error": result.Error, "output": result.Output})
		}
		return jsonResult(map[string]any{"success": true, "output": result.Output})
	}

	// Try installed package.
	notFoundResult := func(detail string) (string, error) {
		errStr := fmt.Sprintf("skill or package %q not found", name)
		if detail != "" {
			errStr = fmt.Sprintf("%s: %s", errStr, detail)
		}
		return jsonResult(map[string]any{
			"error": errStr,
			// LLM-generated JS often does `Skill.run(id).output` without
			// checking .error, so the .output field has to carry the
			// actionable message on its own.
			"output": fmt.Sprintf("'%s' 스킬은 아직 설치되어 있지 않아요. 먼저 Skill.installFromRegistry(\"%s\") 로 설치한 뒤 사용해 주세요.", name, name),
		})
	}
	if s.PackageManager == nil {
		return notFoundResult("")
	}
	pkg, code, err := s.PackageManager.LoadPackage(name)
	if err != nil {
		return notFoundResult(err.Error())
	}

	// Resolve config (secrets, defaults, source bindings).
	config, _ := s.PackageManager.GetConfig(name)

	// Auto-refresh API tokens for source-bound config fields. Fail fast with a
	// clear message if the user is not logged in — silently omitting the token
	// would surface later as an unexplained 401 from the remote API.
	if s.APITokenMgr != nil {
		for _, f := range pkg.Config {
			if f.Key != "access_token" || !strings.HasPrefix(f.Source, "kittypaw-api/") {
				continue
			}
			apiURL := config["api_url"]
			if apiURL == "" {
				apiURL = core.DefaultAPIServerURL
			}
			tok, err := s.APITokenMgr.LoadAccessToken(apiURL)
			if err != nil || tok == "" {
				return jsonResult(map[string]any{
					"error": fmt.Sprintf("skill %q requires API login — run: kittypaw login", name),
				})
			}
			config["access_token"] = tok
		}
	}
	for _, f := range pkg.Config {
		if f.Key != "access_token" {
			continue
		}
		provider, ok := oauthAccessTokenProvider(f.Source)
		if !ok {
			continue
		}
		if s.ServiceTokenMgr == nil {
			return jsonResult(map[string]any{
				"error": fmt.Sprintf("skill %q requires %s connection — run: kittypaw connect %s", name, oauthProviderLabel(provider), provider),
			})
		}
		tok, err := s.ServiceTokenMgr.LoadAccessToken(provider)
		if err != nil || tok == "" {
			return jsonResult(map[string]any{
				"error": fmt.Sprintf("skill %q requires %s connection — run: kittypaw connect %s", name, oauthProviderLabel(provider), provider),
			})
		}
		config["access_token"] = tok
	}

	// Build __context__ JSON string — packages use JSON.parse(__context__).
	// Includes user context based on the package's [permissions].context declaration.
	event := EventFromContext(ctx)
	userCtx := buildUserContext(pkg.Permissions.Context, s, event)
	params = PackageParamsFromContext(ctx)
	ctxObj := map[string]any{"config": config}
	if len(params) > 0 {
		ctxObj["params"] = params
		userCtx = overlayStructuredUserContext(userCtx, params)
	}
	if userCtx != nil {
		ctxObj["user"] = userCtx
	}
	ctxJSON, _ := json.Marshal(ctxObj)
	ctxStr, _ := json.Marshal(string(ctxJSON)) // double-marshal → JS string literal

	// Strip "await" keywords — goja doesn't support async/await, but all skill
	// stubs (Http.get, Llm.generate, etc.) are synchronous in the sandbox.
	syncCode := stripAwait(code)
	wrappedCode := fmt.Sprintf("const __context__ = %s;\n%s", string(ctxStr), syncCode)

	// Extract locale for Llm.generate auto-injection.
	var locale string
	if userCtx != nil {
		if l, ok := userCtx["locale"].(string); ok {
			locale = l
		}
	}
	resolver := buildPackageResolver(ctx, pkg, s, locale)
	result, execErr := s.Sandbox.ExecutePackageOpts(ctx, wrappedCode, map[string]any{}, resolver, s.sandboxOptions())
	if execErr != nil {
		return jsonResult(map[string]any{"error": fmt.Sprintf("package %q execution failed: %v", name, execErr)})
	}
	if !result.Success {
		return jsonResult(map[string]any{"error": result.Error, "output": result.Output})
	}
	output := normalizePackageOutputAttribution(pkg.Meta.ID, result.Output)
	return jsonResult(map[string]any{"success": true, "output": output})
}

func overlayStructuredUserContext(userCtx map[string]any, params map[string]any) map[string]any {
	loc, ok := structuredLocationParam(params)
	if !ok {
		return userCtx
	}
	if userCtx == nil {
		userCtx = map[string]any{}
	}
	userCtx["location"] = loc
	return userCtx
}

func structuredLocationParam(params map[string]any) (map[string]any, bool) {
	if params == nil {
		return nil, false
	}
	raw, ok := params["location"]
	if !ok {
		return nil, false
	}
	loc, ok := raw.(map[string]any)
	if !ok {
		return nil, false
	}
	out := map[string]any{}
	if label, ok := loc["label"].(string); ok && label != "" {
		out["city"] = label
	}
	if city, ok := loc["city"].(string); ok && city != "" {
		out["city"] = city
	}
	if lat, ok := numberParam(loc["lat"]); ok {
		out["lat"] = lat
	}
	if lon, ok := numberParam(loc["lon"]); ok {
		out["lon"] = lon
	}
	return out, len(out) > 0
}

func numberParam(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// buildPackageResolver creates a sandbox.SkillResolver that restricts skill calls
// to the package's declared permissions.primitives. HTTP results are unwrapped
// to return raw body strings — packages expect Http.get to return the response
// body directly, not a {status, body} wrapper.
//
// When locale is non-empty and the package calls Llm.generate, a "Respond in
// {language}" instruction is appended to the prompt automatically. This means
// packages never need to handle locale themselves — they just declare
// context = ["locale"] in package.toml and the engine does the rest.
func buildPackageResolver(_ context.Context, pkg *core.SkillPackage, s *Session, locale string) func(context.Context, core.SkillCall) (string, error) {
	allowed := make(map[string]bool, len(pkg.Permissions.Primitives))
	for _, p := range pkg.Permissions.Primitives {
		allowed[p] = true
	}
	return func(ctx context.Context, call core.SkillCall) (string, error) {
		if !allowed[call.SkillName] {
			return jsonResult(map[string]any{
				"error": fmt.Sprintf("package %q does not have permission for %s", pkg.Meta.ID, call.SkillName),
			})
		}

		// Enforce package-level AllowedHosts for HTTP calls.
		// The package's allowed_hosts overrides the global SSRF check,
		// enabling packages to declare private hosts (e.g. localhost).
		if (call.SkillName == "Http" || call.SkillName == "Web") && len(pkg.Permissions.AllowedHosts) > 0 {
			if len(call.Args) > 0 {
				var u string
				if json.Unmarshal(call.Args[0], &u) == nil {
					if err := validateHTTPTarget(u, pkg.Permissions.AllowedHosts); err != nil {
						return jsonResult(map[string]any{"error": err.Error()})
					}
					// Store the validated hostname so executeHTTP can verify the match.
					if parsed, pErr := url.Parse(u); pErr == nil {
						ctx = context.WithValue(ctx, httpValidatedHostKey, parsed.Hostname())
					}
				}
			}
		}

		// Inject locale instruction for Llm.generate calls.
		if locale != "" && call.SkillName == "Llm" && call.Method == "generate" {
			call = injectLocaleInstruction(call, locale)
		}
		result, err := resolveSkillCall(ctx, call, s, nil)
		if err != nil {
			return result, err
		}
		// Unwrap HTTP responses: packages expect the raw body, not {status, body}.
		if call.SkillName == "Http" || call.SkillName == "Web" {
			result = unwrapHTTPBody(result)
		}
		return result, nil
	}
}

// localeToLanguage maps ISO 639-1 codes to English language names for LLM instruction.
var localeToLanguage = map[string]string{
	"ko": "Korean",
	"ja": "Japanese",
	"zh": "Chinese",
	"en": "English",
	"es": "Spanish",
	"fr": "French",
	"de": "German",
	"pt": "Portuguese",
	"vi": "Vietnamese",
	"th": "Thai",
}

// injectLocaleInstruction appends a "Respond in {language}" instruction to the
// prompt in a Llm.generate call. This lets the engine handle locale transparently
// so packages never need language-specific prompt fragments.
func injectLocaleInstruction(call core.SkillCall, locale string) core.SkillCall {
	if len(call.Args) == 0 || locale == "" || locale == "en" {
		return call // English is the LLM default — no injection needed
	}
	lang, ok := localeToLanguage[locale]
	if !ok {
		lang = locale // pass the raw code; LLMs understand "ko", "ja", etc.
	}
	var prompt string
	if json.Unmarshal(call.Args[0], &prompt) != nil {
		return call
	}
	prompt += "\n\nRespond in " + lang + "."
	b, _ := json.Marshal(prompt)
	newArgs := make([]json.RawMessage, len(call.Args))
	copy(newArgs, call.Args)
	newArgs[0] = b
	call.Args = newArgs
	return call
}

// unwrapHTTPBody extracts the body field from an HTTP result JSON string.
// Input: `{"status":200,"body":"<response body>"}` → Output: `<response body>`
// If the result contains an error or can't be unwrapped, it's returned as-is.
func unwrapHTTPBody(jsonStr string) string {
	var wrapper struct {
		Body  string `json:"body"`
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(jsonStr), &wrapper) != nil {
		return jsonStr
	}
	if wrapper.Error != "" {
		return jsonStr // keep error wrapper so packages can detect failures
	}
	return wrapper.Body
}

// --- Staff Management ---

func executeStaff(ctx context.Context, call core.SkillCall, s *Session) (string, error) {
	switch call.Method {
	case "list":
		base, err := core.ResolveBaseDir(s.BaseDir)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		staff, err := core.ListStaffRecords(base)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"staff": staff})

	case "switch":
		if len(call.Args) == 0 {
			return jsonResult(map[string]any{"error": "staff id required"})
		}
		var id string
		if err := json.Unmarshal(call.Args[0], &id); err != nil {
			return jsonResult(map[string]any{"error": "invalid staff id argument"})
		}
		canonicalID, err := setConversationStaff(s.BaseDir, s.Store, id)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"success": true, "staff": canonicalID})

	case "create":
		if len(call.Args) < 2 {
			return jsonResult(map[string]any{"error": "id and description required"})
		}
		var id, desc string
		if err := json.Unmarshal(call.Args[0], &id); err != nil {
			return jsonResult(map[string]any{"error": "invalid id argument"})
		}
		if err := json.Unmarshal(call.Args[1], &desc); err != nil {
			return jsonResult(map[string]any{"error": "invalid description argument"})
		}
		if err := core.ValidateStaffID(id); err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		base, err := core.ResolveBaseDir(s.BaseDir)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		if core.StaffHasSoul(base, id) {
			return jsonResult(map[string]any{"error": fmt.Sprintf("staff %q already exists", id)})
		}
		draft := buildStaffDraft(id, "runner")
		draft.ID = id
		draft.DisplayName = id
		draft.Description = strings.TrimSpace(desc)
		draft.Aliases = staffAliases(desc, draft.DisplayName, draft.ID)
		draft.Soul = staffSoulDraft(draft)
		conversationID := ConversationIDFromContext(ctx)
		if conversationID == "" {
			conversationID = "default"
		}
		if err := savePendingStaffDraft(s.BaseDir, conversationID, draft); err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{
			"success":           false,
			"requires_approval": true,
			"draft":             draft,
			"output":            formatStaffDraftPreview(draft),
		})

	case "update":
		if len(call.Args) < 2 {
			return jsonResult(map[string]any{"error": "id and description required"})
		}
		var id, desc string
		if err := json.Unmarshal(call.Args[0], &id); err != nil {
			return jsonResult(map[string]any{"error": "invalid id argument"})
		}
		if err := json.Unmarshal(call.Args[1], &desc); err != nil {
			return jsonResult(map[string]any{"error": "invalid description argument"})
		}
		base, err := core.ResolveBaseDir(s.BaseDir)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		canonicalID, ok, err := core.ResolveStaffReference(base, id)
		if err != nil {
			return jsonResult(map[string]any{"error": "staff lookup error: " + err.Error()})
		}
		if !ok {
			return jsonResult(map[string]any{"error": fmt.Sprintf("staff %q not found", id)})
		}
		meta, err := core.ReadStaffMetaFile(base, canonicalID)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		meta.Description = strings.TrimSpace(desc)
		if err := core.WriteStaffMetaFile(base, meta); err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"success": true, "staff": canonicalID})

	default:
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown Staff method: %s", call.Method)})
	}
}

// --- Stubs for complex skills ---

func executeTTS(_ context.Context, _ core.SkillCall, _ *Session) (string, error) {
	// TODO: implement TTS via external API
	return jsonResult(map[string]any{"error": "TTS not yet implemented"})
}

func oauthAccessTokenProvider(source string) (string, bool) {
	ns, key, ok := strings.Cut(source, "/")
	if !ok || key != "access_token" || !strings.HasPrefix(ns, "oauth-") {
		return "", false
	}
	provider := strings.TrimPrefix(ns, "oauth-")
	switch provider {
	case "gmail", "x":
		return provider, true
	default:
		return "", false
	}
}

func oauthProviderLabel(provider string) string {
	switch provider {
	case "gmail":
		return "Gmail"
	case "x":
		return "X"
	default:
		return provider
	}
}

// executeImage and executeVision are in vision.go.

func executeMCP(ctx context.Context, call core.SkillCall, s *Session) (string, error) {
	if s.McpRegistry == nil {
		return jsonResult(map[string]any{"error": "MCP not configured"})
	}
	switch call.Method {
	case "call":
		if len(call.Args) < 3 {
			return jsonResult(map[string]any{"error": "Mcp.call requires (server, tool, args)"})
		}
		var server, tool string
		if err := json.Unmarshal(call.Args[0], &server); err != nil {
			return jsonResult(map[string]any{"error": "invalid server argument"})
		}
		if err := json.Unmarshal(call.Args[1], &tool); err != nil {
			return jsonResult(map[string]any{"error": "invalid tool argument"})
		}
		var args any
		if err := json.Unmarshal(call.Args[2], &args); err != nil {
			return jsonResult(map[string]any{"error": "invalid args argument"})
		}
		return s.McpRegistry.CallTool(ctx, server, tool, args)
	case "listTools":
		if len(call.Args) < 1 {
			return jsonResult(map[string]any{"error": "Mcp.listTools requires (server)"})
		}
		var server string
		if err := json.Unmarshal(call.Args[0], &server); err != nil {
			return jsonResult(map[string]any{"error": "invalid server argument"})
		}
		tools, err := s.McpRegistry.ListTools(ctx, server)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{"tools": tools})
	default:
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown Mcp method: %s", call.Method)})
	}
}

func executeRunner(ctx context.Context, call core.SkillCall, s *Session) (string, error) {
	switch call.Method {
	case "delegate":
		// Runner.delegate(staffId, task, background)
		if len(call.Args) < 2 {
			return jsonResult(map[string]any{"error": "Runner.delegate requires (staffId, task)"})
		}
		var staffID, task string
		if err := json.Unmarshal(call.Args[0], &staffID); err != nil {
			return jsonResult(map[string]any{"error": "invalid staffId argument"})
		}
		if err := json.Unmarshal(call.Args[1], &task); err != nil {
			return jsonResult(map[string]any{"error": "invalid task argument"})
		}
		var background bool
		if len(call.Args) > 2 {
			_ = json.Unmarshal(call.Args[2], &background)
		}

		if len(task) > maxDelegateTaskLen {
			return jsonResult(map[string]any{
				"error":   fmt.Sprintf("task too long (%d > %d chars)", len(task), maxDelegateTaskLen),
				"success": false,
			})
		}

		// Execute delegation.
		spec := PMTaskSpec{StaffID: staffID, Task: task, Background: background}
		maxDepth := 3
		if s.Config.Orchestration.MaxDepth > 0 {
			maxDepth = int(s.Config.Orchestration.MaxDepth)
		}

		result := executeDelegateTask(ctx, spec, s.Provider, s.Store, nil, 1, maxDepth, s.BaseDir)
		return jsonResult(map[string]any{
			"result":      result.Result,
			"success":     result.Success,
			"token_usage": result.TokenUsage,
		})

	default:
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown Runner method: %s", call.Method)})
	}
}

// --- Helpers ---

// stripAwait removes "await " from JavaScript source so it can run in goja's
// synchronous VM. All skill stubs (Http.get, Storage.set, etc.) are already
// synchronous — "await" is only present because packages target QuickJS/Node
// which require it for promise-based APIs.
func stripAwait(code string) string {
	return strings.ReplaceAll(code, "await ", "")
}

// buildUserContext constructs the __context__.user object based on the fields
// declared in the package's [permissions].context. Only requested fields are
// included; omitted fields remain undefined in JS.
func buildUserContext(requested []string, s *Session, event *core.Event) map[string]any {
	if len(requested) == 0 {
		return nil
	}

	result := make(map[string]any, len(requested))

	for _, field := range requested {
		switch field {
		case "locale":
			if s.Config.User.Locale != "" {
				result["locale"] = s.Config.User.Locale
			} else if event != nil {
				p, _ := event.ParsePayload()
				if loc := detectLocale(p.Text); loc != "" {
					result["locale"] = loc
				}
			}
		case "timezone":
			if s.Config.User.Timezone != "" {
				result["timezone"] = s.Config.User.Timezone
			}
		case "location":
			if s.Config.User.City != "" {
				loc := map[string]any{"city": s.Config.User.City}
				if s.Config.User.Latitude != 0 || s.Config.User.Longitude != 0 {
					loc["lat"] = s.Config.User.Latitude
					loc["lon"] = s.Config.User.Longitude
				}
				result["location"] = loc
			}
		case "channel":
			if event != nil {
				result["channel"] = event.Type.ChannelName()
			}
		case "request_text":
			if event != nil {
				p, _ := event.ParsePayload()
				if p.Text != "" {
					result["request_text"] = p.Text
				}
			}
		case "user_name":
			if event != nil {
				p, _ := event.ParsePayload()
				if p.FromName != "" {
					result["user_name"] = p.FromName
				}
			}
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
}

// detectLocale guesses the locale from message text using Unicode ranges.
// Returns "ko", "ja", "zh", or "en". Used as fallback when config.toml
// doesn't set a locale.
//
// Strategy: Hangul and Hiragana/Katakana are script-unique (only Korean/Japanese),
// so they take priority. CJK Unified Ideographs are shared across Chinese and
// Japanese, so they only indicate "zh" when no Japanese kana is present.
func detectLocale(text string) string {
	hasCJK := false
	for _, r := range text {
		if r >= 0xAC00 && r <= 0xD7AF { // Hangul syllables — uniquely Korean
			return "ko"
		}
		if (r >= 0x3040 && r <= 0x309F) || (r >= 0x30A0 && r <= 0x30FF) { // Hiragana/Katakana — uniquely Japanese
			return "ja"
		}
		if r >= 0x4E00 && r <= 0x9FFF { // CJK Unified Ideographs — Chinese or Japanese kanji
			hasCJK = true
		}
	}
	if hasCJK {
		return "zh"
	}
	return "en"
}

func jsonResult(v any) (string, error) {
	if v == nil {
		return "null", nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return "{}", err
	}
	return string(data), nil
}

// isPathAllowed resolves both the target path and the allowed paths, then
// checks containment. Used by tests and callers with raw (unresolved) paths.
// The production hot path in executeFile uses isPathAllowedResolved with
// pre-resolved paths from the Session cache.
func isPathAllowed(path string, allowedPaths []string) bool {
	if len(allowedPaths) == 0 {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	resolved := resolveForValidation(absPath)
	resolvedAllowed := make([]string, 0, len(allowedPaths))
	for _, a := range allowedPaths {
		abs, err := filepath.Abs(a)
		if err != nil {
			continue
		}
		resolvedAllowed = append(resolvedAllowed, resolveForValidation(abs))
	}
	return isPathAllowedResolved(resolved, resolvedAllowed)
}

// isPathAllowedResolved checks an already-resolved absolute path against the
// allowed paths list. The allowed paths are expected to be pre-resolved
// (stored that way by RefreshAllowedPaths).
func isPathAllowedResolved(resolvedPath string, allowedPaths []string) bool {
	if len(allowedPaths) == 0 {
		return false
	}
	for _, allowed := range allowedPaths {
		if resolvedPath == allowed || strings.HasPrefix(resolvedPath, allowed+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// resolveForValidation resolves symlinks for path validation. When the file
// doesn't exist (e.g., write to new file), it walks up to find the deepest
// existing ancestor, resolves that, and re-appends the remaining path segments.
// This prevents symlink-in-parent-dir attacks for non-existent target files.
func resolveForValidation(absPath string) string {
	// Fast path: file/dir exists — resolve directly.
	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		return resolved
	}
	// Walk up to find the deepest existing ancestor.
	current := absPath
	var trail []string
	for {
		parent := filepath.Dir(current)
		trail = append(trail, filepath.Base(current))
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			// Reconstruct: resolved ancestor + unresolved tail segments.
			for i := len(trail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, trail[i])
			}
			return resolved
		}
		if parent == current {
			break // reached filesystem root
		}
		current = parent
	}
	return absPath
}

func stripHTMLTags(s string) string {
	var result strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			result.WriteRune(r)
		}
	}
	return result.String()
}

func sendTelegramMessage(ctx context.Context, token, text string) (string, error) {
	// This needs a chat_id — get it from the event context
	// For now, broadcast to admin chat IDs would need config
	// The actual chat_id comes from the event being processed
	_ = ctx
	_ = token

	// Chunked sending for long messages (Telegram 4096 char limit)
	const maxLen = 4096
	if len(text) <= maxLen {
		return jsonResult(map[string]any{"success": true, "message": text})
	}

	chunks := core.SplitChunks(text, maxLen)
	return jsonResult(map[string]any{"success": true, "chunks": len(chunks)})
}
