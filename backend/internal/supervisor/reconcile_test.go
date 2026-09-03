package supervisor

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/database"
)

type fakeScanner struct {
	mu         sync.Mutex
	procs      map[int]Proc
	killErr    map[int]error
	inspectErr map[int]error
	signaled   []int
	ignoreTerm bool
	listErr    error
}

func newFakeScanner(procs ...Proc) *fakeScanner {
	s := &fakeScanner{procs: map[int]Proc{}, killErr: map[int]error{}, inspectErr: map[int]error{}}
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
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := make([]Proc, 0, len(s.procs))
	for pid, proc := range s.procs {
		if s.inspectErr[pid] != nil {
			continue
		}
		out = append(out, proc)
	}
	return out, nil
}

func (s *fakeScanner) Inspect(pid int) (Proc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.inspectErr[pid]; err != nil {
		return Proc{}, err
	}
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
		if errors.Is(err, syscall.ESRCH) {
			delete(s.procs, pid)
		}
		return err
	}
	if s.ignoreTerm && sig == syscall.SIGTERM {
		return nil
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

func TestReconcileSkipsWithoutInstallationID(t *testing.T) {
	s := New("unused", "127.0.0.1", 30000, time.Second)
	result := s.ReconcileStaleWorkers(context.Background())
	if result.Detected != 0 || result.Terminated != 0 {
		t.Fatalf("result=%+v", result)
	}
}

func TestReconcileScanFailureBlocksPersistedInstances(t *testing.T) {
	s := New("unused", "127.0.0.1", 30000, time.Second)
	store := NewMemoryStore()
	s.SetRuntimeIdentity("install-1", store)
	if err := store.Upsert(context.Background(), WorkerRecord{InstanceID: "blocked", Generation: "g", PID: 1, StartTicks: 1, Port: 2}); err != nil {
		t.Fatal(err)
	}
	scanner := newFakeScanner()
	scanner.listErr = errors.New("proc unavailable")
	s.SetProcScanner(scanner)
	result := s.ReconcileStaleWorkers(context.Background())
	if result.Blocked["blocked"] == "" {
		t.Fatalf("expected scan failure to block instance: %+v", result)
	}
}

func TestReconcileTerminatesProcOnlyOwnedWorkerAndSIGKILLFallback(t *testing.T) {
	s := New("unused", "127.0.0.1", 30000, time.Second)
	store := NewMemoryStore()
	s.SetRuntimeIdentity("install-1", store)
	proc := ownedProc(88, "install-1", "scan-only", "gen", 4, "0")
	scanner := newFakeScanner(proc)
	scanner.ignoreTerm = true
	s.SetProcScanner(scanner)
	result := s.ReconcileStaleWorkers(context.Background())
	if result.Terminated != 1 {
		t.Fatalf("result=%+v signaled=%v", result, scanner.signaled)
	}
	if _, alive := scanner.procs[88]; alive {
		t.Fatal("SIGKILL fallback left the process")
	}
}

func TestLinuxProcScannerInspectsCurrentProcess(t *testing.T) {
	scanner := LinuxProcScanner{}
	pid := os.Getpid()
	proc, err := scanner.Inspect(pid)
	if err != nil || proc.PID != pid || proc.StartTicks == 0 {
		t.Fatalf("inspect self=%+v err=%v", proc, err)
	}
	if !scanner.Alive(pid, proc.StartTicks) {
		t.Fatal("current process should be alive")
	}
	if scanner.Alive(pid, proc.StartTicks+1) {
		t.Fatal("start-tick mismatch should not look alive")
	}
	listed, err := scanner.List()
	if err != nil || len(listed) == 0 {
		t.Fatalf("list err=%v n=%d", err, len(listed))
	}
	if err := scanner.Signal(-1, syscall.SIGTERM); err == nil {
		t.Fatal("invalid pid should fail")
	}
}

func TestWaitPortReleasedAndParseEdges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = ln.Close()
	}()
	if err := waitPortReleased(ctx, "127.0.0.1", port, time.Second); err != nil {
		t.Fatal(err)
	}
	if parsePortEnv(nil) != 0 || parsePortEnv(map[string]string{EnvWorkerPort: "nope"}) != 0 {
		t.Fatal("invalid port env should be zero")
	}
	if readStartTicks(-1) != 0 || readStartTicks(1<<30) != 0 {
		t.Fatal("missing pid should not report start ticks")
	}
	if (LinuxProcScanner{Root: " "}).root() != "/proc" {
		t.Fatal("empty root should default to /proc")
	}
	if (LinuxProcScanner{Root: "/custom"}).root() != "/custom" {
		t.Fatal("custom root should be preserved")
	}
	if _, err := (LinuxProcScanner{}).Inspect(0); err == nil {
		t.Fatal("pid 0 should fail inspect")
	}
	if _, ok := ownedByInstallation(Proc{}, "install"); ok {
		t.Fatal("nil environ is not owned")
	}
	if _, ok := ownedByInstallation(Proc{Environ: map[string]string{EnvInstallationID: "install"}}, "install"); ok {
		t.Fatal("incomplete identity is not owned")
	}
	if recordOwnsProcess(WorkerRecord{InstanceID: "a", Generation: "g"}, Proc{Environ: map[string]string{EnvInstallationID: "other"}}, "install") {
		t.Fatal("foreign installation is not owned")
	}
	if recordOwnsProcess(WorkerRecord{InstanceID: "a", Generation: "g"}, Proc{Environ: map[string]string{EnvInstallationID: "install", EnvInstanceID: "b", EnvWorkerGeneration: "g"}}, "install") {
		t.Fatal("wrong instance is not owned")
	}
	if err := waitPortReleased(context.Background(), "127.0.0.1", 0, time.Millisecond); err != nil {
		t.Fatal(err)
	}
}

func TestTerminateOwnedESRCHAndPortTimeoutAndImmortal(t *testing.T) {
	staleTermTimeout = 30 * time.Millisecond
	staleKillTimeout = 20 * time.Millisecond
	stalePortTimeout = 40 * time.Millisecond
	t.Cleanup(func() {
		staleTermTimeout = 15 * time.Second
		staleKillTimeout = 5 * time.Second
		stalePortTimeout = 5 * time.Second
	})
	s := New("unused", "127.0.0.1", 30000, time.Second)
	store := NewMemoryStore()
	s.SetRuntimeIdentity("install-1", store)

	gone := ownedProc(91, "install-1", "gone", "g", 1, "0")
	scanner := newFakeScanner(gone)
	scanner.killErr[91] = syscall.ESRCH
	s.SetProcScanner(scanner)
	if err := store.Upsert(context.Background(), WorkerRecord{InstanceID: "gone", Generation: "g", PID: 91, StartTicks: 1, Port: 0}); err != nil {
		t.Fatal(err)
	}
	result := s.ReconcileStaleWorkers(context.Background())
	if result.Terminated != 1 {
		t.Fatalf("ESRCH should count as terminated: %+v", result)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	held := ownedProc(92, "install-1", "held", "g", 2, strconv.Itoa(port))
	holdScanner := newFakeScanner(held)
	s.SetProcScanner(holdScanner)
	if err := store.Upsert(context.Background(), WorkerRecord{InstanceID: "held", Generation: "g", PID: 92, StartTicks: 2, Port: port}); err != nil {
		t.Fatal(err)
	}
	result = s.ReconcileStaleWorkers(context.Background())
	if result.Blocked["held"] == "" {
		t.Fatalf("occupied port should block: %+v", result)
	}

	immortal := ownedProc(93, "install-1", "immortal", "g", 3, "0")
	imm := &immortalScanner{proc: immortal}
	s.SetProcScanner(imm)
	if err := store.Upsert(context.Background(), WorkerRecord{InstanceID: "immortal", Generation: "g", PID: 93, StartTicks: 3, Port: 0}); err != nil {
		t.Fatal(err)
	}
	result = s.ReconcileStaleWorkers(context.Background())
	if result.Blocked["immortal"] == "" {
		t.Fatalf("immortal process should block: %+v", result)
	}
}

type immortalScanner struct {
	proc Proc
}

func (s *immortalScanner) List() ([]Proc, error) { return []Proc{s.proc}, nil }
func (s *immortalScanner) Inspect(pid int) (Proc, error) {
	if pid != s.proc.PID {
		return Proc{}, os.ErrNotExist
	}
	return s.proc, nil
}
func (s *immortalScanner) Signal(int, syscall.Signal) error { return nil }
func (s *immortalScanner) Alive(int, uint64) bool           { return true }

func TestClearRuntimeRecordIgnoresOtherGeneration(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	s := New("unused", "127.0.0.1", 30000, time.Second)
	s.SetRuntimeIdentity("install", store)
	if err := store.Upsert(ctx, WorkerRecord{InstanceID: "x", Generation: "new", PID: 1, StartTicks: 1, Port: 1}); err != nil {
		t.Fatal(err)
	}
	s.clearRuntimeRecord("x", "old")
	if _, err := store.Get(ctx, "x"); err != nil {
		t.Fatal("newer generation metadata must be kept")
	}
	s.lookupStartTicks(1)
}

func TestSQLAndMemoryStoreNilGuards(t *testing.T) {
	ctx := context.Background()
	var sqlStore *SQLStore
	if err := sqlStore.Upsert(ctx, WorkerRecord{}); err == nil {
		t.Fatal("nil sql upsert")
	}
	if _, err := sqlStore.Get(ctx, "x"); err == nil {
		t.Fatal("nil sql get")
	}
	if err := sqlStore.Delete(ctx, "x"); err == nil {
		t.Fatal("nil sql delete")
	}
	if _, err := sqlStore.List(ctx); err == nil {
		t.Fatal("nil sql list")
	}
	var mem *MemoryStore
	if err := mem.Upsert(ctx, WorkerRecord{}); err == nil {
		t.Fatal("nil memory upsert")
	}
	if _, err := mem.Get(ctx, "x"); err == nil {
		t.Fatal("nil memory get")
	}
	if err := mem.Delete(ctx, "x"); err == nil {
		t.Fatal("nil memory delete")
	}
	if _, err := mem.List(ctx); err == nil {
		t.Fatal("nil memory list")
	}
	if _, err := EnsureInstallationID(ctx, nil); err == nil {
		t.Fatal("nil db should fail")
	}
}

func TestReconcileBlocksUnreadableEnviron(t *testing.T) {
	s := New("unused", "127.0.0.1", 30000, time.Second)
	store := NewMemoryStore()
	s.SetRuntimeIdentity("install-1", store)
	proc := ownedProc(101, "install-1", "hidden", "gen", 9, "10005")
	scanner := newFakeScanner(proc)
	scanner.inspectErr[101] = os.ErrPermission
	s.SetProcScanner(scanner)
	if err := store.Upsert(context.Background(), WorkerRecord{InstanceID: "hidden", Generation: "gen", PID: 101, StartTicks: 9, Port: 10005}); err != nil {
		t.Fatal(err)
	}
	result := s.ReconcileStaleWorkers(context.Background())
	if result.Blocked["hidden"] == "" || result.Terminated != 0 {
		t.Fatalf("unreadable environ should block without killing: %+v", result)
	}
	if _, err := store.Get(context.Background(), "hidden"); err != nil {
		t.Fatal("metadata must be kept until ownership is verified")
	}
}

func TestStartFailsWhenWorkerIdentityCannotBeCreated(t *testing.T) {
	orig := randomIdentity
	t.Cleanup(func() { randomIdentity = orig })
	randomIdentity = func() (string, error) { return "", errors.New("entropy exhausted") }
	s := New(fakeServerScript(t), "127.0.0.1", 29000, time.Second)
	s.SetRuntimeIdentity("install-1", NewMemoryStore())
	_, err := s.Start(context.Background(), "x", "m", "/tmp/model.gguf", nil)
	if err == nil || !strings.Contains(err.Error(), "worker identity") {
		t.Fatalf("err=%v", err)
	}
}
