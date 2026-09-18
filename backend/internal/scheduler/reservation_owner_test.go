package scheduler

import (
	"testing"

	"github.com/brantje/llamarack/backend/internal/hardware"
)

const ownerTestGiB = int64(1024 * 1024 * 1024)

func TestAcquireOwnerValidationPreservesLegacyContract(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  AcquireRequest
		want string
	}{
		{"empty legacy id", AcquireRequest{}, "instance id is required"},
		{"blank legacy id", AcquireRequest{InstanceID: " \t", Owner: ResourceOwner{Kind: " ", ID: " "}}, "instance id is required"},
		{"explicit instance missing id", AcquireRequest{Owner: ResourceOwner{Kind: ResourceOwnerInstance}}, "resource lease owner id is required"},
		{"explicit benchmark missing id", AcquireRequest{Owner: ResourceOwner{Kind: ResourceOwnerBenchmark}}, "resource lease owner id is required"},
		{"missing kind", AcquireRequest{Owner: ResourceOwner{ID: "job"}}, "resource lease owner kind must be instance or benchmark"},
		{"unknown kind", AcquireRequest{Owner: ResourceOwner{Kind: "other", ID: "job"}}, "resource lease owner kind must be instance or benchmark"},
		{"conflicting legacy id", AcquireRequest{InstanceID: "worker", Owner: ResourceOwner{Kind: ResourceOwnerBenchmark, ID: "worker"}}, "resource lease owner conflicts with legacy instance id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ledger := NewLedger()
			if _, err := ledger.Acquire(tc.req); err == nil || err.Error() != tc.want {
				t.Fatalf("Acquire error = %v, want %q", err, tc.want)
			}
			if len(ledger.leases) != 0 {
				t.Fatal("invalid owner created a lease")
			}
		})
	}
}

func TestBenchmarkOwnerDoesNotReplaceInstanceLease(t *testing.T) {
	ledger := NewLedger()
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", TotalBytes: 32 * ownerTestGiB, FreeBytes: 32 * ownerTestGiB}}}
	request := PlacementRequest{RequiredBytes: 8 * ownerTestGiB, Mode: "manual", Devices: []string{"CUDA0"}, ReserveBytes: 1}

	instanceLease, err := ledger.Acquire(AcquireRequest{InstanceID: "i1", Snapshot: snapshot, Placement: request})
	if err != nil || !instanceLease.Placement.Fits {
		t.Fatalf("instance acquire lease=%+v err=%v", instanceLease, err)
	}
	if err := ledger.Commit(instanceLease.ID); err != nil {
		t.Fatal(err)
	}

	benchmarkOwner := ResourceOwner{Kind: ResourceOwnerBenchmark, ID: "b1"}
	benchmarkLease, err := ledger.Acquire(AcquireRequest{Owner: benchmarkOwner, Snapshot: snapshot, Placement: request})
	if err != nil || !benchmarkLease.Placement.Fits {
		t.Fatalf("benchmark acquire lease=%+v err=%v", benchmarkLease, err)
	}
	if benchmarkLease.InstanceID != "" || benchmarkLease.Owner != benchmarkOwner {
		t.Fatalf("benchmark lease identity=%+v", benchmarkLease)
	}
	if got, ok := ledger.GetByInstance("i1"); !ok || got.ID != instanceLease.ID {
		t.Fatalf("instance lease was replaced: got=%+v ok=%v", got, ok)
	}
	if got, ok := ledger.GetByOwner(benchmarkOwner); !ok || got.ID != benchmarkLease.ID {
		t.Fatalf("benchmark lease missing: got=%+v ok=%v", got, ok)
	}

	ledger.ReleaseOwner(benchmarkOwner)
	if _, ok := ledger.GetByOwner(benchmarkOwner); ok {
		t.Fatal("benchmark lease still present after release")
	}
	if got, ok := ledger.GetByInstance("i1"); !ok || got.ID != instanceLease.ID {
		t.Fatalf("releasing benchmark disturbed instance lease: got=%+v ok=%v", got, ok)
	}
}

func TestBenchmarkOwnersCannotOvercommitSameGPU(t *testing.T) {
	ledger := NewLedger()
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", TotalBytes: 24 * ownerTestGiB, FreeBytes: 24 * ownerTestGiB}}}
	request := PlacementRequest{RequiredBytes: 12 * ownerTestGiB, Mode: "manual", Devices: []string{"CUDA0"}, ReserveBytes: 1}

	first, err := ledger.Acquire(AcquireRequest{Owner: ResourceOwner{Kind: ResourceOwnerBenchmark, ID: "b1"}, Snapshot: snapshot, Placement: request})
	if err != nil || !first.Placement.Fits {
		t.Fatalf("first benchmark lease=%+v err=%v", first, err)
	}
	second, err := ledger.Acquire(AcquireRequest{Owner: ResourceOwner{Kind: ResourceOwnerBenchmark, ID: "b2"}, Snapshot: snapshot, Placement: request})
	if err != nil {
		t.Fatal(err)
	}
	if second.Placement.Fits {
		t.Fatalf("second benchmark unexpectedly fit: %+v", second.Placement)
	}
	if _, ok := ledger.GetByOwner(ResourceOwner{Kind: ResourceOwnerBenchmark, ID: "b2"}); ok {
		t.Fatal("non-fitting benchmark must not create a lease")
	}
}

func TestBackgroundOwnerCannotUseEvictionCredits(t *testing.T) {
	ledger := NewLedger()
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", TotalBytes: 16 * ownerTestGiB, UsedBytes: 12 * ownerTestGiB, FreeBytes: 4 * ownerTestGiB}}}
	request := PlacementRequest{RequiredBytes: 8 * ownerTestGiB, Mode: "manual", Devices: []string{"CUDA0"}, ReserveBytes: 1}

	lease, err := ledger.Acquire(AcquireRequest{
		Owner:     ResourceOwner{Kind: ResourceOwnerBenchmark, ID: "b1"},
		Snapshot:  snapshot,
		Placement: request,
		Credits:   []Credit{{InstanceID: "victim", GPUs: []GPUReservation{{DeviceID: "CUDA0", Bytes: 12 * ownerTestGiB}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if lease.Placement.Fits {
		t.Fatalf("benchmark incorrectly used eviction credit: %+v", lease.Placement)
	}
}

func TestBenchmarkOwnersCannotOvercommitHostRAM(t *testing.T) {
	ledger := NewLedger()
	snapshot := hardware.Snapshot{RAMTotalBytes: 16 * ownerTestGiB, RAMAvailableBytes: 16 * ownerTestGiB}
	placement := PlacementRequest{RequiredBytes: 0, Mode: "auto"}
	first, err := ledger.Acquire(AcquireRequest{Owner: ResourceOwner{Kind: ResourceOwnerBenchmark, ID: "cpu-1"}, Snapshot: snapshot, Placement: placement, HostRAM: 10 * ownerTestGiB})
	if err != nil || !first.Placement.Fits {
		t.Fatalf("first CPU benchmark lease=%+v err=%v", first, err)
	}
	second, err := ledger.Acquire(AcquireRequest{Owner: ResourceOwner{Kind: ResourceOwnerBenchmark, ID: "cpu-2"}, Snapshot: snapshot, Placement: placement, HostRAM: 10 * ownerTestGiB})
	if err != nil {
		t.Fatal(err)
	}
	if second.Placement.Fits {
		t.Fatalf("second CPU benchmark unexpectedly fit: %+v", second)
	}
}
