package config

import (
	"testing"
	"time"
)

func TestAlwaysOnReconcileIntervalConfiguration(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv("LLAMARACK_ALWAYS_ON_RECONCILE_SECONDS", "")
		if got := Load().AlwaysOnReconcileInterval; got != 15*time.Second {
			t.Fatalf("default interval=%v", got)
		}
	})

	t.Run("override", func(t *testing.T) {
		t.Setenv("LLAMARACK_ALWAYS_ON_RECONCILE_SECONDS", "60")
		if got := Load().AlwaysOnReconcileInterval; got != time.Minute {
			t.Fatalf("override interval=%v", got)
		}
	})

	t.Run("zero disables periodic reconciliation", func(t *testing.T) {
		t.Setenv("LLAMARACK_ALWAYS_ON_RECONCILE_SECONDS", "0")
		if got := Load().AlwaysOnReconcileInterval; got != 0 {
			t.Fatalf("disabled interval=%v", got)
		}
	})

	for _, value := range []string{"invalid", "-1"} {
		t.Run("invalid_"+value, func(t *testing.T) {
			t.Setenv("LLAMARACK_ALWAYS_ON_RECONCILE_SECONDS", value)
			if got := Load().AlwaysOnReconcileInterval; got != 15*time.Second {
				t.Fatalf("invalid value %q interval=%v", value, got)
			}
		})
	}
}
