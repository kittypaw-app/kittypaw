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
	"github.com/jinto/kittypaw/store"
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
	event            core.Event
	payload          core.ChatPayload
	inboundID        int64
	inboundLeaseDone chan struct{}
	runtime          *engine.AccountRuntime
	baseRunOpts      *engine.RunOptions
	ch               channel.Channel
	chOK             bool
}

type channelEventWorker struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	jobs   chan channelEventJob
}

func (s *Server) enqueueChannelEvent(ctx context.Context, job channelEventJob) {
	worker := s.channelWorker(ctx, channelWorkerKey(job.event))
	if job.inboundID > 0 {
		job.inboundLeaseDone = make(chan struct{})
	}
	select {
	case worker.jobs <- job:
		s.startDurableInboundLeaseKeeper(worker.ctx, job.event.AccountID, job.inboundID, job.inboundLeaseDone)
	case <-ctx.Done():
	default:
		slog.Warn("channel event: worker queue full",
			"type", job.event.Type,
			"account", job.event.AccountID,
			"chat_id", job.payload.ChatID,
		)
		s.publishAccountEvent(job.event.AccountID, AccountEvent{
			Type:           EventStreamTurnRejected,
			Channel:        string(job.event.Type),
			ConversationID: job.payload.ConversationID,
			ChatID:         job.payload.ChatID,
			ErrorClass:     "channel_queue_full",
		})
		s.sendOrQueueChannelFailure(ctx, job, channelQueueOverflowResponse)
		s.completeDurableInboundEvent(job.event.AccountID, job.inboundID)
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
	if job.inboundLeaseDone != nil {
		defer close(job.inboundLeaseDone)
	}
	handled := false
	defer func() {
		if handled {
			s.completeDurableInboundEvent(job.event.AccountID, job.inboundID)
		}
	}()

	runCtx := ctx
	var cancel context.CancelFunc
	if timeout := s.channelTurnTimeoutDuration(); timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Fold chat-path /model overrides after dequeue, not at enqueue time.
	// A queued /model command must affect later jobs in this same worker.
	runOpts := job.runtime.ApplyActiveModel(job.baseRunOpts)
	response, runErr, panicked := s.runChannelRuntime(runCtx, job.runtime, job.event, runOpts)
	if ctx.Err() != nil {
		return
	}
	if panicked {
		s.sendOrQueueChannelFailure(ctx, job, channelRunFailureResponse)
		handled = true
		return
	}
	if runErr != nil {
		if isRuntimeAdmissionBusy(runErr) {
			s.sendOrQueueChannelFailure(ctx, job, channelQueueOverflowResponse)
			handled = true
			return
		}
		slog.Error("channel event: engine error",
			"worker", key,
			"type", job.event.Type,
			"account", job.event.AccountID,
			"chat_id", job.payload.ChatID,
			"error", runErr,
		)
		s.sendOrQueueChannelFailure(ctx, job, channelRunFailureResponse)
		handled = true
		return
	}

	engine.MarkAccountReady(job.runtime)

	if strings.TrimSpace(response) == "" {
		handled = true
		return
	}

	if !job.chOK {
		slog.Warn("channel event: no channel for response routing, enqueuing for retry",
			"type", job.event.Type, "account", job.event.AccountID)
		queued := job.event.Type == core.EventKakaoTalk
		if job.event.Type != core.EventKakaoTalk {
			id, err := s.store.EnqueueResponseWithID(job.event.AccountID, string(job.event.Type), job.payload.ChatID, response)
			if err != nil {
				slog.Error("channel event: enqueue response failed", "error", err)
				s.releaseDurableInboundEvent(job.event.AccountID, job.inboundID, err.Error())
				publishDeliveryEvent(s, job.event.AccountID, EventStreamDeliveryFailed, job.event.Type, job.payload.ChatID, map[string]string{
					"error_class": "enqueue_failed",
					"reason":      "channel_not_running",
				})
			} else {
				s.recordOutboundDelivery(store.OutboundDeliveryWrite{
					AccountID:         job.event.AccountID,
					EventType:         string(job.event.Type),
					ChatID:            job.payload.ChatID,
					Source:            store.OutboundDeliverySourceChannelReply,
					Status:            store.OutboundDeliveryStatusQueued,
					Response:          response,
					PendingResponseID: id,
				})
				publishDeliveryEvent(s, job.event.AccountID, EventStreamDeliveryQueued, job.event.Type, job.payload.ChatID, map[string]string{
					"reason": "channel_not_running",
				})
				queued = true
			}
		}
		handled = queued
		return
	}

	outbound := core.ParseOutboundResponse(response)
	var pendingID int64
	var pendingQueued bool
	var deliveryID int64
	if job.event.Type != core.EventKakaoTalk {
		id, qErr := s.store.EnqueueResponseWithID(job.event.AccountID, string(job.event.Type), job.payload.ChatID, outbound.Text)
		if qErr != nil {
			slog.Error("channel event: enqueue response before send failed", "error", qErr)
		} else {
			pendingID = id
			pendingQueued = true
			deliveryID = s.recordOutboundDelivery(store.OutboundDeliveryWrite{
				AccountID:         job.event.AccountID,
				EventType:         string(job.event.Type),
				ChatID:            job.payload.ChatID,
				Source:            store.OutboundDeliverySourceChannelReply,
				Status:            store.OutboundDeliveryStatusQueued,
				Response:          outbound.Text,
				PendingResponseID: id,
			})
			publishDeliveryEvent(s, job.event.AccountID, EventStreamDeliveryQueued, job.event.Type, job.payload.ChatID, nil)
		}
	} else {
		deliveryID = s.recordOutboundDelivery(store.OutboundDeliveryWrite{
			AccountID: job.event.AccountID,
			EventType: string(job.event.Type),
			ChatID:    job.payload.ChatID,
			Source:    store.OutboundDeliverySourceChannelReply,
			Status:    store.OutboundDeliveryStatusSending,
			Response:  outbound.Text,
		})
	}
	if err := sendChannelResponse(ctx, job.ch, job.payload.ChatID, outbound, job.payload.ReplyToMessageID); err != nil {
		slog.Error("channel event: send response failed",
			"type", job.event.Type,
			"account", job.event.AccountID,
			"chat_id", job.payload.ChatID,
			"error", err,
		)
		if job.event.Type != core.EventKakaoTalk && !pendingQueued {
			id, qErr := s.store.EnqueueResponseWithID(job.event.AccountID, string(job.event.Type), job.payload.ChatID, outbound.Text)
			if qErr != nil {
				slog.Error("channel event: enqueue response failed", "error", qErr)
				s.markOutboundDelivery(deliveryID, job.event.AccountID, store.OutboundDeliveryStatusFailed, "send_failed", err.Error())
				s.releaseDurableInboundEvent(job.event.AccountID, job.inboundID, qErr.Error())
				return
			} else {
				deliveryID = s.recordOutboundDelivery(store.OutboundDeliveryWrite{
					AccountID:         job.event.AccountID,
					EventType:         string(job.event.Type),
					ChatID:            job.payload.ChatID,
					Source:            store.OutboundDeliverySourceChannelReply,
					Status:            store.OutboundDeliveryStatusQueued,
					Response:          outbound.Text,
					PendingResponseID: id,
					ErrorClass:        "send_failed",
					ErrorMessage:      err.Error(),
				})
				publishDeliveryEvent(s, job.event.AccountID, EventStreamDeliveryQueued, job.event.Type, job.payload.ChatID, map[string]string{
					"reason": "send_failed",
				})
			}
		}
		s.markOutboundDelivery(deliveryID, job.event.AccountID, store.OutboundDeliveryStatusFailed, "send_failed", err.Error())
		publishDeliveryEvent(s, job.event.AccountID, EventStreamDeliveryFailed, job.event.Type, job.payload.ChatID, map[string]string{
			"error_class": "send_failed",
		})
		handled = true
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
		publishDeliveryEvent(s, job.event.AccountID, EventStreamDeliveryDelivered, job.event.Type, job.payload.ChatID, nil)
	}
	s.markOutboundDelivery(deliveryID, job.event.AccountID, store.OutboundDeliveryStatusDelivered, "", "")
	handled = true
}

func (s *Server) runChannelRuntime(ctx context.Context, runtime *engine.AccountRuntime, event core.Event, runOpts *engine.RunOptions) (response string, runErr error, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			engine.RecoverAccountPanic(runtime, "server.channelWorker", r)
		}
	}()
	response, runErr = runtime.Run(ctx, event, runOpts)
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
	id, qErr := s.store.EnqueueResponseWithID(job.event.AccountID, string(job.event.Type), job.payload.ChatID, message)
	if qErr != nil {
		slog.Error("channel event: enqueue failure response failed", "error", qErr)
		return
	}
	s.recordOutboundDelivery(store.OutboundDeliveryWrite{
		AccountID:         job.event.AccountID,
		EventType:         string(job.event.Type),
		ChatID:            job.payload.ChatID,
		Source:            store.OutboundDeliverySourceChannelReply,
		Status:            store.OutboundDeliveryStatusQueued,
		Response:          message,
		PendingResponseID: id,
		ErrorClass:        "turn_failed",
	})
	publishDeliveryEvent(s, job.event.AccountID, EventStreamDeliveryQueued, job.event.Type, job.payload.ChatID, map[string]string{
		"reason": "failure_response",
	})
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
		return firstNonEmptyWorkerScope(payload.SourceSessionID, payload.ChatID)
	default:
		return firstNonEmptyWorkerScope(payload.ChatID, payload.SourceSessionID)
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
