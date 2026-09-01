package lifecycle

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/scheduler"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

func TestConcurrentStartsDoNotOvercommitSameGPU(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, _, m, sup, exec := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	enabled, autoload := true, true
	eviction := false
	first := items[0]
	second, err := s.instances.Create(ctx, instances.CreateInput{
		ModelID: m.ID, Name: "Sibling", Enabled: &enabled, Autoload: &autoload, EvictionEnabled: &eviction,
	})
	if err != nil {
		t.Fatal(err)
	}
	exec("UPDATE instances SET eviction_enabled=0 WHERE id=?", first.ID)
	exec("UPDATE models SET total_bytes=? WHERE id=?", 8*testGiB, m.ID)
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 12 * testGiB}}}}}

	type result struct {
		id  string
		err error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, id := range []string{first.ID, second.ID} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, err := s.StartInstance(ctx, id)
			results <- result{id: id, err: err}
		}(id)
	}
	wg.Wait()
	close(results)

	ready := 0
	failed := 0
	for item := range results {
		if item.err == nil {
			ready++
			continue
		}
		failed++
		if !strings.Contains(item.err.Error(), "insufficient usable VRAM") && !strings.Contains(item.err.Error(), "eligible eviction") {
			t.Fatalf("unexpected start error for %s: %v", item.id, item.err)
		}
	}
	if ready != 1 || failed != 1 {
		t.Fatalf("ready=%d failed=%d want one start", ready, failed)
	}
	readyCount := 0
	if sup.Status(first.ID).State == supervisor.Ready {
		readyCount++
	}
	if sup.Status(second.ID).State == supervisor.Ready {
		readyCount++
	}
	if readyCount != 1 {
		t.Fatalf("ready runtimes=%d first=%s second=%s", readyCount, sup.Status(first.ID).State, sup.Status(second.ID).State)
	}
	leases := s.reservations.All()
	if len(leases) != 1 || leases[0].State != scheduler.LeaseCommitted {
		t.Fatalf("leases after overcommit start=%+v", leases)
	}
}

func TestConcurrentStartsOnIndependentGPUsProceed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, _, m, sup, exec := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	enabled, autoload, eviction := true, true, false
	first := items[0]
	second, err := s.instances.Create(ctx, instances.CreateInput{
		ModelID: m.ID, Name: "Other GPU", Enabled: &enabled, Autoload: &autoload, EvictionEnabled: &eviction,
	})
	if err != nil {
		t.Fatal(err)
	}
	exec("UPDATE models SET total_bytes=? WHERE id=?", 8*testGiB, m.ID)
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{
		{ID: "CUDA0", FreeBytes: 12 * testGiB},
		{ID: "CUDA1", FreeBytes: 12 * testGiB},
	}}}}

	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, id := range []string{first.ID, second.ID} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, err := s.StartInstance(ctx, id)
			errs <- err
		}(id)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if sup.Status(first.ID).State != supervisor.Ready || sup.Status(second.ID).State != supervisor.Ready {
		t.Fatalf("states first=%s second=%s", sup.Status(first.ID).State, sup.Status(second.ID).State)
	}
	leases := s.reservations.All()
	if len(leases) != 2 {
		t.Fatalf("expected two committed leases, got %+v", leases)
	}
	devices := map[string]bool{}
	for _, lease := range leases {
		if lease.State != scheduler.LeaseCommitted || len(lease.GPUs) != 1 {
			t.Fatalf("lease=%+v", lease)
		}
		devices[lease.GPUs[0].DeviceID] = true
	}
	if !devices["CUDA0"] || !devices["CUDA1"] {
		t.Fatalf("expected both GPUs reserved, got %v", devices)
	}
}

func TestPreparePlacementMultiGPULeaseReservesEveryDevice(t *testing.T) {
	s, _, _, _, _ := setupLifecycle(t, true, false)
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{
		{ID: "CUDA0", FreeBytes: 10 * testGiB},
		{ID: "CUDA1", FreeBytes: 9 * testGiB},
	}}}}
	placement, err := s.preparePlacement(context.Background(), instances.Instance{ID: "wide", GPUMode: "auto"}, 14*testGiB)
	if err != nil || !placement.Fits || len(placement.Devices) != 2 {
		t.Fatalf("placement=%+v err=%v", placement, err)
	}
	lease, ok := s.reservations.GetByInstance("wide")
	if !ok || len(lease.GPUs) != 2 {
		t.Fatalf("multi-GPU lease=%+v ok=%v", lease, ok)
	}
	total := int64(0)
	seen := map[string]bool{}
	for _, gpu := range lease.GPUs {
		seen[gpu.DeviceID] = true
		total += gpu.Bytes
	}
	if !seen["CUDA0"] || !seen["CUDA1"] || total != 14*testGiB {
		t.Fatalf("reservations=%+v", lease.GPUs)
	}
}

func TestFailedStartReadinessTimeoutAndCancelReleaseLease(t *testing.T) {
	ctx := context.Background()
	s, _, m, _, exec := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	instance := items[0]
	exec("UPDATE models SET total_bytes=? WHERE id=?", 8*testGiB, m.ID)
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 12 * testGiB}}}}}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.startOneWithEviction(cancelled, instance, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled start err=%v", err)
	}
	if _, ok := s.reservations.GetByInstance(instance.ID); ok {
		t.Fatal("canceled start left a lease")
	}

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	broken := supervisor.New("/nonexistent-llama-server", "127.0.0.1", port, time.Second)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		broken.Shutdown(ctx)
	})
	s.sup = broken
	if _, err := s.startOneWithEviction(ctx, instance, true); err == nil {
		t.Fatal("expected start failure")
	}
	if _, ok := s.reservations.GetByInstance(instance.ID); ok {
		t.Fatal("failed start left a lease")
	}

	readyDelay := supervisor.New(lifecycleFakeBinary(t), "127.0.0.1", port+1, 80*time.Millisecond)
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
	if _, ok := s.reservations.GetByInstance(instance.ID); ok {
		t.Fatal("readiness timeout left a lease")
	}
}

func TestEvictionRequesterKeepsReservationDuringStopStartGap(t *testing.T) {
	ctx := context.Background()
	s, _, m, sup, exec := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	victim := items[0]
	if _, err := s.StartInstance(ctx, victim.ID); err != nil {
		t.Fatal(err)
	}
	exec("UPDATE models SET total_bytes=? WHERE id=?", 8*testGiB, m.ID)
	enabled, autoload, eviction := true, true, false
	requester, err := s.instances.Create(ctx, instances.CreateInput{
		ModelID: m.ID, Name: "Requester", Enabled: &enabled, Autoload: &autoload, EvictionEnabled: &eviction,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{
		{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 4 * testGiB}}},
		{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 4 * testGiB}}},
	}}
	if _, err := s.StartInstance(ctx, requester.ID); err != nil {
		t.Fatal(err)
	}
	if sup.Status(victim.ID).State != supervisor.Unloaded {
		t.Fatalf("victim state=%s", sup.Status(victim.ID).State)
	}
	if sup.Status(requester.ID).State != supervisor.Ready {
		t.Fatalf("requester state=%s", sup.Status(requester.ID).State)
	}
	lease, ok := s.reservations.GetByInstance(requester.ID)
	if !ok || lease.State != scheduler.LeaseCommitted {
		t.Fatalf("requester lease=%+v ok=%v", lease, ok)
	}
	if _, ok := s.reservations.GetByInstance(victim.ID); ok {
		t.Fatal("victim lease should be released after eviction")
	}
}

func TestCommittedLeaseClearedAfterStop(t *testing.T) {
	ctx := context.Background()
	s, _, m, _, exec := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	instance := items[0]
	exec("UPDATE models SET total_bytes=? WHERE id=?", 8*testGiB, m.ID)
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 12 * testGiB}}}}}
	if _, err := s.StartInstance(ctx, instance.ID); err != nil {
		t.Fatal(err)
	}
	lease, ok := s.reservations.GetByInstance(instance.ID)
	if !ok || lease.State != scheduler.LeaseCommitted {
		t.Fatalf("committed lease=%+v ok=%v", lease, ok)
	}
	if pending := s.reservations.Pending(); len(pending) != 0 {
		t.Fatalf("duplicate pending after commit: %+v", pending)
	}
	if err := s.StopInstance(ctx, instance.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.reservations.GetByInstance(instance.ID); ok {
		t.Fatal("stop should release the committed lease")
	}
	if n := len(s.reservations.All()); n != 0 {
		t.Fatalf("stale leases after stop: %d", n)
	}
}

func TestPreparePlacementFailsWhenEvictionRefreshLosesDevice(t *testing.T) {
	ctx := context.Background()
	s, _, m, _, exec := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	victim := items[0]
	if _, err := s.StartInstance(ctx, victim.ID); err != nil {
		t.Fatal(err)
	}
	exec("UPDATE models SET total_bytes=? WHERE id=?", 2*testGiB, m.ID)
	s.hardware = &stagedHardware{snapshots: []hardware.Snapshot{
		{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: testGiB}}},
		{GPUs: []hardware.GPU{{ID: "CUDA1", FreeBytes: 8 * testGiB}}},
	}}
	_, err = s.preparePlacement(ctx, instances.Instance{ID: "target", GPUMode: "auto"}, 2*testGiB)
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("expected missing device after eviction refresh, got %v", err)
	}
	if _, ok := s.reservations.GetByInstance("target"); ok {
		t.Fatal("failed post-eviction confirm should release the requester lease")
	}
}

func TestPlacementDevicesPresent(t *testing.T) {
	if err := placementDevicesPresent(hardware.Snapshot{}, nil); err != nil {
		t.Fatal(err)
	}
	err := placementDevicesPresent(hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0"}}}, []string{"CUDA9"})
	if err == nil || !strings.Contains(err.Error(), "CUDA9") {
		t.Fatalf("missing device err=%v", err)
	}
}
