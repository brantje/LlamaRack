package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/supervisor"
	"github.com/brantje/llamarack/backend/internal/systemlog"
)

func TestStartFailureBackoffSchedule(t *testing.T) {
	want := []time.Duration{15 * time.Second, 30 * time.Second, 60 * time.Second, 2 * time.Minute, 5 * time.Minute, 5 * time.Minute}
	for i, delay := range want {
		if got := startFailureBackoffForCount(i + 1); got != delay {
			t.Fatalf("failures=%d backoff=%s want=%s", i+1, got, delay)
		}
	}
	if got := startFailureBackoffForCount(0); got != 15*time.Second {
		t.Fatalf("zero failures backoff=%s", got)
	}
}

func TestRecordStartFailureIncreasesCooldownAndLogs(t *testing.T) {
	systemlog.Default.Reset()
	defer systemlog.Default.Reset()
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	s := &Service{now: func() time.Time { return now }}

	s.recordStartFailure("coder", "exec: no such file")
	first, ok := s.startFailureState("coder")
	if !ok || first.ConsecutiveFailures != 1 || first.LastError != "exec: no such file" || !first.RetryAfter.Equal(now.Add(15*time.Second)) {
		t.Fatalf("first state=%+v ok=%v", first, ok)
	}
	if !s.inStartBackoff("coder") {
		t.Fatal("expected backoff after first failure")
	}
	logs := systemlog.Default.Snapshot(10)
	if len(logs) != 1 || logs[0].Level != systemlog.Warn || logs[0].Message != "coder: startup backoff 15s after 1 consecutive start failure" {
		t.Fatalf("logs=%+v", logs)
	}

	s.recordStartFailure("coder", "worker did not reach ready state")
	second, _ := s.startFailureState("coder")
	if second.ConsecutiveFailures != 2 || !second.RetryAfter.Equal(now.Add(30*time.Second)) {
		t.Fatalf("second state=%+v", second)
	}
	s.recordStartFailure("coder", "timeout")
	s.recordStartFailure("coder", "timeout")
	s.recordStartFailure("coder", "timeout")
	fifth, _ := s.startFailureState("coder")
	if fifth.ConsecutiveFailures != 5 || !fifth.RetryAfter.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("capped state=%+v", fifth)
	}
	if formatStartBackoff(2*time.Minute) != "2m" || formatStartBackoff(15*time.Second) != "15s" || formatStartBackoff(90*time.Second) != "1m30s" {
		t.Fatalf("format=%q %q %q", formatStartBackoff(2*time.Minute), formatStartBackoff(15*time.Second), formatStartBackoff(90*time.Second))
	}
	var nilService *Service
	_ = nilService.clock()
	plain := &Service{}
	if plain.clock().IsZero() {
		t.Fatal("clock without now should use wall time")
	}
	if _, ok := nilService.startFailureState("x"); ok {
		t.Fatal("nil service reported failure state")
	}
	if !errors.Is(plain.startBackoffError("missing"), errStartBackoff) {
		t.Fatal("missing state should still wrap errStartBackoff")
	}
	plain.startFailures = map[string]StartFailureState{"quiet": {ConsecutiveFailures: 1, RetryAfter: now.Add(time.Second)}}
	err := plain.startBackoffError("quiet")
	if !errors.Is(err, errStartBackoff) || !strings.Contains(err.Error(), "1 consecutive start failures") {
		t.Fatalf("quiet backoff err=%v", err)
	}
}

func TestStartFailureResetOverrideAndStopDoNotCount(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	s := &Service{now: func() time.Time { return now }, manuallyStopped: map[string]bool{}}
	s.recordStartFailure("one", "boom")
	s.overrideStartBackoff("one")
	state, ok := s.startFailureState("one")
	if !ok || state.ConsecutiveFailures != 1 || !state.RetryAfter.IsZero() || s.inStartBackoff("one") {
		t.Fatalf("override state=%+v ok=%v backoff=%v", state, ok, s.inStartBackoff("one"))
	}
	s.resetStartFailures("one")
	if _, ok := s.startFailureState("one"); ok {
		t.Fatal("reset retained state")
	}
	s.recordStartFailure("two", "boom")
	s.resetStartFailures("two")
	if s.inStartBackoff("two") {
		t.Fatal("stop/reset left backoff active")
	}
	s.resetStartFailures("missing")
	s.overrideStartBackoff("missing")
}

func TestRuntimeOmitsZeroRetryAfter(t *testing.T) {
	s := &Service{now: func() time.Time { return time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC) }}
	data, err := json.Marshal(s.attachStartFailure(supervisor.Runtime{InstanceID: "one", State: supervisor.Failed}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "retry_after") {
		t.Fatalf("zero retry_after encoded=%s", data)
	}
	s.recordStartFailure("one", "boom")
	data, err = json.Marshal(s.attachStartFailure(supervisor.Runtime{InstanceID: "one", State: supervisor.Failed}))
	if err != nil || !strings.Contains(string(data), `"retry_after"`) {
		t.Fatalf("active retry_after encoded=%s err=%v", data, err)
	}
}

func TestAttachStartFailurePrefersRecordedLastError(t *testing.T) {
	s := &Service{now: func() time.Time { return time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC) }}
	s.startFailures = map[string]StartFailureState{
		"stale":  {ConsecutiveFailures: 2, LastError: "CUDA allocation failed"},
		"empty":  {ConsecutiveFailures: 1},
		"filled": {ConsecutiveFailures: 1, LastError: "exec: missing"},
	}

	replaced := s.attachStartFailure(supervisor.Runtime{InstanceID: "stale", LastError: "worker exited unexpectedly"})
	if replaced.LastError != "CUDA allocation failed" {
		t.Fatalf("stale runtime last_error=%q", replaced.LastError)
	}
	kept := s.attachStartFailure(supervisor.Runtime{InstanceID: "empty", LastError: "worker exited unexpectedly"})
	if kept.LastError != "worker exited unexpectedly" {
		t.Fatalf("empty state last_error=%q", kept.LastError)
	}
	filled := s.attachStartFailure(supervisor.Runtime{InstanceID: "filled"})
	if filled.LastError != "exec: missing" {
		t.Fatalf("empty runtime last_error=%q", filled.LastError)
	}
	untouched := s.attachStartFailure(supervisor.Runtime{InstanceID: "unknown", LastError: "keep me"})
	if untouched.LastError != "keep me" {
		t.Fatalf("missing state last_error=%q", untouched.LastError)
	}
}

func TestStartFailureStateIsConcurrencySafe(t *testing.T) {
	s := &Service{now: func() time.Time { return time.Unix(1, 0).UTC() }}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.recordStartFailure("shared", "boom")
			_ = s.inStartBackoff("shared")
			_ = s.attachStartFailure(supervisor.Runtime{InstanceID: "shared"})
			s.overrideStartBackoff("shared")
			s.recordStartFailure("other", "boom")
		}()
	}
	wg.Wait()
	state, ok := s.startFailureState("shared")
	if !ok || state.ConsecutiveFailures != 32 {
		t.Fatalf("shared state=%+v ok=%v", state, ok)
	}
}

func syncedClock(t *testing.T, start time.Time) (now func() time.Time, advance func(time.Duration)) {
	t.Helper()
	var mu sync.Mutex
	current := start
	return func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			return current
		}, func(d time.Duration) {
			mu.Lock()
			current = current.Add(d)
			mu.Unlock()
		}
}

func freePort(t *testing.T) int {
	t.Helper()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func attachBrokenSupervisor(t *testing.T, s *Service) *supervisor.Supervisor {
	t.Helper()
	broken := supervisor.New(filepath.Join(t.TempDir(), "missing-llama-server"), "127.0.0.1", freePort(t), time.Second)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		broken.Shutdown(ctx)
	})
	s.sup = broken
	return broken
}

func waitStartFailures(t *testing.T, s *Service, id string, min int) StartFailureState {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if state, ok := s.startFailureState(id); ok && state.ConsecutiveFailures >= min {
			return state
		}
		time.Sleep(5 * time.Millisecond)
	}
	state, ok := s.startFailureState(id)
	t.Fatalf("start failures=%+v ok=%v want>=%d", state, ok, min)
	return state
}

func TestRepeatedAlwaysOnFailuresIncreaseCooldownAndSkipReconcile(t *testing.T) {
	ctx := context.Background()
	s, _, m, _, _ := setupLifecycle(t, true, true)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	id := items[0].ID
	start := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	nowFn, advance := syncedClock(t, start)
	s.now = nowFn
	attachBrokenSupervisor(t, s)

	s.ReconcileAlwaysOn(ctx)
	first := waitStartFailures(t, s, id, 1)
	if !first.RetryAfter.Equal(start.Add(15 * time.Second)) {
		t.Fatalf("first retry=%s", first.RetryAfter)
	}

	s.ReconcileAlwaysOn(ctx)
	time.Sleep(20 * time.Millisecond)
	if state, _ := s.startFailureState(id); state.ConsecutiveFailures != 1 {
		t.Fatalf("reconcile retried during cooldown: %+v", state)
	}
	if _, err := s.startInstance(ctx, id, false); !errors.Is(err, errStartBackoff) {
		t.Fatalf("background start err=%v", err)
	}

	if _, err := s.StartInstance(ctx, id); err == nil {
		t.Fatal("expected explicit launch to attempt and fail")
	}
	second := waitStartFailures(t, s, id, 2)
	if second.ConsecutiveFailures != 2 || !second.RetryAfter.Equal(start.Add(30*time.Second)) {
		t.Fatalf("explicit launch state=%+v", second)
	}

	advance(31 * time.Second)
	s.ReconcileAlwaysOn(ctx)
	waitStartFailures(t, s, id, 3)

	s.overrideStartBackoff(id)
	good := supervisor.New(lifecycleFakeBinary(t), "127.0.0.1", freePort(t), 5*time.Second)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		good.Shutdown(ctx)
	})
	s.sup = good
	if _, err := s.StartInstance(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.startFailureState(id); ok {
		t.Fatal("successful start did not reset failure state")
	}
}

func TestInferenceDuringStartBackoffDoesNotSpawn(t *testing.T) {
	ctx := context.Background()
	s, _, m, _, _ := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	id := items[0].ID
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	attachBrokenSupervisor(t, s)
	if _, err := s.EnsureReady(ctx, id); err == nil {
		t.Fatal("expected first start to fail")
	}
	waitStartFailures(t, s, id, 1)

	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := s.Acquire(ctx, id)
			errs <- err
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.ReconcileAlwaysOn(ctx)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if !errors.Is(err, errStartBackoff) {
			t.Fatalf("acquire err=%v", err)
		}
		if !strings.Contains(err.Error(), "consecutive start failures") {
			t.Fatalf("acquire message=%v", err)
		}
	}
	if state, _ := s.startFailureState(id); state.ConsecutiveFailures != 1 {
		t.Fatalf("retry storm incremented failures: %+v", state)
	}

	rt, err := s.RuntimeInstance(ctx, id)
	if err != nil || rt.ConsecutiveStartFailures != 1 || rt.RetryAfter == nil || rt.LastError == "" {
		t.Fatalf("runtime overlay=%+v err=%v", rt, err)
	}
}

func TestManualStopDoesNotCountAsStartFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, _, m, _, _ := setupLifecycle(t, true, true)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	id := items[0].ID
	if _, err := s.StartInstance(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := s.StopInstance(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.startFailureState(id); ok {
		t.Fatal("manual stop counted as a start failure")
	}
	s.recordStartFailure(id, "boom")
	if err := s.StopInstance(ctx, id); err != nil {
		t.Fatal(err)
	}
	state, ok := s.startFailureState(id)
	if !ok || state.ConsecutiveFailures != 1 {
		t.Fatalf("manual stop reset failure streak: %+v ok=%v", state, ok)
	}
}

func TestResourcePressureBlockDoesNotCountAsStartFailure(t *testing.T) {
	ctx := context.Background()
	s, _, m, _, exec := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	instance := items[0]
	exec("UPDATE models SET total_bytes=? WHERE id=?", 8*testGiB, m.ID)
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 1 * testGiB}}}}}
	if _, err := s.startOneWithEviction(ctx, instance, false); !errors.Is(err, errResourcePressureBlocked) {
		t.Fatalf("err=%v", err)
	}
	if _, ok := s.startFailureState(instance.ID); ok {
		t.Fatal("resource-pressure block counted as crash-loop failure")
	}
}

func TestInsufficientVRAMDoesNotEnterStartupBackoff(t *testing.T) {
	ctx := context.Background()
	s, _, m, _, exec := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	instance := items[0]
	exec("UPDATE models SET total_bytes=? WHERE id=?", 8*testGiB, m.ID)
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 1 * testGiB}}}}}
	_, err = s.startOneWithEviction(ctx, instance, true)
	if !errors.Is(err, errResourcePressureBlocked) || !strings.Contains(err.Error(), "insufficient usable VRAM") {
		t.Fatalf("err=%v", err)
	}
	if _, ok := s.startFailureState(instance.ID); ok {
		t.Fatal("eviction-exhausted wait counted as crash-loop failure")
	}

	_, release, acquireErr := s.Acquire(ctx, instance.ID)
	if release != nil {
		release()
	}
	if errors.Is(acquireErr, errStartBackoff) || (acquireErr != nil && strings.Contains(acquireErr.Error(), "startup backoff")) {
		t.Fatalf("later autoload hit crash-loop backoff after resource pressure: %v", acquireErr)
	}
	if !errors.Is(acquireErr, errResourcePressureBlocked) {
		t.Fatalf("later autoload err=%v", acquireErr)
	}
}

func TestReadinessTimeoutCountsAsStartFailure(t *testing.T) {
	ctx := context.Background()
	s, _, m, _, exec := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	instance := items[0]
	readyDelay := supervisor.New(lifecycleFakeBinary(t), "127.0.0.1", freePort(t), 80*time.Millisecond)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		readyDelay.Shutdown(ctx)
	})
	s.sup = readyDelay
	exec(`INSERT INTO instance_options(instance_id, option_key, option_value) VALUES(?,?,?)`, instance.ID, "test-ready-delay-ms", "2000")
	if _, err := s.startOneWithEviction(ctx, instance, true); err == nil {
		t.Fatal("expected readiness timeout")
	}
	state, ok := s.startFailureState(instance.ID)
	if !ok || state.ConsecutiveFailures != 1 || state.LastError == "" {
		t.Fatalf("timeout state=%+v ok=%v", state, ok)
	}
}

func TestRuntimeOverlayAndSubscribeIncludeBackoff(t *testing.T) {
	ctx := context.Background()
	s, _, m, _, _ := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	id := items[0].ID
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	s.recordStartFailure(id, "exec: missing")

	snapshot, events, cancel, err := s.SubscribeRuntimes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	found := false
	for _, rt := range snapshot {
		if rt.InstanceID == id {
			found = true
			if rt.ConsecutiveStartFailures != 1 || rt.RetryAfter == nil || rt.LastError != "exec: missing" {
				t.Fatalf("snapshot overlay=%+v", rt)
			}
		}
	}
	if !found {
		t.Fatalf("snapshot=%+v", snapshot)
	}

	s.recordStartFailure(id, "still missing")
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		select {
		case rt := <-events:
			if rt.InstanceID == id && rt.ConsecutiveStartFailures == 2 {
				return
			}
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	t.Fatal("did not observe republished backoff runtime")
}

func TestRestartOverridesStartBackoff(t *testing.T) {
	ctx := context.Background()
	s, _, m, _, _ := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	id := items[0].ID
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	attachBrokenSupervisor(t, s)
	if _, err := s.StartInstance(ctx, id); err == nil {
		t.Fatal("expected start failure")
	}
	waitStartFailures(t, s, id, 1)
	if _, err := s.RestartInstance(ctx, id); err == nil {
		t.Fatal("expected restart to attempt and fail")
	}
	state := waitStartFailures(t, s, id, 2)
	if state.ConsecutiveFailures != 2 {
		t.Fatalf("restart did not override cooldown: %+v", state)
	}
}
