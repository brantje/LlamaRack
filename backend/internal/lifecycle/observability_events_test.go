package lifecycle

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
)

type recordedLifecycleEvent struct {
	event      string
	instanceID string
	duration   time.Duration
}

func collectLifecycleEvents(s *Service) (*[]recordedLifecycleEvent, *sync.Mutex) {
	var mu sync.Mutex
	events := []recordedLifecycleEvent{}
	s.SetObservabilityRecorder(func(_ context.Context, event, instanceID string, duration time.Duration) error {
		mu.Lock()
		events = append(events, recordedLifecycleEvent{event: event, instanceID: instanceID, duration: duration})
		mu.Unlock()
		return nil
	})
	return &events, &mu
}

func hasLifecycleEvent(events *[]recordedLifecycleEvent, mu *sync.Mutex, event, instanceID string) (recordedLifecycleEvent, bool) {
	mu.Lock()
	defer mu.Unlock()
	for _, item := range *events {
		if item.event == event && item.instanceID == instanceID {
			return item, true
		}
	}
	return recordedLifecycleEvent{}, false
}

func TestInferenceAutoloadAndLoadRecordExactObservabilityEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, ms, m, sup, _ := setupLifecycle(t, true, false)
	instances, err := ms.Instances(ctx, m.ID)
	if err != nil || len(instances) != 1 {
		t.Fatalf("instances=%+v err=%v", instances, err)
	}
	instanceID := instances[0].ID
	events, mu := collectLifecycleEvents(s)

	endpoint, release, err := s.Acquire(ctx, instanceID)
	if err != nil || endpoint == "" || release == nil {
		t.Fatalf("acquire endpoint=%q release=%v err=%v", endpoint, release != nil, err)
	}
	release()
	waitForRuntimeState(t, sup, instanceID, supervisor.Ready)

	if _, ok := hasLifecycleEvent(events, mu, ObservabilityAutoload, instanceID); !ok {
		t.Fatalf("autoload event missing: %+v", *events)
	}
	load, ok := hasLifecycleEvent(events, mu, ObservabilityLoad, instanceID)
	if !ok || load.duration <= 0 {
		t.Fatalf("load event=%+v events=%+v", load, *events)
	}
	logs := strings.Join(s.Logs(instanceID), "\n")
	if !strings.Contains(logs, "autoload triggered by inference request") || !strings.Contains(logs, "worker ready after") {
		t.Fatalf("logs=%q", logs)
	}
}

func TestFailedStartRecordsExactObservabilityEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, ms, m, _, _ := setupLifecycle(t, true, false)
	instances, err := ms.Instances(ctx, m.ID)
	if err != nil || len(instances) != 1 {
		t.Fatalf("instances=%+v err=%v", instances, err)
	}
	instanceID := instances[0].ID

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	s.sup = supervisor.New(filepath.Join(t.TempDir(), "missing-llama-server"), "127.0.0.1", port, time.Second)
	events, mu := collectLifecycleEvents(s)

	if _, err := s.EnsureReady(ctx, instanceID); err == nil {
		t.Fatal("expected missing llama-server executable to fail startup")
	}
	failed, ok := hasLifecycleEvent(events, mu, ObservabilityFailedStart, instanceID)
	if !ok || failed.duration < 0 {
		t.Fatalf("failed event=%+v events=%+v", failed, *events)
	}
	if logs := strings.Join(s.Logs(instanceID), "\n"); !strings.Contains(logs, "worker failed to start:") {
		t.Fatalf("logs=%q", logs)
	}
}

func TestIdleUnloadRecordsExactObservabilityEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, ms, m, sup, _ := setupLifecycle(t, true, false)
	instances, err := ms.Instances(ctx, m.ID)
	if err != nil || len(instances) != 1 {
		t.Fatalf("instances=%+v err=%v", instances, err)
	}
	instanceID := instances[0].ID

	var event, eventInstance string
	s.SetObservabilityRecorder(func(_ context.Context, gotEvent, gotInstance string, _ time.Duration) error {
		event, eventInstance = gotEvent, gotInstance
		return nil
	})
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	if _, err := s.StartModel(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	waitForRuntimeState(t, sup, instanceID, supervisor.Ready)
	now = now.Add(2 * time.Minute)
	s.ReconcileIdle(ctx, time.Minute)
	waitForRuntimeState(t, sup, instanceID, supervisor.Unloaded)
	if event != ObservabilityIdleUnload || eventInstance != instanceID {
		t.Fatalf("event=%q instance=%q", event, eventInstance)
	}
	if logs := strings.Join(s.Logs(instanceID), "\n"); !strings.Contains(logs, "idle-unloaded after 1m0s") {
		t.Fatalf("logs=%q", logs)
	}
}

func TestEvictionRecordsExactObservabilityEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, ms, m, sup, _ := setupLifecycle(t, true, false)
	instances, err := ms.Instances(ctx, m.ID)
	if err != nil || len(instances) != 1 {
		t.Fatalf("instances=%+v err=%v", instances, err)
	}
	instanceID := instances[0].ID
	var event, eventInstance string
	s.SetObservabilityRecorder(func(_ context.Context, gotEvent, gotInstance string, _ time.Duration) error {
		event, eventInstance = gotEvent, gotInstance
		return nil
	})
	if _, err := s.StartModel(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	waitForRuntimeState(t, sup, instanceID, supervisor.Ready)
	if err := s.evictInstance(ctx, instanceID); err != nil {
		t.Fatal(err)
	}
	waitForRuntimeState(t, sup, instanceID, supervisor.Unloaded)
	if event != ObservabilityEviction || eventInstance != instanceID {
		t.Fatalf("event=%q instance=%q", event, eventInstance)
	}
	if logs := strings.Join(s.Logs(instanceID), "\n"); !strings.Contains(logs, "evicted for resource pressure") {
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
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
	s.SetObservabilityRecorder(nil)
	s.recordObservabilityEvent(context.Background(), ObservabilityIdleUnload, "one", 0)
	if calls != 1 {
		t.Fatalf("nil recorder should not be called: %d", calls)
	}
	s.AddManagerLog("one", " manager line ")
	if got := strings.Join(s.Logs("one"), "\n"); !strings.Contains(got, "manager line") {
		t.Fatalf("logs=%q", got)
	}
}
