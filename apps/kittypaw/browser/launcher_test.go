package browser

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseDevToolsActivePort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "DevToolsActivePort")
	if err := os.WriteFile(path, []byte("49231\n/devtools/browser/abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	port, browserPath, err := parseDevToolsActivePort(path)
	if err != nil {
		t.Fatalf("parseDevToolsActivePort: %v", err)
	}
	if port != "49231" || browserPath != "/devtools/browser/abc" {
		t.Fatalf("port/path = %q/%q", port, browserPath)
	}
}

func TestBuildChromeArgs(t *testing.T) {
	args := buildChromeArgs("/tmp/profile", true)
	joined := strings.Join(args, "\n")
	for _, want := range []string{
		"--remote-debugging-port=0",
		"--remote-debugging-address=127.0.0.1",
		"--user-data-dir=/tmp/profile",
		"--no-first-run",
		"--no-default-browser-check",
		"--headless=new",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args missing %s: %v", want, args)
		}
	}
}

func TestDefaultChromeCandidates(t *testing.T) {
	candidates := defaultChromeCandidates()
	if len(candidates) == 0 {
		t.Fatal("expected candidates")
	}
	switch runtime.GOOS {
	case "darwin":
		if candidates[0] != "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" {
			t.Fatalf("first darwin candidate = %q", candidates[0])
		}
	case "linux":
		if candidates[0] == "" {
			t.Fatal("first linux candidate empty")
		}
	case "windows":
		if candidates[0] == "" {
			t.Fatal("first windows candidate empty")
		}
	}
}
