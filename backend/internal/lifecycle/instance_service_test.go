package lifecycle

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/supervisor"
)

func TestDirectInstanceLifecycleRestartKillRuntimeAndLogs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	s, _, m, sup, _ := setupLifecycle(t, true, false)

	store := s.Instances()
	item, err := store.Get(ctx, m.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	if item.ID != m.PublicID || item.ModelID != m.ID {
		t.Fatalf("instance=%+v model=%+v", item, m)
	}

	_, _, unsubscribe := s.SubscribeLogs(item.ID)
	defer unsubscribe()
	endpoint, err := s.StartInstance(ctx, item.ID)
	if err != nil || endpoint == "" {
		t.Fatalf("start endpoint=%q err=%v", endpoint, err)
	}
	rt, err := s.RuntimeInstance(ctx, item.ID)
	if err != nil || rt.State != supervisor.Ready || rt.ModelID != m.ID {
		t.Fatalf("runtime=%+v err=%v", rt, err)
	}

	restarted, err := s.RestartInstance(ctx, item.ID)
	if err != nil || restarted == "" {
		t.Fatalf("restart endpoint=%q err=%v", restarted, err)
	}
	if rt, err = s.RuntimeInstance(ctx, item.ID); err != nil || rt.State != supervisor.Ready {
		t.Fatalf("runtime after restart=%+v err=%v", rt, err)
	}

	if err := s.KillInstance(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state := sup.Status(item.ID).State
		if state != supervisor.Ready && state != supervisor.Starting {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := sup.Status(item.ID); got.State == supervisor.Ready || got.State == supervisor.Starting {
		t.Fatalf("worker still active after kill: %+v", got)
	}

	if _, err := s.RuntimeInstance(ctx, "missing"); err == nil {
		t.Fatal("expected missing runtime instance error")
	}
	if err := s.KillInstance(ctx, "missing"); err == nil {
		t.Fatal("expected missing kill instance error")
	}
}

func TestDirectAlwaysOnStopAndManualStopOverride(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	s, _, m, _, _ := setupLifecycle(t, true, true)
	item, err := s.Instances().Get(ctx, m.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartInstance(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.StopInstance(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.startInstance(ctx, item.ID, false); err == nil || !strings.Contains(err.Error(), "manually stopped") {
		t.Fatalf("expected manual-stop guard, got %v", err)
	}
	if endpoint, err := s.EnsureReady(ctx, item.ID); err != nil || endpoint == "" {
		t.Fatalf("inference override endpoint=%q err=%v", endpoint, err)
	}
	if s.isManuallyStopped(item.ID) {
		t.Fatal("inference request should clear manual stop")
	}
	if err := s.StopInstance(ctx, "missing"); err == nil {
		t.Fatal("expected missing stop instance error")
	}
}
