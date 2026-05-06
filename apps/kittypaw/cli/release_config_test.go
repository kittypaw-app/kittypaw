package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseConfigTargetsKittypawOrg(t *testing.T) {
	root := filepath.Join("..")
	repoRoot := filepath.Join(root, "..", "..")
	checks := map[string][]string{
		filepath.Join(root, ".goreleaser.yml"): {
			"owner: kittypaw-app",
			"name: kitty",
			"https://raw.githubusercontent.com/kittypaw-app/kitty/main/install-kittypaw.sh",
			"install-kittypaw.sh",
		},
		filepath.Join(root, "install-kittypaw.sh"): {
			`REPO="${KITTYPAW_INSTALL_REPO:-kittypaw-app/kitty}"`,
		},
		filepath.Join(repoRoot, "install-kittypaw.sh"): {
			"https://raw.githubusercontent.com/${REPO}/main/apps/kittypaw/install-kittypaw.sh",
		},
	}
	legacyPaths := []string{
		filepath.Join(root, "install.sh"),
		filepath.Join(repoRoot, "install.sh"),
	}

	for path, wants := range checks {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		content := string(b)
		if strings.Contains(content, "jinto/kittypaw") {
			t.Fatalf("%s still points release/download metadata at jinto/kittypaw", path)
		}
		for _, want := range wants {
			if !strings.Contains(content, want) {
				t.Fatalf("%s missing %q", path, want)
			}
		}
	}

	for _, path := range legacyPaths {
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("%s should not exist; use install-kittypaw.sh", path)
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", path, err)
		}
	}
}
