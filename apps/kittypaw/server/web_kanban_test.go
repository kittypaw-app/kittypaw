package server

import (
	"os"
	"strings"
	"testing"
)

func readWebAssetForKanbanTest(t *testing.T, path string) string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(src)
}

func TestKanbanWebAssetsAreLoadedAndMounted(t *testing.T) {
	index := readWebAssetForKanbanTest(t, "web/index.html")
	if !strings.Contains(index, `<script src="/kanban.js"></script>`) {
		t.Fatal("web index must load kanban.js")
	}

	app := readWebAssetForKanbanTest(t, "web/app.js")
	if !strings.Contains(app, `data-tab="kanban"`) {
		t.Fatal("default shell must expose a Kanban nav item")
	}
	if !strings.Contains(app, "tab === 'kanban'") || !strings.Contains(app, "Kanban.mount(content)") {
		t.Fatal("switchTab must mount the Kanban module")
	}
}
