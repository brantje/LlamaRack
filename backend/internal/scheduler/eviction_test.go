package scheduler

import (
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/hardware"
)

func TestRankEvictionCandidates(t *testing.T) {
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := old.Add(time.Hour)
	items := []Candidate{
		{ModelID: "high", InstanceID: "1", Priority: "high", Ready: true, EvictionEnabled: true, LastUsed: old, EstimatedBytes: 9},
		{ModelID: "normal-new", InstanceID: "1", Priority: "normal", Ready: true, EvictionEnabled: true, LastUsed: newer, EstimatedBytes: 9},
		{ModelID: "low-new", InstanceID: "1", Priority: "LOW", Ready: true, EvictionEnabled: true, LastUsed: newer, EstimatedBytes: 5},
		{ModelID: "low-old-small", InstanceID: "1", Priority: "low", Ready: true, EvictionEnabled: true, LastUsed: old, EstimatedBytes: 3},
		{ModelID: "low-old-large", InstanceID: "1", Priority: "low", Ready: true, EvictionEnabled: true, LastUsed: old, EstimatedBytes: 8},
		{ModelID: "low-never", InstanceID: "1", Priority: "low", Ready: true, EvictionEnabled: true, EstimatedBytes: 1},
		{ModelID: "always", InstanceID: "1", Priority: "low", Ready: true, AlwaysOn: true, EvictionEnabled: true, EstimatedBytes: 100},
		{ModelID: "protected", InstanceID: "1", Priority: "low", Ready: true, EvictionEnabled: false, EstimatedBytes: 100},
		{ModelID: "active", InstanceID: "1", Priority: "low", Ready: true, EvictionEnabled: true, ActiveRequests: 1, EstimatedBytes: 100},
		{ModelID: "unloaded", InstanceID: "1", Priority: "low", Ready: false, EvictionEnabled: true, EstimatedBytes: 100},
	}

	got := RankEvictionCandidates(items)
	want := []string{"always", "low-never", "low-old-large", "low-old-small", "low-new", "normal-new", "high"}
	if len(got) != len(want) {
		t.Fatalf("ranked=%+v", got)
	}
	for i, id := range want {
		if got[i].ModelID != id {
			t.Fatalf("ranked[%d]=%q want=%q; all=%+v", i, got[i].ModelID, id, got)
		}
	}
}

func TestRankEvictionCandidatesPolicyMatrix(t *testing.T) {
	tests := []struct {
		name            string
		alwaysOn        bool
		evictionEnabled bool
		eligible        bool
	}{
		{name: "on demand evictable", alwaysOn: false, evictionEnabled: true, eligible: true},
		{name: "on demand protected", alwaysOn: false, evictionEnabled: false, eligible: false},
		{name: "always on evictable", alwaysOn: true, evictionEnabled: true, eligible: true},
		{name: "always on protected", alwaysOn: true, evictionEnabled: false, eligible: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := RankEvictionCandidates([]Candidate{{
				ModelID: "model", InstanceID: "instance", Priority: "normal", Ready: true,
				AlwaysOn: test.alwaysOn, EvictionEnabled: test.evictionEnabled, EstimatedBytes: 1,
			}})
			if (len(got) == 1) != test.eligible {
				t.Fatalf("eligible=%v ranked=%+v", test.eligible, got)
			}
		})
	}
}

func TestRankEvictionCandidatesStableTieBreakersAndDefaultPriority(t *testing.T) {
	when := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got := RankEvictionCandidates([]Candidate{
		{ModelID: "b", InstanceID: "2", Priority: "mystery", Ready: true, EvictionEnabled: true, LastUsed: when, EstimatedBytes: 4},
		{ModelID: "a", InstanceID: "2", Priority: "normal", Ready: true, EvictionEnabled: true, LastUsed: when, EstimatedBytes: 4},
		{ModelID: "a", InstanceID: "1", Priority: " normal ", Ready: true, EvictionEnabled: true, LastUsed: when, EstimatedBytes: 4},
	})
	if len(got) != 3 || got[0].InstanceID != "1" || got[1].ModelID != "a" || got[2].ModelID != "b" {
		t.Fatalf("stable ranking=%+v", got)
	}
}

func TestPlanEvictions(t *testing.T) {
	items := []Candidate{
		{ModelID: "first", Priority: "low", Ready: true, EvictionEnabled: true, EstimatedBytes: 4},
		{ModelID: "unknown", Priority: "low", Ready: true, EvictionEnabled: true, EstimatedBytes: 0},
		{ModelID: "second", Priority: "normal", Ready: true, EvictionEnabled: true, EstimatedBytes: 7},
	}

	if plan := PlanEvictionsBytes(items, 0); !plan.Fits || len(plan.Evict) != 0 || plan.FreedBytes != 0 {
		t.Fatalf("zero plan=%+v", plan)
	}
	plan := PlanEvictionsBytes(items, 9)
	if !plan.Fits || plan.FreedBytes != 11 || len(plan.Evict) != 3 || plan.Evict[0].ModelID != "first" || plan.Evict[2].ModelID != "second" {
		t.Fatalf("fit plan=%+v", plan)
	}
	plan = PlanEvictionsBytes(items, 20)
	if plan.Fits || plan.FreedBytes != 11 || len(plan.Evict) != 3 {
		t.Fatalf("insufficient plan=%+v", plan)
	}
}

func TestPlanEvictionsSkipsWrongGPUVictim(t *testing.T) {
	const gib int64 = 1024 * 1024 * 1024
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{
		{ID: "CUDA0", FreeBytes: gib},
		{ID: "CUDA1", FreeBytes: 2 * gib},
	}}
	items := []Candidate{
		gpuCandidate("cuda1-only", "CUDA1", 8*gib),
		gpuCandidate("cuda0-victim", "CUDA0", 7*gib),
	}
	plan := PlanEvictions(items, snapshot, PlacementRequest{
		RequiredBytes: 6 * gib, Mode: "manual", Devices: []string{"CUDA0"}, ReserveBytes: 1,
	})
	if !plan.Fits || len(plan.Evict) != 1 || plan.Evict[0].InstanceID != "cuda0-victim" {
		t.Fatalf("CUDA0 target should select CUDA0 victim, got %+v", plan)
	}
	if len(plan.Devices) != 1 || plan.Devices[0] != "CUDA0" {
		t.Fatalf("target devices=%v", plan.Devices)
	}
	if len(plan.Freed) != 1 || plan.Freed[0].DeviceID != "CUDA0" {
		t.Fatalf("freed=%+v", plan.Freed)
	}
}

func TestPlanEvictionsReportsMultiGPUCandidateResources(t *testing.T) {
	const gib int64 = 1024 * 1024 * 1024
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{
		{ID: "CUDA0", FreeBytes: gib},
		{ID: "CUDA1", FreeBytes: gib},
	}}
	span := Candidate{
		ModelID: "span", InstanceID: "span", Priority: "low", Ready: true, EvictionEnabled: true,
		EstimatedBytes: 10 * gib,
		Resources: CandidateResources{GPU: []GPUResource{
			{DeviceID: "CUDA0", Bytes: 6 * gib},
			{DeviceID: "CUDA1", Bytes: 4 * gib},
		}},
	}
	plan := PlanEvictions([]Candidate{span}, snapshot, PlacementRequest{
		RequiredBytes: 8 * gib, Mode: "manual", Devices: []string{"CUDA0", "CUDA1"}, ReserveBytes: 1,
	})
	if !plan.Fits || len(plan.Evict) != 1 || len(plan.Evict[0].Resources.GPU) != 2 {
		t.Fatalf("span plan=%+v", plan)
	}
	if plan.FreedBytes != 10*gib || len(plan.Freed) != 2 {
		t.Fatalf("freed=%+v bytes=%d", plan.Freed, plan.FreedBytes)
	}
}

func TestPlanEvictionsMultiGPUIgnoresUnrelatedDevice(t *testing.T) {
	const gib int64 = 1024 * 1024 * 1024
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{
		{ID: "CUDA0", FreeBytes: gib},
		{ID: "CUDA1", FreeBytes: gib},
		{ID: "CUDA2", FreeBytes: 20 * gib},
	}}
	items := []Candidate{
		gpuCandidate("cuda2", "CUDA2", 12*gib),
		gpuCandidate("cuda1", "CUDA1", 7*gib),
	}
	plan := PlanEvictions(items, snapshot, PlacementRequest{
		RequiredBytes: 6 * gib, Mode: "manual", Devices: []string{"CUDA0", "CUDA1"}, ReserveBytes: 1,
	})
	if !plan.Fits || len(plan.Evict) != 1 || plan.Evict[0].InstanceID != "cuda1" {
		t.Fatalf("multi-GPU target should ignore CUDA2, got %+v", plan)
	}
}

func TestPlanEvictionsAutoPrefersSingleGPUAfterEviction(t *testing.T) {
	const gib int64 = 1024 * 1024 * 1024
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{
		{ID: "CUDA0", FreeBytes: gib},
		{ID: "CUDA1", FreeBytes: 2 * gib},
	}}
	items := []Candidate{
		gpuCandidate("cuda1-only", "CUDA1", 8*gib),
		gpuCandidate("cuda0-victim", "CUDA0", 7*gib),
	}
	plan := PlanEvictions(items, snapshot, PlacementRequest{RequiredBytes: 6 * gib, Mode: "auto", ReserveBytes: 1})
	if !plan.Fits || len(plan.Evict) != 1 || plan.Evict[0].InstanceID != "cuda1-only" {
		t.Fatalf("auto should place on CUDA1 by evicting CUDA1 victim, got %+v", plan)
	}
	if len(plan.Devices) != 1 || plan.Devices[0] != "CUDA1" {
		t.Fatalf("auto devices=%v", plan.Devices)
	}
}

func TestPlanEvictionsPreservesRankingAmongRelevantVictims(t *testing.T) {
	const gib int64 = 1024 * 1024 * 1024
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: gib}}}
	items := []Candidate{
		gpuCandidateAt("newer-small", "CUDA0", 3*gib, old.Add(time.Hour)),
		gpuCandidateAt("older-large", "CUDA0", 8*gib, old),
		gpuCandidate("wrong-gpu", "CUDA1", 20*gib),
	}
	plan := PlanEvictions(items, snapshot, PlacementRequest{
		RequiredBytes: 6 * gib, Mode: "manual", Devices: []string{"CUDA0"}, ReserveBytes: 1,
	})
	if !plan.Fits || len(plan.Evict) != 1 || plan.Evict[0].InstanceID != "older-large" {
		t.Fatalf("expected LRU/larger CUDA0 victim, got %+v", plan)
	}
}

func TestPlanEvictionsAlreadyFitsNeedsNoVictims(t *testing.T) {
	const gib int64 = 1024 * 1024 * 1024
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 20 * gib}}}
	plan := PlanEvictions([]Candidate{gpuCandidate("v", "CUDA0", 8*gib)}, snapshot, PlacementRequest{RequiredBytes: 4 * gib, ReserveBytes: 1})
	if !plan.Fits || len(plan.Evict) != 0 || plan.Devices[0] != "CUDA0" {
		t.Fatalf("already-fit plan=%+v", plan)
	}
}

func TestPlanEvictionsManualMissingDevice(t *testing.T) {
	const gib int64 = 1024 * 1024 * 1024
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: gib}}}
	plan := PlanEvictions([]Candidate{gpuCandidate("v", "CUDA0", 8*gib)}, snapshot, PlacementRequest{
		RequiredBytes: 6 * gib, Mode: "manual", Devices: []string{"CUDA9"}, ReserveBytes: 1,
	})
	if plan.Fits || len(plan.Evict) != 0 {
		t.Fatalf("missing device plan=%+v", plan)
	}
}

func TestAttributeResourcesUsesLeaseBytesWhenEstimateMissing(t *testing.T) {
	got := AttributeResources(ResourceAttribution{
		LeaseGPUs: []GPUReservation{{DeviceID: "CUDA0", Bytes: 4}},
	})
	if len(got.GPU) != 1 || got.GPU[0].DeviceID != "CUDA0" || got.GPU[0].Bytes != 4 {
		t.Fatalf("lease bytes=%+v", got)
	}
	empty := AttributeResources(ResourceAttribution{EstimatedBytes: 8, SnapshotGPUs: nil})
	if len(empty.GPU) != 0 {
		t.Fatalf("no devices=%+v", empty)
	}
}

func TestAttributeResourcesPrefersObservedThenLeaseThenDevices(t *testing.T) {
	const gib int64 = 1024 * 1024 * 1024
	observed := AttributeResources(ResourceAttribution{
		EstimatedBytes: 9 * gib,
		PID:            42,
		Processes:      []hardware.GPUProcess{{PID: 42, DeviceID: "CUDA0", UsedBytes: 3 * gib}, {PID: 42, DeviceID: "CUDA1", UsedBytes: 2 * gib}},
		LeaseGPUs:      []GPUReservation{{DeviceID: "CUDA0", Bytes: 8 * gib}},
		Devices:        []string{"CUDA0"},
	})
	if len(observed.GPU) != 2 || observed.GPU[0].Bytes != 3*gib || observed.GPU[1].DeviceID != "CUDA1" {
		t.Fatalf("observed=%+v", observed)
	}

	lease := AttributeResources(ResourceAttribution{
		EstimatedBytes: 9 * gib,
		LeaseGPUs:      []GPUReservation{{DeviceID: "CUDA1", Bytes: 5 * gib}},
	})
	if len(lease.GPU) != 1 || lease.GPU[0].DeviceID != "CUDA1" || lease.GPU[0].Bytes != 9*gib {
		t.Fatalf("lease devices with current estimate=%+v", lease)
	}

	split := AttributeResources(ResourceAttribution{
		EstimatedBytes: 8 * gib,
		Devices:        []string{"CUDA0", "CUDA1"},
		TensorSplit:    "3,1",
	})
	if len(split.GPU) != 2 || split.GPU[0].Bytes != 6*gib || split.GPU[1].Bytes != 2*gib {
		t.Fatalf("split=%+v", split)
	}

	unknown := AttributeResources(ResourceAttribution{
		EstimatedBytes: 8 * gib,
		SnapshotGPUs:   []hardware.GPU{{ID: "CUDA0"}, {ID: "CUDA1"}},
	})
	if len(unknown.GPU) != 0 {
		t.Fatalf("unknown multi-GPU must not invent a device: %+v", unknown)
	}

	single := AttributeResources(ResourceAttribution{
		EstimatedBytes: 8 * gib,
		SnapshotGPUs:   []hardware.GPU{{ID: "CUDA0"}},
	})
	if len(single.GPU) != 1 || single.GPU[0].DeviceID != "CUDA0" || single.GPU[0].Bytes != 8*gib {
		t.Fatalf("single-GPU fallback=%+v", single)
	}
}

func TestCreditsFromCandidatesOmitsUnassignedBytes(t *testing.T) {
	credits := CreditsFromCandidates([]Candidate{
		{InstanceID: "a", Resources: CandidateResources{GPU: []GPUResource{{DeviceID: "CUDA0", Bytes: 4}}}},
		{InstanceID: ""},
	})
	if len(credits) != 1 || credits[0].Bytes != 0 || len(credits[0].GPUs) != 1 || credits[0].GPUs[0].DeviceID != "CUDA0" {
		t.Fatalf("credits=%+v", credits)
	}
}

func TestApplyCandidateCreditsAddsPerDeviceBytes(t *testing.T) {
	const gib int64 = 1024 * 1024 * 1024
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{
		{ID: "CUDA0", FreeBytes: gib},
		{ID: "CUDA1", FreeBytes: 2 * gib},
	}}
	adjusted := ApplyCandidateCredits(snapshot, []Candidate{gpuCandidate("v", "CUDA0", 4*gib)})
	if adjusted.GPUs[0].FreeBytes != 5*gib || adjusted.GPUs[1].FreeBytes != 2*gib {
		t.Fatalf("credits must stay on CUDA0: %+v", adjusted.GPUs)
	}
}

func TestPlanEvictionsNoGPUsFallsBackToScalar(t *testing.T) {
	items := []Candidate{gpuCandidate("a", "CUDA0", 4)}
	plan := PlanEvictions(items, hardware.Snapshot{}, PlacementRequest{RequiredBytes: 3})
	if !plan.Fits || len(plan.Evict) != 1 {
		t.Fatalf("scalar fallback=%+v", plan)
	}
}

func TestPlanEvictionsSkipsUnknownDeviceEstimate(t *testing.T) {
	const gib int64 = 1024 * 1024 * 1024
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{
		{ID: "CUDA0", FreeBytes: gib},
		{ID: "CUDA1", FreeBytes: 8 * gib},
	}}
	items := []Candidate{{
		ModelID: "unknown", InstanceID: "unknown", Priority: "low", Ready: true, EvictionEnabled: true,
		EstimatedBytes: 20 * gib,
	}}
	plan := PlanEvictions(items, snapshot, PlacementRequest{
		RequiredBytes: 6 * gib, Mode: "manual", Devices: []string{"CUDA0"}, ReserveBytes: 1,
	})
	if plan.Fits || len(plan.Evict) != 0 {
		t.Fatalf("unknown-device estimate must not cover CUDA0: %+v", plan)
	}
}

func TestPlanEvictionsAutoFallsBackToMultiGPU(t *testing.T) {
	const gib int64 = 1024 * 1024 * 1024
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{
		{ID: "CUDA0", FreeBytes: 3 * gib},
		{ID: "CUDA1", FreeBytes: 3 * gib},
	}}
	items := []Candidate{
		gpuCandidate("cuda0", "CUDA0", 5*gib),
		gpuCandidate("cuda1", "CUDA1", 5*gib),
	}
	plan := PlanEvictions(items, snapshot, PlacementRequest{RequiredBytes: 10 * gib, Mode: "auto", ReserveBytes: 1})
	if !plan.Fits || len(plan.Evict) != 1 || plan.Evict[0].InstanceID != "cuda0" || len(plan.Devices) != 2 {
		t.Fatalf("multi-GPU fallback plan=%+v", plan)
	}
}

func TestSplitEstimateEvenWhenTensorSplitInvalid(t *testing.T) {
	got := SplitEstimateAcrossDevices(8, []string{"CUDA0", "CUDA1"}, "nope")
	if len(got) != 2 || got[0].Bytes+got[1].Bytes != 8 || got[0].Bytes != 4 {
		t.Fatalf("even split=%+v", got)
	}
	if SplitEstimateAcrossDevices(0, []string{"CUDA0"}, "") != nil {
		t.Fatal("zero total")
	}
}

func gpuCandidate(id, device string, bytes int64) Candidate {
	return gpuCandidateAt(id, device, bytes, time.Time{})
}

func gpuCandidateAt(id, device string, bytes int64, lastUsed time.Time) Candidate {
	return Candidate{
		ModelID: id, InstanceID: id, Priority: "low", Ready: true, EvictionEnabled: true,
		LastUsed: lastUsed, EstimatedBytes: bytes,
		Resources: CandidateResources{GPU: []GPUResource{{DeviceID: device, Bytes: bytes}}},
	}
}
