package supervisor

import (
	"strconv"
	"strings"
	"time"
)

// AddManagerLog adds an in-memory manager lifecycle line beside the worker's
// stdout/stderr ring. Manager lines intentionally share the same bounded ring
// and retention behavior as raw worker output.
func (s *Supervisor) AddManagerLog(instanceID, line string) {
	line = strings.TrimSpace(line)
	if instanceID == "" || line == "" { return }
	s.mu.Lock()
	logRing := s.logRingLocked(instanceID)
	s.mu.Unlock()
	logRing.add(formatStoredLogLine("manager", line))
}

// formatStoredLogLine keeps the supervisor ring backward-compatible with its
// compact string storage while attaching the receive timestamp used by the
// structured log API. Tabs make the timestamp boundary unambiguous even when
// the original worker line contains spaces.
func formatStoredLogLine(source, line string) string {
	return "[" + source + "]\t" + time.Now().UTC().Format(time.RFC3339Nano) + "\t" + line
}

// formatLaunchCommand renders the exact argv passed to exec.Command in a form
// that is unambiguous in the manager log. Every token is quoted so paths and
// values containing whitespace remain distinguishable.
func formatLaunchCommand(binary string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, strconv.Quote(binary))
	for _, arg := range args {
		parts = append(parts, strconv.Quote(arg))
	}
	return strings.Join(parts, " ")
}
