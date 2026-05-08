package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jinto/kittypaw/core"
)

func seedActiveStaffFile(t *testing.T, baseDir, id, displayName, description string, aliases ...string) {
	t.Helper()
	base, err := core.ResolveBaseDir(baseDir)
	if err != nil {
		t.Fatal(err)
	}
	meta := core.StaffMetaFile{
		ID:          id,
		DisplayName: displayName,
		Description: description,
		Aliases:     aliases,
	}
	if err := core.WriteStaffMetaFile(base, meta); err != nil {
		t.Fatalf("write staff meta %s: %v", id, err)
	}
	if err := os.WriteFile(filepath.Join(base, "staff", id, "SOUL.md"), []byte(id+" soul"), 0o644); err != nil {
		t.Fatalf("write staff soul %s: %v", id, err)
	}
}
