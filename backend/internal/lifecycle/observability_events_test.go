package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
)

func TestIdleUnloadRecordsExactObservabilityEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, ms, m, sup, _ := setupLifecycle(t, true, false)
	instances, err := ms.Instances(ctx, m.ID)
	if err != nil || len(instances) != 1 { t.Fatalf("instances=%+v err=%v", instances, err) }
	instanceID := instances[0].ID

	var event, eventInstance string
	s.SetObservabilityRecorder(func(_ context.Context, gotEvent, gotInstance string, _ time.Duration) error {
		event, eventInstance = gotEvent, gotInstance
		return nil
	})
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	if _, err := s.StartModel(ctx, m.ID); err != nil { t.Fatal(err) }
	waitForRuntimeState(t, sup, instanceID, supervisor.Ready)
	now = now.Add(2 * time.Minute)
	s.ReconcileIdle(ctx, time.Minute)
	waitForRuntimeState(t, sup, instanceID, supervisor.Unloaded)
	if event != ObservabilityIdleUnload || eventInstance != instanceID { t.Fatalf("event=%q instance=%q", event, eventInstance) }
	if logs := strings.Join(s.Logs(instanceID), "\n"); !strings.Contains(logs, "[manager] idle-unloaded after 1m0s") {
		t.Fatalf("logs=%q", logs)
	}
}

func TestEvictionRecordsExactObservabilityEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, ms, m, sup, _ := setupLifecycle(t, true, false)
	instances, err := ms.Instances(ctx, m.ID)
	if err != nil || len(instances) != 1 { t.Fatalf("instances=%+v err=%v", instances, err) }
	instanceID := instances[0].ID
	var event, eventInstance string
	s.SetObservabilityRecorder(func(_ context.Context, gotEvent, gotInstance string, _ time.Duration) error {
		event, eventInstance = gotEvent, gotInstance
		return nil
	})
	if _, err := s.StartModel(ctx, m.ID); err != nil { t.Fatal(err) }
	waitForRuntimeState(t, sup, instanceID, supervisor.Ready)
	if err := s.evictInstance(ctx, instanceID); err != nil { t.Fatal(err) }
	waitForRuntimeState(t, sup, instanceID, supervisor.Unloaded)
	if event != ObservabilityEviction || eventInstance != instanceID { t.Fatalf("event=%q instance=%q", event, eventInstance) }
	if logs := strings.Join(s.Logs(instanceID), "\n"); !strings.Contains(logs, "[manager] evicted for resource pressure") {
		t.Fatalf("logs=%q", logs)
	}
}

func TestObservabilityRecorderFailuresAreNonFatal(t *testing.T) {
	s, _, _, _, _ := setupLifecycle(t, false, false)
	calls := 0
	s.SetObservabilityRecorder(func(context.Context, string, string, time.Duration) error {
		calls++
		return errors.New("database unavailable")
	})
	s.recordObservabilityEvent(context.Background(), ObservabilityEviction, "one", time.Second)
	if calls != 1 { t.Fatalf("calls=%d", calls) }
	s.SetObservabilityRecorder(nil)
	s.recordObservabilityEvent(context.Background(), ObservabilityIdleUnload, "one", 0)
	if calls != 1 { t.Fatalf("nil recorder should not be called: %d", calls) }
	s.AddManagerLog("one", " manager line ")
	if got := strings.Join(s.Logs("one"), "\n"); got != "[manager] manager line" { t.Fatalf("logs=%q", got) }
}
