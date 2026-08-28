package lifecycle

// OperationalState exposes the small amount of lifecycle state needed to
// classify runtime transitions for observability. It deliberately contains no
// persistence or observability-package dependency so lifecycle remains the
// owner of scheduling semantics.
type OperationalState struct {
	Activity            Activity `json:"activity"`
	ManuallyStopped     bool     `json:"manually_stopped"`
	ResourceBlocked     bool     `json:"resource_blocked"`
	ResourceStartActive bool     `json:"resource_start_active"`
}

func (s *Service) OperationalState(id string) OperationalState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return OperationalState{
		Activity:            s.activities[id],
		ManuallyStopped:     s.manuallyStopped[id],
		ResourceBlocked:     s.resourceBlocked[id] != "",
		ResourceStartActive: s.resourceStarts > 0,
	}
}
