package benchmark

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/scheduler"
)

type failingBenchmarkStore struct {
	Store
	createErr, listErr, transitionErr, completeErr error
}

func (s failingBenchmarkStore) CreateRun(ctx context.Context, run Run) error {
	if s.createErr != nil {
		return s.createErr
	}
	return s.Store.CreateRun(ctx, run)
}
func (s failingBenchmarkStore) ListRuns(ctx context.Context, filter Filter) (Page, error) {
	if s.listErr != nil {
		return Page{}, s.listErr
	}
	return s.Store.ListRuns(ctx, filter)
}
func (s failingBenchmarkStore) TransitionRun(ctx context.Context, id string, from, to Status, update TransitionUpdate) (Run, error) {
	if s.transitionErr != nil {
		return Run{}, s.transitionErr
	}
	return s.Store.TransitionRun(ctx, id, from, to, update)
}
func (s failingBenchmarkStore) CompleteRun(ctx context.Context, id string, completion Completion, results []Result) (Run, error) {
	if s.completeErr != nil {
		return Run{}, s.completeErr
	}
	return s.Store.CompleteRun(ctx, id, completion, results)
}

func TestServiceCreateFailuresNeverLeakReservations(t *testing.T) {
	boom := errors.New("fixture failure")
	for _, tc := range []struct {
		name     string
		setup    func(*Service)
		workload *WorkloadProfile
		want     error
	}{
		{name: "unconfigured", setup: func(s *Service) { s.executor = nil }},
		{name: "workload", workload: &WorkloadProfile{Repetitions: 101}, want: ErrInvalidWorkload},
		{name: "context", setup: func(s *Service) { s.config.(*benchmarkTestConfigSource).effective.Values["ctx-size"] = "8" }, want: ErrInvalidWorkload},
		{name: "capability error", setup: func(s *Service) {
			s.discoverCapabilities = func(context.Context, string) (Capabilities, error) { return Capabilities{}, boom }
		}, want: boom},
		{name: "unavailable", setup: func(s *Service) {
			s.discoverCapabilities = func(context.Context, string) (Capabilities, error) { return Capabilities{Reason: "missing"}, nil }
		}, want: ErrUnavailable},
		{name: "thread resolution", setup: func(s *Service) { s.config.(*benchmarkTestConfigSource).effective.Values["threads"] = "invalid" }, want: ErrUnsupportedConfig},
		{name: "hardware", setup: func(s *Service) { s.hardware = benchmarkTestHardware{err: boom} }, want: boom},
		{name: "id", setup: func(s *Service) { s.newID = func() (string, error) { return "", boom } }, want: boom},
		{name: "placement", setup: func(s *Service) { s.instances.(*benchmarkTestInstanceSource).item.GPUMode = "invalid" }},
		{name: "unsupported config", setup: func(s *Service) {
			s.config.(*benchmarkTestConfigSource).effective.Values["mmproj-unsupported"] = "x"
			s.config.(*benchmarkTestConfigSource).effective.Sources["mmproj-unsupported"] = "instance"
		}, want: ErrUnsupportedConfig},
		{name: "executable", setup: func(s *Service) { s.binaryPath = "" }, want: ErrUnsupportedConfig},
		{name: "warmup", workload: &WorkloadProfile{PromptTokens: []int{1}, Repetitions: 1}, want: ErrInvalidWorkload},
		{name: "store", setup: func(s *Service) { s.store = failingBenchmarkStore{Store: s.store, createErr: boom} }, want: boom},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, store, ledger, _, _ := testBenchmarkService(t, benchmarkTestExecutor{})
			if tc.setup != nil {
				tc.setup(s)
			}
			_, err := s.Create(context.Background(), "instance-1", tc.workload)
			if err == nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("error=%v want=%v", err, tc.want)
			}
			if _, ok := ledger.GetByOwner(scheduler.ResourceOwner{Kind: scheduler.ResourceOwnerBenchmark, ID: "run-1"}); ok {
				t.Fatal("failed creation leaked reservation")
			}
			if len(store.runs) != 0 {
				t.Fatal("failed creation persisted a run")
			}
		})
	}
	var absent *Service
	if _, err := absent.Create(context.Background(), "x", nil); err == nil {
		t.Fatal("nil service accepted create")
	}
}

func TestServiceExecutionFailureStates(t *testing.T) {
	for _, tc := range []struct {
		name    string
		output  RunOutput
		err     error
		persist bool
		want    string
	}{
		{name: "process", err: errors.New("process\n failed"), want: "process failed"},
		{name: "truncated", output: RunOutput{MachineTruncated: true}, want: "exceeded the manager limit"},
		{name: "malformed", output: RunOutput{MachineOutput: []byte("bad json")}, want: "benchmark parser"},
		{name: "persistence", output: RunOutput{MachineOutput: []byte(`[{"n_prompt":1,"avg_ts":1}]`)}, persist: true, want: "persist benchmark results"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.output.DiagnosticOutput = " tool diagnostic "
			tc.output.DiagnosticTruncated = true
			s, store, ledger, _, _ := testBenchmarkService(t, benchmarkTestExecutor{output: tc.output, err: tc.err})
			if tc.persist {
				s.store = failingBenchmarkStore{Store: store, completeErr: errors.New("disk full")}
			}
			run, err := s.Create(context.Background(), "instance-1", nil)
			if err != nil {
				t.Fatal(err)
			}
			s.wg.Wait()
			got, err := s.Get(context.Background(), run.ID)
			if err != nil || got.Status != StatusFailed || !strings.Contains(got.Failure, tc.want) || got.CompletedAt == nil || len(got.Results) != 0 {
				t.Fatalf("run=%+v err=%v", got, err)
			}
			if got.DiagnosticOutput != "tool diagnostic\n[diagnostic output truncated]" {
				t.Fatalf("diagnostic=%q", got.DiagnosticOutput)
			}
			waitBenchmarkReservationReleased(t, ledger, run.ID)
		})
	}
	if boundedDiagnostic(RunOutput{DiagnosticTruncated: true}) != "[diagnostic output truncated]" {
		t.Fatal("missing truncation marker")
	}
	if sanitizeFailure(nil) != "" || len(sanitizeFailure(errors.New(strings.Repeat("x", 4096)))) > 2051 {
		t.Fatal("failure text not bounded")
	}
}

func TestServiceQueuedCancellationAndPublicHistory(t *testing.T) {
	s, store, ledger, _, _ := testBenchmarkService(t, benchmarkTestExecutor{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.root = ctx
	run, err := s.Create(context.Background(), "instance-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.wg.Wait()
	got, err := s.Cancel(context.Background(), run.ID)
	if err != nil || got.Status != StatusCancelled {
		t.Fatalf("run=%+v err=%v", got, err)
	}
	waitBenchmarkReservationReleased(t, ledger, run.ID)
	page, err := s.List(context.Background(), Filter{})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if err := s.Delete(context.Background(), run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cancel(context.Background(), run.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cancel err=%v", err)
	}
	if err := s.Delete(context.Background(), run.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete err=%v", err)
	}
	store.runs["orphan"] = Run{ID: "orphan", Status: StatusQueued}
	got, err = s.Cancel(context.Background(), "orphan")
	if err != nil || got.Status != StatusCancelled {
		t.Fatalf("orphan=%+v err=%v", got, err)
	}
}

func TestServiceCapabilitiesDefaultsAndRetry(t *testing.T) {
	s := NewService(nil, nil, nil, nil, nil, nil, nil, " bench ")
	if s.root == nil || s.hardware == nil || s.reservations == nil || s.executor == nil || s.binaryPath != "bench" {
		t.Fatalf("defaults=%+v", s)
	}
	calls := 0
	s.discoverCapabilities = func(context.Context, string) (Capabilities, error) {
		calls++
		return Capabilities{Available: calls > 1, Reason: "retry"}, nil
	}
	for i := 0; i < 3; i++ {
		if _, err := s.Capabilities(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("discovery calls=%d", calls)
	}
	var absent *Service
	caps, err := absent.Capabilities(context.Background())
	if err != nil || caps.Available || caps.Reason == "" {
		t.Fatalf("caps=%+v err=%v", caps, err)
	}
}

func TestServiceShutdownCancelsAndHonorsDeadline(t *testing.T) {
	s, store, _, _, _ := testBenchmarkService(t, benchmarkTestExecutor{wait: make(chan struct{})})
	run, err := s.Create(context.Background(), "instance-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetRun(context.Background(), run.ID)
	if got.Status != StatusCancelled {
		t.Fatalf("status=%v", got.Status)
	}
	s.wg.Add(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Shutdown(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown err=%v", err)
	}
	s.wg.Done()
}

func TestServiceReconciliationFailuresAndTransitions(t *testing.T) {
	for _, failure := range []error{errors.New("database unavailable"), ErrTransitionConflict} {
		s, store, _, _, _ := testBenchmarkService(t, benchmarkTestExecutor{})
		store.runs["old"] = Run{ID: "old", Status: StatusRunning}
		s.store = failingBenchmarkStore{Store: store, transitionErr: failure}
		done := make(chan error, 1)
		go func() { done <- s.ReconcileInterrupted(context.Background()) }()
		select {
		case err := <-done:
			if errors.Is(failure, ErrTransitionConflict) {
				if err != nil {
					t.Fatalf("conflict reconcile err=%v", err)
				}
			} else if !errors.Is(err, failure) {
				t.Fatalf("err=%v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("ReconcileInterrupted did not return")
		}
		// The runner must release ownership even if it cannot transition to RUNNING.
		s.wg.Add(1)
		s.runJob(context.Background(), "old", nil, DefaultWorkload(), scheduler.ResourceOwner{Kind: scheduler.ResourceOwnerBenchmark, ID: "old"})
		if store.runs["old"].Status != StatusRunning {
			t.Fatal("unexpected transition")
		}
	}
	s, _, _, _, _ := testBenchmarkService(t, benchmarkTestExecutor{})
	s.store = failingBenchmarkStore{Store: s.store, listErr: errors.New("list failure")}
	if err := s.ReconcileInterrupted(context.Background()); err == nil {
		t.Fatal("list failure ignored")
	}
	// Demand includes selected dependency bytes even when main metadata is absent.
	target := capturedTarget{Artifact: ArtifactSnapshot{Size: 1, Dependencies: []ArtifactDependencySnapshot{{Files: []ArtifactFileSnapshot{{Size: 2}}}}}, Config: InstanceConfigSnapshot{Options: map[string]string{"n-gpu-layers": "0"}}}
	demand := s.estimateDemand(target)
	if demand.HostRAMBytes < 3 {
		t.Fatalf("demand=%+v", demand)
	}
	target.Artifact = ArtifactSnapshot{}
	target.Model.TotalBytes = 10
	if s.estimateDemand(target).HostRAMBytes < 10 {
		t.Fatal("missing registered weight fallback")
	}
}
