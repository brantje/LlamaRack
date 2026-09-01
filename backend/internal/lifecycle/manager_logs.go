package lifecycle

import (
	"context"
	"fmt"
	"strings"

	"github.com/brantje/llamarack/backend/internal/systemlog"
)

func (s *Service) AddManagerLog(instanceID, line string) {
	if s == nil || s.sup == nil {
		return
	}
	instanceID = strings.TrimSpace(instanceID)
	line = strings.TrimSpace(line)
	if instanceID == "" || line == "" {
		return
	}
	s.sup.AddManagerLog(instanceID, line)

	switch {
	case strings.HasPrefix(line, "idle-unloaded after "):
		duration := strings.TrimSuffix(strings.TrimPrefix(line, "idle-unloaded after "), " without active requests")
		systemlog.Log(systemlog.Info, "manager", fmt.Sprintf("idle unload %s after %s without activity", instanceID, duration))
	case strings.HasPrefix(line, "worker ready after "):
		s.resetStartFailures(instanceID)
	case line == "worker stopped":
		s.resetStartFailures(instanceID)
		s.logReleasedEstimate(instanceID, false)
	case line == "worker killed":
		s.resetStartFailures(instanceID)
	case line == "evicted for resource pressure":
		s.logReleasedEstimate(instanceID, true)
	default:
		systemlog.Log(systemlog.Debug, "manager", instanceID+": "+line)
	}
}

func (s *Service) logAlwaysOnReconcile(satisfied int) {
	label := "Instances"
	if satisfied == 1 {
		label = "Instance"
	}
	systemlog.Log(systemlog.Info, "manager", fmt.Sprintf("reconcile: %d Always On %s satisfied", satisfied, label))
}

func (s *Service) logReleasedEstimate(instanceID string, evictionPlan bool) {
	if s.instances == nil || s.models == nil {
		return
	}
	instance, err := s.instances.Get(context.Background(), instanceID)
	if err != nil {
		return
	}
	model, err := s.models.GetByID(context.Background(), instance.ModelID)
	if err != nil || model.TotalBytes <= 0 {
		return
	}
	device := "GPU"
	if len(instance.GPUDevices) > 0 && strings.TrimSpace(instance.GPUDevices[0]) != "" {
		device = instance.GPUDevices[0]
	}
	plan := "not required"
	if evictionPlan {
		plan = "required"
	}
	systemlog.Log(systemlog.Debug, "manager", fmt.Sprintf("released %.1f GiB on %s (eviction plan %s)", float64(model.TotalBytes)/(1<<30), device, plan))
}
