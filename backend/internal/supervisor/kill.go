package supervisor

import "errors"

// Kill immediately terminates a managed worker. It is intentionally distinct
// from Stop so the Instance control plane can expose an emergency kill action.
func (s *Supervisor) Kill(id string) error {
	s.mu.RLock()
	w := s.workers[id]
	if w == nil || w.cmd == nil || w.cmd.Process == nil {
		s.mu.RUnlock()
		return nil
	}
	process := w.cmd.Process
	s.mu.RUnlock()
	if err := process.Kill(); err != nil && !errors.Is(err, errors.ErrUnsupported) {
		return err
	}
	return nil
}
