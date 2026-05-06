package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFrameRoundTripRequest(t *testing.T) {
	body := json.RawMessage(`{"model":"kittypaw","stream":true}`)
	in := Frame{
		Type:      FrameRequest,
		ID:        "req_123",
		AccountID: "alice",
		Operation: OperationOpenAIChatCompletions,
		Method:    "POST",
		Path:      "/v1/chat/completions",
		Body:      body,
	}

	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out Frame
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if out.Type != FrameRequest || out.ID != "req_123" || out.AccountID != "alice" || out.Operation != OperationOpenAIChatCompletions {
		t.Fatalf("round trip frame = %+v", out)
	}
	if string(out.Body) != string(body) {
		t.Fatalf("body = %s, want %s", out.Body, body)
	}
}

func TestValidateHelloRequiresDeviceAndAccount(t *testing.T) {
	validHello := Frame{
		Type:            FrameHello,
		DeviceID:        "dev_123",
		LocalAccounts:   []string{"alice"},
		ProtocolVersion: ProtocolVersion1,
		Capabilities:    []Operation{OperationOpenAIModels},
	}
	tests := []struct {
		name  string
		frame Frame
		want  string
	}{
		{
			name:  "missing device",
			frame: Frame{Type: FrameHello, LocalAccounts: []string{"alice"}, ProtocolVersion: ProtocolVersion1, Capabilities: []Operation{OperationOpenAIModels}},
			want:  "device_id is required",
		},
		{
			name:  "missing account",
			frame: Frame{Type: FrameHello, DeviceID: "dev_123", ProtocolVersion: ProtocolVersion1, Capabilities: []Operation{OperationOpenAIModels}},
			want:  "at least one local account is required",
		},
		{
			name:  "missing protocol version",
			frame: Frame{Type: FrameHello, DeviceID: "dev_123", LocalAccounts: []string{"alice"}, Capabilities: []Operation{OperationOpenAIModels}},
			want:  "protocol_version must be 1",
		},
		{
			name:  "missing capabilities",
			frame: Frame{Type: FrameHello, DeviceID: "dev_123", LocalAccounts: []string{"alice"}, ProtocolVersion: ProtocolVersion1},
			want:  "at least one capability is required",
		},
		{
			name:  "unknown capability",
			frame: Frame{Type: FrameHello, DeviceID: "dev_123", LocalAccounts: []string{"alice"}, ProtocolVersion: ProtocolVersion1, Capabilities: []Operation{"unknown"}},
			want:  "capability is not supported",
		},
		{
			name:  "valid hello",
			frame: validHello,
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.frame.Validate()
			if tt.want == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestValidateRequestRestrictsOperations(t *testing.T) {
	allowed := []Operation{OperationOpenAIModels, OperationOpenAIChatCompletions}
	for _, operation := range allowed {
		method, path, ok := HTTPForOperation(operation)
		if !ok {
			t.Fatalf("HTTPForOperation(%s) ok = false", operation)
		}
		frame := Frame{
			Type:      FrameRequest,
			ID:        "req_allowed",
			AccountID: "alice",
			Operation: operation,
			Method:    method,
			Path:      path,
		}
		if err := frame.Validate(); err != nil {
			t.Fatalf("Validate(%s) error = %v, want nil", operation, err)
		}
	}

	frame := Frame{
		Type:      FrameRequest,
		ID:        "req_forbidden",
		AccountID: "alice",
		Operation: "unknown",
	}
	if err := frame.Validate(); err == nil || !strings.Contains(err.Error(), "operation is not supported") {
		t.Fatalf("Validate() error = %v, want forbidden operation error", err)
	}
}

func TestValidateRequestRejectsMismatchedHTTPCompatibilityFields(t *testing.T) {
	frame := Frame{
		Type:      FrameRequest,
		ID:        "req_mismatch",
		AccountID: "alice",
		Operation: OperationOpenAIModels,
		Method:    "POST",
		Path:      "/v1/chat/completions",
	}
	if err := frame.Validate(); err == nil || !strings.Contains(err.Error(), "method/path do not match operation") {
		t.Fatalf("Validate() error = %v, want method/path mismatch error", err)
	}
}

func TestValidateFrameRejectsOversizedID(t *testing.T) {
	frame := Frame{
		Type:      FrameRequest,
		ID:        strings.Repeat("x", MaxIDLength+1),
		AccountID: "alice",
		Operation: OperationOpenAIModels,
	}
	if err := frame.Validate(); err == nil || !strings.Contains(err.Error(), "id exceeds maximum length") {
		t.Fatalf("Validate() error = %v, want oversized id error", err)
	}
}
