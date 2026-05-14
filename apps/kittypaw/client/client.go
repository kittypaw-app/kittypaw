package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Client communicates with a KittyPaw server instance via REST API.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// New creates a new Client targeting the given server address.
func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// Health checks server liveness. Returns nil if healthy.
func (c *Client) Health() error {
	_, err := c.get("/health")
	return err
}

// ServerInfo returns version, model, and channels from the health endpoint.
func (c *Client) ServerInfo() (version, model string, channels []string, err error) {
	data, err := c.get("/health")
	if err != nil {
		return "", "", nil, err
	}
	if v, ok := data["version"].(string); ok {
		version = v
	}
	if m, ok := data["model"].(string); ok {
		model = m
	}
	if chs, ok := data["channels"].([]any); ok {
		for _, ch := range chs {
			if s, ok := ch.(string); ok {
				channels = append(channels, s)
			}
		}
	}
	return
}

// Status returns today's execution statistics.
func (c *Client) Status() (map[string]any, error) {
	return c.get("/api/v1/status")
}

// Executions returns recent execution records, optionally filtered by skill name.
func (c *Client) Executions(skill string, limit int) (map[string]any, error) {
	path := fmt.Sprintf("/api/v1/executions?limit=%d", limit)
	if skill != "" {
		path += "&skill=" + url.QueryEscape(skill)
	}
	return c.get(path)
}

// Deliveries returns recent outbound delivery ledger rows.
func (c *Client) Deliveries(limit int, status, channel, source string) (map[string]any, error) {
	if limit <= 0 {
		limit = 50
	}
	values := url.Values{}
	values.Set("limit", fmt.Sprintf("%d", limit))
	if status != "" {
		values.Set("status", status)
	}
	if channel != "" {
		values.Set("channel", channel)
	}
	if source != "" {
		values.Set("source", source)
	}
	return c.get("/api/v1/deliveries?" + values.Encode())
}

// ChatHistory returns recent account-wide conversation turns.
func (c *Client) ChatHistory(limit int) (map[string]any, error) {
	return c.ChatHistoryForConversation(limit, "")
}

// ChatHistoryForConversation returns recent turns for one conversation.
func (c *Client) ChatHistoryForConversation(limit int, conversationID string) (map[string]any, error) {
	if limit <= 0 {
		limit = 50
	}
	path := fmt.Sprintf("/api/v1/chat/history?limit=%d", limit)
	if conversationID != "" {
		path += "&conversation_id=" + url.QueryEscape(conversationID)
	}
	return c.get(path)
}

// ChatForget clears account-wide conversation history.
func (c *Client) ChatForget() (map[string]any, error) {
	return c.ChatForgetForConversation("")
}

// ChatForgetForConversation clears one conversation history.
func (c *Client) ChatForgetForConversation(conversationID string) (map[string]any, error) {
	if conversationID == "" {
		return c.post("/api/v1/chat/forget", nil)
	}
	return c.post("/api/v1/chat/forget", map[string]string{"conversation_id": conversationID})
}

// ChatCompact compacts older account-wide conversation turns.
func (c *Client) ChatCompact(keepRecent int) (map[string]any, error) {
	return c.ChatCompactForConversation(keepRecent, "")
}

// ChatCompactForConversation compacts older turns for one conversation.
func (c *Client) ChatCompactForConversation(keepRecent int, conversationID string) (map[string]any, error) {
	body := map[string]any{}
	if keepRecent > 0 {
		body["keep_recent"] = keepRecent
	}
	if conversationID != "" {
		body["conversation_id"] = conversationID
	}
	return c.post("/api/v1/chat/compact", body)
}

// Skills returns all skills.
func (c *Client) Skills() (map[string]any, error) {
	return c.get("/api/v1/skills")
}

// RunSkill dispatches a skill by name.
func (c *Client) RunSkill(name string) (map[string]any, error) {
	return c.post("/api/v1/skills/run", map[string]string{"name": name})
}

// Teach creates a skill from a description.
func (c *Client) Teach(description string) (map[string]any, error) {
	return c.post("/api/v1/skills/teach", map[string]string{"description": description})
}

// Chat sends a chat message and returns the response.
func (c *Client) Chat(text, sessionID string) (map[string]any, error) {
	body := map[string]string{"text": text}
	if sessionID != "" {
		body["session_id"] = sessionID
	}
	return c.post("/api/v1/chat", body)
}

// DeleteSkill removes a skill by name.
func (c *Client) DeleteSkill(name string) (map[string]any, error) {
	return c.delete("/api/v1/skills/" + url.PathEscape(name))
}

// DisableSkill disables a skill by name.
func (c *Client) DisableSkill(name string) (map[string]any, error) {
	return c.post("/api/v1/skills/"+url.PathEscape(name)+"/disable", nil)
}

// ConfigCheck returns configuration summary.
func (c *Client) ConfigCheck() (map[string]any, error) {
	return c.get("/api/v1/config/check")
}

// MemoryList returns prompt-safe user memory.
func (c *Client) MemoryList(limit int) (map[string]any, error) {
	return c.get(fmt.Sprintf("/api/v1/memory?limit=%d", limit))
}

// MemoryExport returns prompt-safe user memory for export.
func (c *Client) MemoryExport(limit int) (map[string]any, error) {
	return c.get(fmt.Sprintf("/api/v1/memory/export?limit=%d", limit))
}

// MemorySearch searches prompt-safe user memory.
func (c *Client) MemorySearch(query string, limit int) (map[string]any, error) {
	return c.get(fmt.Sprintf("/api/v1/memory/search?q=%s&limit=%d", url.QueryEscape(query), limit))
}

// MemoryDelete deletes one prompt-safe user memory by key.
func (c *Client) MemoryDelete(key string) (map[string]any, error) {
	return c.MemoryDeleteScoped(key, "", "")
}

// MemoryDeleteScoped deletes one prompt-safe user memory by key and exact scope.
func (c *Client) MemoryDeleteScoped(key, scopeType, scopeID string) (map[string]any, error) {
	path := "/api/v1/memory/" + url.PathEscape(key)
	values := url.Values{}
	if scopeType != "" {
		values.Set("scope_type", scopeType)
	}
	if scopeID != "" {
		values.Set("scope_id", scopeID)
	}
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return c.delete(path)
}

// MemoryForgetAll deletes all prompt-safe user memory.
func (c *Client) MemoryForgetAll() (map[string]any, error) {
	return c.post("/api/v1/memory/forget-all", nil)
}

// MemoryPending lists memory writes awaiting explicit confirmation.
func (c *Client) MemoryPending(limit int) (map[string]any, error) {
	return c.get(fmt.Sprintf("/api/v1/memory/pending?limit=%d", limit))
}

// MemoryConfirm confirms a pending memory write.
func (c *Client) MemoryConfirm(id int64) (map[string]any, error) {
	return c.post(fmt.Sprintf("/api/v1/memory/pending/%d/confirm", id), nil)
}

// MemoryReject rejects a pending memory write.
func (c *Client) MemoryReject(id int64) (map[string]any, error) {
	return c.post(fmt.Sprintf("/api/v1/memory/pending/%d/reject", id), nil)
}

// MemoryCurate returns reviewable memory cleanup suggestions.
func (c *Client) MemoryCurate(limit int) (map[string]any, error) {
	return c.get(fmt.Sprintf("/api/v1/memory/curate?limit=%d", limit))
}

// MemoryCurateApply applies one reviewable memory cleanup suggestion.
func (c *Client) MemoryCurateApply(id string) (map[string]any, error) {
	return c.post("/api/v1/memory/curate/"+url.PathEscape(id)+"/apply", nil)
}

// Reload triggers a config reload on the server.
func (c *Client) Reload() (map[string]any, error) {
	return c.post("/api/v1/reload", nil)
}

// ReloadAccount triggers a config reload for one active account.
func (c *Client) ReloadAccount(accountID string) (map[string]any, error) {
	return c.post("/api/v1/reload", map[string]string{"account_id": accountID})
}

// TelegramPairingChatID asks the local server to coordinate Telegram chat_id
// detection. When the daemon is already polling the bot token, this avoids a
// second competing getUpdates consumer in the CLI process.
func (c *Client) TelegramPairingChatID(accountID, token string) (map[string]any, error) {
	return c.post("/api/telegram/pairing/chat-id", map[string]string{
		"account_id": accountID,
		"token":      token,
	})
}

// AccountActivate registers an on-disk account with the running server, spawning
// its channels without a restart. The account directory must already exist
// (typically created by `kittypaw account add`). Errors surface the HTTP status
// verbatim so callers can distinguish 404 (not-provisioned) from 409 (already
// active) from 400 (invalid config).
func (c *Client) AccountActivate(accountID string) (map[string]any, error) {
	return c.post("/api/v1/admin/accounts", map[string]string{
		"account_id": accountID,
	})
}

// AccountRemove deactivates a live account. Mirrors AccountActivate. 200 on
// success, 404 if not currently active (caller treats as "nothing to do"),
// 400 on malformed ID, 500 if the server can't drain channels cleanly.
func (c *Client) AccountRemove(accountID string) (map[string]any, error) {
	return c.post("/api/v1/admin/accounts/"+url.PathEscape(accountID)+"/delete", nil)
}

// EnableSkill sets a skill's enabled state to true.
func (c *Client) EnableSkill(name string) (map[string]any, error) {
	return c.post("/api/v1/skills/"+url.PathEscape(name)+"/enable", nil)
}

// ExplainSkill asks the LLM to explain a skill.
func (c *Client) ExplainSkill(name string) (map[string]any, error) {
	return c.post("/api/v1/skills/"+url.PathEscape(name)+"/explain", nil)
}

// ChannelsList returns active channels.
func (c *Client) ChannelsList() (map[string]any, error) {
	return c.get("/api/v1/channels")
}

// StaffList returns all staff with preset status.
func (c *Client) StaffList() (map[string]any, error) {
	return c.get("/api/v1/staff")
}

// StaffActivate activates a staff identity by ID, optionally applying a preset first.
func (c *Client) StaffActivate(id, presetID string) (map[string]any, error) {
	var body any
	if presetID != "" {
		body = map[string]string{"preset_id": presetID}
	}
	return c.post("/api/v1/staff/"+url.PathEscape(id)+"/activate", body)
}

// TeachApprove saves a generated skill after user approval.
func (c *Client) TeachApprove(name, description, code, trigger, schedule string) (map[string]any, error) {
	return c.post("/api/v1/skills/teach/approve", map[string]string{
		"name":        name,
		"description": description,
		"code":        code,
		"trigger":     trigger,
		"schedule":    schedule,
	})
}

// Install installs a skill from a source (GitHub URL or local path).
func (c *Client) Install(source, mdMode string) (map[string]any, error) {
	return c.post("/api/v1/install", map[string]string{
		"source":  source,
		"md_mode": mdMode,
	})
}

// Search searches the registry for packages matching a keyword.
func (c *Client) Search(keyword string) (map[string]any, error) {
	return c.get("/api/v1/search?q=" + url.QueryEscape(keyword))
}

func (c *Client) get(path string) (map[string]any, error) {
	req, err := http.NewRequest("GET", c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	return c.do(req)
}

func (c *Client) post(path string, body any) (map[string]any, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest("POST", c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.do(req)
}

func (c *Client) delete(path string) (map[string]any, error) {
	req, err := http.NewRequest("DELETE", c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	return c.do(req)
}

func (c *Client) do(req *http.Request) (map[string]any, error) {
	if c.apiKey != "" {
		req.Header.Set("x-api-key", c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("server error %d: %s", resp.StatusCode, string(data))
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		// Some endpoints (e.g., /channels) return arrays. Wrap for consistency.
		var arr []any
		if json.Unmarshal(data, &arr) == nil {
			return map[string]any{"items": arr}, nil
		}
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return result, nil
}
