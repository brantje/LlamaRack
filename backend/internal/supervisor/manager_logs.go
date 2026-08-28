package supervisor

import "strings"

// AddManagerLog adds an in-memory manager lifecycle line beside the worker's
// stdout/stderr ring. Manager lines intentionally share the same bounded ring
// and retention behavior as raw worker output.
func (s *Supervisor) AddManagerLog(instanceID, line string) {
	line = strings.TrimSpace(line)
	if instanceID == "" || line == "" { return }
	s.mu.Lock()
	logRing := s.logRingLocked(instanceID)
	s.mu.Unlock()
	logRing.add("[manager] " + line)
}
