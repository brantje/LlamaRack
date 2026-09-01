package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

func waitForSupervisorState(t *testing.T, sup *supervisor.Supervisor, id string, want supervisor.State, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if sup.Status(id).State == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s: %+v", want, sup.Status(id))
}

func enableReadyDelay(t *testing.T, exec func(string, ...any), instanceID string, delayMS int) {
	t.Helper()
	exec(`INSERT INTO instance_options(instance_id, option_key, option_value) VALUES(?,?,?)`, instanceID, "test-ready-delay-ms", fmt.Sprintf("%d", delayMS))
}

func TestKillInstanceInterruptsLoading(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, _, m, sup, exec := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	id := items[0].ID
	enableReadyDelay(t, exec, id, 8000)
	exec("UPDATE models SET total_bytes=? WHERE id=?", 8*testGiB, m.ID)
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 12 * testGiB}}}}}

	type result struct {
		endpoint string
		err      error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			endpoint, err := s.StartInstance(ctx, id)
			results <- result{endpoint: endpoint, err: err}
		}()
	}
	waitForSupervisorState(t, sup, id, supervisor.Loading, 2*time.Second)

	killStarted := time.Now()
	if err := s.KillInstance(ctx, id); err != nil {
		t.Fatalf("kill during load: %v", err)
	}
	if elapsed := time.Since(killStarted); elapsed > 2*time.Second {
		t.Fatalf("KillInstance took %s, want well under startup timeout", elapsed)
	}

	for i := 0; i < 2; i++ {
		select {
		case got := <-results:
			if got.endpoint != "" {
				t.Fatalf("waiter %d got endpoint %q", i, got.endpoint)
			}
			if !errors.Is(got.err, errStartupKilled) && !errors.Is(got.err, supervisor.ErrKilled) && !errors.Is(got.err, context.Canceled) {
				t.Fatalf("waiter %d err=%v", i, got.err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("waiter %d did not complete after kill", i)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sup.Status(id).State == supervisor.Ready {
			t.Fatalf("runtime became READY after kill: %+v", sup.Status(id))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := sup.Status(id); got.State == supervisor.Ready {
		t.Fatalf("runtime READY after kill: %+v", got)
	}
	if _, ok := s.reservations.GetByInstance(id); ok {
		t.Fatal("killed startup left a scheduler lease")
	}

	logs := strings.Join(s.Logs(id), "\n")
	if strings.Contains(logs, "worker ready after") {
		t.Fatalf("killed startup later published ready log: %s", logs)
	}
	if !strings.Contains(logs, "worker killed") && !strings.Contains(logs, "startup killed") {
		t.Fatalf("expected killed log, got %s", logs)
	}

	exec("DELETE FROM instance_options WHERE instance_id=? AND option_key=?", id, "test-ready-delay-ms")
	endpoint, err := s.StartInstance(ctx, id)
	if err != nil || endpoint == "" {
		t.Fatalf("start after kill endpoint=%q err=%v", endpoint, err)
	}
	if rt := sup.Status(id); rt.State != supervisor.Ready {
		t.Fatalf("runtime after subsequent start: %+v", rt)
	}
}

func TestConcurrentKillInstanceDuringLoading(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	s, _, m, sup, exec := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	id := items[0].ID
	enableReadyDelay(t, exec, id, 8000)

	startErr := make(chan error, 1)
	go func() {
		_, err := s.StartInstance(ctx, id)
		startErr <- err
	}()
	waitForSupervisorState(t, sup, id, supervisor.Loading, 2*time.Second)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.KillInstance(ctx, id)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent kill: %v", err)
		}
	}
	select {
	case err := <-startErr:
		if err == nil {
			t.Fatal("start succeeded after concurrent kills")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("start did not complete after concurrent kills")
	}
	if got := sup.Status(id); got.State == supervisor.Ready {
		t.Fatalf("runtime READY after concurrent kills: %+v", got)
	}
}

func TestStopDuringLoadingDoesNotPublishReady(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	s, _, m, sup, exec := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	id := items[0].ID
	enableReadyDelay(t, exec, id, 8000)

	startErr := make(chan error, 1)
	go func() {
		_, err := s.StartInstance(ctx, id)
		startErr <- err
	}()
	waitForSupervisorState(t, sup, id, supervisor.Loading, 2*time.Second)

	stopStarted := time.Now()
	if err := s.StopInstance(ctx, id); err != nil {
		t.Fatalf("stop during load: %v", err)
	}
	if elapsed := time.Since(stopStarted); elapsed > 2*time.Second {
		t.Fatalf("StopInstance took %s, want prompt interrupt", elapsed)
	}
	select {
	case err := <-startErr:
		if err == nil {
			t.Fatal("start succeeded after stop during load")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("start did not complete after stop")
	}
	if got := sup.Status(id); got.State == supervisor.Ready {
		t.Fatalf("runtime READY after stop during load: %+v", got)
	}
}

func TestStartupGenerationHelpers(t *testing.T) {
	s, _, m, _, _ := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(context.Background(), m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	id := items[0].ID
	ctx1, gen1 := s.beginStartup(id)
	ctx2, gen2 := s.beginStartup(id)
	if gen2 <= gen1 {
		t.Fatalf("gen2=%d gen1=%d", gen2, gen1)
	}
	select {
	case <-ctx1.Done():
	default:
		t.Fatal("replaced startup context should be cancelled")
	}
	if ctx2.Err() != nil {
		t.Fatalf("live startup context err=%v", ctx2.Err())
	}
	s.finishStartup(id, gen1)
	if ctx2.Err() != nil {
		t.Fatal("stale finishStartup cancelled the live generation")
	}
	s.finishStartup(id, gen2)
	select {
	case <-ctx2.Done():
	default:
		t.Fatal("finishStartup should cancel the live generation")
	}
	if !s.generationLive(context.Background(), id) {
		t.Fatal("contexts without a generation must remain live")
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	s.mu.Lock()
	s.loads[id] = &loadCall{done: make(chan struct{})}
	s.mu.Unlock()
	if err := s.waitLoad(cancelled, id); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitLoad cancelled err=%v", err)
	}
}

func TestStartOneRejectsStaleGeneration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	s, _, m, sup, _ := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	instance := items[0]
	startCtx, gen := s.beginStartup(instance.ID)
	s.mu.Lock()
	s.startupGen[instance.ID] = gen + 1
	s.mu.Unlock()
	if _, err := s.startOne(startCtx, instance); !errors.Is(err, errStartupKilled) {
		t.Fatalf("stale generation err=%v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sup.Status(instance.ID).State != supervisor.Ready {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("stale generation remained READY: %+v", sup.Status(instance.ID))
}

func TestKillInstanceCanceledWhileBusy(t *testing.T) {
	ctx := context.Background()
	s, _, m, _, _ := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	release, err := s.acquireOperation(ctx, items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.KillInstance(cancelled, items[0].ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("busy kill err=%v", err)
	}
}
