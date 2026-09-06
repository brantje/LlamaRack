package lifecycle

import (
	"context"
	"fmt"
	"strings"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/llamacpp"
)

type runtimeDeviceCapability struct {
	enforced  bool
	available bool
	profile   llamacpp.Profile
	reason    string
}

type runtimeDeviceSnapshotter struct {
	base    hardware.Snapshotter
	profile func() (llamacpp.Profile, error)
}

func (s *Service) SetRuntimeDeviceProfile(getter func() (llamacpp.Profile, error)) {
	s.SetProfileGetter(getter)
	if s.hardware != nil {
		base := s.hardware
		if current, ok := base.(*runtimeDeviceSnapshotter); ok {
			base = current.base
		}
		s.hardware = &runtimeDeviceSnapshotter{base: base, profile: getter}
	}
	if s.sup != nil {
		s.sup.SetDeviceValidator(func(devices []string) error {
			return runtimeDevicesFromGetter(getter).validateManual(devices)
		})
	}
}

// HostHardwareSnapshot bypasses the runtime-addressability filter. Placement
// uses the filtered snapshot, while observability can still report physical host
// hardware that the active llama-server build cannot address.
func (s *Service) HostHardwareSnapshot(ctx context.Context) (hardware.Snapshot, error) {
	if s == nil || s.hardware == nil {
		return hardware.Snapshot{}, nil
	}
	if filtered, ok := s.hardware.(*runtimeDeviceSnapshotter); ok && filtered.base != nil {
		return filtered.base.Snapshot(ctx)
	}
	return s.hardware.Snapshot(ctx)
}

func (s *runtimeDeviceSnapshotter) Snapshot(ctx context.Context) (hardware.Snapshot, error) {
	if s == nil || s.base == nil {
		return hardware.Snapshot{}, nil
	}
	snapshot, err := s.base.Snapshot(ctx)
	if err != nil {
		return snapshot, err
	}
	return runtimeDevicesFromGetter(s.profile).filter(snapshot), nil
}

func runtimeDevicesFromGetter(getter func() (llamacpp.Profile, error)) runtimeDeviceCapability {
	if getter == nil {
		return runtimeDeviceCapability{}
	}
	profile, err := getter()
	if err != nil {
		return runtimeDeviceCapability{enforced: true, reason: err.Error()}
	}
	return runtimeDevicesFromProfile(profile)
}

func runtimeDevicesFromProfile(profile llamacpp.Profile) runtimeDeviceCapability {
	if profile.DeviceDiscoveryAvailable {
		return runtimeDeviceCapability{enforced: true, available: true, profile: profile}
	}
	if strings.TrimSpace(profile.DeviceDiscoveryError) != "" {
		return runtimeDeviceCapability{enforced: true, profile: profile, reason: profile.DeviceDiscoveryError}
	}
	// Hand-constructed/legacy profiles predate runtime device discovery. Keep
	// them non-enforcing so compatibility callers do not accidentally turn an
	// absent field into an authoritative CPU-only declaration.
	return runtimeDeviceCapability{profile: profile}
}

func (c runtimeDeviceCapability) filter(snapshot hardware.Snapshot) hardware.Snapshot {
	if !c.enforced || !c.available {
		if c.enforced {
			snapshot.GPUs = []hardware.GPU{}
			snapshot.Processes = []hardware.GPUProcess{}
		}
		return snapshot
	}
	supported := make(map[string]bool, len(c.profile.Devices))
	for _, raw := range c.profile.Devices {
		if id := strings.TrimSpace(raw); id != "" {
			supported[id] = true
		}
	}
	gpus := make([]hardware.GPU, 0, len(snapshot.GPUs))
	for _, gpu := range snapshot.GPUs {
		if supported[strings.TrimSpace(gpu.ID)] {
			gpus = append(gpus, gpu)
		}
	}
	processes := make([]hardware.GPUProcess, 0, len(snapshot.Processes))
	for _, process := range snapshot.Processes {
		if supported[strings.TrimSpace(process.DeviceID)] {
			processes = append(processes, process)
		}
	}
	snapshot.GPUs = gpus
	snapshot.Processes = processes
	return snapshot
}

func (c runtimeDeviceCapability) validateManual(devices []string) error {
	normalized := make([]string, 0, len(devices))
	for _, raw := range devices {
		if id := strings.TrimSpace(raw); id != "" {
			normalized = append(normalized, id)
		}
	}
	if len(normalized) == 0 || !c.enforced {
		return nil
	}
	if !c.available {
		if strings.TrimSpace(c.reason) == "" {
			return fmt.Errorf("cannot validate manual GPU placement: llama-server device discovery unavailable")
		}
		return fmt.Errorf("cannot validate manual GPU placement: llama-server device discovery unavailable: %s", c.reason)
	}
	supported := make(map[string]bool, len(c.profile.Devices))
	for _, raw := range c.profile.Devices {
		if id := strings.TrimSpace(raw); id != "" {
			supported[id] = true
		}
	}
	for _, id := range normalized {
		if !supported[id] {
			return fmt.Errorf("configured GPU device is not exposed by the active llama-server runtime: %s", id)
		}
	}
	return nil
}
