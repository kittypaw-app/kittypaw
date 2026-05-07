package smoke

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/kittypaw-app/kittyspace/internal/broker"
	"github.com/kittypaw-app/kittyspace/internal/daemonws"
	"github.com/kittypaw-app/kittyspace/internal/identity"
	"github.com/kittypaw-app/kittyspace/internal/openai"
	"github.com/kittypaw-app/kittyspace/internal/protocol"
	"github.com/kittypaw-app/kittyspace/internal/server"
	"github.com/kittypaw-app/kittyspace/internal/webapp"
)

const (
	localAPIToken      = "api_secret"
	localDeviceToken   = "dev_secret"
	localUserID        = "user_1"
	localDeviceID      = "dev_1"
	localAccountID     = "alice"
	localSmokeUserText = "hello from smoke"
)

func RunLocal(ctx context.Context, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	router, cleanup, err := localRouter()
	if err != nil {
		return err
	}
	defer cleanup()
	srv, err := newLocalServer(router, net.Listen)
	if err != nil {
		return err
	}
	defer srv.Close()

	if err := runStaticSurfaces(ctx, srv.URL); err != nil {
		return err
	}
	if err := writeProgress(out, "ok static chat and kanban surfaces"); err != nil {
		return err
	}

	daemonReady := make(chan struct{})
	daemonDone := make(chan error, 1)
	go func() {
		daemonDone <- runFakeDaemon(ctx, srv.URL, daemonReady)
	}()

	select {
	case <-daemonReady:
		if err := writeProgress(out, "ok daemon connected"); err != nil {
			return err
		}
	case err := <-daemonDone:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}

	if err := waitForRoute(ctx, srv.URL); err != nil {
		return err
	}
	if err := writeProgress(out, "ok route discovery dev_1/alice"); err != nil {
		return err
	}

	if err := runChatCompletion(ctx, srv.URL); err != nil {
		return err
	}
	if err := writeProgress(out, "ok chat completion relayed"); err != nil {
		return err
	}

	sessionCookie, err := runBFFLogin(ctx, srv.URL)
	if err != nil {
		return err
	}
	if err := writeProgress(out, "ok bff login"); err != nil {
		return err
	}

	if err := runBFFRoutes(ctx, srv.URL, sessionCookie); err != nil {
		return err
	}
	if err := writeProgress(out, "ok bff route discovery dev_1/alice"); err != nil {
		return err
	}

	if err := runBFFChatCompletion(ctx, srv.URL, sessionCookie); err != nil {
		return err
	}
	if err := writeProgress(out, "ok bff chat completion relayed"); err != nil {
		return err
	}

	select {
	case err := <-daemonDone:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func writeProgress(out io.Writer, message string) error {
	_, err := fmt.Fprintln(out, message)
	return err
}

func localRouter() (http.Handler, func(), error) {
	verifier := identity.NewMemoryCredentialVerifier()
	if err := verifier.AddAPIClient(localAPIToken, identity.APIClientClaims{
		Subject:   localUserID,
		Audiences: []string{identity.AudienceKittySpace},
		Version:   identity.CredentialVersion1,
		Scopes:    []identity.Scope{identity.ScopeChatRelay, identity.ScopeModelsRead},
		UserID:    localUserID,
		DeviceID:  localDeviceID,
		AccountID: localAccountID,
	}); err != nil {
		return nil, nil, fmt.Errorf("seed api client: %w", err)
	}
	if err := verifier.AddDevice(localDeviceToken, identity.DeviceClaims{
		Subject:         "device:" + localDeviceID,
		Audiences:       []string{identity.AudienceKittySpace},
		Version:         identity.CredentialVersion1,
		Scopes:          []identity.Scope{identity.ScopeDaemonConnect},
		UserID:          localUserID,
		DeviceID:        localDeviceID,
		LocalAccountIDs: []string{localAccountID},
	}); err != nil {
		return nil, nil, fmt.Errorf("seed device: %w", err)
	}

	b := broker.New(broker.Config{RequestTimeout: 2 * time.Second})
	openAIHandler := openai.NewHandler(identity.APIAuthenticator{
		Verifier: verifier,
	}, b)
	portal := newFakePortal()
	cleanup := portal.Close
	webHandler, err := webapp.New(webapp.Config{
		PublicBaseURL:  "https://space.kittypaw.app",
		APIAuthBaseURL: strings.TrimRight(portal.URL, "/") + "/auth",
		Verifier:       verifier,
		OpenAIHandler:  openAIHandler.Routes(),
		HTTPClient:     portal.Client(),
	})
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("web app: %w", err)
	}
	return server.NewRouter(server.Config{
		Version:    "smoke",
		WebHandler: webHandler,
		DaemonHandler: daemonws.NewHandler(identity.DeviceAuthenticator{
			Verifier: verifier,
		}, b),
		OpenAIHandler: openAIHandler,
	}), cleanup, nil
}

func newFakePortal() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/web/exchange":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			var body struct {
				Code         string `json:"code"`
				CodeVerifier string `json:"code_verifier"`
				RedirectURI  string `json:"redirect_uri"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			if body.Code != "smoke-oauth-code" || body.CodeVerifier == "" || body.RedirectURI == "" {
				http.Error(w, "bad exchange", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"access_token":%q,"refresh_token":"smoke-refresh","token_type":"Bearer","expires_in":900}`, localAPIToken)
		case "/auth/token/refresh":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"access_token":%q,"refresh_token":"smoke-refresh-2","token_type":"Bearer","expires_in":900}`, localAPIToken)
		default:
			http.NotFound(w, r)
		}
	}))
}

type listenFunc func(network, address string) (net.Listener, error)

func newLocalServer(handler http.Handler, listen listenFunc) (*httptest.Server, error) {
	if listen == nil {
		listen = net.Listen
	}
	ln, err := listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen local smoke server: %w", err)
	}
	srv := &httptest.Server{
		Listener: ln,
		Config:   &http.Server{Handler: handler},
	}
	srv.Start()
	return srv, nil
}

func runFakeDaemon(ctx context.Context, baseURL string, ready chan<- struct{}) error {
	wsURL := "ws" + strings.TrimPrefix(baseURL, "http") + "/daemon/connect"
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + localDeviceToken}},
	})
	if err != nil {
		return fmt.Errorf("connect fake daemon: %w", err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "smoke done") }()

	if err := wsjson.Write(ctx, conn, protocol.Frame{
		Type:            protocol.FrameHello,
		DeviceID:        localDeviceID,
		LocalAccounts:   []string{localAccountID},
		DaemonVersion:   "smoke",
		ProtocolVersion: protocol.ProtocolVersion1,
		Capabilities:    []protocol.Operation{protocol.OperationOpenAIChatCompletions},
	}); err != nil {
		return fmt.Errorf("write hello: %w", err)
	}
	close(ready)

	for i := 0; i < 2; i++ {
		var req protocol.Frame
		if err := wsjson.Read(ctx, conn, &req); err != nil {
			return fmt.Errorf("read relay request: %w", err)
		}
		if err := validateSmokeRequest(req); err != nil {
			return err
		}
		if err := writeSmokeResponse(ctx, conn, req); err != nil {
			return err
		}
	}
	return nil
}

func validateSmokeRequest(frame protocol.Frame) error {
	if frame.Type != protocol.FrameRequest {
		return fmt.Errorf("request frame type = %q, want %q", frame.Type, protocol.FrameRequest)
	}
	if frame.AccountID != localAccountID {
		return fmt.Errorf("request account_id = %q, want %q", frame.AccountID, localAccountID)
	}
	if frame.Operation != protocol.OperationOpenAIChatCompletions {
		return fmt.Errorf("request operation = %q, want %q", frame.Operation, protocol.OperationOpenAIChatCompletions)
	}
	if frame.Method != http.MethodPost || frame.Path != "/v1/chat/completions" {
		return fmt.Errorf("request method/path = %s %s, want POST /v1/chat/completions", frame.Method, frame.Path)
	}

	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(frame.Body, &body); err != nil {
		return fmt.Errorf("decode request body: %w", err)
	}
	for _, message := range body.Messages {
		if message.Role == "user" && message.Content == localSmokeUserText {
			return nil
		}
	}
	return fmt.Errorf("request body does not contain smoke user message")
}

func writeSmokeResponse(ctx context.Context, conn *websocket.Conn, req protocol.Frame) error {
	var frames []protocol.Frame
	if requestWantsStream(req) {
		frames = []protocol.Frame{
			{
				Type:    protocol.FrameResponseHeaders,
				ID:      req.ID,
				Status:  http.StatusOK,
				Headers: map[string]string{"content-type": "text/event-stream"},
			},
			{
				Type: protocol.FrameResponseChunk,
				ID:   req.ID,
				Data: "data: {\"choices\":[{\"delta\":{\"content\":\"hello from fake daemon\"}}]}\n\n",
			},
			{Type: protocol.FrameResponseChunk, ID: req.ID, Data: "data: [DONE]\n\n"},
			{Type: protocol.FrameResponseEnd, ID: req.ID},
		}
	} else {
		frames = []protocol.Frame{
			{
				Type:    protocol.FrameResponseHeaders,
				ID:      req.ID,
				Status:  http.StatusOK,
				Headers: map[string]string{"content-type": "application/json"},
			},
			{
				Type: protocol.FrameResponseChunk,
				ID:   req.ID,
				Data: `{"choices":[{"message":{"role":"assistant","content":"hello from fake daemon"}}]}`,
			},
			{Type: protocol.FrameResponseEnd, ID: req.ID},
		}
	}
	for _, frame := range frames {
		if err := wsjson.Write(ctx, conn, frame); err != nil {
			return fmt.Errorf("write daemon response: %w", err)
		}
	}
	return nil
}

func requestWantsStream(frame protocol.Frame) bool {
	var body struct {
		Stream bool `json:"stream"`
	}
	return json.Unmarshal(frame.Body, &body) == nil && body.Stream
}

func waitForRoute(ctx context.Context, baseURL string) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		ok, err := routeOnline(ctx, baseURL)
		if ok {
			return nil
		}
		if err != nil {
			lastErr = err
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("wait for route: %w", lastErr)
			}
			return ctx.Err()
		}
	}
}

func routeOnline(ctx context.Context, baseURL string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/routes", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+localAPIToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("routes status = %d; body=%s", resp.StatusCode, raw)
	}
	var body struct {
		Object string `json:"object"`
		Data   []struct {
			DeviceID      string               `json:"device_id"`
			LocalAccounts []string             `json:"local_accounts"`
			Capabilities  []protocol.Operation `json:"capabilities"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return false, err
	}
	for _, route := range body.Data {
		if route.DeviceID == localDeviceID && hasString(route.LocalAccounts, localAccountID) &&
			hasOperation(route.Capabilities, protocol.OperationOpenAIChatCompletions) {
			return true, nil
		}
	}
	return false, nil
}

func runBFFLogin(ctx context.Context, baseURL string) (*http.Cookie, error) {
	noRedirect := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/auth/login/google", nil)
	if err != nil {
		return nil, err
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusFound {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("login status = %d; body=%s", resp.StatusCode, raw)
	}
	location := resp.Header.Get("Location")
	u, err := url.Parse(location)
	if err != nil {
		return nil, fmt.Errorf("parse login redirect: %w", err)
	}
	if !strings.HasSuffix(u.Path, "/auth/web/google") {
		return nil, fmt.Errorf("login redirect path = %q, want /auth/web/google", u.Path)
	}
	state := u.Query().Get("state")
	if state == "" || u.Query().Get("code_challenge") == "" {
		return nil, fmt.Errorf("login redirect missing state or code_challenge")
	}

	callbackURL := baseURL + "/auth/callback?code=smoke-oauth-code&state=" + url.QueryEscape(state)
	callbackReq, err := http.NewRequestWithContext(ctx, http.MethodGet, callbackURL, nil)
	if err != nil {
		return nil, err
	}
	callbackResp, err := noRedirect.Do(callbackReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = callbackResp.Body.Close() }()
	if callbackResp.StatusCode != http.StatusFound {
		raw, _ := io.ReadAll(callbackResp.Body)
		return nil, fmt.Errorf("callback status = %d; body=%s", callbackResp.StatusCode, raw)
	}
	if got := callbackResp.Header.Get("Location"); got != "/chat/" {
		return nil, fmt.Errorf("callback location = %q, want /chat/", got)
	}
	for _, cookie := range callbackResp.Cookies() {
		if cookie.Name == "kittyspace_session" && cookie.Value != "" {
			return cookie, nil
		}
	}
	return nil, fmt.Errorf("session cookie missing")
}

func runStaticSurfaces(ctx context.Context, baseURL string) error {
	checks := []struct {
		path string
		want string
	}{
		{path: "/chat/", want: "space-chat-root"},
		{path: "/kanban/", want: "space-kanban-root"},
		{path: "/assets/chat.js", want: "/chat/api/routes"},
		{path: "/assets/kanban-page.js", want: "/kanban/api/routes"},
	}
	for _, check := range checks {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+check.path, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		raw, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return readErr
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s status = %d; body=%s", check.path, resp.StatusCode, raw)
		}
		if !strings.Contains(string(raw), check.want) {
			return fmt.Errorf("%s missing %q", check.path, check.want)
		}
	}
	return nil
}

func runBFFRoutes(ctx context.Context, baseURL string, cookie *http.Cookie) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/chat/api/routes", nil)
	if err != nil {
		return err
	}
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bff routes status = %d; body=%s", resp.StatusCode, raw)
	}
	var body struct {
		Data []struct {
			DeviceID      string   `json:"device_id"`
			LocalAccounts []string `json:"local_accounts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return err
	}
	for _, route := range body.Data {
		if route.DeviceID == localDeviceID && hasString(route.LocalAccounts, localAccountID) {
			return nil
		}
	}
	return fmt.Errorf("bff route discovery missing %s/%s", localDeviceID, localAccountID)
}

func runBFFChatCompletion(ctx context.Context, baseURL string, cookie *http.Cookie) error {
	body := []byte(`{"model":"kittypaw","messages":[{"role":"user","content":"hello from smoke"}]}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/api/nodes/dev_1/accounts/alice/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.AddCookie(cookie)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bff chat status = %d; body=%s", resp.StatusCode, raw)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		return fmt.Errorf("bff chat content-type = %q, want application/json", got)
	}
	if !strings.Contains(string(raw), "hello from fake daemon") {
		return fmt.Errorf("bff chat body = %q, want fake daemon content", raw)
	}
	return nil
}

func runChatCompletion(ctx context.Context, baseURL string) error {
	body := []byte(`{"model":"kittypaw","stream":true,"messages":[{"role":"user","content":"hello from smoke"}]}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/nodes/dev_1/accounts/alice/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+localAPIToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("chat status = %d; body=%s", resp.StatusCode, raw)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		return fmt.Errorf("chat content-type = %q, want text/event-stream", got)
	}
	text := string(raw)
	if !strings.Contains(text, "hello from fake daemon") || !strings.Contains(text, "data: [DONE]") {
		return fmt.Errorf("chat body = %q, want fake daemon content and done marker", text)
	}
	return nil
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasOperation(values []protocol.Operation, want protocol.Operation) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
