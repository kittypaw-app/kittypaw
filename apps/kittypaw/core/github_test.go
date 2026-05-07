package core

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseGitHubURL(t *testing.T) {
	tests := []struct {
		url       string
		wantOwner string
		wantRepo  string
		wantErr   bool
	}{
		{"https://github.com/user/my-skill", "user", "my-skill", false},
		{"https://github.com/org/repo.git", "org", "repo", false},
		{"https://github.com/user/repo/", "user", "repo", false},
		{"https://gitlab.com/user/repo", "", "", true}, // wrong host
		{"https://github.com/user", "", "", true},      // no repo
		{"https://github.com/", "", "", true},          // no path
		{"not-a-url", "", "", true},                    // invalid
		{"http://github.com/user/repo", "", "", true},  // HTTP not allowed
	}

	for _, tt := range tests {
		owner, repo, err := ParseGitHubURL(tt.url)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseGitHubURL(%q) should fail", tt.url)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseGitHubURL(%q) error: %v", tt.url, err)
			continue
		}
		if owner != tt.wantOwner {
			t.Errorf("ParseGitHubURL(%q) owner = %q, want %q", tt.url, owner, tt.wantOwner)
		}
		if repo != tt.wantRepo {
			t.Errorf("ParseGitHubURL(%q) repo = %q, want %q", tt.url, repo, tt.wantRepo)
		}
	}
}

func TestResolveGitHubSourceSkillMd(t *testing.T) {
	skillMd := `---
name: test-skill
description: a test
permissions:
  - Http
---

Do things.
`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user/repo/main/SKILL.md":
			w.Write([]byte(skillMd))
		default:
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()

	result, err := ResolveGitHubSource(ts.URL, "user", "repo")
	if err != nil {
		t.Fatalf("ResolveGitHubSource: %v", err)
	}
	if result.Format != SourceFormatSkillMd {
		t.Errorf("Format = %q, want %q", result.Format, SourceFormatSkillMd)
	}
	if result.SkillMdContent == "" {
		t.Error("SkillMdContent should not be empty")
	}
}

func TestResolveGitHubSourceNative(t *testing.T) {
	pkgToml := `[meta]
id = "test-pkg"
name = "Test"
version = "1.0.0"
`
	mainJS := `return "hello"`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user/repo/main/package.toml":
			w.Write([]byte(pkgToml))
		case "/user/repo/main/main.js":
			w.Write([]byte(mainJS))
		default:
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()

	result, err := ResolveGitHubSource(ts.URL, "user", "repo")
	if err != nil {
		t.Fatalf("ResolveGitHubSource: %v", err)
	}
	if result.Format != SourceFormatNative {
		t.Errorf("Format = %q, want %q", result.Format, SourceFormatNative)
	}
	if result.TempDir == "" {
		t.Error("TempDir should be set for native packages")
	}
}

func TestResolveGitHubSourceMasterFallback(t *testing.T) {
	skillMd := `---
name: master-skill
description: on master branch
---
body
`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user/repo/master/SKILL.md":
			w.Write([]byte(skillMd))
		default:
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()

	result, err := ResolveGitHubSource(ts.URL, "user", "repo")
	if err != nil {
		t.Fatalf("ResolveGitHubSource: %v", err)
	}
	if result.Format != SourceFormatSkillMd {
		t.Errorf("Format = %q, want skillmd", result.Format)
	}
}

func TestResolveGitHubSource404(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer ts.Close()

	_, err := ResolveGitHubSource(ts.URL, "user", "repo")
	if err == nil {
		t.Error("expected error when all probes return 404")
	}
}
