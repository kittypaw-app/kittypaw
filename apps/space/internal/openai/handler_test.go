package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/kittypaw-app/kittyspace/internal/broker"
	"github.com/kittypaw-app/kittyspace/internal/protocol"
)

type staticAuth struct {
	principal Principal
	err       error
}

func (a staticAuth) Authenticate(*http.Request) (Principal, error) {
	return a.principal, a.err
}

type fakeBroker struct {
	req    broker.Request
	frames []protocol.Frame
	err    error
	userID string
	routes []broker.Route
}

func (b *fakeBroker) Request(_ context.Context, req broker.Request) (<-chan protocol.Frame, error) {
	b.req = req
	if b.err != nil {
		return nil, b.err
	}
	ch := make(chan protocol.Frame, len(b.frames))
	for _, frame := range b.frames {
		ch <- frame
	}
	close(ch)
	return ch, nil
}

func (b *fakeBroker) Routes(userID string) []broker.Route {
	b.userID = userID
	return append([]broker.Route(nil), b.routes...)
}

func TestHandlerReturnsUnauthorizedWithoutAPIKey(t *testing.T) {
	h := NewHandler(staticAuth{err: ErrUnauthorized}, &fakeBroker{})
	req := httptest.NewRequest(http.MethodGet, "/nodes/dev_1/v1/models", nil)
	rr := httptest.NewRecorder()

	h.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rr.Code, rr.Body.String())
	}
}

func TestModelsRelaysThroughBroker(t *testing.T) {
	fb := &fakeBroker{frames: []protocol.Frame{
		{Type: protocol.FrameResponseHeaders, ID: "req_1", Status: http.StatusOK, Headers: map[string]string{"content-type": "application/json"}},
		{Type: protocol.FrameResponseChunk, ID: "req_1", Data: `{"object":"list","data":[]}`},
		{Type: protocol.FrameResponseEnd, ID: "req_1"},
	}}
	h := NewHandler(staticAuth{principal: Principal{
		UserID: "user_1", DeviceID: "dev_1", AccountID: "alice", Scopes: []string{"models:read"},
	}}, fb)

	req := httptest.NewRequest(http.MethodGet, "/nodes/dev_1/v1/models", nil)
	req.Header.Set("Authorization", "Bearer kp_test")
	rr := httptest.NewRecorder()
	h.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if fb.req.UserID != "user_1" || fb.req.DeviceID != "dev_1" || fb.req.AccountID != "alice" {
		t.Fatalf("broker request = %+v", fb.req)
	}
	if fb.req.Operation != protocol.OperationOpenAIModels || fb.req.Method != http.MethodGet || fb.req.Path != "/v1/models" {
		t.Fatalf("broker operation/method/path = %s %s %s", fb.req.Operation, fb.req.Method, fb.req.Path)
	}
	if strings.TrimSpace(rr.Body.String()) != `{"object":"list","data":[]}` {
		t.Fatalf("body = %q", rr.Body.String())
	}
}

func TestAccountScopedModelsRouteUsesURLAccount(t *testing.T) {
	fb := &fakeBroker{frames: []protocol.Frame{
		{Type: protocol.FrameResponseHeaders, ID: "req_1", Status: http.StatusOK},
		{Type: protocol.FrameResponseEnd, ID: "req_1"},
	}}
	h := NewHandler(staticAuth{principal: Principal{
		UserID: "user_1", DeviceID: "dev_1", Scopes: []string{"models:read"},
	}}, fb)

	req := httptest.NewRequest(http.MethodGet, "/nodes/dev_1/accounts/bob/v1/models", nil)
	req.Header.Set("Authorization", "Bearer kp_test")
	rr := httptest.NewRecorder()
	h.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if fb.req.UserID != "user_1" || fb.req.DeviceID != "dev_1" || fb.req.AccountID != "bob" {
		t.Fatalf("broker request = %+v, want account bob", fb.req)
	}
	if fb.req.Operation != protocol.OperationOpenAIModels {
		t.Fatalf("operation = %s, want %s", fb.req.Operation, protocol.OperationOpenAIModels)
	}
}

func TestAccountScopedRouteRejectsPrincipalAccountMismatch(t *testing.T) {
	h := NewHandler(staticAuth{principal: Principal{
		UserID: "user_1", DeviceID: "dev_1", AccountID: "alice", Scopes: []string{"models:read"},
	}}, &fakeBroker{})

	req := httptest.NewRequest(http.MethodGet, "/nodes/dev_1/accounts/bob/v1/models", nil)
	req.Header.Set("Authorization", "Bearer kp_test")
	rr := httptest.NewRecorder()
	h.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

func TestAccountScopedRouteAllowsUserScopedPrincipal(t *testing.T) {
	fb := &fakeBroker{frames: []protocol.Frame{
		{Type: protocol.FrameResponseHeaders, ID: "req_1", Status: http.StatusOK},
		{Type: protocol.FrameResponseEnd, ID: "req_1"},
	}}
	h := NewHandler(staticAuth{principal: Principal{
		UserID: "user_1", Scopes: []string{"models:read"},
	}}, fb)

	req := httptest.NewRequest(http.MethodGet, "/nodes/dev_2/accounts/bob/v1/models", nil)
	req.Header.Set("Authorization", "Bearer kp_test")
	rr := httptest.NewRecorder()
	h.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if fb.req.UserID != "user_1" || fb.req.DeviceID != "dev_2" || fb.req.AccountID != "bob" {
		t.Fatalf("broker request = %+v, want user_1/dev_2/bob", fb.req)
	}
}

func TestChatCompletionsStreamingRelaysSSE(t *testing.T) {
	body := map[string]any{
		"model":  "kittypaw",
		"stream": true,
		"messages": []map[string]string{
			{"role": "user", "content": "hello"},
		},
	}
	raw, _ := json.Marshal(body)
	fb := &fakeBroker{frames: []protocol.Frame{
		{Type: protocol.FrameResponseHeaders, ID: "req_1", Status: http.StatusOK, Headers: map[string]string{"content-type": "text/event-stream"}},
		{Type: protocol.FrameResponseChunk, ID: "req_1", Data: "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"},
		{Type: protocol.FrameResponseChunk, ID: "req_1", Data: "data: [DONE]\n\n"},
		{Type: protocol.FrameResponseEnd, ID: "req_1"},
	}}
	h := NewHandler(staticAuth{principal: Principal{
		UserID: "user_1", DeviceID: "dev_1", AccountID: "alice", Scopes: []string{"chat:relay"},
	}}, fb)

	req := httptest.NewRequest(http.MethodPost, "/nodes/dev_1/v1/chat/completions", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer kp_test")
	rr := httptest.NewRecorder()
	h.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content-type = %q, want text/event-stream", got)
	}
	if !strings.Contains(rr.Body.String(), "data: [DONE]") {
		t.Fatalf("body = %q, want SSE done frame", rr.Body.String())
	}
	if fb.req.Operation != protocol.OperationOpenAIChatCompletions || fb.req.Method != http.MethodPost || fb.req.Path != "/v1/chat/completions" {
		t.Fatalf("broker operation/method/path = %s %s %s", fb.req.Operation, fb.req.Method, fb.req.Path)
	}
	if string(fb.req.Body) != string(raw) {
		t.Fatalf("broker body = %s, want %s", fb.req.Body, raw)
	}
}

func TestChatCompletionsPassesThroughDaemonHTTPError(t *testing.T) {
	body := map[string]any{
		"model": "kittypaw",
		"messages": []map[string]string{
			{"role": "user", "content": "hello"},
		},
	}
	raw, _ := json.Marshal(body)
	fb := &fakeBroker{frames: []protocol.Frame{
		{Type: protocol.FrameResponseHeaders, ID: "req_1", Status: http.StatusBadGateway, Headers: map[string]string{"content-type": "application/json"}},
		{Type: protocol.FrameResponseChunk, ID: "req_1", Data: `{"error":"provider bad gateway"}`},
		{Type: protocol.FrameResponseEnd, ID: "req_1"},
	}}
	h := NewHandler(staticAuth{principal: Principal{
		UserID: "user_1", DeviceID: "dev_1", AccountID: "alice", Scopes: []string{"chat:relay"},
	}}, fb)

	req := httptest.NewRequest(http.MethodPost, "/nodes/dev_1/v1/chat/completions", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer kp_test")
	rr := httptest.NewRecorder()
	h.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("X-KittySpace-Relay-Source"); got != "daemon" {
		t.Fatalf("relay source header = %q, want daemon", got)
	}
	if strings.TrimSpace(rr.Body.String()) != `{"error":"provider bad gateway"}` {
		t.Fatalf("body = %q, want daemon/provider error body", rr.Body.String())
	}
}

func TestKittyPawAPIRelaysWhitelistedLocalAPIRequest(t *testing.T) {
	fb := &fakeBroker{frames: []protocol.Frame{
		{Type: protocol.FrameResponseHeaders, ID: "req_1", Status: http.StatusOK, Headers: map[string]string{"content-type": "application/json"}},
		{Type: protocol.FrameResponseChunk, ID: "req_1", Data: `{"projects":[]}`},
		{Type: protocol.FrameResponseEnd, ID: "req_1"},
	}}
	h := NewHandler(staticAuth{principal: Principal{
		UserID: "user_1", DeviceID: "dev_1", AccountID: "alice", Scopes: []string{"chat:relay"},
	}}, fb)

	req := httptest.NewRequest(http.MethodGet, "/nodes/dev_1/accounts/alice/api/v1/projects?archived=0", nil)
	req.Header.Set("Authorization", "Bearer kp_test")
	rr := httptest.NewRecorder()
	h.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if fb.req.UserID != "user_1" || fb.req.DeviceID != "dev_1" || fb.req.AccountID != "alice" {
		t.Fatalf("broker request = %+v", fb.req)
	}
	if fb.req.Operation != protocol.OperationKittyPawAPI || fb.req.Method != http.MethodGet || fb.req.Path != "/api/v1/projects?archived=0" {
		t.Fatalf("broker operation/method/path = %s %s %s", fb.req.Operation, fb.req.Method, fb.req.Path)
	}
	if strings.TrimSpace(rr.Body.String()) != `{"projects":[]}` {
		t.Fatalf("body = %q, want local API response", rr.Body.String())
	}
}

func TestKittyPawAPIRejectsNonAllowlistedLocalAPIPath(t *testing.T) {
	h := NewHandler(staticAuth{principal: Principal{
		UserID: "user_1", DeviceID: "dev_1", AccountID: "alice", Scopes: []string{"chat:relay"},
	}}, &fakeBroker{})

	req := httptest.NewRequest(http.MethodPost, "/nodes/dev_1/accounts/alice/api/v1/chat", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer kp_test")
	rr := httptest.NewRecorder()
	h.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for non-allowlisted local API path; body=%s", rr.Code, rr.Body.String())
	}
}

func TestAccountScopedChatCompletionsRouteUsesURLAccount(t *testing.T) {
	body := map[string]any{
		"model": "kittypaw",
		"messages": []map[string]string{
			{"role": "user", "content": "hello"},
		},
	}
	raw, _ := json.Marshal(body)
	fb := &fakeBroker{frames: []protocol.Frame{
		{Type: protocol.FrameResponseHeaders, ID: "req_1", Status: http.StatusOK},
		{Type: protocol.FrameResponseEnd, ID: "req_1"},
	}}
	h := NewHandler(staticAuth{principal: Principal{
		UserID: "user_1", DeviceID: "dev_1", Scopes: []string{"chat:relay"},
	}}, fb)

	req := httptest.NewRequest(http.MethodPost, "/nodes/dev_1/accounts/bob/v1/chat/completions", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer kp_test")
	rr := httptest.NewRecorder()
	h.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if fb.req.UserID != "user_1" || fb.req.DeviceID != "dev_1" || fb.req.AccountID != "bob" {
		t.Fatalf("broker request = %+v, want account bob", fb.req)
	}
	if fb.req.Operation != protocol.OperationOpenAIChatCompletions || string(fb.req.Body) != string(raw) {
		t.Fatalf("broker request = %+v, body=%s", fb.req, fb.req.Body)
	}
}

func TestHandlerReturnsOfflineWhenBrokerHasNoDevice(t *testing.T) {
	h := NewHandler(staticAuth{principal: Principal{
		UserID: "user_1", DeviceID: "dev_1", AccountID: "alice", Scopes: []string{"models:read"},
	}}, &fakeBroker{err: broker.ErrDeviceOffline})
	req := httptest.NewRequest(http.MethodGet, "/nodes/dev_1/v1/models", nil)
	req.Header.Set("Authorization", "Bearer kp_test")
	rr := httptest.NewRecorder()

	h.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	if got := rr.Header().Get("X-KittySpace-Relay-Source"); got != "relay" {
		t.Fatalf("relay source header = %q, want relay", got)
	}
}

func TestHandlerRejectsAPIKeyForAnotherDevice(t *testing.T) {
	h := NewHandler(staticAuth{principal: Principal{
		UserID: "user_1", DeviceID: "dev_allowed", AccountID: "alice", Scopes: []string{"models:read"},
	}}, &fakeBroker{})
	req := httptest.NewRequest(http.MethodGet, "/nodes/dev_other/v1/models", nil)
	req.Header.Set("Authorization", "Bearer kp_test")
	rr := httptest.NewRecorder()

	h.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestLegacyRouteRequiresPrincipalDefaultAccount(t *testing.T) {
	h := NewHandler(staticAuth{principal: Principal{
		UserID: "user_1", Scopes: []string{"models:read"},
	}}, &fakeBroker{})
	req := httptest.NewRequest(http.MethodGet, "/nodes/dev_1/v1/models", nil)
	req.Header.Set("Authorization", "Bearer kp_test")
	rr := httptest.NewRecorder()

	h.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandlerRejectsMissingOperationScope(t *testing.T) {
	h := NewHandler(staticAuth{principal: Principal{
		UserID: "user_1", DeviceID: "dev_1", AccountID: "alice", Scopes: []string{"models:read"},
	}}, &fakeBroker{})
	req := httptest.NewRequest(http.MethodPost, "/nodes/dev_1/v1/chat/completions", strings.NewReader(`{"messages":[]}`))
	req.Header.Set("Authorization", "Bearer kp_test")
	rr := httptest.NewRecorder()

	h.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

func TestRoutesReturnsOnlineRoutesForAuthenticatedUser(t *testing.T) {
	fb := &fakeBroker{routes: []broker.Route{
		{
			DeviceID:        "dev_1",
			LocalAccountIDs: []string{"alice", "bob"},
			Capabilities:    []protocol.Operation{protocol.OperationOpenAIModels, protocol.OperationOpenAIChatCompletions},
		},
		{
			DeviceID:        "dev_2",
			LocalAccountIDs: []string{"work"},
			Capabilities:    []protocol.Operation{protocol.OperationOpenAIModels},
		},
	}}
	h := NewHandler(staticAuth{principal: Principal{
		UserID: "user_1", Scopes: []string{"models:read"},
	}}, fb)

	req := httptest.NewRequest(http.MethodGet, "/v1/routes", nil)
	req.Header.Set("Authorization", "Bearer kp_test")
	rr := httptest.NewRecorder()
	h.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if fb.userID != "user_1" {
		t.Fatalf("broker Routes userID = %q, want user_1", fb.userID)
	}
	var body routeListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rr.Body.String(), err)
	}
	if body.Object != "list" || len(body.Data) != 2 {
		t.Fatalf("body = %+v, want list with 2 routes", body)
	}
	if body.Data[0].DeviceID != "dev_1" || len(body.Data[0].LocalAccounts) != 2 || body.Data[0].LocalAccounts[0] != "alice" || body.Data[0].Capabilities[1] != protocol.OperationOpenAIChatCompletions {
		t.Fatalf("first route = %+v", body.Data[0])
	}
	if body.Data[1].DeviceID != "dev_2" || len(body.Data[1].LocalAccounts) != 1 || body.Data[1].LocalAccounts[0] != "work" {
		t.Fatalf("second route = %+v", body.Data[1])
	}
}

func TestRoutesFiltersPrincipalDeviceAndAccountRestrictions(t *testing.T) {
	fb := &fakeBroker{routes: []broker.Route{
		{
			DeviceID:        "dev_1",
			LocalAccountIDs: []string{"alice", "bob"},
			Capabilities:    []protocol.Operation{protocol.OperationOpenAIModels},
		},
		{
			DeviceID:        "dev_2",
			LocalAccountIDs: []string{"bob"},
			Capabilities:    []protocol.Operation{protocol.OperationOpenAIModels},
		},
	}}
	h := NewHandler(staticAuth{principal: Principal{
		UserID: "user_1", DeviceID: "dev_1", AccountID: "bob", Scopes: []string{"chat:relay"},
	}}, fb)

	req := httptest.NewRequest(http.MethodGet, "/v1/routes", nil)
	req.Header.Set("Authorization", "Bearer kp_test")
	rr := httptest.NewRecorder()
	h.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var body routeListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rr.Body.String(), err)
	}
	if len(body.Data) != 1 {
		t.Fatalf("routes = %+v, want one filtered route", body.Data)
	}
	if body.Data[0].DeviceID != "dev_1" || len(body.Data[0].LocalAccounts) != 1 || body.Data[0].LocalAccounts[0] != "bob" {
		t.Fatalf("filtered route = %+v, want dev_1/bob", body.Data[0])
	}
}

func TestRoutesRejectsMissingRouteDiscoveryScope(t *testing.T) {
	h := NewHandler(staticAuth{principal: Principal{
		UserID: "user_1", Scopes: []string{"daemon:connect"},
	}}, &fakeBroker{})

	req := httptest.NewRequest(http.MethodGet, "/v1/routes", nil)
	req.Header.Set("Authorization", "Bearer kp_test")
	rr := httptest.NewRecorder()
	h.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

func TestRoutesReturnsUnauthorizedWithoutCredential(t *testing.T) {
	h := NewHandler(staticAuth{err: ErrUnauthorized}, &fakeBroker{})

	req := httptest.NewRequest(http.MethodGet, "/v1/routes", nil)
	rr := httptest.NewRecorder()
	h.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rr.Code, rr.Body.String())
	}
}

func TestRoutesMountUnderChi(t *testing.T) {
	r := chi.NewRouter()
	r.Mount("/", NewHandler(staticAuth{err: errors.New("no auth")}, &fakeBroker{}).Routes())

	req := httptest.NewRequest(http.MethodGet, "/nodes/dev_1/v1/models", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want auth handler response", rr.Code)
	}
}
