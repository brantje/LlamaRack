package settings

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/database"
)

func testSettings(t *testing.T) *Service {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db, Defaults{
		SessionLifetime: 24 * time.Hour, AllowedOrigins: "http://localhost:3000", StartupTimeout: 180 * time.Second,
		AlwaysOnReconcile: 15 * time.Second,
		DataDir:           "/config", ModelsDir: "/models", DatabasePath: "/config/manager.db", ListenAddr: ":8000", LlamaServerPath: "llama-server",
	})
}

func TestGeneralDefaultsAndDatabaseOverrides(t *testing.T) {
	ctx := context.Background()
	s := testSettings(t)
	general, err := s.General(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if general.SessionLifetime.Value != 86400 || general.SessionLifetime.Source != "default" || !general.SessionLifetime.Editable {
		t.Fatalf("session default=%+v", general.SessionLifetime)
	}
	if general.IdleUnloadTimeout.Value != 300 || general.IdleUnloadTimeout.Source != "default" || !general.IdleUnloadTimeout.Editable {
		t.Fatalf("idle unload default=%+v", general.IdleUnloadTimeout)
	}
	if general.ObservabilityRetention.Value != 30 || general.ObservabilityRetention.Source != "default" || !general.ObservabilityRetention.Editable {
		t.Fatalf("observability retention default=%+v", general.ObservabilityRetention)
	}
	if general.PrometheusToken.Value != "" || general.PrometheusToken.Source != "default" || !general.PrometheusToken.Editable {
		t.Fatalf("prometheus token default=%+v", general.PrometheusToken)
	}
	if general.Runtime.ModelsDir != "/models" || general.AllowedOrigins.Value != "http://localhost:3000" {
		t.Fatalf("general=%+v", general)
	}
	value, err := s.Set(ctx, SessionLifetimeSeconds, 7200)
	if err != nil || value.Value != 7200 || value.Source != "database" {
		t.Fatalf("set=%+v err=%v", value, err)
	}
	if got, err := s.Int(ctx, SessionLifetimeSeconds); err != nil || got != 7200 {
		t.Fatalf("int=%d err=%v", got, err)
	}
	if _, err := s.Set(ctx, LoginProtectionEnabled, false); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Bool(ctx, LoginProtectionEnabled); err != nil || got {
		t.Fatalf("bool=%v err=%v", got, err)
	}
	if _, err := s.Set(ctx, TrustedProxies, "10.0.0.0/8"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.String(ctx, TrustedProxies); err != nil || got != "10.0.0.0/8" {
		t.Fatalf("string=%q err=%v", got, err)
	}
	if _, err := s.Set(ctx, IdleUnloadSeconds, 600); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Int(ctx, IdleUnloadSeconds); err != nil || got != 600 {
		t.Fatalf("idle unload=%d err=%v", got, err)
	}
	if _, err := s.Set(ctx, ObservabilityRetentionDays, 45); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Int(ctx, ObservabilityRetentionDays); err != nil || got != 45 {
		t.Fatalf("retention=%d err=%v", got, err)
	}
	if _, err := s.Set(ctx, PrometheusAuthToken, "dashboard-token"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.String(ctx, PrometheusAuthToken); err != nil || got != "dashboard-token" {
		t.Fatalf("prometheus token=%q err=%v", got, err)
	}
}

func TestPrometheusTokenDatabaseCanOverrideEnvironment(t *testing.T) {
	ctx := context.Background()
	t.Setenv("LLAMARACK_PROMETHEUS_AUTH_TOKEN", "environment-token")
	s := testSettings(t)
	value, err := s.Resolve(ctx, PrometheusAuthToken)
	if err != nil || value.Value != "environment-token" || value.Source != "environment" || !value.Editable {
		t.Fatalf("environment token=%+v err=%v", value, err)
	}
	if _, err := s.Set(ctx, PrometheusAuthToken, "database-token"); err != nil {
		t.Fatal(err)
	}
	value, err = s.Resolve(ctx, PrometheusAuthToken)
	if err != nil || value.Value != "database-token" || value.Source != "database" || !value.Editable {
		t.Fatalf("database token=%+v err=%v", value, err)
	}
}

func TestEnvironmentOverridesDatabaseAndValidation(t *testing.T) {
	ctx := context.Background()
	s := testSettings(t)
	if _, err := s.Set(ctx, LoginFailureThreshold, 7); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLAMARACK_LOGIN_FAILURE_THRESHOLD", "9")
	value, err := s.Resolve(ctx, LoginFailureThreshold)
	if err != nil || value.Value != 9 || value.Source != "environment" || value.Editable {
		t.Fatalf("environment value=%+v err=%v", value, err)
	}
	if _, err := s.Set(ctx, LoginFailureThreshold, 8); err == nil {
		t.Fatal("environment-controlled setting should reject writes")
	}
	t.Setenv("LLAMARACK_LOGIN_FAILURE_THRESHOLD", "invalid")
	if _, err := s.Resolve(ctx, LoginFailureThreshold); err == nil {
		t.Fatal("invalid environment value should fail")
	}
}

func TestPreviousEnvPrefixDoesNotControlSettings(t *testing.T) {
	ctx := context.Background()
	s := testSettings(t)
	t.Setenv("LCM_ALLOWED_ORIGIN", "http://ignored.example:3000")
	t.Setenv("LCM_LOGIN_FAILURE_THRESHOLD", "9")
	value, err := s.Resolve(ctx, AllowedOrigins)
	if err != nil || value.Value != "http://localhost:3000" || value.Source != "default" {
		t.Fatalf("previous env prefix must be ignored for allowed origins: %+v err=%v", value, err)
	}
	value, err = s.Resolve(ctx, LoginFailureThreshold)
	if err != nil || value.Value != 5 || value.Source != "default" {
		t.Fatalf("previous env prefix must be ignored for login threshold: %+v err=%v", value, err)
	}
}

func TestIdleUnloadIgnoresLegacyEnvironmentVariable(t *testing.T) {
	ctx := context.Background()
	s := testSettings(t)
	t.Setenv("LCM_IDLE_UNLOAD_SECONDS", "900")

	value, err := s.Resolve(ctx, IdleUnloadSeconds)
	if err != nil || value.Value != 300 || value.Source != "default" || !value.Editable {
		t.Fatalf("legacy environment must not control idle unload: value=%+v err=%v", value, err)
	}
	if _, err := s.Set(ctx, IdleUnloadSeconds, 120); err != nil {
		t.Fatalf("idle unload should remain database-configurable: %v", err)
	}
	value, err = s.Resolve(ctx, IdleUnloadSeconds)
	if err != nil || value.Value != 120 || value.Source != "database" || !value.Editable {
		t.Fatalf("database idle unload=%+v err=%v", value, err)
	}
}

func TestSettingValidationAndTypeErrors(t *testing.T) {
	ctx := context.Background()
	s := testSettings(t)
	for _, tc := range []struct {
		key   string
		value any
	}{
		{SessionLifetimeSeconds, 1}, {LoginFailureThreshold, 1}, {LoginLockoutSeconds, 0}, {StartupTimeoutSeconds, 0}, {IdleUnloadSeconds, -1}, {AlwaysOnReconcileSeconds, -1}, {ObservabilityRetentionDays, 0}, {ObservabilityRetentionDays, 3651},
	} {
		if _, err := s.Set(ctx, tc.key, tc.value); err == nil {
			t.Fatalf("expected validation error for %s", tc.key)
		}
	}
	if _, err := s.Set(ctx, LoginProtectionEnabled, "not-bool"); err == nil {
		t.Fatal("expected bool validation error")
	}
	if _, err := s.Set(ctx, "missing", "x"); err == nil {
		t.Fatal("expected unknown setting error")
	}
	if _, err := s.Resolve(ctx, "missing"); err == nil {
		t.Fatal("expected unknown resolve error")
	}
	if _, err := s.Int(ctx, TrustedProxies); err == nil {
		t.Fatal("expected integer type error")
	}
	if _, err := s.String(ctx, SessionLifetimeSeconds); err == nil {
		t.Fatal("expected string type error")
	}
	if _, err := s.Bool(ctx, TrustedProxies); err == nil {
		t.Fatal("expected bool type error")
	}
}
