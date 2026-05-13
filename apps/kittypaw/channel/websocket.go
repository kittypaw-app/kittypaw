package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"github.com/jinto/kittypaw/core"
)

// wsClient tracks a single WebSocket connection and its session.
type wsClient struct {
	conn      *websocket.Conn
	sessionID string
}

// WebSocketChannel implements Channel as a legacy/test channel.
//
// Deprecated: use the server built-in /ws or /chat/ws endpoints instead.
// This legacy/test channel is excluded from ChannelSpawner.Reconcile and does not implement the product WebSocket auth, heartbeat, turn_id, conversation_id, or permission flow.
type WebSocketChannel struct {
	accountID string
	bindAddr  string
	clients   map[string]*wsClient
	mu        sync.RWMutex
}

// NewWebSocket creates a WebSocketChannel listening on the given address.
// accountID is stamped on every emitted Event for AccountRouter dispatch.
//
// Deprecated: use the server built-in /ws or /chat/ws endpoints instead.
func NewWebSocket(accountID, bindAddr string) *WebSocketChannel {
	return &WebSocketChannel{
		accountID: accountID,
		bindAddr:  bindAddr,
		clients:   make(map[string]*wsClient),
	}
}

func (w *WebSocketChannel) Name() string { return "web" }

// Start launches an HTTP server that accepts WebSocket connections.
// Each connection runs a read loop in its own goroutine.
func (w *WebSocketChannel) Start(ctx context.Context, eventCh chan<- core.Event) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(rw http.ResponseWriter, r *http.Request) {
		w.handleUpgrade(ctx, rw, r, eventCh)
	})

	srv := &http.Server{
		Addr:    w.bindAddr,
		Handler: mux,
	}

	// Graceful shutdown on context cancellation.
	go func() {
		<-ctx.Done()
		slog.Info("websocket: shutting down HTTP server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	slog.Info("websocket: listening", "addr", w.bindAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("websocket server: %w", err)
	}
	return nil
}

// SendResponse sends a text response to the WebSocket client matching chatID.
// Falls back to broadcast only if no matching session is found.
// replyToMessageID is unused on WebSocket — clients handle context themselves.
func (w *WebSocketChannel) SendResponse(ctx context.Context, chatID, response, _ string) error {
	msg := core.NewDoneMsg(response, nil)
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return w.writeToClientOrBroadcast(ctx, chatID, data)
}

// SendRichResponse sends a structured response to WebSocket clients.
func (w *WebSocketChannel) SendRichResponse(ctx context.Context, chatID string, response core.OutboundResponse, _ string) error {
	msg := core.NewDoneMsgFromOutbound(response, nil)
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return w.writeToClientOrBroadcast(ctx, chatID, data)
}

func (w *WebSocketChannel) writeToClientOrBroadcast(ctx context.Context, chatID string, data []byte) error {
	w.mu.RLock()
	defer w.mu.RUnlock()

	// Route to the specific session if we have a matching client.
	if client, ok := w.clients[chatID]; ok {
		return client.conn.Write(ctx, websocket.MessageText, data)
	}

	// No exact match — broadcast as fallback.
	var lastErr error
	for id, client := range w.clients {
		if err := client.conn.Write(ctx, websocket.MessageText, data); err != nil {
			slog.Warn("websocket: write to client failed", "session", id, "error", err)
			lastErr = err
		}
	}
	return lastErr
}

// --- internal ---

func (w *WebSocketChannel) handleUpgrade(ctx context.Context, rw http.ResponseWriter, r *http.Request, eventCh chan<- core.Event) {
	conn, err := websocket.Accept(rw, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // Allow any origin for dev.
	})
	if err != nil {
		slog.Warn("websocket: accept failed", "error", err)
		return
	}

	sessionID := generateSessionID()
	client := &wsClient{conn: conn, sessionID: sessionID}

	w.mu.Lock()
	w.clients[sessionID] = client
	w.mu.Unlock()

	slog.Info("websocket: new connection", "session", sessionID)

	// Send session message to the client.
	sessionMsg := core.NewSessionMsg(sessionID)
	data, _ := json.Marshal(sessionMsg)
	_ = conn.Write(ctx, websocket.MessageText, data)

	// Read loop (blocks until connection closes).
	w.readLoop(ctx, client, eventCh)

	// Cleanup.
	w.mu.Lock()
	delete(w.clients, sessionID)
	w.mu.Unlock()

	conn.CloseNow()
	slog.Info("websocket: connection closed", "session", sessionID)
}

func (w *WebSocketChannel) readLoop(ctx context.Context, client *wsClient, eventCh chan<- core.Event) {
	for {
		_, data, err := client.conn.Read(ctx)
		if err != nil {
			if ctx.Err() == nil {
				slog.Debug("websocket: read error", "session", client.sessionID, "error", err)
			}
			return
		}

		var msg core.WsClientMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			slog.Warn("websocket: unmarshal client msg", "error", err)
			continue
		}

		switch msg.Type {
		case core.WsMsgChat:
			if msg.Text == "" {
				continue
			}

			payload := core.ChatPayload{
				ChatID:          client.sessionID,
				Text:            msg.Text,
				SourceSessionID: client.sessionID,
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				slog.Error("websocket: marshal payload", "error", err)
				continue
			}

			event := core.Event{
				Type:      core.EventWebChat,
				AccountID: w.accountID,
				Payload:   raw,
			}

			select {
			case eventCh <- event:
			case <-ctx.Done():
				return
			}

		case core.WsMsgPermit:
			// TODO: forward permission responses to the engine.

		default:
			slog.Debug("websocket: unknown client message type", "type", msg.Type)
		}
	}
}

// generateSessionID produces a short unique session identifier.
// Uses timestamp + a simple counter for uniqueness without external deps.
var (
	sessionCounter uint64
	sessionMu      sync.Mutex
)

func generateSessionID() string {
	sessionMu.Lock()
	sessionCounter++
	n := sessionCounter
	sessionMu.Unlock()
	return fmt.Sprintf("ws-%d-%d", time.Now().UnixMilli(), n)
}
