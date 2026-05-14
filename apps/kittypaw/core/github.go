package core

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SourceFormat describes the packaging format of a skill source.
type SourceFormat string

const (
	SourceFormatNative        SourceFormat = "native"  // package.toml + main.js
	SourceFormatMarkdownSkill SourceFormat = "skillmd" // SKILL.md
)

// SourceResult holds the result of resolving a skill source from GitHub.
type SourceResult struct {
	Format         SourceFormat
	SkillMdContent string // populated for SkillMd format
	TempDir        string // populated for Native format (caller must clean up)
	SourceURL      string // original GitHub URL
}

// ParseGitHubURL extracts owner and repo from a GitHub URL.
// Only HTTPS github.com URLs are accepted.
func ParseGitHubURL(rawURL string) (owner, repo string, err error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", "", fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Scheme != "https" {
		return "", "", fmt.Errorf("only HTTPS GitHub URLs are supported: %s", rawURL)
	}
	if parsed.Host != "github.com" {
		return "", "", fmt.Errorf("only github.com URLs are supported, got %s", parsed.Host)
	}

	// Path is /owner/repo or /owner/repo/ or /owner/repo.git
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("GitHub URL must be https://github.com/<owner>/<repo>: %s", rawURL)
	}

	repo = strings.TrimSuffix(parts[1], ".git")
	return parts[0], repo, nil
}

// ResolveGitHubSource probes a GitHub repository for skill files and returns
// the discovered source. Probing order:
//  1. SKILL.md on main branch, then master
//  2. package.toml on main branch, then master
//  3. .agents/skills/{repo}/SKILL.md on main, then master
//
// baseURL can be overridden for testing (default: https://raw.githubusercontent.com).
func ResolveGitHubSource(baseURL, owner, repo string) (*SourceResult, error) {
	client := &http.Client{
		Timeout: 30 * time.Second,
		// Allow redirects — raw.githubusercontent.com may redirect via CDN.
	}

	branches := []string{"main", "master"}

	// Probe 1: SKILL.md at root
	for _, branch := range branches {
		path := fmt.Sprintf("/%s/%s/%s/SKILL.md", owner, repo, branch)
		body, err := fetchRaw(client, baseURL+path)
		if err == nil {
			return &SourceResult{
				Format:         SourceFormatMarkdownSkill,
				SkillMdContent: string(body),
				SourceURL:      fmt.Sprintf("https://github.com/%s/%s", owner, repo),
			}, nil
		}
	}

	// Probe 2: package.toml + main.js at root
	for _, branch := range branches {
		tomlPath := fmt.Sprintf("/%s/%s/%s/package.toml", owner, repo, branch)
		tomlBody, err := fetchRaw(client, baseURL+tomlPath)
		if err != nil {
			continue
		}

		jsPath := fmt.Sprintf("/%s/%s/%s/main.js", owner, repo, branch)
		jsBody, err := fetchRaw(client, baseURL+jsPath)
		if err != nil {
			continue
		}

		// Write to temp dir
		tmpDir, err := os.MkdirTemp("", "kittypaw-github-"+repo+"-")
		if err != nil {
			return nil, fmt.Errorf("create temp dir: %w", err)
		}

		if err := os.WriteFile(filepath.Join(tmpDir, "package.toml"), tomlBody, 0o644); err != nil {
			os.RemoveAll(tmpDir)
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(tmpDir, "main.js"), jsBody, 0o644); err != nil {
			os.RemoveAll(tmpDir)
			return nil, err
		}

		return &SourceResult{
			Format:    SourceFormatNative,
			TempDir:   tmpDir,
			SourceURL: fmt.Sprintf("https://github.com/%s/%s", owner, repo),
		}, nil
	}

	// Probe 3: .agents/skills/{repo}/SKILL.md
	for _, branch := range branches {
		path := fmt.Sprintf("/%s/%s/%s/.agents/skills/%s/SKILL.md", owner, repo, branch, repo)
		body, err := fetchRaw(client, baseURL+path)
		if err == nil {
			return &SourceResult{
				Format:         SourceFormatMarkdownSkill,
				SkillMdContent: string(body),
				SourceURL:      fmt.Sprintf("https://github.com/%s/%s", owner, repo),
			}, nil
		}
	}

	return nil, fmt.Errorf("no supported skill files found in github.com/%s/%s (tried SKILL.md, package.toml)", owner, repo)
}

// fetchRaw downloads a single file from a raw URL. Returns the body on 200,
// or an error for any other status.
func fetchRaw(client *http.Client, rawURL string) ([]byte, error) {
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("GitHub API rate limit exceeded (403). Try again later")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20)) // 2MB max
	if err != nil {
		return nil, err
	}
	return body, nil
}
