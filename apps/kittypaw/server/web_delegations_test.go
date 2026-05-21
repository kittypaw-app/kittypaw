package server

import (
	"os"
	"strings"
	"testing"
)

func readWebAssetForDelegationsTest(t *testing.T, path string) string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(src)
}

func TestDelegationsWebAssetsAreLoadedAndMounted(t *testing.T) {
	index := readWebAssetForDelegationsTest(t, "web/index.html")
	if !strings.Contains(index, `<script src="/delegations.js"></script>`) {
		t.Fatal("web index must load delegations.js")
	}

	app := readWebAssetForDelegationsTest(t, "web/app.js")
	for _, token := range []string{
		`data-tab="delegations"`,
		`Delegations.mount(content)`,
	} {
		if !strings.Contains(app, token) {
			t.Fatalf("web app missing delegations token %s", token)
		}
	}
}

func TestDelegationsWebModuleUsesTreeAPIsAndCancelControls(t *testing.T) {
	src := readWebAssetForDelegationsTest(t, "web/delegations.js")
	for _, token := range []string{
		"const Delegations =",
		"/api/v1/conversations?limit=50",
		"/api/v1/delegations?limit=50",
		"/api/v1/delegations/tree?conversation_id=",
		"/api/v1/delegations/' + encodeURIComponent(jobID) + '/cancel",
		"parent_conversation_id",
		"_treeNodeHTML",
		"_cancelJob",
		"status === 'queued' || status === 'running'",
	} {
		if !strings.Contains(src, token) {
			t.Fatalf("delegations module missing token %s", token)
		}
	}
}

func TestDelegationsWebModuleIgnoresStaleTreeResponses(t *testing.T) {
	src := readWebAssetForDelegationsTest(t, "web/delegations.js")
	for _, token := range []string{
		"treeRequestSeq: 0",
		"const requestedConversationID = this.selectedConversationID",
		"const requestSeq = ++this.treeRequestSeq",
		"requestSeq !== this.treeRequestSeq",
		"encodeURIComponent(requestedConversationID)",
		"requestedConversationID !== this.selectedConversationID",
		"return;",
	} {
		if !strings.Contains(src, token) {
			t.Fatalf("delegations module missing stale response guard token %s", token)
		}
	}
}

func TestDelegationsWebStylesProvideTreeSurface(t *testing.T) {
	src := readWebAssetForDelegationsTest(t, "web/style.css")
	for _, token := range []string{
		".delegations-view",
		".delegations-toolbar",
		".delegations-tree",
		".delegations-node",
		".delegations-node-children",
		".delegations-status",
		".delegations-status--running",
		".delegations-summary",
	} {
		if !strings.Contains(src, token) {
			t.Fatalf("delegations styles missing token %s", token)
		}
	}
}
