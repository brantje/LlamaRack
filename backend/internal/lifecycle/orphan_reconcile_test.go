package lifecycle

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

type countingHardware struct {
	calls atomic.Int32
	inner hardware.Snapshotter
}

func (h *countingHardware) Snapshot(ctx context.Context) (hardware.Snapshot, error) {
	h.calls.Add(1)
	if h.inner != nil {
		return h.inner.Snapshot(ctx)
	}
	return hardware.Snapshot{}, nil
}

func setupIdentity(t *testing.T, s *Service, sup *supervisor.Supervisor) (supervisor.RuntimeStore, string) {
	t.Helper()
	ctx := context.Background()
	id, err := supervisor.EnsureInstallationID(ctx, s.models.DB())
	if err != nil {
		t.Fatal(err)
	}
	store := supervisor.NewSQLStore(s.models.DB())
	sup.SetRuntimeIdentity(id, store)
	return store, id
}

func TestStartupReconcileGateBlocksAlwaysOnAndAutoload(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	s, _, m, sup, _ := setupLifecycle(t, true, true)
	id := instanceID(t, s, m.ID)
	s.ArmStartupReconcile()

	alwaysOnDone := make(chan struct{})
	go func() {
		s.ReconcileAlwaysOn(ctx)
		close(alwaysOnDone)
	}()
	autoloadErr := make(chan error, 1)
	go func() {
		_, err := s.EnsureReady(ctx, id)
		autoloadErr <- err
	}()

	time.Sleep(150 * time.Millisecond)
	select {
	case <-alwaysOnDone:
		t.Fatal("Always-On reconciliation returned before startup cleanup")
	default:
	}
	select {
	case err := <-autoloadErr:
		t.Fatalf("autoload returned before startup cleanup: %v", err)
	default:
	}
	if sup.Status(id).PID != 0 {
		t.Fatal("replacement worker started before startup reconciliation")
	}

	if err := s.ReconcileStaleWorkers(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-alwaysOnDone:
	case <-ctx.Done():
		t.Fatal("Always-On did not resume after startup reconciliation")
	}
	select {
	case err := <-autoloadErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("autoload did not resume after startup reconciliation")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if sup.Status(id).State == supervisor.Ready {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("worker did not become ready: %+v", sup.Status(id))
}

func TestStartupReconcileRefreshesHardwareBeforePlacement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	s, _, m, _, _ := setupLifecycle(t, true, false)
	id := instanceID(t, s, m.ID)
	hw := &countingHardware{inner: abundantSingleGPUHardware()}
	s.hardware = hw
	s.ArmStartupReconcile()
	if err := s.ReconcileStaleWorkers(ctx); err != nil {
		t.Fatal(err)
	}
	afterCleanup := hw.calls.Load()
	if afterCleanup < 1 {
		t.Fatal("hardware was not refreshed after stale-worker cleanup")
	}
	if _, err := s.StartInstance(ctx, id); err != nil {
		t.Fatal(err)
	}
	if hw.calls.Load() <= afterCleanup {
		t.Fatal("placement did not snapshot hardware after cleanup")
	}
}

func TestOrphanCleanupFailureDoesNotDoubleStart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, _, m, sup, _ := setupLifecycle(t, true, true)
	id := instanceID(t, s, m.ID)
	store := supervisor.NewMemoryStore()
	sup.SetRuntimeIdentity("install-1", store)
	proc := supervisor.Proc{
		PID:        77,
		StartTicks: 3,
		Environ: map[string]string{
			supervisor.EnvInstallationID:   "install-1",
			supervisor.EnvInstanceID:       id,
			supervisor.EnvWorkerGeneration: "stale",
			supervisor.EnvWorkerPort:       "10001",
		},
	}
	scanner := newBlockingScanner(proc)
	sup.SetProcScanner(scanner)
	if err := store.Upsert(ctx, supervisor.WorkerRecord{InstanceID: id, Generation: "stale", PID: 77, StartTicks: 3, Port: 10001}); err != nil {
		t.Fatal(err)
	}
	s.ArmStartupReconcile()
	if err := s.ReconcileStaleWorkers(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := s.StartInstance(ctx, id)
	if err == nil || !errors.Is(err, errOrphanCleanup) {
		t.Fatalf("start err=%v", err)
	}
	if sup.Status(id).PID != 0 {
		t.Fatal("blocked instance started a replacement worker")
	}
	s.ReconcileAlwaysOn(ctx)
	time.Sleep(50 * time.Millisecond)
	if sup.Status(id).PID != 0 {
		t.Fatal("Always-On started a duplicate worker after cleanup failure")
	}
}

func TestManagerRestartRemovesOwnedWorkerAndStartsOneReplacement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, ms, m, sup, _ := setupLifecycle(t, true, true)
	id := instanceID(t, s, m.ID)
	store, installID := setupIdentity(t, s, sup)
	s.hardware = abundantSingleGPUHardware()
	if _, err := s.StartInstance(ctx, id); err != nil {
		t.Fatal(err)
	}
	old := sup.Status(id)
	if old.State != supervisor.Ready || old.PID == 0 {
		t.Fatalf("original runtime=%+v", old)
	}

	restartedSup := supervisor.New(lifecycleFakeBinary(t), "127.0.0.1", old.Port, 5*time.Second)
	restartedSup.SetRuntimeIdentity(installID, store)
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		restartedSup.Shutdown(stopCtx)
	})
	restarted := New(ms, restartedSup)
	restarted.hardware = abundantSingleGPUHardware()
	restarted.ArmStartupReconcile()
	if err := restarted.ReconcileStaleWorkers(ctx); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(old.PID, 0); err == nil {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) && syscall.Kill(old.PID, 0) == nil {
			time.Sleep(20 * time.Millisecond)
		}
		if err := syscall.Kill(old.PID, 0); err == nil {
			t.Fatalf("stale pid %d still alive", old.PID)
		}
	}
	restarted.ReconcileAlwaysOn(ctx)
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		st := restartedSup.Status(id)
		if st.State == supervisor.Ready && st.PID != 0 && st.PID != old.PID {
			if syscall.Kill(old.PID, 0) == nil {
				t.Fatal("original worker still running beside replacement")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("replacement runtime=%+v", restartedSup.Status(id))
}

type blockingScanner struct {
	proc supervisor.Proc
}

func newBlockingScanner(proc supervisor.Proc) *blockingScanner {
	return &blockingScanner{proc: proc}
}

func (s *blockingScanner) List() ([]supervisor.Proc, error) {
	return []supervisor.Proc{s.proc}, nil
}
func (s *blockingScanner) Inspect(pid int) (supervisor.Proc, error) {
	if pid != s.proc.PID {
		return supervisor.Proc{}, os.ErrNotExist
	}
	return s.proc, nil
}
func (s *blockingScanner) Signal(int, syscall.Signal) error {
	return errors.New("refusing to kill owned worker")
}
func (s *blockingScanner) Alive(pid int, startTicks uint64) bool {
	return pid == s.proc.PID && (startTicks == 0 || startTicks == s.proc.StartTicks)
}

func TestWaitStartupReadyCanceledAndHardwareRefreshError(t *testing.T) {
	ctx := context.Background()
	s, _, m, _, _ := setupLifecycle(t, true, false)
	id := instanceID(t, s, m.ID)
	s.ArmStartupReconcile()
	s.ArmStartupReconcile()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.waitStartupReady(canceled); err == nil {
		t.Fatal("canceled wait should fail")
	}
	s.hardware = &sequenceHardware{err: errors.New("probe down")}
	if err := s.ReconcileStaleWorkers(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartInstance(ctx, id); err != nil {
		t.Fatal(err)
	}
}

func TestOrphanBlockClearedAfterSuccessfulReconcile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	s, _, m, sup, _ := setupLifecycle(t, true, false)
	id := instanceID(t, s, m.ID)
	store := supervisor.NewMemoryStore()
	sup.SetRuntimeIdentity("install-1", store)
	proc := supervisor.Proc{
		PID:        78,
		StartTicks: 4,
		Environ: map[string]string{
			supervisor.EnvInstallationID:   "install-1",
			supervisor.EnvInstanceID:       id,
			supervisor.EnvWorkerGeneration: "stale",
			supervisor.EnvWorkerPort:       "10001",
		},
	}
	sup.SetProcScanner(newBlockingScanner(proc))
	if err := store.Upsert(ctx, supervisor.WorkerRecord{InstanceID: id, Generation: "stale", PID: 78, StartTicks: 4, Port: 10001}); err != nil {
		t.Fatal(err)
	}
	s.hardware = abundantSingleGPUHardware()
	s.ArmStartupReconcile()
	if err := s.ReconcileStaleWorkers(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartInstance(ctx, id); err == nil || !errors.Is(err, errOrphanCleanup) {
		t.Fatalf("expected block, err=%v", err)
	}
	sup.SetProcScanner(newBlockingScanner(supervisor.Proc{}))
	s.ArmStartupReconcile()
	if err := s.ReconcileStaleWorkers(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartInstance(ctx, id); err != nil {
		t.Fatal(err)
	}
}
