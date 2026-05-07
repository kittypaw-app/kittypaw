package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/kittypaw-app/kittyspace/internal/broker"
	"github.com/kittypaw-app/kittyspace/internal/protocol"
)

const maxRequestBodyBytes = 1 << 20

var ErrUnauthorized = errors.New("unauthorized")

type Principal struct {
	UserID    string
	DeviceID  string
	AccountID string
	Scopes    []string
}

type Authenticator interface {
	Authenticate(r *http.Request) (Principal, error)
}

type Broker interface {
	Request(ctx context.Context, req broker.Request) (<-chan protocol.Frame, error)
	Routes(userID string) []broker.Route
}

type Handler struct {
	auth   Authenticator
	broker Broker
}

type relayLogFields struct {
	UserID    string
	DeviceID  string
	AccountID string
	Operation protocol.Operation
	Method    string
	Path      string
}

func NewHandler(auth Authenticator, b Broker) *Handler {
	return &Handler{auth: auth, broker: b}
}

func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/v1/routes", h.handleRoutes)
	r.Get("/nodes/{device_id}/accounts/{account_id}/v1/models", h.handleModels)
	r.Post("/nodes/{device_id}/accounts/{account_id}/v1/chat/completions", h.handleChatCompletions)
	h.mountKittyPawAPIRoutes(r, "/nodes/{device_id}/accounts/{account_id}/api/*")
	r.Get("/nodes/{device_id}/v1/models", h.handleModels)
	r.Post("/nodes/{device_id}/v1/chat/completions", h.handleChatCompletions)
	h.mountKittyPawAPIRoutes(r, "/nodes/{device_id}/api/*")
	return r
}

func (h *Handler) mountKittyPawAPIRoutes(r chi.Router, pattern string) {
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPatch} {
		r.MethodFunc(method, pattern, h.handleKittyPawAPI)
	}
}

type routeListResponse struct {
	Object string          `json:"object"`
	Data   []routeResponse `json:"data"`
}

type routeResponse struct {
	DeviceID      string               `json:"device_id"`
	LocalAccounts []string             `json:"local_accounts"`
	Capabilities  []protocol.Operation `json:"capabilities"`
}

func (h *Handler) handleRoutes(w http.ResponseWriter, r *http.Request) {
	if h.auth == nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	principal, err := h.auth.Authenticate(r)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !principal.HasScope("models:read") && !principal.HasScope("chat:relay") {
		writeJSONError(w, http.StatusForbidden, "forbidden")
		return
	}
	if h.broker == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "relay broker unavailable")
		return
	}

	response := routeListResponse{
		Object: "list",
		Data:   routesForPrincipal(h.broker.Routes(principal.UserID), principal),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request) {
	h.relay(w, r, protocol.OperationOpenAIModels, http.MethodGet, "/v1/models", nil)
}

func (h *Handler) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBodyBytes))
	if err != nil {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	h.relay(w, r, protocol.OperationOpenAIChatCompletions, http.MethodPost, "/v1/chat/completions", body)
}

func (h *Handler) handleKittyPawAPI(w http.ResponseWriter, r *http.Request) {
	localPath := "/api/" + strings.TrimPrefix(chi.URLParam(r, "*"), "/")
	if r.URL.RawQuery != "" {
		localPath += "?" + r.URL.RawQuery
	}
	if !protocol.AllowedKittyPawAPIRequest(r.Method, localPath) {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBodyBytes))
	if err != nil {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	h.relay(w, r, protocol.OperationKittyPawAPI, r.Method, localPath, body)
}

func (h *Handler) relay(w http.ResponseWriter, r *http.Request, operation protocol.Operation, method, path string, body []byte) {
	if h.auth == nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	principal, err := h.auth.Authenticate(r)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	deviceID := chi.URLParam(r, "device_id")
	if principal.DeviceID != "" && principal.DeviceID != deviceID {
		writeJSONError(w, http.StatusForbidden, "forbidden")
		return
	}
	if scope := requiredScope(operation); scope != "" && !principal.HasScope(scope) {
		writeJSONError(w, http.StatusForbidden, "forbidden")
		return
	}
	if h.broker == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "relay broker unavailable")
		return
	}
	accountID := chi.URLParam(r, "account_id")
	if accountID != "" && principal.AccountID != "" && principal.AccountID != accountID {
		writeJSONError(w, http.StatusForbidden, "forbidden")
		return
	}
	if accountID == "" {
		accountID = principal.AccountID
	}
	if accountID == "" {
		writeJSONError(w, http.StatusBadRequest, "account_id is required")
		return
	}

	fields := relayLogFields{
		UserID:    principal.UserID,
		DeviceID:  deviceID,
		AccountID: accountID,
		Operation: operation,
		Method:    method,
		Path:      path,
	}
	stream, err := h.broker.Request(r.Context(), broker.Request{
		UserID:    principal.UserID,
		DeviceID:  deviceID,
		AccountID: accountID,
		Operation: operation,
		Method:    method,
		Path:      path,
		Body:      body,
	})
	if err != nil {
		slog.Warn("space openai relay request rejected",
			"user_id", fields.UserID,
			"device_id", fields.DeviceID,
			"account_id", fields.AccountID,
			"operation", fields.Operation,
			"method", fields.Method,
			"path", fields.Path,
			"error", err,
		)
		w.Header().Set("X-KittySpace-Relay-Source", "relay")
		switch {
		case errors.Is(err, broker.ErrDeviceOffline):
			writeJSONError(w, http.StatusServiceUnavailable, "device offline")
		case errors.Is(err, broker.ErrForbidden):
			writeJSONError(w, http.StatusForbidden, "forbidden")
		case errors.Is(err, broker.ErrBackpressure):
			writeJSONError(w, http.StatusTooManyRequests, "too many in-flight requests")
		case errors.Is(err, broker.ErrUnsupportedOperation):
			writeJSONError(w, http.StatusBadGateway, "operation unsupported by device")
		default:
			writeJSONError(w, http.StatusBadGateway, err.Error())
		}
		return
	}

	h.writeRelayStream(w, stream, fields)
}

func (h *Handler) writeRelayStream(w http.ResponseWriter, stream <-chan protocol.Frame, fields relayLogFields) {
	headerWritten := false
	writeHeaders := func(status int, headers map[string]string) {
		if headerWritten {
			return
		}
		if status == 0 {
			status = http.StatusOK
		}
		w.Header().Set("X-KittySpace-Relay-Source", "daemon")
		for key, value := range headers {
			if strings.EqualFold(key, "content-type") {
				w.Header().Set("Content-Type", value)
				continue
			}
			w.Header().Set(key, value)
		}
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json")
		}
		if status >= http.StatusBadRequest {
			slog.Warn("space openai relay downstream response",
				"user_id", fields.UserID,
				"device_id", fields.DeviceID,
				"account_id", fields.AccountID,
				"operation", fields.Operation,
				"method", fields.Method,
				"path", fields.Path,
				"status", status,
			)
		}
		w.WriteHeader(status)
		headerWritten = true
	}

	for frame := range stream {
		switch frame.Type {
		case protocol.FrameResponseHeaders:
			writeHeaders(frame.Status, frame.Headers)
		case protocol.FrameResponseChunk:
			writeHeaders(http.StatusOK, nil)
			if _, err := io.WriteString(w, frame.Data); err != nil {
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		case protocol.FrameResponseEnd:
			writeHeaders(http.StatusOK, nil)
			return
		case protocol.FrameError:
			slog.Warn("space openai relay daemon error",
				"user_id", fields.UserID,
				"device_id", fields.DeviceID,
				"account_id", fields.AccountID,
				"operation", fields.Operation,
				"method", fields.Method,
				"path", fields.Path,
				"code", frame.Code,
				"message", frame.Message,
			)
			if !headerWritten {
				w.Header().Set("X-KittySpace-Relay-Source", "relay")
				writeJSONError(w, http.StatusBadGateway, frame.Message)
			}
			return
		}
	}
	if !headerWritten {
		w.Header().Set("X-KittySpace-Relay-Source", "relay")
		writeJSONError(w, http.StatusBadGateway, "relay stream ended without response")
	}
}

type StaticTokenAuthenticator struct {
	Token     string
	Principal Principal
}

func (a StaticTokenAuthenticator) Authenticate(r *http.Request) (Principal, error) {
	token := bearerToken(r)
	if token == "" || a.Token == "" || token != a.Token {
		return Principal{}, ErrUnauthorized
	}
	return a.Principal, nil
}

func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	if key := r.Header.Get("x-api-key"); key != "" {
		return key
	}
	return ""
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (p Principal) Validate() error {
	if p.UserID == "" {
		return fmt.Errorf("user_id is required")
	}
	if p.DeviceID == "" {
		return fmt.Errorf("device_id is required")
	}
	if p.AccountID == "" {
		return fmt.Errorf("account_id is required")
	}
	return nil
}

func (p Principal) HasScope(want string) bool {
	for _, scope := range p.Scopes {
		if scope == want {
			return true
		}
	}
	return false
}

func requiredScope(operation protocol.Operation) string {
	switch operation {
	case protocol.OperationOpenAIModels:
		return "models:read"
	case protocol.OperationOpenAIChatCompletions, protocol.OperationKittyPawAPI:
		return "chat:relay"
	default:
		return ""
	}
}

func routesForPrincipal(routes []broker.Route, principal Principal) []routeResponse {
	response := make([]routeResponse, 0, len(routes))
	for _, route := range routes {
		if principal.DeviceID != "" && route.DeviceID != principal.DeviceID {
			continue
		}
		accounts := append([]string(nil), route.LocalAccountIDs...)
		if principal.AccountID != "" {
			if !containsString(accounts, principal.AccountID) {
				continue
			}
			accounts = []string{principal.AccountID}
		}
		response = append(response, routeResponse{
			DeviceID:      route.DeviceID,
			LocalAccounts: accounts,
			Capabilities:  append([]protocol.Operation(nil), route.Capabilities...),
		})
	}
	return response
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
