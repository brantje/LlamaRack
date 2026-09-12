package benchmark

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	defaultMachineOutputLimit = int64(8 * 1024 * 1024)
	defaultDiagnosticLimit    = int64(64 * 1024)
	defaultCancelGrace        = 2 * time.Second
)

type RunOutput struct {
	MachineOutput       []byte
	DiagnosticOutput    string
	ExitCode            int
	MachineTruncated    bool
	DiagnosticTruncated bool
}

type Runner struct {
	binaryPath      string
	machineLimit    int64
	diagnosticLimit int64
	cancelGrace     time.Duration
}

func NewRunner(binaryPath string) *Runner {
	return &Runner{
		binaryPath:      strings.TrimSpace(binaryPath),
		machineLimit:    defaultMachineOutputLimit,
		diagnosticLimit: defaultDiagnosticLimit,
		cancelGrace:     defaultCancelGrace,
	}
}

func (r *Runner) Run(ctx context.Context, argv []string) (RunOutput, error) {
	if r == nil || strings.TrimSpace(r.binaryPath) == "" {
		return RunOutput{}, errors.New("benchmark runner executable is not configured")
	}
	if len(argv) == 0 {
		return RunOutput{}, errors.New("benchmark argv is empty")
	}
	if !sameExecutable(argv[0], r.binaryPath) {
		return RunOutput{}, fmt.Errorf("benchmark argv executable %q does not match configured executable", argv[0])
	}
	if err := ctx.Err(); err != nil {
		return RunOutput{}, err
	}

	stdout := newBoundedCapture(r.machineLimit)
	stderr := newBoundedCapture(r.diagnosticLimit)
	cmd := exec.Command(r.binaryPath, argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return captureRunOutput(stdout, stderr, -1), fmt.Errorf("start llama-bench: %w", err)
	}

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	select {
	case err := <-waitCh:
		out := captureRunOutput(stdout, stderr, exitCode(err))
		if err != nil {
			return out, fmt.Errorf("llama-bench exited with code %d: %w", out.ExitCode, err)
		}
		return out, nil
	case <-ctx.Done():
		signalProcessGroup(cmd, syscall.SIGINT)
		timer := time.NewTimer(r.cancelGrace)
		defer timer.Stop()
		select {
		case <-waitCh:
		case <-timer.C:
			signalProcessGroup(cmd, syscall.SIGKILL)
			<-waitCh
		}
		out := captureRunOutput(stdout, stderr, -1)
		if cmd.ProcessState != nil {
			out.ExitCode = cmd.ProcessState.ExitCode()
		}
		return out, ctx.Err()
	}
}

func signalProcessGroup(cmd *exec.Cmd, signal syscall.Signal) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, signal); err != nil {
		if signal == syscall.SIGKILL {
			_ = cmd.Process.Kill()
		} else {
			_ = cmd.Process.Signal(os.Interrupt)
		}
	}
}

func sameExecutable(got, want string) bool {
	got = strings.TrimSpace(got)
	want = strings.TrimSpace(want)
	if got == "" || want == "" {
		return false
	}
	gotAbs, gotErr := filepath.Abs(got)
	wantAbs, wantErr := filepath.Abs(want)
	if gotErr == nil && wantErr == nil {
		return filepath.Clean(gotAbs) == filepath.Clean(wantAbs)
	}
	return filepath.Clean(got) == filepath.Clean(want)
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func captureRunOutput(stdout, stderr *boundedCapture, exit int) RunOutput {
	return RunOutput{
		MachineOutput:       stdout.Bytes(),
		DiagnosticOutput:    string(stderr.Bytes()),
		ExitCode:            exit,
		MachineTruncated:    stdout.Truncated(),
		DiagnosticTruncated: stderr.Truncated(),
	}
}

type boundedCapture struct {
	mu        sync.Mutex
	limit     int64
	data      []byte
	truncated bool
}

func newBoundedCapture(limit int64) *boundedCapture {
	if limit < 0 {
		limit = 0
	}
	return &boundedCapture{limit: limit}
}

func (b *boundedCapture) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - int64(len(b.data))
	if remaining > 0 {
		take := int64(len(p))
		if take > remaining {
			take = remaining
		}
		b.data = append(b.data, p[:int(take)]...)
	}
	if int64(len(p)) > remaining {
		b.truncated = true
	}
	return len(p), nil
}

func (b *boundedCapture) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.data...)
}

func (b *boundedCapture) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

var _ io.Writer = (*boundedCapture)(nil)
