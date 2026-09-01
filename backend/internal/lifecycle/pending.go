package lifecycle

import (
	"context"
	"errors"

	"github.com/brantje/llamarack/backend/internal/instances"
)

const (
	defaultMaxPendingPerInstance = 32
	defaultMaxPendingGlobal      = 128
	queueLimitScopeInstance      = "instance"
	queueLimitScopeGlobal        = "global"
)

// ErrQueueLimitExceeded is returned when a request is rejected because the
// Instance or manager pending-request admission cap has been reached.
var ErrQueueLimitExceeded = errors.New("pending request limit exceeded")

type queueLimitError struct {
	scope string
}

func (e *queueLimitError) Error() string {
	if e.scope == queueLimitScopeGlobal {
		return "too many pending requests across the manager"
	}
	return "too many pending requests for this model"
}

func (e *queueLimitError) Unwrap() error { return ErrQueueLimitExceeded }

// QueueLimitScope reports whether a queue-limit error was caused by the
// per-Instance cap or the manager-wide cap.
func QueueLimitScope(err error) string {
	var typed *queueLimitError
	if errors.As(err, &typed) {
		return typed.scope
	}
	return ""
}

func (s *Service) SetPendingLimits(getter func(context.Context) (perInstance, global int)) {
	s.pendingLimits = getter
}

func (s *Service) SetLoadHold(fn func(id string)) {
	s.holdLoad = fn
}

func (s *Service) managerPendingLimits(ctx context.Context) (perInstance, global int) {
	if s.pendingLimits != nil {
		return s.pendingLimits(ctx)
	}
	return defaultMaxPendingPerInstance, defaultMaxPendingGlobal
}

func effectivePendingLimit(instanceOverride, managerDefault int) int {
	if instanceOverride > 0 {
		return instanceOverride
	}
	return managerDefault
}

func (s *Service) reservePending(ctx context.Context, i instances.Instance) error {
	perInstanceDefault, global := s.managerPendingLimits(ctx)
	effective := effectivePendingLimit(i.MaxPendingRequests, perInstanceDefault)
	s.mu.Lock()
	defer s.mu.Unlock()
	activity := s.activities[i.ID]
	if effective > 0 && activity.PendingRequests >= effective {
		return &queueLimitError{scope: queueLimitScopeInstance}
	}
	if global > 0 && s.globalPendingLocked() >= global {
		return &queueLimitError{scope: queueLimitScopeGlobal}
	}
	activity.PendingRequests++
	activity.LastUsed = s.now().UTC()
	s.activities[i.ID] = activity
	return nil
}

func (s *Service) globalPendingLocked() int {
	total := 0
	for _, activity := range s.activities {
		total += activity.PendingRequests
	}
	return total
}

func (s *Service) releasePending(id string) {
	s.mu.Lock()
	activity := s.activities[id]
	if activity.PendingRequests > 0 {
		activity.PendingRequests--
	}
	activity.LastUsed = s.now().UTC()
	s.activities[id] = activity
	s.mu.Unlock()
}

func (s *Service) activateRequest(id string) {
	s.mu.Lock()
	activity := s.activities[id]
	if activity.PendingRequests > 0 {
		activity.PendingRequests--
	}
	activity.ActiveRequests++
	activity.LastUsed = s.now().UTC()
	s.activities[id] = activity
	s.mu.Unlock()
}

func (s *Service) finishActive(id string) {
	s.mu.Lock()
	activity := s.activities[id]
	if activity.ActiveRequests > 0 {
		activity.ActiveRequests--
	}
	activity.LastUsed = s.now().UTC()
	s.activities[id] = activity
	s.mu.Unlock()
}

func (a Activity) demand() int {
	return a.PendingRequests + a.ActiveRequests
}
