package chatrelay

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"strings"
)

const ProtocolVersion = "1"

const (
	OperationOpenAIModels          = "openai.models"
	OperationOpenAIChatCompletions = "openai.chat_completions"
	OperationKittyPawAPI           = "kittypaw.api"
)

const (
	ScopeChatRelay     = "chat:relay"
	ScopeModelsRead    = "models:read"
	ScopeDaemonConnect = "daemon:connect"
)

const (
	FrameHello           = "hello"
	FrameRequest         = "request"
	FrameResponseHeaders = "response_headers"
	FrameResponseChunk   = "response_chunk"
	FrameResponseEnd     = "response_end"
	FrameError           = "error"
)

type HelloFrame struct {
	Type            string   `json:"type"`
	DeviceID        string   `json:"device_id"`
	LocalAccounts   []string `json:"local_accounts"`
	DaemonVersion   string   `json:"daemon_version"`
	ProtocolVersion string   `json:"protocol_version"`
	Capabilities    []string `json:"capabilities"`
}

type RequestFrame struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Operation string          `json:"operation"`
	AccountID string          `json:"account_id"`
	Method    string          `json:"method,omitempty"`
	Path      string          `json:"path,omitempty"`
	Body      json.RawMessage `json:"body,omitempty"`
}

type ResponseHeadersFrame struct {
	Type    string            `json:"type"`
	ID      string            `json:"id"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
}

type ResponseChunkFrame struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Data string `json:"data"`
}

type ResponseEndFrame struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type ErrorFrame struct {
	Type    string `json:"type"`
	ID      string `json:"id,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

func DefaultCapabilities() []string {
	return []string{OperationOpenAIModels, OperationOpenAIChatCompletions, OperationKittyPawAPI}
}

func NewHelloFrame(deviceID string, localAccounts []string, daemonVersion string, capabilities []string) HelloFrame {
	return HelloFrame{
		Type:            FrameHello,
		DeviceID:        deviceID,
		LocalAccounts:   append([]string(nil), localAccounts...),
		DaemonVersion:   daemonVersion,
		ProtocolVersion: ProtocolVersion,
		Capabilities:    EffectiveCapabilities(capabilities),
	}
}

func EffectiveCapabilities(capabilities []string) []string {
	if capabilities == nil {
		return DefaultCapabilities()
	}
	return append([]string(nil), capabilities...)
}

func SupportedOperation(operation string) bool {
	switch operation {
	case OperationOpenAIModels, OperationOpenAIChatCompletions, OperationKittyPawAPI:
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
		if len(parts) == 3 && parts[2] == "board" {
			return method == http.MethodGet
		}
		if len(parts) == 3 && parts[2] == "brief-drafts" {
			return method == http.MethodGet || method == http.MethodPost
		}
		if len(parts) == 4 && parts[2] == "brief-drafts" {
			return method == http.MethodPatch
		}
		if len(parts) == 5 && parts[2] == "brief-drafts" && parts[4] == "commit" {
			return method == http.MethodPost
		}
	case "tickets":
		return allowedKittyPawTicketsRequest(method, parts)
	case "jobs":
		return allowedKittyPawJobsRequest(method, parts)
	case "drivers":
		return allowedKittyPawDriversRequest(method, parts)
	}
	return false
}

func allowedKittyPawTicketsRequest(method string, parts []string) bool {
	if len(parts) == 1 {
		return method == http.MethodGet || method == http.MethodPost
	}
	if len(parts) == 2 {
		return method == http.MethodGet
	}
	if len(parts) == 3 && parts[2] == "actions" {
		return method == http.MethodPost
	}
	if len(parts) == 3 && parts[2] == "archive" {
		return method == http.MethodPost
	}
	if len(parts) == 3 && parts[2] == "jobs" {
		return method == http.MethodGet
	}
	if len(parts) == 4 && parts[2] == "jobs" && parts[3] == "plan" {
		return method == http.MethodPost
	}
	return false
}

func allowedKittyPawJobsRequest(method string, parts []string) bool {
	if len(parts) == 2 {
		return method == http.MethodGet
	}
	if len(parts) != 3 {
		return false
	}
	switch parts[2] {
	case "approve", "start", "cancel":
		return method == http.MethodPost
	case "logs":
		return method == http.MethodGet
	}
	return false
}

func allowedKittyPawDriversRequest(method string, parts []string) bool {
	if len(parts) == 1 {
		return method == http.MethodGet || method == http.MethodPost
	}
	if len(parts) == 2 {
		return method == http.MethodPatch
	}
	return false
}
