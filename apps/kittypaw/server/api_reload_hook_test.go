package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/jinto/kittypaw/core"
)

func TestHandleReloadRunsPostReloadHook(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.LLM.APIKey = "test-key"
	writeReloadConfig(t, cfg)

	srv, _ := newReloadTestServer(t, &cfg, []*core.Account{
		{ID: DefaultAccountID, Config: &cfg},
	})
	var hookN int32
	srv.SetPostReloadHook(func(context.Context) error {
		atomic.AddInt32(&hookN, 1)
		return nil
	})

	ts := httptest.NewServer(http.HandlerFunc(srv.handleReload))
	defer ts.Close()
	resp, err := http.Post(ts.URL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&hookN); got != 1 {
		t.Fatalf("post reload hook calls = %d, want 1", got)
	}
}
