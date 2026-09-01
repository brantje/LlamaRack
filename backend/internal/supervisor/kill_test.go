package supervisor

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestKillMissingAndRunningWorker(t *testing.T) {
	s := New(fakeServerScript(t), "127.0.0.1", 28100, 5*time.Second)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		s.Shutdown(ctx)
	})

	if err := s.Kill("missing"); err != nil {
		t.Fatalf("kill missing: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if _, err := s.Start(ctx, "kill-me", "model", "/tmp/kill.gguf", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Kill("kill-me"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state := s.Status("kill-me").State
		if state != Ready && state != Starting && state != Stopping {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("worker remains active after kill: %+v", s.Status("kill-me"))
}

func TestKillInterruptsReadinessWait(t *testing.T) {
	s := New(fakeServerScript(t), "127.0.0.1", 28200, 5*time.Second)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		s.Shutdown(ctx)
	})

	started := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_, err := s.Start(ctx, "slow-kill", "model", "/tmp/slow.gguf", []string{"--test-ready-delay-ms", "8000"})
		started <- err
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.Status("slow-kill").State == Loading {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := s.Status("slow-kill"); got.State != Loading {
		t.Fatalf("expected LOADING before kill, got %+v", got)
	}

	killStarted := time.Now()
	if err := s.Kill("slow-kill"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-started:
		if err == nil {
			t.Fatal("expected start to fail after kill")
		}
		if !errors.Is(err, ErrKilled) && !errors.Is(err, context.Canceled) {
			t.Fatalf("start err=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("start did not return after kill")
	}
	if elapsed := time.Since(killStarted); elapsed > 2*time.Second {
		t.Fatalf("kill waited %s, want well under readiness timeout", elapsed)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state := s.Status("slow-kill").State
		if state != Ready && state != Starting && state != Loading {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("worker became or stayed active after kill: %+v", s.Status("slow-kill"))
}

func TestKillStartingWorkerWithoutProcess(t *testing.T) {
	s := New(fakeServerScript(t), "127.0.0.1", 28300, time.Second)
	s.mu.Lock()
	s.workers["starting"] = &worker{runtime: Runtime{InstanceID: "starting", State: Starting}, done: make(chan struct{})}
	s.mu.Unlock()
	if err := s.Kill("starting"); err != nil {
		t.Fatalf("kill starting worker: %v", err)
	}
	s.mu.RLock()
	killed := s.workers["starting"].killed
	s.mu.RUnlock()
	if !killed {
		t.Fatal("expected starting worker to be marked killed")
	}
}
