package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
)

func TestFirecrawlReadBackendRead(t *testing.T) {
	var gotReq struct {
		URL             string   `json:"url"`
		Formats         []string `json:"formats"`
		OnlyMainContent bool     `json:"onlyMainContent"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/scrape" {
			t.Fatalf("path = %q, want /v2/scrape", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer fc-test" {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"success": true,
			"data": {
				"markdown": "# Article\n\nFirecrawl markdown",
				"html": "<html><head><title>Ignored</title></head><body>Firecrawl HTML</body></html>",
				"metadata": {
					"title": "Firecrawl Title",
					"sourceURL": "https://example.com/final",
					"statusCode": 200
				}
			},
			"warning": "cached"
		}`))
	}))
	defer srv.Close()

	backend := &FirecrawlReadBackend{APIKey: "fc-test", BaseURL: srv.URL}
	result, err := backend.Read(context.Background(), "https://example.com/start", ReadOptions{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if gotReq.URL != "https://example.com/start" {
		t.Fatalf("request url = %q", gotReq.URL)
	}
	if len(gotReq.Formats) != 2 || gotReq.Formats[0] != "markdown" || gotReq.Formats[1] != "html" {
		t.Fatalf("formats = %#v, want markdown/html", gotReq.Formats)
	}
	if !gotReq.OnlyMainContent {
		t.Fatal("onlyMainContent = false, want true")
	}
	if !result.OK || result.Backend != "firecrawl" || result.Status != 200 {
		t.Fatalf("result = %+v, want successful firecrawl result", result)
	}
	if result.Title != "Firecrawl Title" || result.FinalURL != "https://example.com/final" {
		t.Fatalf("title/finalURL = %q/%q", result.Title, result.FinalURL)
	}
	if !strings.Contains(result.Markdown, "Firecrawl markdown") || !strings.Contains(result.Text, "Firecrawl HTML") {
		t.Fatalf("content = text %q markdown %q", result.Text, result.Markdown)
	}
	if result.Warning != "cached" {
		t.Fatalf("warning = %q, want cached", result.Warning)
	}
}

func TestNewReadBackendFirecrawlRequiresKey(t *testing.T) {
	_, err := NewReadBackend(&core.WebConfig{ReadBackend: "firecrawl"})
	if err == nil || !strings.Contains(err.Error(), "firecrawl read backend requires") {
		t.Fatalf("err = %v, want missing key error", err)
	}
}

func TestNewReadBackendBrowserRequiresController(t *testing.T) {
	_, err := NewReadBackendWithBrowser(&core.WebConfig{ReadBackend: "browser"}, nil)
	if err == nil || !strings.Contains(err.Error(), "browser read backend requires browser controller") {
		t.Fatalf("err = %v, want missing browser controller error", err)
	}
}

func TestBrowserReadBackendRead(t *testing.T) {
	controller := &fakeBrowserReadController{}
	backend, err := NewReadBackendWithBrowser(&core.WebConfig{ReadBackend: "browser"}, controller)
	if err != nil {
		t.Fatalf("NewReadBackendWithBrowser: %v", err)
	}
	result, err := backend.Read(context.Background(), "https://example.com/rendered", ReadOptions{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(controller.calls) != 2 || controller.calls[0].Method != "open" || controller.calls[1].Method != "snapshot" {
		t.Fatalf("calls = %+v, want Browser.open then Browser.snapshot", controller.calls)
	}
	if !result.OK || result.Backend != "browser" {
		t.Fatalf("result = %+v, want browser success", result)
	}
	if result.Title != "Browser Title" || result.FinalURL != "https://example.com/final" {
		t.Fatalf("title/finalURL = %q/%q", result.Title, result.FinalURL)
	}
	if result.Text != "Rendered browser text" || result.Markdown != "Rendered browser text" {
		t.Fatalf("text/markdown = %q/%q", result.Text, result.Markdown)
	}
}

func TestAutoReadBackendFallsBackToFirecrawlWhenStaticWeak(t *testing.T) {
	weakPage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
	}))
	defer weakPage.Close()

	firecrawl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"success": true,
			"data": {
				"markdown": "Fallback markdown",
				"html": "<main>Fallback html</main>",
				"metadata": {"title": "Fallback", "sourceURL": "https://example.com/fallback", "statusCode": 200}
			}
		}`))
	}))
	defer firecrawl.Close()

	backend, err := NewReadBackend(&core.WebConfig{
		ReadBackend:  "auto",
		FirecrawlKey: "fc-test",
		FirecrawlURL: firecrawl.URL,
	})
	if err != nil {
		t.Fatalf("NewReadBackend: %v", err)
	}
	result, err := backend.Read(context.Background(), weakPage.URL, ReadOptions{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if result.Backend != "firecrawl" || !result.OK {
		t.Fatalf("result = %+v, want firecrawl fallback", result)
	}
	if !strings.Contains(result.Warning, "static backend weak") {
		t.Fatalf("warning = %q, want fallback warning", result.Warning)
	}
}

func TestExecuteHTTPWebFetchUsesConfiguredFirecrawlReadBackend(t *testing.T) {
	firecrawl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"success": true,
			"data": {
				"markdown": "Configured backend markdown",
				"html": "<main>Configured backend html</main>",
				"metadata": {"title": "Configured", "sourceURL": "https://example.com/configured", "statusCode": 200}
			}
		}`))
	}))
	defer firecrawl.Close()

	cfg := core.DefaultConfig()
	cfg.Web.ReadBackend = "firecrawl"
	cfg.Web.FirecrawlKey = "fc-test"
	cfg.Web.FirecrawlURL = firecrawl.URL
	s := &Session{Config: &cfg}
	urlArg, _ := json.Marshal("https://example.com/article")
	result, err := executeHTTP(context.Background(), core.SkillCall{
		SkillName: "Web",
		Method:    "fetch",
		Args:      []json.RawMessage{urlArg},
	}, s)
	if err != nil {
		t.Fatalf("executeHTTP: %v", err)
	}
	var resp ReadResult
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if resp.Backend != "firecrawl" || resp.Title != "Configured" {
		t.Fatalf("resp = %+v, want configured firecrawl backend", resp)
	}
}

func TestExecuteHTTPWebFetchUsesConfiguredBrowserReadBackend(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Web.ReadBackend = "browser"
	s := &Session{Config: &cfg, BrowserController: &fakeBrowserReadController{}}
	urlArg, _ := json.Marshal("https://example.com/rendered")
	result, err := executeHTTP(context.Background(), core.SkillCall{
		SkillName: "Web",
		Method:    "fetch",
		Args:      []json.RawMessage{urlArg},
	}, s)
	if err != nil {
		t.Fatalf("executeHTTP: %v", err)
	}
	var resp ReadResult
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if resp.Backend != "browser" || resp.Text != "Rendered browser text" {
		t.Fatalf("resp = %+v, want configured browser backend", resp)
	}
}

type fakeBrowserReadController struct {
	calls []core.SkillCall
}

func (f *fakeBrowserReadController) Execute(_ context.Context, call core.SkillCall) (string, error) {
	f.calls = append(f.calls, call)
	switch call.Method {
	case "open":
		return `{"target_id":"t1","url":"https://example.com/rendered","title":"Opened"}`, nil
	case "snapshot":
		return `{"target_id":"t1","url":"https://example.com/final","title":"Browser Title","text":"Rendered browser text","elements":[]}`, nil
	default:
		return `{"error":"unexpected method"}`, nil
	}
}

func (f *fakeBrowserReadController) Close() error { return nil }
