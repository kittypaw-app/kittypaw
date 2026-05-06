package protocol

import (
	"encoding/json"
	"fmt"
	"net/http"
)

const MaxIDLength = 128

type FrameType string
type Operation string

const (
	FrameHello           FrameType = "hello"
	FrameRequest         FrameType = "request"
	FrameResponseHeaders FrameType = "response_headers"
	FrameResponseChunk   FrameType = "response_chunk"
	FrameResponseEnd     FrameType = "response_end"
	FrameError           FrameType = "error"
	FramePing            FrameType = "ping"
	FramePong            FrameType = "pong"
)

const (
	OperationOpenAIModels          Operation = "openai.models"
	OperationOpenAIChatCompletions Operation = "openai.chat_completions"

	ProtocolVersion1 = "1"
)

type Frame struct {
	Type            FrameType         `json:"type"`
	ID              string            `json:"id,omitempty"`
	DeviceID        string            `json:"device_id,omitempty"`
	AccountID       string            `json:"account_id,omitempty"`
	LocalAccounts   []string          `json:"local_accounts,omitempty"`
	DaemonVersion   string            `json:"daemon_version,omitempty"`
	ProtocolVersion string            `json:"protocol_version,omitempty"`
	Capabilities    []Operation       `json:"capabilities,omitempty"`
	Version         string            `json:"version,omitempty"`
	Operation       Operation         `json:"operation,omitempty"`
	Method          string            `json:"method,omitempty"`
	Path            string            `json:"path,omitempty"`
	Status          int               `json:"status,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	Body            json.RawMessage   `json:"body,omitempty"`
	Data            string            `json:"data,omitempty"`
	Code            string            `json:"code,omitempty"`
	Message         string            `json:"message,omitempty"`
}

func (f Frame) Validate() error {
	if f.Type == "" {
		return fmt.Errorf("type is required")
	}
	if len(f.ID) > MaxIDLength {
		return fmt.Errorf("id exceeds maximum length")
	}

	switch f.Type {
	case FrameHello:
		if f.DeviceID == "" {
			return fmt.Errorf("device_id is required")
		}
		if len(f.LocalAccounts) == 0 {
			return fmt.Errorf("at least one local account is required")
		}
		for _, accountID := range f.LocalAccounts {
			if accountID == "" {
				return fmt.Errorf("local account id is required")
			}
		}
		if f.ProtocolVersion != ProtocolVersion1 {
			return fmt.Errorf("protocol_version must be 1")
		}
		if len(f.Capabilities) == 0 {
			return fmt.Errorf("at least one capability is required")
		}
		for _, capability := range f.Capabilities {
			if !AllowedOperation(capability) {
				return fmt.Errorf("capability is not supported")
			}
		}
	case FrameRequest:
		if f.ID == "" {
			return fmt.Errorf("id is required")
		}
		if f.AccountID == "" {
			return fmt.Errorf("account_id is required")
		}
		if !AllowedOperation(f.Operation) {
			return fmt.Errorf("operation is not supported")
		}
		if f.Method != "" || f.Path != "" {
			method, path, ok := HTTPForOperation(f.Operation)
			if !ok || f.Method != method || f.Path != path {
				return fmt.Errorf("method/path do not match operation")
			}
		}
	case FrameResponseHeaders:
		if f.ID == "" {
			return fmt.Errorf("id is required")
		}
		if f.Status < 100 || f.Status > 599 {
			return fmt.Errorf("status is invalid")
		}
	case FrameResponseChunk, FrameResponseEnd, FrameError:
		if f.ID == "" {
			return fmt.Errorf("id is required")
		}
	case FramePing, FramePong:
	default:
		return fmt.Errorf("unknown frame type %q", f.Type)
	}

	return nil
}

func AllowedOperation(operation Operation) bool {
	switch operation {
	case OperationOpenAIModels, OperationOpenAIChatCompletions:
		return true
	default:
		return false
	}
}

func HTTPForOperation(operation Operation) (method string, path string, ok bool) {
	switch operation {
	case OperationOpenAIModels:
		return http.MethodGet, "/v1/models", true
	case OperationOpenAIChatCompletions:
		return http.MethodPost, "/v1/chat/completions", true
	default:
		return "", "", false
	}
}

func AllowedRelayPath(path string) bool {
	switch path {
	case "/v1/models", "/v1/chat/completions":
		return true
	default:
		return false
	}
}
