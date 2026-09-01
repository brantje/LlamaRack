package supervisor

import (
	"errors"
	"os"
)

// ErrKilled is returned when a worker start is interrupted by Kill.
var ErrKilled = errors.New("startup killed")

// Kill immediately terminates a managed worker. It is intentionally distinct
// from Stop so the Instance control plane can expose an emergency kill action.
func (s *Supervisor) Kill(id string) error {
	s.mu.Lock()
	w := s.workers[id]
	if w == nil {
		s.mu.Unlock()
		return nil
	}
	w.killed = true
	cancel := w.startCancel
	var process *os.Process
	if w.cmd != nil {
		process = w.cmd.Process
	}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if process == nil {
		return nil
	}
	if err := process.Kill(); err != nil && !errors.Is(err, errors.ErrUnsupported) && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}
