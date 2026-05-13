package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jinto/kittypaw/core"
)

const defaultFirecrawlAPIURL = "https://api.firecrawl.dev"

var readHTTPClient = &http.Client{Timeout: 30 * time.Second}

type ReadOptions struct{}

type ReadResult struct {
	OK          bool           `json:"ok"`
	Error       string         `json:"error"`
	Text        string         `json:"text"`
	Markdown    string         `json:"markdown"`
	Title       string         `json:"title"`
	Status      int            `json:"status"`
	ContentType string         `json:"contentType"`
	FinalURL    string         `json:"finalUrl"`
	Backend     string         `json:"backend"`
	Warning     string         `json:"warning,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type ReadBackend interface {
	Read(ctx context.Context, targetURL string, opts ReadOptions) (ReadResult, error)
}

type browserSkillExecutor interface {
	Execute(ctx context.Context, call core.SkillCall) (string, error)
}

type browserSkillExecutorFunc func(context.Context, core.SkillCall) (string, error)

func (f browserSkillExecutorFunc) Execute(ctx context.Context, call core.SkillCall) (string, error) {
	return f(ctx, call)
}

func NewReadBackend(cfg *core.WebConfig) (ReadBackend, error) {
	return NewReadBackendWithBrowser(cfg, nil)
}

func NewReadBackendWithBrowser(cfg *core.WebConfig, browserExecutor browserSkillExecutor) (ReadBackend, error) {
	if cfg == nil {
		cfg = &core.WebConfig{}
	}
	backend := strings.ToLower(strings.TrimSpace(cfg.ReadBackend))
	if backend == "" {
		backend = "auto"
	}
	switch backend {
	case "static":
		return &StaticReadBackend{}, nil
	case "firecrawl":
		firecrawl, err := newFirecrawlReadBackend(cfg)
		if err != nil {
			return nil, err
		}
		return firecrawl, nil
	case "browser":
		if browserExecutor == nil {
			return nil, fmt.Errorf("browser read backend requires browser controller")
		}
		return &BrowserReadBackend{Executor: browserExecutor}, nil
	case "auto":
		var firecrawl ReadBackend
		if cfg.FirecrawlKey != "" {
			if fc, err := newFirecrawlReadBackend(cfg); err == nil {
				firecrawl = fc
			}
		}
		return &AutoReadBackend{Static: &StaticReadBackend{}, Fallback: firecrawl}, nil
	default:
		return nil, fmt.Errorf("unknown read backend: %q (supported: auto, static, firecrawl, browser)", backend)
	}
}

type AutoReadBackend struct {
	Static   ReadBackend
	Fallback ReadBackend
}

func (a *AutoReadBackend) Read(ctx context.Context, targetURL string, opts ReadOptions) (ReadResult, error) {
	static := a.Static
	if static == nil {
		static = &StaticReadBackend{}
	}
	result, err := static.Read(ctx, targetURL, opts)
	if err != nil || !isWeakReadResult(result) || a.Fallback == nil {
		return result, err
	}
	fallback, fbErr := a.Fallback.Read(ctx, targetURL, opts)
	if fbErr != nil || !fallback.OK {
		if result.Warning == "" {
			result.Warning = fmt.Sprintf("firecrawl fallback failed: %v", firstNonNilError(fbErr, errorsFromReadResult(fallback)))
		}
		return result, err
	}
	if fallback.Warning == "" {
		fallback.Warning = "static backend weak; used firecrawl"
	} else {
		fallback.Warning = "static backend weak; used firecrawl; " + fallback.Warning
	}
	return fallback, nil
}

type StaticReadBackend struct {
	Client *http.Client
}

func (b *StaticReadBackend) Read(ctx context.Context, targetURL string, _ ReadOptions) (ReadResult, error) {
	result := ReadResult{Backend: "static", FinalURL: targetURL}
	req, err := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}
	req.Header.Set("User-Agent", "KittyPaw/1.0")

	client := b.Client
	if client == nil {
		client = readHTTPClient
	}
	resp, err := client.Do(req)
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 500_000))
	result.Status = resp.StatusCode
	result.ContentType = resp.Header.Get("Content-Type")
	if resp.Request != nil && resp.Request.URL != nil {
		result.FinalURL = resp.Request.URL.String()
	}
	rawHTML := string(body)
	result.Text = truncate(stripHTMLTags(rawHTML), 10000)
	result.Markdown = truncate(htmlToMarkdown(rawHTML), 10000)
	result.Title = extractTitle(rawHTML)
	result.OK = resp.StatusCode >= 200 && resp.StatusCode < 300 && len(body) > 0 && isReadableWebContentType(result.ContentType)
	switch {
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		result.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
	case len(body) == 0:
		result.Error = "empty body"
	case !isReadableWebContentType(result.ContentType):
		result.Error = "unsupported content type: " + result.ContentType
	}
	return result, nil
}

type FirecrawlReadBackend struct {
	APIKey  string
	BaseURL string
	Client  *http.Client
}

type BrowserReadBackend struct {
	Executor browserSkillExecutor
}

func (b *BrowserReadBackend) Read(ctx context.Context, targetURL string, _ ReadOptions) (ReadResult, error) {
	result := ReadResult{Backend: "browser", FinalURL: targetURL, ContentType: "text/plain", Status: http.StatusOK}
	if b.Executor == nil {
		result.Error = "browser read backend requires browser controller"
		return result, nil
	}
	urlArg, _ := json.Marshal(targetURL)
	openResult, err := b.Executor.Execute(ctx, core.SkillCall{
		SkillName: "Browser",
		Method:    "open",
		Args:      []json.RawMessage{urlArg},
	})
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}
	if errText := browserToolError(openResult); errText != "" {
		result.Error = errText
		return result, nil
	}
	targetID := browserToolTargetID(openResult)
	if targetID == "" {
		result.Error = "browser open missing target_id"
		return result, nil
	}
	snapshotOpts, _ := json.Marshal(map[string]string{"target_id": targetID})
	snapshotResult, err := b.Executor.Execute(ctx, core.SkillCall{
		SkillName: "Browser",
		Method:    "snapshot",
		Args:      []json.RawMessage{snapshotOpts},
	})
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}
	if errText := browserToolError(snapshotResult); errText != "" {
		result.Error = errText
		return result, nil
	}
	var snapshot struct {
		URL   string `json:"url"`
		Title string `json:"title"`
		Text  string `json:"text"`
	}
	if err := json.Unmarshal([]byte(snapshotResult), &snapshot); err != nil {
		result.Error = "browser snapshot parse: " + err.Error()
		return result, nil
	}
	if strings.TrimSpace(snapshot.URL) != "" {
		result.FinalURL = snapshot.URL
	}
	result.Title = snapshot.Title
	result.Text = truncate(snapshot.Text, 10000)
	result.Markdown = result.Text
	if strings.TrimSpace(result.Text) == "" {
		result.Error = "empty body"
		return result, nil
	}
	result.OK = true
	return result, nil
}

func newFirecrawlReadBackend(cfg *core.WebConfig) (*FirecrawlReadBackend, error) {
	if strings.TrimSpace(cfg.FirecrawlKey) == "" {
		return nil, fmt.Errorf("firecrawl read backend requires firecrawl_api_key in [web] config")
	}
	apiURL := strings.TrimSpace(cfg.FirecrawlURL)
	if apiURL == "" {
		apiURL = defaultFirecrawlAPIURL
	}
	parsed, err := url.Parse(apiURL)
	if err != nil {
		return nil, fmt.Errorf("invalid firecrawl_api_url: %w", err)
	}
	if parsed.Scheme != "https" && parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" {
		return nil, fmt.Errorf("firecrawl_api_url must use HTTPS (got %s)", parsed.Scheme)
	}
	return &FirecrawlReadBackend{APIKey: cfg.FirecrawlKey, BaseURL: strings.TrimRight(apiURL, "/")}, nil
}

func (f *FirecrawlReadBackend) Read(ctx context.Context, targetURL string, _ ReadOptions) (ReadResult, error) {
	result := ReadResult{Backend: "firecrawl", FinalURL: targetURL}
	reqBody, _ := json.Marshal(map[string]any{
		"url":             targetURL,
		"formats":         []string{"markdown", "html"},
		"onlyMainContent": true,
	})
	req, err := http.NewRequestWithContext(ctx, "POST", f.BaseURL+"/v2/scrape", bytes.NewReader(reqBody))
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+f.APIKey)

	client := f.Client
	if client == nil {
		client = readHTTPClient
	}
	resp, err := client.Do(req)
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}
	defer resp.Body.Close()

	result.Status = resp.StatusCode
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 500_000))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.Error = fmt.Sprintf("firecrawl API error %d: %s", resp.StatusCode, string(body))
		return result, nil
	}

	var parsed struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
		Data    struct {
			Markdown string         `json:"markdown"`
			HTML     string         `json:"html"`
			Metadata map[string]any `json:"metadata"`
		} `json:"data"`
		Warning string `json:"warning"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		result.Error = "firecrawl response parse: " + err.Error()
		return result, nil
	}
	result.Warning = parsed.Warning
	result.Markdown = truncate(parsed.Data.Markdown, 10000)
	result.Text = truncate(stripHTMLTags(parsed.Data.HTML), 10000)
	if strings.TrimSpace(result.Text) == "" {
		result.Text = truncate(parsed.Data.Markdown, 10000)
	}
	result.Metadata = parsed.Data.Metadata
	result.Title = metadataString(parsed.Data.Metadata, "title")
	if result.Title == "" {
		result.Title = extractTitle(parsed.Data.HTML)
	}
	if finalURL := firstNonEmptyString(
		metadataString(parsed.Data.Metadata, "sourceURL"),
		metadataString(parsed.Data.Metadata, "url"),
	); finalURL != "" {
		result.FinalURL = finalURL
	}
	result.ContentType = metadataString(parsed.Data.Metadata, "contentType")
	if status := metadataInt(parsed.Data.Metadata, "statusCode"); status > 0 {
		result.Status = status
	}
	if parsed.Error != "" {
		result.Error = parsed.Error
	} else if metaErr := metadataString(parsed.Data.Metadata, "error"); metaErr != "" {
		result.Error = metaErr
	} else if !parsed.Success {
		result.Error = "firecrawl scrape failed"
	} else if strings.TrimSpace(result.Markdown) == "" && strings.TrimSpace(result.Text) == "" {
		result.Error = "empty body"
	}
	result.OK = result.Error == ""
	return result, nil
}

func isWeakReadResult(result ReadResult) bool {
	return !result.OK || (strings.TrimSpace(result.Text) == "" && strings.TrimSpace(result.Markdown) == "")
}

func isReadableWebContentType(contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if contentType == "" {
		return true
	}
	return strings.HasPrefix(contentType, "text/") ||
		strings.Contains(contentType, "html") ||
		strings.Contains(contentType, "xml") ||
		strings.Contains(contentType, "json")
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, _ := metadata[key].(string)
	return strings.TrimSpace(value)
}

func metadataInt(metadata map[string]any, key string) int {
	if metadata == nil {
		return 0
	}
	switch value := metadata[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case json.Number:
		i, _ := value.Int64()
		return int(i)
	default:
		return 0
	}
}

func browserToolError(raw string) string {
	var resp struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return ""
	}
	return strings.TrimSpace(resp.Error)
}

func browserToolTargetID(raw string) string {
	var resp struct {
		TargetID string `json:"target_id"`
	}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return ""
	}
	return strings.TrimSpace(resp.TargetID)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstNonNilError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func errorsFromReadResult(result ReadResult) error {
	if result.Error == "" {
		return nil
	}
	return errors.New(result.Error)
}
