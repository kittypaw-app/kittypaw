package webapp

import "testing"

func TestSpaceAppAPIPathMapsChatRoutes(t *testing.T) {
	path, ok := appAPIPath("/chat/api/routes")
	if !ok || path != "/v1/routes" {
		t.Fatalf("appAPIPath routes = (%q, %v), want /v1/routes true", path, ok)
	}

	path, ok = appAPIPath("/chat/api/nodes/dev_1/accounts/alice/v1/chat/completions")
	if !ok || path != "/nodes/dev_1/accounts/alice/v1/chat/completions" {
		t.Fatalf("appAPIPath nodes = (%q, %v)", path, ok)
	}
}
