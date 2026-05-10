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

	"github.com/creack/pty"
)

const projectJobInputLimit = 16 * 1024

var (
	projectPTYCSI = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]")
	projectPTYOSC = regexp.MustCompile("\x1b\\][^\x07]*(\x07|\x1b\\\\)")
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

	once  sync.Once
	errMu sync.Mutex
	err   error
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
	go session.readLoop(spec.Emit)
	if spec.InitialInput != "" {
		if _, err := f.Write([]byte(spec.InitialInput)); err != nil {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			_ = f.Close()
			return nil, err
		}
	}
	return session, nil
}

func (s *osJobPTYSession) readLoop(emit func([]byte)) {
	buf := make([]byte, 4096)
	for {
		n, err := s.file.Read(buf)
		if n > 0 && emit != nil {
			emit(append([]byte(nil), buf[:n]...))
		}
		if err != nil {
			if err != io.EOF && !strings.Contains(err.Error(), "input/output error") && !strings.Contains(err.Error(), "file already closed") {
				s.setErr(err)
			}
			return
		}
	}
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
	if readErr := s.getErr(); readErr != nil && err == nil {
		return JobPTYResult{ExitCode: 1, ErrorText: readErr.Error()}
	}
	return JobPTYResult{ExitCode: exitCode}
}

func (s *osJobPTYSession) Close() error {
	var closeErr error
	s.once.Do(func() {
		if s.file != nil {
			closeErr = s.file.Close()
			if closeErr != nil && !strings.Contains(closeErr.Error(), "file already closed") {
				s.setErr(closeErr)
			}
		}
	})
	return closeErr
}

func (s *osJobPTYSession) setErr(err error) {
	if err == nil {
		return
	}
	s.errMu.Lock()
	defer s.errMu.Unlock()
	if s.err == nil {
		s.err = err
	}
}

func (s *osJobPTYSession) getErr() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
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
	return b.String()
}
