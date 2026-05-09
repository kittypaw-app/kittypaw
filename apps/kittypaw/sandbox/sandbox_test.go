package sandbox

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
)

func TestExecuteSimpleReturn(t *testing.T) {
	sb := New(core.SandboxConfig{TimeoutSecs: 5})
	result, err := sb.Execute(context.Background(), `return 1 + 2;`, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if result.Output != "3" {
		t.Errorf("expected output %q, got %q", "3", result.Output)
	}
}

func TestExecuteConsoleLog(t *testing.T) {

	sb := New(core.SandboxConfig{TimeoutSecs: 5})
	result, err := sb.Execute(context.Background(), `console.log("hello world");`, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "hello world") {
		t.Errorf("expected output to contain %q, got %q", "hello world", result.Output)
	}
}

func TestExecuteSkillCall(t *testing.T) {

	sb := New(core.SandboxConfig{TimeoutSecs: 5})
	code := `
		Http.get("https://example.com");
		Storage.set("key", "value");
		return "done";
	`
	result, err := sb.Execute(context.Background(), code, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if len(result.SkillCalls) != 2 {
		t.Fatalf("expected 2 skill calls, got %d", len(result.SkillCalls))
	}

	sc0 := result.SkillCalls[0]
	if sc0.SkillName != "Http" || sc0.Method != "get" {
		t.Errorf("call 0: expected Http.get, got %s.%s", sc0.SkillName, sc0.Method)
	}
	if len(sc0.Args) != 1 || string(sc0.Args[0]) != `"https://example.com"` {
		t.Errorf("call 0: unexpected args %v", sc0.Args)
	}

	sc1 := result.SkillCalls[1]
	if sc1.SkillName != "Storage" || sc1.Method != "set" {
		t.Errorf("call 1: expected Storage.set, got %s.%s", sc1.SkillName, sc1.Method)
	}
}

func TestExecuteProjectsSkillCall(t *testing.T) {
	sb := New(core.SandboxConfig{TimeoutSecs: 5})
	code := `
		Projects.showTicket("KITTY-001");
		Projects.moveTicket("KITTY-001", {status: "doing", message: "starting"});
		return "ok";
	`
	result, err := sb.Execute(context.Background(), code, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if len(result.SkillCalls) != 2 {
		t.Fatalf("expected 2 skill calls, got %d", len(result.SkillCalls))
	}
	if result.SkillCalls[0].SkillName != "Projects" || result.SkillCalls[0].Method != "showTicket" {
		t.Fatalf("call 0 = %+v", result.SkillCalls[0])
	}
	if result.SkillCalls[1].SkillName != "Projects" || result.SkillCalls[1].Method != "moveTicket" {
		t.Fatalf("call 1 = %+v", result.SkillCalls[1])
	}
}

func TestExecuteWithContext(t *testing.T) {

	sb := New(core.SandboxConfig{TimeoutSecs: 5})
	jsCtx := map[string]any{"user": "alice", "count": 42}
	code := `return context.user + ":" + context.count;`
	result, err := sb.Execute(context.Background(), code, jsCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if result.Output != "alice:42" {
		t.Errorf("expected %q, got %q", "alice:42", result.Output)
	}
}

func TestExecuteError(t *testing.T) {

	sb := New(core.SandboxConfig{TimeoutSecs: 5})
	result, err := sb.Execute(context.Background(), `throw new Error("boom");`, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(result.Error, "boom") {
		t.Errorf("expected error to contain %q, got %q", "boom", result.Error)
	}
}

func TestExecuteWithResolver(t *testing.T) {

	sb := New(core.SandboxConfig{TimeoutSecs: 5})
	var resolved []core.SkillCall

	resolver := func(ctx context.Context, call core.SkillCall) (string, error) {
		resolved = append(resolved, call)
		return `{"ok":true}`, nil
	}

	code := `
		Telegram.send("chat123", "hello");
		Shell.exec("ls -la");
	`
	result, err := sb.ExecuteWithResolver(context.Background(), code, nil, resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if len(resolved) != 2 {
		t.Fatalf("expected 2 resolved calls, got %d", len(resolved))
	}
	if resolved[0].SkillName != "Telegram" || resolved[0].Method != "send" {
		t.Errorf("resolved[0]: expected Telegram.send, got %s.%s", resolved[0].SkillName, resolved[0].Method)
	}
	if resolved[1].SkillName != "Shell" || resolved[1].Method != "exec" {
		t.Errorf("resolved[1]: expected Shell.exec, got %s.%s", resolved[1].SkillName, resolved[1].Method)
	}
}

func TestSynchronousResolver(t *testing.T) {

	sb := New(core.SandboxConfig{TimeoutSecs: 5})

	resolver := func(_ context.Context, call core.SkillCall) (string, error) {
		if call.SkillName == "Env" && call.Method == "get" {
			return `{"value":"test-path"}`, nil
		}
		return `null`, nil
	}

	code := `
		const result = Env.get("PATH");
		return result.value;
	`
	result, err := sb.ExecuteWithResolver(context.Background(), code, nil, resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if result.Output != "test-path" {
		t.Errorf("expected %q, got %q", "test-path", result.Output)
	}
}

func TestResolverErrorThrows(t *testing.T) {
	sb := New(core.SandboxConfig{TimeoutSecs: 5})

	resolver := func(_ context.Context, call core.SkillCall) (string, error) {
		return "", fmt.Errorf("path not allowed")
	}

	// JS catches the exception — proves it's a throw, not a null return.
	code := `
		try {
			File.read("/forbidden");
			return "should not reach";
		} catch(e) {
			return "caught:" + e;
		}
	`
	result, err := sb.ExecuteWithResolver(context.Background(), code, nil, resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success (catch should handle), got error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "caught:") || !strings.Contains(result.Output, "path not allowed") {
		t.Errorf("expected caught error, got %q", result.Output)
	}
}

func TestResolverErrorCatchHasMessage(t *testing.T) {
	sb := New(core.SandboxConfig{TimeoutSecs: 5})

	resolver := func(_ context.Context, call core.SkillCall) (string, error) {
		return "", fmt.Errorf("path not allowed")
	}

	code := `
		try {
			File.read("/forbidden");
			return "should not reach";
		} catch(e) {
			return "caught:" + e.message;
		}
	`
	result, err := sb.ExecuteWithResolver(context.Background(), code, nil, resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success (catch should handle), got error: %s", result.Error)
	}
	if result.Output != "caught:path not allowed" {
		t.Errorf("expected caught message, got %q", result.Output)
	}
}

func TestResolverErrorUncaughtFails(t *testing.T) {
	sb := New(core.SandboxConfig{TimeoutSecs: 5})

	resolver := func(_ context.Context, call core.SkillCall) (string, error) {
		return "", fmt.Errorf("access denied")
	}

	// No try/catch — exception should cause result.Success = false.
	code := `
		const data = File.read("/forbidden");
		return "unreachable";
	`
	result, err := sb.ExecuteWithResolver(context.Background(), code, nil, resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Fatal("expected failure from uncaught resolver error")
	}
	if !strings.Contains(result.Error, "access denied") {
		t.Errorf("expected error containing 'access denied', got %q", result.Error)
	}
}

func TestRunnerObserve(t *testing.T) {
	sb := New(core.SandboxConfig{TimeoutSecs: 5})
	code := `
		var data = "search results here";
		Runner.observe({data: data, label: "search"});
		return "should not reach";
	`
	result, err := sb.ExecuteWithResolver(context.Background(), code, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Observe {
		t.Fatal("expected Observe = true")
	}
	if len(result.Observations) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(result.Observations))
	}
	obs := result.Observations[0]
	if obs.Label != "search" {
		t.Errorf("label = %q, want %q", obs.Label, "search")
	}
	if obs.Data != "search results here" {
		t.Errorf("data = %q, want %q", obs.Data, "search results here")
	}
	// Output should contain any console.log before observe, not "should not reach"
	if strings.Contains(result.Output, "should not reach") {
		t.Error("code after Runner.observe should not execute")
	}
}

func TestRunnerObserveTruncation(t *testing.T) {
	sb := New(core.SandboxConfig{TimeoutSecs: 5})
	bigData := strings.Repeat("x", 6000)
	code := fmt.Sprintf(`Runner.observe({data: "%s", label: "big"});`, bigData)
	result, err := sb.ExecuteWithResolver(context.Background(), code, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Observe {
		t.Fatal("expected Observe = true")
	}
	if len(result.Observations[0].Data) != 5000 {
		t.Errorf("expected data truncated to 5000, got %d", len(result.Observations[0].Data))
	}
}

func TestRunnerObserveStringArg(t *testing.T) {
	sb := New(core.SandboxConfig{TimeoutSecs: 5})
	code := `Runner.observe("plain string");`
	result, err := sb.ExecuteWithResolver(context.Background(), code, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Observe {
		t.Fatal("expected Observe = true")
	}
	if result.Observations[0].Data != "plain string" {
		t.Errorf("data = %q, want %q", result.Observations[0].Data, "plain string")
	}
}

func TestNoObserve_ExistingBehaviorUnchanged(t *testing.T) {
	// AC #10: When Runner.observe is not called, behavior is identical to before.
	sb := New(core.SandboxConfig{TimeoutSecs: 5})
	code := `return "hello";`
	result, err := sb.ExecuteWithResolver(context.Background(), code, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Observe {
		t.Error("Observe should be false when not called")
	}
	if result.Observations != nil {
		t.Error("Observations should be nil when not called")
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if result.Output != "hello" {
		t.Errorf("output = %q, want %q", result.Output, "hello")
	}
}

func TestLegacyRunnerAliasRemoved(t *testing.T) {
	sb := New(core.SandboxConfig{TimeoutSecs: 5})
	legacyGlobal := "A" + "gent"
	result, err := sb.ExecuteWithResolver(context.Background(), fmt.Sprintf("return typeof %s;", legacyGlobal), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "undefined" {
		t.Fatalf("legacy runner alias = %q, want undefined", result.Output)
	}
}

func TestAutoReturn(t *testing.T) {
	sb := New(core.SandboxConfig{TimeoutSecs: 5})
	// Code without return — autoReturn should add it.
	result, err := sb.Execute(context.Background(), `"hello from auto-return"`, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if result.Output != "hello from auto-return" {
		t.Errorf("expected %q, got %q", "hello from auto-return", result.Output)
	}
}

func TestExecutePackageRawResults(t *testing.T) {
	sb := New(core.SandboxConfig{TimeoutSecs: 5})

	resolver := func(_ context.Context, call core.SkillCall) (string, error) {
		// Simulates executeHTTP returning {status, body}
		return `{"status":200,"body":"{\"daily\":{\"time\":[\"2026-04-16\"]}}"}`, nil
	}

	// Package code pattern: JSON.parse the raw result, then access body.
	code := `
		var raw = Http.get("https://api.example.com/data");
		var resp = JSON.parse(raw);
		var data = JSON.parse(resp.body);
		return data.daily.time[0];
	`
	result, err := sb.ExecutePackage(context.Background(), code, nil, resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if result.Output != "2026-04-16" {
		t.Errorf("expected %q, got %q", "2026-04-16", result.Output)
	}
}

func TestExecuteWithResolverParsedResults(t *testing.T) {
	sb := New(core.SandboxConfig{TimeoutSecs: 5})

	resolver := func(_ context.Context, call core.SkillCall) (string, error) {
		return `{"status":200,"body":"{\"key\":\"value\"}"}`, nil
	}

	// LLM code pattern: access properties directly on auto-parsed object.
	code := `
		var resp = Http.get("https://api.example.com/data");
		return resp.status + ":" + resp.body;
	`
	result, err := sb.ExecuteWithResolver(context.Background(), code, nil, resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	expected := `200:{"key":"value"}`
	if result.Output != expected {
		t.Errorf("expected %q, got %q", expected, result.Output)
	}
}

// TestFanoutHiddenByDefault locks in the defense-in-depth gate: a personal
// account must see `typeof Fanout === "undefined"`. The engine-level nil check
// would also refuse, but hiding the binding means a buggy skill can't even
// discover the API exists.
func TestFanoutHiddenByDefault(t *testing.T) {
	sb := New(core.SandboxConfig{TimeoutSecs: 5})

	result, err := sb.Execute(context.Background(), `return typeof Fanout;`, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if result.Output != "undefined" {
		t.Errorf("Fanout must be hidden on personal accounts; got typeof=%q", result.Output)
	}
}

// TestFanoutExposedWhenOpted verifies the team-space coordinator path. When the engine
// opts in (Session.Fanout != nil), the global appears with the expected method
// surface. We check for the two methods explicitly — a bare `typeof` would
// also pass if the binding was half-wired.
func TestFanoutExposedWhenOpted(t *testing.T) {
	sb := New(core.SandboxConfig{TimeoutSecs: 5})

	resolver := func(_ context.Context, _ core.SkillCall) (string, error) {
		return `{"success":true}`, nil
	}
	code := `
		if (typeof Fanout !== "object") return "missing:" + typeof Fanout;
		if (typeof Fanout.send !== "function") return "no-send";
		if (typeof Fanout.broadcast !== "function") return "no-broadcast";
		return "ok";
	`
	result, err := sb.ExecuteWithResolverOpts(context.Background(), code, nil, resolver, Options{ExposeFanout: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if result.Output != "ok" {
		t.Errorf("expected ok, got %q", result.Output)
	}
}

// TestExecuteWithResolverOpts_ExposeShareFalse_HidesShareGlobal mirrors the
// Fanout defense-in-depth model for Share. Team-space coordinators do not read from
// members — they're the authoritative source — so the Share global must be
// absent there. A personal account probing `typeof Share` on the team-space
// session should see undefined, not a bound object that errors on call.
func TestExecuteWithResolverOpts_ExposeShareFalse_HidesShareGlobal(t *testing.T) {
	sb := New(core.SandboxConfig{TimeoutSecs: 5})

	resolver := func(_ context.Context, _ core.SkillCall) (string, error) {
		return `{"success":true}`, nil
	}
	result, err := sb.ExecuteWithResolverOpts(context.Background(), `return typeof Share;`, nil, resolver,
		Options{ExposeShare: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if result.Output != "undefined" {
		t.Errorf("Share must be hidden when ExposeShare=false; got typeof=%q", result.Output)
	}
}

// TestExecuteWithResolverOpts_ExposeShareTrue_ShowsShareGlobal is the positive
// counterpart — personal accounts (the Share readers) must get the full API
// surface. We check Share.read is a callable function so a half-wired binding
// wouldn't slip through as "object".
func TestExecuteWithResolverOpts_ExposeShareTrue_ShowsShareGlobal(t *testing.T) {
	sb := New(core.SandboxConfig{TimeoutSecs: 5})

	resolver := func(_ context.Context, _ core.SkillCall) (string, error) {
		return `{"content":"ok"}`, nil
	}
	code := `
		if (typeof Share !== "object") return "missing:" + typeof Share;
		if (typeof Share.read !== "function") return "no-read";
		return "ok";
	`
	result, err := sb.ExecuteWithResolverOpts(context.Background(), code, nil, resolver, Options{ExposeShare: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if result.Output != "ok" {
		t.Errorf("expected ok, got %q", result.Output)
	}
}
