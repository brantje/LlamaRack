package lifecycle

import (
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/systemlog"
)

func TestAlwaysOnReconcileDiagnosticFormatting(t *testing.T) {
	systemlog.Default.Reset()
	defer systemlog.Default.Reset()
	s := &Service{}
	s.logAlwaysOnReconcile(1)
	s.logAlwaysOnReconcile(2)
	logs := systemlog.Default.Snapshot(10)
	if len(logs) != 2 || logs[0].Message != "reconcile: 1 Always On Instance satisfied" || logs[1].Message != "reconcile: 2 Always On Instances satisfied" {
		t.Fatalf("reconcile logs=%+v", logs)
	}
}

func TestStartFailureBackoffWarnsOncePerFailure(t *testing.T) {
	systemlog.Default.Reset()
	defer systemlog.Default.Reset()
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	s := &Service{now: func() time.Time { return now }}
	s.recordStartFailure("qwen-coder-ci", "exec: no such file")
	logs := systemlog.Default.Snapshot(10)
	if len(logs) != 1 || logs[0].Level != systemlog.Warn || !strings.Contains(logs[0].Message, "qwen-coder-ci: startup backoff 15s after 1 consecutive start failure") {
		t.Fatalf("backoff diagnostics=%+v", logs)
	}
	s.recordStartFailure("qwen-coder-ci", "still broken")
	logs = systemlog.Default.Snapshot(10)
	if len(logs) != 2 || !strings.Contains(logs[1].Message, "startup backoff 30s after 2 consecutive start failures") {
		t.Fatalf("second backoff diagnostics=%+v", logs)
	}
}
