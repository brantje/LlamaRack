package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/database"
)

func TestWorkerIdentityPersistsAndClearsOnStop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
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
	s := New(fakeServerScript(t), "127.0.0.1", 28000, 5*time.Second)
	s.SetRuntimeIdentity(installID, store)
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer stopCancel()
		s.Shutdown(stopCtx)
	})

	rt, err := s.Start(ctx, "owned", "model-1", "/tmp/model.gguf", nil)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := store.Get(ctx, "owned")
	if err != nil {
		t.Fatal(err)
	}
	if rec.PID != rt.PID || rec.Port != rt.Port || rec.Generation == "" || rec.StartTicks == 0 {
		t.Fatalf("persisted record=%+v runtime=%+v", rec, rt)
	}
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(rt.PID), "environ"))
	if err != nil {
		t.Fatal(err)
	}
	environ := parseEnviron(data)
	if environ[EnvInstallationID] != installID || environ[EnvInstanceID] != "owned" || environ[EnvWorkerGeneration] != rec.Generation {
		t.Fatalf("worker env=%v record=%+v", environ, rec)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopCancel()
	if err := s.Stop(stopCtx, "owned"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := store.Get(ctx, "owned"); err == ErrRuntimeNotFound {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := store.Get(ctx, "owned"); err != ErrRuntimeNotFound {
		t.Fatalf("graceful stop left runtime record: %v", err)
	}
}

func TestParseStartTicksAndEnviron(t *testing.T) {
	ticks, err := parseStartTicks("123 (llama server) S 1 1 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 98765 0")
	if err != nil || ticks != 98765 {
		t.Fatalf("ticks=%d err=%v", ticks, err)
	}
	if _, err := parseStartTicks("no-paren"); err == nil {
		t.Fatal("expected invalid stat error")
	}
	env := parseEnviron([]byte("A=1\x00B=2=3\x00LLAMARACK_WORKER_PORT=10001\x00"))
	if env["A"] != "1" || env["B"] != "2=3" || parsePortEnv(env) != 10001 {
		t.Fatalf("environ=%v", env)
	}
	if len(identityEnv("", "i", "g", 1)) != 0 || len(identityEnv("inst", "i", "g", 9)) != 4 {
		t.Fatal("identity env should require installation id")
	}
}

type upsertFailStore struct {
	inner RuntimeStore
	err   error
}

func (s *upsertFailStore) Upsert(context.Context, WorkerRecord) error { return s.err }
func (s *upsertFailStore) Get(ctx context.Context, instanceID string) (WorkerRecord, error) {
	return s.inner.Get(ctx, instanceID)
}
func (s *upsertFailStore) Delete(ctx context.Context, instanceID string) error {
	return s.inner.Delete(ctx, instanceID)
}
func (s *upsertFailStore) List(ctx context.Context) ([]WorkerRecord, error) {
	return s.inner.List(ctx)
}

type inspectFailScanner struct {
	err   error
	ticks uint64
}

func (s inspectFailScanner) List() ([]Proc, error) { return nil, nil }
func (s inspectFailScanner) Inspect(pid int) (Proc, error) {
	if s.err != nil {
		return Proc{}, s.err
	}
	return Proc{PID: pid, StartTicks: s.ticks}, nil
}
func (inspectFailScanner) Signal(int, syscall.Signal) error { return nil }
func (inspectFailScanner) Alive(int, uint64) bool           { return true }

func TestStartFailsWhenRuntimeRecordCannotBePersisted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store := &upsertFailStore{inner: NewMemoryStore(), err: errors.New("disk full")}
	s := New(fakeServerScript(t), "127.0.0.1", 28100, 2*time.Second)
	s.SetRuntimeIdentity("install-1", store)
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		s.Shutdown(stopCtx)
	})
	rt, err := s.Start(ctx, "persist-fail", "model-1", "/tmp/model.gguf", nil)
	if err == nil {
		t.Fatal("expected persist failure")
	}
	if rt.PID != 0 && syscall.Kill(rt.PID, 0) == nil {
		t.Fatalf("worker pid %d still alive after persist failure", rt.PID)
	}
	if _, getErr := store.Get(ctx, "persist-fail"); !errors.Is(getErr, ErrRuntimeNotFound) {
		t.Fatalf("failed persist left runtime record: %v", getErr)
	}
	if s.Status("persist-fail").State != Failed {
		t.Fatalf("status=%+v", s.Status("persist-fail"))
	}
}

func TestStartFailsWhenProcScannerCannotProvideStartIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store := NewMemoryStore()
	s := New(fakeServerScript(t), "127.0.0.1", 28200, 2*time.Second)
	s.SetRuntimeIdentity("install-1", store)
	s.SetProcScanner(inspectFailScanner{err: os.ErrPermission})
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		s.Shutdown(stopCtx)
	})
	rt, err := s.Start(ctx, "scanner-fail", "model-1", "/tmp/model.gguf", nil)
	if err == nil {
		t.Fatal("expected start-identity failure")
	}
	if rt.PID != 0 && syscall.Kill(rt.PID, 0) == nil {
		t.Fatalf("worker pid %d still alive after scanner failure", rt.PID)
	}
	if _, getErr := store.Get(ctx, "scanner-fail"); !errors.Is(getErr, ErrRuntimeNotFound) {
		t.Fatalf("failed identity lookup left runtime record: %v", getErr)
	}
}

func TestStartFailsWhenStartTicksAreZero(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store := NewMemoryStore()
	s := New(fakeServerScript(t), "127.0.0.1", 28300, 2*time.Second)
	s.SetRuntimeIdentity("install-1", store)
	s.SetProcScanner(inspectFailScanner{})
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		s.Shutdown(stopCtx)
	})
	if _, err := s.Start(ctx, "zero-ticks", "model-1", "/tmp/model.gguf", nil); err == nil {
		t.Fatal("expected zero start-ticks to fail launch")
	}
	if _, getErr := store.Get(ctx, "zero-ticks"); !errors.Is(getErr, ErrRuntimeNotFound) {
		t.Fatalf("zero ticks left runtime record: %v", getErr)
	}
}

func TestPersistWorkerNoopsWithoutIdentity(t *testing.T) {
	s := New("unused", "127.0.0.1", 28400, time.Second)
	if err := s.persistWorker("x", "g", 1, 1); err != nil {
		t.Fatal(err)
	}
	if ticks, err := s.lookupStartTicks(1 << 30); err == nil || ticks != 0 {
		t.Fatalf("missing pid should fail lookup ticks=%d err=%v", ticks, err)
	}
}
