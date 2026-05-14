package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jinto/kittypaw/channel"
	"github.com/jinto/kittypaw/core"
)

// stopTimeout is the maximum time to wait for a channel goroutine to exit
// after its context is canceled. This defends against buggy channel
// implementations that ignore context cancellation.
const stopTimeout = 10 * time.Second

// ErrChannelNotFound is returned when Stop is called for a channel that
// is not currently running.
var ErrChannelNotFound = errors.New("channel not found")

// spawnerKey composites account ID and channel type into the lookup key.
// Each account hosts at most one channel per type.
type spawnerKey struct {
	AccountID   string
	ChannelType string
}

// runningChannel tracks a single active channel and the machinery needed
// to stop it cleanly. The owning account is encoded in the spawnerKey used
// to look up this struct, so it is not repeated here.
type runningChannel struct {
	cancel func()             // cancels the context passed to Start
	ch     channel.Channel    // the live channel instance
	done   chan struct{}      // closed when the Start goroutine exits
	config core.ChannelConfig // config snapshot for Reconcile diff
}

// ChannelStatus is the API-facing representation of a running channel.
type ChannelStatus struct {
	AccountID string `json:"account_id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Running   bool   `json:"running"`
}

// ChannelSpawner manages the lifecycle of messaging channels.
// It is safe for concurrent use.
type ChannelSpawner struct {
	mu          sync.RWMutex
	reconcileMu sync.Mutex // serializes Reconcile calls
	running     map[spawnerKey]*runningChannel
	eventCh     chan<- core.Event
	eventSink   channel.EventSink
	baseCtx     context.Context // long-lived context for channel goroutines
}

// NewChannelSpawner creates a spawner that will pass eventCh to every
// channel it starts. baseCtx should be a long-lived context (e.g., from
// signal.NotifyContext) — all channel goroutines derive their contexts
// from it, regardless of the caller's context.
func NewChannelSpawner(baseCtx context.Context, eventCh chan<- core.Event, eventSink ...channel.EventSink) *ChannelSpawner {
	var sink channel.EventSink
	if len(eventSink) > 0 {
		sink = eventSink[0]
	}
	return &ChannelSpawner{
		running:   make(map[spawnerKey]*runningChannel),
		eventCh:   eventCh,
		eventSink: sink,
		baseCtx:   baseCtx,
	}
}

// TrySpawn starts a channel for accountID if one with the same
// (account, type) is not already running. Idempotent — returns nil if
// already running. The channel goroutine's context is derived from the
// spawner's baseCtx, not the caller's context, so HTTP request contexts
// won't kill long-lived channels.
func (s *ChannelSpawner) TrySpawn(accountID string, ch channel.Channel, cfg core.ChannelConfig) error {
	key := spawnerKey{AccountID: accountID, ChannelType: ch.Name()}

	s.mu.Lock()
	if _, exists := s.running[key]; exists {
		s.mu.Unlock()
		return nil // idempotent
	}

	chCtx, cancel := context.WithCancel(s.baseCtx)
	done := make(chan struct{})
	rc := &runningChannel{
		cancel: cancel,
		ch:     ch,
		done:   done,
		config: cfg,
	}
	s.running[key] = rc
	s.mu.Unlock()

	slog.Info("channel spawned",
		"account", accountID, "name", key.ChannelType)
	go func() {
		defer func() {
			s.mu.Lock()
			if s.running[key] == rc {
				delete(s.running, key)
			}
			s.mu.Unlock()
			close(done)
		}()
		var err error
		if starter, ok := ch.(channel.EventSinkStarter); ok && s.eventSink != nil {
			err = starter.StartWithEventSink(chCtx, s.eventSink)
		} else {
			err = ch.Start(chCtx, s.eventCh)
		}
		if err != nil && chCtx.Err() == nil {
			slog.Error("channel stopped unexpectedly",
				"account", accountID, "name", key.ChannelType, "error", err)
		}
	}()

	return nil
}

// Stop cancels a running channel for (accountID, channelType) and waits
// for its goroutine to exit.
//
// Lock discipline: the write lock is released BEFORE blocking on <-done.
// This prevents deadlocking concurrent GetChannel/List callers.
func (s *ChannelSpawner) Stop(accountID, channelType string) error {
	key := spawnerKey{AccountID: accountID, ChannelType: channelType}
	s.mu.Lock()
	rc, ok := s.running[key]
	if !ok {
		s.mu.Unlock()
		return ErrChannelNotFound
	}
	delete(s.running, key)
	s.mu.Unlock()

	rc.cancel()
	select {
	case <-rc.done:
		slog.Info("channel stopped",
			"account", accountID, "name", channelType)
	case <-time.After(stopTimeout):
		slog.Error("channel stop: timed out waiting for goroutine",
			"account", accountID, "name", channelType)
	}
	return nil
}

// GetChannel returns the Channel for (accountID, eventType), or nil and
// false if not running. Accounts are isolated: a channel registered under
// account A cannot be reached by passing account B's ID.
func (s *ChannelSpawner) GetChannel(accountID string, eventType core.EventType) (channel.Channel, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rc, ok := s.running[spawnerKey{AccountID: accountID, ChannelType: string(eventType)}]
	if !ok {
		return nil, false
	}
	return rc.ch, true
}

type lastChatIDReporter interface {
	LastChatID() (string, bool)
}

// TelegramLastChatID returns the latest chat_id observed by the live Telegram
// channel for accountID/token. The third return value indicates that a matching
// channel is active even if it has not observed a chat yet.
func (s *ChannelSpawner) TelegramLastChatID(accountID, token string) (string, bool, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rc, ok := s.running[spawnerKey{AccountID: accountID, ChannelType: string(core.EventTelegram)}]
	if !ok || rc.config.Token != token {
		return "", false, false
	}
	reporter, ok := rc.ch.(lastChatIDReporter)
	if !ok {
		return "", false, true
	}
	chatID, found := reporter.LastChatID()
	return chatID, found, true
}

// TelegramLastChatIDByToken returns the latest chat_id observed by any live
// Telegram channel using token. It is used by localhost setup flows before the
// target account exists on disk, where account-scoped API-key discovery cannot
// yet succeed but the running server may already be consuming that bot token.
func (s *ChannelSpawner) TelegramLastChatIDByToken(token string) (accountID, chatID string, found, active bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for key, rc := range s.running {
		if key.ChannelType != string(core.EventTelegram) || rc.config.Token != token {
			continue
		}
		reporter, ok := rc.ch.(lastChatIDReporter)
		if !ok {
			return key.AccountID, "", false, true
		}
		chatID, found := reporter.LastChatID()
		return key.AccountID, chatID, found, true
	}
	return "", "", false, false
}

// ReplaceSpawn stops the existing channel for (accountID, ch.Name()) if
// any and spawns a new one. Used when a channel's config has changed.
func (s *ChannelSpawner) ReplaceSpawn(accountID string, ch channel.Channel, cfg core.ChannelConfig) error {
	name := ch.Name()
	if err := s.Stop(accountID, name); err != nil && !errors.Is(err, ErrChannelNotFound) {
		return fmt.Errorf("stop %s/%s: %w", accountID, name, err)
	}
	return s.TrySpawn(accountID, ch, cfg)
}

// configEqual compares two ChannelConfig values for diff purposes.
func configEqual(a, b core.ChannelConfig) bool {
	return a.ChannelType == b.ChannelType &&
		a.Token == b.Token &&
		a.BindAddr == b.BindAddr &&
		a.KakaoWSURL == b.KakaoWSURL
}

// Reconcile compares the currently running channels for accountID against
// the desired configs and starts, stops, or replaces channels to match.
// Channels owned by other accounts are untouched.
//
// Serialized via reconcileMu to prevent concurrent calls from corrupting
// state. Best-effort: individual failures are aggregated, not fatal.
// WebSocket channels are excluded (port-binding makes hot-reload unsafe).
func (s *ChannelSpawner) Reconcile(accountID string, configs []core.ChannelConfig) error {
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()

	// Inject Kakao relay WS URL from the account's per-account secrets into channel configs.
	core.InjectKakaoWSURL(accountID, configs)

	// Build desired map keyed by channel type, filtering out WebSocket
	// channels.
	desired := make(map[string]core.ChannelConfig)
	for _, cfg := range configs {
		if cfg.ChannelType == core.ChannelWeb {
			continue
		}
		name := string(cfg.ChannelType.ToEventType())
		desired[name] = cfg
	}

	var errs []error

	// Phase 1: Stop removed channels (only within this account).
	s.mu.RLock()
	var toRemove []spawnerKey
	for key := range s.running {
		if key.AccountID != accountID {
			continue
		}
		if _, ok := desired[key.ChannelType]; !ok {
			toRemove = append(toRemove, key)
		}
	}
	s.mu.RUnlock()

	for _, key := range toRemove {
		if err := s.Stop(accountID, key.ChannelType); err != nil {
			slog.Warn("reconcile: stop removed channel failed",
				"account", accountID, "name", key.ChannelType, "error", err)
		}
	}

	// Phase 2: Replace changed channels (only within this account).
	s.mu.RLock()
	var toReplace []core.ChannelConfig
	for key, rc := range s.running {
		if key.AccountID != accountID {
			continue
		}
		if dcfg, ok := desired[key.ChannelType]; ok && !configEqual(rc.config, dcfg) {
			toReplace = append(toReplace, dcfg)
		}
	}
	s.mu.RUnlock()

	for _, cfg := range toReplace {
		ch, err := channel.FromConfig(accountID, cfg)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", cfg.ChannelType, err))
			continue
		}
		if err := s.ReplaceSpawn(accountID, ch, cfg); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", cfg.ChannelType, err))
		}
	}

	// Phase 3: Spawn new channels (only within this account).
	s.mu.RLock()
	var toAdd []core.ChannelConfig
	for name, cfg := range desired {
		if _, ok := s.running[spawnerKey{AccountID: accountID, ChannelType: name}]; !ok {
			toAdd = append(toAdd, cfg)
		}
	}
	s.mu.RUnlock()

	for _, cfg := range toAdd {
		ch, err := channel.FromConfig(accountID, cfg)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", cfg.ChannelType, err))
			continue
		}
		if err := s.TrySpawn(accountID, ch, cfg); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", cfg.ChannelType, err))
		}
	}

	return errors.Join(errs...)
}

// StopAll cancels all running channels across all accounts in parallel and
// waits for them to exit with a single shared deadline. Used during
// graceful shutdown.
func (s *ChannelSpawner) StopAll() {
	s.mu.Lock()
	snapshot := s.running
	s.running = make(map[spawnerKey]*runningChannel)
	s.mu.Unlock()

	if len(snapshot) == 0 {
		return
	}

	// Cancel all contexts first (immediate signal to all goroutines).
	for _, rc := range snapshot {
		rc.cancel()
	}

	// Wait for all goroutines to exit with a single shared deadline.
	deadline := time.After(stopTimeout)
	for key, rc := range snapshot {
		select {
		case <-rc.done:
			slog.Info("channel stopped",
				"account", key.AccountID, "name", key.ChannelType)
		case <-deadline:
			slog.Error("channel stopAll: shared deadline exceeded",
				"account", key.AccountID, "name", key.ChannelType)
			return // remaining channels will be abandoned
		}
	}
}

// List returns the status of every running channel across all accounts.
func (s *ChannelSpawner) List() []ChannelStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	statuses := make([]ChannelStatus, 0, len(s.running))
	for key, rc := range s.running {
		statuses = append(statuses, ChannelStatus{
			AccountID: key.AccountID,
			Name:      key.ChannelType,
			Type:      string(rc.config.ChannelType),
			Running:   true,
		})
	}
	return statuses
}
