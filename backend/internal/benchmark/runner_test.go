package benchmark

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeExecutable(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "llama-bench")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunnerSuccessAndExecutableLock(t *testing.T) {
	path := writeExecutable(t, `printf '[{"n_prompt":1,"avg_ts":2}]'; printf 'diag' >&2`)
	runner := NewRunner(path)
	out, err := runner.Run(context.Background(), []string{path, "--output", "json"})
	if err != nil || !strings.Contains(string(out.MachineOutput), "n_prompt") || out.DiagnosticOutput != "diag" || out.ExitCode != 0 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if _, err := runner.Run(context.Background(), []string{"/bin/echo", "oops"}); err == nil {
		t.Fatal("expected executable mismatch")
	}
}

func TestRunnerBoundsOutputWithoutBreakingChild(t *testing.T) {
	path := writeExecutable(t, `i=0; while [ $i -lt 1000 ]; do printf '0123456789'; printf 'abcdefghij' >&2; i=$((i+1)); done`)
	runner := NewRunner(path)
	runner.machineLimit = 64
	runner.diagnosticLimit = 32
	out, err := runner.Run(context.Background(), []string{path})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.MachineOutput) != 64 || len(out.DiagnosticOutput) != 32 || !out.MachineTruncated || !out.DiagnosticTruncated {
		t.Fatalf("out=%+v", out)
	}
}

func TestRunnerReportsNonZeroExit(t *testing.T) {
	path := writeExecutable(t, `printf 'bad' >&2; exit 7`)
	out, err := NewRunner(path).Run(context.Background(), []string{path})
	if err == nil || out.ExitCode != 7 || out.DiagnosticOutput != "bad" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestRunnerCancellationInterruptsAndReaps(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "interrupted")
	path := writeExecutable(t, `trap 'echo yes > "`+marker+`"; exit 0' INT TERM; while :; do sleep 1; done`)
	runner := NewRunner(path)
	runner.cancelGrace = 500 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := runner.Run(ctx, []string{path})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("interrupt trap was not executed: %v", err)
	}
}

func TestRunnerCancellationForceKills(t *testing.T) {
	path := writeExecutable(t, `trap '' INT TERM; while :; do sleep 1; done`)
	runner := NewRunner(path)
	runner.cancelGrace = 50 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := runner.Run(ctx, []string{path})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("forced cancellation took too long")
	}
}
