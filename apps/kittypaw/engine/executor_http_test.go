package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
)

func TestExecuteHTTP_HeadersSupport(t *testing.T) {
	var gotHeaders http.Header
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header
		if r.Body != nil {
			_, _ = io.ReadAll(r.Body)
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	s := &AccountRuntime{Config: &core.Config{}}

	tests := []struct {
		name       string
		method     string
		args       []any
		wantHeader string
		wantValue  string
	}{
		{
			name:   "GET no headers",
			method: "get",
			args:   []any{ts.URL + "/test"},
		},
		{
			name:       "GET with Authorization header",
			method:     "get",
			args:       []any{ts.URL + "/test", map[string]any{"headers": map[string]any{"Authorization": "Bearer tok123"}}},
			wantHeader: "Authorization",
			wantValue:  "Bearer tok123",
		},
		{
			name:   "POST no headers",
			method: "post",
			args:   []any{ts.URL + "/test", `{"key":"val"}`},
		},
		{
			name:       "POST with custom header",
			method:     "post",
			args:       []any{ts.URL + "/test", `{"key":"val"}`, map[string]any{"headers": map[string]any{"X-Custom": "hello"}}},
			wantHeader: "X-Custom",
			wantValue:  "hello",
		},
		{
			name:   "GET with malformed options (string not object)",
			method: "get",
			args:   []any{ts.URL + "/test", "not-an-object"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotHeaders = nil

			rawArgs := make([]json.RawMessage, len(tt.args))
			for i, a := range tt.args {
				b, err := json.Marshal(a)
				if err != nil {
					t.Fatal(err)
				}
				rawArgs[i] = b
			}

			call := core.SkillCall{
				SkillName: "Http",
				Method:    tt.method,
				Args:      rawArgs,
			}

			// httptest.NewServer binds to 127.0.0.1 which is private.
			// Use the bypass flag since we're testing headers, not SSRF.
			ctx := context.WithValue(context.Background(), httpValidatedHostKey, "127.0.0.1")
			result, err := executeHTTP(ctx, call, s)
			if err != nil {
				t.Fatalf("executeHTTP returned error: %v", err)
			}

			var resp map[string]any
			if err := json.Unmarshal([]byte(result), &resp); err != nil {
				t.Fatalf("invalid JSON response: %v", err)
			}
			if status, ok := resp["status"].(float64); !ok || status != 200 {
				t.Errorf("expected status 200, got %v", resp["status"])
			}

			if tt.wantHeader != "" {
				if gotHeaders == nil {
					t.Fatal("no headers received")
				}
				got := gotHeaders.Get(tt.wantHeader)
				if got != tt.wantValue {
					t.Errorf("header %q = %q, want %q", tt.wantHeader, got, tt.wantValue)
				}
			}
		})
	}
}

func TestExecuteHTTP_HostValidatedBypassesSSRF(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	s := &AccountRuntime{Config: &core.Config{}}

	urlArg, _ := json.Marshal(ts.URL + "/test")
	call := core.SkillCall{
		SkillName: "Http",
		Method:    "get",
		Args:      []json.RawMessage{urlArg},
	}

	// Without bypass: httptest.NewServer is on 127.0.0.1 → blocked by SSRF.
	result, err := executeHTTP(context.Background(), call, s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp["error"] == nil {
		t.Error("expected SSRF error for 127.0.0.1 without bypass, got success")
	}

	// With bypass flag: should succeed.
	ctx := context.WithValue(context.Background(), httpValidatedHostKey, "127.0.0.1")
	result, err = executeHTTP(ctx, call, s)
	if err != nil {
		t.Fatalf("unexpected error with bypass: %v", err)
	}
	var resp2 map[string]any
	if err := json.Unmarshal([]byte(result), &resp2); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp2["error"] != nil {
		t.Errorf("expected success with bypass flag, got error: %v", resp2["error"])
	}
	if status, ok := resp2["status"].(float64); !ok || status != 200 {
		t.Errorf("expected status 200, got %v", resp2["status"])
	}
}

func TestExecuteHTTP_WebSearchSkipsURLAllowedHosts(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Web.SearchBackend = "no-such-backend"
	cfg.Sandbox.AllowedHosts = []string{"example.com"}
	s := &AccountRuntime{Config: &cfg}

	queryArg, _ := json.Marshal("AI news today")
	call := core.SkillCall{
		SkillName: "Web",
		Method:    "search",
		Args:      []json.RawMessage{queryArg},
	}

	result, err := executeHTTP(context.Background(), call, s)
	if err != nil {
		t.Fatalf("executeHTTP returned error: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	gotErr, _ := resp["error"].(string)
	if !strings.Contains(gotErr, "unknown search backend") {
		t.Fatalf("error = %q, want search backend error", gotErr)
	}
	if strings.Contains(gotErr, "allowed hosts") || strings.Contains(gotErr, "host") {
		t.Fatalf("Web.search query was treated as a URL: %q", gotErr)
	}
}

func TestPackageResolver_WebFetchKeepsStructuredResult(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Pkg Page</title></head><body><main>Hello package web fetch</main></body></html>`))
	}))
	defer ts.Close()

	cfg := core.DefaultConfig()
	s := &AccountRuntime{Config: &cfg}
	pkg := &core.SkillPackage{
		Meta: core.PackageMeta{ID: "pkg-web-fetch"},
		Permissions: core.PackagePermissions{
			Primitives:   []string{"Web"},
			AllowedHosts: []string{"127.0.0.1"},
		},
	}
	resolver := buildPackageResolver(context.Background(), pkg, s, "")
	urlArg, _ := json.Marshal(ts.URL)

	result, err := resolver(context.Background(), core.SkillCall{
		SkillName: "Web",
		Method:    "fetch",
		Args:      []json.RawMessage{urlArg},
	})
	if err != nil {
		t.Fatalf("resolver returned error: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("Web.fetch package result should remain structured JSON, got %q: %v", result, err)
	}
	if status, ok := resp["status"].(float64); !ok || status != 200 {
		t.Fatalf("status = %v, want 200", resp["status"])
	}
	if title, _ := resp["title"].(string); title != "Pkg Page" {
		t.Fatalf("title = %q, want Pkg Page", title)
	}
}

func TestPackageResolver_WebSearchKeepsStructuredResult(t *testing.T) {
	searchSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/search" {
			t.Fatalf("path = %q, want /v1/search", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":[{"url":"https://example.com/a","title":"A","description":"Result A"}]}`))
	}))
	defer searchSrv.Close()

	cfg := core.DefaultConfig()
	cfg.Web.SearchBackend = "firecrawl"
	cfg.Web.FirecrawlKey = "fc-test"
	cfg.Web.FirecrawlURL = searchSrv.URL
	s := &AccountRuntime{Config: &cfg}
	pkg := &core.SkillPackage{
		Meta:        core.PackageMeta{ID: "pkg-web-search"},
		Permissions: core.PackagePermissions{Primitives: []string{"Web"}},
	}
	resolver := buildPackageResolver(context.Background(), pkg, s, "")
	queryArg, _ := json.Marshal("AI news today")

	result, err := resolver(context.Background(), core.SkillCall{
		SkillName: "Web",
		Method:    "search",
		Args:      []json.RawMessage{queryArg},
	})
	if err != nil {
		t.Fatalf("resolver returned error: %v", err)
	}
	var resp struct {
		Results []WebSearchResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("Web.search package result should remain structured JSON, got %q: %v", result, err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Title != "A" {
		t.Fatalf("results = %+v, want one structured result", resp.Results)
	}
}

func TestPackageResolver_HttpStillUnwrapsBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("raw package body"))
	}))
	defer ts.Close()

	cfg := core.DefaultConfig()
	s := &AccountRuntime{Config: &cfg}
	pkg := &core.SkillPackage{
		Meta: core.PackageMeta{ID: "pkg-http"},
		Permissions: core.PackagePermissions{
			Primitives:   []string{"Http"},
			AllowedHosts: []string{"127.0.0.1"},
		},
	}
	resolver := buildPackageResolver(context.Background(), pkg, s, "")
	urlArg, _ := json.Marshal(ts.URL)

	result, err := resolver(context.Background(), core.SkillCall{
		SkillName: "Http",
		Method:    "get",
		Args:      []json.RawMessage{urlArg},
	})
	if err != nil {
		t.Fatalf("resolver returned error: %v", err)
	}
	if result != "raw package body" {
		t.Fatalf("Http.get package result = %q, want raw body", result)
	}
}

func TestExecuteHTTP_WebFetchContractSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><title>Contract Page</title></head><body><p>Hello contract</p></body></html>`))
	}))
	defer ts.Close()

	resp := executeWebFetchForTest(t, ts.URL)
	if resp["ok"] != true {
		t.Fatalf("ok = %v, want true; resp=%v", resp["ok"], resp)
	}
	if resp["backend"] != "static" {
		t.Fatalf("backend = %v, want static", resp["backend"])
	}
	if resp["status"] != float64(200) {
		t.Fatalf("status = %v, want 200", resp["status"])
	}
	if ct, _ := resp["contentType"].(string); !strings.Contains(ct, "text/html") {
		t.Fatalf("contentType = %q, want text/html", ct)
	}
	if resp["finalUrl"] != ts.URL {
		t.Fatalf("finalUrl = %v, want %s", resp["finalUrl"], ts.URL)
	}
	if resp["title"] != "Contract Page" {
		t.Fatalf("title = %v, want Contract Page", resp["title"])
	}
	if text, _ := resp["text"].(string); !strings.Contains(text, "Hello contract") {
		t.Fatalf("text = %q, want page text", text)
	}
}

func TestExecuteHTTP_WebFetchContractNon2xx(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<html><body>missing page</body></html>`))
	}))
	defer ts.Close()

	resp := executeWebFetchForTest(t, ts.URL)
	if resp["ok"] != false {
		t.Fatalf("ok = %v, want false; resp=%v", resp["ok"], resp)
	}
	if resp["status"] != float64(http.StatusNotFound) {
		t.Fatalf("status = %v, want 404", resp["status"])
	}
	if got, _ := resp["error"].(string); !strings.Contains(got, "HTTP 404") {
		t.Fatalf("error = %q, want HTTP 404", got)
	}
}

func TestExecuteHTTP_WebFetchContractEmptyBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	resp := executeWebFetchForTest(t, ts.URL)
	if resp["ok"] != false {
		t.Fatalf("ok = %v, want false; resp=%v", resp["ok"], resp)
	}
	if got, _ := resp["error"].(string); !strings.Contains(got, "empty body") {
		t.Fatalf("error = %q, want empty body", got)
	}
}

func TestExecuteHTTP_WebFetchContractRedirectFinalURL(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body>final page</body></html>`))
	}))
	defer ts.Close()

	resp := executeWebFetchForTest(t, ts.URL+"/start")
	if resp["ok"] != true {
		t.Fatalf("ok = %v, want true; resp=%v", resp["ok"], resp)
	}
	if resp["finalUrl"] != ts.URL+"/final" {
		t.Fatalf("finalUrl = %v, want %s", resp["finalUrl"], ts.URL+"/final")
	}
}

func TestExecuteHTTP_WebFetchContractUnsupportedContentType(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n"))
	}))
	defer ts.Close()

	resp := executeWebFetchForTest(t, ts.URL)
	if resp["ok"] != false {
		t.Fatalf("ok = %v, want false; resp=%v", resp["ok"], resp)
	}
	if got, _ := resp["error"].(string); !strings.Contains(got, "unsupported content type") {
		t.Fatalf("error = %q, want unsupported content type", got)
	}
}

func executeWebFetchForTest(t *testing.T, targetURL string) map[string]any {
	t.Helper()
	cfg := core.DefaultConfig()
	s := &AccountRuntime{Config: &cfg}
	urlArg, _ := json.Marshal(targetURL)
	call := core.SkillCall{
		SkillName: "Web",
		Method:    "fetch",
		Args:      []json.RawMessage{urlArg},
	}
	ctx := context.WithValue(context.Background(), httpValidatedHostKey, "127.0.0.1")
	result, err := executeHTTP(ctx, call, s)
	if err != nil {
		t.Fatalf("executeHTTP returned error: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("invalid JSON response %q: %v", result, err)
	}
	return resp
}
