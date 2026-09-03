package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
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
