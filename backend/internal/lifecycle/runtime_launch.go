package lifecycle

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/llamacpp"
	"github.com/brantje/llamarack/backend/internal/models"
	"github.com/brantje/llamarack/backend/internal/recommendations"
	"github.com/brantje/llamarack/backend/internal/scheduler"
)

func (s *Service) prepareRuntimeLaunch(
	ctx context.Context,
	instance instances.Instance,
	model models.Model,
	path string,
	launchOptions map[string]string,
	profile llamacpp.Profile,
	allowEviction bool,
) (scheduler.RuntimePlan, error) {
	if strings.EqualFold(strings.TrimSpace(instance.GPUMode), "manual") {
		if err := runtimeDevicesFromProfile(profile).validateManual(instance.GPUDevices); err != nil {
			return scheduler.RuntimePlan{}, err
		}
	}
	demandInput := s.demandInput(model, path, launchOptions)
	request := scheduler.RuntimePlanRequest{
		Demand: demandInput,
		Placement: scheduler.PlacementRequest{
			Mode: instance.GPUMode, Devices: append([]string(nil), instance.GPUDevices...), TensorSplit: instance.TensorSplit,
		},
		AllowSystemSpillover: instance.SystemSpilloverEnabled,
		Capabilities:         runtimeCapabilities(profile),
	}
	if s.hardware == nil || s.reservations == nil {
		demand := scheduler.EstimateDemand(demandInput)
		return scheduler.RuntimePlan{
			Fits: true, Mode: "unmanaged", Demand: demand,
			Options: cloneOptions(launchOptions),
		}, nil
	}

	snapshot, err := s.hardware.Snapshot(ctx)
	if err != nil {
		slog.Warn("hardware snapshot unavailable; preserving compatibility placement", "instance_id", instance.ID, "error", err)
		demand := scheduler.EstimateDemand(demandInput)
		return scheduler.RuntimePlan{
			Fits: true, Mode: "unmanaged", Demand: demand,
			Options: cloneOptions(launchOptions),
		}, nil
	}

	idleSnapshot := recommendations.AssumeIdleSnapshot(snapshot)
	request.IdleSnapshot = &idleSnapshot
	stopped := make([]scheduler.Candidate, 0, 2)
	skip := map[string]bool{}

	for attempt := 0; attempt <= maxEvictionPlanAttempts; attempt++ {
		lease, plan, err := s.reservations.AcquireRuntime(scheduler.RuntimeAcquireRequest{
			InstanceID: instance.ID,
			Snapshot:   snapshot,
			Plan:       request,
			Credits:    scheduler.CreditsFromCandidates(stopped),
		})
		if err != nil {
			return scheduler.RuntimePlan{}, err
		}
		if plan.Fits {
			s.logReservation("reserved", instance.ID, lease)
			return plan, nil
		}
		if !allowEviction {
			return scheduler.RuntimePlan{}, runtimePressureError(plan)
		}
		if attempt == maxEvictionPlanAttempts {
			return scheduler.RuntimePlan{}, fmt.Errorf("%w: eviction attempts exhausted", errResourcePressureBlocked)
		}

		owner := scheduler.ResourceOwner{Kind: scheduler.ResourceOwnerInstance, ID: instance.ID}
		baseCredits := scheduler.CreditsFromCandidates(stopped)
		snapshotFor := func(selected []scheduler.Candidate) hardware.Snapshot {
			credits := append([]scheduler.Credit(nil), baseCredits...)
			credits = append(credits, scheduler.CreditsFromCandidates(selected)...)
			return s.reservations.PlanningSnapshotWithCredits(snapshot, owner, credits)
		}
		planning := snapshotFor(nil)
		candidates, err := s.evictionCandidates(ctx, planning, instance.ID, skip)
		if err != nil {
			return scheduler.RuntimePlan{}, err
		}
		eviction := scheduler.PlanRuntimeEvictionsWithSnapshots(candidates, request, snapshotFor)
		if !eviction.Fits {
			return scheduler.RuntimePlan{}, runtimePressureError(plan)
		}

		claim := append(append([]scheduler.Candidate(nil), stopped...), eviction.Evict...)
		claimedLease, claimedPlan, err := s.reservations.AcquireRuntime(scheduler.RuntimeAcquireRequest{
			InstanceID: instance.ID,
			Snapshot:   snapshot,
			Plan:       request,
			Credits:    scheduler.CreditsFromCandidates(claim),
		})
		if err != nil {
			return scheduler.RuntimePlan{}, err
		}
		if !claimedPlan.Fits {
			return scheduler.RuntimePlan{}, runtimePressureError(claimedPlan)
		}
		s.logReservation("reserved", instance.ID, claimedLease)

		var victim scheduler.Candidate
		for _, candidate := range eviction.Evict {
			if candidate.InstanceID != "" && candidate.InstanceID != instance.ID {
				victim = candidate
				break
			}
		}
		if victim.InstanceID == "" {
			return claimedPlan, nil
		}
		if !s.victimPlacementCurrent(victim, snapshot) {
			s.releaseReservation(instance.ID)
			skip[victim.InstanceID] = true
			continue
		}
		if err := s.evictInstance(ctx, victim.InstanceID); err != nil {
			if err == errEvictionIneligible {
				s.releaseReservation(instance.ID)
				skip[victim.InstanceID] = true
				continue
			}
			s.releaseReservation(instance.ID)
			return scheduler.RuntimePlan{}, fmt.Errorf("evict %s: %w", victim.InstanceID, err)
		}
		stopped = append(stopped, victim)

		snapshot, err = s.hardware.Snapshot(ctx)
		if err != nil {
			s.releaseReservation(instance.ID)
			return scheduler.RuntimePlan{}, fmt.Errorf("refresh hardware after eviction: %w", err)
		}
		stopped = stopped[:0]
	}
	return scheduler.RuntimePlan{}, fmt.Errorf("%w: eviction attempts exhausted", errResourcePressureBlocked)
}

func runtimeCapabilities(profile llamacpp.Profile) scheduler.RuntimeCapabilities {
	return scheduler.RuntimeCapabilities{
		NCPUMoe:         profile.Has("n-cpu-moe"),
		CPUMoe:          profile.Has("cpu-moe"),
		NoKVOffload:     profile.Has("no-kv-offload"),
		GPULayers:       profile.Has("n-gpu-layers") || profile.Has("gpu-layers"),
		GPULayersOption: gpuLayerOptionFromProfile(profile),
	}
}

func gpuLayerOptionFromProfile(profile llamacpp.Profile) string {
	if profile.Has("n-gpu-layers") {
		return "n-gpu-layers"
	}
	if profile.Has("gpu-layers") {
		return "gpu-layers"
	}
	return ""
}

func runtimePressureError(plan scheduler.RuntimePlan) error {
	required := plan.Demand.VRAMBytes()
	available := plan.Placement.AvailableBytes
	if plan.Demand.HostRAMBytes > 0 {
		return fmt.Errorf("%w: insufficient usable VRAM or host RAM: need %d VRAM bytes and %d host RAM bytes, have %d usable VRAM bytes", errResourcePressureBlocked, required, plan.Demand.HostRAMBytes, available)
	}
	return fmt.Errorf("%w: insufficient usable VRAM: need %d bytes, have %d", errResourcePressureBlocked, required, available)
}

func (s *Service) logRuntimeSpill(instanceID string, plan scheduler.RuntimePlan) {
	if !plan.RequiresSpillover {
		return
	}
	switch plan.Mode {
	case "partial", "hybrid":
		layers := optionInt64(plan.Options, "n-gpu-layers")
		if layers == 0 {
			layers = optionInt64(plan.Options, "gpu-layers")
		}
		device := strings.Join(plan.Placement.Devices, ",")
		s.AddManagerLog(instanceID, fmt.Sprintf("spillover %s layers=%d device=%s host_ram=%d", plan.Mode, layers, device, plan.Demand.HostRAMBytes))
	case "cpu":
		s.AddManagerLog(instanceID, fmt.Sprintf("spillover cpu-only ram=%d", plan.Demand.HostRAMBytes))
	}
}
