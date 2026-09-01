package lifecycle

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/brantje/llamarack/backend/internal/systemlog"
)

type failureBackoffKey struct {
	service *Service
	id      string
}

type failureBackoffState struct {
	failures   int
	backingOff bool
	generation uint64
}

var (
	failureBackoffMu       sync.Mutex
	failureBackoffs        = map[failureBackoffKey]*failureBackoffState{}
	startFailureBackoffFor = 60 * time.Second
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
	case strings.HasPrefix(line, "worker failed to start: "):
		s.noteStartFailure(instanceID)
	case strings.HasPrefix(line, "worker ready after "):
		s.clearStartFailures(instanceID)
	case line == "worker stopped":
		s.cancelStartFailureBackoff(instanceID)
		s.logReleasedEstimate(instanceID, false)
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

func (s *Service) noteStartFailure(instanceID string) {
	key := failureBackoffKey{service: s, id: instanceID}
	failureBackoffMu.Lock()
	state := failureBackoffs[key]
	if state == nil {
		state = &failureBackoffState{}
		failureBackoffs[key] = state
	}
	state.failures++
	if state.failures < 3 || state.backingOff {
		failureBackoffMu.Unlock()
		return
	}
	state.backingOff = true
	state.generation++
	generation := state.generation
	failureBackoffMu.Unlock()

	// Always-On reconciliation already skips manually-stopped Instances. Reuse
	// that gate for the short automatic failure backoff while still allowing an
	// explicit user/inference start to override it immediately.
	s.markManualStop(instanceID)
	delay := startFailureBackoffFor
	backoffLabel := fmt.Sprintf("%.0fs", delay.Seconds())
	systemlog.Log(systemlog.Warn, "manager", fmt.Sprintf("%s: 3 consecutive start failures, backing off %s", instanceID, backoffLabel))
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		<-timer.C
		failureBackoffMu.Lock()
		current := failureBackoffs[key]
		if current == nil || !current.backingOff || current.generation != generation {
			failureBackoffMu.Unlock()
			return
		}
		delete(failureBackoffs, key)
		failureBackoffMu.Unlock()
		s.clearManualStop(instanceID)
	}()
}

func (s *Service) clearStartFailures(instanceID string) {
	key := failureBackoffKey{service: s, id: instanceID}
	failureBackoffMu.Lock()
	state := failureBackoffs[key]
	if state != nil {
		state.generation++
		delete(failureBackoffs, key)
	}
	failureBackoffMu.Unlock()
}

func (s *Service) cancelStartFailureBackoff(instanceID string) {
	key := failureBackoffKey{service: s, id: instanceID}
	failureBackoffMu.Lock()
	state := failureBackoffs[key]
	if state != nil {
		state.generation++
		delete(failureBackoffs, key)
	}
	failureBackoffMu.Unlock()
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
