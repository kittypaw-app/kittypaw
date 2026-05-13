package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
)

// newShareFixture stands up a two-account topology on disk (team-space owner +
// alice reader) with a weather.json that team-space members can read.
// The fixture returns an AccountRuntime wired as alice so each test exercises
// the same execution path the sandbox uses at runtime — the exported
// executeShare is the only thing under test; everything else is plumbing.
func newShareFixture(t *testing.T) (sess *AccountRuntime, teamDir string) {
	t.Helper()
	root := t.TempDir()

	teamDir = filepath.Join(root, "accounts", "team")
	aliceDir := filepath.Join(root, "accounts", "alice")
	if err := os.MkdirAll(filepath.Join(teamDir, "memory"), 0o755); err != nil {
		t.Fatalf("mkdir team: %v", err)
	}
	if err := os.MkdirAll(aliceDir, 0o755); err != nil {
		t.Fatalf("mkdir alice: %v", err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "memory", "weather.json"), []byte(`{"t":18}`), 0o644); err != nil {
		t.Fatalf("write weather: %v", err)
	}

	reg := core.NewAccountRegistry(filepath.Join(root, "accounts"), "team")
	reg.Register(&core.Account{
		ID:      "team",
		BaseDir: teamDir,
		Config: &core.Config{
			IsShared:  true,
			TeamSpace: core.TeamSpaceConfig{Members: []string{"alice"}},
		},
	})
	reg.Register(&core.Account{ID: "alice", BaseDir: aliceDir, Config: &core.Config{}})

	sess = &AccountRuntime{
		Config:          &core.Config{},
		AccountID:       "alice",
		AccountRegistry: reg,
	}
	return sess, teamDir
}

func mustCall(t *testing.T, accountID, path string) core.SkillCall {
	t.Helper()
	tid, _ := json.Marshal(accountID)
	p, _ := json.Marshal(path)
	return core.SkillCall{SkillName: "Share", Method: "read", Args: []json.RawMessage{tid, p}}
}

// TestShareRead_Success pins the happy path — alice asking for a
// team-space memory path returns the file body. The pair (AccountRuntime.AccountID,
// target Account from registry) is what makes the cross-account check
// meaningful; without the session field wired up the whole surface is
// dead code.
func TestShareRead_Success(t *testing.T) {
	sess, _ := newShareFixture(t)

	out, err := executeShare(context.Background(), mustCall(t, "team", "memory/weather.json"), sess)
	if err != nil {
		t.Fatalf("executeShare: %v", err)
	}
	var resp map[string]string
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["content"] != `{"t":18}` {
		t.Errorf("content = %q", resp["content"])
	}
}

// TestShareRead_NotShareable checks that a path outside team-space shareable
// data surfaces as a JS-level error, not a filesystem leak.
// The reject path is the critical one: it's where a hostile or sloppy
// skill would try to escalate, so the failure must be explicit.
func TestShareRead_NotShareable(t *testing.T) {
	sess, teamDir := newShareFixture(t)
	if err := os.WriteFile(filepath.Join(teamDir, "config.toml"), []byte(`is_shared=true`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	out, err := executeShare(context.Background(), mustCall(t, "team", "config.toml"), sess)
	if err != nil {
		t.Fatalf("executeShare: %v", err)
	}
	var resp map[string]string
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.Contains(resp["error"], "shareable") {
		t.Errorf("expected shareable error, got %q", resp["error"])
	}
}

// TestShareRead_UnknownAccount rejects typos at the API boundary — a
// skill asking to read from "grandma" when no such account exists must
// NOT fall through to some default account lookup. The whole value of
// AccountRouter's strict routing vanishes if share reads silently
// rewrite unknown targets.
//
// The externally-visible error string is the same as a non-team-space target
// (defense against account ID enumeration via error oracle); the audit log
// carries reason=unknown_account for forensics.
func TestShareRead_UnknownAccount(t *testing.T) {
	sess, _ := newShareFixture(t)

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	out, _ := executeShare(context.Background(), mustCall(t, "grandma", "memory/weather.json"), sess)
	var resp map[string]string
	_ = json.Unmarshal([]byte(out), &resp)
	if !strings.Contains(resp["error"], "target is not the team space") {
		t.Errorf("expected unified team-space target error, got %q", resp["error"])
	}
	if !strings.Contains(buf.String(), `"reason":"unknown_account"`) {
		t.Errorf("audit log should carry unknown_account reason; got: %s", buf.String())
	}
}

// TestShareRead_AuditLog verifies the operational contract — every successful
// cross-account read emits a structured slog record with {from, to, path,
// bytes}. Silent success would make data-flow auditing impossible when a
// deployment goes sideways.
func TestShareRead_AuditLog(t *testing.T) {
	sess, _ := newShareFixture(t)

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	if _, err := executeShare(context.Background(), mustCall(t, "team", "memory/weather.json"), sess); err != nil {
		t.Fatalf("executeShare: %v", err)
	}

	log := buf.String()
	if !strings.Contains(log, `"msg":"cross_account_read"`) {
		t.Errorf("audit record missing: %s", log)
	}
	if !strings.Contains(log, `"from":"alice"`) || !strings.Contains(log, `"to":"team"`) {
		t.Errorf("audit record missing account labels: %s", log)
	}
}

// TestShareRead_NoRegistry protects against the "runtime wired without
// account context" case — e.g. a legacy single-account server or a test
// setup that forgot to inject the registry. Rather than panic on nil,
// surface a clear "unavailable" error so skill authors see what's
// missing instead of debugging a segfault.
func TestShareRead_NoRegistry(t *testing.T) {
	sess := &AccountRuntime{Config: &core.Config{}} // AccountID="", AccountRegistry=nil

	out, _ := executeShare(context.Background(), mustCall(t, "team", "x"), sess)
	var resp map[string]string
	_ = json.Unmarshal([]byte(out), &resp)
	if !strings.Contains(resp["error"], "unavailable") {
		t.Errorf("expected unavailable error, got %q", resp["error"])
	}
}

func TestShareRead_RejectsNonMember(t *testing.T) {
	sess, _ := newShareFixture(t)
	out, _ := executeShare(context.Background(), mustCall(t, "team", "memory/weather.json"), &AccountRuntime{
		AccountID:       "bob",
		AccountRegistry: sess.AccountRegistry,
	})
	var resp map[string]string
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	if !strings.Contains(resp["error"], "team space") {
		t.Errorf("expected team-space membership error, got %q", resp["error"])
	}
}

func TestShareRead_NonMemberAndUnknownTargetShareExternalError(t *testing.T) {
	sess, _ := newShareFixture(t)

	nonMemberOut, _ := executeShare(context.Background(), mustCall(t, "team", "memory/weather.json"), &AccountRuntime{
		AccountID:       "bob",
		AccountRegistry: sess.AccountRegistry,
	})
	nonTeamSpaceOut, _ := executeShare(context.Background(), mustCall(t, "alice", "memory/weather.json"), &AccountRuntime{
		AccountID:       "bob",
		AccountRegistry: sess.AccountRegistry,
	})
	unknownOut, _ := executeShare(context.Background(), mustCall(t, "grandma", "memory/weather.json"), &AccountRuntime{
		AccountID:       "bob",
		AccountRegistry: sess.AccountRegistry,
	})

	var nonMemberResp, nonTeamSpaceResp, unknownResp map[string]string
	if err := json.Unmarshal([]byte(nonMemberOut), &nonMemberResp); err != nil {
		t.Fatalf("non-member json: %v", err)
	}
	if err := json.Unmarshal([]byte(nonTeamSpaceOut), &nonTeamSpaceResp); err != nil {
		t.Fatalf("non-team-space json: %v", err)
	}
	if err := json.Unmarshal([]byte(unknownOut), &unknownResp); err != nil {
		t.Fatalf("unknown json: %v", err)
	}
	if nonMemberResp["error"] != unknownResp["error"] {
		t.Errorf("non-member and unknown target errors should match, got %q vs %q", nonMemberResp["error"], unknownResp["error"])
	}
	if nonMemberResp["error"] != nonTeamSpaceResp["error"] {
		t.Errorf("non-member and non-team-space target errors should match, got %q vs %q", nonMemberResp["error"], nonTeamSpaceResp["error"])
	}
}

// TestShareRead_RejectsNonTeamSpaceTarget is the invariant that closes the I5
// hole: even if bob's config legally contains `[share.alice] read = [...]`,
// alice cannot read from bob because bob is not the team-space account. The
// membership grants access to a team space; the owner gate decides
// whether the owner is even reachable. Without this, Plan B's "personal ↔
// personal forbidden" rule would depend entirely on admins never adding a
// Share block to a personal config — a policy we can't enforce at the doc
// layer.
func TestShareRead_RejectsNonTeamSpaceTarget(t *testing.T) {
	t.Helper()
	root := t.TempDir()

	bobDir := filepath.Join(root, "accounts", "bob")
	aliceDir := filepath.Join(root, "accounts", "alice")
	if err := os.MkdirAll(filepath.Join(bobDir, "memory"), 0o755); err != nil {
		t.Fatalf("mkdir bob: %v", err)
	}
	if err := os.MkdirAll(aliceDir, 0o755); err != nil {
		t.Fatalf("mkdir alice: %v", err)
	}
	// Drop a file bob would "share" if the gate were absent — proves the
	// gate fires before any filesystem read, not after.
	if err := os.WriteFile(filepath.Join(bobDir, "memory", "notes.json"), []byte(`{"secret":true}`), 0o644); err != nil {
		t.Fatalf("write bob: %v", err)
	}

	reg := core.NewAccountRegistry(filepath.Join(root, "accounts"), "family")
	// bob is a personal account (IsFamily=false) that has legacy share config.
	// The team-space target gate MUST reject regardless.
	reg.Register(&core.Account{
		ID:      "bob",
		BaseDir: bobDir,
		Config: &core.Config{
			IsFamily: false,
			Share:    map[string]core.ShareConfig{"alice": {Read: []string{"memory/notes.json"}}},
		},
	})
	reg.Register(&core.Account{ID: "alice", BaseDir: aliceDir, Config: &core.Config{}})

	sess := &AccountRuntime{
		Config:          &core.Config{},
		AccountID:       "alice",
		AccountRegistry: reg,
	}

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	out, err := executeShare(context.Background(), mustCall(t, "bob", "memory/notes.json"), sess)
	if err != nil {
		t.Fatalf("executeShare: %v", err)
	}
	var resp map[string]string
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// The error surface is checked loosely (substring "team space") so future
	// wording changes don't force test churn, but the key property —
	// rejected without a "content" field — is pinned.
	if _, ok := resp["content"]; ok {
		t.Errorf("must not return content for non-shared target: %q", resp["content"])
	}
	if resp["error"] == "" {
		t.Errorf("expected an error field, got %+v", resp)
	}
	if !strings.Contains(strings.ToLower(resp["error"]), "team space") {
		t.Errorf("expected team-space-only error, got %q", resp["error"])
	}

	log := buf.String()
	if !strings.Contains(log, "cross_account_read_rejected") {
		t.Errorf("expected rejection audit record, got: %s", log)
	}
	if !strings.Contains(log, `"to":"bob"`) {
		t.Errorf("expected audit to capture target identity, got: %s", log)
	}
}

// TestShareRead_RejectsSelfTarget pins the edge case where alice targets
// "alice" — self-reads should never need Share.read (direct fs access is
// the intended path) and allowing them would create a subtle bypass where
// a personal account writes a loopback Share config to exercise the
// audit/read code path on its own data.
func TestShareRead_RejectsSelfTarget(t *testing.T) {
	sess, _ := newShareFixture(t)

	out, _ := executeShare(context.Background(), mustCall(t, "alice", "memory/weather.json"), sess)
	var resp map[string]string
	_ = json.Unmarshal([]byte(out), &resp)
	if resp["content"] != "" {
		t.Errorf("self-target must not return content: %q", resp["content"])
	}
	if !strings.Contains(strings.ToLower(resp["error"]), "team space") {
		t.Errorf("expected team-space-only error for self target, got %q", resp["error"])
	}
}
