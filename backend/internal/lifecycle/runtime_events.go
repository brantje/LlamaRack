package lifecycle

import (
	"context"

	"github.com/brantje/llamarack/backend/internal/supervisor"
)

// SubscribeRuntimes exposes an atomic supervisor snapshot plus live observed
// transitions. Durable stopped Instances are present as UNLOADED.
func (s *Service) SubscribeRuntimes(ctx context.Context) ([]supervisor.Runtime, <-chan supervisor.Runtime, func(), error) {
	snapshot, events, cancel := s.sup.SubscribeRuntimes()
	known := make(map[string]struct{}, len(snapshot))
	for _, runtime := range snapshot {
		known[runtime.InstanceID] = struct{}{}
	}
	items, err := s.instances.List(ctx)
	if err != nil {
		cancel()
		return nil, nil, func() {}, err
	}
	for _, instance := range items {
		if _, ok := known[instance.ID]; ok {
			continue
		}
		snapshot = append(snapshot, supervisor.Runtime{
			InstanceID: instance.ID,
			ModelID:    instance.ModelID,
			State:      supervisor.Unloaded,
		})
	}
	return snapshot, events, cancel, nil
}
