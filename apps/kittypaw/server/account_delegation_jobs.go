package server

import (
	"context"
	"sync"

	"github.com/jinto/kittypaw/engine"
)

type AccountDelegationJobRuntimes struct {
	mu       sync.Mutex
	runtimes map[string]*engine.DelegationJobRuntime
	started  map[string]bool
	ctx      context.Context
	running  bool
}

func NewAccountDelegationJobRuntimes() *AccountDelegationJobRuntimes {
	return &AccountDelegationJobRuntimes{
		runtimes: make(map[string]*engine.DelegationJobRuntime),
		started:  make(map[string]bool),
	}
}

func (a *AccountDelegationJobRuntimes) Register(accountID string, runtime *engine.DelegationJobRuntime) {
	if a == nil || accountID == "" || runtime == nil {
		return
	}
	var startCtx context.Context
	a.mu.Lock()
	a.runtimes[accountID] = runtime
	if a.running && !a.started[accountID] {
		a.started[accountID] = true
		startCtx = a.ctx
	}
	a.mu.Unlock()
	if startCtx != nil {
		runtime.StartAsync(startCtx)
	}
}

func (a *AccountDelegationJobRuntimes) Replace(accountID string, runtime *engine.DelegationJobRuntime) *engine.DelegationJobRuntime {
	if a == nil || accountID == "" || runtime == nil {
		return nil
	}
	var startCtx context.Context
	a.mu.Lock()
	old := a.runtimes[accountID]
	a.runtimes[accountID] = runtime
	if a.running {
		a.started[accountID] = true
		startCtx = a.ctx
	} else {
		delete(a.started, accountID)
	}
	a.mu.Unlock()
	if old != nil {
		go old.Close()
	}
	if startCtx != nil {
		runtime.StartAsync(startCtx)
	}
	return old
}

func (a *AccountDelegationJobRuntimes) Detach(accountID string) *engine.DelegationJobRuntime {
	if a == nil || accountID == "" {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	runtime := a.runtimes[accountID]
	delete(a.runtimes, accountID)
	delete(a.started, accountID)
	return runtime
}

func (a *AccountDelegationJobRuntimes) StartAll(ctx context.Context) {
	if a == nil {
		return
	}
	var toStart []*engine.DelegationJobRuntime
	a.mu.Lock()
	a.ctx = ctx
	a.running = true
	for accountID, runtime := range a.runtimes {
		if runtime == nil || a.started[accountID] {
			continue
		}
		a.started[accountID] = true
		toStart = append(toStart, runtime)
	}
	a.mu.Unlock()
	for _, runtime := range toStart {
		runtime.StartAsync(ctx)
	}
}

func (a *AccountDelegationJobRuntimes) StopAll() {
	if a == nil {
		return
	}
	var toStop []*engine.DelegationJobRuntime
	a.mu.Lock()
	a.running = false
	a.ctx = nil
	for _, runtime := range a.runtimes {
		if runtime != nil {
			toStop = append(toStop, runtime)
		}
	}
	a.mu.Unlock()
	for _, runtime := range toStop {
		runtime.Close()
	}
}
