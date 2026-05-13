package browser

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jinto/kittypaw/core"
)

func TestControllerBackendStatusBeforeLaunch(t *testing.T) {
	c := NewController(ControllerOptions{Config: testBrowserConfig(), BaseDir: t.TempDir()})
	status, err := c.status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Enabled || status.Running {
		t.Fatalf("status = %#v", status)
	}
}

func TestControllerBackendOpenUsesCDP(t *testing.T) {
	conn := newFakeCDPConn()
	c := NewController(ControllerOptions{Config: testBrowserConfig(), BaseDir: t.TempDir()})
	c.client = newCDPClient(conn)
	c.targets = make(map[string]string)
	defer c.client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	done := make(chan tabInfo, 1)
	errCh := make(chan error, 1)
	go func() {
		tab, err := c.open(ctx, "https://example.com")
		if err != nil {
			errCh <- err
			return
		}
		done <- tab
	}()

	req := readCDPRequest(t, conn)
	if req.Method != "Target.createTarget" {
		t.Fatalf("first method = %s", req.Method)
	}
	conn.reads <- []byte(`{"id":1,"result":{"targetId":"target-1"}}`)

	req = readCDPRequest(t, conn)
	if req.Method != "Target.attachToTarget" {
		t.Fatalf("second method = %s", req.Method)
	}
	conn.reads <- []byte(`{"id":2,"result":{"sessionId":"session-1"}}`)

	req = readCDPRequest(t, conn)
	if req.Method != "Target.activateTarget" {
		t.Fatalf("third method = %s", req.Method)
	}
	conn.reads <- []byte(`{"id":3,"result":{}}`)

	req = readCDPRequest(t, conn)
	if req.Method != "Target.getTargets" {
		t.Fatalf("fourth method = %s", req.Method)
	}
	conn.reads <- []byte(`{"id":4,"result":{"targetInfos":[{"targetId":"target-1","type":"page","url":"https://example.com","title":"Example"}]}}`)

	select {
	case err := <-errCh:
		t.Fatalf("open: %v", err)
	case tab := <-done:
		if tab.TargetID != "target-1" || tab.URL != "https://example.com" || tab.Title != "Example" || !tab.Active {
			t.Fatalf("tab = %#v", tab)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestControllerBackendEvaluateUsesRuntime(t *testing.T) {
	conn := newFakeCDPConn()
	c := NewController(ControllerOptions{Config: testBrowserConfig(), BaseDir: t.TempDir()})
	c.client = newCDPClient(conn)
	c.targets = map[string]string{"target-1": "session-1"}
	c.activeTargetID = "target-1"
	defer c.client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	done := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		out, err := c.evaluate(ctx, "1+1")
		if err != nil {
			errCh <- err
			return
		}
		done <- out
	}()

	req := readCDPRequest(t, conn)
	if req.Method != "Runtime.evaluate" || req.SessionID != "session-1" {
		t.Fatalf("request = %#v", req)
	}
	conn.reads <- []byte(`{"id":1,"result":{"result":{"type":"number","value":2}}}`)

	select {
	case err := <-errCh:
		t.Fatalf("evaluate: %v", err)
	case got := <-done:
		if got != "2" {
			t.Fatalf("got %q", got)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestControllerBackendRefActionsUseSnapshotTarget(t *testing.T) {
	conn := newFakeCDPConn()
	c := NewController(ControllerOptions{Config: testBrowserConfig(), BaseDir: t.TempDir()})
	c.client = newCDPClient(conn)
	c.targets = map[string]string{
		"target-active": "session-active",
		"target-snap":   "session-snap",
	}
	c.activeTargetID = "target-active"
	defer c.client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	snapshotDone := make(chan error, 1)
	go func() {
		_, err := c.snapshot(ctx, map[string]any{"target_id": "target-snap"})
		snapshotDone <- err
	}()

	req := readCDPRequest(t, conn)
	if req.Method != "Runtime.evaluate" || req.SessionID != "session-snap" {
		t.Fatalf("snapshot request = %#v", req)
	}
	conn.reads <- []byte(`{"id":1,"result":{"result":{"type":"object","value":{"url":"https://example.com","title":"Example","text":"","elements":[{"role":"button","text":"Go","selector":"#go"}]}}}}`)
	if err := <-snapshotDone; err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	clickDone := make(chan error, 1)
	go func() {
		_, err := c.click(ctx, "e1")
		clickDone <- err
	}()

	req = readCDPRequest(t, conn)
	if req.Method != "Runtime.evaluate" || req.SessionID != "session-snap" {
		t.Fatalf("click point request = %#v", req)
	}
	conn.reads <- []byte(`{"id":2,"result":{"result":{"type":"object","value":{"x":10,"y":20}}}}`)

	req = readCDPRequest(t, conn)
	if req.Method != "Input.dispatchMouseEvent" || req.SessionID != "session-snap" {
		t.Fatalf("click press request = %#v", req)
	}
	conn.reads <- []byte(`{"id":3,"result":{}}`)

	req = readCDPRequest(t, conn)
	if req.Method != "Input.dispatchMouseEvent" || req.SessionID != "session-snap" {
		t.Fatalf("click release request = %#v", req)
	}
	conn.reads <- []byte(`{"id":4,"result":{}}`)

	if err := <-clickDone; err != nil {
		t.Fatalf("click: %v", err)
	}

	typeDone := make(chan error, 1)
	go func() {
		_, err := c.typeText(ctx, "e1", "hello")
		typeDone <- err
	}()

	req = readCDPRequest(t, conn)
	if req.Method != "Runtime.evaluate" || req.SessionID != "session-snap" {
		t.Fatalf("type request = %#v", req)
	}
	conn.reads <- []byte(`{"id":5,"result":{"result":{"type":"object","value":{"success":true}}}}`)

	if err := <-typeDone; err != nil {
		t.Fatalf("typeText: %v", err)
	}
}

func testBrowserConfig() core.BrowserConfig {
	return core.BrowserConfig{Enabled: true, TimeoutSeconds: 1}
}

func readCDPRequest(t *testing.T, conn *fakeCDPConn) cdpRequest {
	t.Helper()
	var req cdpRequest
	if err := json.Unmarshal(<-conn.writes, &req); err != nil {
		t.Fatalf("request json: %v", err)
	}
	return req
}
