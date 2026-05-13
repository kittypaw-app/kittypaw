package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"nhooyr.io/websocket"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
)

func TestValidateTurnID_EmptyAllowed(t *testing.T) {
	if msg, ok := validateTurnID(""); !ok || msg != "" {
		t.Errorf("empty turn_id should pass (legacy fallback): got msg=%q ok=%v", msg, ok)
	}
}

func TestValidateTurnID_AcceptsUUID(t *testing.T) {
	id := uuid.NewString()
	if msg, ok := validateTurnID(id); !ok || msg != "" {
		t.Errorf("uuid %q should pass: got msg=%q ok=%v", id, msg, ok)
	}
}

func TestValidateTurnID_RejectsOverLength(t *testing.T) {
	long := strings.Repeat("a", maxTurnIDLen+1)
	msg, ok := validateTurnID(long)
	if ok {
		t.Fatal("over-length id should fail")
	}
	if !strings.Contains(msg, "length") {
		t.Errorf("error msg should mention length: %q", msg)
	}
}

func TestValidateTurnID_RejectsNonUUID(t *testing.T) {
	cases := []string{
		"1",
		"abc",
		"not-a-uuid-at-all",
		"00000000-0000-0000-0000-00000000000",  // 35 chars (one short)
		"00000000-0000-0000-0000-000000000g00", // bad hex
	}
	for _, id := range cases {
		t.Run(id, func(t *testing.T) {
			msg, ok := validateTurnID(id)
			if ok {
				t.Errorf("non-UUID %q should fail validation", id)
			}
			if !strings.Contains(msg, "UUID") {
				t.Errorf("error msg should mention UUID: %q", msg)
			}
		})
	}
}

func TestWebSocketUsesAuthenticatedAccount(t *testing.T) {
	aliceCfg := core.DefaultConfig()
	aliceCfg.Server.APIKey = "alice-key"
	bobCfg := core.DefaultConfig()
	bobCfg.Server.APIKey = "bob-key"
	srv := newMultiAccountAuthTestServer(t, "alice", map[string]string{
		"alice": "alice-pw",
		"bob":   "bob-pw",
	}, map[string]*core.Config{
		"alice": &aliceCfg,
		"bob":   &bobCfg,
	})
	srv.accounts.Runtime("alice").Provider = wsProvider{content: `return "from alice";`}
	srv.accounts.Runtime("bob").Provider = wsProvider{content: `return "from bob";`}

	cookie := loginSessionCookie(t, srv, "bob", "bob-pw")
	httpServer := httptest.NewServer(srv.setupRoutes())
	t.Cleanup(httpServer.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws"
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{cookie.String()}},
	})
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.CloseNow()

	msg := readWSServerMsg(t, ctx, conn)
	if msg.Type != core.WsMsgSession {
		t.Fatalf("first ws msg type = %q, want session", msg.Type)
	}

	writeWSClientMsg(t, ctx, conn, core.WsClientMsg{
		Type: core.WsMsgChat,
		Text: "hello",
	})
	msg = readWSServerMsg(t, ctx, conn)
	if msg.Type != core.WsMsgDone {
		t.Fatalf("second ws msg type = %q message=%q, want done", msg.Type, msg.Message)
	}
	if msg.FullText != "from bob" {
		t.Fatalf("full_text = %q, want bob account response", msg.FullText)
	}
}

func TestWebSocketRouteOutlivesHTTPMiddlewareTimeout(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Server.APIKey = "alice-key"
	srv := newAuthTestServer(t, t.TempDir(), "alice", &cfg)
	srv.accounts.Runtime("alice").Provider = slowWSProvider{
		delay:   50 * time.Millisecond,
		content: `return "slow done";`,
	}

	httpServer := httptest.NewServer(srv.setupRoutesWithTimeout(10 * time.Millisecond))
	t.Cleanup(httpServer.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws?token=alice-key"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.CloseNow()

	readWSServerMsg(t, ctx, conn)
	writeWSClientMsg(t, ctx, conn, core.WsClientMsg{Type: core.WsMsgChat, Text: "hello"})
	msg := readWSServerMsg(t, ctx, conn)
	if msg.Type != core.WsMsgDone {
		t.Fatalf("msg type = %q message=%q, want done", msg.Type, msg.Message)
	}
	if msg.FullText != "slow done" {
		t.Fatalf("full_text = %q, want slow done", msg.FullText)
	}
}

func TestWebSocketSingleAccountAcceptsMasterAPIKey(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	auth := core.NewLocalAuthStore(filepath.Join(root, "accounts"))
	if err := auth.CreateUser("alice", "pw"); err != nil {
		t.Fatalf("create local auth user: %v", err)
	}
	deps := buildAccountDeps(t, filepath.Join(root, "accounts"), "alice", &core.Config{})
	srv := NewWithServerConfig([]*AccountDeps{deps}, "test", core.TopLevelServerConfig{
		MasterAPIKey: "master-key",
	})

	req := httptest.NewRequest(http.MethodGet, "/ws?token=master-key", nil)
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code == http.StatusUnauthorized {
		t.Fatalf("websocket rejected master api key for single account")
	}
}

func TestWebSocketSingleAccountStaticKeyRequiresToken(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Server.APIKey = "account-key"
	srv := newAuthTestServer(t, t.TempDir(), "alice", &cfg)

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("websocket without token code = %d, want 401", rr.Code)
	}
}

func TestWebSocketUsesAccountScopedAPIKey(t *testing.T) {
	aliceCfg := core.DefaultConfig()
	aliceCfg.Server.APIKey = "alice-key"
	bobCfg := core.DefaultConfig()
	bobCfg.Server.APIKey = "bob-key"
	srv := newMultiAccountAuthTestServer(t, "alice", map[string]string{
		"alice": "alice-pw",
		"bob":   "bob-pw",
	}, map[string]*core.Config{
		"alice": &aliceCfg,
		"bob":   &bobCfg,
	})
	srv.accounts.Runtime("alice").Provider = wsProvider{content: `return "from alice";`}
	srv.accounts.Runtime("bob").Provider = wsProvider{content: `return "from bob";`}

	httpServer := httptest.NewServer(srv.setupRoutes())
	t.Cleanup(httpServer.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws?token=bob-key"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.CloseNow()
	readWSServerMsg(t, ctx, conn)
	writeWSClientMsg(t, ctx, conn, core.WsClientMsg{Type: core.WsMsgChat, Text: "hello"})
	msg := readWSServerMsg(t, ctx, conn)
	if msg.FullText != "from bob" {
		t.Fatalf("full_text = %q, want bob account response", msg.FullText)
	}
}

func TestWebSocketRejectsMasterAPIKeyWithMultipleAccounts(t *testing.T) {
	aliceCfg := core.DefaultConfig()
	aliceCfg.Server.APIKey = "alice-key"
	bobCfg := core.DefaultConfig()
	bobCfg.Server.APIKey = "bob-key"
	srv := newMultiAccountAuthTestServer(t, "alice", map[string]string{
		"alice": "alice-pw",
		"bob":   "bob-pw",
	}, map[string]*core.Config{
		"alice": &aliceCfg,
		"bob":   &bobCfg,
	})
	srv.masterAPIKey = "master-key"

	req := httptest.NewRequest(http.MethodGet, "/ws?token=master-key", nil)
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("websocket with multi-account master key code = %d, want 401", rr.Code)
	}
}

func TestWebSocketStopsWhenAccountRemoved(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Server.APIKey = "alice-key"
	srv := newServerWithLocalUserAndConfig(t, "alice", "pw", &cfg)
	srv.accounts.Runtime("alice").Provider = wsProvider{content: `return "from alice";`}
	cookie := loginSessionCookie(t, srv, "alice", "pw")
	httpServer := httptest.NewServer(srv.setupRoutes())
	t.Cleanup(httpServer.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws"
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{cookie.String()}},
	})
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.CloseNow()
	readWSServerMsg(t, ctx, conn)

	srv.accountMu.Lock()
	delete(srv.accountDeps, "alice")
	srv.accountMu.Unlock()
	srv.accounts.Remove("alice")

	writeWSClientMsg(t, ctx, conn, core.WsClientMsg{Type: core.WsMsgChat, Text: "hello"})
	msg := readWSServerMsg(t, ctx, conn)
	if msg.Type != core.WsMsgError {
		t.Fatalf("msg type = %q, want error", msg.Type)
	}
	if !strings.Contains(msg.Message, "account inactive") {
		t.Fatalf("error message = %q, want account inactive", msg.Message)
	}
}

type wsProvider struct {
	content string
}

func (p wsProvider) Generate(context.Context, []core.LlmMessage) (*llm.Response, error) {
	return &llm.Response{Content: p.content}, nil
}

func (p wsProvider) GenerateWithTools(ctx context.Context, msgs []core.LlmMessage, _ []llm.Tool) (*llm.Response, error) {
	return p.Generate(ctx, msgs)
}

func (p wsProvider) ContextWindow() int { return 128_000 }
func (p wsProvider) MaxTokens() int     { return 4096 }

type slowWSProvider struct {
	delay   time.Duration
	content string
}

func (p slowWSProvider) Generate(ctx context.Context, _ []core.LlmMessage) (*llm.Response, error) {
	select {
	case <-time.After(p.delay):
		return &llm.Response{Content: p.content}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p slowWSProvider) GenerateWithTools(ctx context.Context, msgs []core.LlmMessage, _ []llm.Tool) (*llm.Response, error) {
	return p.Generate(ctx, msgs)
}

func (p slowWSProvider) ContextWindow() int { return 128_000 }
func (p slowWSProvider) MaxTokens() int     { return 4096 }

func readWSServerMsg(t *testing.T, ctx context.Context, conn *websocket.Conn) core.WsServerMsg {
	t.Helper()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read ws msg: %v", err)
	}
	var msg core.WsServerMsg
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("decode ws msg %q: %v", string(data), err)
	}
	return msg
}

func writeWSClientMsg(t *testing.T, ctx context.Context, conn *websocket.Conn, msg core.WsClientMsg) {
	t.Helper()
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal ws msg: %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write ws msg: %v", err)
	}
}
