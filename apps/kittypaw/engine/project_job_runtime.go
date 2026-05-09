package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jinto/kittypaw/store"
)

const (
	projectJobLogTailLimit      = 64 * 1024
	projectJobEventLogLimit     = 8 * 1024
	projectJobErrorExcerptLimit = 4 * 1024
)

type ProjectJobRuntimeOptions struct {
	Store     *store.Store
	AccountID string
	BaseDir   string
	Runner    JobCommandRunner
}

type ProjectJobRuntime struct {
	store     *store.Store
	accountID string
	baseDir   string
	runner    JobCommandRunner

	mu      sync.Mutex
	running map[string]*runningProjectJob
	done    map[string]chan struct{}
}

type runningProjectJob struct {
	cancel context.CancelFunc
}

type StartProjectJobOptions struct {
	ActorID string `json:"actor_id"`
}

type ProjectGitStatus struct {
	ProjectID       string `json:"project_id"`
	RootPath        string `json:"root_path"`
	IsGitRepository bool   `json:"is_git_repository"`
	HasHead         bool   `json:"has_head"`
	IsDirty         bool   `json:"is_dirty"`
	Message         string `json:"message"`
}

type ProjectJobLogs struct {
	Job     *store.Job       `json:"job"`
	LogTail string           `json:"log_tail"`
	Events  []store.JobEvent `json:"events"`
}

type preparedProjectJob struct {
	Job          *store.Job
	Project      *store.Project
	Ticket       *store.Ticket
	Driver       store.DriverDefinition
	WorktreePath string
	BranchName   string
	Prompt       string
}

type JobCommandSpec struct {
	Command string
	Args    []string
	Dir     string
	Env     []string
	Stdin   string
	Emit    func([]byte)
}

type JobCommandResult struct {
	ExitCode  int
	Summary   string
	ErrorText string
}

type JobCommandRunner interface {
	Run(ctx context.Context, spec JobCommandSpec) JobCommandResult
}

type OSJobCommandRunner struct{}

func NewProjectJobRuntime(opts ProjectJobRuntimeOptions) *ProjectJobRuntime {
	runner := opts.Runner
	if runner == nil {
		runner = OSJobCommandRunner{}
	}
	return &ProjectJobRuntime{
		store:     opts.Store,
		accountID: strings.TrimSpace(opts.AccountID),
		baseDir:   strings.TrimSpace(opts.BaseDir),
		runner:    runner,
		running:   map[string]*runningProjectJob{},
		done:      map[string]chan struct{}{},
	}
}

func (OSJobCommandRunner) Run(ctx context.Context, spec JobCommandSpec) JobCommandResult {
	cmd := exec.CommandContext(ctx, spec.Command, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = append(os.Environ(), spec.Env...)
	if spec.Stdin != "" {
		cmd.Stdin = strings.NewReader(spec.Stdin)
	}
	var combined bytes.Buffer
	cmd.Stdout = emitWriter{emit: spec.Emit, mirror: &combined}
	cmd.Stderr = emitWriter{emit: spec.Emit, mirror: &combined}
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitCode = 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		if ctx.Err() != nil {
			return JobCommandResult{ExitCode: -1, ErrorText: ctx.Err().Error()}
		}
	}
	return JobCommandResult{ExitCode: exitCode, ErrorText: strings.TrimSpace(combined.String())}
}

type emitWriter struct {
	emit   func([]byte)
	mirror *bytes.Buffer
}

func (w emitWriter) Write(p []byte) (int, error) {
	if w.mirror != nil {
		_, _ = w.mirror.Write(p)
	}
	if w.emit != nil {
		cp := append([]byte(nil), p...)
		w.emit(cp)
	}
	return len(p), nil
}

func (r *ProjectJobRuntime) ProjectGitStatus(ctx context.Context, projectID string) (ProjectGitStatus, error) {
	project, err := r.store.GetProject(projectID)
	if err != nil {
		return ProjectGitStatus{}, err
	}
	status := ProjectGitStatus{ProjectID: project.ID, RootPath: project.RootPath}
	if err := runGitQuiet(ctx, project.RootPath, "rev-parse", "--is-inside-work-tree"); err != nil {
		status.Message = "This project is not a git repository. Initialize git for this project?"
		return status, nil
	}
	status.IsGitRepository = true
	if err := runGitQuiet(ctx, project.RootPath, "rev-parse", "--verify", "HEAD"); err != nil {
		status.Message = "Create an initial commit before starting a job."
		return status, nil
	}
	status.HasHead = true
	out, err := gitOutput(ctx, project.RootPath, "status", "--porcelain")
	if err != nil {
		return status, err
	}
	status.IsDirty = strings.TrimSpace(out) != ""
	if status.IsDirty {
		status.Message = "Commit or stash project changes before starting a job."
	}
	return status, nil
}

func (r *ProjectJobRuntime) InitProjectGit(ctx context.Context, projectID string) (ProjectGitStatus, error) {
	project, err := r.store.GetProject(projectID)
	if err != nil {
		return ProjectGitStatus{}, err
	}
	if err := runGitQuiet(ctx, project.RootPath, "init"); err != nil {
		return ProjectGitStatus{}, err
	}
	return r.ProjectGitStatus(ctx, project.ID)
}

func (r *ProjectJobRuntime) StartJob(ctx context.Context, jobID string, opts StartProjectJobOptions) (*store.Job, error) {
	job, err := r.store.GetJob(jobID)
	if err != nil {
		return nil, err
	}
	prepared, err := r.prepareApprovedJob(ctx, job, opts)
	if err != nil {
		return nil, err
	}
	metadata := projectJobMetadata(prepared, 0, 0)
	started, err := r.store.StartJob(job.ID, store.StartJobRequest{
		ActorID:      opts.ActorID,
		WorktreePath: prepared.WorktreePath,
		BranchName:   prepared.BranchName,
		MetadataJSON: metadata,
	})
	if err != nil {
		return nil, err
	}
	prepared.Job = started

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	r.mu.Lock()
	r.running[job.ID] = &runningProjectJob{cancel: cancel}
	r.done[job.ID] = done
	r.mu.Unlock()

	go r.runPreparedJob(runCtx, prepared, done)
	return started, nil
}

func (r *ProjectJobRuntime) CancelJob(_ context.Context, jobID, actorID, reason string) (*store.Job, error) {
	r.mu.Lock()
	if running := r.running[strings.TrimSpace(jobID)]; running != nil && running.cancel != nil {
		running.cancel()
	}
	r.mu.Unlock()
	return r.store.CancelJob(jobID, actorID, reason)
}

func (r *ProjectJobRuntime) JobLogs(jobID string) (*ProjectJobLogs, error) {
	job, err := r.store.GetJob(jobID)
	if err != nil {
		return nil, err
	}
	events, err := r.store.ListJobEvents(job.ID)
	if err != nil {
		return nil, err
	}
	return &ProjectJobLogs{Job: job, LogTail: job.LogTail, Events: events}, nil
}

func (r *ProjectJobRuntime) WaitForJob(jobID string, timeout time.Duration) bool {
	r.mu.Lock()
	done := r.done[strings.TrimSpace(jobID)]
	r.mu.Unlock()
	if done == nil {
		return false
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

func (r *ProjectJobRuntime) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, running := range r.running {
		if running.cancel != nil {
			running.cancel()
		}
	}
}

func (r *ProjectJobRuntime) prepareApprovedJob(ctx context.Context, job *store.Job, _ StartProjectJobOptions) (*preparedProjectJob, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("project job runtime unavailable")
	}
	if job.Status != store.JobStatusApproved {
		if job.Status == store.JobStatusPlanned {
			return nil, &store.ProjectJobError{Code: store.ProjectJobErrJobNotApproved, Message: fmt.Sprintf("job %q is not approved", job.ID)}
		}
		return nil, &store.ProjectJobError{Code: store.ProjectJobErrJobAlreadyStarted, Message: fmt.Sprintf("job %q cannot start from status %q", job.ID, job.Status)}
	}
	if job.Mode != store.JobModeOneShot {
		return nil, &store.ProjectJobError{Code: store.ProjectJobErrDriverModeUnsupported, Message: fmt.Sprintf("driver mode %q is not supported yet", job.Mode)}
	}
	project, err := r.store.GetProject(job.ProjectID)
	if err != nil {
		return nil, err
	}
	ticket, err := r.store.GetTicket(job.TicketID)
	if err != nil {
		return nil, err
	}
	driver, err := decodeJobDriver(job)
	if err != nil {
		return nil, err
	}
	status, err := r.ProjectGitStatus(ctx, project.ID)
	if err != nil {
		return nil, err
	}
	switch {
	case !status.IsGitRepository:
		return nil, &store.ProjectJobError{Code: store.ProjectJobErrProjectNotGitRepository, Message: status.Message}
	case !status.HasHead:
		return nil, &store.ProjectJobError{Code: store.ProjectJobErrProjectGitHeadMissing, Message: status.Message}
	case status.IsDirty:
		return nil, &store.ProjectJobError{Code: store.ProjectJobErrProjectGitDirty, Message: status.Message}
	}

	baseDir := r.baseDir
	if baseDir == "" {
		baseDir = filepath.Dir(project.RootPath)
	}
	worktreePath := filepath.Join(baseDir, "worktrees", project.ID, job.TicketID, job.ID)
	if _, err := os.Stat(worktreePath); err == nil {
		return nil, &store.ProjectJobError{Code: store.ProjectJobErrWorktreeCreateFailed, Message: "job worktree already exists: " + worktreePath}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return nil, err
	}
	branchName := projectJobBranchName(ticket.Key, job.ID)
	if out, err := gitCombined(ctx, project.RootPath, "worktree", "add", "-b", branchName, worktreePath, "HEAD"); err != nil {
		return nil, &store.ProjectJobError{Code: store.ProjectJobErrWorktreeCreateFailed, Message: strings.TrimSpace(out)}
	}
	prepared := &preparedProjectJob{
		Job:          job,
		Project:      project,
		Ticket:       ticket,
		Driver:       driver,
		WorktreePath: worktreePath,
		BranchName:   branchName,
	}
	prepared.Prompt = buildProjectJobPrompt(prepared)
	return prepared, nil
}

func (r *ProjectJobRuntime) runPreparedJob(ctx context.Context, prepared *preparedProjectJob, done chan struct{}) {
	defer func() {
		r.mu.Lock()
		delete(r.running, prepared.Job.ID)
		r.mu.Unlock()
		_, _ = r.store.AddJobEvent(store.AddJobEventRequest{
			JobID:        prepared.Job.ID,
			Type:         "cleanup",
			Message:      "runtime cleanup",
			MetadataJSON: projectJobMetadata(prepared, 0, 0),
		})
		close(done)
	}()

	var tail boundedJobLog
	spec, err := buildProjectJobCommand(prepared)
	if err != nil {
		_, _ = r.store.FailJob(prepared.Job.ID, store.FinishJobRequest{
			ActorID:       "runtime",
			ResultSummary: "driver command failed",
			ErrorExcerpt:  truncateString(err.Error(), projectJobErrorExcerptLimit),
			ExitCode:      1,
			MetadataJSON:  projectJobMetadata(prepared, 1, 0),
		})
		return
	}
	startedAt := time.Now()
	spec.Emit = func(chunk []byte) {
		if len(chunk) == 0 {
			return
		}
		truncated := tail.Append(string(chunk))
		_, _ = r.store.UpdateJobLog(prepared.Job.ID, store.UpdateJobLogRequest{LogTail: tail.String(), LogTruncated: truncated})
		_, _ = r.store.AddJobEvent(store.AddJobEventRequest{
			JobID:        prepared.Job.ID,
			Type:         "log",
			Message:      truncateString(string(chunk), projectJobEventLogLimit),
			MetadataJSON: projectJobMetadata(prepared, 0, time.Since(startedAt).Milliseconds()),
		})
	}
	result := r.runner.Run(ctx, spec)
	current, err := r.store.GetJob(prepared.Job.ID)
	if err == nil && current.Status == store.JobStatusCanceled {
		return
	}
	durationMS := time.Since(startedAt).Milliseconds()
	metadata := projectJobMetadata(prepared, result.ExitCode, durationMS)
	summary := strings.TrimSpace(result.Summary)
	if summary == "" && result.ExitCode == 0 {
		summary = "job completed"
	}
	if summary == "" {
		summary = "job failed"
	}
	if result.ExitCode == 0 {
		_, _ = r.store.SucceedJob(prepared.Job.ID, store.FinishJobRequest{
			ActorID:       "runtime",
			ResultSummary: summary,
			LogTail:       tail.String(),
			LogTruncated:  tail.Truncated(),
			ExitCode:      result.ExitCode,
			MetadataJSON:  metadata,
		})
		return
	}
	errorText := strings.TrimSpace(result.ErrorText)
	if errorText == "" {
		errorText = tail.String()
	}
	_, _ = r.store.FailJob(prepared.Job.ID, store.FinishJobRequest{
		ActorID:       "runtime",
		ResultSummary: summary,
		LogTail:       tail.String(),
		ErrorExcerpt:  truncateString(errorText, projectJobErrorExcerptLimit),
		LogTruncated:  tail.Truncated(),
		ExitCode:      result.ExitCode,
		MetadataJSON:  metadata,
	})
}

func buildProjectJobPrompt(p *preparedProjectJob) string {
	return fmt.Sprintf(`Project: %s - %s
Project root: %s
Ticket: %s - %s
Ticket status: %s
Ticket priority: %d
Job: %s
Driver: %s
Mode: %s

User-approved prompt:
%s

Leave all changes in this worktree:
%s

Do not commit, push, or open a pull request.`,
		p.Project.Key, p.Project.Name,
		p.Project.RootPath,
		p.Ticket.Key, p.Ticket.Title,
		p.Ticket.Status,
		p.Ticket.Priority,
		p.Job.ID,
		p.Driver.ID,
		p.Job.Mode,
		p.Job.PromptText,
		p.WorktreePath,
	)
}

func buildProjectJobCommand(p *preparedProjectJob) (JobCommandSpec, error) {
	if p.Job.Mode != store.JobModeOneShot {
		return JobCommandSpec{}, &store.ProjectJobError{Code: store.ProjectJobErrDriverModeUnsupported, Message: fmt.Sprintf("driver mode %q is not supported yet", p.Job.Mode)}
	}
	command := strings.TrimSpace(p.Driver.Command)
	if command == "" {
		return JobCommandSpec{}, &store.ProjectJobError{Code: store.ProjectJobErrDriverNotFound, Message: "driver command is empty"}
	}
	switch p.Driver.ID {
	case "codex":
		return JobCommandSpec{
			Command: command,
			Args:    []string{"exec", "-C", p.WorktreePath, "--json", "--sandbox", "workspace-write", "--ask-for-approval", "never", p.Prompt},
			Dir:     p.WorktreePath,
		}, nil
	case "claude":
		return JobCommandSpec{
			Command: command,
			Args:    []string{"-p", "--output-format", "stream-json", "--permission-mode", "acceptEdits", p.Prompt},
			Dir:     p.WorktreePath,
		}, nil
	case "shell":
		args, err := driverDefaultArgs(p.Driver.DefaultArgsJSON)
		if err != nil {
			return JobCommandSpec{}, err
		}
		return JobCommandSpec{
			Command: command,
			Args:    args,
			Dir:     p.WorktreePath,
			Stdin:   p.Job.PromptText,
			Env: []string{
				"KITTYPAW_JOB_PROMPT=" + p.Job.PromptText,
				"KITTYPAW_JOB_CONTEXT=" + p.Prompt,
			},
		}, nil
	default:
		args, err := driverDefaultArgs(p.Driver.DefaultArgsJSON)
		if err != nil {
			return JobCommandSpec{}, err
		}
		return JobCommandSpec{
			Command: command,
			Args:    args,
			Dir:     p.WorktreePath,
			Stdin:   p.Prompt,
			Env:     []string{"KITTYPAW_JOB_PROMPT=" + p.Prompt},
		}, nil
	}
}

func decodeJobDriver(job *store.Job) (store.DriverDefinition, error) {
	var driver store.DriverDefinition
	if err := json.Unmarshal([]byte(job.DriverSnapshotJSON), &driver); err != nil {
		return driver, &store.ProjectJobError{Code: store.ProjectJobErrDriverNotFound, Message: "invalid driver snapshot"}
	}
	if strings.TrimSpace(driver.ID) == "" {
		driver.ID = job.DriverID
	}
	if strings.TrimSpace(driver.Command) == "" {
		return driver, &store.ProjectJobError{Code: store.ProjectJobErrDriverNotFound, Message: "driver command is empty"}
	}
	return driver, nil
}

func driverDefaultArgs(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var args []string
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, err
	}
	return args, nil
}

func projectJobBranchName(ticketKey, jobID string) string {
	short := strings.TrimPrefix(strings.TrimSpace(jobID), "job_")
	if len(short) > 8 {
		short = short[:8]
	}
	replacer := strings.NewReplacer(" ", "-", "_", "-", "/", "-", "\\", "-", ":", "-")
	key := strings.Trim(replacer.Replace(strings.TrimSpace(ticketKey)), "-")
	if key == "" {
		key = "ticket"
	}
	if short == "" {
		short = "job"
	}
	return "kittypaw/" + key + "/" + short
}

func projectJobMetadata(p *preparedProjectJob, exitCode int, durationMS int64) string {
	data, _ := json.Marshal(map[string]any{
		"exit_code":     exitCode,
		"duration_ms":   durationMS,
		"driver_id":     p.Driver.ID,
		"mode":          p.Job.Mode,
		"worktree_path": p.WorktreePath,
		"branch_name":   p.BranchName,
	})
	return string(data)
}

type boundedJobLog struct {
	value     string
	truncated bool
}

func (b *boundedJobLog) Append(s string) bool {
	b.value += s
	if len(b.value) > projectJobLogTailLimit {
		b.value = b.value[len(b.value)-projectJobLogTailLimit:]
		b.truncated = true
	}
	return b.truncated
}

func (b *boundedJobLog) String() string {
	return b.value
}

func (b *boundedJobLog) Truncated() bool {
	return b.truncated
}

func truncateString(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[len(s)-limit:]
}

func runGitQuiet(ctx context.Context, root string, args ...string) error {
	_, err := gitCombined(ctx, root, args...)
	return err
}

func gitOutput(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	return string(out), err
}

func gitCombined(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
