package engine

import (
	"context"
	"errors"
	"sync"

	"github.com/jinto/kittypaw/core"
)

var ErrRuntimeAdmissionBusy = errors.New("runtime admission busy")

type AdmissionClass string

const (
	AdmissionForeground AdmissionClass = "foreground"
	AdmissionBackground AdmissionClass = "background"
	AdmissionRetry      AdmissionClass = "retry"
)

type RuntimeAdmissionConfig struct {
	MaxConcurrentAccount uint32
	MaxQueuedAccount     uint32
	MaxConcurrentScope   uint32
}

type RuntimeAdmissionRequest struct {
	AccountID string
	ScopeKey  string
	Class     AdmissionClass
}

type RuntimeAdmissionSnapshot struct {
	AccountRunning uint32 `json:"account_running"`
	AccountQueued  uint32 `json:"account_queued"`
	ScopeRunning   uint32 `json:"scope_running"`
	ScopeQueued    uint32 `json:"scope_queued"`
}

type RuntimeAdmission struct {
	account *admissionGate

	mu     sync.Mutex
	scopes map[string]*admissionGate
	cfg    RuntimeAdmissionConfig
}

type RuntimeAdmissionLease struct {
	once    sync.Once
	release func()
}

func NewRuntimeAdmission(cfg RuntimeAdmissionConfig) *RuntimeAdmission {
	return &RuntimeAdmission{
		account: newAdmissionGate(cfg.MaxConcurrentAccount, cfg.MaxQueuedAccount),
		scopes:  make(map[string]*admissionGate),
		cfg:     cfg,
	}
}

func RuntimeAdmissionConfigFromCore(cfg *core.Config) RuntimeAdmissionConfig {
	if cfg == nil {
		return RuntimeAdmissionConfig{}
	}
	return RuntimeAdmissionConfig{
		MaxConcurrentAccount: cfg.Runtime.MaxConcurrentTurnsPerAccount,
		MaxQueuedAccount:     cfg.Runtime.MaxQueuedTurnsPerAccount,
		MaxConcurrentScope:   cfg.Runtime.MaxConcurrentTurnsPerConversation,
	}
}

func (a *RuntimeAdmission) Acquire(ctx context.Context, req RuntimeAdmissionRequest) (*RuntimeAdmissionLease, error) {
	if a == nil {
		return nil, nil
	}
	scope := a.scopeGate(req.ScopeKey)

	accountLease, accountOK := a.account.tryAcquire()
	if accountOK {
		scopeLease, scopeOK := scope.tryAcquire()
		if scopeOK {
			return &RuntimeAdmissionLease{release: func() {
				accountLease.Release()
				scopeLease.Release()
			}}, nil
		}
		accountLease.Release()
	}

	if !a.account.reserveQueueSlot() {
		return nil, ErrRuntimeAdmissionBusy
	}
	defer a.account.releaseQueueSlot()

	scopeLease, err := scope.wait(ctx)
	if err != nil {
		scopeLease.Release()
		return nil, err
	}

	accountLease, err = a.account.waitReserved(ctx)
	if err != nil {
		scopeLease.Release()
		return nil, err
	}

	return &RuntimeAdmissionLease{release: func() {
		accountLease.Release()
		scopeLease.Release()
	}}, nil
}

func (a *RuntimeAdmission) Snapshot() RuntimeAdmissionSnapshot {
	if a == nil {
		return RuntimeAdmissionSnapshot{}
	}
	snap := a.account.snapshot()

	a.mu.Lock()
	defer a.mu.Unlock()
	for _, scope := range a.scopes {
		scopeSnap := scope.snapshot()
		snap.ScopeRunning += scopeSnap.AccountRunning
		snap.ScopeQueued += scopeSnap.AccountQueued
	}
	return snap
}

func (l *RuntimeAdmissionLease) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		if l.release != nil {
			l.release()
		}
	})
}

func (a *RuntimeAdmission) scopeGate(scopeKey string) *admissionGate {
	if a == nil || a.cfg.MaxConcurrentScope == 0 || scopeKey == "" {
		return unlimitedAdmissionGate()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if gate := a.scopes[scopeKey]; gate != nil {
		return gate
	}
	gate := newAdmissionGate(a.cfg.MaxConcurrentScope, a.cfg.MaxQueuedAccount)
	a.scopes[scopeKey] = gate
	return gate
}

type admissionGate struct {
	slots chan struct{}

	mu     sync.Mutex
	queued uint32
	maxQ   uint32
}

func newAdmissionGate(maxConcurrent, maxQueued uint32) *admissionGate {
	if maxConcurrent == 0 {
		return unlimitedAdmissionGate()
	}
	return &admissionGate{
		slots: make(chan struct{}, maxConcurrent),
		maxQ:  maxQueued,
	}
}

func unlimitedAdmissionGate() *admissionGate {
	return &admissionGate{}
}

func (g *admissionGate) acquire(ctx context.Context) (*RuntimeAdmissionLease, error) {
	if g == nil || g.slots == nil {
		return &RuntimeAdmissionLease{}, nil
	}
	if lease, ok := g.tryAcquire(); ok {
		return lease, nil
	}
	if !g.reserveQueueSlot() {
		return nil, ErrRuntimeAdmissionBusy
	}
	defer g.releaseQueueSlot()
	return g.waitReserved(ctx)
}

func (g *admissionGate) tryAcquire() (*RuntimeAdmissionLease, bool) {
	if g == nil || g.slots == nil {
		return &RuntimeAdmissionLease{}, true
	}
	select {
	case g.slots <- struct{}{}:
		return &RuntimeAdmissionLease{release: func() { <-g.slots }}, true
	default:
		return nil, false
	}
}

func (g *admissionGate) wait(ctx context.Context) (*RuntimeAdmissionLease, error) {
	if g == nil || g.slots == nil {
		return &RuntimeAdmissionLease{}, nil
	}
	g.mu.Lock()
	g.queued++
	g.mu.Unlock()
	defer g.releaseQueueSlot()
	return g.waitReserved(ctx)
}

func (g *admissionGate) waitReserved(ctx context.Context) (*RuntimeAdmissionLease, error) {
	if g == nil || g.slots == nil {
		return &RuntimeAdmissionLease{}, nil
	}
	select {
	case g.slots <- struct{}{}:
		return &RuntimeAdmissionLease{release: func() { <-g.slots }}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (g *admissionGate) reserveQueueSlot() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.queued >= g.maxQ {
		return false
	}
	g.queued++
	return true
}

func (g *admissionGate) releaseQueueSlot() {
	g.mu.Lock()
	if g.queued > 0 {
		g.queued--
	}
	g.mu.Unlock()
}

func (g *admissionGate) snapshot() RuntimeAdmissionSnapshot {
	if g == nil || g.slots == nil {
		return RuntimeAdmissionSnapshot{}
	}
	g.mu.Lock()
	queued := g.queued
	g.mu.Unlock()
	return RuntimeAdmissionSnapshot{
		AccountRunning: uint32(len(g.slots)),
		AccountQueued:  queued,
	}
}
