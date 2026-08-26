package lifecycle

import (
	"context"
	"strings"
	"testing"
)

func TestManualStartRejectsDisabledModel(t *testing.T) {
	s, _, m, _, exec := setupLifecycle(t, true, false)
	exec("UPDATE models SET enabled=0 WHERE id=?", m.ID)
	if _, err := s.StartModel(context.Background(), m.ID); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled model start error, got %v", err)
	}
}
