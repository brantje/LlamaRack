package benchmark

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/buildinfo"
	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/llamaconfig"
	"github.com/brantje/llamarack/backend/internal/llamacpp"
	"github.com/brantje/llamarack/backend/internal/models"
	"github.com/brantje/llamarack/backend/internal/recommendations"
	"github.com/brantje/llamarack/backend/internal/scheduler"
)

type benchmarkTestInstanceSource struct{ item instances.Instance }

func (s *benchmarkTestInstanceSource) Get(context.Context, string) (instances.Instance, error) {
	return s.item, nil
}

type benchmarkTestModelSource struct {
	item       models.Model
	root       string
	inspection models.GGUFInspection
}

func (s *benchmarkTestModelSource) GetByID(context.Context, string) (models.Model, error) {
	return s.item, nil
}
func (s *benchmarkTestModelSource) ModelAbsolutePath(model models.Model) (string, error) {
	return filepath.Join(s.root, filepath.FromSlash(model.GGUFPath)), nil
}
func (s *benchmarkTestModelSource) InspectGGUFArtifact(context.Context, string) (models.GGUFInspection, error) {
	return s.inspection, nil
}

type benchmarkTestConfigSource struct{ effective llamaconfig.Effective }

func (s *benchmarkTestConfigSource) Effective(context.Context, string, string) (llamaconfig.Effective, error) {
	return s.effective, nil
}

type benchmarkTestHardware struct {
	snapshot hardware.Snapshot
	err      error
}

func (s benchmarkTestHardware) Snapshot(context.Context) (hardware.Snapshot, error) {
	return s.snapshot, s.err
}

type benchmarkTestExecutor struct {
	output RunOutput
	err    error
	wait   <-chan struct{}
}

func (e benchmarkTestExecutor) Run(ctx context.Context, _ []string) (RunOutput, error) {
	if e.wait != nil {
		select {
		case <-ctx.Done():
			return RunOutput{}, ctx.Err()
		case <-e.wait:
		}
	}
	return e.output, e.err
}

type benchmarkMemoryStore struct {
	mu           sync.Mutex
	runs         map[string]Run
	terminal     chan struct{}
	terminalOnce sync.Once
}

func newBenchmarkMemoryStore() *benchmarkMemoryStore {
	return &benchmarkMemoryStore{runs: map[string]Run{}, terminal: make(chan struct{})}
}
func (s *benchmarkMemoryStore) CreateRun(_ context.Context, run Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[run.ID] = run
	return nil
}
func (s *benchmarkMemoryStore) GetRun(_ context.Context, id string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[id]
	if !ok {
		return Run{}, ErrNotFound
	}
	return run, nil
}
func (s *benchmarkMemoryStore) ListRuns(_ context.Context, filter Filter) (Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	page := Page{Limit: filter.Limit, Offset: filter.Offset}
	for _, run := range s.runs {
		if filter.Status != "" && run.Status != filter.Status {
			continue
		}
		page.Items = append(page.Items, run)
	}
	page.Total = len(page.Items)
	return page, nil
}
func (s *benchmarkMemoryStore) TransitionRun(_ context.Context, id string, from, to Status, update TransitionUpdate) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[id]
	if !ok {
		return Run{}, ErrNotFound
	}
	if run.Status != from {
		return Run{}, ErrTransitionConflict
	}
	run.Status = to
	if update.StartedAt != nil {
		run.StartedAt = update.StartedAt
	}
	if update.CompletedAt != nil {
		run.CompletedAt = update.CompletedAt
	}
	run.Failure = update.Failure
	run.DiagnosticOutput = update.DiagnosticOutput
	s.runs[id] = run
	if to.Terminal() {
		s.terminalOnce.Do(func() { close(s.terminal) })
	}
	return run, nil
}
func (s *benchmarkMemoryStore) CompleteRun(_ context.Context, id string, completion Completion, results []Result) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[id]
	if run.Status != StatusRunning {
		return Run{}, ErrTransitionConflict
	}
	run.Status = StatusCompleted
	run.CompletedAt = &completion.CompletedAt
	run.DiagnosticOutput = completion.DiagnosticOutput
	run.Results = append([]Result(nil), results...)
	s.runs[id] = run
	s.terminalOnce.Do(func() { close(s.terminal) })
	return run, nil
}
func (s *benchmarkMemoryStore) DeleteRun(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.runs, id)
	return nil
}

func testBenchmarkService(t *testing.T, executor benchmarkExecutor) (*Service, *benchmarkMemoryStore, *scheduler.Ledger, *benchmarkTestInstanceSource, *benchmarkTestConfigSource) {
	t.Helper()
	root := t.TempDir()
	modelPath := filepath.Join(root, "model.gguf")
	if err := os.WriteFile(modelPath, []byte("model-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	inst := &benchmarkTestInstanceSource{item: instances.Instance{ID: "instance-1", Slug: "coder", ModelID: "model-1", Name: "Coder", GPUMode: "auto"}}
	model := &benchmarkTestModelSource{
		item: models.Model{ID: "model-1", Slug: "model", Name: "Model", GGUFPath: "model.gguf", TotalBytes: 11, Quantization: "Q4_K_M"},
		root: root,
		inspection: models.GGUFInspection{ID: "model.gguf", Name: "model.gguf", ModelBytes: 11, ShardCount: 1, ExpectedShards: 1, Complete: true, Files: []models.GGUFArtifactFile{{Path: "model.gguf", Size: 11}}},
	}
	cfg := &benchmarkTestConfigSource{effective: llamaconfig.Effective{Values: map[string]string{"n-gpu-layers": "0"}, Sources: map[string]string{"n-gpu-layers": "instance"}}}
	store := newBenchmarkMemoryStore()
	ledger := scheduler.NewLedger()
	s := &Service{
		root: context.Background(), store: store, instances: inst, models: model, config: cfg,
		hardware: benchmarkTestHardware{snapshot: hardware.Snapshot{RAMTotalBytes: 2 << 30, RAMAvailableBytes: 2 << 30}},
		reservations: ledger, executor: executor, binaryPath: "/app/llama-bench",
		discoverCapabilities: func(context.Context, string) (Capabilities, error) {
			return testCapabilities(llamacpp.Option{Key: "n-gpu-layers", Kind: "integer"}), nil
		},
		readMetadata: func(string) (recommendations.Metadata, error) { return recommendations.Metadata{}, nil },
		buildIdentity: func() buildinfo.Identity {
			return buildinfo.Identity{Version: "test", Commit: "abc", Variant: "cpu"}
		},
		newID: func() (string, error) { return "run-1", nil }, now: time.Now, active: map[string]context.CancelFunc{},
	}
	return s, store, ledger, inst, cfg
}

func waitBenchmarkReservationReleased(t *testing.T, ledger *scheduler.Ledger, runID string) {
	t.Helper()
	owner := scheduler.ResourceOwner{Kind: scheduler.ResourceOwnerBenchmark, ID: runID}
	deadline := time.Now().Add(time.Second)
	for {
		if _, ok := ledger.GetByOwner(owner); !ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("benchmark reservation leaked")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestBenchmarkServiceCompletesAndKeepsImmutableSnapshot(t *testing.T) {
	executor := benchmarkTestExecutor{output: RunOutput{MachineOutput: []byte(`[{"n_prompt":512,"avg_ts":123.4}]`)}}
	s, store, ledger, inst, cfg := testBenchmarkService(t, executor)
	run, err := s.Create(context.Background(), "instance-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	inst.item.Name = "Renamed"
	cfg.effective.Values["n-gpu-layers"] = "99"
	select {
	case <-store.terminal:
	case <-time.After(2 * time.Second):
		t.Fatal("benchmark did not finish")
	}
	got, err := store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusCompleted || got.InstanceNameSnapshot != "Coder" || got.InstanceConfig.Options["n-gpu-layers"] != "0" || len(got.Results) != 1 {
		t.Fatalf("run=%+v", got)
	}
	waitBenchmarkReservationReleased(t, ledger, run.ID)
}

func TestBenchmarkServiceCancellationReleasesReservation(t *testing.T) {
	wait := make(chan struct{})
	s, store, ledger, _, _ := testBenchmarkService(t, benchmarkTestExecutor{wait: wait})
	run, err := s.Create(context.Background(), "instance-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		got, _ := store.GetRun(context.Background(), run.ID)
		if got.Status == StatusRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("did not start")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := s.Cancel(context.Background(), run.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.terminal:
	case <-time.After(2 * time.Second):
		t.Fatal("benchmark did not cancel")
	}
	got, _ := store.GetRun(context.Background(), run.ID)
	if got.Status != StatusCancelled {
		t.Fatalf("status=%s", got.Status)
	}
	waitBenchmarkReservationReleased(t, ledger, run.ID)
}

func TestBenchmarkReconcileInterrupted(t *testing.T) {
	s, store, _, _, _ := testBenchmarkService(t, benchmarkTestExecutor{})
	now := time.Now().UTC()
	store.runs["running"] = Run{ID: "running", Status: StatusRunning, CreatedAt: now}
	store.runs["queued"] = Run{ID: "queued", Status: StatusQueued, CreatedAt: now}
	if err := s.ReconcileInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	running, _ := store.GetRun(context.Background(), "running")
	queued, _ := store.GetRun(context.Background(), "queued")
	if running.Status != StatusFailed || queued.Status != StatusCancelled {
		t.Fatalf("running=%s queued=%s", running.Status, queued.Status)
	}
}

func TestCaptureArtifactFingerprintChangesWithBytes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "model.gguf")
	if err := os.WriteFile(path, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := &benchmarkTestModelSource{root: root, item: models.Model{GGUFPath: "model.gguf"}, inspection: models.GGUFInspection{ModelBytes: 5, ShardCount: 1, ExpectedShards: 1, Complete: true, Files: []models.GGUFArtifactFile{{Path: "model.gguf", Size: 5}}}}
	first, err := captureArtifact(context.Background(), source, source.item)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := captureArtifact(context.Background(), source, source.item)
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint == second.Fingerprint {
		t.Fatal("fingerprint did not change")
	}
}

func TestBenchmarkCreateDoesNotDisplaceRunningInstanceLease(t *testing.T) {
	s, _, ledger, _, cfg := testBenchmarkService(t, benchmarkTestExecutor{})
	modelSource := s.models.(*benchmarkTestModelSource)
	modelSource.inspection.ModelBytes = 1 << 30
	cfg.effective = llamaconfig.Effective{Values: map[string]string{}, Sources: map[string]string{}}
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", TotalBytes: 2 << 30, FreeBytes: 2 << 30}}, RAMTotalBytes: 4 << 30, RAMAvailableBytes: 4 << 30}
	s.hardware = benchmarkTestHardware{snapshot: snapshot}
	worker, err := ledger.Acquire(scheduler.AcquireRequest{InstanceID: "instance-1", Snapshot: snapshot, Placement: scheduler.PlacementRequest{RequiredBytes: 1 << 30, Mode: "auto"}})
	if err != nil || !worker.Placement.Fits {
		t.Fatalf("worker lease=%+v err=%v", worker, err)
	}
	if err := ledger.Commit(worker.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(context.Background(), "instance-1", nil); !errors.Is(err, ErrInsufficientResources) {
		t.Fatalf("create err=%v", err)
	}
	got, ok := ledger.GetByInstance("instance-1")
	if !ok || got.ID != worker.ID {
		t.Fatalf("running Instance reservation changed: got=%+v ok=%v want=%+v", got, ok, worker)
	}
}

func TestBenchmarkDeleteRequiresTerminalState(t *testing.T) {
	s, store, _, _, _ := testBenchmarkService(t, benchmarkTestExecutor{})
	store.runs["queued"] = Run{ID: "queued", Status: StatusQueued}
	if err := s.Delete(context.Background(), "queued"); !errors.Is(err, ErrTransitionConflict) {
		t.Fatalf("err=%v", err)
	}
}
