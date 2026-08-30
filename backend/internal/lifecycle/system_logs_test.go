package lifecycle

import (
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/systemlog"
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

func TestThreeStartFailuresBackOffForExactDurationAndReset(t *testing.T) {
	systemlog.Default.Reset()
	defer systemlog.Default.Reset()
	oldDuration := startFailureBackoffFor
	startFailureBackoffFor = 10 * time.Millisecond
	defer func() { startFailureBackoffFor = oldDuration }()

	s := &Service{manuallyStopped: map[string]bool{}}
	key := failureBackoffKey{service: s, id: "qwen-coder-ci"}
	failureBackoffMu.Lock()
	delete(failureBackoffs, key)
	failureBackoffMu.Unlock()
	defer func() {
		failureBackoffMu.Lock()
		delete(failureBackoffs, key)
		failureBackoffMu.Unlock()
	}()

	s.noteStartFailure("qwen-coder-ci")
	s.noteStartFailure("qwen-coder-ci")
	if s.isManuallyStopped("qwen-coder-ci") {
		t.Fatal("backoff engaged before the third failure")
	}
	s.noteStartFailure("qwen-coder-ci")
	if !s.isManuallyStopped("qwen-coder-ci") {
		t.Fatal("third failure did not engage backoff")
	}
	logs := systemlog.Default.Snapshot(10)
	if len(logs) != 1 || logs[0].Level != systemlog.Warn || !strings.Contains(logs[0].Message, "3 consecutive start failures, backing off 0s") {
		t.Fatalf("backoff diagnostics=%+v", logs)
	}

	time.Sleep(30 * time.Millisecond)
	if s.isManuallyStopped("qwen-coder-ci") {
		t.Fatal("temporary backoff did not release")
	}
}

func TestCancelStartFailureBackoffResetsFailureCount(t *testing.T) {
	s := &Service{manuallyStopped: map[string]bool{}}
	key := failureBackoffKey{service: s, id: "one"}
	failureBackoffMu.Lock()
	failureBackoffs[key] = &failureBackoffState{failures: 2}
	failureBackoffMu.Unlock()
	s.cancelStartFailureBackoff("one")
	failureBackoffMu.Lock()
	_, exists := failureBackoffs[key]
	failureBackoffMu.Unlock()
	if exists {
		t.Fatal("cancelled backoff retained stale failure state")
	}
}
