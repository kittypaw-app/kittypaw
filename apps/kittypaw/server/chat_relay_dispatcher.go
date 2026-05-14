package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/engine"
	"github.com/jinto/kittypaw/remote/chatrelay"
)

type chatRelayDispatcher struct {
	server *Server
}

func NewChatRelayDispatcher(s *Server) chatrelay.Dispatcher {
	return &chatRelayDispatcher{server: s}
}

func (d *chatRelayDispatcher) Dispatch(ctx context.Context, req chatrelay.RequestFrame) (chatrelay.DispatchResult, error) {
	if d == nil || d.server == nil {
		return jsonDispatch(http.StatusServiceUnavailable, map[string]any{"error": "chat relay dispatcher unavailable"}), nil
	}
	acct, err := d.server.requestAccountByID(req.AccountID)
	if err != nil {
		return jsonDispatch(http.StatusNotFound, map[string]any{"error": "account not found"}), nil
	}
	var result chatrelay.DispatchResult
	switch req.Operation {
	case chatrelay.OperationOpenAIModels:
		result = d.dispatchModels(acct)
	case chatrelay.OperationOpenAIChatCompletions:
		var err error
		result, err = d.dispatchChatCompletions(ctx, acct, req)
		if err != nil {
			slog.Warn("chat relay dispatch error",
				"request_id", req.ID,
				"account_id", req.AccountID,
				"operation", req.Operation,
				"error", err,
			)
			return chatrelay.DispatchResult{}, err
		}
	case chatrelay.OperationKittyPawAPI:
		var err error
		result, err = d.dispatchKittyPawAPI(ctx, acct, req)
		if err != nil {
			slog.Warn("chat relay dispatch error",
				"request_id", req.ID,
				"account_id", req.AccountID,
				"operation", req.Operation,
				"method", req.Method,
				"path", req.Path,
				"error", err,
			)
			return chatrelay.DispatchResult{}, err
		}
	default:
		return chatrelay.DispatchResult{}, chatrelay.DispatchError{
			Code:    "unsupported_operation",
			Message: "unsupported chat relay operation",
		}
	}
	status := result.Status
	if status == 0 {
		status = http.StatusOK
	}
	logFn := slog.Info
	if status >= http.StatusBadRequest {
		logFn = slog.Warn
	}
	logFn("chat relay dispatch result",
		"request_id", req.ID,
		"account_id", req.AccountID,
		"operation", req.Operation,
		"status", status,
	)
	return result, nil
}

func (d *chatRelayDispatcher) dispatchKittyPawAPI(
	ctx context.Context,
	acct *requestAccount,
	req chatrelay.RequestFrame,
) (chatrelay.DispatchResult, error) {
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if !chatrelay.AllowedKittyPawAPIRequest(method, req.Path) {
		return chatrelay.DispatchResult{}, chatrelay.DispatchError{
			Code:    "unsupported_local_api_path",
			Message: "local API path is not supported by the hosted relay",
		}
	}

	internalReq, err := http.NewRequestWithContext(ctx, method, "http://kittypaw.local"+req.Path, bytes.NewReader(req.Body))
	if err != nil {
		return chatrelay.DispatchResult{}, err
	}
	internalReq.Header.Set("Accept", "application/json")
	if len(req.Body) > 0 {
		internalReq.Header.Set("Content-Type", "application/json")
	}
	internalReq.AddCookie(d.server.newWebSessionCookie(internalReq, acct.ID, time.Now().Add(webSessionTTL)))

	rr := newRelayResponseRecorder()
	d.server.setupRoutes().ServeHTTP(rr, internalReq)
	headers := map[string]string{}
	if contentType := rr.Header().Get("Content-Type"); contentType != "" {
		headers["content-type"] = contentType
	}
	return chatrelay.DispatchResult{
		Status:  rr.StatusCode(),
		Headers: headers,
		Body:    rr.body.Bytes(),
	}, nil
}

type relayResponseRecorder struct {
	header http.Header
	body   bytes.Buffer
	code   int
}

func newRelayResponseRecorder() *relayResponseRecorder {
	return &relayResponseRecorder{header: make(http.Header)}
}

func (r *relayResponseRecorder) Header() http.Header {
	return r.header
}

func (r *relayResponseRecorder) WriteHeader(status int) {
	if r.code != 0 {
		return
	}
	r.code = status
}

func (r *relayResponseRecorder) Write(data []byte) (int, error) {
	if r.code == 0 {
		r.code = http.StatusOK
	}
	return r.body.Write(data)
}

func (r *relayResponseRecorder) StatusCode() int {
	if r.code == 0 {
		return http.StatusOK
	}
	return r.code
}

func (d *chatRelayDispatcher) dispatchModels(acct *requestAccount) chatrelay.DispatchResult {
	cfg := acct.Runtime.Config
	models := make([]map[string]any, 0, len(cfg.LLM.Models)+len(cfg.Models)+1)
	for _, model := range cfg.LLM.Models {
		id := strings.TrimSpace(model.ModelID())
		if id == "" || modelIDExists(models, id) {
			continue
		}
		models = append(models, openAIModelItem(id))
	}
	for _, model := range cfg.Models {
		id := strings.TrimSpace(model.ModelID())
		if id == "" || modelIDExists(models, id) {
			continue
		}
		models = append(models, openAIModelItem(id))
	}
	if len(models) == 0 {
		if id := strings.TrimSpace(cfg.LLM.Model); id != "" {
			models = append(models, openAIModelItem(id))
		}
	}
	return jsonDispatch(http.StatusOK, map[string]any{
		"object": "list",
		"data":   models,
	})
}

func (d *chatRelayDispatcher) dispatchChatCompletions(
	ctx context.Context,
	acct *requestAccount,
	req chatrelay.RequestFrame,
) (chatrelay.DispatchResult, error) {
	var body openAIChatCompletionRequest
	if len(req.Body) == 0 {
		return jsonDispatch(http.StatusBadRequest, openAIError("request body is required")), nil
	}
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return jsonDispatch(http.StatusBadRequest, openAIError("invalid JSON body")), nil
	}
	text := body.UserText()
	if strings.TrimSpace(text) == "" {
		return jsonDispatch(http.StatusBadRequest, openAIError("at least one user message is required")), nil
	}

	sessionID := body.SessionID(req.ID)
	payload := core.ChatPayload{
		ChatID:          sessionID,
		Text:            text,
		SourceSessionID: sessionID,
	}
	raw, _ := json.Marshal(payload)
	event := core.Event{
		Type:      core.EventWebChat,
		AccountID: acct.ID,
		Payload:   raw,
	}
	opts := &engine.RunOptions{ModelOverride: body.ModelOverride(acct.Runtime.Config)}
	// Chat-path /model override fallback (only applies when the relay
	// caller did NOT specify model — explicit body.Model wins).
	opts = acct.Runtime.ApplyActiveModel(opts)
	output, err := acct.Runtime.RunTurn(ctx, req.ID, event, opts)
	if err != nil {
		if status, message, ok := runtimeErrorHTTPStatus(err); ok {
			return jsonDispatch(status, openAIServerError(message)), nil
		}
		slog.Warn("chat relay chat completion failed",
			"request_id", req.ID,
			"account_id", req.AccountID,
			"error", err,
		)
		return jsonDispatch(http.StatusInternalServerError, openAIServerError(err.Error())), nil
	}
	outbound := core.ParseOutboundResponse(output)
	model := body.ResponseModel(acct.Runtime.Config)
	if body.Stream {
		return sseDispatch(openAIChatCompletionSSE(req.ID, model, outbound.Text)), nil
	}
	return jsonDispatch(http.StatusOK, openAIChatCompletionJSON(req.ID, model, outbound.Text)), nil
}

type openAIChatCompletionRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	User     string          `json:"user"`
	Metadata map[string]any  `json:"metadata"`
}

type openAIMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func (r openAIChatCompletionRequest) LastUserText() string {
	idx, text := r.lastUserMessage()
	if idx < 0 {
		return ""
	}
	return text
}

func (r openAIChatCompletionRequest) UserText() string {
	lastIdx, lastText := r.lastUserMessage()
	if lastIdx < 0 {
		return ""
	}
	if !r.hasPriorText(lastIdx) {
		return lastText
	}

	var b strings.Builder
	b.WriteString("OpenAI-compatible conversation transcript:\n")
	for i, msg := range r.Messages {
		content := strings.TrimSpace(rawOpenAIContentText(msg.Content))
		if content == "" {
			continue
		}
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "message"
		}
		if i == lastIdx {
			b.WriteString("\nCurrent user message:\n")
			b.WriteString(content)
			continue
		}
		b.WriteString(role)
		b.WriteString(": ")
		b.WriteString(content)
		b.WriteString("\n")
	}
	return b.String()
}

func (r openAIChatCompletionRequest) lastUserMessage() (int, string) {
	for i := len(r.Messages) - 1; i >= 0; i-- {
		if r.Messages[i].Role != string(core.RoleUser) {
			continue
		}
		text := rawOpenAIContentText(r.Messages[i].Content)
		if strings.TrimSpace(text) != "" {
			return i, text
		}
	}
	return -1, ""
}

func (r openAIChatCompletionRequest) hasPriorText(lastIdx int) bool {
	for i := 0; i < lastIdx; i++ {
		if strings.TrimSpace(rawOpenAIContentText(r.Messages[i].Content)) != "" {
			return true
		}
	}
	return false
}

func (r openAIChatCompletionRequest) SessionID(fallback string) string {
	for _, key := range []string{"kittypaw_session_id", "session_id"} {
		if value, ok := r.Metadata[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	if strings.TrimSpace(r.User) != "" {
		return strings.TrimSpace(r.User)
	}
	if strings.TrimSpace(fallback) != "" {
		return strings.TrimSpace(fallback)
	}
	return "remote"
}

func (r openAIChatCompletionRequest) ModelOverride(cfg *core.Config) string {
	if cfg == nil {
		return ""
	}
	model := strings.TrimSpace(r.Model)
	if model == "" || model == cfg.LLM.Model || model == cfg.LLM.Default {
		return ""
	}
	if def := cfg.DefaultModel(); def != nil && model == def.ModelID() {
		return ""
	}
	if cfg.FindModel(model) != nil {
		return model
	}
	return ""
}

func (r openAIChatCompletionRequest) ResponseModel(cfg *core.Config) string {
	if strings.TrimSpace(r.Model) != "" {
		return strings.TrimSpace(r.Model)
	}
	if cfg != nil {
		if def := cfg.DefaultModel(); def != nil && strings.TrimSpace(def.ModelID()) != "" {
			return strings.TrimSpace(def.ModelID())
		}
	}
	if cfg != nil && strings.TrimSpace(cfg.LLM.Model) != "" {
		return strings.TrimSpace(cfg.LLM.Model)
	}
	return "kittypaw"
}

func rawOpenAIContentText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		var b strings.Builder
		for _, part := range parts {
			if part.Type == "text" || part.Type == "" {
				b.WriteString(part.Text)
			}
		}
		return b.String()
	}
	return ""
}

func jsonDispatch(status int, body map[string]any) chatrelay.DispatchResult {
	raw, _ := json.Marshal(body)
	return chatrelay.DispatchResult{
		Status:  status,
		Headers: map[string]string{"content-type": "application/json"},
		Body:    raw,
	}
}

func sseDispatch(body []byte) chatrelay.DispatchResult {
	return chatrelay.DispatchResult{
		Status:  http.StatusOK,
		Headers: map[string]string{"content-type": "text/event-stream"},
		Body:    body,
	}
}

func openAIModelItem(id string) map[string]any {
	return map[string]any{
		"id":       id,
		"object":   "model",
		"owned_by": "kittypaw",
	}
}

func modelIDExists(models []map[string]any, id string) bool {
	for _, model := range models {
		if model["id"] == id {
			return true
		}
	}
	return false
}

func openAIError(message string) map[string]any {
	return map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "invalid_request_error",
		},
	}
}

func openAIServerError(message string) map[string]any {
	return map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "server_error",
			"code":    "kittypaw_turn_failed",
		},
	}
}

func openAIChatCompletionJSON(id, model, content string) map[string]any {
	created := time.Now().Unix()
	return map[string]any{
		"id":      completionID(id),
		"object":  "chat.completion",
		"created": created,
		"model":   model,
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": content,
				},
				"finish_reason": "stop",
			},
		},
	}
}

func openAIChatCompletionSSE(id, model, content string) []byte {
	created := time.Now().Unix()
	completionID := completionID(id)
	chunk := map[string]any{
		"id":      completionID,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []map[string]any{
			{
				"index": 0,
				"delta": map[string]any{
					"role":    "assistant",
					"content": content,
				},
				"finish_reason": nil,
			},
		},
	}
	done := map[string]any{
		"id":      completionID,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []map[string]any{
			{
				"index":         0,
				"delta":         map[string]any{},
				"finish_reason": "stop",
			},
		},
	}
	var b strings.Builder
	writeSSEData(&b, chunk)
	writeSSEData(&b, done)
	b.WriteString("data: [DONE]\n\n")
	return []byte(b.String())
}

func writeSSEData(b *strings.Builder, value map[string]any) {
	raw, _ := json.Marshal(value)
	b.WriteString("data: ")
	b.Write(raw)
	b.WriteString("\n\n")
}

func completionID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		id = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return "chatcmpl-" + id
}
