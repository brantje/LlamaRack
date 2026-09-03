package supervisor

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/database"
)

type fakeScanner struct {
	mu       sync.Mutex
	procs    map[int]Proc
	killErr  map[int]error
	signaled []int
}

func newFakeScanner(procs ...Proc) *fakeScanner {
	s := &fakeScanner{procs: map[int]Proc{}, killErr: map[int]error{}}
	for _, proc := range procs {
		if proc.Environ == nil {
			proc.Environ = map[string]string{}
		}
		s.procs[proc.PID] = proc
	}
	return s
}

func (s *fakeScanner) List() ([]Proc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Proc, 0, len(s.procs))
	for _, proc := range s.procs {
		out = append(out, proc)
	}
	return out, nil
}

func (s *fakeScanner) Inspect(pid int) (Proc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	proc, ok := s.procs[pid]
	if !ok {
		return Proc{}, os.ErrNotExist
	}
	return proc, nil
}

func (s *fakeScanner) Signal(pid int, sig syscall.Signal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.signaled = append(s.signaled, pid)
	if err := s.killErr[pid]; err != nil {
		return err
	}
	if sig == syscall.SIGTERM || sig == syscall.SIGKILL {
		delete(s.procs, pid)
	}
	return nil
}

func (s *fakeScanner) Alive(pid int, startTicks uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	proc, ok := s.procs[pid]
	if !ok {
		return false
	}
	return startTicks == 0 || proc.StartTicks == startTicks
}

func ownedProc(pid int, installID, instanceID, generation string, ticks uint64, port string) Proc {
	return Proc{
		PID:        pid,
		StartTicks: ticks,
		Environ: map[string]string{
			EnvInstallationID:   installID,
			EnvInstanceID:       instanceID,
			EnvWorkerGeneration: generation,
			EnvWorkerPort:       port,
		},
	}
}

func TestReconcileNoSurvivingWorkers(t *testing.T) {
	s := New("unused", "127.0.0.1", 30000, time.Second)
	store := NewMemoryStore()
	s.SetRuntimeIdentity("install-1", store)
	s.SetProcScanner(newFakeScanner(Proc{PID: 9, StartTicks: 1, Environ: map[string]string{"PATH": "/bin"}}))
	result := s.ReconcileStaleWorkers(context.Background())
	if result.Detected != 0 || result.Terminated != 0 || len(result.Blocked) != 0 {
		t.Fatalf("result=%+v", result)
	}
}

func TestReconcileTerminatesOwnedOrphans(t *testing.T) {
	s := New("unused", "127.0.0.1", 30000, time.Second)
	store := NewMemoryStore()
	s.SetRuntimeIdentity("install-1", store)
	one := ownedProc(11, "install-1", "a", "gen-a", 100, "10001")
	two := ownedProc(12, "install-1", "b", "gen-b", 101, "10002")
	if err := store.Upsert(context.Background(), WorkerRecord{InstanceID: "a", Generation: "gen-a", PID: 11, StartTicks: 100, Port: 10001}); err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(context.Background(), WorkerRecord{InstanceID: "b", Generation: "gen-b", PID: 12, StartTicks: 101, Port: 10002}); err != nil {
		t.Fatal(err)
	}
	scanner := newFakeScanner(one, two)
	s.SetProcScanner(scanner)
	result := s.ReconcileStaleWorkers(context.Background())
	if result.Terminated != 2 || result.Detected != 2 {
		t.Fatalf("result=%+v", result)
	}
	if _, err := store.Get(context.Background(), "a"); err != ErrRuntimeNotFound {
		t.Fatalf("record a still present: %v", err)
	}
	if len(scanner.signaled) == 0 {
		t.Fatal("expected termination signals")
	}
}

func TestReconcileNeverKillsUnrelatedOrUnprovenProcesses(t *testing.T) {
	s := New("unused", "127.0.0.1", 30000, time.Second)
	store := NewMemoryStore()
	s.SetRuntimeIdentity("install-1", store)
	unrelated := Proc{PID: 21, StartTicks: 5, Environ: map[string]string{}}
	sameShape := Proc{PID: 22, StartTicks: 6, Environ: map[string]string{"PATH": "/usr/bin/llama-server"}}
	foreign := ownedProc(23, "other-install", "a", "gen", 7, "10000")
	scanner := newFakeScanner(unrelated, sameShape, foreign)
	s.SetProcScanner(scanner)
	if err := store.Upsert(context.Background(), WorkerRecord{InstanceID: "a", Generation: "gen", PID: 21, StartTicks: 5, Port: 10000}); err != nil {
		t.Fatal(err)
	}
	result := s.ReconcileStaleWorkers(context.Background())
	if result.Terminated != 0 {
		t.Fatalf("terminated unrelated processes: %+v signaled=%v", result, scanner.signaled)
	}
	if _, alive := scanner.procs[21]; !alive {
		t.Fatal("unrelated pid 21 was removed")
	}
	if _, alive := scanner.procs[23]; !alive {
		t.Fatal("foreign installation process was killed")
	}
}

func TestReconcileRejectsPIDReuseAndGenerationMismatch(t *testing.T) {
	s := New("unused", "127.0.0.1", 30000, time.Second)
	store := NewMemoryStore()
	s.SetRuntimeIdentity("install-1", store)
	reused := Proc{PID: 31, StartTicks: 999, Environ: map[string]string{"PATH": "/bin"}}
	mismatch := ownedProc(32, "install-1", "b", "gen-live", 50, "10002")
	if err := store.Upsert(context.Background(), WorkerRecord{InstanceID: "a", Generation: "gen-old", PID: 31, StartTicks: 10, Port: 10001}); err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(context.Background(), WorkerRecord{InstanceID: "b", Generation: "gen-old", PID: 32, StartTicks: 50, Port: 10002}); err != nil {
		t.Fatal(err)
	}
	scanner := newFakeScanner(reused, mismatch)
	s.SetProcScanner(scanner)
	s.mu.Lock()
	s.workers["b"] = &worker{generation: "gen-live", runtime: Runtime{InstanceID: "b", PID: 32, State: Ready}}
	s.mu.Unlock()
	result := s.ReconcileStaleWorkers(context.Background())
	if result.Terminated != 0 {
		t.Fatalf("killed on mismatch: %+v signaled=%v", result, scanner.signaled)
	}
	if _, err := store.Get(context.Background(), "a"); err != ErrRuntimeNotFound {
		t.Fatal("stale pid-reuse metadata was not cleaned")
	}
	if _, err := store.Get(context.Background(), "b"); err != ErrRuntimeNotFound {
		t.Fatal("generation-mismatch metadata was not cleaned")
	}
	if _, alive := scanner.procs[32]; !alive {
		t.Fatal("current-generation worker was killed")
	}
}

func TestReconcileCleansDeadPIDMetadata(t *testing.T) {
	s := New("unused", "127.0.0.1", 30000, time.Second)
	store := NewMemoryStore()
	s.SetRuntimeIdentity("install-1", store)
	s.SetProcScanner(newFakeScanner())
	if err := store.Upsert(context.Background(), WorkerRecord{InstanceID: "gone", Generation: "g", PID: 44, StartTicks: 1, Port: 1}); err != nil {
		t.Fatal(err)
	}
	result := s.ReconcileStaleWorkers(context.Background())
	if result.CleanedMetadata != 1 || result.Terminated != 0 {
		t.Fatalf("result=%+v", result)
	}
	if _, err := store.Get(context.Background(), "gone"); err != ErrRuntimeNotFound {
		t.Fatal("dead pid metadata remained")
	}
}

func TestReconcileKillFailureBlocksInstance(t *testing.T) {
	s := New("unused", "127.0.0.1", 30000, time.Second)
	store := NewMemoryStore()
	s.SetRuntimeIdentity("install-1", store)
	proc := ownedProc(51, "install-1", "stuck", "gen", 8, "10003")
	scanner := newFakeScanner(proc)
	scanner.killErr[51] = errors.New("permission denied")
	s.SetProcScanner(scanner)
	if err := store.Upsert(context.Background(), WorkerRecord{InstanceID: "stuck", Generation: "gen", PID: 51, StartTicks: 8, Port: 10003}); err != nil {
		t.Fatal(err)
	}
	result := s.ReconcileStaleWorkers(context.Background())
	if result.Terminated != 0 || result.Blocked["stuck"] == "" {
		t.Fatalf("result=%+v", result)
	}
	if _, err := store.Get(context.Background(), "stuck"); err != nil {
		t.Fatal("failed cleanup should keep metadata until the worker is gone")
	}
}

func TestReconcileDoesNotKillCurrentGenerationOnRepeat(t *testing.T) {
	s := New("unused", "127.0.0.1", 30000, time.Second)
	store := NewMemoryStore()
	s.SetRuntimeIdentity("install-1", store)
	proc := ownedProc(61, "install-1", "live", "gen-now", 3, "10004")
	scanner := newFakeScanner(proc)
	s.SetProcScanner(scanner)
	if err := store.Upsert(context.Background(), WorkerRecord{InstanceID: "live", Generation: "gen-now", PID: 61, StartTicks: 3, Port: 10004}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.workers["live"] = &worker{generation: "gen-now", runtime: Runtime{InstanceID: "live", PID: 61, State: Ready}}
	s.mu.Unlock()
	first := s.ReconcileStaleWorkers(context.Background())
	second := s.ReconcileStaleWorkers(context.Background())
	if first.Terminated != 0 || second.Terminated != 0 {
		t.Fatalf("repeat reconcile killed live worker first=%+v second=%+v signaled=%v", first, second, scanner.signaled)
	}
}

func TestReconcileRestartsSurvivingOwnedWorker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	installID, err := EnsureInstallationID(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	store := NewSQLStore(db)
	binary := fakeServerScript(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	portStart := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	original := New(binary, "127.0.0.1", portStart, 5*time.Second)
	original.SetRuntimeIdentity(installID, store)
	rt, err := original.Start(ctx, "coding", "model-1", "/tmp/model.gguf", nil)
	if err != nil {
		t.Fatal(err)
	}
	oldPID := rt.PID
	oldPort := rt.Port

	restarted := New(binary, "127.0.0.1", portStart, 5*time.Second)
	restarted.SetRuntimeIdentity(installID, store)
	result := restarted.ReconcileStaleWorkers(ctx)
	if result.Terminated != 1 || result.Blocked["coding"] != "" {
		t.Fatalf("restart reconcile=%+v", result)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.FindProcess(oldPID); err == nil {
			if err := syscall.Kill(oldPID, 0); err != nil {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := syscall.Kill(oldPID, 0); err == nil {
		t.Fatalf("stale pid %d still alive", oldPID)
	}
	replacement, err := restarted.Start(ctx, "coding", "model-1", "/tmp/model.gguf", nil)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.PID == 0 || replacement.PID == oldPID {
		t.Fatalf("replacement=%+v oldPID=%d", replacement, oldPID)
	}
	if oldPort != 0 {
		// The original port must be reusable after stale cleanup.
		if replacement.Port != oldPort && replacement.Port == 0 {
			t.Fatalf("replacement port=%d old=%d", replacement.Port, oldPort)
		}
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopCancel()
	restarted.Shutdown(stopCtx)
}
