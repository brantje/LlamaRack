package lifecycle

import "github.com/brantje/llamacpp-manager/backend/internal/supervisor"

// SubscribeRuntimes exposes supervisor-observed runtime state to management
// transports without allowing those transports to mutate worker processes.
func (s *Service) SubscribeRuntimes() ([]supervisor.Runtime, <-chan supervisor.Runtime, func()) {
	return s.sup.SubscribeRuntimes()
}
