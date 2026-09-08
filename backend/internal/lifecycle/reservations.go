package lifecycle

import "github.com/brantje/llamarack/backend/internal/scheduler"

// Reservations exposes the lifecycle scheduler ledger to manager-owned
// workloads that must compete with normal Instance workers for the same host
// resources. Callers must use their own scheduler owner namespace.
func (s *Service) Reservations() *scheduler.Ledger {
	if s == nil {
		return nil
	}
	return s.reservations
}
