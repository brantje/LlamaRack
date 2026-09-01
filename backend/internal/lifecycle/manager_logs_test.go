package lifecycle

import (
	"testing"

	"github.com/brantje/llamarack/backend/internal/systemlog"
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

func TestManagerLogDefensiveBranches(t *testing.T) {
	var nilService *Service
	nilService.AddManagerLog("worker", "line")

	s := &Service{}
	s.AddManagerLog("worker", "line") // nil supervisor is safe
	s.resetStartFailures("missing")
	s.overrideStartBackoff("missing")
	s.logReleasedEstimate("missing", false)
}

func TestManagerLogReadyClearsBackoffWhileStopAndKillPreserveIt(t *testing.T) {
	s, _, _, sup, _ := setupLifecycle(t, true, false)
	s.recordStartFailure("worker-a", "boom")
	s.sup = sup
	s.AddManagerLog("worker-a", "worker ready after 12ms")
	if _, ok := s.startFailureState("worker-a"); ok {
		t.Fatal("ready log did not reset failure state")
	}
	s.recordStartFailure("worker-b", "boom")
	s.AddManagerLog("worker-b", "worker stopped")
	if _, ok := s.startFailureState("worker-b"); !ok {
		t.Fatal("stop log reset failure state")
	}
	s.recordStartFailure("worker-c", "boom")
	s.AddManagerLog("worker-c", "worker killed")
	if _, ok := s.startFailureState("worker-c"); !ok {
		t.Fatal("kill log reset failure state")
	}
}
