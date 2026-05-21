package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/store"
)

const (
	defaultDelegationJobLease        = 10 * time.Minute
	defaultDelegationJobPollInterval = 5 * time.Second
	defaultDelegationJobBatchSize    = 2
)

type DelegationJobRuntimeOptions struct {
	Store         *store.Store
	Runtime       *AccountRuntime
	AccountID     string
	LeaseDuration time.Duration
	PollInterval  time.Duration
	BatchSize     int
}

type DelegationJobRuntime struct {
	store         *store.Store
	runtime       *AccountRuntime
	accountID     string
	leaseDuration time.Duration
	pollInterval  time.Duration
	batchSize     int

	mu      sync.Mutex
	running map[string]context.CancelFunc
	done    map[string]chan struct{}
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func NewDelegationJobRuntime(opts DelegationJobRuntimeOptions) *DelegationJobRuntime {
	accountID := strings.TrimSpace(opts.AccountID)
	if accountID == "" && opts.Runtime != nil {
		accountID = strings.TrimSpace(opts.Runtime.AccountID)
	}
	if accountID == "" {
		accountID = core.DefaultAccountID
	}
	lease := opts.LeaseDuration
	if lease <= 0 {
		lease = defaultDelegationJobLease
	}
	poll := opts.PollInterval
	if poll <= 0 {
		poll = defaultDelegationJobPollInterval
	}
	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = defaultDelegationJobBatchSize
	}
	if batchSize > 10 {
		batchSize = 10
	}
	return &DelegationJobRuntime{
		store:         opts.Store,
		runtime:       opts.Runtime,
		accountID:     accountID,
		leaseDuration: lease,
		pollInterval:  poll,
		batchSize:     batchSize,
		running:       map[string]context.CancelFunc{},
		done:          map[string]chan struct{}{},
	}
}

func (r *DelegationJobRuntime) Enqueue(ctx context.Context, task PMTaskSpec, parentConversationID string, parentEvent *core.Event, depth, maxDepth int, parentStaffID string) (*store.DelegationJob, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("delegation job runtime unavailable")
	}
	if r.runtime == nil {
		return nil, fmt.Errorf("delegation job account runtime unavailable")
	}
	task.StaffID = strings.TrimSpace(task.StaffID)
	task.Task = strings.TrimSpace(task.Task)
	if task.Task == "" {
		return nil, fmt.Errorf("delegation task is required")
	}
	if len(task.Task) > maxDelegateTaskLen {
		return nil, fmt.Errorf("task too long (%d > %d chars)", len(task.Task), maxDelegateTaskLen)
	}
	if maxDepth <= 0 {
		maxDepth = 3
	}
	if depth <= 0 {
		depth = 1
	}
	if depth >= maxDepth {
		return nil, fmt.Errorf("max delegation depth reached (%d)", maxDepth)
	}
	staffID, err := canonicalDelegationStaffID(r.runtime, task.StaffID)
	if err != nil {
		return nil, err
	}
	task.StaffID = staffID
	if parentConversationID == "" {
		parentConversationID = store.DefaultConversationID
	}
	var parentEventJSON string
	if parentEvent != nil {
		body, err := json.Marshal(parentEvent)
		if err != nil {
			return nil, fmt.Errorf("encode parent event: %w", err)
		}
		parentEventJSON = string(body)
	}
	parentJobID := ""
	if info, ok := DelegationInfoFromContext(ctx); ok {
		parentJobID = strings.TrimSpace(info.DelegationJobID)
	}
	return r.store.CreateDelegationJob(store.CreateDelegationJobRequest{
		AccountID:              r.accountID,
		StaffID:                task.StaffID,
		Task:                   task.Task,
		ParentConversationID:   parentConversationID,
		DelegateConversationID: delegateConversationID(parentConversationID, task.StaffID),
		ParentJobID:            parentJobID,
		ParentStaffID:          strings.TrimSpace(parentStaffID),
		ParentEventJSON:        parentEventJSON,
		Depth:                  depth,
		MaxDepth:               maxDepth,
		Now:                    time.Now().UTC(),
	})
}

func canonicalDelegationStaffID(s *AccountRuntime, staffID string) (string, error) {
	staffID = strings.TrimSpace(staffID)
	if err := core.ValidateStaffID(staffID); err != nil {
		return "", fmt.Errorf("invalid staff ID: %w", err)
	}
	if s == nil {
		return "", fmt.Errorf("no session available")
	}
	base, err := core.ResolveBaseDir(s.BaseDir)
	if err != nil {
		return "", fmt.Errorf("staff base error: %w", err)
	}
	canonicalID, ok, err := core.ResolveStaffReference(base, staffID)
	if err != nil {
		return "", fmt.Errorf("staff lookup error: %w", err)
	}
	if !ok {
		return "", fmt.Errorf("staff %q not found", staffID)
	}
	if _, err := core.ReadStaffMetaFile(base, canonicalID); err != nil {
		return "", fmt.Errorf("staff metadata error: %w", err)
	}
	return canonicalID, nil
}

func (r *DelegationJobRuntime) StartAsync(ctx context.Context) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.cancel != nil {
		r.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.wg.Add(1)
	r.mu.Unlock()

	go func() {
		defer r.wg.Done()
		r.run(runCtx)
	}()
}

func (r *DelegationJobRuntime) run(ctx context.Context) {
	_, _ = r.StartAvailable(ctx)
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := r.StartAvailable(ctx); err != nil {
				slog.Warn("delegation jobs: run once failed", "account", r.accountID, "error", err)
			}
		}
	}
}

func (r *DelegationJobRuntime) RunOnce(ctx context.Context) (int, error) {
	jobs, err := r.claimAvailableJobs()
	if err != nil {
		return 0, err
	}
	var wg sync.WaitGroup
	for _, job := range jobs {
		job := job
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.runClaimedJob(ctx, job)
		}()
	}
	wg.Wait()
	return len(jobs), nil
}

func (r *DelegationJobRuntime) StartAvailable(ctx context.Context) (int, error) {
	jobs, err := r.claimAvailableJobs()
	if err != nil {
		return 0, err
	}
	for _, job := range jobs {
		job := job
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			r.runClaimedJob(ctx, job)
		}()
	}
	return len(jobs), nil
}

func (r *DelegationJobRuntime) claimAvailableJobs() ([]store.DelegationJob, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("delegation job runtime unavailable")
	}
	r.mu.Lock()
	capacity := r.batchSize - len(r.running)
	r.mu.Unlock()
	if capacity <= 0 {
		return []store.DelegationJob{}, nil
	}
	jobs, err := r.store.ClaimDelegationJobs(store.ClaimDelegationJobsRequest{
		AccountID:     r.accountID,
		Limit:         capacity,
		Now:           time.Now().UTC(),
		LeaseDuration: r.leaseDuration,
	})
	if err != nil {
		return nil, err
	}
	return jobs, nil
}

func (r *DelegationJobRuntime) runClaimedJob(ctx context.Context, job store.DelegationJob) {
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	r.mu.Lock()
	r.running[job.ID] = cancel
	r.done[job.ID] = done
	r.mu.Unlock()
	defer func() {
		if p := recover(); p != nil {
			RecoverAccountPanic(r.runtime, "delegation.job", p)
			r.failPanickedJob(job, p)
		}
		cancel()
		close(done)
		r.mu.Lock()
		delete(r.running, job.ID)
		delete(r.done, job.ID)
		r.mu.Unlock()
	}()

	stopHeartbeat := r.startLeaseHeartbeat(runCtx, job)
	defer stopHeartbeat()

	parentEvent := parentEventFromDelegationJob(job)
	runCtx = ContextWithDelegationInfo(runCtx, DelegationRunOptions{
		DelegationJobID: job.ID,
		StaffID:         job.ParentStaffID,
	})
	result := executeDelegateTask(runCtx, PMTaskSpec{
		StaffID:    job.StaffID,
		Task:       job.Task,
		Background: true,
	}, r.runtime, job.Depth, job.MaxDepth, job.ParentConversationID, parentEvent)

	current, ok, err := r.store.GetDelegationJob(job.ID)
	if err == nil && ok && current.Status == store.DelegationJobStatusCanceled {
		return
	}
	if ctx.Err() != nil {
		_, _ = r.store.ReleaseDelegationJobClaim(job.ID, job.ClaimToken, time.Now().UTC(), "runtime shutting down")
		return
	}

	status := store.DelegationJobStatusSucceeded
	errorClass := ""
	errorMessage := ""
	if !result.Success {
		status = store.DelegationJobStatusFailed
		errorClass = "delegate_failed"
		errorMessage = result.Result
	}
	if _, err := r.store.FinishDelegationJob(store.FinishDelegationJobRequest{
		ID:                     job.ID,
		ClaimToken:             job.ClaimToken,
		Status:                 status,
		Result:                 result.Result,
		ErrorClass:             errorClass,
		ErrorMessage:           errorMessage,
		TokenUsage:             result.TokenUsage,
		DurationMS:             result.DurationMs,
		DelegateConversationID: result.ConversationID,
		Now:                    time.Now().UTC(),
	}); err != nil {
		slog.Warn("delegation jobs: finish failed", "account", r.accountID, "job_id", job.ID, "error", err)
	}
}

func (r *DelegationJobRuntime) failPanickedJob(job store.DelegationJob, recovered any) {
	if r == nil || r.store == nil {
		return
	}
	current, ok, err := r.store.GetDelegationJob(job.ID)
	if err == nil && ok && current.Status == store.DelegationJobStatusCanceled {
		return
	}
	message := fmt.Sprintf("%v", recovered)
	result := "delegate panicked: " + message
	if _, err := r.store.FinishDelegationJob(store.FinishDelegationJobRequest{
		ID:           job.ID,
		ClaimToken:   job.ClaimToken,
		Status:       store.DelegationJobStatusFailed,
		Result:       result,
		ErrorClass:   "panic",
		ErrorMessage: message,
		Now:          time.Now().UTC(),
	}); err != nil {
		slog.Warn("delegation jobs: panic finish failed", "account", r.accountID, "job_id", job.ID, "error", err)
	}
}

func (r *DelegationJobRuntime) startLeaseHeartbeat(ctx context.Context, job store.DelegationJob) func() {
	interval := r.leaseDuration / 3
	if interval <= 0 {
		interval = time.Minute
	}
	if interval > time.Minute {
		interval = time.Minute
	}
	heartbeatCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				ok, err := r.store.ExtendDelegationJobLease(job.ID, job.ClaimToken, time.Now().UTC(), r.leaseDuration)
				if err != nil {
					slog.Warn("delegation jobs: lease heartbeat failed", "account", r.accountID, "job_id", job.ID, "error", err)
					continue
				}
				if !ok {
					return
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func parentEventFromDelegationJob(job store.DelegationJob) *core.Event {
	if strings.TrimSpace(job.ParentEventJSON) == "" {
		return nil
	}
	var event core.Event
	if err := json.Unmarshal([]byte(job.ParentEventJSON), &event); err != nil {
		slog.Warn("delegation jobs: bad parent event json", "job_id", job.ID, "error", err)
		return nil
	}
	return &event
}

func (r *DelegationJobRuntime) GetJob(jobID string) (*store.DelegationJob, bool, error) {
	if r == nil || r.store == nil {
		return nil, false, fmt.Errorf("delegation job runtime unavailable")
	}
	job, ok, err := r.store.GetDelegationJob(jobID)
	if err != nil || !ok {
		return job, ok, err
	}
	if job.AccountID != r.accountID {
		return nil, false, sql.ErrNoRows
	}
	return job, true, nil
}

func (r *DelegationJobRuntime) ListJobs(filter store.DelegationJobListFilter) ([]store.DelegationJob, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("delegation job runtime unavailable")
	}
	filter.AccountID = r.accountID
	return r.store.ListDelegationJobs(filter)
}

func (r *DelegationJobRuntime) CancelJob(_ context.Context, jobID, actorID, reason string) (*store.DelegationJob, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("delegation job runtime unavailable")
	}
	jobID = strings.TrimSpace(jobID)
	job, err := r.store.CancelDelegationJobForAccount(r.accountID, jobID, actorID, reason)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	cancel := r.running[jobID]
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return job, nil
}

func (r *DelegationJobRuntime) WaitForJob(jobID string, timeout time.Duration) bool {
	if r == nil {
		return false
	}
	jobID = strings.TrimSpace(jobID)
	r.mu.Lock()
	done := r.done[jobID]
	r.mu.Unlock()
	if done == nil {
		job, ok, err := r.GetJob(jobID)
		return err == nil && ok && isTerminalDelegationJob(job.Status)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func isTerminalDelegationJob(status string) bool {
	return status == store.DelegationJobStatusSucceeded ||
		status == store.DelegationJobStatusFailed ||
		status == store.DelegationJobStatusCanceled
}

func (r *DelegationJobRuntime) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	for _, cancel := range r.running {
		if cancel != nil {
			cancel()
		}
	}
	r.mu.Unlock()
	r.wg.Wait()
}
