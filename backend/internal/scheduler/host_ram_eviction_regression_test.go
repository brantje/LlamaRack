package scheduler

import (
	"sync"
	"testing"

	"github.com/brantje/llamarack/backend/internal/hardware"
)

const hostRAMTestGiB int64 = 1024 * 1024 * 1024

func TestHostRAMEvictionPlanningWhenGPUAlreadyFits(t *testing.T) {
	snapshot := hardware.Snapshot{
		RAMTotalBytes: 16 * hostRAMTestGiB, RAMAvailableBytes: 4 * hostRAMTestGiB,
		GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 12 * hostRAMTestGiB}},
	}
	candidate := Candidate{
		ModelID: "victim-model", InstanceID: "victim", Priority: "low", Ready: true, EvictionEnabled: true,
		EstimatedBytes: hostRAMTestGiB,
		Resources:      CandidateResources{HostRAMBytes: 6 * hostRAMTestGiB},
	}
	plan := PlanEvictions([]Candidate{candidate}, snapshot, PlacementRequest{
		RequiredBytes: hostRAMTestGiB, HostRAMBytes: 8 * hostRAMTestGiB,
		Mode: "manual", Devices: []string{"CUDA0"}, ReserveBytes: 1,
	})
	if !plan.Fits || len(plan.Evict) != 1 || plan.Evict[0].InstanceID != "victim" {
		t.Fatalf("host-RAM pressure should select the eligible victim: %+v", plan)
	}
	if plan.FreedHostRAMBytes != 6*hostRAMTestGiB {
		t.Fatalf("freed host RAM=%d", plan.FreedHostRAMBytes)
	}
}

func TestHostRAMEvictionPlanningFailsWithoutEnoughEligibleRAM(t *testing.T) {
	snapshot := hardware.Snapshot{
		RAMTotalBytes: 16 * hostRAMTestGiB, RAMAvailableBytes: 4 * hostRAMTestGiB,
		GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 12 * hostRAMTestGiB}},
	}
	candidates := []Candidate{
		{ModelID: "small", InstanceID: "small", Ready: true, EvictionEnabled: true, Resources: CandidateResources{HostRAMBytes: 2 * hostRAMTestGiB}},
		{ModelID: "protected", InstanceID: "protected", Ready: true, EvictionEnabled: false, Resources: CandidateResources{HostRAMBytes: 12 * hostRAMTestGiB}},
	}
	plan := PlanEvictions(candidates, snapshot, PlacementRequest{
		RequiredBytes: hostRAMTestGiB, HostRAMBytes: 8 * hostRAMTestGiB,
		Mode: "manual", Devices: []string{"CUDA0"}, ReserveBytes: 1,
	})
	if plan.Fits || plan.FreedHostRAMBytes != 2*hostRAMTestGiB || len(plan.Evict) != 1 || plan.Evict[0].InstanceID != "small" {
		t.Fatalf("insufficient host-RAM victims should fail deterministically: %+v", plan)
	}
}

func TestBenchmarkOwnerCannotConsumeEvictionCreditsOrClaimVictim(t *testing.T) {
	ledger := NewLedger()
	victimSnapshot := hardware.Snapshot{RAMTotalBytes: 16 * hostRAMTestGiB, RAMAvailableBytes: 16 * hostRAMTestGiB}
	victim, err := ledger.Acquire(AcquireRequest{InstanceID: "victim", Snapshot: victimSnapshot, HostRAM: 6 * hostRAMTestGiB})
	if err != nil || !victim.Placement.Fits {
		t.Fatalf("victim lease=%+v err=%v", victim, err)
	}
	if err := ledger.Commit(victim.ID); err != nil {
		t.Fatal(err)
	}

	pressure := hardware.Snapshot{RAMTotalBytes: 16 * hostRAMTestGiB, RAMAvailableBytes: 6 * hostRAMTestGiB}
	benchmark, err := ledger.Acquire(AcquireRequest{
		Owner:    ResourceOwner{Kind: ResourceOwnerBenchmark, ID: "benchmark-1"},
		Snapshot: pressure,
		Credits:  []Credit{{InstanceID: "victim", HostRAM: 6 * hostRAMTestGiB}},
		HostRAM:  8 * hostRAMTestGiB,
	})
	if err != nil {
		t.Fatal(err)
	}
	if benchmark.Placement.Fits {
		t.Fatalf("benchmark unexpectedly received eviction credit: %+v", benchmark)
	}
	if _, ok := ledger.GetByInstance("victim"); !ok {
		t.Fatal("benchmark admission removed or claimed the normal Instance lease")
	}

	instance, err := ledger.Acquire(AcquireRequest{
		InstanceID: "requester", Snapshot: pressure,
		Credits: []Credit{{InstanceID: "victim", HostRAM: 6 * hostRAMTestGiB}},
		HostRAM: 8 * hostRAMTestGiB,
	})
	if err != nil || !instance.Placement.Fits {
		t.Fatalf("normal Instance should receive managed victim credit: lease=%+v err=%v", instance, err)
	}
}

func TestReleasedVictimHostRAMIsNotCreditedOnFreshSnapshot(t *testing.T) {
	ledger := NewLedger()
	victim, err := ledger.Acquire(AcquireRequest{
		InstanceID: "victim",
		Snapshot:   hardware.Snapshot{RAMTotalBytes: 16 * hostRAMTestGiB, RAMAvailableBytes: 16 * hostRAMTestGiB},
		HostRAM:    6 * hostRAMTestGiB,
	})
	if err != nil || !victim.Placement.Fits {
		t.Fatalf("victim lease=%+v err=%v", victim, err)
	}
	if err := ledger.Commit(victim.ID); err != nil {
		t.Fatal(err)
	}
	ledger.ReleaseInstance("victim")

	// This is a fresh post-eviction host snapshot. Its 6 GiB availability already
	// includes whatever RAM the stopped victim actually released, so the old
	// victim estimate must not be added a second time.
	lease, err := ledger.Acquire(AcquireRequest{
		InstanceID: "requester",
		Snapshot:   hardware.Snapshot{RAMTotalBytes: 16 * hostRAMTestGiB, RAMAvailableBytes: 6 * hostRAMTestGiB},
		Credits:    []Credit{{InstanceID: "victim", HostRAM: 6 * hostRAMTestGiB}},
		HostRAM:    8 * hostRAMTestGiB,
	})
	if err != nil {
		t.Fatal(err)
	}
	if lease.Placement.Fits {
		t.Fatalf("released victim RAM was double-counted against a fresh snapshot: %+v", lease)
	}
}

func TestConcurrentHostRAMReservationsCannotOvercommit(t *testing.T) {
	ledger := NewLedger()
	snapshot := hardware.Snapshot{RAMTotalBytes: 8 * hostRAMTestGiB, RAMAvailableBytes: 8 * hostRAMTestGiB}

	const count = 12
	fits := make(chan bool, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			lease, err := ledger.Acquire(AcquireRequest{
				InstanceID: string(rune('a' + index)), Snapshot: snapshot, HostRAM: 6 * hostRAMTestGiB,
			})
			if err != nil {
				t.Errorf("acquire %d: %v", index, err)
				return
			}
			fits <- lease.Placement.Fits
		}(i)
	}
	wg.Wait()
	close(fits)
	accepted := 0
	for fit := range fits {
		if fit {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted %d concurrent 6 GiB reservations on an 8 GiB host", accepted)
	}
}

func TestGPUOnlyEvictionBehaviorRemainsUnchanged(t *testing.T) {
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: hostRAMTestGiB}}}
	victim := Candidate{
		ModelID: "gpu-victim", InstanceID: "gpu-victim", Ready: true, EvictionEnabled: true,
		EstimatedBytes: 6 * hostRAMTestGiB,
		Resources: CandidateResources{GPU: []GPUResource{{DeviceID: "CUDA0", Bytes: 6 * hostRAMTestGiB}}},
	}
	plan := PlanEvictions([]Candidate{victim}, snapshot, PlacementRequest{
		RequiredBytes: 4 * hostRAMTestGiB, Mode: "manual", Devices: []string{"CUDA0"}, ReserveBytes: 1,
	})
	if !plan.Fits || len(plan.Evict) != 1 || plan.FreedBytes != 6*hostRAMTestGiB || plan.FreedHostRAMBytes != 0 {
		t.Fatalf("GPU-only eviction behavior changed: %+v", plan)
	}
}
