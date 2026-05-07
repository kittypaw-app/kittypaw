package chatrelay

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestNewHelloFramePinsProtocolVersionAndCapabilities(t *testing.T) {
	got := NewHelloFrame("dev_1", []string{"alice"}, "0.1.5", nil)

	if got.Type != FrameHello {
		t.Fatalf("Type = %q, want %q", got.Type, FrameHello)
	}
	if got.DeviceID != "dev_1" {
		t.Fatalf("DeviceID = %q", got.DeviceID)
	}
	if !reflect.DeepEqual(got.LocalAccounts, []string{"alice"}) {
		t.Fatalf("LocalAccounts = %#v", got.LocalAccounts)
	}
	if got.DaemonVersion != "0.1.5" {
		t.Fatalf("DaemonVersion = %q", got.DaemonVersion)
	}
	if got.ProtocolVersion != ProtocolVersion {
		t.Fatalf("ProtocolVersion = %q, want %q", got.ProtocolVersion, ProtocolVersion)
	}
	if !reflect.DeepEqual(got.Capabilities, DefaultCapabilities()) {
		t.Fatalf("Capabilities = %#v, want %#v", got.Capabilities, DefaultCapabilities())
	}

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["protocol_version"] != "1" {
		t.Fatalf("hello JSON protocol_version = %#v", decoded["protocol_version"])
	}
	if decoded["daemon_version"] != "0.1.5" {
		t.Fatalf("hello JSON daemon_version = %#v", decoded["daemon_version"])
	}
}

func TestNewHelloFramePreservesExplicitEmptyCapabilities(t *testing.T) {
	got := NewHelloFrame("dev_1", []string{"alice"}, "0.1.5", []string{})
	if len(got.Capabilities) != 0 {
		t.Fatalf("Capabilities = %#v, want explicit empty capability set", got.Capabilities)
	}
}

func TestOperationSupportIsOperationBased(t *testing.T) {
	for _, op := range []string{OperationOpenAIModels, OperationOpenAIChatCompletions, OperationKittyPawAPI} {
		if !SupportedOperation(op) {
			t.Fatalf("SupportedOperation(%q) = false, want true", op)
		}
	}

	for _, op := range []string{"GET /v1/models", "/v1/chat/completions", "settings.update", ""} {
		if SupportedOperation(op) {
			t.Fatalf("SupportedOperation(%q) = true, want false", op)
		}
	}
}

func TestDefaultCapabilitiesIncludeLocalAPIForHostedKanban(t *testing.T) {
	got := DefaultCapabilities()
	for _, want := range []string{OperationOpenAIModels, OperationOpenAIChatCompletions, OperationKittyPawAPI} {
		found := false
		for _, capability := range got {
			if capability == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("DefaultCapabilities() = %#v, missing %q", got, want)
		}
	}
}

func TestRequestFrameCarriesLocalAPIMethodAndPath(t *testing.T) {
	raw, err := json.Marshal(RequestFrame{
		Type:      FrameRequest,
		ID:        "req_api",
		Operation: OperationKittyPawAPI,
		AccountID: "alice",
		Method:    "GET",
		Path:      "/api/v1/projects?archived=0",
	})
	if err != nil {
		t.Fatal(err)
	}
	var got RequestFrame
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Method != "GET" || got.Path != "/api/v1/projects?archived=0" {
		t.Fatalf("request frame = %#v, want method/path preserved", got)
	}
}

func TestScopeVocabularyMatchesKittyAPISpec(t *testing.T) {
	got := []string{ScopeChatRelay, ScopeModelsRead, ScopeDaemonConnect}
	want := []string{"chat:relay", "models:read", "daemon:connect"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scopes = %#v, want %#v", got, want)
	}
}

func TestResponseFramesMarshalRelayJSONShape(t *testing.T) {
	headers := ResponseHeadersFrame{
		Type:    FrameResponseHeaders,
		ID:      "req_1",
		Status:  200,
		Headers: map[string]string{"content-type": "text/event-stream"},
	}
	raw, err := json.Marshal(headers)
	if err != nil {
		t.Fatal(err)
	}
	wantHeaders := `{"type":"response_headers","id":"req_1","status":200,"headers":{"content-type":"text/event-stream"}}`
	if string(raw) != wantHeaders {
		t.Fatalf("response headers JSON = %s, want %s", raw, wantHeaders)
	}

	chunk := ResponseChunkFrame{
		Type: FrameResponseChunk,
		ID:   "req_1",
		Data: "data: hello\n\n",
	}
	raw, err = json.Marshal(chunk)
	if err != nil {
		t.Fatal(err)
	}
	wantChunk := `{"type":"response_chunk","id":"req_1","data":"data: hello\n\n"}`
	if string(raw) != wantChunk {
		t.Fatalf("response chunk JSON = %s, want %s", raw, wantChunk)
	}
}
