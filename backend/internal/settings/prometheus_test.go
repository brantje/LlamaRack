package settings

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/huggingface"
)

func testPrometheusStore(t *testing.T) (*sql.DB, *huggingface.SecretStore) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	db, err := database.Open(ctx, filepath.Join(root, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := huggingface.NewSecretStore(db, root)
	if err != nil {
		t.Fatal(err)
	}
	return db, store
}

func TestPrometheusTokenStatusAndResolvePreferEncryptedSecret(t *testing.T) {
	ctx := context.Background()
	db, store := testPrometheusStore(t)
	t.Setenv(prometheusAuthTokenEnv, "environment-token")

	status, err := PrometheusTokenStatus(ctx, store)
	if err != nil || !status.Configured || status.Source != "environment" || status.Prefix != "environm" {
		t.Fatalf("env status=%+v err=%v", status, err)
	}
	if got, err := ResolvePrometheusToken(ctx, store); err != nil || got != "environment-token" {
		t.Fatalf("env resolve=%q err=%v", got, err)
	}

	if err := SetPrometheusToken(ctx, store, "database-token"); err != nil {
		t.Fatal(err)
	}
	status, err = PrometheusTokenStatus(ctx, store)
	if err != nil || !status.Configured || status.Source != "database" || status.Prefix != "database" {
		t.Fatalf("database status=%+v err=%v", status, err)
	}
	if got, err := ResolvePrometheusToken(ctx, store); err != nil || got != "database-token" {
		t.Fatalf("database resolve=%q err=%v", got, err)
	}
	assertManagerSettingsHasNoToken(t, db, "database-token")

	if err := SetPrometheusToken(ctx, store, " replacement-token "); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolvePrometheusToken(ctx, store); err != nil || got != "replacement-token" {
		t.Fatalf("replaced resolve=%q err=%v", got, err)
	}

	if err := SetPrometheusToken(ctx, store, ""); err != nil {
		t.Fatal(err)
	}
	status, err = PrometheusTokenStatus(ctx, store)
	if err != nil || !status.Configured || status.Source != "environment" {
		t.Fatalf("cleared falls back to env: %+v err=%v", status, err)
	}
	if got, err := ResolvePrometheusToken(ctx, store); err != nil || got != "environment-token" {
		t.Fatalf("cleared resolve=%q err=%v", got, err)
	}
}

func TestPrometheusTokenDefaultAndUnavailableStore(t *testing.T) {
	ctx := context.Background()
	status, err := PrometheusTokenStatus(ctx, nil)
	if err != nil || status.Configured || status.Source != "default" || !status.Editable {
		t.Fatalf("default status=%+v err=%v", status, err)
	}
	if got, err := ResolvePrometheusToken(ctx, nil); err != nil || got != "" {
		t.Fatalf("default resolve=%q err=%v", got, err)
	}
	if err := SetPrometheusToken(ctx, nil, "secret"); err == nil {
		t.Fatal("expected unavailable store error")
	}
}

func TestPrometheusTokenSetDoesNotPersistPlaintextSetting(t *testing.T) {
	ctx := context.Background()
	db, store := testPrometheusStore(t)
	const token = "inspect-me-token"
	if err := SetPrometheusToken(ctx, store, token); err != nil {
		t.Fatal(err)
	}
	assertManagerSettingsHasNoToken(t, db, token)
	var ciphertext []byte
	if err := db.QueryRowContext(ctx, `SELECT ciphertext FROM provider_secrets WHERE name=?`, huggingface.SecretPrometheusAuthToken).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ciphertext), token) {
		t.Fatal("ciphertext contained plaintext token")
	}
}

func TestPrometheusTokenStatusClosedStore(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := database.Open(ctx, filepath.Join(root, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := huggingface.NewSecretStore(db, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := PrometheusTokenStatus(ctx, store); err == nil {
		t.Fatal("expected closed db status error")
	}
	if _, err := ResolvePrometheusToken(ctx, store); err == nil {
		t.Fatal("expected closed db resolve error")
	}
}

func assertManagerSettingsHasNoToken(t *testing.T, db *sql.DB, token string) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM manager_settings WHERE setting_key=?`, PrometheusAuthToken).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("prometheus still stored in manager_settings: count=%d", count)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM manager_settings WHERE instr(setting_value, ?) > 0`, token).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("raw token present in manager_settings: count=%d", count)
	}
}
