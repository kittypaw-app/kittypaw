package protocol

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
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
	OperationKittyPawAPI           Operation = "kittypaw.api"

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
		if f.Operation == OperationKittyPawAPI {
			if !AllowedKittyPawAPIRequest(f.Method, f.Path) {
				return fmt.Errorf("method/path do not match operation")
			}
		} else if f.Method != "" || f.Path != "" {
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
	case OperationOpenAIModels, OperationOpenAIChatCompletions, OperationKittyPawAPI:
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

func AllowedKittyPawAPIRequest(method string, requestPath string) bool {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" || requestPath == "" {
		return false
	}
	u, err := url.ParseRequestURI(requestPath)
	if err != nil || u.Host != "" || u.Scheme != "" {
		return false
	}
	clean := path.Clean(u.Path)
	if clean != u.Path || strings.Contains(u.Path, "\x00") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] != "api" {
		return false
	}
	if len(parts) == 3 && parts[1] == "settings" && parts[2] == "workspaces" {
		return method == http.MethodGet
	}
	if len(parts) < 3 || parts[1] != "v1" {
		return false
	}
	return allowedKittyPawV1Request(method, parts[2:])
}

func allowedKittyPawV1Request(method string, parts []string) bool {
	switch parts[0] {
	case "projects":
		if len(parts) == 1 {
			return method == http.MethodGet || method == http.MethodPost
		}
		if len(parts) == 2 {
			return method == http.MethodGet
		}
		if len(parts) == 3 && (parts[2] == "boards" || parts[2] == "milestones") {
			if parts[2] == "milestones" {
				return method == http.MethodGet || method == http.MethodPost
			}
			return method == http.MethodGet
		}
	case "kanban":
		return allowedKittyPawKanbanRequest(method, parts[1:])
	}
	return false
}

func allowedKittyPawKanbanRequest(method string, parts []string) bool {
	if len(parts) == 2 && parts[0] == "runs" && parts[1] == "stale" {
		return method == http.MethodGet
	}
	if len(parts) == 1 && parts[0] == "tasks" {
		return method == http.MethodGet || method == http.MethodPost
	}
	if len(parts) < 2 || parts[0] != "tasks" {
		return false
	}
	if len(parts) == 2 {
		return method == http.MethodGet || method == http.MethodPatch
	}
	if len(parts) != 3 {
		return false
	}
	switch parts[2] {
	case "claim", "heartbeat", "complete", "fail", "cancel", "reclaim", "archive", "block", "unblock", "links":
		return method == http.MethodPost
	case "comments":
		return method == http.MethodGet || method == http.MethodPost
	case "runs":
		return method == http.MethodGet
	default:
		return false
	}
}
