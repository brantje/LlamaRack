package scheduler

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/hardware"
)

func TestLedgerPreventsConcurrentOvercommitOnSameGPU(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	ledger := NewLedger()
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 12 * gib}}}
	req := func(id string) AcquireRequest {
		return AcquireRequest{InstanceID: id, Snapshot: snapshot, Placement: PlacementRequest{RequiredBytes: 8 * gib}}
	}

	first, err := ledger.Acquire(req("a"))
	if err != nil || !first.Placement.Fits || first.ID == "" {
		t.Fatalf("first acquire=%+v err=%v", first, err)
	}
	second, err := ledger.Acquire(req("b"))
	if err != nil {
		t.Fatal(err)
	}
	if second.Placement.Fits || second.ID != "" {
		t.Fatalf("second start should not reserve the same VRAM, got %+v", second)
	}
	if len(first.GPUs) != 1 || first.GPUs[0].DeviceID != "CUDA0" || first.GPUs[0].Bytes != 8*gib {
		t.Fatalf("unexpected first reservation: %+v", first.GPUs)
	}
}

func TestLedgerAllowsIndependentGPUsConcurrently(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	ledger := NewLedger()
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{
		{ID: "CUDA0", FreeBytes: 12 * gib},
		{ID: "CUDA1", FreeBytes: 12 * gib},
	}}
	first, err := ledger.Acquire(AcquireRequest{InstanceID: "a", Snapshot: snapshot, Placement: PlacementRequest{RequiredBytes: 8 * gib}})
	if err != nil || !first.Placement.Fits {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := ledger.Acquire(AcquireRequest{InstanceID: "b", Snapshot: snapshot, Placement: PlacementRequest{RequiredBytes: 8 * gib}})
	if err != nil || !second.Placement.Fits {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	if len(first.Placement.Devices) != 1 || len(second.Placement.Devices) != 1 || first.Placement.Devices[0] == second.Placement.Devices[0] {
		t.Fatalf("expected distinct GPUs, first=%v second=%v", first.Placement.Devices, second.Placement.Devices)
	}
}

func TestLedgerMultiGPUReservesEverySelectedDevice(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	ledger := NewLedger()
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{
		{ID: "CUDA0", FreeBytes: 10 * gib},
		{ID: "CUDA1", FreeBytes: 9 * gib},
	}}
	lease, err := ledger.Acquire(AcquireRequest{InstanceID: "wide", Snapshot: snapshot, Placement: PlacementRequest{RequiredBytes: 14 * gib}})
	if err != nil || !lease.Placement.Fits || len(lease.GPUs) != 2 {
		t.Fatalf("multi-GPU lease=%+v err=%v", lease, err)
	}
	seen := map[string]int64{}
	total := int64(0)
	for _, gpu := range lease.GPUs {
		seen[gpu.DeviceID] = gpu.Bytes
		total += gpu.Bytes
	}
	if _, ok := seen["CUDA0"]; !ok {
		t.Fatalf("missing CUDA0 reservation: %+v", lease.GPUs)
	}
	if _, ok := seen["CUDA1"]; !ok {
		t.Fatalf("missing CUDA1 reservation: %+v", lease.GPUs)
	}
	if total != 14*gib {
		t.Fatalf("reserved %d want %d", total, 14*gib)
	}
}

func TestLedgerReleaseAndCommitLifecycle(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	ledger := NewLedger()
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 12 * gib}}}
	req := AcquireRequest{InstanceID: "a", Snapshot: snapshot, Placement: PlacementRequest{RequiredBytes: 8 * gib}, HostRAM: gib}
	lease, err := ledger.Acquire(req)
	if err != nil {
		t.Fatal(err)
	}
	if lease.HostRAM != gib {
		t.Fatalf("host ram=%d", lease.HostRAM)
	}
	if err := ledger.Commit(lease.ID); err != nil {
		t.Fatal(err)
	}
	got, ok := ledger.Get(lease.ID)
	if !ok || got.State != LeaseCommitted || !got.ExpiresAt.IsZero() {
		t.Fatalf("committed lease=%+v ok=%v", got, ok)
	}
	if pending := ledger.Pending(); len(pending) != 0 {
		t.Fatalf("pending after commit: %+v", pending)
	}

	competitor, err := ledger.Acquire(AcquireRequest{InstanceID: "b", Snapshot: snapshot, Placement: PlacementRequest{RequiredBytes: 8 * gib}})
	if err != nil || competitor.Placement.Fits {
		t.Fatalf("committed allocation should still occupy capacity: %+v err=%v", competitor, err)
	}

	ledger.ReleaseInstance("a")
	if _, ok := ledger.GetByInstance("a"); ok {
		t.Fatal("expected lease released")
	}
	retry, err := ledger.Acquire(AcquireRequest{InstanceID: "b", Snapshot: snapshot, Placement: PlacementRequest{RequiredBytes: 8 * gib}})
	if err != nil || !retry.Placement.Fits {
		t.Fatalf("capacity should return after release: %+v err=%v", retry, err)
	}
}

func TestLedgerExpiredPendingLeaseIsSwept(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	ledger := NewLedgerWithTTL(time.Minute)
	ledger.SetClock(func() time.Time { return now })
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 12 * gib}}}
	if _, err := ledger.Acquire(AcquireRequest{InstanceID: "stale", Snapshot: snapshot, Placement: PlacementRequest{RequiredBytes: 8 * gib}}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	ledger.SweepExpired()
	if _, ok := ledger.GetByInstance("stale"); ok {
		t.Fatal("expired pending lease should be gone")
	}
	lease, err := ledger.Acquire(AcquireRequest{InstanceID: "fresh", Snapshot: snapshot, Placement: PlacementRequest{RequiredBytes: 8 * gib}})
	if err != nil || !lease.Placement.Fits {
		t.Fatalf("expired lease should free capacity: %+v err=%v", lease, err)
	}
}

func TestLedgerCreditsVictimCapacityOnce(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	ledger := NewLedger()
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{{
		ID: "CUDA0", TotalBytes: 12 * gib, UsedBytes: 8 * gib, FreeBytes: 4 * gib,
	}}}
	victim, err := ledger.Acquire(AcquireRequest{InstanceID: "victim", Snapshot: hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 12 * gib}}}, Placement: PlacementRequest{RequiredBytes: 8 * gib}})
	if err != nil || !victim.Placement.Fits {
		t.Fatalf("victim=%+v err=%v", victim, err)
	}
	if err := ledger.Commit(victim.ID); err != nil {
		t.Fatal(err)
	}

	first, err := ledger.Acquire(AcquireRequest{
		InstanceID: "requester",
		Snapshot:   snapshot,
		Placement:  PlacementRequest{RequiredBytes: 8 * gib},
		Credits:    []Credit{{InstanceID: "victim"}},
	})
	if err != nil || !first.Placement.Fits {
		t.Fatalf("requester should fit using victim credit: %+v err=%v", first, err)
	}
	second, err := ledger.Acquire(AcquireRequest{
		InstanceID: "other",
		Snapshot:   snapshot,
		Placement:  PlacementRequest{RequiredBytes: 8 * gib},
		Credits:    []Credit{{InstanceID: "victim"}},
	})
	if err != nil || second.Placement.Fits {
		t.Fatalf("claimed victim must not be credited twice: %+v err=%v", second, err)
	}
}

func TestLedgerTotalBytesAccountingDoesNotDoubleCountCommitted(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	ledger := NewLedger()
	empty := hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", TotalBytes: 12 * gib, FreeBytes: 12 * gib}}}
	lease, err := ledger.Acquire(AcquireRequest{InstanceID: "running", Snapshot: empty, Placement: PlacementRequest{RequiredBytes: 8 * gib}})
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.Commit(lease.ID); err != nil {
		t.Fatal(err)
	}
	observed := hardware.Snapshot{GPUs: []hardware.GPU{{
		ID: "CUDA0", TotalBytes: 12 * gib, UsedBytes: 8 * gib, FreeBytes: 4 * gib,
	}}}
	other, err := ledger.Acquire(AcquireRequest{InstanceID: "other", Snapshot: observed, Placement: PlacementRequest{RequiredBytes: 3 * gib}})
	if err != nil || !other.Placement.Fits {
		t.Fatalf("remaining capacity after committed+observed should still fit 3GiB: %+v err=%v", other, err)
	}
	tooBig, err := ledger.Acquire(AcquireRequest{InstanceID: "too-big", Snapshot: observed, Placement: PlacementRequest{RequiredBytes: 8 * gib}})
	if err != nil || tooBig.Placement.Fits {
		t.Fatalf("double-count would reject 3GiB or accept 8GiB: tooBig=%+v other=%+v err=%v", tooBig, other, err)
	}
}

func TestLedgerAcquireRequiresInstanceAndRejectsInvalidPlacement(t *testing.T) {
	ledger := NewLedger()
	if _, err := ledger.Acquire(AcquireRequest{Snapshot: hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 1}}}}); err == nil || !strings.Contains(err.Error(), "instance id") {
		t.Fatalf("expected instance id error, got %v", err)
	}
	_, err := ledger.Acquire(AcquireRequest{InstanceID: "a", Snapshot: hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 1}}}, Placement: PlacementRequest{RequiredBytes: -1}})
	if err == nil || !strings.Contains(err.Error(), "zero or greater") {
		t.Fatalf("expected placement error, got %v", err)
	}
}

func TestLedgerCommitAndGetUnknown(t *testing.T) {
	ledger := NewLedger()
	if err := ledger.Commit("missing"); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("commit missing: %v", err)
	}
	if err := ledger.CommitInstance("missing"); err != nil {
		t.Fatalf("commit instance missing should be a no-op: %v", err)
	}
	if _, ok := ledger.Get("missing"); ok {
		t.Fatal("get missing")
	}
	ledger.Release("missing")
	ledger.ReleaseInstance("missing")
}

func TestLedgerCommitRejectsNonPending(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	ledger := NewLedger()
	lease, err := ledger.Acquire(AcquireRequest{
		InstanceID: "a",
		Snapshot:   hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 12 * gib}}},
		Placement:  PlacementRequest{RequiredBytes: gib},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.CommitInstance("a"); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Commit(lease.ID); err == nil || !strings.Contains(err.Error(), "not pending") {
		t.Fatalf("second commit: %v", err)
	}
	if err := ledger.CommitInstance("a"); err == nil || !strings.Contains(err.Error(), "not pending") {
		t.Fatalf("second instance commit: %v", err)
	}
}

func TestLedgerConcurrentAcquireIsAtomic(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	ledger := NewLedger()
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 12 * gib}}}
	var wg sync.WaitGroup
	results := make(chan ResourceLease, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			lease, err := ledger.Acquire(AcquireRequest{
				InstanceID: "inst-" + strings.Repeat("x", i+1),
				Snapshot:   snapshot,
				Placement:  PlacementRequest{RequiredBytes: 8 * gib},
			})
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			results <- lease
		}(i)
	}
	wg.Wait()
	close(results)
	fitted := 0
	for lease := range results {
		if lease.Placement.Fits && lease.ID != "" {
			fitted++
		}
	}
	if fitted != 1 {
		t.Fatalf("expected exactly one reservation, got %d", fitted)
	}
	if n := len(ledger.All()); n != 1 {
		t.Fatalf("stale leases after concurrent acquire: %d", n)
	}
}

func TestLedgerReplaceExistingInstanceLease(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	ledger := NewLedger()
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 12 * gib}}}
	first, err := ledger.Acquire(AcquireRequest{InstanceID: "a", Snapshot: snapshot, Placement: PlacementRequest{RequiredBytes: 8 * gib}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := ledger.Acquire(AcquireRequest{InstanceID: "a", Snapshot: snapshot, Placement: PlacementRequest{RequiredBytes: gib}})
	if err != nil || !second.Placement.Fits || second.ID == first.ID {
		t.Fatalf("replace=%+v first=%+v err=%v", second, first, err)
	}
	if _, ok := ledger.Get(first.ID); ok {
		t.Fatal("replaced lease should be gone")
	}
}

func TestLedgerSkipsSelfAndClaimedCredits(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	ledger := NewLedger()
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 4 * gib}}}
	lease, err := ledger.Acquire(AcquireRequest{
		InstanceID: "self",
		Snapshot:   snapshot,
		Placement:  PlacementRequest{RequiredBytes: 8 * gib},
		Credits:    []Credit{{InstanceID: "self", Bytes: 8 * gib}, {InstanceID: "", Bytes: 8 * gib}},
	})
	if err != nil || lease.Placement.Fits {
		t.Fatalf("self-credit must not invent capacity: %+v err=%v", lease, err)
	}
}

func TestAdjustSnapshotAndReservationsHelpers(t *testing.T) {
	if got := adjustSnapshot(hardware.Snapshot{}, nil, nil); len(got.GPUs) != 0 {
		t.Fatalf("empty snapshot=%+v", got)
	}
	if got := reservationsFor(Placement{}, hardware.Snapshot{}, PlacementRequest{RequiredBytes: 1}); got != nil {
		t.Fatalf("empty placement reservations=%v", got)
	}
	if got := reservationsFor(Placement{Devices: []string{"CUDA0"}}, hardware.Snapshot{}, PlacementRequest{RequiredBytes: -1}); len(got) != 1 || got[0].Bytes != 0 {
		t.Fatalf("negative required: %+v", got)
	}
	gib := int64(1024 * 1024 * 1024)
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{
		{ID: "CUDA0", FreeBytes: 10 * gib},
		{ID: "CUDA1", FreeBytes: 9 * gib},
	}}
	placement, err := PlanPlacement(snapshot, PlacementRequest{RequiredBytes: 14 * gib})
	if err != nil {
		t.Fatal(err)
	}
	got := reservationsFor(placement, snapshot, PlacementRequest{RequiredBytes: 14 * gib})
	if len(got) != 2 || got[0].Bytes+got[1].Bytes != 14*gib {
		t.Fatalf("split reservations=%+v", got)
	}
}

func TestNewLedgerWithTTLAndNilClock(t *testing.T) {
	ledger := NewLedgerWithTTL(0)
	ledger.SetClock(nil)
	if ledger.ttl != defaultLeaseTTL {
		t.Fatalf("ttl=%s", ledger.ttl)
	}
	if newLeaseID() == "" {
		t.Fatal("lease id")
	}
}
