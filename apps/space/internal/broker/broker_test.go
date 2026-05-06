package broker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kittypaw-app/kittyspace/internal/protocol"
)

type fakeConn struct {
	requests chan protocol.Frame
	closed   chan struct{}
}

func newFakeConn() *fakeConn {
	return &fakeConn{
		requests: make(chan protocol.Frame, 8),
		closed:   make(chan struct{}),
	}
}

func (c *fakeConn) Send(ctx context.Context, frame protocol.Frame) error {
	select {
	case c.requests <- frame:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *fakeConn) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return nil
}

func TestBrokerRequestForwardsToRegisteredDevice(t *testing.T) {
	b := New(Config{RequestTimeout: time.Second, MaxInflightPerDevice: 2})
	conn := newFakeConn()
	principal := DevicePrincipal{
		UserID:          "user_1",
		DeviceID:        "dev_1",
		LocalAccountIDs: []string{"alice"},
		Capabilities:    []protocol.Operation{protocol.OperationOpenAIModels},
	}
	if err := b.Register(context.Background(), principal, conn); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	stream, err := b.Request(context.Background(), Request{
		UserID:    "user_1",
		DeviceID:  "dev_1",
		AccountID: "alice",
		Operation: protocol.OperationOpenAIModels,
		Method:    "GET",
		Path:      "/v1/models",
	})
	if err != nil {
		t.Fatalf("Request() error = %v", err)
	}

	var sent protocol.Frame
	select {
	case sent = <-conn.requests:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for request frame")
	}
	if sent.Type != protocol.FrameRequest || sent.ID == "" || sent.Operation != protocol.OperationOpenAIModels || sent.Path != "/v1/models" {
		t.Fatalf("sent frame = %+v", sent)
	}

	b.Deliver("user_1", "dev_1", protocol.Frame{Type: protocol.FrameResponseEnd, ID: sent.ID})
	select {
	case got := <-stream:
		if got.Type != protocol.FrameResponseEnd || got.ID != sent.ID {
			t.Fatalf("stream frame = %+v, want response_end for %s", got, sent.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for response frame")
	}
}

func TestBrokerOfflineDeviceReturnsError(t *testing.T) {
	b := New(Config{RequestTimeout: time.Second, MaxInflightPerDevice: 2})
	_, err := b.Request(context.Background(), Request{
		UserID:    "user_1",
		DeviceID:  "missing",
		AccountID: "alice",
		Operation: protocol.OperationOpenAIModels,
	})
	if !errors.Is(err, ErrDeviceOffline) {
		t.Fatalf("Request() error = %v, want ErrDeviceOffline", err)
	}
}

func TestBrokerRejectsWrongUserAndAccount(t *testing.T) {
	b := New(Config{RequestTimeout: time.Second, MaxInflightPerDevice: 2})
	conn := newFakeConn()
	if err := b.Register(context.Background(), DevicePrincipal{
		UserID:          "user_1",
		DeviceID:        "dev_1",
		LocalAccountIDs: []string{"alice"},
		Capabilities:    []protocol.Operation{protocol.OperationOpenAIModels},
	}, conn); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	_, err := b.Request(context.Background(), Request{
		UserID:    "user_2",
		DeviceID:  "dev_1",
		AccountID: "alice",
		Operation: protocol.OperationOpenAIModels,
	})
	if !errors.Is(err, ErrDeviceOffline) {
		t.Fatalf("wrong user error = %v, want ErrDeviceOffline", err)
	}

	_, err = b.Request(context.Background(), Request{
		UserID:    "user_1",
		DeviceID:  "dev_1",
		AccountID: "bob",
		Operation: protocol.OperationOpenAIModels,
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("wrong account error = %v, want ErrForbidden", err)
	}
}

func TestBrokerDuplicateConnectionClosesOldConnection(t *testing.T) {
	b := New(Config{RequestTimeout: time.Second, MaxInflightPerDevice: 2})
	oldConn := newFakeConn()
	newConn := newFakeConn()
	principal := DevicePrincipal{
		UserID:          "user_1",
		DeviceID:        "dev_1",
		LocalAccountIDs: []string{"alice"},
		Capabilities:    []protocol.Operation{protocol.OperationOpenAIModels},
	}
	if err := b.Register(context.Background(), principal, oldConn); err != nil {
		t.Fatalf("register old: %v", err)
	}
	if err := b.Register(context.Background(), principal, newConn); err != nil {
		t.Fatalf("register new: %v", err)
	}

	select {
	case <-oldConn.closed:
	case <-time.After(time.Second):
		t.Fatal("old connection was not closed")
	}

	b.Unregister("user_1", "dev_1", oldConn)
	if _, err := b.Request(context.Background(), Request{
		UserID:    "user_1",
		DeviceID:  "dev_1",
		AccountID: "alice",
		Operation: protocol.OperationOpenAIModels,
		Method:    "GET",
		Path:      "/v1/models",
	}); err != nil {
		t.Fatalf("request after old unregister = %v, want new connection still registered", err)
	}
	select {
	case <-newConn.requests:
	case <-time.After(time.Second):
		t.Fatal("new connection did not receive request after old unregister")
	}
}

func TestBrokerSeparatesConnectionsByUserAndDevice(t *testing.T) {
	b := New(Config{RequestTimeout: time.Second, MaxInflightPerDevice: 2})
	user1Conn := newFakeConn()
	user2Conn := newFakeConn()
	if err := b.Register(context.Background(), DevicePrincipal{
		UserID:          "user_1",
		DeviceID:        "dev_shared",
		LocalAccountIDs: []string{"alice"},
		Capabilities:    []protocol.Operation{protocol.OperationOpenAIModels},
	}, user1Conn); err != nil {
		t.Fatalf("register user1: %v", err)
	}
	if err := b.Register(context.Background(), DevicePrincipal{
		UserID:          "user_2",
		DeviceID:        "dev_shared",
		LocalAccountIDs: []string{"alice"},
		Capabilities:    []protocol.Operation{protocol.OperationOpenAIModels},
	}, user2Conn); err != nil {
		t.Fatalf("register user2: %v", err)
	}

	if _, err := b.Request(context.Background(), Request{
		UserID:    "user_1",
		DeviceID:  "dev_shared",
		AccountID: "alice",
		Operation: protocol.OperationOpenAIModels,
		Method:    "GET",
		Path:      "/v1/models",
	}); err != nil {
		t.Fatalf("user1 request: %v", err)
	}

	select {
	case <-user1Conn.requests:
	case <-time.After(time.Second):
		t.Fatal("user1 request was not sent to user1 connection")
	}
	select {
	case frame := <-user2Conn.requests:
		t.Fatalf("user2 connection received user1 request: %+v", frame)
	default:
	}
	select {
	case <-user1Conn.closed:
		t.Fatal("registering same device id for user2 closed user1 connection")
	default:
	}
}

func TestBrokerRejectsUnsupportedCapability(t *testing.T) {
	b := New(Config{RequestTimeout: time.Second, MaxInflightPerDevice: 2})
	conn := newFakeConn()
	if err := b.Register(context.Background(), DevicePrincipal{
		UserID:          "user_1",
		DeviceID:        "dev_1",
		LocalAccountIDs: []string{"alice"},
		Capabilities:    []protocol.Operation{protocol.OperationOpenAIModels},
	}, conn); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	_, err := b.Request(context.Background(), Request{
		UserID:    "user_1",
		DeviceID:  "dev_1",
		AccountID: "alice",
		Operation: protocol.OperationOpenAIChatCompletions,
		Method:    "POST",
		Path:      "/v1/chat/completions",
	})
	if !errors.Is(err, ErrUnsupportedOperation) {
		t.Fatalf("Request() error = %v, want ErrUnsupportedOperation", err)
	}
	select {
	case frame := <-conn.requests:
		t.Fatalf("unsupported request was sent to daemon: %+v", frame)
	default:
	}
}

func TestBrokerRoutesReturnsUserScopedSortedSnapshot(t *testing.T) {
	b := New(Config{RequestTimeout: time.Second, MaxInflightPerDevice: 2})
	if err := b.Register(context.Background(), DevicePrincipal{
		UserID:          "user_1",
		DeviceID:        "dev_b",
		LocalAccountIDs: []string{"bob", "alice"},
		Capabilities:    []protocol.Operation{protocol.OperationOpenAIChatCompletions, protocol.OperationOpenAIModels},
	}, newFakeConn()); err != nil {
		t.Fatalf("register user1 dev_b: %v", err)
	}
	if err := b.Register(context.Background(), DevicePrincipal{
		UserID:          "user_1",
		DeviceID:        "dev_a",
		LocalAccountIDs: []string{"carol"},
		Capabilities:    []protocol.Operation{protocol.OperationOpenAIModels},
	}, newFakeConn()); err != nil {
		t.Fatalf("register user1 dev_a: %v", err)
	}
	if err := b.Register(context.Background(), DevicePrincipal{
		UserID:          "user_2",
		DeviceID:        "dev_other",
		LocalAccountIDs: []string{"alice"},
		Capabilities:    []protocol.Operation{protocol.OperationOpenAIModels},
	}, newFakeConn()); err != nil {
		t.Fatalf("register user2: %v", err)
	}

	routes := b.Routes("user_1")
	if len(routes) != 2 {
		t.Fatalf("routes length = %d, want 2: %+v", len(routes), routes)
	}
	if routes[0].DeviceID != "dev_a" || routes[1].DeviceID != "dev_b" {
		t.Fatalf("device order = %+v, want dev_a then dev_b", routes)
	}
	assertStringSlice(t, routes[0].LocalAccountIDs, []string{"carol"})
	assertOperations(t, routes[0].Capabilities, []protocol.Operation{protocol.OperationOpenAIModels})
	assertStringSlice(t, routes[1].LocalAccountIDs, []string{"alice", "bob"})
	assertOperations(t, routes[1].Capabilities, []protocol.Operation{protocol.OperationOpenAIChatCompletions, protocol.OperationOpenAIModels})
}

func TestBrokerRoutesReturnsDefensiveCopies(t *testing.T) {
	b := New(Config{RequestTimeout: time.Second, MaxInflightPerDevice: 2})
	if err := b.Register(context.Background(), DevicePrincipal{
		UserID:          "user_1",
		DeviceID:        "dev_1",
		LocalAccountIDs: []string{"alice"},
		Capabilities:    []protocol.Operation{protocol.OperationOpenAIModels},
	}, newFakeConn()); err != nil {
		t.Fatalf("register: %v", err)
	}

	routes := b.Routes("user_1")
	routes[0].DeviceID = "mutated"
	routes[0].LocalAccountIDs[0] = "mutated"
	routes[0].Capabilities[0] = protocol.OperationOpenAIChatCompletions

	again := b.Routes("user_1")
	if len(again) != 1 {
		t.Fatalf("routes length = %d, want 1", len(again))
	}
	if again[0].DeviceID != "dev_1" {
		t.Fatalf("DeviceID = %q, want dev_1", again[0].DeviceID)
	}
	assertStringSlice(t, again[0].LocalAccountIDs, []string{"alice"})
	assertOperations(t, again[0].Capabilities, []protocol.Operation{protocol.OperationOpenAIModels})
}

func assertStringSlice(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("slice length = %d, want %d: got=%v want=%v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("slice[%d] = %q, want %q: got=%v want=%v", i, got[i], want[i], got, want)
		}
	}
}

func assertOperations(t *testing.T, got, want []protocol.Operation) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("operations length = %d, want %d: got=%v want=%v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("operations[%d] = %q, want %q: got=%v want=%v", i, got[i], want[i], got, want)
		}
	}
}
