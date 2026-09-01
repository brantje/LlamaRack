package lifecycle

import (
	"errors"
	"fmt"
	"time"

	"github.com/brantje/llamarack/backend/internal/supervisor"
	"github.com/brantje/llamarack/backend/internal/systemlog"
)

// StartFailureState is session-local crash-loop protection for Instance startup.
type StartFailureState struct {
	ConsecutiveFailures int
	LastFailureAt       time.Time
	LastError           string
	RetryAfter          time.Time
}

var (
	errStartBackoff = errors.New("instance in startup backoff")

	// startFailureBackoffSchedule is the delay after 1st, 2nd, 3rd, 4th, and
	// 5th+ consecutive genuine start failures.
	startFailureBackoffSchedule = []time.Duration{
		15 * time.Second,
		30 * time.Second,
		60 * time.Second,
		2 * time.Minute,
		5 * time.Minute,
	}
)

func startFailureBackoffForCount(failures int) time.Duration {
	if failures <= 0 {
		return startFailureBackoffSchedule[0]
	}
	index := failures - 1
	if index >= len(startFailureBackoffSchedule) {
		index = len(startFailureBackoffSchedule) - 1
	}
	return startFailureBackoffSchedule[index]
}

func formatStartBackoff(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	minutes := d / time.Minute
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return d.String()
}

func (s *Service) clock() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Service) ensureStartFailuresLocked() {
	if s.startFailures == nil {
		s.startFailures = map[string]StartFailureState{}
	}
}

func (s *Service) startFailureState(id string) (StartFailureState, bool) {
	if s == nil {
		return StartFailureState{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.startFailures == nil {
		return StartFailureState{}, false
	}
	state, ok := s.startFailures[id]
	return state, ok
}

func (s *Service) inStartBackoff(id string) bool {
	state, ok := s.startFailureState(id)
	if !ok || state.RetryAfter.IsZero() {
		return false
	}
	return s.clock().Before(state.RetryAfter)
}

func (s *Service) startBackoffError(id string) error {
	state, ok := s.startFailureState(id)
	if !ok {
		return errStartBackoff
	}
	retry := state.RetryAfter.UTC().Format(time.RFC3339)
	if state.LastError != "" {
		return fmt.Errorf("%w until %s after %d consecutive start failures: %s", errStartBackoff, retry, state.ConsecutiveFailures, state.LastError)
	}
	return fmt.Errorf("%w until %s after %d consecutive start failures", errStartBackoff, retry, state.ConsecutiveFailures)
}

func (s *Service) recordStartFailure(id, lastError string) {
	if s == nil || id == "" {
		return
	}
	now := s.clock()
	s.mu.Lock()
	s.ensureStartFailuresLocked()
	state := s.startFailures[id]
	state.ConsecutiveFailures++
	state.LastFailureAt = now
	state.LastError = lastError
	delay := startFailureBackoffForCount(state.ConsecutiveFailures)
	state.RetryAfter = now.Add(delay)
	s.startFailures[id] = state
	failures := state.ConsecutiveFailures
	s.mu.Unlock()

	noun := "failures"
	if failures == 1 {
		noun = "failure"
	}
	systemlog.Log(systemlog.Warn, "manager", fmt.Sprintf("%s: startup backoff %s after %d consecutive start %s", id, formatStartBackoff(delay), failures, noun))
	s.publishRuntimeOverlay(id)
}

func (s *Service) overrideStartBackoff(id string) {
	if s == nil || id == "" {
		return
	}
	changed := false
	s.mu.Lock()
	if s.startFailures != nil {
		if state, ok := s.startFailures[id]; ok && !state.RetryAfter.IsZero() {
			state.RetryAfter = time.Time{}
			s.startFailures[id] = state
			changed = true
		}
	}
	s.mu.Unlock()
	if changed {
		s.publishRuntimeOverlay(id)
	}
}

func (s *Service) resetStartFailures(id string) {
	if s == nil || id == "" {
		return
	}
	changed := false
	s.mu.Lock()
	if s.startFailures != nil {
		if _, ok := s.startFailures[id]; ok {
			delete(s.startFailures, id)
			changed = true
		}
	}
	s.mu.Unlock()
	if changed {
		s.publishRuntimeOverlay(id)
	}
}

func (s *Service) attachStartFailure(runtime supervisor.Runtime) supervisor.Runtime {
	state, ok := s.startFailureState(runtime.InstanceID)
	if !ok {
		return runtime
	}
	runtime.ConsecutiveStartFailures = state.ConsecutiveFailures
	if !state.RetryAfter.IsZero() {
		retryAfter := state.RetryAfter.UTC()
		runtime.RetryAfter = &retryAfter
	}
	if runtime.LastError == "" && state.LastError != "" {
		runtime.LastError = state.LastError
	}
	return runtime
}

func (s *Service) publishRuntimeOverlay(id string) {
	if s == nil || s.sup == nil || id == "" {
		return
	}
	s.sup.PublishRuntime(s.attachStartFailure(s.sup.Status(id)))
}
