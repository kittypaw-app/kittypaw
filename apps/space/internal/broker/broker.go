package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kittypaw-app/kittyspace/internal/protocol"
)

var (
	ErrDeviceOffline        = errors.New("device offline")
	ErrForbidden            = errors.New("forbidden")
	ErrBackpressure         = errors.New("too many in-flight requests")
	ErrUnsupportedOperation = errors.New("operation unsupported by device")
)

type Config struct {
	RequestTimeout       time.Duration
	MaxInflightPerDevice int
}

type DevicePrincipal struct {
	UserID          string
	DeviceID        string
	LocalAccountIDs []string
	Capabilities    []protocol.Operation
}

type Route struct {
	DeviceID        string
	LocalAccountIDs []string
	Capabilities    []protocol.Operation
}

type DeviceConn interface {
	Send(ctx context.Context, frame protocol.Frame) error
	Close() error
}

type Request struct {
	UserID    string
	DeviceID  string
	AccountID string
	Operation protocol.Operation
	Method    string
	Path      string
	Body      []byte
}

type Broker struct {
	mu      sync.Mutex
	cfg     Config
	devices map[deviceKey]*deviceState
}

type deviceKey struct {
	userID   string
	deviceID string
}

type deviceState struct {
	principal    DevicePrincipal
	accounts     map[string]struct{}
	capabilities map[protocol.Operation]struct{}
	conn         DeviceConn
	pending      map[string]chan protocol.Frame
}

func New(cfg Config) *Broker {
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 120 * time.Second
	}
	if cfg.MaxInflightPerDevice <= 0 {
		cfg.MaxInflightPerDevice = 16
	}
	return &Broker{
		cfg:     cfg,
		devices: make(map[deviceKey]*deviceState),
	}
}

func (b *Broker) Register(ctx context.Context, principal DevicePrincipal, conn DeviceConn) error {
	if principal.UserID == "" {
		return fmt.Errorf("user_id is required")
	}
	if principal.DeviceID == "" {
		return fmt.Errorf("device_id is required")
	}
	if len(principal.LocalAccountIDs) == 0 {
		return fmt.Errorf("at least one local account is required")
	}
	accounts := make(map[string]struct{}, len(principal.LocalAccountIDs))
	for _, accountID := range principal.LocalAccountIDs {
		if accountID == "" {
			return fmt.Errorf("local account id is required")
		}
		accounts[accountID] = struct{}{}
	}
	if len(principal.Capabilities) == 0 {
		return fmt.Errorf("at least one capability is required")
	}
	capabilities := make(map[protocol.Operation]struct{}, len(principal.Capabilities))
	for _, capability := range principal.Capabilities {
		if !protocol.AllowedOperation(capability) {
			return fmt.Errorf("capability is not supported")
		}
		capabilities[capability] = struct{}{}
	}
	if conn == nil {
		return fmt.Errorf("device connection is required")
	}

	key := keyFor(principal.UserID, principal.DeviceID)
	b.mu.Lock()
	old := b.devices[key]
	b.devices[key] = &deviceState{
		principal:    principal,
		accounts:     accounts,
		capabilities: capabilities,
		conn:         conn,
		pending:      make(map[string]chan protocol.Frame),
	}
	b.mu.Unlock()

	if old != nil {
		_ = old.conn.Close()
		for id, ch := range old.pending {
			ch <- protocol.Frame{Type: protocol.FrameError, ID: id, Code: "replaced", Message: "device connection replaced"}
			close(ch)
		}
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func (b *Broker) Unregister(userID, deviceID string, conn DeviceConn) {
	key := keyFor(userID, deviceID)
	b.mu.Lock()
	state := b.devices[key]
	if state != nil && state.conn == conn {
		delete(b.devices, key)
	} else {
		state = nil
	}
	b.mu.Unlock()
	if state == nil {
		return
	}
	_ = state.conn.Close()
	for id, ch := range state.pending {
		ch <- protocol.Frame{Type: protocol.FrameError, ID: id, Code: "offline", Message: "device offline"}
		close(ch)
	}
}

func (b *Broker) IsOnline(userID, deviceID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.devices[keyFor(userID, deviceID)]
	return ok
}

func (b *Broker) Routes(userID string) []Route {
	b.mu.Lock()
	defer b.mu.Unlock()

	routes := make([]Route, 0)
	for key, state := range b.devices {
		if key.userID != userID {
			continue
		}
		accounts := make([]string, 0, len(state.accounts))
		for accountID := range state.accounts {
			accounts = append(accounts, accountID)
		}
		sort.Strings(accounts)

		capabilities := make([]protocol.Operation, 0, len(state.capabilities))
		for capability := range state.capabilities {
			capabilities = append(capabilities, capability)
		}
		sort.Slice(capabilities, func(i, j int) bool {
			return capabilities[i] < capabilities[j]
		})

		routes = append(routes, Route{
			DeviceID:        key.deviceID,
			LocalAccountIDs: accounts,
			Capabilities:    capabilities,
		})
	}
	sort.Slice(routes, func(i, j int) bool {
		return routes[i].DeviceID < routes[j].DeviceID
	})
	return routes
}

func (b *Broker) Request(ctx context.Context, req Request) (<-chan protocol.Frame, error) {
	frame := protocol.Frame{
		Type:      protocol.FrameRequest,
		ID:        "req_" + uuid.NewString(),
		AccountID: req.AccountID,
		Operation: req.Operation,
		Method:    req.Method,
		Path:      req.Path,
		Body:      json.RawMessage(req.Body),
	}
	if err := frame.Validate(); err != nil {
		return nil, err
	}

	b.mu.Lock()
	key := keyFor(req.UserID, req.DeviceID)
	state := b.devices[key]
	if state == nil {
		b.mu.Unlock()
		return nil, ErrDeviceOffline
	}
	if _, ok := state.accounts[req.AccountID]; !ok {
		b.mu.Unlock()
		return nil, ErrForbidden
	}
	if _, ok := state.capabilities[req.Operation]; !ok {
		b.mu.Unlock()
		return nil, ErrUnsupportedOperation
	}
	if len(state.pending) >= b.cfg.MaxInflightPerDevice {
		b.mu.Unlock()
		return nil, ErrBackpressure
	}

	stream := make(chan protocol.Frame, 16)
	state.pending[frame.ID] = stream
	conn := state.conn
	timeout := b.cfg.RequestTimeout
	b.mu.Unlock()

	if err := conn.Send(ctx, frame); err != nil {
		b.finish(key, frame.ID, protocol.Frame{
			Type:    protocol.FrameError,
			ID:      frame.ID,
			Code:    "send_failed",
			Message: err.Error(),
		})
		return nil, err
	}

	go func() {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			b.finish(key, frame.ID, protocol.Frame{
				Type:    protocol.FrameError,
				ID:      frame.ID,
				Code:    "canceled",
				Message: ctx.Err().Error(),
			})
		case <-timer.C:
			b.finish(key, frame.ID, protocol.Frame{
				Type:    protocol.FrameError,
				ID:      frame.ID,
				Code:    "timeout",
				Message: "request timed out",
			})
		}
	}()

	return stream, nil
}

func (b *Broker) Deliver(userID, deviceID string, frame protocol.Frame) {
	key := keyFor(userID, deviceID)
	if frame.Type == protocol.FrameResponseEnd || frame.Type == protocol.FrameError {
		b.finish(key, frame.ID, frame)
		return
	}

	b.mu.Lock()
	state := b.devices[key]
	var ch chan protocol.Frame
	if state != nil {
		ch = state.pending[frame.ID]
	}
	b.mu.Unlock()

	if ch == nil {
		return
	}
	ch <- frame
}

func (b *Broker) finish(key deviceKey, requestID string, frame protocol.Frame) {
	b.mu.Lock()
	state := b.devices[key]
	var ch chan protocol.Frame
	if state != nil {
		ch = state.pending[requestID]
		delete(state.pending, requestID)
	}
	b.mu.Unlock()

	if ch == nil {
		return
	}
	ch <- frame
	close(ch)
}

func keyFor(userID, deviceID string) deviceKey {
	return deviceKey{userID: userID, deviceID: deviceID}
}
