# Project PTY Job Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add executable `pty` Job mode so Projects Jobs can run interactive Drivers, stream transcripts, and accept user input while running.

**Architecture:** Reuse the existing Project/Ticket/Job/Driver store model, git worktree preparation, and `ProjectJobRuntime` lifecycle. Add a PTY runner abstraction beside the existing one-shot command runner, route running Job input through the live runtime session, and expose the same input path through API, web UI, and the `Projects` engine tool.

**Tech Stack:** Go, SQLite store, `github.com/creack/pty`, existing chi HTTP server, vanilla `server/web/projects.js`, existing Go test suite.

---

## File Structure

- Modify `store/projects.go`: default Driver mode support and structured PTY input error codes.
- Modify `store/projects_test.go`: default Driver coverage for `pty`.
- Create `engine/project_job_pty.go`: PTY runner interfaces, OS implementation, transcript sanitizer.
- Create `engine/project_job_pty_test.go`: sanitizer and fake PTY runner tests.
- Modify `engine/project_job_runtime.go`: branch `one_shot` vs `pty`, live session tracking, input method.
- Modify `engine/project_job_runtime_test.go`: PTY lifecycle/input tests and optional live smoke.
- Modify `engine/executor.go`: route `Projects.appendJobInput` through runtime.
- Modify `engine/projects_scope_test.go`: engine tool coverage for PTY input.
- Modify `server/server.go`: add `/api/v1/jobs/{job}/input`.
- Modify `server/api_projects.go`: add input handler and error handling.
- Modify `server/api_projects_test.go`: HTTP input coverage.
- Modify `server/web/projects.js`: running PTY input controls and API call.
- Modify `server/web_projects_test.go`: static web token coverage.
- Modify `server/web/style.css`: compact Job input layout if existing classes are insufficient.
- Modify `TASKS.md` and `../../TASKS.md`: mark plan creation and track implementation progress.

---

## Task 1: Store Constants and Default Driver Modes

**Files:**
- Modify: `store/projects.go`
- Modify: `store/projects_test.go`

- [ ] **Step 1: Write the failing default Driver test**

Add this test near `TestEnsureDefaultDriversAndListDrivers` in `store/projects_test.go`:

```go
func TestEnsureDefaultDriversIncludePTYMode(t *testing.T) {
	st := openProjectsTestStore(t)
	if err := st.EnsureDefaultDrivers(); err != nil {
		t.Fatalf("EnsureDefaultDrivers() error = %v", err)
	}
	drivers, err := st.ListDrivers()
	if err != nil {
		t.Fatalf("ListDrivers() error = %v", err)
	}
	for _, id := range []string{"codex", "claude", "shell"} {
		var found *DriverDefinition
		for i := range drivers {
			if drivers[i].ID == id {
				found = &drivers[i]
				break
			}
		}
		if found == nil {
			t.Fatalf("driver %q not found in %+v", id, drivers)
		}
		if !driverSupportsMode(*found, JobModeOneShot) || !driverSupportsMode(*found, JobModePTY) {
			t.Fatalf("driver %q modes = %s, want one_shot and pty", id, found.SupportedModesJSON)
		}
	}
}
```

- [ ] **Step 2: Run the store test and verify it fails**

Run:

```bash
go test ./store -run TestEnsureDefaultDriversIncludePTYMode -count=1
```

Expected: fail because default Drivers only include `["one_shot"]`.

- [ ] **Step 3: Add structured error constants and PTY default modes**

In `store/projects.go`, extend the project job error constants:

```go
ProjectJobErrJobNotRunning        = "job_not_running"
ProjectJobErrJobInputNotSupported = "job_input_not_supported"
ProjectJobErrJobSessionUnavailable = "job_session_unavailable"
ProjectJobErrJobInputInvalid      = "job_input_invalid"
ProjectJobErrJobInputTooLarge     = "job_input_too_large"
```

Update `EnsureDefaultDrivers` defaults:

```go
defaults := []UpsertDriverRequest{
	{ID: "codex", DisplayName: "Codex", Command: "codex", SupportedModesJSON: `["one_shot","pty"]`, DefaultArgsJSON: `[]`, Enabled: true},
	{ID: "claude", DisplayName: "Claude", Command: "claude", SupportedModesJSON: `["one_shot","pty"]`, DefaultArgsJSON: `[]`, Enabled: true},
	{ID: "shell", DisplayName: "Shell", Command: "sh", SupportedModesJSON: `["one_shot","pty"]`, DefaultArgsJSON: `[]`, Enabled: true},
}
```

- [ ] **Step 4: Run the store test and verify it passes**

Run:

```bash
go test ./store -run 'TestEnsureDefaultDriversIncludePTYMode|TestEnsureDefaultDriversAndListDrivers|TestJobPlanStoresResolvedDriverSnapshot' -count=1
```

Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add store/projects.go store/projects_test.go
git commit -m "feat(projects): allow pty job mode in drivers"
```

---

## Task 2: PTY Runner Interface and Transcript Sanitizer

**Files:**
- Create: `engine/project_job_pty.go`
- Create: `engine/project_job_pty_test.go`

- [ ] **Step 1: Write failing sanitizer tests**

Create `engine/project_job_pty_test.go`:

```go
package engine

import (
	"strings"
	"testing"
)

func TestSanitizeProjectPTYTranscriptStripsANSIAndOSC(t *testing.T) {
	raw := "\x1b[31mred\x1b[0m\n\x1b]0;title\x07prompt\tok\r\n"
	got := sanitizeProjectPTYTranscript([]byte(raw))
	if strings.Contains(got, "\x1b") || strings.Contains(got, "]0;title") {
		t.Fatalf("sanitized transcript still has control sequence: %q", got)
	}
	if got != "red\nprompt\tok\r\n" {
		t.Fatalf("sanitized transcript = %q", got)
	}
}

func TestSanitizeProjectPTYTranscriptReplacesInvalidUTF8(t *testing.T) {
	got := sanitizeProjectPTYTranscript([]byte{'o', 'k', 0xff, '\n'})
	if got != "ok�\n" {
		t.Fatalf("sanitized invalid utf8 = %q", got)
	}
}
```

- [ ] **Step 2: Run the sanitizer tests and verify they fail**

Run:

```bash
go test ./engine -run TestSanitizeProjectPTYTranscript -count=1
```

Expected: fail because `sanitizeProjectPTYTranscript` does not exist.

- [ ] **Step 3: Add PTY types and sanitizer implementation**

Create `engine/project_job_pty.go`:

```go
package engine

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/creack/pty"
)

const projectJobInputLimit = 16 * 1024

var (
	projectPTYCSI = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	projectPTYOSC = regexp.MustCompile(`\x1b\][^\x07]*(\x07|\x1b\\)`)
)

type JobPTYSpec struct {
	Command      string
	Args         []string
	Dir          string
	Env          []string
	InitialInput string
	Emit         func([]byte)
}

type JobPTYResult struct {
	ExitCode  int
	Summary   string
	ErrorText string
}

type JobPTYSession interface {
	Input(text string) error
	Wait(ctx context.Context) JobPTYResult
	Close() error
}

type JobPTYRunner interface {
	Start(ctx context.Context, spec JobPTYSpec) (JobPTYSession, error)
}

type OSJobPTYRunner struct{}

type osJobPTYSession struct {
	cmd  *exec.Cmd
	file *os.File
	once sync.Once
	err  error
}

func (OSJobPTYRunner) Start(ctx context.Context, spec JobPTYSpec) (JobPTYSession, error) {
	cmd := exec.CommandContext(ctx, spec.Command, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = append(os.Environ(), spec.Env...)
	f, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	session := &osJobPTYSession{cmd: cmd, file: f}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := f.Read(buf)
			if n > 0 && spec.Emit != nil {
				chunk := append([]byte(nil), buf[:n]...)
				spec.Emit(chunk)
			}
			if readErr != nil {
				if readErr != io.EOF && !strings.Contains(readErr.Error(), "input/output error") {
					session.err = readErr
				}
				return
			}
		}
	}()
	if spec.InitialInput != "" {
		if _, err := f.Write([]byte(spec.InitialInput)); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	return session, nil
}

func (s *osJobPTYSession) Input(text string) error {
	_, err := s.file.Write([]byte(text))
	return err
}

func (s *osJobPTYSession) Wait(ctx context.Context) JobPTYResult {
	err := s.cmd.Wait()
	_ = s.Close()
	exitCode := 0
	if err != nil {
		exitCode = 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		if ctx.Err() != nil {
			return JobPTYResult{ExitCode: -1, ErrorText: ctx.Err().Error()}
		}
	}
	if s.err != nil && err == nil {
		return JobPTYResult{ExitCode: 1, ErrorText: s.err.Error()}
	}
	return JobPTYResult{ExitCode: exitCode}
}

func (s *osJobPTYSession) Close() error {
	s.once.Do(func() {
		if s.file != nil {
			s.err = s.file.Close()
		}
	})
	return s.err
}

func sanitizeProjectPTYTranscript(p []byte) string {
	s := string(bytes.ToValidUTF8(p, []byte("�")))
	s = projectPTYOSC.ReplaceAllString(s, "")
	s = projectPTYCSI.ReplaceAllString(s, "")
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteRune(r)
		case r >= 0x20 && r != 0x7f:
			b.WriteRune(r)
		}
	}
	if !utf8.ValidString(b.String()) {
		return string(bytes.ToValidUTF8([]byte(b.String()), []byte("�")))
	}
	return b.String()
}
```

- [ ] **Step 4: Run sanitizer tests and gofmt**

Run:

```bash
gofmt -w engine/project_job_pty.go engine/project_job_pty_test.go
go test ./engine -run TestSanitizeProjectPTYTranscript -count=1
```

Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add engine/project_job_pty.go engine/project_job_pty_test.go
git commit -m "feat(projects): add pty job runner primitives"
```

---

## Task 3: Runtime PTY Lifecycle and Input

**Files:**
- Modify: `engine/project_job_runtime.go`
- Modify: `engine/project_job_runtime_test.go`

- [ ] **Step 1: Write failing fake PTY runtime tests**

Append these fake types to `engine/project_job_runtime_test.go`:

```go
type fakeJobPTYRunner struct {
	Started chan JobPTYSpec
	Session *fakeJobPTYSession
}

func (r fakeJobPTYRunner) Start(ctx context.Context, spec JobPTYSpec) (JobPTYSession, error) {
	if r.Started != nil {
		r.Started <- spec
	}
	if r.Session == nil {
		return nil, fmt.Errorf("missing fake PTY session")
	}
	if spec.Emit != nil {
		spec.Emit([]byte("\x1b[32mpty output\x1b[0m\n"))
	}
	return r.Session, nil
}

type fakeJobPTYSession struct {
	InputCh  chan string
	ResultCh chan JobPTYResult
	Closed   bool
}

func (s *fakeJobPTYSession) Input(text string) error {
	s.InputCh <- text
	return nil
}

func (s *fakeJobPTYSession) Wait(ctx context.Context) JobPTYResult {
	select {
	case result := <-s.ResultCh:
		return result
	case <-ctx.Done():
		return JobPTYResult{ExitCode: -1, ErrorText: ctx.Err().Error()}
	}
}

func (s *fakeJobPTYSession) Close() error {
	s.Closed = true
	return nil
}
```

Add these tests:

```go
func TestProjectJobRuntimeStartsPTYAndAcceptsInput(t *testing.T) {
	st := openProjectJobRuntimeStore(t)
	root := t.TempDir()
	gitInit(t, root)
	gitCommitFile(t, root, "README.md", "clean\n")
	project := createRuntimeProject(t, st, root)
	job := planApprovedRuntimeJobWithMode(t, st, project.ID, store.JobModePTY, "hello pty")
	session := &fakeJobPTYSession{InputCh: make(chan string, 1), ResultCh: make(chan JobPTYResult, 1)}
	rt := NewProjectJobRuntime(ProjectJobRuntimeOptions{
		Store:     st,
		AccountID: "alice",
		BaseDir:   t.TempDir(),
		PTYRunner: fakeJobPTYRunner{Started: make(chan JobPTYSpec, 1), Session: session},
	})

	started, err := rt.StartJob(context.Background(), job.ID, StartProjectJobOptions{ActorID: "pm"})
	if err != nil {
		t.Fatalf("StartJob() error = %v", err)
	}
	if started.Status != store.JobStatusRunning {
		t.Fatalf("started = %+v, want running", started)
	}
	input, err := rt.AppendJobInput(context.Background(), job.ID, "alice", "continue\n")
	if err != nil {
		t.Fatalf("AppendJobInput() error = %v", err)
	}
	if input.Event.Type != "input" || input.Job.ID != job.ID {
		t.Fatalf("input result = %+v", input)
	}
	select {
	case got := <-session.InputCh:
		if got != "continue\n" {
			t.Fatalf("input text = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("PTY input was not written")
	}
	session.ResultCh <- JobPTYResult{ExitCode: 0, Summary: "pty done"}
	if !rt.WaitForJob(job.ID, 2*time.Second) {
		t.Fatal("pty job did not finish")
	}
	got, err := st.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if got.Status != store.JobStatusSucceeded || got.ResultSummary != "pty done" {
		t.Fatalf("job after pty success = %+v", got)
	}
	events, err := st.ListJobEvents(job.ID)
	if err != nil {
		t.Fatalf("ListJobEvents() error = %v", err)
	}
	if !hasJobEvent(events, "pty_started") || !hasJobEvent(events, "input") || !hasJobEvent(events, "transcript") || !hasJobEvent(events, "pty_closed") {
		t.Fatalf("events = %+v, want pty_started/input/transcript/pty_closed", events)
	}
	if strings.Contains(got.LogTail, "\x1b") || !strings.Contains(got.LogTail, "pty output") {
		t.Fatalf("log tail = %q, want sanitized PTY output", got.LogTail)
	}
}

func TestProjectJobRuntimePTYInputErrors(t *testing.T) {
	st := openProjectJobRuntimeStore(t)
	root := t.TempDir()
	gitInit(t, root)
	gitCommitFile(t, root, "README.md", "clean\n")
	project := createRuntimeProject(t, st, root)
	oneShot := planApprovedRuntimeJobWithMode(t, st, project.ID, store.JobModeOneShot, "echo ok")
	rt := NewProjectJobRuntime(ProjectJobRuntimeOptions{Store: st, AccountID: "alice", BaseDir: t.TempDir(), Runner: fakeBlockingJobCommandRunner{Block: make(chan struct{})}})

	if _, err := rt.AppendJobInput(context.Background(), oneShot.ID, "alice", "x"); !store.IsProjectJobError(err, store.ProjectJobErrJobNotRunning) {
		t.Fatalf("AppendJobInput(not running) error = %v, want %s", err, store.ProjectJobErrJobNotRunning)
	}
	if _, err := rt.StartJob(context.Background(), oneShot.ID, StartProjectJobOptions{ActorID: "pm"}); err != nil {
		t.Fatalf("StartJob(oneShot) error = %v", err)
	}
	if _, err := rt.AppendJobInput(context.Background(), oneShot.ID, "alice", "x"); !store.IsProjectJobError(err, store.ProjectJobErrJobInputNotSupported) {
		t.Fatalf("AppendJobInput(one-shot) error = %v, want %s", err, store.ProjectJobErrJobInputNotSupported)
	}
	if _, err := rt.AppendJobInput(context.Background(), oneShot.ID, "alice", strings.Repeat("x", projectJobInputLimit+1)); !store.IsProjectJobError(err, store.ProjectJobErrJobInputTooLarge) {
		t.Fatalf("AppendJobInput(large) error = %v, want %s", err, store.ProjectJobErrJobInputTooLarge)
	}
}
```

Add helpers:

```go
func planApprovedRuntimeJobWithMode(t *testing.T, st *store.Store, projectID, mode, prompt string) *store.Job {
	t.Helper()
	if err := st.EnsureDefaultDrivers(); err != nil {
		t.Fatalf("EnsureDefaultDrivers() error = %v", err)
	}
	ticket, err := st.CreateTicket(store.CreateTicketRequest{ProjectID: projectID, Title: "Run driver"})
	if err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}
	job, err := st.PlanJob(store.PlanJobRequest{
		ProjectID:     projectID,
		TicketID:      ticket.ID,
		DriverID:      "shell",
		Mode:          mode,
		PromptSummary: "Run driver",
		PromptText:    prompt,
		CreatedBy:     "pm",
	})
	if err != nil {
		t.Fatalf("PlanJob() error = %v", err)
	}
	approved, err := st.ApproveJob(job.ID, "pm")
	if err != nil {
		t.Fatalf("ApproveJob() error = %v", err)
	}
	return approved
}

func hasJobEvent(events []store.JobEvent, typ string) bool {
	for _, event := range events {
		if event.Type == typ {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run the runtime PTY tests and verify they fail**

Run:

```bash
go test ./engine -run 'TestProjectJobRuntimeStartsPTYAndAcceptsInput|TestProjectJobRuntimePTYInputErrors' -count=1
```

Expected: fail because `PTYRunner`, `AppendJobInput`, and PTY mode support are not implemented.

- [ ] **Step 3: Extend runtime options and running session state**

In `engine/project_job_runtime.go`, change the runtime structs:

```go
type ProjectJobRuntimeOptions struct {
	Store     *store.Store
	AccountID string
	BaseDir   string
	Runner    JobCommandRunner
	PTYRunner JobPTYRunner
}

type ProjectJobRuntime struct {
	store     *store.Store
	accountID string
	baseDir   string
	runner    JobCommandRunner
	ptyRunner JobPTYRunner

	mu      sync.Mutex
	running map[string]*runningProjectJob
	done    map[string]chan struct{}
}

type runningProjectJob struct {
	mode   string
	cancel context.CancelFunc
	input  func(string) error
	close  func() error
}
```

Initialize `ptyRunner` in `NewProjectJobRuntime`:

```go
ptyRunner := opts.PTYRunner
if ptyRunner == nil {
	ptyRunner = OSJobPTYRunner{}
}
```

- [ ] **Step 4: Allow `pty` in preparation and command building**

In `prepareApprovedJob`, replace the one-shot-only check with:

```go
if job.Mode != store.JobModeOneShot && job.Mode != store.JobModePTY {
	return nil, &store.ProjectJobError{Code: store.ProjectJobErrDriverModeUnsupported, Message: fmt.Sprintf("driver mode %q is not supported yet", job.Mode)}
}
```

Add `buildProjectPTYJobSpec(prepared)`:

```go
func buildProjectPTYJobSpec(p *preparedProjectJob) (JobPTYSpec, error) {
	command := strings.TrimSpace(p.Driver.Command)
	if command == "" {
		return JobPTYSpec{}, &store.ProjectJobError{Code: store.ProjectJobErrDriverNotFound, Message: "driver command is empty"}
	}
	switch p.Driver.ID {
	case "codex":
		return JobPTYSpec{
			Command: command,
			Args:    []string{"-C", p.WorktreePath, "--sandbox", "workspace-write", "--ask-for-approval", "on-request", "--no-alt-screen", p.Prompt},
			Dir:     p.WorktreePath,
		}, nil
	case "claude":
		return JobPTYSpec{
			Command: command,
			Args:    []string{"--permission-mode", "default", p.Prompt},
			Dir:     p.WorktreePath,
		}, nil
	default:
		args, err := driverDefaultArgs(p.Driver.DefaultArgsJSON)
		if err != nil {
			return JobPTYSpec{}, err
		}
		initial := p.Prompt
		if p.Driver.ID == "shell" {
			initial = p.Job.PromptText
		}
		if initial != "" && !strings.HasSuffix(initial, "\n") {
			initial += "\n"
		}
		return JobPTYSpec{
			Command:      command,
			Args:         args,
			Dir:          p.WorktreePath,
			InitialInput: initial,
			Env: []string{
				"KITTYPAW_JOB_PROMPT=" + p.Job.PromptText,
				"KITTYPAW_JOB_CONTEXT=" + p.Prompt,
			},
		}, nil
	}
}
```

- [ ] **Step 5: Branch `StartJob` into one-shot and PTY runners**

After `store.StartJob` succeeds, keep the existing one-shot path for `one_shot`.
For `pty`, call a new `runPreparedPTYJob` goroutine and register the live
session's input function before returning.

Use this pattern:

```go
if prepared.Job.Mode == store.JobModePTY {
	go r.runPreparedPTYJob(runCtx, prepared, done)
	return started, nil
}
go r.runPreparedJob(runCtx, prepared, done)
return started, nil
```

Inside `runPreparedPTYJob`:

- build `JobPTYSpec`,
- set `spec.Emit` to sanitize, append tail, and add `transcript` events,
- start the PTY session,
- register `running[jobID].input = session.Input` and `close = session.Close`,
- add `pty_started`,
- wait for result,
- handle `canceled`,
- call `SucceedJob` or `FailJob`,
- add `pty_closed`,
- always cleanup `running` and close `done`.

- [ ] **Step 6: Add `AppendJobInput`**

Add:

```go
type AppendProjectJobInputResult struct {
	Job   *store.Job      `json:"job"`
	Event *store.JobEvent `json:"event"`
}

func (r *ProjectJobRuntime) AppendJobInput(ctx context.Context, jobID, actorID, text string) (*AppendProjectJobInputResult, error) {
	text = strings.ReplaceAll(text, "\x00", "")
	if text == "" {
		return nil, &store.ProjectJobError{Code: store.ProjectJobErrJobInputInvalid, Message: "job input is empty"}
	}
	if len(text) > projectJobInputLimit {
		return nil, &store.ProjectJobError{Code: store.ProjectJobErrJobInputTooLarge, Message: "job input is too large"}
	}
	job, err := r.store.GetJob(jobID)
	if err != nil {
		return nil, err
	}
	if job.Status != store.JobStatusRunning {
		return nil, &store.ProjectJobError{Code: store.ProjectJobErrJobNotRunning, Message: fmt.Sprintf("job %q is not running", job.ID)}
	}
	if job.Mode != store.JobModePTY {
		return nil, &store.ProjectJobError{Code: store.ProjectJobErrJobInputNotSupported, Message: fmt.Sprintf("job mode %q does not accept input", job.Mode)}
	}
	r.mu.Lock()
	running := r.running[job.ID]
	r.mu.Unlock()
	if running == nil || running.input == nil {
		return nil, &store.ProjectJobError{Code: store.ProjectJobErrJobSessionUnavailable, Message: "job session is not available in this daemon"}
	}
	if err := running.input(text); err != nil {
		return nil, &store.ProjectJobError{Code: store.ProjectJobErrDriverProcessFailed, Message: err.Error()}
	}
	event, err := r.store.AddJobEvent(store.AddJobEventRequest{
		JobID:        job.ID,
		Type:         "input",
		ActorID:      actorID,
		Message:      truncateString(text, projectJobInputLimit),
		MetadataJSON: projectJobInputMetadata(job, len(text)),
	})
	if err != nil {
		return nil, err
	}
	_ = ctx
	return &AppendProjectJobInputResult{Job: job, Event: event}, nil
}
```

- [ ] **Step 7: Run runtime tests**

Run:

```bash
gofmt -w engine/project_job_runtime.go engine/project_job_runtime_test.go
go test ./engine -run 'TestProjectJobRuntimeStartsPTYAndAcceptsInput|TestProjectJobRuntimePTYInputErrors|TestProjectJobRuntimeRunsShellDriverAndRecordsSuccess|TestProjectJobRuntimeCancelBestEffort' -count=1
```

Expected: pass.

- [ ] **Step 8: Commit**

```bash
git add engine/project_job_runtime.go engine/project_job_runtime_test.go
git commit -m "feat(projects): run interactive pty jobs"
```

---

## Task 4: Job Input HTTP API

**Files:**
- Modify: `server/server.go`
- Modify: `server/api_projects.go`
- Modify: `server/api_projects_test.go`

- [ ] **Step 1: Write failing API test**

In `server/api_projects_test.go`, add a helper similar to
`newProjectsAPITestServerWithRunner` that accepts `engine.JobPTYRunner`:

Also add `fmt` to the test file imports because the fake PTY runner returns a
formatted error when no session is configured.

```go
func newProjectsAPITestServerWithPTYRunner(t *testing.T, runner engine.JobPTYRunner) *Server {
	t.Helper()
	cfg := core.DefaultConfig()
	cfg.Server.APIKey = "api-key"
	cfg.Workspace.LiveIndex = false
	deps := buildAccountDeps(t, filepath.Join(t.TempDir(), "accounts"), "alice", &cfg)
	deps.JobRuntime = engine.NewProjectJobRuntime(engine.ProjectJobRuntimeOptions{
		Store:     deps.Store,
		AccountID: deps.Account.ID,
		BaseDir:   deps.Account.BaseDir,
		PTYRunner: runner,
	})
	srv := New([]*AccountDeps{deps}, "test")
	if srv.session != nil {
		srv.session.Indexer = nil
	}
	deps.LiveIndexer = nil
	return srv
}
```

Add `TestProjectsAPIJobInputUsesRuntime` using fake PTY types from a server
test local copy:

```go
type fakeServerPTYRunner struct {
	Session *fakeServerPTYSession
}

func (r fakeServerPTYRunner) Start(ctx context.Context, spec engine.JobPTYSpec) (engine.JobPTYSession, error) {
	if r.Session == nil {
		return nil, fmt.Errorf("missing fake PTY session")
	}
	if spec.Emit != nil {
		spec.Emit([]byte("server pty output\n"))
	}
	return r.Session, nil
}

type fakeServerPTYSession struct {
	InputCh  chan string
	ResultCh chan engine.JobPTYResult
}

func (s *fakeServerPTYSession) Input(text string) error {
	s.InputCh <- text
	return nil
}

func (s *fakeServerPTYSession) Wait(ctx context.Context) engine.JobPTYResult {
	select {
	case result := <-s.ResultCh:
		return result
	case <-ctx.Done():
		return engine.JobPTYResult{ExitCode: -1, ErrorText: ctx.Err().Error()}
	}
}

func (s *fakeServerPTYSession) Close() error {
	return nil
}

func projectsAPIPlanJobWithMode(t *testing.T, srv *Server, ticketID, driverID, mode, prompt string) struct{ ID string } {
	t.Helper()
	var planned struct {
		Job struct {
			ID string `json:"id"`
		} `json:"job"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/tickets/"+ticketID+"/jobs/plan", map[string]any{
		"driver_id":      driverID,
		"mode":           mode,
		"prompt_summary": "Run job",
		"prompt_text":    prompt,
	}, http.StatusCreated, &planned)
	return struct{ ID string }{ID: planned.Job.ID}
}
```

```go
func TestProjectsAPIJobInputUsesRuntime(t *testing.T) {
	session := &fakeServerPTYSession{InputCh: make(chan string, 1), ResultCh: make(chan engine.JobPTYResult, 1)}
	srv := newProjectsAPITestServerWithPTYRunner(t, fakeServerPTYRunner{Session: session})
	project := projectsAPICreateGitProject(t, srv, "kitty")
	ticket := projectsAPICreateTicket(t, srv, project.ID, "Run pty job")
	planned := projectsAPIPlanJobWithMode(t, srv, ticket.ID, "shell", store.JobModePTY, "cat")
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/jobs/"+planned.ID+"/approve", map[string]any{"actor_id": "alice"}, http.StatusOK, nil)
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/jobs/"+planned.ID+"/start", map[string]any{"actor_id": "alice"}, http.StatusAccepted, nil)

	var input struct {
		Accepted bool `json:"accepted"`
		Job      struct {
			ID string `json:"id"`
		} `json:"job"`
		Event struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"event"`
	}
	projectsAPIRequest(t, srv, http.MethodPost, "/api/v1/jobs/"+planned.ID+"/input", map[string]any{
		"actor_id": "web",
		"text":     "hello\n",
	}, http.StatusOK, &input)
	if !input.Accepted || input.Job.ID != planned.ID || input.Event.Type != "input" || input.Event.Message != "hello\n" {
		t.Fatalf("input response = %+v", input)
	}
	select {
	case got := <-session.InputCh:
		if got != "hello\n" {
			t.Fatalf("input text = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("PTY input was not forwarded")
	}
	session.ResultCh <- engine.JobPTYResult{ExitCode: 0, Summary: "done"}
}
```

- [ ] **Step 2: Run the API test and verify it fails**

Run:

```bash
go test ./server -run TestProjectsAPIJobInputUsesRuntime -count=1
```

Expected: fail because route and handler are missing.

- [ ] **Step 3: Add route and handler**

In `server/server.go`, add:

```go
r.Post("/jobs/{job}/input", s.handleJobInput)
```

In `server/api_projects.go`, add:

```go
func (s *Server) handleJobInput(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ActorID string `json:"actor_id"`
		Text    string `json:"text"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	runtime := s.projectsSession(r).ProjectJobRuntime
	if runtime == nil {
		writeError(w, http.StatusInternalServerError, "project job runtime unavailable")
		return
	}
	result, err := runtime.AppendJobInput(r.Context(), chi.URLParam(r, "job"), body.ActorID, body.Text)
	if err != nil {
		writeProjectJobAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "job": result.Job, "event": result.Event})
}
```

Update `writeProjectJobAPIError` to map the new input errors:

```go
case store.ProjectJobErrJobNotRunning, store.ProjectJobErrJobInputNotSupported, store.ProjectJobErrJobSessionUnavailable, store.ProjectJobErrJobInputInvalid, store.ProjectJobErrJobInputTooLarge:
	status = http.StatusConflict
```

- [ ] **Step 4: Run API tests**

Run:

```bash
gofmt -w server/server.go server/api_projects.go server/api_projects_test.go
go test ./server -run 'TestProjectsAPIJobInputUsesRuntime|TestProjectsAPIJobStartAndLogsUseRuntime' -count=1
```

Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add server/server.go server/api_projects.go server/api_projects_test.go
git commit -m "feat(projects): expose pty job input api"
```

---

## Task 5: Projects Engine Tool Input Routing

**Files:**
- Modify: `engine/executor.go`
- Modify: `engine/projects_scope_test.go`

- [ ] **Step 1: Write failing engine tool test**

Add to `engine/projects_scope_test.go`:

Also add `fmt` to the test file imports because the fake PTY runner returns a
formatted error when no session is configured.

```go
func TestExecuteProjectsAppendJobInputUsesRuntime(t *testing.T) {
	st := openTestStore(t)
	project := createProjectsScopeRuntimeProject(t, st, "kitty")
	ticket, err := st.CreateTicket(store.CreateTicketRequest{ProjectID: project.ID, Title: "PTY"})
	if err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}
	if err := st.EnsureDefaultDrivers(); err != nil {
		t.Fatalf("EnsureDefaultDrivers() error = %v", err)
	}
	job, err := st.PlanJob(store.PlanJobRequest{
		ProjectID:     project.ID,
		TicketID:      ticket.ID,
		DriverID:      "shell",
		Mode:          store.JobModePTY,
		PromptSummary: "PTY",
		PromptText:    "cat",
		CreatedBy:     "pm",
	})
	if err != nil {
		t.Fatalf("PlanJob() error = %v", err)
	}
	approved, err := st.ApproveJob(job.ID, "pm")
	if err != nil {
		t.Fatalf("ApproveJob() error = %v", err)
	}
	session := &fakeProjectsToolPTYSession{InputCh: make(chan string, 1), ResultCh: make(chan JobPTYResult, 1)}
	rt := NewProjectJobRuntime(ProjectJobRuntimeOptions{Store: st, AccountID: "alice", BaseDir: t.TempDir(), PTYRunner: fakeProjectsToolPTYRunner{Session: session}})
	sess := &Session{Store: st, ProjectJobRuntime: rt}
	if _, err := executeProjects(context.Background(), skillCallForProjectsTest("startJob", approved.ID, map[string]any{"actor_id": "pm"}), sess); err != nil {
		t.Fatalf("executeProjects(startJob) error = %v", err)
	}
	result, err := executeProjects(context.Background(), skillCallForProjectsTest("appendJobInput", approved.ID, map[string]any{"actor_id": "pm", "text": "yes\n"}), sess)
	if err != nil {
		t.Fatalf("executeProjects(appendJobInput) error = %v", err)
	}
	if !strings.Contains(result, `"event"`) || !strings.Contains(result, `"input"`) {
		t.Fatalf("appendJobInput result = %s", result)
	}
	select {
	case got := <-session.InputCh:
		if got != "yes\n" {
			t.Fatalf("input = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("input was not forwarded")
	}
	session.ResultCh <- JobPTYResult{ExitCode: 0, Summary: "done"}
}
```

Add the fake types near `fakeProjectsToolRunner`:

```go
type fakeProjectsToolPTYRunner struct {
	Session *fakeProjectsToolPTYSession
}

func (r fakeProjectsToolPTYRunner) Start(ctx context.Context, spec JobPTYSpec) (JobPTYSession, error) {
	if r.Session == nil {
		return nil, fmt.Errorf("missing fake PTY session")
	}
	if spec.Emit != nil {
		spec.Emit([]byte("tool pty output\n"))
	}
	return r.Session, nil
}

type fakeProjectsToolPTYSession struct {
	InputCh  chan string
	ResultCh chan JobPTYResult
}

func (s *fakeProjectsToolPTYSession) Input(text string) error {
	s.InputCh <- text
	return nil
}

func (s *fakeProjectsToolPTYSession) Wait(ctx context.Context) JobPTYResult {
	select {
	case result := <-s.ResultCh:
		return result
	case <-ctx.Done():
		return JobPTYResult{ExitCode: -1, ErrorText: ctx.Err().Error()}
	}
}

func (s *fakeProjectsToolPTYSession) Close() error {
	return nil
}
```

- [ ] **Step 2: Run the engine tool test and verify it fails**

Run:

```bash
go test ./engine -run TestExecuteProjectsAppendJobInputUsesRuntime -count=1
```

Expected: fail because `appendJobInput` only appends an event and does not write to runtime.

- [ ] **Step 3: Route tool to runtime**

Replace the `appendJobInput` case in `engine/executor.go` with:

```go
case "appendJobInput":
	jobID, err := projectsToolStringArg(call, 0, "job")
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	if s.ProjectJobRuntime == nil {
		return jsonResult(map[string]any{"error": "project job runtime unavailable"})
	}
	opts := projectsJobOptionsArg(call, 1)
	result, err := s.ProjectJobRuntime.AppendJobInput(ctx, jobID, strings.TrimSpace(opts.ActorID), opts.Text)
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	return jsonResult(map[string]any{"job": result.Job, "event": result.Event})
```

- [ ] **Step 4: Run engine tool tests**

Run:

```bash
gofmt -w engine/executor.go engine/projects_scope_test.go
go test ./engine -run 'TestExecuteProjectsAppendJobInputUsesRuntime|TestProjectsToolCancelJobStopsRuntimeJob|TestExecuteProjectsPlanJobAndRejectStart' -count=1
```

Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add engine/executor.go engine/projects_scope_test.go
git commit -m "feat(projects): route job input through engine tool"
```

---

## Task 6: Web UI Input Controls

**Files:**
- Modify: `server/web/projects.js`
- Modify: `server/web/style.css`
- Modify: `server/web_projects_test.go`

- [ ] **Step 1: Write failing web asset test**

Extend `TestProjectsWebIncludesJobRuntimeControls` in `server/web_projects_test.go` with tokens:

```go
for _, token := range []string{
	"_sendJobInput",
	"projects-job-input",
	"projects-job-input-send",
	"/api/v1/jobs/' + encodeURIComponent(this._selectedJob) + '/input",
	"current.status === 'running' && current.mode === 'pty'",
} {
	if !strings.Contains(src, token) {
		t.Fatalf("projects module missing pty input token %s", token)
	}
}
```

- [ ] **Step 2: Run the web test and verify it fails**

Run:

```bash
go test ./server -run TestProjectsWebIncludesJobRuntimeControls -count=1
```

Expected: fail because the UI lacks input controls.

- [ ] **Step 3: Add running PTY input UI**

In `_jobDetailHTML()` in `server/web/projects.js`, after the cancel button:

```js
    if (current.status === 'running' && current.mode === 'pty') {
      html += '<form class="projects-job-input" id="projects-job-input-form">' +
        '<textarea class="input projects-job-input-text" id="projects-job-input" rows="2" spellcheck="false" placeholder="Input is recorded in Job events"></textarea>' +
        '<button class="btn btn--primary btn--sm" id="projects-job-input-send" type="submit">Send</button>' +
      '</form>';
    }
```

In `_bindEvents()`, add:

```js
    const jobInputForm = document.getElementById('projects-job-input-form');
    if (jobInputForm) {
      jobInputForm.onsubmit = event => {
        event.preventDefault();
        this._sendJobInput();
      };
    }
```

Add method:

```js
  async _sendJobInput() {
    if (!this._selectedJob) return;
    const input = document.getElementById('projects-job-input');
    if (!input) return;
    const text = input.value || '';
    if (text.length === 0) return;
    try {
      await api('/api/v1/jobs/' + encodeURIComponent(this._selectedJob) + '/input', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ actor_id: 'web', text }),
      });
      input.value = '';
      await this._loadJobLogs(this._selectedJob);
      await this._loadProjectBoard();
    } catch (e) {
      this._setError(e);
    }
    this._render();
  },
```

In `server/web/style.css`, add:

```css
.projects-job-input {
  display: grid;
  gap: 8px;
  margin: 10px 0;
}

.projects-job-input-text {
  min-height: 72px;
  resize: vertical;
  font-family: var(--font-mono);
}
```

- [ ] **Step 4: Run web tests**

Run:

```bash
go test ./server -run 'TestProjectsWebIncludesJobRuntimeControls|TestProjectsWebModuleUsesProjectsTicketsJobsAndDriversAPIs' -count=1
```

Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add server/web/projects.js server/web/style.css server/web_projects_test.go
git commit -m "feat(projects): add pty job input controls"
```

---

## Task 7: Optional Live Shell PTY Smoke

**Files:**
- Modify: `engine/project_job_runtime_test.go`

- [ ] **Step 1: Add opt-in live smoke test**

Add:

```go
func TestProjectJobRuntimeLiveShellPTYEcho(t *testing.T) {
	if os.Getenv("KITTYPAW_LIVE_PTY") != "1" {
		t.Skip("set KITTYPAW_LIVE_PTY=1 to run live PTY smoke")
	}
	st := openProjectJobRuntimeStore(t)
	root := t.TempDir()
	gitInit(t, root)
	gitCommitFile(t, root, "README.md", "clean\n")
	project := createRuntimeProject(t, st, root)
	job := planApprovedRuntimeJobWithMode(t, st, project.ID, store.JobModePTY, "while read line; do echo got:$line; break; done\n")
	rt := NewProjectJobRuntime(ProjectJobRuntimeOptions{Store: st, AccountID: "alice", BaseDir: t.TempDir()})
	if _, err := rt.StartJob(context.Background(), job.ID, StartProjectJobOptions{ActorID: "pm"}); err != nil {
		t.Fatalf("StartJob() error = %v", err)
	}
	if _, err := rt.AppendJobInput(context.Background(), job.ID, "alice", "hello\n"); err != nil {
		t.Fatalf("AppendJobInput() error = %v", err)
	}
	if !rt.WaitForJob(job.ID, 5*time.Second) {
		t.Fatal("live PTY job did not finish")
	}
	got, err := st.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if got.Status != store.JobStatusSucceeded || !strings.Contains(got.LogTail, "got:hello") {
		t.Fatalf("live PTY job = %+v log=%q", got, got.LogTail)
	}
}
```

- [ ] **Step 2: Verify default skip and optional pass**

Run:

```bash
go test ./engine -run TestProjectJobRuntimeLiveShellPTYEcho -count=1
KITTYPAW_LIVE_PTY=1 go test ./engine -run TestProjectJobRuntimeLiveShellPTYEcho -count=1
```

Expected: first command skips; second command passes on macOS/Linux with PTY support.

- [ ] **Step 3: Commit**

```bash
git add engine/project_job_runtime_test.go
git commit -m "test(projects): add live pty shell smoke"
```

---

## Task 8: Full Verification, TASKS, and Review Prep

**Files:**
- Modify: `TASKS.md`
- Modify: `../../TASKS.md`

- [ ] **Step 1: Run focused package tests**

Run:

```bash
go test ./store ./engine ./server -count=1
```

Expected: pass.

- [ ] **Step 2: Run CI gate**

Run:

```bash
make test-ci
```

Expected: pass.

- [ ] **Step 3: Update TASKS**

In `TASKS.md`, update the Projects Phase 2 block:

```markdown
- [x] **D2: 사용자 spec review** — `2026-05-10-project-pty-job-runtime-design.md` 승인 반영.
- [x] **T0: 구현 계획 작성** — `docs/superpowers/plans/2026-05-10-project-pty-job-runtime-implementation.md`.
- [x] **T1: store/runtime/API/tool/UI 구현** — PTY Job mode, input forwarding, transcript events, web controls.
- [x] **T2: 검증** — `go test ./store ./engine ./server -count=1`, `make test-ci`, opt-in `KITTYPAW_LIVE_PTY=1` smoke.
```

In `../../TASKS.md`, update the Projects PTY follow-up:

```markdown
- [x] **D2**: 사용자 spec review.
- [x] **T0**: 구현 계획 작성.
- [x] **T1**: PTY Job Runtime 구현.
- [x] **T2**: focused tests, CI, live PTY smoke 검증.
```

- [ ] **Step 4: Commit verification docs**

```bash
git add TASKS.md ../../TASKS.md
git commit -m "docs(projects): record pty job runtime implementation"
```

- [ ] **Step 5: Request final code review**

Use the repository review flow requested by the user. If findings come back,
fix them with focused tests, rerun `go test ./store ./engine ./server -count=1`
and `make test-ci`, then commit the fixes.

---

## Self-Review

- Spec coverage: store mode support, PTY runner, runtime lifecycle, input API,
  engine tool, web UI, transcript sanitization, cancellation, restart behavior,
  tests, and live smoke all map to tasks.
- Scope check: tmux, full terminal emulator, full raw log archive,
  worktree cleanup, commit/push/PR, and session recovery are not included.
- Type consistency: `JobPTYRunner`, `JobPTYSession`, `JobPTYSpec`,
  `AppendProjectJobInputResult`, `AppendJobInput`, `projectJobInputLimit`, and
  new store error constants use the same names throughout.
- Test coverage: deterministic fake PTY tests cover lifecycle and input; live
  shell PTY smoke is opt-in because it depends on local PTY behavior.
