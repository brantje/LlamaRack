package lifecycle

import (
	"context"

	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
)

// SubscribeRuntimes exposes an atomic supervisor snapshot plus live observed
// transitions. Configured instances that have never started are added to the
// snapshot as UNLOADED so a reconnect can clear stale browser runtime state
// after a manager restart.
func (s *Service) SubscribeRuntimes(ctx context.Context) ([]supervisor.Runtime, <-chan supervisor.Runtime, func(), error) {
	snapshot, events, cancel := s.sup.SubscribeRuntimes()
	known := make(map[string]struct{}, len(snapshot))
	for _, runtime := range snapshot {
		known[runtime.InstanceID] = struct{}{}
	}

	models, err := s.models.List(ctx)
	if err != nil {
		cancel()
		return nil, nil, func() {}, err
	}
	for _, model := range models {
		instances, err := s.models.Instances(ctx, model.ID)
		if err != nil {
			cancel()
			return nil, nil, func() {}, err
		}
		for _, instance := range instances {
			if _, ok := known[instance.ID]; ok {
				continue
			}
			snapshot = append(snapshot, supervisor.Runtime{
				InstanceID: instance.ID,
				ModelID:    model.ID,
				State:      supervisor.Unloaded,
			})
			known[instance.ID] = struct{}{}
		}
	}
	return snapshot, events, cancel, nil
}
