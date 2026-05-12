package server

import (
	"context"
	"fmt"
	"log/slog"
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

type channelEventJob struct {
	event   core.Event
	payload core.ChatPayload
	session *engine.Session
	runOpts *engine.RunOptions
	ch      channel.Channel
	chOK    bool
}

type channelEventWorker struct {
	jobs chan channelEventJob
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
	worker := &channelEventWorker{jobs: make(chan channelEventJob, channelDispatchQueueSize)}
	s.channelWorkers[key] = worker
	go s.runChannelWorker(ctx, key, worker)
	return worker
}

func (s *Server) runChannelWorker(ctx context.Context, key string, worker *channelEventWorker) {
	defer func() {
		s.channelWorkersMu.Lock()
		if s.channelWorkers[key] == worker {
			delete(s.channelWorkers, key)
		}
		s.channelWorkersMu.Unlock()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-worker.jobs:
			s.processChannelEvent(ctx, key, job)
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

	response, runErr, panicked := s.runChannelSession(runCtx, job.session, job.event, job.runOpts)
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

	if !job.chOK {
		slog.Warn("channel event: no channel for response routing, enqueuing for retry",
			"type", job.event.Type, "account", job.event.AccountID)
		if job.event.Type != core.EventKakaoTalk {
			_ = s.store.EnqueueResponse(job.event.AccountID, string(job.event.Type), job.payload.ChatID, response)
		}
		return
	}

	outbound := core.ParseOutboundResponse(response)
	if err := sendChannelResponse(ctx, job.ch, job.payload.ChatID, outbound, job.payload.ReplyToMessageID); err != nil {
		slog.Error("channel event: send response failed",
			"type", job.event.Type,
			"account", job.event.AccountID,
			"chat_id", job.payload.ChatID,
			"error", err,
		)
		if job.event.Type != core.EventKakaoTalk {
			if qErr := s.store.EnqueueResponse(job.event.AccountID, string(job.event.Type), job.payload.ChatID, outbound.Text); qErr != nil {
				slog.Error("channel event: enqueue response failed", "error", qErr)
			}
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
	return fmt.Sprintf("%s%s%s", event.AccountID, channelDispatchWorkerScopeSep, event.Type)
}
