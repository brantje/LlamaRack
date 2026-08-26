package lifecycle

import (
	"context"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
)

func TestAcquireTracksInferenceActivity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, ms, m, _, _ := setupLifecycle(t, true, false)

	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	endpoint, release, err := s.Acquire(ctx, m.PublicID)
	if err != nil || endpoint == "" || release == nil {
		t.Fatalf("acquire endpoint=%q release=%v err=%v", endpoint, release != nil, err)
	}
	activity := s.Activity(m.ID)
	if activity.ActiveRequests != 1 || !activity.LastUsed.Equal(now) {
		t.Fatalf("active activity=%+v", activity)
	}
	instances, err := ms.Instances(ctx, m.ID)
	if err != nil || len(instances) != 1 {
		t.Fatalf("instances=%+v err=%v", instances, err)
	}

	now = now.Add(2 * time.Minute)
	release()
	release() // release is intentionally idempotent.
	activity = s.Activity(m.ID)
	if activity.ActiveRequests != 0 || !activity.LastUsed.Equal(now) {
		t.Fatalf("released activity=%+v", activity)
	}

	if got := s.sup.Status(instances[0].ID).State; got != supervisor.Ready {
		t.Fatalf("runtime state=%s", got)
	}
}

func TestAcquireFailureReleasesActivity(t *testing.T) {
	ctx := context.Background()
	s, _, m, _, _ := setupLifecycle(t, false, false)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	if _, release, err := s.Acquire(ctx, m.PublicID); err == nil || release != nil {
		t.Fatalf("expected failed acquire, release=%v err=%v", release != nil, err)
	}
	activity := s.Activity(m.ID)
	if activity.ActiveRequests != 0 || !activity.LastUsed.Equal(now) {
		t.Fatalf("failed acquire activity=%+v", activity)
	}
}

func TestIdleUnloadStopsInactiveModelButNotActiveRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, ms, m, sup, _ := setupLifecycle(t, true, false)
	instances, err := ms.Instances(ctx, m.ID)
	if err != nil || len(instances) != 1 {
		t.Fatalf("instances=%+v err=%v", instances, err)
	}
	instanceID := instances[0].ID

	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	_, release, err := s.Acquire(ctx, m.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	waitForRuntimeState(t, sup, instanceID, supervisor.Ready)

	now = now.Add(10 * time.Minute)
	s.ReconcileIdle(ctx, time.Minute)
	if got := sup.Status(instanceID).State; got != supervisor.Ready {
		t.Fatalf("active request was idle-unloaded: %+v", sup.Status(instanceID))
	}

	release()
	now = now.Add(2 * time.Minute)
	s.ReconcileIdle(ctx, time.Minute)
	waitForRuntimeState(t, sup, instanceID, supervisor.Unloaded)
}

func TestIdleUnloadSkipsAlwaysOnAndDisabledTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	s, ms, m, sup, _ := setupLifecycle(t, true, true)
	instances, err := ms.Instances(ctx, m.ID)
	if err != nil || len(instances) != 1 {
		t.Fatalf("instances=%+v err=%v", instances, err)
	}
	instanceID := instances[0].ID

	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	if _, err := s.StartModel(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	waitForRuntimeState(t, sup, instanceID, supervisor.Ready)
	now = now.Add(24 * time.Hour)

	s.ReconcileIdle(ctx, 0)
	s.ReconcileIdle(ctx, time.Second)
	if got := sup.Status(instanceID).State; got != supervisor.Ready {
		t.Fatalf("always-on model was idle-unloaded: %+v", sup.Status(instanceID))
	}
}

func TestEvictionPlanUsesActivityAndAlwaysOnProtection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	t.Run("inactive model eligible", func(t *testing.T) {
		s, _, m, _, _ := setupLifecycle(t, true, false)
		if _, err := s.StartModel(ctx, m.ID); err != nil {
			t.Fatal(err)
		}
		plan, err := s.EvictionPlan(ctx, 1)
		if err != nil || !plan.Fits || len(plan.Evict) != 1 || plan.Evict[0].ModelID != m.ID {
			t.Fatalf("inactive plan=%+v err=%v", plan, err)
		}

		_, release, err := s.Acquire(ctx, m.PublicID)
		if err != nil {
			t.Fatal(err)
		}
		plan, err = s.EvictionPlan(ctx, 1)
		release()
		if err != nil || plan.Fits || len(plan.Evict) != 0 {
			t.Fatalf("active plan=%+v err=%v", plan, err)
		}
	})

	t.Run("always-on protected", func(t *testing.T) {
		s, _, m, _, _ := setupLifecycle(t, true, true)
		if _, err := s.StartModel(ctx, m.ID); err != nil {
			t.Fatal(err)
		}
		plan, err := s.EvictionPlan(ctx, 1)
		if err != nil || plan.Fits || len(plan.Evict) != 0 {
			t.Fatalf("always-on plan=%+v err=%v", plan, err)
		}
	})
}

func TestRunIdleReconcilerStopsWithContext(t *testing.T) {
	for _, timeout := range []time.Duration{0, 2 * time.Second} {
		t.Run(timeout.String(), func(t *testing.T) {
			s, _, _, _, _ := setupLifecycle(t, false, false)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				s.RunIdleReconciler(ctx, timeout)
				close(done)
			}()
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("idle reconciler did not stop")
			}
		})
	}
}
