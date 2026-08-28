package observability

import (
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/instances"
	"github.com/brantje/llamacpp-manager/backend/internal/lifecycle"
	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
)

type lifecycleTransition struct {
	Event    string
	Duration time.Duration
}

func classifyLifecycle(previous, next supervisor.Runtime, state lifecycle.OperationalState, instance *instances.Instance, idleTimeout time.Duration, now time.Time) []lifecycleTransition {
	if next.InstanceID == "" || previous.State == next.State { return nil }
	var transitions []lifecycleTransition
	if next.State == supervisor.Starting && state.Activity.ActiveRequests > 0 {
		transitions = append(transitions, lifecycleTransition{Event: LifecycleAutoload})
	}
	if next.State == supervisor.Ready {
		duration := time.Duration(0)
		if !next.StartedAt.IsZero() && !next.ReadyAt.IsZero() && next.ReadyAt.After(next.StartedAt) { duration = next.ReadyAt.Sub(next.StartedAt) }
		transitions = append(transitions, lifecycleTransition{Event: LifecycleLoad, Duration: duration})
	}
	if next.State == supervisor.Failed {
		transitions = append(transitions, lifecycleTransition{Event: LifecycleFailedStart})
	}
	if !runtimeWasRunning(previous.State) || (next.State != supervisor.Stopping && next.State != supervisor.Unloaded) { return transitions }
	if state.ResourceBlocked || state.ResourceStartActive {
		return append(transitions, lifecycleTransition{Event: LifecycleEviction})
	}
	if instance == nil || instance.AlwaysOn || state.ManuallyStopped || state.Activity.LastUsed.IsZero() { return transitions }
	idle := idleTimeout
	if instance.IdleUnloadSeconds > 0 { idle = time.Duration(instance.IdleUnloadSeconds) * time.Second }
	if idle > 0 && now.Sub(state.Activity.LastUsed) >= idle {
		transitions = append(transitions, lifecycleTransition{Event: LifecycleIdleUnload})
	}
	return transitions
}
