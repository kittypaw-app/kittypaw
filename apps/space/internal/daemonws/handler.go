package daemonws

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/go-chi/chi/v5"
	"github.com/kittypaw-app/kittyspace/internal/broker"
	"github.com/kittypaw-app/kittyspace/internal/protocol"
)

const (
	readTimeout  = 5 * time.Minute
	writeTimeout = 30 * time.Second
)

var ErrUnauthorized = errors.New("unauthorized")

type DeviceAuthenticator interface {
	Authenticate(r *http.Request) (broker.DevicePrincipal, error)
}

type Broker interface {
	Register(ctx context.Context, principal broker.DevicePrincipal, conn broker.DeviceConn) error
	Deliver(userID, deviceID string, frame protocol.Frame)
	Unregister(userID, deviceID string, conn broker.DeviceConn)
}

type Handler struct {
	auth   DeviceAuthenticator
	broker Broker
}

func NewHandler(auth DeviceAuthenticator, b Broker) *Handler {
	return &Handler{auth: auth, broker: b}
}

func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/connect", h.handleConnect)
	return r
}

func (h *Handler) handleConnect(w http.ResponseWriter, r *http.Request) {
	if h.auth == nil || h.broker == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	principal, err := h.auth.Authenticate(r)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		return
	}
	conn.SetReadLimit(1 << 20)

	ctx := r.Context()
	var hello protocol.Frame
	readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = wsjson.Read(readCtx, conn, &hello)
	cancel()
	if err != nil {
		_ = conn.Close(websocket.StatusPolicyViolation, "hello required")
		return
	}
	if err := hello.Validate(); err != nil {
		_ = conn.Close(websocket.StatusPolicyViolation, err.Error())
		return
	}
	activePrincipal, err := principalForHello(hello, principal)
	if err != nil {
		_ = conn.Close(websocket.StatusPolicyViolation, err.Error())
		return
	}

	wsConn := &deviceConn{conn: conn}
	if err := h.broker.Register(ctx, activePrincipal, wsConn); err != nil {
		_ = conn.Close(websocket.StatusInternalError, err.Error())
		return
	}
	defer h.broker.Unregister(activePrincipal.UserID, activePrincipal.DeviceID, wsConn)
	defer func() { _ = conn.CloseNow() }()

	for {
		var frame protocol.Frame
		readCtx, cancel := context.WithTimeout(ctx, readTimeout)
		err := wsjson.Read(readCtx, conn, &frame)
		cancel()
		if err != nil {
			return
		}
		if err := frame.Validate(); err != nil {
			_ = conn.Close(websocket.StatusPolicyViolation, err.Error())
			return
		}
		if frame.Type == protocol.FramePing {
			if err := writePong(ctx, conn, frame.ID); err != nil {
				return
			}
			continue
		}
		if frame.Type == protocol.FramePong {
			continue
		}
		if !daemonResponseFrame(frame.Type) {
			_ = conn.Close(websocket.StatusPolicyViolation, "unexpected daemon frame type")
			return
		}
		h.broker.Deliver(activePrincipal.UserID, activePrincipal.DeviceID, frame)
	}
}

func daemonResponseFrame(frameType protocol.FrameType) bool {
	switch frameType {
	case protocol.FrameResponseHeaders, protocol.FrameResponseChunk, protocol.FrameResponseEnd, protocol.FrameError:
		return true
	default:
		return false
	}
}

func writePong(ctx context.Context, conn *websocket.Conn, id string) error {
	writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return wsjson.Write(writeCtx, conn, protocol.Frame{Type: protocol.FramePong, ID: id})
}

func principalForHello(hello protocol.Frame, principal broker.DevicePrincipal) (broker.DevicePrincipal, error) {
	if hello.DeviceID != principal.DeviceID {
		return broker.DevicePrincipal{}, fmt.Errorf("hello device_id does not match credential")
	}
	if len(principal.LocalAccountIDs) > 0 {
		allowed := make(map[string]struct{}, len(principal.LocalAccountIDs))
		for _, accountID := range principal.LocalAccountIDs {
			allowed[accountID] = struct{}{}
		}
		for _, accountID := range hello.LocalAccounts {
			if _, ok := allowed[accountID]; !ok {
				return broker.DevicePrincipal{}, fmt.Errorf("hello local account does not match credential")
			}
		}
	}
	return broker.DevicePrincipal{
		UserID:          principal.UserID,
		DeviceID:        principal.DeviceID,
		LocalAccountIDs: append([]string(nil), hello.LocalAccounts...),
		Capabilities:    append([]protocol.Operation(nil), hello.Capabilities...),
	}, nil
}

type deviceConn struct {
	conn *websocket.Conn
}

func (c *deviceConn) Send(ctx context.Context, frame protocol.Frame) error {
	writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return wsjson.Write(writeCtx, c.conn, frame)
}

func (c *deviceConn) Close() error {
	return c.conn.Close(websocket.StatusNormalClosure, "device disconnected")
}

type StaticTokenAuthenticator struct {
	Token     string
	Principal broker.DevicePrincipal
}

func (a StaticTokenAuthenticator) Authenticate(r *http.Request) (broker.DevicePrincipal, error) {
	token := bearerToken(r)
	if token == "" || a.Token == "" || token != a.Token {
		return broker.DevicePrincipal{}, ErrUnauthorized
	}
	return a.Principal, nil
}

func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	if key := r.Header.Get("x-device-token"); key != "" {
		return key
	}
	return ""
}
