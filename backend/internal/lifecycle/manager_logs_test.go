package lifecycle

import (
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/systemlog"
)

func TestAlwaysOnReconcileDiagnosticGrammar(t *testing.T) {
	systemlog.Default.Reset()
	s := &Service{}
	s.logAlwaysOnReconcile(1)
	s.logAlwaysOnReconcile(2)
	entries := systemlog.Default.Snapshot(10)
	if len(entries) != 2 {
		t.Fatalf("entries=%v", entries)
	}
	if entries[0].Message != "reconcile: 1 Always On Instance satisfied" {
		t.Fatalf("singular=%q", entries[0].Message)
	}
	if entries[1].Message != "reconcile: 2 Always On Instances satisfied" {
		t.Fatalf("plural=%q", entries[1].Message)
	}
}

func TestStartFailureBackoffAndReset(t *testing.T) {
	systemlog.Default.Reset()
	oldBackoff := startFailureBackoffFor
	startFailureBackoffFor = 5 * time.Millisecond
	defer func() { startFailureBackoffFor = oldBackoff }()

	s := &Service{manuallyStopped: map[string]bool{}}
	s.noteStartFailure("worker-a")
	s.noteStartFailure("worker-a")
	if s.isManuallyStopped("worker-a") {
		t.Fatal("backoff started before third failure")
	}
	s.noteStartFailure("worker-a")
	if !s.isManuallyStopped("worker-a") {
		t.Fatal("third failure should engage backoff")
	}
	// Repeated failures while backing off must not create another timer/message.
	s.noteStartFailure("worker-a")
	entries := systemlog.Default.Snapshot(10)
	if len(entries) != 1 || entries[0].Level != systemlog.Warn || !strings.Contains(entries[0].Message, "3 consecutive start failures, backing off 60s") {
		t.Fatalf("backoff entries=%v", entries)
	}

	deadline := time.Now().Add(250 * time.Millisecond)
	for s.isManuallyStopped("worker-a") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.isManuallyStopped("worker-a") {
		t.Fatal("automatic backoff did not clear")
	}

	s.noteStartFailure("worker-b")
	s.noteStartFailure("worker-b")
	s.clearStartFailures("worker-b")
	s.noteStartFailure("worker-b")
	if s.isManuallyStopped("worker-b") {
		t.Fatal("successful start should reset consecutive failure count")
	}

	s.noteStartFailure("worker-c")
	s.noteStartFailure("worker-c")
	s.noteStartFailure("worker-c")
	if !s.isManuallyStopped("worker-c") {
		t.Fatal("expected worker-c backoff")
	}
	s.cancelStartFailureBackoff("worker-c")
	s.clearManualStop("worker-c")
	if s.isManuallyStopped("worker-c") {
		t.Fatal("cancelled backoff should permit explicit lifecycle action")
	}
}

func TestManagerLogDefensiveBranches(t *testing.T) {
	var nilService *Service
	nilService.AddManagerLog("worker", "line")

	s := &Service{}
	s.AddManagerLog("worker", "line") // nil supervisor is safe
	s.clearStartFailures("missing")
	s.cancelStartFailureBackoff("missing")
	s.logReleasedEstimate("missing", false)
}
