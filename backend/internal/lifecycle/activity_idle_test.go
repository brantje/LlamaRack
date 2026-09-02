package lifecycle

import (
	"context"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/supervisor"
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
	release()
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

func TestIdleUnloadUsesPerInstanceOverrideAndGlobalFallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	t.Run("override works when global is disabled", func(t *testing.T) {
		s, ms, m, sup, exec := setupLifecycle(t, true, false)
		exec("UPDATE instances SET idle_unload_seconds=60 WHERE model_id=?", m.ID)
		instances, err := ms.Instances(ctx, m.ID)
		if err != nil || len(instances) != 1 {
			t.Fatalf("instances=%+v err=%v", instances, err)
		}
		now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
		s.now = func() time.Time { return now }
		if _, err := s.StartModel(ctx, m.ID); err != nil {
			t.Fatal(err)
		}
		now = now.Add(61 * time.Second)
		s.ReconcileIdle(ctx, 0)
		waitForRuntimeState(t, sup, instances[0].ID, supervisor.Unloaded)
	})
	t.Run("zero inherits global", func(t *testing.T) {
		s, ms, m, sup, _ := setupLifecycle(t, true, false)
		instances, err := ms.Instances(ctx, m.ID)
		if err != nil || len(instances) != 1 {
			t.Fatalf("instances=%+v err=%v", instances, err)
		}
		now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
		s.now = func() time.Time { return now }
		if _, err := s.StartModel(ctx, m.ID); err != nil {
			t.Fatal(err)
		}
		now = now.Add(30 * time.Second)
		s.ReconcileIdle(ctx, time.Minute)
		if got := sup.Status(instances[0].ID).State; got != supervisor.Ready {
			t.Fatalf("instance unloaded before inherited timeout: %+v", sup.Status(instances[0].ID))
		}
		now = now.Add(31 * time.Second)
		s.ReconcileIdle(ctx, time.Minute)
		waitForRuntimeState(t, sup, instances[0].ID, supervisor.Unloaded)
	})
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
		t.Fatalf("always-on instance was idle-unloaded: %+v", sup.Status(instanceID))
	}
}

func TestEvictionPlanUsesActivityAlwaysOnAndInstancePolicy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	t.Run("inactive instance eligible unless eviction disabled", func(t *testing.T) {
		s, _, m, _, exec := setupLifecycle(t, true, false)
		s.hardware = abundantSingleGPUHardware()
		if _, err := s.StartModel(ctx, m.ID); err != nil {
			t.Fatal(err)
		}
		s.hardware = insufficientGPUHardware()
		exec("UPDATE models SET total_bytes=? WHERE id=?", 2*testGiB, m.ID)
		plan, err := s.EvictionPlan(ctx, testGiB)
		if err != nil || !plan.Fits || len(plan.Evict) != 1 || plan.Evict[0].ModelID != m.ID {
			t.Fatalf("inactive plan=%+v err=%v", plan, err)
		}
		exec("UPDATE instances SET eviction_enabled=0 WHERE model_id=?", m.ID)
		plan, err = s.EvictionPlan(ctx, 1)
		if err != nil || plan.Fits || len(plan.Evict) != 0 {
			t.Fatalf("eviction-disabled plan=%+v err=%v", plan, err)
		}
	})
	t.Run("active instance protected", func(t *testing.T) {
		s, _, m, _, _ := setupLifecycle(t, true, false)
		s.hardware = abundantSingleGPUHardware()
		if _, err := s.StartModel(ctx, m.ID); err != nil {
			t.Fatal(err)
		}
		_, release, err := s.Acquire(ctx, m.PublicID)
		if err != nil {
			t.Fatal(err)
		}
		s.hardware = insufficientGPUHardware()
		plan, err := s.EvictionPlan(ctx, 1)
		release()
		if err != nil || plan.Fits || len(plan.Evict) != 0 {
			t.Fatalf("active plan=%+v err=%v", plan, err)
		}
	})
	t.Run("always-on follows eviction policy", func(t *testing.T) {
		s, _, m, _, exec := setupLifecycle(t, true, true)
		s.hardware = abundantSingleGPUHardware()
		if _, err := s.StartModel(ctx, m.ID); err != nil {
			t.Fatal(err)
		}
		s.hardware = insufficientGPUHardware()
		exec("UPDATE models SET total_bytes=? WHERE id=?", 2*testGiB, m.ID)
		plan, err := s.EvictionPlan(ctx, testGiB)
		if err != nil || !plan.Fits || len(plan.Evict) != 1 || !plan.Evict[0].AlwaysOn {
			t.Fatalf("evictable always-on plan=%+v err=%v", plan, err)
		}
		exec("UPDATE instances SET eviction_enabled=0 WHERE model_id=?", m.ID)
		plan, err = s.EvictionPlan(ctx, 1)
		if err != nil || plan.Fits || len(plan.Evict) != 0 {
			t.Fatalf("protected always-on plan=%+v err=%v", plan, err)
		}
	})
}

func TestRunIdleReconcilerStopsWithContext(t *testing.T) {
	for _, timeout := range []time.Duration{0, 2 * time.Second} {
		t.Run(timeout.String(), func(t *testing.T) {
			s, _, _, _, _ := setupLifecycle(t, false, false)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { s.RunIdleReconciler(ctx, timeout); close(done) }()
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("idle reconciler did not stop")
			}
		})
	}
}
