package core

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func testRegistryClient(t *testing.T, ts *httptest.Server) *RegistryClient {
	t.Helper()
	tsURL, _ := url.Parse(ts.URL)
	return &RegistryClient{
		baseURL:       ts.URL,
		allowedHost:   tsURL.Host,
		allowedScheme: tsURL.Scheme,
		client:        ts.Client(),
		cacheDir:      t.TempDir(),
	}
}

func TestNewRegistryClient_RequiresHTTPS(t *testing.T) {
	_, err := NewRegistryClient("http://insecure.example.com")
	if err == nil {
		t.Error("expected error for non-HTTPS URL")
	}
}

func TestNewRegistryClient_AllowsExplicitLoopbackHTTPRegistry(t *testing.T) {
	t.Setenv("KITTYPAW_ALLOW_INSECURE_REGISTRY", "1")
	t.Setenv("KITTYPAW_CONFIG_DIR", t.TempDir())

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.json" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`[{"id":"local","name":"Local","version":"1.0.0"}]`))
	}))
	defer ts.Close()

	client, err := NewRegistryClient(ts.URL)
	if err != nil {
		t.Fatalf("NewRegistryClient(loopback http) error = %v", err)
	}
	entries, err := client.FetchIndex()
	if err != nil {
		t.Fatalf("FetchIndex() error = %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "local" {
		t.Fatalf("entries = %+v, want local package", entries)
	}
}

func TestNewRegistryClient_RejectsNonLoopbackHTTPRegistryEvenWhenEnabled(t *testing.T) {
	t.Setenv("KITTYPAW_ALLOW_INSECURE_REGISTRY", "1")
	t.Setenv("KITTYPAW_CONFIG_DIR", t.TempDir())

	_, err := NewRegistryClient("http://example.com/registry")
	if err == nil {
		t.Fatal("expected non-loopback HTTP registry to be rejected")
	}
}

// ---------------------------------------------------------------------------
// DownloadPackage — SSRF
// ---------------------------------------------------------------------------

func TestRegistryClient_DownloadSSRFDefense(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pkg/package.toml":
			w.Write([]byte("[meta]\nid = \"test-pkg\"\nname = \"Test\"\nversion = \"1.0.0\"\n"))
		case "/pkg/main.js":
			w.Write([]byte(`return "hello"`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	client := testRegistryClient(t, ts)

	// Download from allowed URL should work.
	entry := RegistryEntry{
		ID:  "test-pkg",
		URL: ts.URL + "/pkg",
	}
	dir, err := client.DownloadPackage(entry)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if dir == "" {
		t.Error("expected non-empty temp dir")
	}

	// Download from different host should fail.
	entry.URL = "https://evil.com/steal?data=1"
	_, err = client.DownloadPackage(entry)
	if err == nil {
		t.Error("expected SSRF rejection for external URL")
	}
}

// ---------------------------------------------------------------------------
// DownloadPackage — multi-file
// ---------------------------------------------------------------------------

func TestRegistryClient_DownloadMultiFile(t *testing.T) {
	tomlContent := "[meta]\nid = \"multi\"\nname = \"Multi\"\nversion = \"2.0.0\"\n"
	jsContent := `return "multi"`
	readmeContent := "# Multi Package\n"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/multi/package.toml":
			w.Write([]byte(tomlContent))
		case "/multi/main.js":
			w.Write([]byte(jsContent))
		case "/multi/README.md":
			w.Write([]byte(readmeContent))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	client := testRegistryClient(t, ts)
	dir, err := client.DownloadPackage(RegistryEntry{ID: "multi", URL: ts.URL + "/multi"})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	for _, tc := range []struct {
		name    string
		content string
	}{
		{"package.toml", tomlContent},
		{"main.js", jsContent},
		{"README.md", readmeContent},
	} {
		got, err := os.ReadFile(filepath.Join(dir, tc.name))
		if err != nil {
			t.Errorf("expected %s to exist: %v", tc.name, err)
			continue
		}
		if string(got) != tc.content {
			t.Errorf("%s content = %q, want %q", tc.name, got, tc.content)
		}
	}
}

func TestRegistryClient_DownloadRequiredFile404(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/broken/package.toml":
			w.Write([]byte("[meta]\nid = \"broken\"\nname = \"Broken\"\nversion = \"1.0.0\"\n"))
		default:
			// main.js returns 404
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	client := testRegistryClient(t, ts)
	dir, err := client.DownloadPackage(RegistryEntry{ID: "broken", URL: ts.URL + "/broken"})
	if err == nil {
		os.RemoveAll(dir)
		t.Fatal("expected error when required main.js is 404")
	}

	// Verify tmpDir was cleaned up — the returned dir should be empty string.
	if dir != "" {
		if _, statErr := os.Stat(dir); statErr == nil {
			t.Error("expected tmpDir to be cleaned up on failure")
		}
	}
}

func TestRegistryClient_DownloadOptionalReadme404(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/noreadme/package.toml":
			w.Write([]byte("[meta]\nid = \"noreadme\"\nname = \"No Readme\"\nversion = \"1.0.0\"\n"))
		case "/noreadme/main.js":
			w.Write([]byte(`return "ok"`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	client := testRegistryClient(t, ts)
	dir, err := client.DownloadPackage(RegistryEntry{ID: "noreadme", URL: ts.URL + "/noreadme"})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// package.toml and main.js should exist.
	for _, name := range []string{"package.toml", "main.js"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected %s to exist", name)
		}
	}

	// README.md should NOT exist.
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err == nil {
		t.Error("expected README.md to be absent when server returns 404")
	}
}

// ---------------------------------------------------------------------------
// DownloadPackage — path traversal + invalid ID
// ---------------------------------------------------------------------------

func TestRegistryClient_DownloadPathTraversal(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("data"))
	}))
	defer ts.Close()

	client := testRegistryClient(t, ts)

	entry := RegistryEntry{
		ID:  "test-pkg",
		URL: ts.URL + "/../../etc/passwd",
	}
	_, err := client.DownloadPackage(entry)
	if err == nil {
		t.Error("expected rejection for path traversal in URL")
	}
}

func TestRegistryClient_DownloadInvalidID(t *testing.T) {
	client := &RegistryClient{
		baseURL:       "https://example.com",
		allowedHost:   "example.com",
		allowedScheme: "https",
		cacheDir:      t.TempDir(),
	}

	entry := RegistryEntry{
		ID:  "../escape",
		URL: "https://example.com/pkg",
	}
	_, err := client.DownloadPackage(entry)
	if err == nil {
		t.Error("expected rejection for invalid package ID")
	}
}

// ---------------------------------------------------------------------------
// FetchIndex + cache fallback
// ---------------------------------------------------------------------------

func TestRegistryClient_FetchIndex(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.json" {
			w.Write([]byte(`[{"id":"hello","name":"Hello","version":"1.0.0","url":"https://example.com/hello"}]`))
		}
	}))
	defer ts.Close()

	client := testRegistryClient(t, ts)

	entries, err := client.FetchIndex()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries len = %d, want 1", len(entries))
	}
	if entries[0].ID != "hello" {
		t.Errorf("entries[0].ID = %q", entries[0].ID)
	}
}

func TestRegistryClient_FetchIndexCacheFallback(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	}))
	defer ts.Close()

	cacheDir := t.TempDir()
	cacheContent := `[{"id":"cached","name":"Cached Pkg","version":"0.1.0"}]`
	writeFile(t, cacheDir, "index.json", cacheContent)

	tsURL, _ := url.Parse(ts.URL)
	client := &RegistryClient{
		baseURL:       ts.URL,
		allowedHost:   tsURL.Host,
		allowedScheme: tsURL.Scheme,
		client:        ts.Client(),
		cacheDir:      cacheDir,
	}

	entries, err := client.FetchIndex()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != "cached" {
		t.Error("should fall back to cached index")
	}
}

// ---------------------------------------------------------------------------
// No redirect
// ---------------------------------------------------------------------------

func TestRegistryClient_NoRedirect(t *testing.T) {
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("redirected"))
	}))
	defer redirectTarget.Close()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL, http.StatusFound)
	}))
	defer ts.Close()

	tsURL, _ := url.Parse(ts.URL)
	client := &RegistryClient{
		baseURL:       ts.URL,
		allowedHost:   tsURL.Host,
		allowedScheme: tsURL.Scheme,
		client: &http.Client{
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		cacheDir: t.TempDir(),
	}

	entry := RegistryEntry{
		ID:  "redirect-pkg",
		URL: ts.URL + "/redirect",
	}
	_, err := client.DownloadPackage(entry)
	if err == nil {
		t.Error("expected failure — redirects should not be followed")
	}
}

// ---------------------------------------------------------------------------
// FilterEntries — table driven
// ---------------------------------------------------------------------------

func TestFilterEntries(t *testing.T) {
	entries := []RegistryEntry{
		{ID: "rss-digest", Name: "RSS Digest", Description: "RSS feed summary"},
		{ID: "weather", Name: "Weather", Description: "Weather briefing"},
		{ID: "reminder", Name: "Reminder", Description: "Set reminders"},
	}

	tests := []struct {
		query string
		want  int
		ids   []string
	}{
		{"", 3, []string{"rss-digest", "weather", "reminder"}},
		{"rss", 1, []string{"rss-digest"}},
		{"RSS", 1, []string{"rss-digest"}}, // case insensitive
		{"brief", 1, []string{"weather"}},  // matches description
		{"nonexistent", 0, nil},
		{"re", 1, []string{"reminder"}}, // only "reminder" contains "re"
	}

	for _, tc := range tests {
		t.Run("query="+tc.query, func(t *testing.T) {
			got := FilterEntries(entries, tc.query)
			if len(got) != tc.want {
				var gotIDs []string
				for _, e := range got {
					gotIDs = append(gotIDs, e.ID)
				}
				t.Errorf("FilterEntries(%q) returned %d entries %v, want %d", tc.query, len(got), gotIDs, tc.want)
				return
			}
			if tc.ids != nil {
				for i, e := range got {
					if e.ID != tc.ids[i] {
						t.Errorf("got[%d].ID = %q, want %q", i, e.ID, tc.ids[i])
					}
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// SearchEntries
// ---------------------------------------------------------------------------

func TestRegistryClient_SearchEntries(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.json" {
			w.Write([]byte(`[
				{"id":"rss-digest","name":"RSS Digest","version":"1.0.0","description":"RSS feed summary"},
				{"id":"weather","name":"Weather","version":"1.0.0","description":"Weather briefing"}
			]`))
		}
	}))
	defer ts.Close()

	client := testRegistryClient(t, ts)

	results, err := client.SearchEntries("rss")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "rss-digest" {
		t.Errorf("SearchEntries(\"rss\") = %v, want [rss-digest]", results)
	}

	// Empty query returns all.
	all, err := client.SearchEntries("")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Errorf("SearchEntries(\"\") returned %d, want 2", len(all))
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSearchEntries(t *testing.T) {
	entries := []RegistryEntry{
		{ID: "weather", Name: "Weather Skill", Description: "Get weather info"},
		{ID: "todo", Name: "Todo Manager", Description: "Manage tasks"},
		{ID: "daily-news", Name: "Daily News", Description: "Fetch daily weather and news"},
	}

	// Match by ID
	results := SearchEntries(entries, "weather")
	if len(results) != 2 { // "weather" by ID and "daily-news" by description
		t.Errorf("search 'weather' got %d results, want 2", len(results))
	}

	// Match by name
	results = SearchEntries(entries, "todo")
	if len(results) != 1 {
		t.Errorf("search 'todo' got %d results, want 1", len(results))
	}

	// No match
	results = SearchEntries(entries, "nonexistent")
	if len(results) != 0 {
		t.Errorf("search 'nonexistent' got %d results, want 0", len(results))
	}

	// Empty keyword returns all
	results = SearchEntries(entries, "")
	if len(results) != 3 {
		t.Errorf("empty search got %d results, want 3", len(results))
	}

	// Case insensitive
	results = SearchEntries(entries, "WEATHER")
	if len(results) != 2 {
		t.Errorf("case insensitive search got %d results, want 2", len(results))
	}
}
