package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jinto/kittypaw/channel"
	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/engine"
)

const (
	channelDispatchQueueSize      = 32
	defaultChannelTurnTimeout     = 5 * time.Minute
	channelRunFailureResponse     = "처리 중 오류가 발생했습니다. 잠시 후 다시 시도해주세요."
	channelQueueOverflowResponse  = "요청이 밀려 있습니다. 잠시 후 다시 시도해주세요."
	channelDispatchWorkerScopeSep = "\x00"
)

var channelWorkerStopTimeout = 10 * time.Second

type channelEventJob struct {
	event       core.Event
	payload     core.ChatPayload
	session     *engine.Session
	baseRunOpts *engine.RunOptions
	ch          channel.Channel
	chOK        bool
}

type channelEventWorker struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	jobs   chan channelEventJob
}

func (s *Server) enqueueChannelEvent(ctx context.Context, job channelEventJob) {
	worker := s.channelWorker(ctx, channelWorkerKey(job.event))
	select {
	case worker.jobs <- job:
	case <-ctx.Done():
	default:
		slog.Warn("channel event: worker queue full",
			"type", job.event.Type,
			"account", job.event.AccountID,
			"chat_id", job.payload.ChatID,
		)
		s.sendOrQueueChannelFailure(ctx, job, channelQueueOverflowResponse)
	}
}

func (s *Server) channelWorker(ctx context.Context, key string) *channelEventWorker {
	s.channelWorkersMu.Lock()
	defer s.channelWorkersMu.Unlock()
	if s.channelWorkers == nil {
		s.channelWorkers = make(map[string]*channelEventWorker)
	}
	if worker := s.channelWorkers[key]; worker != nil {
		return worker
	}
	workerCtx, cancel := context.WithCancel(ctx)
	worker := &channelEventWorker{
		ctx:    workerCtx,
		cancel: cancel,
		done:   make(chan struct{}),
		jobs:   make(chan channelEventJob, channelDispatchQueueSize),
	}
	s.channelWorkers[key] = worker
	go s.runChannelWorker(key, worker)
	return worker
}

func (s *Server) runChannelWorker(key string, worker *channelEventWorker) {
	defer func() {
		s.channelWorkersMu.Lock()
		if s.channelWorkers[key] == worker {
			delete(s.channelWorkers, key)
		}
		s.channelWorkersMu.Unlock()
		close(worker.done)
	}()
	for {
		select {
		case <-worker.ctx.Done():
			return
		case job := <-worker.jobs:
			if worker.ctx.Err() != nil {
				return
			}
			s.processChannelEvent(worker.ctx, key, job)
		}
	}
}

func (s *Server) processChannelEvent(ctx context.Context, key string, job channelEventJob) {
	runCtx := ctx
	var cancel context.CancelFunc
	if timeout := s.channelTurnTimeoutDuration(); timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Fold chat-path /model overrides after dequeue, not at enqueue time.
	// A queued /model command must affect later jobs in this same worker.
	runOpts := job.session.ApplyActiveModel(job.baseRunOpts)
	response, runErr, panicked := s.runChannelSession(runCtx, job.session, job.event, runOpts)
	if ctx.Err() != nil {
		return
	}
	if panicked {
		s.sendOrQueueChannelFailure(ctx, job, channelRunFailureResponse)
		return
	}
	if runErr != nil {
		slog.Error("channel event: engine error",
			"worker", key,
			"type", job.event.Type,
			"account", job.event.AccountID,
			"chat_id", job.payload.ChatID,
			"error", runErr,
		)
		s.sendOrQueueChannelFailure(ctx, job, channelRunFailureResponse)
		return
	}

	engine.MarkAccountReady(job.session)

	if strings.TrimSpace(response) == "" {
		return
	}

	if !job.chOK {
		slog.Warn("channel event: no channel for response routing, enqueuing for retry",
			"type", job.event.Type, "account", job.event.AccountID)
		if job.event.Type != core.EventKakaoTalk {
			_ = s.store.EnqueueResponse(job.event.AccountID, string(job.event.Type), job.payload.ChatID, response)
		}
		return
	}

	outbound := core.ParseOutboundResponse(response)
	var pendingID int64
	var pendingQueued bool
	if job.event.Type != core.EventKakaoTalk {
		id, qErr := s.store.EnqueueResponseWithID(job.event.AccountID, string(job.event.Type), job.payload.ChatID, outbound.Text)
		if qErr != nil {
			slog.Error("channel event: enqueue response before send failed", "error", qErr)
		} else {
			pendingID = id
			pendingQueued = true
		}
	}
	if err := sendChannelResponse(ctx, job.ch, job.payload.ChatID, outbound, job.payload.ReplyToMessageID); err != nil {
		slog.Error("channel event: send response failed",
			"type", job.event.Type,
			"account", job.event.AccountID,
			"chat_id", job.payload.ChatID,
			"error", err,
		)
		if job.event.Type != core.EventKakaoTalk && !pendingQueued {
			if qErr := s.store.EnqueueResponse(job.event.AccountID, string(job.event.Type), job.payload.ChatID, outbound.Text); qErr != nil {
				slog.Error("channel event: enqueue response failed", "error", qErr)
			}
		}
		return
	}
	if pendingQueued {
		if err := s.store.MarkResponseDelivered(pendingID); err != nil {
			slog.Error("channel event: clear delivered outbox response failed",
				"id", pendingID,
				"account", job.event.AccountID,
				"chat_id", job.payload.ChatID,
				"error", err,
			)
		}
	}
}

func (s *Server) runChannelSession(ctx context.Context, session *engine.Session, event core.Event, runOpts *engine.RunOptions) (response string, runErr error, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			engine.RecoverAccountPanic(session, "server.channelWorker", r)
		}
	}()
	response, runErr = session.Run(ctx, event, runOpts)
	return response, runErr, false
}

func (s *Server) sendOrQueueChannelFailure(ctx context.Context, job channelEventJob, message string) {
	if job.chOK {
		outbound := core.OutboundResponse{Text: message}
		if err := sendChannelResponse(ctx, job.ch, job.payload.ChatID, outbound, job.payload.ReplyToMessageID); err == nil {
			return
		} else {
			slog.Error("channel event: send failure response failed",
				"type", job.event.Type,
				"account", job.event.AccountID,
				"chat_id", job.payload.ChatID,
				"error", err,
			)
		}
	}
	if job.event.Type == core.EventKakaoTalk {
		return
	}
	if qErr := s.store.EnqueueResponse(job.event.AccountID, string(job.event.Type), job.payload.ChatID, message); qErr != nil {
		slog.Error("channel event: enqueue failure response failed", "error", qErr)
	}
}

func (s *Server) channelTurnTimeoutDuration() time.Duration {
	if s.channelTurnTimeout < 0 {
		return 0
	}
	if s.channelTurnTimeout > 0 {
		return s.channelTurnTimeout
	}
	return defaultChannelTurnTimeout
}

func channelWorkerKey(event core.Event) string {
	return fmt.Sprintf("%s%s%s%s%s",
		event.AccountID,
		channelDispatchWorkerScopeSep,
		event.Type,
		channelDispatchWorkerScopeSep,
		channelWorkerScope(event),
	)
}

func channelWorkerScope(event core.Event) string {
	payload, err := event.ParsePayload()
	if err != nil {
		return ""
	}
	if id := strings.TrimSpace(payload.ConversationID); id != "" {
		return id
	}
	switch event.Type {
	case core.EventKakaoTalk, core.EventWebChat, core.EventDesktop:
		return firstNonEmptyWorkerScope(payload.SessionID, payload.ChatID)
	default:
		return firstNonEmptyWorkerScope(payload.ChatID, payload.SessionID)
	}
}

func firstNonEmptyWorkerScope(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (s *Server) isAccountRemovalInProgress(accountID string) bool {
	s.removingAccountMu.RLock()
	defer s.removingAccountMu.RUnlock()
	return s.removingAccount != nil && s.removingAccount[accountID]
}

func (s *Server) setAccountRemovalInProgress(accountID string, removing bool) {
	s.removingAccountMu.Lock()
	defer s.removingAccountMu.Unlock()
	if s.removingAccount == nil {
		s.removingAccount = make(map[string]bool)
	}
	if removing {
		s.removingAccount[accountID] = true
		return
	}
	delete(s.removingAccount, accountID)
}

func (s *Server) stopChannelWorkersForAccount(accountID string) error {
	prefix := accountID + channelDispatchWorkerScopeSep
	s.channelWorkersMu.Lock()
	workers := make([]*channelEventWorker, 0)
	for key, worker := range s.channelWorkers {
		if strings.HasPrefix(key, prefix) {
			workers = append(workers, worker)
		}
	}
	s.channelWorkersMu.Unlock()

	for _, worker := range workers {
		worker.cancel()
	}
	timer := time.NewTimer(channelWorkerStopTimeout)
	defer timer.Stop()
	for _, worker := range workers {
		select {
		case <-worker.done:
		case <-timer.C:
			return fmt.Errorf("timed out waiting for channel workers for account %q", accountID)
		}
	}
	return nil
}
