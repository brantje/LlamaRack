package lifecycle

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

func instanceID(t *testing.T, s *Service, modelID string) string {
	t.Helper()
	items, err := s.instances.ListByModel(context.Background(), modelID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	return items[0].ID
}

func waitPendingCount(t *testing.T, s *Service, id string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.Activity(id).PendingRequests == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("pending=%d want=%d", s.Activity(id).PendingRequests, want)
}

func TestPendingAdmissionRejectsOverPerInstanceLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, _, m, _, _ := setupLifecycle(t, true, false)
	id := instanceID(t, s, m.ID)
	s.SetPendingLimits(func(context.Context) (int, int) { return 2, 100 })
	hold := make(chan struct{})
	s.SetLoadHold(func(string) { <-hold })

	type acquired struct {
		release func()
		err     error
	}
	results := make(chan acquired, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, release, err := s.Acquire(ctx, id)
			results <- acquired{release: release, err: err}
		}()
	}
	waitPendingCount(t, s, id, 2)
	_, release, err := s.Acquire(ctx, id)
	if !errors.Is(err, ErrQueueLimitExceeded) || release != nil {
		t.Fatalf("expected instance queue limit, release=%v err=%v", release != nil, err)
	}
	if QueueLimitScope(err) != queueLimitScopeInstance {
		t.Fatalf("scope=%q", QueueLimitScope(err))
	}
	if err.Error() != "too many pending requests for this model" {
		t.Fatalf("instance message=%q", err.Error())
	}
	if s.Activity(id).PendingRequests != 2 {
		t.Fatalf("pending after reject=%+v", s.Activity(id))
	}
	s.mu.Lock()
	loads := len(s.loads)
	s.mu.Unlock()
	if loads != 1 {
		t.Fatalf("rejected request started extra load calls=%d", loads)
	}

	close(hold)
	for i := 0; i < 2; i++ {
		got := <-results
		if got.err != nil {
			t.Fatalf("admitted waiter err=%v", got.err)
		}
		got.release()
	}
	if activity := s.Activity(id); activity.PendingRequests != 0 || activity.ActiveRequests != 0 {
		t.Fatalf("released activity=%+v", activity)
	}
}

func TestPendingAdmissionInstanceOverrideAndInherit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	t.Run("override tighter than manager default", func(t *testing.T) {
		s, _, m, _, exec := setupLifecycle(t, true, false)
		id := instanceID(t, s, m.ID)
		s.SetPendingLimits(func(context.Context) (int, int) { return 5, 100 })
		exec("UPDATE instances SET max_pending_requests=1 WHERE id=?", id)
		hold := make(chan struct{})
		s.SetLoadHold(func(string) { <-hold })
		done := make(chan error, 1)
		go func() {
			_, release, err := s.Acquire(ctx, id)
			if release != nil {
				release()
			}
			done <- err
		}()
		waitPendingCount(t, s, id, 1)
		_, _, err := s.Acquire(ctx, id)
		if !errors.Is(err, ErrQueueLimitExceeded) || QueueLimitScope(err) != queueLimitScopeInstance {
			t.Fatalf("err=%v scope=%q", err, QueueLimitScope(err))
		}
		close(hold)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
	t.Run("zero inherits manager default", func(t *testing.T) {
		s, _, m, _, _ := setupLifecycle(t, true, false)
		id := instanceID(t, s, m.ID)
		s.SetPendingLimits(func(context.Context) (int, int) { return 1, 100 })
		hold := make(chan struct{})
		s.SetLoadHold(func(string) { <-hold })
		done := make(chan error, 1)
		go func() {
			_, release, err := s.Acquire(ctx, id)
			if release != nil {
				release()
			}
			done <- err
		}()
		waitPendingCount(t, s, id, 1)
		_, _, err := s.Acquire(ctx, id)
		if !errors.Is(err, ErrQueueLimitExceeded) {
			t.Fatalf("inherited limit not applied: %v", err)
		}
		close(hold)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestPendingAdmissionGlobalLimitAcrossInstances(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	s, _, m, sup, _ := setupLifecycle(t, true, false)
	first := instanceID(t, s, m.ID)
	enabled, autoload, eviction := true, true, true
	second, err := s.Instances().Create(ctx, instances.CreateInput{
		ModelID: m.ID, Name: "Second cold", Enabled: &enabled, Autoload: &autoload, EvictionEnabled: &eviction,
	})
	if err != nil {
		t.Fatal(err)
	}
	third, err := s.Instances().Create(ctx, instances.CreateInput{
		ModelID: m.ID, Name: "Third cold", Enabled: &enabled, Autoload: &autoload, EvictionEnabled: &eviction,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.SetPendingLimits(func(context.Context) (int, int) { return 10, 2 })
	hold := make(chan struct{})
	s.SetLoadHold(func(string) { <-hold })

	type acquired struct {
		release func()
		err     error
	}
	results := make(chan acquired, 2)
	go func() {
		_, release, err := s.Acquire(ctx, first)
		results <- acquired{release: release, err: err}
	}()
	waitPendingCount(t, s, first, 1)
	go func() {
		_, release, err := s.Acquire(ctx, second.ID)
		results <- acquired{release: release, err: err}
	}()
	waitPendingCount(t, s, second.ID, 1)
	_, release, err := s.Acquire(ctx, third.ID)
	if !errors.Is(err, ErrQueueLimitExceeded) || QueueLimitScope(err) != queueLimitScopeGlobal || release != nil {
		t.Fatalf("expected global limit err=%v release=%v", err, release != nil)
	}
	if err.Error() != "too many pending requests across the manager" {
		t.Fatalf("global message=%q", err.Error())
	}
	if got := sup.Status(third.ID).State; got != supervisor.Unloaded {
		t.Fatalf("rejected instance started: %+v", sup.Status(third.ID))
	}

	close(hold)
	for i := 0; i < 2; i++ {
		got := <-results
		if got.err != nil {
			t.Fatalf("admitted waiter err=%v", got.err)
		}
		got.release()
	}
}

func TestPendingAdmissionGlobalBindsWhenInstanceOverrideHigher(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, _, m, _, exec := setupLifecycle(t, true, false)
	id := instanceID(t, s, m.ID)
	exec("UPDATE instances SET max_pending_requests=50 WHERE id=?", id)
	s.SetPendingLimits(func(context.Context) (int, int) { return 1, 1 })
	hold := make(chan struct{})
	s.SetLoadHold(func(string) { <-hold })
	done := make(chan error, 1)
	go func() {
		_, release, err := s.Acquire(ctx, id)
		if release != nil {
			release()
		}
		done <- err
	}()
	waitPendingCount(t, s, id, 1)
	_, release, err := s.Acquire(ctx, id)
	if !errors.Is(err, ErrQueueLimitExceeded) || QueueLimitScope(err) != queueLimitScopeGlobal || release != nil {
		t.Fatalf("expected global cap to bind over instance override err=%v release=%v", err, release != nil)
	}
	close(hold)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPendingAdmissionCancelReleasesSlot(t *testing.T) {
	s, _, m, _, _ := setupLifecycle(t, true, false)
	id := instanceID(t, s, m.ID)
	s.SetPendingLimits(func(context.Context) (int, int) { return 2, 100 })
	hold := make(chan struct{})
	s.SetLoadHold(func(string) { <-hold })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, release, err := s.Acquire(ctx, id)
		if release != nil {
			release()
		}
		done <- err
	}()
	waitPendingCount(t, s, id, 1)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	waitPendingCount(t, s, id, 0)
	close(hold)
}

func TestPendingAdmissionStartupFailureReleasesWaiters(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, _, m, _, exec := setupLifecycle(t, true, false)
	id := instanceID(t, s, m.ID)
	s.SetPendingLimits(func(context.Context) (int, int) { return 4, 100 })
	hold := make(chan struct{})
	s.SetLoadHold(func(string) { <-hold })
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, release, err := s.Acquire(ctx, id)
			if release != nil {
				release()
			}
			results <- err
		}()
	}
	waitPendingCount(t, s, id, 2)
	exec("UPDATE instances SET enabled=0 WHERE id=?", id)
	close(hold)
	for i := 0; i < 2; i++ {
		if err := <-results; err == nil {
			t.Fatal("expected startup failure")
		}
	}
	activity := s.Activity(id)
	if activity.PendingRequests != 0 || activity.ActiveRequests != 0 {
		t.Fatalf("failed start activity=%+v", activity)
	}
}

func TestPendingToActiveTransitionAndRelease(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, _, m, _, _ := setupLifecycle(t, true, false)
	id := instanceID(t, s, m.ID)
	_, release, err := s.Acquire(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	activity := s.Activity(id)
	if activity.PendingRequests != 0 || activity.ActiveRequests != 1 {
		t.Fatalf("activated activity=%+v", activity)
	}
	release()
	release()
	activity = s.Activity(id)
	if activity.PendingRequests != 0 || activity.ActiveRequests != 0 {
		t.Fatalf("completed activity=%+v", activity)
	}
}

func TestConcurrentPendingAdmissionNeverExceedsBound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, _, m, _, _ := setupLifecycle(t, true, false)
	id := instanceID(t, s, m.ID)
	const limit = 5
	const callers = 40
	s.SetPendingLimits(func(context.Context) (int, int) { return limit, 1000 })
	hold := make(chan struct{})
	s.SetLoadHold(func(string) { <-hold })
	var rejected atomic.Int32
	var wg sync.WaitGroup
	releases := make(chan func(), callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, release, err := s.Acquire(ctx, id)
			if errors.Is(err, ErrQueueLimitExceeded) {
				rejected.Add(1)
				return
			}
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			releases <- release
		}()
	}
	waitPendingCount(t, s, id, limit)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && int(rejected.Load())+s.Activity(id).PendingRequests < callers {
		time.Sleep(5 * time.Millisecond)
	}
	if pending := s.Activity(id).PendingRequests; pending != limit {
		t.Fatalf("pending=%d want=%d", pending, limit)
	}
	if int(rejected.Load()) != callers-limit {
		t.Fatalf("rejected=%d want=%d", rejected.Load(), callers-limit)
	}
	close(hold)
	wg.Wait()
	close(releases)
	for release := range releases {
		release()
	}
}

func TestQueueLimitScopeIgnoresUnrelatedErrors(t *testing.T) {
	if QueueLimitScope(errors.New("unrelated")) != "" {
		t.Fatal("unrelated errors must not report a queue-limit scope")
	}
}

func TestPendingDemandBlocksEvictionAndIdleUnload(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, victim, sup := startedInstance(t, true)
	s.mu.Lock()
	activity := s.activities[victim.ID]
	activity.PendingRequests = 1
	activity.LastUsed = s.now().UTC()
	s.activities[victim.ID] = activity
	s.mu.Unlock()
	if err := s.claimEviction(ctx, victim.ID); !errors.Is(err, errEvictionIneligible) {
		t.Fatalf("pending instance remained evictable: %v", err)
	}
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	s.touch(victim.ID)
	s.mu.Lock()
	activity = s.activities[victim.ID]
	activity.PendingRequests = 1
	s.activities[victim.ID] = activity
	s.mu.Unlock()
	now = now.Add(10 * time.Minute)
	s.ReconcileIdle(ctx, time.Minute)
	if got := sup.Status(victim.ID).State; got != supervisor.Ready {
		t.Fatalf("pending instance was idle-unloaded: %+v", sup.Status(victim.ID))
	}
}