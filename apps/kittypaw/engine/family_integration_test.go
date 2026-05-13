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
	"github.com/jinto/kittypaw/sandbox"
)

// TestFamily_ShareReadE2E drives the full cross-account read stack through the
// JS sandbox: alice's skill calls `Share.read("team", ...)`, the call
// crosses the resolver into executeShare, passes ValidateSharedReadPath
// against team-space membership, and returns the file content.
// bob — not a member — gets a denial, and the audit log captures both
// outcomes. Without this end-to-end, the unit tests green-light pieces the
// JS layer can't actually reach.
func TestFamily_ShareReadE2E(t *testing.T) {
	root := t.TempDir()

	// --- account layout ---
	team := makeAccount(t, root, "team", &core.Config{
		IsShared:  true,
		TeamSpace: core.TeamSpaceConfig{Members: []string{"alice"}},
	})
	alice := makeAccount(t, root, "alice", &core.Config{})
	bob := makeAccount(t, root, "bob", &core.Config{})

	// Drop a file the team space shares with members.
	writeAccountFile(t, filepath.Join(team.BaseDir, "memory", "weather.json"),
		`{"today":"sunny","high":22}`)

	registry := core.NewAccountRegistry(root, "alice")
	registry.Register(team)
	registry.Register(alice)
	registry.Register(bob)

	// Capture slog to verify the cross_account_read audit record fires.
	var logBuf bytes.Buffer
	origLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(origLogger) })

	sbox := sandbox.New(core.SandboxConfig{TimeoutSecs: 5})

	// --- alice: team-space member -> success ---
	aliceSess := &AccountRuntime{
		Sandbox:         sbox,
		Config:          alice.Config,
		AccountID:       alice.ID,
		AccountRegistry: registry,
	}
	resolver := func(ctx context.Context, call core.SkillCall) (string, error) {
		return resolveSkillCall(ctx, call, aliceSess, nil)
	}
	code := `
		var r = Share.read("team", "memory/weather.json");
		return r.content;
	`
	result, err := sbox.ExecuteWithResolverOpts(context.Background(), code, nil, resolver,
		sandbox.Options{ExposeShare: !aliceSess.Config.IsFamily})
	if err != nil {
		t.Fatalf("alice sandbox: %v", err)
	}
	if !result.Success {
		t.Fatalf("alice sandbox errored: %s", result.Error)
	}
	if !strings.Contains(result.Output, `"today":"sunny"`) {
		t.Errorf("alice did not receive file content: %q", result.Output)
	}

	if !strings.Contains(logBuf.String(), `"cross_account_read"`) {
		t.Errorf("missing cross_account_read audit log; got: %s", logBuf.String())
	}
	if !strings.Contains(logBuf.String(), `"from":"alice"`) {
		t.Errorf("audit log missing reader identity; got: %s", logBuf.String())
	}

	// --- bob: not a team-space member -> denied ---
	logBuf.Reset()
	bobSess := &AccountRuntime{
		Sandbox:         sbox,
		Config:          bob.Config,
		AccountID:       bob.ID,
		AccountRegistry: registry,
	}
	bobResolver := func(ctx context.Context, call core.SkillCall) (string, error) {
		return resolveSkillCall(ctx, call, bobSess, nil)
	}
	denyCode := `
		var r = Share.read("team", "memory/weather.json");
		return JSON.stringify(r);
	`
	result, err = sbox.ExecuteWithResolverOpts(context.Background(), denyCode, nil, bobResolver,
		sandbox.Options{ExposeShare: !bobSess.Config.IsFamily})
	if err != nil {
		t.Fatalf("bob sandbox: %v", err)
	}
	if !result.Success {
		t.Fatalf("bob sandbox errored: %s", result.Error)
	}
	// Denial is returned as an `error` field, not a thrown exception — skill
	// code is expected to branch on response shape.
	if !strings.Contains(result.Output, `"error"`) {
		t.Errorf("bob should see error field, got: %q", result.Output)
	}
	if !strings.Contains(logBuf.String(), "cross_account_read_rejected") {
		t.Errorf("missing rejection audit for bob; got: %s", logBuf.String())
	}
}

// TestFamily_FanoutE2E proves the team-space to personal push path end-to-end.
// A team-space skill calls Fanout.send("alice", …) through the actual
// Sandbox, the event lands on eventCh as EventTeamSpacePush with the target
// accountID, and alice never sees the Fanout global at all (defense in
// depth — a personal skill probing `typeof Fanout` hits undefined).
func TestFamily_FanoutE2E(t *testing.T) {
	root := t.TempDir()
	family := makeAccount(t, root, "family", &core.Config{
		IsShared:  true,
		TeamSpace: core.TeamSpaceConfig{Members: []string{"alice"}},
	})
	alice := makeAccount(t, root, "alice", &core.Config{})

	registry := core.NewAccountRegistry(root, "alice")
	registry.Register(family)
	registry.Register(alice)

	eventCh := make(chan core.Event, 4)
	fanout := core.NewChannelFanout(eventCh, registry, "family")

	sbox := sandbox.New(core.SandboxConfig{TimeoutSecs: 5})

	// --- family: Fanout wired → push succeeds ---
	famSess := &AccountRuntime{
		Sandbox:         sbox,
		Config:          family.Config,
		AccountID:       family.ID,
		AccountRegistry: registry,
		Fanout:          fanout,
	}
	famResolver := func(ctx context.Context, call core.SkillCall) (string, error) {
		return resolveSkillCall(ctx, call, famSess, nil)
	}
	code := `
		if (typeof Fanout !== "object") return "missing:" + typeof Fanout;
		var r = Fanout.send("alice", {text: "🍚 저녁 준비됐어!"});
		return JSON.stringify(r);
	`
	result, err := sbox.ExecuteWithResolverOpts(context.Background(), code, nil, famResolver,
		sandbox.Options{ExposeFanout: famSess.Fanout != nil})
	if err != nil {
		t.Fatalf("family sandbox: %v", err)
	}
	if !result.Success {
		t.Fatalf("family sandbox errored: %s", result.Error)
	}
	if !strings.Contains(result.Output, `"success":true`) {
		t.Errorf("family expected success, got %q", result.Output)
	}

	select {
	case ev := <-eventCh:
		if ev.Type != core.EventTeamSpacePush {
			t.Errorf("expected EventTeamSpacePush, got %q", ev.Type)
		}
		if ev.AccountID != "alice" {
			t.Errorf("expected target=alice, got %q", ev.AccountID)
		}
		var body core.FanoutPayload
		if err := json.Unmarshal(ev.Payload, &body); err != nil {
			t.Fatalf("payload unmarshal: %v", err)
		}
		if !strings.Contains(body.Text, "저녁 준비됐어") {
			t.Errorf("payload text wrong: %q", body.Text)
		}
	default:
		t.Fatal("expected EventTeamSpacePush on channel; nothing published")
	}

	// --- alice: no Fanout wired → JS global hidden ---
	aliceSess := &AccountRuntime{
		Sandbox:         sbox,
		Config:          alice.Config,
		AccountID:       alice.ID,
		AccountRegistry: registry,
	}
	aliceResolver := func(ctx context.Context, call core.SkillCall) (string, error) {
		return resolveSkillCall(ctx, call, aliceSess, nil)
	}
	probeCode := `return typeof Fanout;`
	probe, err := sbox.ExecuteWithResolverOpts(context.Background(), probeCode, nil, aliceResolver,
		sandbox.Options{ExposeFanout: aliceSess.Fanout != nil})
	if err != nil {
		t.Fatalf("alice probe: %v", err)
	}
	if probe.Output != "undefined" {
		t.Errorf("personal account must not see Fanout; got %q", probe.Output)
	}
}

// --- helpers ---

func makeAccount(t *testing.T, root, id string, cfg *core.Config) *core.Account {
	t.Helper()
	baseDir := filepath.Join(root, id)
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", baseDir, err)
	}
	return &core.Account{ID: id, BaseDir: baseDir, Config: cfg}
}

func writeAccountFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
