package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/store"
)

func TestDeliveriesAPIListsCurrentAccountLedger(t *testing.T) {
	root := t.TempDir()
	aliceCfg := &core.Config{Server: core.ServerConfig{APIKey: "api-key"}}
	bobCfg := &core.Config{Server: core.ServerConfig{APIKey: "bob-key"}}
	aliceDeps := buildAccountDeps(t, root, "alice", aliceCfg)
	bobDeps := buildAccountDeps(t, root, "bob", bobCfg)
	srv := New([]*AccountDeps{aliceDeps, bobDeps}, "test", "alice")

	if _, err := aliceDeps.Store.CreateOutboundDelivery(store.OutboundDeliveryWrite{
		AccountID:    "alice",
		EventType:    "telegram",
		ChatID:       "alice-chat",
		Source:       store.OutboundDeliverySourceNotify,
		Status:       store.OutboundDeliveryStatusFailed,
		Response:     "alice failed",
		ErrorClass:   "send_failed",
		ErrorMessage: "telegram rejected request",
	}); err != nil {
		t.Fatalf("seed alice delivery: %v", err)
	}
	if _, err := bobDeps.Store.CreateOutboundDelivery(store.OutboundDeliveryWrite{
		AccountID: "bob",
		EventType: "telegram",
		ChatID:    "bob-chat",
		Source:    store.OutboundDeliverySourceNotify,
		Status:    store.OutboundDeliveryStatusFailed,
		Response:  "bob failed",
	}); err != nil {
		t.Fatalf("seed bob delivery: %v", err)
	}

	var body struct {
		Deliveries []store.OutboundDeliveryRecord `json:"deliveries"`
	}
	projectsAPIRequest(t, srv, http.MethodGet, "/api/v1/deliveries?status=failed&limit=10", nil, http.StatusOK, &body)
	if len(body.Deliveries) != 1 {
		t.Fatalf("deliveries = %+v, want one alice row", body.Deliveries)
	}
	got := body.Deliveries[0]
	if got.AccountID != "alice" || got.ChatID != "alice-chat" || got.Status != store.OutboundDeliveryStatusFailed || got.ErrorClass != "send_failed" {
		t.Fatalf("delivery row = %+v", got)
	}
}

func TestDeliveriesAPIAcceptsNonDefaultAccountAPIKey(t *testing.T) {
	root := t.TempDir()
	aliceCfg := &core.Config{Server: core.ServerConfig{APIKey: "alice-key"}}
	bobCfg := &core.Config{Server: core.ServerConfig{APIKey: "bob-key"}}
	aliceDeps := buildAccountDeps(t, root, "alice", aliceCfg)
	bobDeps := buildAccountDeps(t, root, "bob", bobCfg)
	srv := New([]*AccountDeps{aliceDeps, bobDeps}, "test", "alice")

	if _, err := aliceDeps.Store.CreateOutboundDelivery(store.OutboundDeliveryWrite{
		AccountID: "alice",
		EventType: "telegram",
		ChatID:    "alice-chat",
		Source:    store.OutboundDeliverySourceNotify,
		Status:    store.OutboundDeliveryStatusDelivered,
		Response:  "alice delivery",
	}); err != nil {
		t.Fatalf("seed alice delivery: %v", err)
	}
	if _, err := bobDeps.Store.CreateOutboundDelivery(store.OutboundDeliveryWrite{
		AccountID: "bob",
		EventType: "telegram",
		ChatID:    "bob-chat",
		Source:    store.OutboundDeliverySourceNotify,
		Status:    store.OutboundDeliveryStatusDelivered,
		Response:  "bob delivery",
	}); err != nil {
		t.Fatalf("seed bob delivery: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/deliveries?limit=10", nil)
	req.Header.Set("x-api-key", "bob-key")
	rr := httptest.NewRecorder()
	srv.setupRoutes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/deliveries as bob code = %d body=%s, want 200", rr.Code, rr.Body.String())
	}

	var body struct {
		Deliveries []store.OutboundDeliveryRecord `json:"deliveries"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rr.Body.String())
	}
	if len(body.Deliveries) != 1 {
		t.Fatalf("deliveries = %+v, want one bob row", body.Deliveries)
	}
	if got := body.Deliveries[0]; got.AccountID != "bob" || got.ChatID != "bob-chat" {
		t.Fatalf("delivery row = %+v, want bob row", got)
	}
}
