package lifecycle

import (
	"testing"
	"time"
)

func TestOperationalState(t *testing.T) {
	lastUsed := time.Date(2026, 8, 28, 8, 0, 0, 0, time.UTC)
	s := &Service{
		activities:      map[string]Activity{"one": {ActiveRequests: 2, LastUsed: lastUsed}},
		manuallyStopped: map[string]bool{"one": true},
		resourceBlocked: map[string]string{"one": resourcePressureReason},
		resourceStarts:  1,
	}
	state := s.OperationalState("one")
	if state.Activity.ActiveRequests != 2 || !state.Activity.LastUsed.Equal(lastUsed) {
		t.Fatalf("activity=%+v", state.Activity)
	}
	if !state.ManuallyStopped || !state.ResourceBlocked || !state.ResourceStartActive {
		t.Fatalf("state=%+v", state)
	}
	missing := s.OperationalState("missing")
	if missing.Activity.ActiveRequests != 0 || missing.ManuallyStopped || missing.ResourceBlocked || !missing.ResourceStartActive {
		t.Fatalf("missing=%+v", missing)
	}
}
