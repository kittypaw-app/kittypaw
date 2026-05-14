package server

import (
	"context"
	"sync"

	"github.com/jinto/kittypaw/engine"
)

// AccountSchedulers owns one scheduler per active account.
type AccountSchedulers struct {
	mu         sync.Mutex
	schedulers map[string]*engine.Scheduler
	started    map[string]bool
	ctx        context.Context
	running    bool
}

func NewAccountSchedulers() *AccountSchedulers {
	return &AccountSchedulers{
		schedulers: make(map[string]*engine.Scheduler),
		started:    make(map[string]bool),
	}
}

func (a *AccountSchedulers) Register(accountID string, scheduler *engine.Scheduler) {
	if a == nil || accountID == "" || scheduler == nil {
		return
	}

	var startCtx context.Context
	a.mu.Lock()
	a.schedulers[accountID] = scheduler
	if a.running && !a.started[accountID] {
		a.started[accountID] = true
		startCtx = a.ctx
	}
	a.mu.Unlock()

	if startCtx != nil {
		scheduler.StartAsync(startCtx)
	}
}

func (a *AccountSchedulers) Replace(accountID string, scheduler *engine.Scheduler) *engine.Scheduler {
	if a == nil || accountID == "" || scheduler == nil {
		return nil
	}

	var startCtx context.Context
	a.mu.Lock()
	old := a.schedulers[accountID]
	a.schedulers[accountID] = scheduler
	if a.running {
		a.started[accountID] = true
		startCtx = a.ctx
	} else {
		delete(a.started, accountID)
	}
	a.mu.Unlock()

	if old != nil {
		old.Stop()
	}
	if startCtx != nil {
		scheduler.StartAsync(startCtx)
	}
	return old
}

func (a *AccountSchedulers) Remove(accountID string) {
	scheduler := a.Detach(accountID)
	if scheduler != nil {
		scheduler.Stop()
		scheduler.Wait()
	}
}

func (a *AccountSchedulers) Detach(accountID string) *engine.Scheduler {
	if a == nil || accountID == "" {
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	scheduler := a.schedulers[accountID]
	delete(a.schedulers, accountID)
	delete(a.started, accountID)
	return scheduler
}

func (a *AccountSchedulers) StartAll(ctx context.Context) {
	if a == nil {
		return
	}

	var toStart []*engine.Scheduler
	a.mu.Lock()
	a.ctx = ctx
	a.running = true
	for accountID, scheduler := range a.schedulers {
		if scheduler == nil || a.started[accountID] {
			continue
		}
		a.started[accountID] = true
		toStart = append(toStart, scheduler)
	}
	a.mu.Unlock()

	for _, scheduler := range toStart {
		scheduler.StartAsync(ctx)
	}
}

func (a *AccountSchedulers) StopAll() {
	if a == nil {
		return
	}

	var toStop []*engine.Scheduler
	a.mu.Lock()
	a.running = false
	a.ctx = nil
	for _, scheduler := range a.schedulers {
		if scheduler != nil {
			toStop = append(toStop, scheduler)
		}
	}
	a.mu.Unlock()

	for _, scheduler := range toStop {
		scheduler.Stop()
	}
}

func (a *AccountSchedulers) WaitAll() {
	if a == nil {
		return
	}

	var toWait []*engine.Scheduler
	a.mu.Lock()
	for _, scheduler := range a.schedulers {
		if scheduler != nil {
			toWait = append(toWait, scheduler)
		}
	}
	a.mu.Unlock()

	for _, scheduler := range toWait {
		scheduler.Wait()
	}
}

func (a *AccountSchedulers) Has(accountID string) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.schedulers[accountID]
	return ok
}

func (a *AccountSchedulers) Len() int {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.schedulers)
}

func (a *AccountSchedulers) Snapshot() map[string]engine.SchedulerSnapshot {
	if a == nil {
		return map[string]engine.SchedulerSnapshot{}
	}
	a.mu.Lock()
	schedulers := make(map[string]*engine.Scheduler, len(a.schedulers))
	for accountID, scheduler := range a.schedulers {
		schedulers[accountID] = scheduler
	}
	a.mu.Unlock()

	out := make(map[string]engine.SchedulerSnapshot, len(schedulers))
	for accountID, scheduler := range schedulers {
		out[accountID] = scheduler.Snapshot()
	}
	return out
}
