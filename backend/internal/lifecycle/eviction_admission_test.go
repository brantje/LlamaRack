package lifecycle

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

type claimGate struct {
	once    sync.Once
	claimed chan struct{}
	release chan struct{}
}

func newClaimGate() *claimGate {
	return &claimGate{claimed: make(chan struct{}), release: make(chan struct{})}
}

func (g *claimGate) hook(string) {
	g.once.Do(func() { close(g.claimed) })
	<-g.release
}

func startedInstance(t *testing.T, autoload bool) (*Service, instances.Instance, *supervisor.Supervisor) {
	t.Helper()
	ctx := context.Background()
	s, _, m, sup, _ := setupLifecycle(t, autoload, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	if _, err := s.StartInstance(ctx, items[0].ID); err != nil {
		t.Fatal(err)
	}
	waitForRuntimeState(t, sup, items[0].ID, supervisor.Ready)
	return s, items[0], sup
}

func TestAcquireBeforeEvictionClaimWins(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	s, victim, sup := startedInstance(t, true)

	gate := newClaimGate()
	s.beforeEvictionLock = gate.hook

	evictDone := make(chan error, 1)
	go func() { evictDone <- s.evictInstance(ctx, victim.ID) }()
	<-gate.claimed

	endpoint, release, err := s.Acquire(ctx, victim.ID)
	if err != nil || endpoint == "" || release == nil {
		t.Fatalf("acquire endpoint=%q err=%v", endpoint, err)
	}
	close(gate.release)
	if err := <-evictDone; !errors.Is(err, errEvictionIneligible) {
		t.Fatalf("evict err=%v", err)
	}
	if got := sup.Status(victim.ID); got.State != supervisor.Ready {
		t.Fatalf("victim stopped after losing admission: %+v", got)
	}
	if activity := s.Activity(victim.ID); activity.ActiveRequests != 1 {
		t.Fatalf("activity=%+v", activity)
	}
	release()
}

func TestAcquireAfterEvictionClaimDoesNotGetDyingEndpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	s, victim, sup := startedInstance(t, true)
	dyingPID := sup.Status(victim.ID).PID
	if dyingPID == 0 {
		t.Fatal("expected ready pid")
	}

	gate := newClaimGate()
	s.afterEvictionClaim = gate.hook
	evictDone := make(chan error, 1)
	go func() { evictDone <- s.evictInstance(ctx, victim.ID) }()
	<-gate.claimed

	if got := sup.Status(victim.ID); got.State != supervisor.Draining {
		t.Fatalf("state after claim=%s", got.State)
	}
	if _, ok := s.sup.Endpoint(victim.ID); ok {
		t.Fatal("draining instance still exposed a READY endpoint")
	}

	type result struct {
		endpoint string
		err      error
		release  func()
	}
	acquired := make(chan result, 1)
	go func() {
		endpoint, release, err := s.Acquire(ctx, victim.ID)
		acquired <- result{endpoint: endpoint, err: err, release: release}
	}()

	close(gate.release)
	if err := <-evictDone; err != nil {
		t.Fatalf("evict err=%v", err)
	}
	got := <-acquired
	if got.err != nil || got.endpoint == "" || got.release == nil {
		t.Fatalf("acquire endpoint=%q err=%v", got.endpoint, got.err)
	}
	defer got.release()
	if pid := sup.Status(victim.ID).PID; pid == 0 || pid == dyingPID {
		t.Fatalf("acquire reused dying worker pid=%d dying=%d state=%s", pid, dyingPID, sup.Status(victim.ID).State)
	}
	if got := sup.Status(victim.ID); got.State != supervisor.Ready {
		t.Fatalf("autoloaded state=%s", got.State)
	}
}

func TestAcquireAfterEvictionClaimWithoutAutoloadFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	s, victim, _ := startedInstance(t, false)

	gate := newClaimGate()
	s.afterEvictionClaim = gate.hook
	evictDone := make(chan error, 1)
	go func() { evictDone <- s.evictInstance(ctx, victim.ID) }()
	<-gate.claimed

	acquired := make(chan error, 1)
	go func() {
		_, release, err := s.Acquire(ctx, victim.ID)
		if release != nil {
			release()
		}
		acquired <- err
	}()
	close(gate.release)
	if err := <-evictDone; err != nil {
		t.Fatalf("evict err=%v", err)
	}
	err := <-acquired
	if err == nil || !strings.Contains(err.Error(), "autoload disabled") {
		t.Fatalf("acquire err=%v", err)
	}
}

func TestConcurrentAcquiresCannotBypassDrainClaim(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, victim, sup := startedInstance(t, true)
	dyingPID := sup.Status(victim.ID).PID

	gate := newClaimGate()
	s.afterEvictionClaim = gate.hook
	evictDone := make(chan error, 1)
	go func() { evictDone <- s.evictInstance(ctx, victim.ID) }()
	<-gate.claimed

	const n = 5
	type result struct {
		endpoint string
		pid      int
		err      error
		release  func()
	}
	results := make(chan result, n)
	for i := 0; i < n; i++ {
		go func() {
			endpoint, release, err := s.Acquire(ctx, victim.ID)
			results <- result{endpoint: endpoint, pid: sup.Status(victim.ID).PID, err: err, release: release}
		}()
	}
	close(gate.release)
	if err := <-evictDone; err != nil {
		t.Fatalf("evict err=%v", err)
	}

	var first string
	for i := 0; i < n; i++ {
		got := <-results
		if got.err != nil || got.endpoint == "" {
			t.Fatalf("acquire %d endpoint=%q err=%v", i, got.endpoint, got.err)
		}
		if got.pid == dyingPID {
			t.Fatal("concurrent acquire received the dying worker")
		}
		if first == "" {
			first = got.endpoint
		} else if got.endpoint != first {
			t.Fatalf("single-flight mismatch %q vs %q", first, got.endpoint)
		}
		if got.release != nil {
			got.release()
		}
	}
}

func TestPreparePlacementReplansWhenVictimBecomesActive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	s, victim, sup, execDB := func() (*Service, instances.Instance, *supervisor.Supervisor, func(string, ...any)) {
		t.Helper()
		s, _, m, sup, execDB := setupLifecycle(t, true, false)
		items, err := s.instances.ListByModel(ctx, m.ID)
		if err != nil || len(items) != 1 {
			t.Fatalf("instances=%+v err=%v", items, err)
		}
		s.hardware = abundantSingleGPUHardware()
		if _, err := s.StartInstance(ctx, items[0].ID); err != nil {
			t.Fatal(err)
		}
		execDB("UPDATE models SET total_bytes=? WHERE id=?", 2*testGiB, m.ID)
		return s, items[0], sup, execDB
	}()
	_ = execDB

	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{
		{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 1 * testGiB}}},
		{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 3 * testGiB}}},
	}}
	s.beforeEvictionLock = func(id string) {
		_, release, err := s.Acquire(ctx, id)
		if err != nil {
			t.Errorf("acquire during stale plan: %v", err)
			return
		}
		t.Cleanup(release)
	}

	_, err := s.preparePlacement(ctx, instances.Instance{ID: "target", GPUMode: "auto"}, 2*testGiB)
	if err == nil {
		t.Fatal("expected stale victim to fail or replan without enough capacity")
	}
	if !strings.Contains(err.Error(), "no longer eligible") && !strings.Contains(err.Error(), "eligible eviction victims") {
		t.Fatalf("unexpected placement error: %v", err)
	}
	if got := sup.Status(victim.ID); got.State != supervisor.Ready {
		t.Fatalf("active victim was stopped: %+v", got)
	}
}

func TestIdleUnloadClaimRejectsNewAcquireOfDyingEndpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, victim, sup := startedInstance(t, true)
	dyingPID := sup.Status(victim.ID).PID
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	s.touch(victim.ID)
	now = now.Add(10 * time.Minute)

	gate := newClaimGate()
	s.afterIdleDrainClaim = gate.hook
	idleDone := make(chan struct{})
	go func() {
		s.ReconcileIdle(ctx, time.Minute)
		close(idleDone)
	}()
	<-gate.claimed
	if got := sup.Status(victim.ID); got.State != supervisor.Draining {
		t.Fatalf("idle claim state=%s", got.State)
	}

	acquired := make(chan struct {
		endpoint string
		err      error
		release  func()
	}, 1)
	go func() {
		endpoint, release, err := s.Acquire(ctx, victim.ID)
		acquired <- struct {
			endpoint string
			err      error
			release  func()
		}{endpoint, err, release}
	}()
	close(gate.release)
	<-idleDone
	got := <-acquired
	if got.err != nil {
		t.Fatalf("acquire after idle drain: %v", got.err)
	}
	defer got.release()
	if pid := sup.Status(victim.ID).PID; pid == dyingPID {
		t.Fatal("idle-unload acquire received the dying worker")
	}
}

func TestManualStopDuringDrainClaimDoesNotRestoreDyingEndpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	s, victim, sup := startedInstance(t, true)
	dyingPID := sup.Status(victim.ID).PID

	gate := newClaimGate()
	s.afterEvictionClaim = gate.hook
	evictDone := make(chan error, 1)
	go func() { evictDone <- s.evictInstance(ctx, victim.ID) }()
	<-gate.claimed

	stopDone := make(chan error, 1)
	go func() { stopDone <- s.StopInstance(ctx, victim.ID) }()

	acquired := make(chan error, 1)
	go func() {
		endpoint, release, err := s.Acquire(ctx, victim.ID)
		if release != nil {
			if pid := sup.Status(victim.ID).PID; pid == dyingPID {
				acquired <- errors.New("acquire returned dying worker")
				release()
				return
			}
			release()
		}
		_ = endpoint
		acquired <- err
	}()

	close(gate.release)
	_ = <-evictDone
	if err := <-stopDone; err != nil {
		t.Fatalf("stop err=%v", err)
	}
	if err := <-acquired; err != nil && !errors.Is(err, errStartupKilled) && !strings.Contains(err.Error(), "autoload") {
		t.Fatalf("acquire err=%v", err)
	}
	if got := sup.Status(victim.ID); got.State == supervisor.Ready && got.PID == dyingPID {
		t.Fatal("dying READY endpoint was restored for waiters")
	}
}

func TestAcquireCancelledWhileWaitingForDrain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	s, victim, _ := startedInstance(t, true)

	gate := newClaimGate()
	s.afterEvictionClaim = gate.hook
	evictDone := make(chan error, 1)
	go func() { evictDone <- s.evictInstance(ctx, victim.ID) }()
	<-gate.claimed

	waitCtx, waitCancel := context.WithCancel(ctx)
	waitCancel()
	_, release, err := s.Acquire(waitCtx, victim.ID)
	if release != nil {
		release()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("acquire err=%v want canceled", err)
	}
	close(gate.release)
	if err := <-evictDone; err != nil {
		t.Fatalf("evict err=%v", err)
	}
}

func TestEnsureReadyWaitsForDirectSupervisorStop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	s, victim, sup := startedInstance(t, true)
	dyingPID := sup.Status(victim.ID).PID
	if !s.sup.BeginDrain(victim.ID) {
		t.Fatal("BeginDrain")
	}

	stopDone := make(chan error, 1)
	go func() { stopDone <- s.sup.Stop(ctx, victim.ID) }()
	endpoint, err := s.EnsureReady(ctx, victim.ID)
	if err := <-stopDone; err != nil {
		t.Fatalf("stop err=%v", err)
	}
	if err != nil || endpoint == "" {
		t.Fatalf("ensure-ready endpoint=%q err=%v", endpoint, err)
	}
	if pid := sup.Status(victim.ID).PID; pid == 0 || pid == dyingPID {
		t.Fatalf("ensure-ready reused dying pid=%d dying=%d", pid, dyingPID)
	}
}

func TestEvictInstanceAbortsDrainWhenStopCannotRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, victim, sup := startedInstance(t, true)

	s.afterEvictionClaim = func(string) { cancel() }
	if err := s.evictInstance(ctx, victim.ID); err == nil {
		t.Fatal("expected cancelled eviction to fail")
	}
	if got := sup.Status(victim.ID); got.State != supervisor.Ready {
		t.Fatalf("aborted drain left state=%s", got.State)
	}
	endpoint, release, err := s.Acquire(context.Background(), victim.ID)
	if err != nil || endpoint == "" {
		t.Fatalf("acquire after aborted drain endpoint=%q err=%v", endpoint, err)
	}
	release()
}
