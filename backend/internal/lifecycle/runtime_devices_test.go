package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/llamacpp"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

func runtimeProfile(devices ...string) llamacpp.Profile {
	return llamacpp.Profile{DeviceDiscoveryAvailable: true, Devices: devices}
}

func TestRuntimeDeviceCapabilityFiltersSnapshot(t *testing.T) {
	capability := runtimeDevicesFromProfile(runtimeProfile("CUDA0"))
	snapshot := hardware.Snapshot{
		GPUs:      []hardware.GPU{{ID: "CUDA0"}, {ID: "CUDA1"}},
		Processes: []hardware.GPUProcess{{PID: 1, DeviceID: "CUDA0"}, {PID: 2, DeviceID: "CUDA1"}},
	}
	filtered := capability.filter(snapshot)
	if len(filtered.GPUs) != 1 || filtered.GPUs[0].ID != "CUDA0" {
		t.Fatalf("filtered GPUs=%+v", filtered.GPUs)
	}
	if len(filtered.Processes) != 1 || filtered.Processes[0].DeviceID != "CUDA0" {
		t.Fatalf("filtered processes=%+v", filtered.Processes)
	}
	if len(snapshot.GPUs) != 2 || len(snapshot.Processes) != 2 {
		t.Fatal("filter must not mutate the source snapshot slices")
	}
}

func TestRuntimeDeviceCapabilityManualValidation(t *testing.T) {
	capability := runtimeDevicesFromProfile(runtimeProfile("CUDA0", "Vulkan0"))
	if err := capability.validateManual([]string{"Vulkan0"}); err != nil {
		t.Fatalf("supported manual device rejected: %v", err)
	}
	if err := capability.validateManual([]string{"CUDA1"}); err == nil || !strings.Contains(err.Error(), "not exposed") {
		t.Fatalf("expected unsupported runtime device error, got %v", err)
	}
	unavailable := runtimeDevicesFromGetter(func() (llamacpp.Profile, error) {
		return llamacpp.Profile{}, errors.New("probe unavailable")
	})
	if err := unavailable.validateManual([]string{"CUDA0"}); err == nil || !strings.Contains(err.Error(), "discovery unavailable") {
		t.Fatalf("expected discovery error, got %v", err)
	}
	if err := unavailable.validateManual(nil); err != nil {
		t.Fatalf("CPU launch without devices must remain valid: %v", err)
	}
}

func TestRuntimeDeviceSnapshotterFiltersPlacementButPreservesHostSnapshot(t *testing.T) {
	s, _, _, _, _ := setupLifecycle(t, true, false)
	fake := &sequenceHardware{snapshots: []hardware.Snapshot{{
		RAMAvailableBytes: 1234,
		GPUs: []hardware.GPU{
			{ID: "CUDA0", FreeBytes: 6 * testGiB},
			{ID: "CUDA1", FreeBytes: 12 * testGiB},
		},
	}}}
	s.hardware = fake
	s.SetRuntimeDeviceProfile(func() (llamacpp.Profile, error) { return runtimeProfile("CUDA0"), nil })

	placement, err := s.preparePlacement(context.Background(), instances.Instance{ID: "target", GPUMode: "auto"}, 4*testGiB)
	if err != nil {
		t.Fatal(err)
	}
	if !placement.Fits || len(placement.Devices) != 1 || placement.Devices[0] != "CUDA0" {
		t.Fatalf("runtime-filtered placement=%+v", placement)
	}
	host, err := s.HostHardwareSnapshot(context.Background())
	if err != nil || len(host.GPUs) != 2 || host.RAMAvailableBytes != 1234 {
		t.Fatalf("host snapshot=%+v err=%v", host, err)
	}
}

func TestPreparePlacementCPUOnlyAndManualFailure(t *testing.T) {
	s, _, _, _, _ := setupLifecycle(t, true, false)
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * testGiB}}}}}
	profile := runtimeProfile()
	s.SetRuntimeDeviceProfile(func() (llamacpp.Profile, error) { return profile, nil })
	placement, err := s.preparePlacement(context.Background(), instances.Instance{ID: "auto", GPUMode: "auto"}, testGiB)
	if err != nil || len(placement.Devices) != 0 {
		t.Fatalf("CPU-only auto placement=%+v err=%v", placement, err)
	}
}

func TestPreparePlacementAllowsRuntimeOnlyManualBackend(t *testing.T) {
	s, _, _, _, _ := setupLifecycle(t, true, false)
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * testGiB}}}}}
	s.SetRuntimeDeviceProfile(func() (llamacpp.Profile, error) { return runtimeProfile("Vulkan0"), nil })
	placement, err := s.preparePlacement(context.Background(), instances.Instance{ID: "manual", GPUMode: "manual", GPUDevices: []string{"Vulkan0"}}, testGiB)
	if err != nil || len(placement.Devices) != 0 {
		t.Fatalf("manual runtime-only fallback=%+v err=%v", placement, err)
	}
}

func TestRuntimeDeviceProfileRefreshFiltersStalePlacement(t *testing.T) {
	s, _, _, _, _ := setupLifecycle(t, true, false)
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * testGiB}}}}}
	profile := runtimeProfile("CUDA0")
	s.SetRuntimeDeviceProfile(func() (llamacpp.Profile, error) { return profile, nil })
	instance := instances.Instance{ID: "auto", GPUMode: "auto"}
	placement, err := s.preparePlacement(context.Background(), instance, testGiB)
	if err != nil || len(placement.Devices) != 1 {
		t.Fatalf("initial placement=%+v err=%v", placement, err)
	}
	profile = runtimeProfile()
	placement, err = s.preparePlacement(context.Background(), instance, testGiB)
	if err != nil || len(placement.Devices) != 0 {
		t.Fatalf("stale runtime device remained addressable: %+v err=%v", placement, err)
	}
}

func TestRuntimeDiscoveryFailureFailsClosed(t *testing.T) {
	s, _, _, _, _ := setupLifecycle(t, true, false)
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * testGiB}}}}}
	s.SetRuntimeDeviceProfile(func() (llamacpp.Profile, error) {
		return llamacpp.Profile{DeviceDiscoveryError: "exit status 2"}, nil
	})
	placement, err := s.preparePlacement(context.Background(), instances.Instance{ID: "auto", GPUMode: "auto"}, testGiB)
	if err != nil || len(placement.Devices) != 0 {
		t.Fatalf("auto fail-closed placement=%+v err=%v", placement, err)
	}
}

func TestCPUOnlyStartDoesNotEmitPlacementDeviceFlags(t *testing.T) {
	ctx := context.Background()
	s, _, m, sup, _ := setupLifecycle(t, true, false)
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * testGiB}}}}}
	s.SetRuntimeDeviceProfile(func() (llamacpp.Profile, error) { return runtimeProfile(), nil })
	i, err := s.instances.Get(ctx, m.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	i.GPUMode = "auto"
	if _, err := s.startOne(ctx, i); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.StopInstance(context.Background(), i.ID) }()
	if sup.Status(i.ID).State != supervisor.Ready {
		t.Fatalf("worker state=%s", sup.Status(i.ID).State)
	}
	for _, line := range s.Logs(i.ID) {
		if !strings.Contains(line, "launch command:") {
			continue
		}
		if strings.Contains(line, "--device") || strings.Contains(line, "--split-mode") || strings.Contains(line, "--tensor-split") {
			t.Fatalf("CPU-only launch contained placement GPU flags: %s", line)
		}
		return
	}
	t.Fatal("missing launch command log")
}

func TestInvalidManualRuntimeDeviceFailsBeforeWorkerLaunch(t *testing.T) {
	ctx := context.Background()
	s, _, m, sup, _ := setupLifecycle(t, true, false)
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * testGiB}}}}}
	s.SetRuntimeDeviceProfile(func() (llamacpp.Profile, error) { return runtimeProfile(), nil })
	i, err := s.instances.Get(ctx, m.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	i.GPUMode = "manual"
	i.GPUDevices = []string{"CUDA0"}
	_, err = s.startOne(ctx, i)
	if err == nil || !strings.Contains(err.Error(), "not exposed") {
		t.Fatalf("expected manager runtime-device validation error, got %v", err)
	}
	if state := sup.Status(i.ID).State; state != supervisor.Unloaded {
		t.Fatalf("invalid manual placement launched worker, state=%s", state)
	}
	for _, line := range s.Logs(i.ID) {
		if strings.Contains(line, "launch command:") {
			t.Fatalf("invalid manual placement reached worker launch: %s", line)
		}
	}
}
