package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/store"
)

const (
	inboundDrainBatch       = 16
	inboundClaimLease       = 10 * time.Minute
	inboundDrainIdle        = 500 * time.Millisecond
	inboundEventWakeBufSize = 1
)

func (s *Server) PublishEvent(ctx context.Context, event core.Event) error {
	_, _, err := s.publishInboundEvent(ctx, event)
	return err
}

func (s *Server) publishInboundEvent(ctx context.Context, event core.Event) (int64, bool, error) {
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	st := s.storeForInboundEvent(event)
	if st == nil {
		return 0, false, fmt.Errorf("inbound event store unavailable for account %q", event.AccountID)
	}
	id, inserted, err := st.EnqueueInboundEvent(event)
	if err != nil {
		return 0, false, err
	}
	if inserted {
		s.signalInboundDrain()
	}
	return id, inserted, nil
}

func (s *Server) storeForInboundEvent(event core.Event) *store.Store {
	accountID := strings.TrimSpace(event.AccountID)
	if accountID == "" {
		accountID = s.defaultAccountID()
	}
	if td := s.accountDepsForID(accountID); td != nil {
		return td.Store
	}
	if accountID == s.defaultAccountID() {
		return s.store
	}
	return nil
}

func (s *Server) signalInboundDrain() {
	if s == nil || s.inboundWake == nil {
		return
	}
	select {
	case s.inboundWake <- struct{}{}:
	default:
	}
}

func (s *Server) drainInboundEvents(ctx context.Context) {
	s.signalInboundDrain()
	ticker := time.NewTicker(inboundDrainIdle)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.inboundWake:
			if err := s.drainInboundEventsOnce(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("inbound event drain failed", "error", err)
			}
		case <-ticker.C:
			if err := s.drainInboundEventsOnce(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("inbound event drain failed", "error", err)
			}
		}
	}
}

func (s *Server) drainInboundEventsOnce(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.inboundDrainMu.Lock()
	defer s.inboundDrainMu.Unlock()

	for _, deps := range s.activeAccountDeps() {
		if deps == nil || deps.Store == nil {
			continue
		}
		records, err := deps.Store.ClaimInboundEvents(inboundDrainBatch, inboundClaimLease)
		if err != nil {
			return err
		}
		for _, rec := range records {
			event := rec.Event
			event.DurableInboundID = rec.ID
			if err := s.dispatchDurableInboundEvent(ctx, deps.Store, event); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Server) dispatchDurableInboundEvent(ctx context.Context, st *store.Store, event core.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case s.eventCh <- event:
		return nil
	case <-ctx.Done():
		if event.DurableInboundID > 0 {
			_ = st.ReleaseInboundEvent(event.DurableInboundID, ctx.Err().Error())
		}
		return ctx.Err()
	}
}

func (s *Server) startDurableInboundLeaseKeeper(ctx context.Context, accountID string, id int64, done <-chan struct{}) {
	if id <= 0 || done == nil {
		return
	}
	interval := inboundClaimLease / 3
	if interval <= 0 {
		interval = time.Minute
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				s.refreshDurableInboundEventLease(accountID, id)
			}
		}
	}()
}

func (s *Server) refreshDurableInboundEventLease(accountID string, id int64) {
	if id <= 0 {
		return
	}
	if st := s.storeForInboundEvent(core.Event{AccountID: accountID}); st != nil {
		if err := st.RefreshInboundEventLease(id, inboundClaimLease); err != nil {
			slog.Warn("refresh durable inbound event lease failed", "account", accountID, "id", id, "error", err)
		}
	}
}

func (s *Server) completeDurableInboundEvent(accountID string, id int64) {
	if id <= 0 {
		return
	}
	if st := s.storeForInboundEvent(core.Event{AccountID: accountID}); st != nil {
		if err := st.CompleteInboundEvent(id); err != nil {
			slog.Warn("complete durable inbound event failed", "account", accountID, "id", id, "error", err)
		}
	}
}

func (s *Server) releaseDurableInboundEvent(accountID string, id int64, errText string) {
	if id <= 0 {
		return
	}
	if st := s.storeForInboundEvent(core.Event{AccountID: accountID}); st != nil {
		if err := st.ReleaseInboundEvent(id, errText); err != nil {
			slog.Warn("release durable inbound event failed", "account", accountID, "id", id, "error", err)
		}
	}
}

func (s *Server) failDurableInboundEvent(accountID string, id int64, errText string) {
	if id <= 0 {
		return
	}
	if st := s.storeForInboundEvent(core.Event{AccountID: accountID}); st != nil {
		if err := st.FailInboundEvent(id, errText); err != nil {
			slog.Warn("fail durable inbound event failed", "account", accountID, "id", id, "error", err)
		}
	}
}
