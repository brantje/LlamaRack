package lifecycle

import (
	"context"
	"strings"
	"testing"
)

func TestManualStartRejectsDisabledInstance(t *testing.T) {
	s, _, m, _, exec := setupLifecycle(t, true, false)
	exec("UPDATE instances SET enabled=0 WHERE model_id=?", m.ID)
	if _, err := s.StartModel(context.Background(), m.ID); err == nil || (!strings.Contains(err.Error(), "disabled") && !strings.Contains(err.Error(), "no enabled instance")) {
		t.Fatalf("expected disabled instance start error, got %v", err)
	}
}
