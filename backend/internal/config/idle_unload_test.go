package config

import (
	"testing"
	"time"
)

func TestIdleUnloadTimeoutConfiguration(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv("LCM_IDLE_UNLOAD_SECONDS", "")
		if got := Load().IdleUnloadTimeout; got != 5*time.Minute {
			t.Fatalf("default idle timeout=%v", got)
		}
	})

	t.Run("override", func(t *testing.T) {
		t.Setenv("LCM_IDLE_UNLOAD_SECONDS", "900")
		if got := Load().IdleUnloadTimeout; got != 15*time.Minute {
			t.Fatalf("override idle timeout=%v", got)
		}
	})

	t.Run("zero disables", func(t *testing.T) {
		t.Setenv("LCM_IDLE_UNLOAD_SECONDS", "0")
		if got := Load().IdleUnloadTimeout; got != 0 {
			t.Fatalf("disabled idle timeout=%v", got)
		}
	})

	for _, value := range []string{"invalid", "-1"} {
		t.Run("invalid_"+value, func(t *testing.T) {
			t.Setenv("LCM_IDLE_UNLOAD_SECONDS", value)
			if got := Load().IdleUnloadTimeout; got != 5*time.Minute {
				t.Fatalf("invalid value %q idle timeout=%v", value, got)
			}
		})
	}
}
