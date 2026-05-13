package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
	"github.com/jinto/kittypaw/store"
)

func TestIsPathAllowed(t *testing.T) {
	tests := []struct {
		path    string
		allowed []string
		want    bool
	}{
		// No allowed paths → deny all
		{"/tmp/file.txt", nil, false},
		{"/tmp/file.txt", []string{}, false},

		// Exact match
		{"/tmp/safe", []string{"/tmp/safe"}, true},

		// Subdirectory
		{"/tmp/safe/file.txt", []string{"/tmp/safe"}, true},
		{"/tmp/safe/sub/deep", []string{"/tmp/safe"}, true},

		// Separator boundary — the critical security fix
		{"/tmp/safe-evil/file.txt", []string{"/tmp/safe"}, false},
		{"/tmp/safefile", []string{"/tmp/safe"}, false},

		// Multiple allowed paths
		{"/home/user/file", []string{"/tmp", "/home/user"}, true},
		{"/etc/passwd", []string{"/tmp", "/home/user"}, false},
	}
	for _, tt := range tests {
		got := isPathAllowed(tt.path, tt.allowed)
		if got != tt.want {
			t.Errorf("isPathAllowed(%q, %v) = %v, want %v", tt.path, tt.allowed, got, tt.want)
		}
	}
}

func TestIsPathAllowedSymlinkParent(t *testing.T) {
	// Create a real directory structure with symlinks to test parent resolution.
	tmpDir := t.TempDir()
	allowedDir := filepath.Join(tmpDir, "allowed")
	outsideDir := filepath.Join(tmpDir, "outside")
	if err := os.MkdirAll(allowedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a symlink inside the allowed dir that points outside.
	symlinkPath := filepath.Join(allowedDir, "escape")
	if err := os.Symlink(outsideDir, symlinkPath); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	allowed := []string{allowedDir}

	// Existing file via symlink — should be denied (resolves to outside).
	existingFile := filepath.Join(outsideDir, "secret.txt")
	os.WriteFile(existingFile, []byte("secret"), 0o644)
	if isPathAllowed(filepath.Join(allowedDir, "escape", "secret.txt"), allowed) {
		t.Error("existing file via symlink to outside should be denied")
	}

	// Non-existent file via symlink — the critical bug fix.
	// Without parent walk, this would be allowed because EvalSymlinks fails on
	// non-existent files, leaving the unresolved path that starts with allowedDir.
	if isPathAllowed(filepath.Join(allowedDir, "escape", "newfile.txt"), allowed) {
		t.Error("non-existent file via parent symlink to outside should be denied")
	}

	// Legitimate file within allowed dir should still work.
	if !isPathAllowed(filepath.Join(allowedDir, "safe.txt"), allowed) {
		t.Error("file directly in allowed dir should be allowed")
	}

	// Non-existent file within allowed dir (no symlinks) should be allowed.
	if !isPathAllowed(filepath.Join(allowedDir, "newfile.txt"), allowed) {
		t.Error("non-existent file in allowed dir should be allowed")
	}

	// Deep nested non-existent file in allowed dir.
	if !isPathAllowed(filepath.Join(allowedDir, "sub", "deep", "file.txt"), allowed) {
		t.Error("deep non-existent file in allowed dir should be allowed")
	}
}

func TestResolveForValidation(t *testing.T) {
	tmpDir := t.TempDir()
	realDir := filepath.Join(tmpDir, "real")
	os.MkdirAll(realDir, 0o755)

	// Resolve the real dir itself (macOS: /var → /private/var).
	resolvedRealDir, _ := filepath.EvalSymlinks(realDir)

	// Symlink: tmpDir/link → tmpDir/real
	linkPath := filepath.Join(tmpDir, "link")
	if err := os.Symlink(realDir, linkPath); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	// Existing file through symlink.
	os.WriteFile(filepath.Join(realDir, "exists.txt"), []byte("hi"), 0o644)
	resolved := resolveForValidation(filepath.Join(linkPath, "exists.txt"))
	expected := filepath.Join(resolvedRealDir, "exists.txt")
	if resolved != expected {
		t.Errorf("existing file: got %q, want %q", resolved, expected)
	}

	// Non-existent file through symlink — should still resolve parent.
	resolved = resolveForValidation(filepath.Join(linkPath, "nofile.txt"))
	expected = filepath.Join(resolvedRealDir, "nofile.txt")
	if resolved != expected {
		t.Errorf("non-existent file: got %q, want %q", resolved, expected)
	}

	// Non-existent deep path through symlink.
	resolved = resolveForValidation(filepath.Join(linkPath, "a", "b", "c.txt"))
	expected = filepath.Join(resolvedRealDir, "a", "b", "c.txt")
	if resolved != expected {
		t.Errorf("deep non-existent: got %q, want %q", resolved, expected)
	}
}

func TestFileSizeLimit(t *testing.T) {
	tmpDir := t.TempDir()
	allowed := []string{tmpDir}

	// Create a file just over the limit.
	bigFile := filepath.Join(tmpDir, "big.bin")
	f, err := os.Create(bigFile)
	if err != nil {
		t.Fatal(err)
	}
	// Write 10MB + 1 byte.
	if err := f.Truncate(maxFileReadSize + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	// Verify the constant is 10MB.
	if maxFileReadSize != 10*1024*1024 {
		t.Fatalf("maxFileReadSize = %d, want 10MB", maxFileReadSize)
	}

	// File within limit should work (we just check isPathAllowed + size gate here).
	smallFile := filepath.Join(tmpDir, "small.txt")
	os.WriteFile(smallFile, []byte("hello"), 0o644)

	// Verify small file is allowed.
	if !isPathAllowed(smallFile, allowed) {
		t.Error("small file should be in allowed path")
	}

	// Verify big file is allowed path-wise (the size limit is in executeFile, not isPathAllowed).
	if !isPathAllowed(bigFile, allowed) {
		t.Error("big file should be in allowed path")
	}
}

func TestValidateHTTPTarget(t *testing.T) {
	tests := []struct {
		url     string
		allowed []string
		wantErr bool
	}{
		// Public URL, no restrictions
		{"https://example.com/api", nil, false},
		{"https://example.com/api", []string{}, false},

		// Private IPs blocked (no allowlist)
		{"http://127.0.0.1:8080/admin", nil, true},
		{"http://localhost/secret", nil, true},
		{"http://10.0.0.1/internal", nil, true},
		{"http://192.168.1.1/router", nil, true},
		{"http://169.254.1.1/metadata", nil, true},

		// AllowedHosts whitelist
		{"https://api.example.com/data", []string{"api.example.com"}, false},
		{"https://evil.com/data", []string{"api.example.com"}, true},

		// Wildcard in allowed hosts
		{"https://anything.com/path", []string{"*"}, false},

		// AllowedHosts permits private IPs when explicitly listed (package use case).
		{"http://localhost:8080/api", []string{"localhost"}, false},
		{"http://127.0.0.1:8080/api", []string{"127.0.0.1"}, false},
		{"http://localhost:8080/api", []string{"*"}, false},

		// AllowedHosts still rejects unlisted hosts.
		{"http://evil.com/api", []string{"localhost"}, true},

		// Invalid URL
		{"://bad", nil, true},
	}
	for _, tt := range tests {
		err := validateHTTPTarget(tt.url, tt.allowed)
		if (err != nil) != tt.wantErr {
			t.Errorf("validateHTTPTarget(%q, %v) error = %v, wantErr %v", tt.url, tt.allowed, err, tt.wantErr)
		}
	}
}

func TestStripHTMLTags(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"<p>hello</p>", "hello"},
		{"no tags", "no tags"},
		{"<b>bold</b> and <i>italic</i>", "bold and italic"},
		{"<a href=\"url\">link</a>", "link"},
		{"", ""},
		{"<>empty tag</>", "empty tag"},
		{"nested <div><span>text</span></div>", "nested text"},
	}
	for _, tt := range tests {
		got := stripHTMLTags(tt.input)
		if got != tt.want {
			t.Errorf("stripHTMLTags(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// extractSearchResults and cleanDuckDuckGoURL tests moved to search_test.go

// ---------------------------------------------------------------------------
// File index dispatch tests
// ---------------------------------------------------------------------------

func TestExecuteFileSearch_Dispatch(t *testing.T) {
	st := openTestStore(t)
	ix := NewFTS5Indexer(st)
	dir := setupTestWorkspace(t)
	ix.Index(context.Background(), "ws-exec", dir)

	s := &Session{Store: st, Indexer: ix}
	// Pre-load allowed paths with workspace dir.
	paths := []string{dir}
	s.allowedPaths.Store(&paths)

	call := core.SkillCall{
		SkillName: "File",
		Method:    "search",
		Args:      []json.RawMessage{json.RawMessage(`"handleSearch"`)},
	}
	result, err := executeFile(context.Background(), call, s)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}

	// Parse result.
	var sr SearchResult
	if err := json.Unmarshal([]byte(result), &sr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if sr.Total < 1 {
		t.Errorf("total: got %d, want >= 1", sr.Total)
	}
}

func TestExecuteFileSearch_NilIndexer(t *testing.T) {
	s := &Session{}
	call := core.SkillCall{
		SkillName: "File",
		Method:    "search",
		Args:      []json.RawMessage{json.RawMessage(`"test"`)},
	}
	_, err := executeFile(context.Background(), call, s)
	if err == nil {
		t.Fatal("expected error for nil indexer")
	}
}

func TestExecuteFileSearch_AllowedPathsFilter(t *testing.T) {
	st := openTestStore(t)
	ix := NewFTS5Indexer(st)
	dir := setupTestWorkspace(t)
	ix.Index(context.Background(), "ws-filter", dir)

	s := &Session{Store: st, Indexer: ix}
	// Set AllowedPaths to a non-matching path — all results should be filtered out.
	paths := []string{"/some/other/path"}
	s.allowedPaths.Store(&paths)

	call := core.SkillCall{
		SkillName: "File",
		Method:    "search",
		Args:      []json.RawMessage{json.RawMessage(`"handleSearch"`)},
	}
	result, err := executeFile(context.Background(), call, s)
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	var sr SearchResult
	json.Unmarshal([]byte(result), &sr)
	if len(sr.Files) != 0 {
		t.Errorf("expected 0 files after filter, got %d", len(sr.Files))
	}
}

func TestExecuteFileStats_Dispatch(t *testing.T) {
	st := openTestStore(t)
	ix := NewFTS5Indexer(st)
	dir := setupTestWorkspace(t)
	ix.Index(context.Background(), "ws-stats-exec", dir)

	s := &Session{Store: st, Indexer: ix}
	call := core.SkillCall{
		SkillName: "File",
		Method:    "stats",
	}
	result, err := executeFile(context.Background(), call, s)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}

	var stats IndexStats
	if err := json.Unmarshal([]byte(result), &stats); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if stats.TotalFiles < 1 {
		t.Errorf("total_files: got %d, want >= 1", stats.TotalFiles)
	}
}

func TestExecuteFileReindex_Dispatch(t *testing.T) {
	st := openTestStore(t)
	ix := NewFTS5Indexer(st)
	dir := setupTestWorkspace(t)
	ix.Index(context.Background(), "ws-reindex-exec", dir)

	// Register workspace in store.
	st.SaveWorkspace(&store.Workspace{ID: "ws-reindex-exec", Name: "test", RootPath: dir})

	s := &Session{Store: st, Indexer: ix}
	call := core.SkillCall{
		SkillName: "File",
		Method:    "reindex",
	}
	result, err := executeFile(context.Background(), call, s)
	if err != nil {
		t.Fatalf("reindex: %v", err)
	}

	var ir IndexResult
	if err := json.Unmarshal([]byte(result), &ir); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ir.Indexed < 1 {
		t.Errorf("indexed: got %d, want >= 1", ir.Indexed)
	}
}

func TestExecuteFileRead_StillWorks(t *testing.T) {
	st := openTestStore(t)
	dir := setupTestWorkspace(t)
	s := &Session{Store: st}
	// Resolve the path to handle macOS /private/var symlink.
	resolvedDir := resolveForValidation(dir)
	paths := []string{resolvedDir}
	s.allowedPaths.Store(&paths)

	call := core.SkillCall{
		SkillName: "File",
		Method:    "read",
		Args:      []json.RawMessage{json.RawMessage(`"` + filepath.Join(dir, "main.go") + `"`)},
	}
	result, err := executeFile(context.Background(), call, s)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if result == "" {
		t.Fatal("expected content from File.read")
	}
}

func TestExecuteFileWrite_RelativePathUsesWorkspaceRoot(t *testing.T) {
	workspaceRoot := resolveForValidation(t.TempDir())
	processCWD := t.TempDir()
	t.Chdir(processCWD)

	s := &Session{}
	paths := []string{workspaceRoot}
	s.allowedPaths.Store(&paths)

	call := core.SkillCall{
		SkillName: "File",
		Method:    "write",
		Args: []json.RawMessage{
			json.RawMessage(`"memo.txt"`),
			json.RawMessage(`"today tired"`),
		},
	}
	_, err := executeFile(context.Background(), call, s)
	if err != nil {
		t.Fatalf("write relative path: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(workspaceRoot, "memo.txt"))
	if err != nil {
		t.Fatalf("read workspace memo: %v", err)
	}
	if string(got) != "today tired" {
		t.Fatalf("workspace memo content = %q, want %q", string(got), "today tired")
	}
	if _, err := os.Stat(filepath.Join(processCWD, "memo.txt")); !os.IsNotExist(err) {
		t.Fatalf("process cwd memo stat error = %v, want not exist", err)
	}
}

func TestExecuteFileEdit_ReplacesExactlyOneOccurrence(t *testing.T) {
	workspaceRoot := resolveForValidation(t.TempDir())
	path := filepath.Join(workspaceRoot, "memo.txt")
	if err := os.WriteFile(path, []byte("alpha old omega\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &Session{}
	paths := []string{workspaceRoot}
	s.allowedPaths.Store(&paths)

	result, err := executeFile(context.Background(), core.SkillCall{
		SkillName: "File",
		Method:    "edit",
		Args: []json.RawMessage{
			json.RawMessage(`"memo.txt"`),
			json.RawMessage(`"old"`),
			json.RawMessage(`"new"`),
		},
	}, s)
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if !strings.Contains(result, `"success":true`) || !strings.Contains(result, `"replacements":1`) {
		t.Fatalf("unexpected edit result: %s", result)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "alpha new omega\n" {
		t.Fatalf("file content = %q, want guarded replacement", string(got))
	}
}

func TestExecuteFileEdit_FailsWithoutChangingMissingOrAmbiguousOldText(t *testing.T) {
	workspaceRoot := resolveForValidation(t.TempDir())
	path := filepath.Join(workspaceRoot, "memo.txt")
	original := "alpha old beta old gamma\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &Session{}
	paths := []string{workspaceRoot}
	s.allowedPaths.Store(&paths)

	for _, tt := range []struct {
		name    string
		oldText string
		wantErr string
	}{
		{name: "missing", oldText: "absent", wantErr: "old_text not found"},
		{name: "ambiguous", oldText: "old", wantErr: "old_text matched 2 times"},
		{name: "empty", oldText: "", wantErr: "old_text must not be empty"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result, err := executeFile(context.Background(), core.SkillCall{
				SkillName: "File",
				Method:    "edit",
				Args: []json.RawMessage{
					json.RawMessage(`"memo.txt"`),
					json.RawMessage(`"` + tt.oldText + `"`),
					json.RawMessage(`"new"`),
				},
			}, s)
			if err != nil {
				t.Fatalf("edit: %v", err)
			}
			if !strings.Contains(result, `"success":false`) || !strings.Contains(result, tt.wantErr) {
				t.Fatalf("result = %s, want error containing %q", result, tt.wantErr)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != original {
				t.Fatalf("file changed on failed edit: %q", string(got))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// needsPermission tests
// ---------------------------------------------------------------------------

func TestNeedsPermission(t *testing.T) {
	tests := []struct {
		name     string
		skill    string
		method   string
		autonomy core.AutonomyLevel
		custom   []string // nil = use defaults
		want     bool
	}{
		// AutonomyFull never needs permission
		{"full_shell", "Shell", "exec", core.AutonomyFull, nil, false},
		{"full_git_push", "Git", "push", core.AutonomyFull, nil, false},

		// AutonomyReadonly never needs permission (blocked elsewhere)
		{"readonly_shell", "Shell", "exec", core.AutonomyReadonly, nil, false},

		// Supervised with default list
		{"supervised_shell_exec", "Shell", "exec", core.AutonomySupervised, nil, true},
		{"supervised_git_commit", "Git", "commit", core.AutonomySupervised, nil, true},
		{"supervised_git_push", "Git", "push", core.AutonomySupervised, nil, true},
		{"supervised_git_pull", "Git", "pull", core.AutonomySupervised, nil, true},
		{"supervised_file_delete", "File", "delete", core.AutonomySupervised, nil, true},
		{"supervised_file_write", "File", "write", core.AutonomySupervised, nil, true},
		{"supervised_file_append", "File", "append", core.AutonomySupervised, nil, true},
		{"supervised_file_mkdir", "File", "mkdir", core.AutonomySupervised, nil, true},
		{"supervised_file_edit", "File", "edit", core.AutonomySupervised, nil, true},
		{"supervised_browser_open", "Browser", "open", core.AutonomySupervised, nil, true},
		{"supervised_browser_evaluate", "Browser", "evaluate", core.AutonomySupervised, nil, true},
		{"supervised_skill_uninstall", "Skill", "uninstall", core.AutonomySupervised, nil, true},

		// Non-destructive ops not in default list
		{"supervised_git_status", "Git", "status", core.AutonomySupervised, nil, false},
		{"supervised_git_log", "Git", "log", core.AutonomySupervised, nil, false},
		{"supervised_git_diff", "Git", "diff", core.AutonomySupervised, nil, false},
		{"supervised_file_read", "File", "read", core.AutonomySupervised, nil, false},
		{"supervised_http_get", "Http", "get", core.AutonomySupervised, nil, false},
		{"supervised_browser_snapshot", "Browser", "snapshot", core.AutonomySupervised, nil, false},

		// Custom list overrides defaults
		{"custom_file_write", "File", "write", core.AutonomySupervised, []string{"File.write"}, true},
		{"custom_shell_not_listed", "Shell", "exec", core.AutonomySupervised, []string{"File.write"}, false},

		// Empty custom list = nothing needs permission
		{"empty_list", "Shell", "exec", core.AutonomySupervised, []string{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &core.Config{
				AutonomyLevel: tt.autonomy,
				Permissions:   core.PermissionPolicy{RequireApproval: tt.custom},
			}
			got := needsPermission(tt.skill, tt.method, cfg)
			if got != tt.want {
				t.Errorf("needsPermission(%q, %q) = %v, want %v", tt.skill, tt.method, got, tt.want)
			}
		})
	}
}

func TestResolveSkillCallPackageUninstall(t *testing.T) {
	baseDir := t.TempDir()
	pkgDir := filepath.Join(baseDir, "packages", "weather-now")
	if err := os.MkdirAll(pkgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	s := &Session{
		BaseDir:        baseDir,
		Config:         &core.Config{AutonomyLevel: core.AutonomyFull},
		PackageManager: core.NewPackageManagerFrom(baseDir, nil),
	}
	name := json.RawMessage(`"weather-now"`)
	got, err := resolveSkillCall(context.Background(), core.SkillCall{
		SkillName: "Skill",
		Method:    "uninstall",
		Args:      []json.RawMessage{name},
	}, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"success":true`) || !strings.Contains(got, `"kind":"package"`) {
		t.Fatalf("unexpected uninstall result: %s", got)
	}
	if _, err := os.Stat(pkgDir); !os.IsNotExist(err) {
		t.Fatalf("package dir still exists or stat failed unexpectedly: %v", err)
	}
}

func TestResolveSkillCallPackageUninstallByDisplayName(t *testing.T) {
	baseDir := t.TempDir()
	pm := installTestPackage(t, baseDir, `[meta]
id = "weather-now"
name = "날씨 조회"
version = "1.0.0"
description = "현재 날씨와 비 여부를 확인합니다."
`, `return "ok";`)
	s := &Session{
		BaseDir:        baseDir,
		Config:         &core.Config{AutonomyLevel: core.AutonomyFull},
		PackageManager: pm,
	}

	name := json.RawMessage(`"날씨 조회"`)
	got, err := resolveSkillCall(context.Background(), core.SkillCall{
		SkillName: "Skill",
		Method:    "uninstall",
		Args:      []json.RawMessage{name},
	}, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"success":true`) || !strings.Contains(got, `"name":"weather-now"`) {
		t.Fatalf("unexpected uninstall result: %s", got)
	}
	if _, _, err := pm.LoadPackage("weather-now"); err == nil {
		t.Fatal("weather-now package still installed")
	}
}

func TestResolveSkillCallCreateOnceDelayStoresRunAt(t *testing.T) {
	baseDir := t.TempDir()
	s := &Session{
		BaseDir: baseDir,
		Config:  &core.Config{AutonomyLevel: core.AutonomyFull},
	}
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	before := time.Now().UTC().Add(90 * time.Second)

	got, err := resolveSkillCall(context.Background(), core.SkillCall{
		SkillName: "Skill",
		Method:    "create",
		Args: []json.RawMessage{
			raw(`"remind"`),
			raw(`"2분 뒤 알림"`),
			raw(`"return \"ok\";"`),
			raw(`"once"`),
			raw(`"2m"`),
		},
	}, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"success":true`) {
		t.Fatalf("Skill.create result = %s", got)
	}

	skill, _, err := core.LoadSkillFrom(baseDir, "remind")
	if err != nil {
		t.Fatalf("LoadSkillFrom(remind): %v", err)
	}
	if skill.Trigger.Type != "once" {
		t.Fatalf("trigger type = %q, want once", skill.Trigger.Type)
	}
	if skill.Trigger.Cron != "" {
		t.Fatalf("once trigger cron = %q, want empty", skill.Trigger.Cron)
	}
	runAt, err := time.Parse(time.RFC3339, skill.Trigger.RunAt)
	if err != nil {
		t.Fatalf("run_at = %q, want RFC3339: %v", skill.Trigger.RunAt, err)
	}
	after := time.Now().UTC().Add(150 * time.Second)
	if runAt.Before(before) || runAt.After(after) {
		t.Fatalf("run_at = %s, want roughly 2m from now (%s..%s)", runAt, before, after)
	}

	sched := NewScheduler(&Session{Store: openTestStore(t), Config: &core.Config{}}, nil)
	if sched.isDue(skill) {
		t.Fatal("new delayed once skill should not be due before run_at")
	}
}

func TestResolveSkillCallPermissionGate(t *testing.T) {
	st := openTestStore(t)
	s := &Session{
		Store: st,
		Config: &core.Config{
			AutonomyLevel: core.AutonomySupervised,
		},
	}

	call := core.SkillCall{
		SkillName: "Shell",
		Method:    "exec",
		Args:      []json.RawMessage{json.RawMessage(`"echo hello"`)},
	}

	// No permFn → should be denied
	result, err := resolveSkillCall(context.Background(), call, s, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "requires permission approval") {
		t.Errorf("expected permission denied, got: %s", result)
	}
}

func TestResolveSkillCallPermissionApproved(t *testing.T) {
	st := openTestStore(t)
	s := &Session{
		Store: st,
		Config: &core.Config{
			AutonomyLevel: core.AutonomySupervised,
		},
	}

	call := core.SkillCall{
		SkillName: "Shell",
		Method:    "exec",
		Args:      []json.RawMessage{json.RawMessage(`"echo hello"`)},
	}

	// Approving permFn → should execute
	approveFn := func(ctx context.Context, desc, res string) (bool, error) {
		return true, nil
	}

	result, err := resolveSkillCall(context.Background(), call, s, approveFn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "permission denied") {
		t.Errorf("expected success, got: %s", result)
	}
	// Should contain actual output
	if !strings.Contains(result, "hello") {
		t.Errorf("expected 'hello' in output, got: %s", result)
	}
}

func TestResolveSkillCallPermissionDenied(t *testing.T) {
	st := openTestStore(t)
	s := &Session{
		Store: st,
		Config: &core.Config{
			AutonomyLevel: core.AutonomySupervised,
		},
	}

	call := core.SkillCall{
		SkillName: "Shell",
		Method:    "exec",
		Args:      []json.RawMessage{json.RawMessage(`"echo hello"`)},
	}

	// Denying permFn → should be denied
	denyFn := func(ctx context.Context, desc, res string) (bool, error) {
		return false, nil
	}

	result, err := resolveSkillCall(context.Background(), call, s, denyFn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "permission denied") {
		t.Errorf("expected permission denied, got: %s", result)
	}
}

func TestResolveSkillCallFullAutonomy(t *testing.T) {
	st := openTestStore(t)
	s := &Session{
		Store: st,
		Config: &core.Config{
			AutonomyLevel: core.AutonomyFull,
		},
	}

	call := core.SkillCall{
		SkillName: "Shell",
		Method:    "exec",
		Args:      []json.RawMessage{json.RawMessage(`"echo full"`)},
	}

	// AutonomyFull: should execute without any permFn
	result, err := resolveSkillCall(context.Background(), call, s, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "full") {
		t.Errorf("expected 'full' in output, got: %s", result)
	}
}

func TestResolveSkillCallCustomPermissionList(t *testing.T) {
	st := openTestStore(t)
	s := &Session{
		Store: st,
		Config: &core.Config{
			AutonomyLevel: core.AutonomySupervised,
			Permissions: core.PermissionPolicy{
				RequireApproval: []string{"File.write"}, // Shell.exec NOT listed
			},
		},
	}

	// Shell.exec NOT in custom list → should execute without permFn
	shellCall := core.SkillCall{
		SkillName: "Shell",
		Method:    "exec",
		Args:      []json.RawMessage{json.RawMessage(`"echo custom"`)},
	}
	result, err := resolveSkillCall(context.Background(), shellCall, s, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "custom") {
		t.Errorf("expected 'custom' in output, got: %s", result)
	}
}

type fakeBrowserController struct {
	calls []core.SkillCall
}

func (f *fakeBrowserController) Execute(_ context.Context, call core.SkillCall) (string, error) {
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

func TestResolveSkillCallGitNonDestructive(t *testing.T) {
	st := openTestStore(t)
	s := &Session{
		Store: st,
		Config: &core.Config{
			AutonomyLevel: core.AutonomySupervised,
		},
	}

	// Git.status is not in the default require_approval list
	call := core.SkillCall{
		SkillName: "Git",
		Method:    "status",
	}

	// Should work without permFn since Git.status is not protected
	result, err := resolveSkillCall(context.Background(), call, s, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should not be blocked by permission gate
	if strings.Contains(result, "permission denied") || strings.Contains(result, "requires permission approval") {
		t.Errorf("Git.status should not require permission, got: %s", result)
	}
}

func TestDetectLocale(t *testing.T) {
	tests := []struct {
		text string
		want string
	}{
		{"오늘 날씨 알려줘", "ko"},
		{"서울 날씨", "ko"},
		{"What's the weather?", "en"},
		{"今日の天気", "ja"},
		{"今天天气怎么样", "zh"},
		{"", "en"},
		{"Hello 안녕", "ko"}, // first CJK char wins
	}
	for _, tt := range tests {
		got := detectLocale(tt.text)
		if got != tt.want {
			t.Errorf("detectLocale(%q) = %q, want %q", tt.text, got, tt.want)
		}
	}
}

func TestBuildUserContext(t *testing.T) {
	cfg := &core.Config{}
	cfg.User.Locale = "ko"
	cfg.User.Timezone = "Asia/Seoul"
	cfg.User.City = "Seoul"
	cfg.User.Latitude = 37.57
	cfg.User.Longitude = 126.98

	sess := &Session{Config: cfg}

	payload, _ := json.Marshal(core.ChatPayload{
		Text:     "오늘 날씨 알려줘",
		FromName: "제권",
	})
	event := &core.Event{
		Type:    core.EventTelegram,
		Payload: payload,
	}

	t.Run("all fields declared", func(t *testing.T) {
		requested := []string{"locale", "timezone", "location", "channel", "request_text", "user_name"}
		result := buildUserContext(requested, sess, event)

		if result["locale"] != "ko" {
			t.Errorf("locale = %v, want ko", result["locale"])
		}
		if result["timezone"] != "Asia/Seoul" {
			t.Errorf("timezone = %v, want Asia/Seoul", result["timezone"])
		}
		loc := result["location"].(map[string]any)
		if loc["city"] != "Seoul" {
			t.Errorf("location.city = %v, want Seoul", loc["city"])
		}
		if result["channel"] != "telegram" {
			t.Errorf("channel = %v, want telegram", result["channel"])
		}
		if result["request_text"] != "오늘 날씨 알려줘" {
			t.Errorf("request_text = %v", result["request_text"])
		}
		if result["user_name"] != "제권" {
			t.Errorf("user_name = %v", result["user_name"])
		}
	})

	t.Run("only locale declared", func(t *testing.T) {
		result := buildUserContext([]string{"locale"}, sess, event)
		if result["locale"] != "ko" {
			t.Errorf("locale = %v, want ko", result["locale"])
		}
		if _, ok := result["timezone"]; ok {
			t.Error("timezone should not be present")
		}
		if _, ok := result["location"]; ok {
			t.Error("location should not be present")
		}
	})

	t.Run("no declaration = nil", func(t *testing.T) {
		result := buildUserContext(nil, sess, event)
		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})

	t.Run("nil event = scheduler path", func(t *testing.T) {
		requested := []string{"locale", "channel", "request_text", "user_name"}
		result := buildUserContext(requested, sess, nil)

		if result["locale"] != "ko" {
			t.Errorf("locale = %v, want ko (from config)", result["locale"])
		}
		if _, ok := result["channel"]; ok {
			t.Error("channel should not be present without event")
		}
		if _, ok := result["request_text"]; ok {
			t.Error("request_text should not be present without event")
		}
	})

	t.Run("locale fallback to detection", func(t *testing.T) {
		noLocaleCfg := &core.Config{}
		noLocaleSess := &Session{Config: noLocaleCfg}
		result := buildUserContext([]string{"locale"}, noLocaleSess, event)
		if result["locale"] != "ko" {
			t.Errorf("locale = %v, want ko (detected from Korean text)", result["locale"])
		}
	})

	t.Run("config locale beats detection", func(t *testing.T) {
		// AC #3: event text is Korean but config says "en" — config must win.
		enCfg := &core.Config{}
		enCfg.User.Locale = "en"
		enSess := &Session{Config: enCfg}
		result := buildUserContext([]string{"locale"}, enSess, event)
		if result["locale"] != "en" {
			t.Errorf("locale = %v, want en (config must beat detection)", result["locale"])
		}
	})
}

func TestInjectLocaleInstruction(t *testing.T) {
	makeCall := func(prompt string) core.SkillCall {
		b, _ := json.Marshal(prompt)
		return core.SkillCall{
			SkillName: "Llm",
			Method:    "generate",
			Args:      []json.RawMessage{b},
		}
	}
	extractPrompt := func(call core.SkillCall) string {
		var p string
		json.Unmarshal(call.Args[0], &p)
		return p
	}

	t.Run("Korean locale appends instruction", func(t *testing.T) {
		call := makeCall("Tell me about the weather.")
		result := injectLocaleInstruction(call, "ko")
		prompt := extractPrompt(result)
		if !strings.HasSuffix(prompt, "\n\nRespond in Korean.") {
			t.Errorf("got %q, want suffix 'Respond in Korean.'", prompt)
		}
	})

	t.Run("English locale is no-op", func(t *testing.T) {
		call := makeCall("Tell me about the weather.")
		result := injectLocaleInstruction(call, "en")
		if extractPrompt(result) != "Tell me about the weather." {
			t.Error("English locale should not modify prompt")
		}
	})

	t.Run("empty locale is no-op", func(t *testing.T) {
		call := makeCall("Tell me about the weather.")
		result := injectLocaleInstruction(call, "")
		if extractPrompt(result) != "Tell me about the weather." {
			t.Error("empty locale should not modify prompt")
		}
	})

	t.Run("unknown locale uses raw code", func(t *testing.T) {
		call := makeCall("Hello")
		result := injectLocaleInstruction(call, "ar")
		prompt := extractPrompt(result)
		if !strings.HasSuffix(prompt, "\n\nRespond in ar.") {
			t.Errorf("got %q, want suffix 'Respond in ar.'", prompt)
		}
	})

	t.Run("no args is safe", func(t *testing.T) {
		call := core.SkillCall{SkillName: "Llm", Method: "generate"}
		result := injectLocaleInstruction(call, "ko")
		if len(result.Args) != 0 {
			t.Error("should not add args")
		}
	})
}

// recordingProvider captures the messages handed to Generate so tests can
// assert on the wire-shape that downstream LLMs would actually see.
type recordingProvider struct {
	captured [][]core.LlmMessage
	resp     *llm.Response
}

func (r *recordingProvider) Generate(_ context.Context, msgs []core.LlmMessage) (*llm.Response, error) {
	clone := make([]core.LlmMessage, len(msgs))
	copy(clone, msgs)
	r.captured = append(r.captured, clone)
	if r.resp == nil {
		return &llm.Response{Content: "ok", Usage: &llm.TokenUsage{Model: "mock"}}, nil
	}
	return r.resp, nil
}

func (r *recordingProvider) GenerateWithTools(ctx context.Context, msgs []core.LlmMessage, _ []llm.Tool) (*llm.Response, error) {
	return r.Generate(ctx, msgs)
}

func (r *recordingProvider) ContextWindow() int { return 128_000 }
func (r *recordingProvider) MaxTokens() int     { return 4096 }

// TestExecuteLLM_ToolResultProtocol pins the Phase A native tool_result
// protocol. The mis-attribution bug ("제공해주신 검색 결과는…") is structural,
// not a wording problem — the model only stops attributing tool data to the
// user when the data arrives in a tool_result content block instead of the
// raw user-message string. This test fails the moment that wrapping breaks,
// regardless of what the model actually says.
func TestExecuteLLM_ToolResultProtocol(t *testing.T) {
	prov := &recordingProvider{}
	sess := &Session{Provider: prov}

	const promptPayload = "다음 검색 결과를 정리해주세요.\n[search dump: Seoul 12C cloudy]"
	args, err := json.Marshal(promptPayload)
	if err != nil {
		t.Fatalf("marshal prompt: %v", err)
	}
	call := core.SkillCall{
		SkillName: "Llm",
		Method:    "generate",
		Args:      []json.RawMessage{args},
	}

	if _, err := executeLLM(context.Background(), call, sess); err != nil {
		t.Fatalf("executeLLM: %v", err)
	}
	if len(prov.captured) != 1 {
		t.Fatalf("expected exactly 1 Generate call, got %d", len(prov.captured))
	}
	msgs := prov.captured[0]
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages (priming user + assistant tool_use + user tool_result), got %d: %+v", len(msgs), msgs)
	}

	// msg[0]: priming user message in plain string Content.
	if msgs[0].Role != core.RoleUser {
		t.Errorf("msg[0].Role = %q, want user", msgs[0].Role)
	}
	if msgs[0].Content == "" || len(msgs[0].ContentBlocks) != 0 {
		t.Errorf("msg[0] should be plain-string priming, got Content=%q ContentBlocks=%+v", msgs[0].Content, msgs[0].ContentBlocks)
	}
	if strings.Contains(msgs[0].Content, promptPayload) {
		t.Error("msg[0] (priming) leaked the prompt payload — must stay procedural")
	}

	// msg[1]: assistant with one tool_use block. Capture the synthetic id.
	if msgs[1].Role != core.RoleAssistant {
		t.Errorf("msg[1].Role = %q, want assistant", msgs[1].Role)
	}
	if len(msgs[1].ContentBlocks) != 1 {
		t.Fatalf("msg[1] should have exactly 1 content block, got %+v", msgs[1].ContentBlocks)
	}
	toolUse := msgs[1].ContentBlocks[0]
	if toolUse.Type != core.BlockTypeToolUse {
		t.Errorf("msg[1] block type = %q, want tool_use", toolUse.Type)
	}
	if !strings.HasPrefix(toolUse.ID, "toolu_") {
		t.Errorf("tool_use ID = %q, want toolu_<hex> prefix", toolUse.ID)
	}
	if toolUse.Name == "" {
		t.Error("tool_use Name is empty — model can't tell what produced the result")
	}
	if msgs[1].Content != "" {
		t.Errorf("msg[1] should rely on ContentBlocks only, got Content=%q", msgs[1].Content)
	}

	// msg[2]: user with one tool_result block carrying the prompt payload.
	if msgs[2].Role != core.RoleUser {
		t.Errorf("msg[2].Role = %q, want user", msgs[2].Role)
	}
	if len(msgs[2].ContentBlocks) != 1 {
		t.Fatalf("msg[2] should have exactly 1 content block, got %+v", msgs[2].ContentBlocks)
	}
	toolResult := msgs[2].ContentBlocks[0]
	if toolResult.Type != core.BlockTypeToolResult {
		t.Errorf("msg[2] block type = %q, want tool_result", toolResult.Type)
	}
	if toolResult.ToolUseID != toolUse.ID {
		t.Errorf("tool_result.tool_use_id = %q, want %q (must match preceding tool_use.id)", toolResult.ToolUseID, toolUse.ID)
	}
	// Phase B reframe: the tool_result content is XML-wrapped for an extra
	// structural signal. The original payload must still be inside; the
	// wrapper must surround it; nothing else may leak through.
	if !strings.Contains(toolResult.Content, promptPayload) {
		t.Errorf("tool_result.Content lost the original payload\n got: %q\nwant contains: %q", toolResult.Content, promptPayload)
	}
	if !strings.Contains(toolResult.Content, "<tool_result") {
		t.Errorf("tool_result.Content missing opening XML role tag: %q", toolResult.Content)
	}
	if !strings.Contains(toolResult.Content, "</tool_result>") {
		t.Errorf("tool_result.Content missing closing XML role tag: %q", toolResult.Content)
	}
	if msgs[2].Content != "" {
		t.Errorf("msg[2] should carry the payload via ContentBlocks only, got Content=%q", msgs[2].Content)
	}

	// Hard contract: the prompt payload must NOT appear in the string Content
	// of any message. If it does, the model would re-read it as user input
	// and the mis-attribution returns.
	for i, m := range msgs {
		if strings.Contains(m.Content, promptPayload) {
			t.Errorf("msg[%d].Content (string) leaks the prompt payload — defeats the tool_result wrap", i)
		}
	}
}

// TestSubLLMPriming_AssistantContract pins the sub-LLM priming user message
// against the assistant behavior contract — the message must teach the model,
// in general principle (not specific phrase enumeration), that the upcoming
// tool_result is the assistant's own observation, not user input. This is
// the Phase B reframe of the priming after Phase A's protocol fix proved
// insufficient against Korean honorific priors.
func TestSubLLMPriming_AssistantContract(t *testing.T) {
	msgs := buildSubLLMMessages("any prompt")
	if len(msgs) == 0 || msgs[0].Role != core.RoleUser {
		t.Fatalf("expected first message to be user-role priming, got: %+v", msgs)
	}
	priming := msgs[0].Content

	// General-principle markers (no specific Korean phrase enumeration —
	// that path collides with priors per R-MVP).
	requiredMarkers := []string{
		"비서",           // assistant identity
		"first person", // first-person framing in English
		"도구 결과",        // tool result, not user-provided
		"솔직히",          // honest uncertainty
	}
	for _, m := range requiredMarkers {
		if !strings.Contains(priming, m) {
			t.Errorf("priming missing required principle marker %q\nfull priming:\n%s", m, priming)
		}
	}
}

// TestSubLLMRoleTagging pins the XML role-tag wrap as a defense-in-depth
// signal beyond the Anthropic content-block protocol. If the wrap regresses
// the priors-resistant layer is gone and mis-attribution can re-emerge in
// languages with strong honorific defaults.
func TestSubLLMRoleTagging(t *testing.T) {
	out := wrapSubLLMToolResult("payload-XYZ")
	if !strings.HasPrefix(out, "<tool_result") {
		t.Errorf("wrap missing opening tag, got: %q", out)
	}
	if !strings.Contains(out, "source=\"framework_context\"") {
		t.Errorf("wrap missing source attribute, got: %q", out)
	}
	if !strings.HasSuffix(out, "</tool_result>") {
		t.Errorf("wrap missing closing tag, got: %q", out)
	}
	if !strings.Contains(out, "payload-XYZ") {
		t.Errorf("wrap dropped the payload, got: %q", out)
	}
}

// TestExecuteLLM_ToolUseIDsAreUnique guards against a regression where two
// concurrent skill calls share the same synthetic tool_use_id, which would
// let the model conflate their tool_result payloads.
func TestExecuteLLM_ToolUseIDsAreUnique(t *testing.T) {
	const calls = 20
	seen := make(map[string]struct{}, calls)
	for i := 0; i < calls; i++ {
		id := newSubLLMToolUseID()
		if !strings.HasPrefix(id, "toolu_") {
			t.Fatalf("id %q missing toolu_ prefix", id)
		}
		if len(id) <= len("toolu_") {
			t.Fatalf("id %q has no random body", id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate tool_use id after %d calls: %q", i, id)
		}
		seen[id] = struct{}{}
	}
}
