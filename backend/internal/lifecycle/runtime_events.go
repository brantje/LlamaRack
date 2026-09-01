package lifecycle

import (
	"context"

	"github.com/brantje/llamarack/backend/internal/supervisor"
)

// SubscribeRuntimes exposes an atomic supervisor snapshot plus live observed
// transitions. Durable stopped Instances are present as UNLOADED. Startup
// backoff fields are overlaid so clients see why automatic retry is delayed.
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
	for i := range snapshot {
		snapshot[i] = s.attachStartFailure(snapshot[i])
	}

	wrapped := make(chan supervisor.Runtime, 64)
	go func() {
		defer close(wrapped)
		for runtime := range events {
			select {
			case wrapped <- s.attachStartFailure(runtime):
			case <-ctx.Done():
				return
			}
		}
	}()
	return snapshot, wrapped, cancel, nil
}
