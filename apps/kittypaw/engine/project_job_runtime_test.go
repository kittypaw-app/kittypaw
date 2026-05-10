package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jinto/kittypaw/store"
)

func TestOSJobCommandRunnerBoundsCombinedErrorText(t *testing.T) {
	var emitted int
	result := OSJobCommandRunner{}.Run(context.Background(), JobCommandSpec{
		Command: "sh",
		Args: []string{
			"-c",
			fmt.Sprintf("head -c %d /dev/zero | tr '\\0' x; exit 7", projectJobErrorExcerptLimit+4096),
		},
		Dir: t.TempDir(),
		Emit: func(chunk []byte) {
			emitted += len(chunk)
		},
	})

	if result.ExitCode != 7 {
		t.Fatalf("exit code = %d, want 7", result.ExitCode)
	}
	if emitted <= projectJobErrorExcerptLimit {
		t.Fatalf("emitted = %d, want more than bounded error excerpt limit", emitted)
	}
	if len(result.ErrorText) > projectJobErrorExcerptLimit {
		t.Fatalf("error text length = %d, want <= %d", len(result.ErrorText), projectJobErrorExcerptLimit)
	}
}

func TestProjectJobRuntimeRequiresGitRepository(t *testing.T) {
	st := openProjectJobRuntimeStore(t)
	project := createRuntimeProject(t, st, t.TempDir())
	rt := NewProjectJobRuntime(ProjectJobRuntimeOptions{Store: st, AccountID: "alice", BaseDir: t.TempDir()})

	_, err := rt.prepareApprovedJob(context.Background(), planApprovedRuntimeJob(t, st, project.ID), StartProjectJobOptions{ActorID: "pm"})
	if !store.IsProjectJobError(err, store.ProjectJobErrProjectNotGitRepository) {
		t.Fatalf("prepareApprovedJob() error = %v, want %s", err, store.ProjectJobErrProjectNotGitRepository)
	}
}

func TestProjectJobRuntimeRequiresGitHead(t *testing.T) {
	st := openProjectJobRuntimeStore(t)
	root := t.TempDir()
	gitInit(t, root)
	project := createRuntimeProject(t, st, root)
	rt := NewProjectJobRuntime(ProjectJobRuntimeOptions{Store: st, AccountID: "alice", BaseDir: t.TempDir()})

	_, err := rt.prepareApprovedJob(context.Background(), planApprovedRuntimeJob(t, st, project.ID), StartProjectJobOptions{ActorID: "pm"})
	if !store.IsProjectJobError(err, store.ProjectJobErrProjectGitHeadMissing) {
		t.Fatalf("prepareApprovedJob() error = %v, want %s", err, store.ProjectJobErrProjectGitHeadMissing)
	}
}

func TestProjectJobRuntimeRejectsDirtyRoot(t *testing.T) {
	st := openProjectJobRuntimeStore(t)
	root := t.TempDir()
	gitInit(t, root)
	gitCommitFile(t, root, "README.md", "clean\n")
	if err := os.WriteFile(filepath.Join(root, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}
	project := createRuntimeProject(t, st, root)
	rt := NewProjectJobRuntime(ProjectJobRuntimeOptions{Store: st, AccountID: "alice", BaseDir: t.TempDir()})

	_, err := rt.prepareApprovedJob(context.Background(), planApprovedRuntimeJob(t, st, project.ID), StartProjectJobOptions{ActorID: "pm"})
	if !store.IsProjectJobError(err, store.ProjectJobErrProjectGitDirty) {
		t.Fatalf("prepareApprovedJob() error = %v, want %s", err, store.ProjectJobErrProjectGitDirty)
	}
}

func TestProjectJobRuntimeCreatesWorktreeForCleanGitRoot(t *testing.T) {
	st := openProjectJobRuntimeStore(t)
	root := t.TempDir()
	gitInit(t, root)
	gitCommitFile(t, root, "README.md", "clean\n")
	project := createRuntimeProject(t, st, root)
	job := planApprovedRuntimeJob(t, st, project.ID)
	baseDir := t.TempDir()
	rt := NewProjectJobRuntime(ProjectJobRuntimeOptions{Store: st, AccountID: "alice", BaseDir: baseDir})

	prepared, err := rt.prepareApprovedJob(context.Background(), job, StartProjectJobOptions{ActorID: "pm"})
	if err != nil {
		t.Fatalf("prepareApprovedJob() error = %v", err)
	}
	if !strings.Contains(prepared.WorktreePath, filepath.Join(baseDir, "worktrees", project.ID, job.TicketID, job.ID)) {
		t.Fatalf("worktree path = %q, want account managed path", prepared.WorktreePath)
	}
	if prepared.BranchName == "" || !strings.HasPrefix(prepared.BranchName, "kittypaw/") {
		t.Fatalf("branch name = %q, want kittypaw prefix", prepared.BranchName)
	}
	if _, err := os.Stat(filepath.Join(prepared.WorktreePath, "README.md")); err != nil {
		t.Fatalf("worktree README missing: %v", err)
	}
}

func TestProjectJobRuntimeRunsShellDriverAndRecordsSuccess(t *testing.T) {
	st := openProjectJobRuntimeStore(t)
	root := t.TempDir()
	gitInit(t, root)
	gitCommitFile(t, root, "README.md", "clean\n")
	project := createRuntimeProject(t, st, root)
	job := planApprovedRuntimeJob(t, st, project.ID)
	rt := NewProjectJobRuntime(ProjectJobRuntimeOptions{
		Store:     st,
		AccountID: "alice",
		BaseDir:   t.TempDir(),
		Runner: fakeJobCommandRunner{
			Stdout:     "driver output\n",
			ResultText: "changed README",
			ExitCode:   0,
		},
	})

	started, err := rt.StartJob(context.Background(), job.ID, StartProjectJobOptions{ActorID: "pm"})
	if err != nil {
		t.Fatalf("StartJob() error = %v", err)
	}
	if started.Status != store.JobStatusRunning {
		t.Fatalf("started = %+v, want running", started)
	}
	if !rt.WaitForJob(job.ID, 2*time.Second) {
		t.Fatal("job did not finish")
	}
	got, err := st.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if got.Status != store.JobStatusSucceeded || got.ExitCode != 0 || got.ResultSummary != "changed README" {
		t.Fatalf("job after success = %+v", got)
	}
	ticket, err := st.GetTicket(job.TicketID)
	if err != nil {
		t.Fatalf("GetTicket() error = %v", err)
	}
	if ticket.Status != store.TicketStatusReview {
		t.Fatalf("ticket status = %q, want review", ticket.Status)
	}
}

func TestProjectJobRuntimeLiveShellDriverRunsApprovedScript(t *testing.T) {
	st := openProjectJobRuntimeStore(t)
	root := t.TempDir()
	gitInit(t, root)
	gitCommitFile(t, root, "README.md", "clean\n")
	project := createRuntimeProject(t, st, root)
	job := planApprovedRuntimeJobWithPrompt(t, st, project.ID, "printf 'smoke\\n' > smoke.txt\n")
	rt := NewProjectJobRuntime(ProjectJobRuntimeOptions{
		Store:     st,
		AccountID: "alice",
		BaseDir:   t.TempDir(),
	})

	if _, err := rt.StartJob(context.Background(), job.ID, StartProjectJobOptions{ActorID: "pm"}); err != nil {
		t.Fatalf("StartJob() error = %v", err)
	}
	if !rt.WaitForJob(job.ID, 2*time.Second) {
		t.Fatal("job did not finish")
	}
	got, err := st.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if got.Status != store.JobStatusSucceeded || got.ExitCode != 0 {
		t.Fatalf("job after live shell run = %+v, want succeeded exit 0", got)
	}
	data, err := os.ReadFile(filepath.Join(got.WorktreePath, "smoke.txt"))
	if err != nil {
		t.Fatalf("read smoke output: %v", err)
	}
	if string(data) != "smoke\n" {
		t.Fatalf("smoke output = %q, want smoke newline", string(data))
	}
}

func TestProjectJobRuntimeRecordsFailureExitCodeAndBoundedLogs(t *testing.T) {
	st := openProjectJobRuntimeStore(t)
	root := t.TempDir()
	gitInit(t, root)
	gitCommitFile(t, root, "README.md", "clean\n")
	project := createRuntimeProject(t, st, root)
	job := planApprovedRuntimeJob(t, st, project.ID)
	rt := NewProjectJobRuntime(ProjectJobRuntimeOptions{
		Store:     st,
		AccountID: "alice",
		BaseDir:   t.TempDir(),
		Runner: fakeJobCommandRunner{
			Stdout:     strings.Repeat("x", projectJobLogTailLimit+512),
			Stderr:     "exit status 9",
			ResultText: "failed",
			ExitCode:   9,
		},
	})

	if _, err := rt.StartJob(context.Background(), job.ID, StartProjectJobOptions{ActorID: "pm"}); err != nil {
		t.Fatalf("StartJob() error = %v", err)
	}
	if !rt.WaitForJob(job.ID, 2*time.Second) {
		t.Fatal("job did not finish")
	}
	got, err := st.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if got.Status != store.JobStatusFailed || got.ExitCode != 9 || !got.LogTruncated {
		t.Fatalf("job after failure = %+v", got)
	}
	if len(got.LogTail) > projectJobLogTailLimit {
		t.Fatalf("log tail length = %d, want <= %d", len(got.LogTail), projectJobLogTailLimit)
	}
	if len(got.ErrorExcerpt) > projectJobErrorExcerptLimit {
		t.Fatalf("error excerpt length = %d, want <= %d", len(got.ErrorExcerpt), projectJobErrorExcerptLimit)
	}
}

func TestProjectJobRuntimeCancelBestEffort(t *testing.T) {
	st := openProjectJobRuntimeStore(t)
	root := t.TempDir()
	gitInit(t, root)
	gitCommitFile(t, root, "README.md", "clean\n")
	project := createRuntimeProject(t, st, root)
	job := planApprovedRuntimeJob(t, st, project.ID)
	block := make(chan struct{})
	rt := NewProjectJobRuntime(ProjectJobRuntimeOptions{
		Store:     st,
		AccountID: "alice",
		BaseDir:   t.TempDir(),
		Runner:    fakeBlockingJobCommandRunner{Block: block},
	})

	if _, err := rt.StartJob(context.Background(), job.ID, StartProjectJobOptions{ActorID: "pm"}); err != nil {
		t.Fatalf("StartJob() error = %v", err)
	}
	canceled, err := rt.CancelJob(context.Background(), job.ID, "alice", "stop")
	if err != nil {
		t.Fatalf("CancelJob() error = %v", err)
	}
	close(block)
	if canceled.Status != store.JobStatusCanceled {
		t.Fatalf("canceled = %+v", canceled)
	}
	ticket, err := st.GetTicket(job.TicketID)
	if err != nil {
		t.Fatalf("GetTicket() error = %v", err)
	}
	if ticket.Status != store.TicketStatusBacklog {
		t.Fatalf("ticket status = %q, want backlog", ticket.Status)
	}
}

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
	block := make(chan struct{})
	rt := NewProjectJobRuntime(ProjectJobRuntimeOptions{Store: st, AccountID: "alice", BaseDir: t.TempDir(), Runner: fakeBlockingJobCommandRunner{Block: block}})
	defer func() {
		close(block)
		_ = rt.WaitForJob(oneShot.ID, 2*time.Second)
	}()

	if _, err := rt.AppendJobInput(context.Background(), oneShot.ID, "alice", "x"); !store.IsProjectJobError(err, store.ProjectJobErrJobNotRunning) {
		t.Fatalf("AppendJobInput(not running) error = %v, want %s", err, store.ProjectJobErrJobNotRunning)
	}
	if _, err := rt.StartJob(context.Background(), oneShot.ID, StartProjectJobOptions{ActorID: "pm"}); err != nil {
		t.Fatalf("StartJob(oneShot) error = %v", err)
	}
	if _, err := rt.AppendJobInput(context.Background(), oneShot.ID, "alice", "x"); !store.IsProjectJobError(err, store.ProjectJobErrJobInputNotSupported) {
		t.Fatalf("AppendJobInput(one-shot) error = %v, want %s", err, store.ProjectJobErrJobInputNotSupported)
	}
	if _, err := rt.AppendJobInput(context.Background(), oneShot.ID, "alice", "\x00"); !store.IsProjectJobError(err, store.ProjectJobErrJobInputInvalid) {
		t.Fatalf("AppendJobInput(empty) error = %v, want %s", err, store.ProjectJobErrJobInputInvalid)
	}
	if _, err := rt.AppendJobInput(context.Background(), oneShot.ID, "alice", strings.Repeat("x", projectJobInputLimit+1)); !store.IsProjectJobError(err, store.ProjectJobErrJobInputTooLarge) {
		t.Fatalf("AppendJobInput(large) error = %v, want %s", err, store.ProjectJobErrJobInputTooLarge)
	}
}

func TestProjectJobRuntimeLiveShellPTYEcho(t *testing.T) {
	if os.Getenv("KITTYPAW_LIVE_PTY") != "1" {
		t.Skip("set KITTYPAW_LIVE_PTY=1 to run live PTY smoke")
	}
	st := openProjectJobRuntimeStore(t)
	root := t.TempDir()
	gitInit(t, root)
	gitCommitFile(t, root, "README.md", "clean\n")
	project := createRuntimeProject(t, st, root)
	job := planApprovedRuntimeJobWithMode(t, st, project.ID, store.JobModePTY, "while read line; do echo got:$line; break; done; exit\n")
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

func TestProjectJobRuntimeCleansWorktreeWhenStoreStartRejectsConcurrentJob(t *testing.T) {
	st := openProjectJobRuntimeStore(t)
	root := t.TempDir()
	gitInit(t, root)
	gitCommitFile(t, root, "README.md", "clean\n")
	project := createRuntimeProject(t, st, root)
	ticket, err := st.CreateTicket(store.CreateTicketRequest{ProjectID: project.ID, Title: "Concurrent"})
	if err != nil {
		t.Fatalf("CreateTicket() error = %v", err)
	}
	first := planApprovedRuntimeJobForTicket(t, st, project.ID, ticket.ID, "first")
	second := planApprovedRuntimeJobForTicket(t, st, project.ID, ticket.ID, "second")
	baseDir := t.TempDir()
	block := make(chan struct{})
	rt := NewProjectJobRuntime(ProjectJobRuntimeOptions{
		Store:     st,
		AccountID: "alice",
		BaseDir:   baseDir,
		Runner:    fakeBlockingJobCommandRunner{Block: block},
	})

	if _, err := rt.StartJob(context.Background(), first.ID, StartProjectJobOptions{ActorID: "pm"}); err != nil {
		t.Fatalf("StartJob(first) error = %v", err)
	}
	secondWorktree := filepath.Join(baseDir, "worktrees", project.ID, ticket.ID, second.ID)
	_, err = rt.StartJob(context.Background(), second.ID, StartProjectJobOptions{ActorID: "pm"})
	close(block)
	if !rt.WaitForJob(first.ID, 2*time.Second) {
		t.Fatal("first job did not finish")
	}
	if !store.IsProjectJobError(err, store.ProjectJobErrTicketHasRunningJob) {
		t.Fatalf("StartJob(second) error = %v, want %s", err, store.ProjectJobErrTicketHasRunningJob)
	}
	if _, statErr := os.Stat(secondWorktree); !os.IsNotExist(statErr) {
		t.Fatalf("second worktree stat error = %v, want not exist", statErr)
	}
	secondBranch := projectJobBranchName(ticket.Key, second.ID)
	out := gitOutputForTest(t, root, "branch", "--list", secondBranch)
	if strings.TrimSpace(out) != "" {
		t.Fatalf("second branch still exists: %q", out)
	}
}

func openProjectJobRuntimeStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func createRuntimeProject(t *testing.T, st *store.Store, root string) *store.Project {
	t.Helper()
	project, err := st.CreateProject(store.CreateProjectRequest{Key: "KITTY", Name: "Kitty", RootPath: root})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	return project
}

func planApprovedRuntimeJob(t *testing.T, st *store.Store, projectID string) *store.Job {
	t.Helper()
	return planApprovedRuntimeJobWithPrompt(t, st, projectID, "echo ok")
}

func planApprovedRuntimeJobWithPrompt(t *testing.T, st *store.Store, projectID, prompt string) *store.Job {
	t.Helper()
	return planApprovedRuntimeJobWithMode(t, st, projectID, store.JobModeOneShot, prompt)
}

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

func planApprovedRuntimeJobForTicket(t *testing.T, st *store.Store, projectID, ticketID, summary string) *store.Job {
	t.Helper()
	if err := st.EnsureDefaultDrivers(); err != nil {
		t.Fatalf("EnsureDefaultDrivers() error = %v", err)
	}
	job, err := st.PlanJob(store.PlanJobRequest{
		ProjectID:     projectID,
		TicketID:      ticketID,
		DriverID:      "shell",
		Mode:          store.JobModeOneShot,
		PromptSummary: summary,
		PromptText:    "echo " + summary,
		CreatedBy:     "pm",
	})
	if err != nil {
		t.Fatalf("PlanJob(%s) error = %v", summary, err)
	}
	approved, err := st.ApproveJob(job.ID, "pm")
	if err != nil {
		t.Fatalf("ApproveJob(%s) error = %v", summary, err)
	}
	return approved
}

func gitInit(t *testing.T, root string) {
	t.Helper()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "kittypaw@example.test")
	runGit(t, root, "config", "user.name", "KittyPaw Test")
}

func gitCommitFile(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	runGit(t, root, "add", name)
	runGit(t, root, "commit", "-m", "initial")
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

func gitOutputForTest(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
	return string(out)
}

type fakeJobCommandRunner struct {
	Stdout     string
	Stderr     string
	ResultText string
	ExitCode   int
}

func (r fakeJobCommandRunner) Run(ctx context.Context, spec JobCommandSpec) JobCommandResult {
	if r.Stdout != "" {
		spec.Emit([]byte(r.Stdout))
	}
	if r.Stderr != "" {
		spec.Emit([]byte(r.Stderr))
	}
	return JobCommandResult{ExitCode: r.ExitCode, Summary: r.ResultText, ErrorText: r.Stderr}
}

type fakeBlockingJobCommandRunner struct {
	Block chan struct{}
}

func (r fakeBlockingJobCommandRunner) Run(ctx context.Context, spec JobCommandSpec) JobCommandResult {
	select {
	case <-ctx.Done():
		return JobCommandResult{ExitCode: -1, ErrorText: ctx.Err().Error()}
	case <-r.Block:
		return JobCommandResult{ExitCode: 0, Summary: "released"}
	}
}

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
