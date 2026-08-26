package supervisor

import (
	"context"
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
