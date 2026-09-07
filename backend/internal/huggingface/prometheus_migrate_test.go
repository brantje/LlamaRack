package huggingface

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/database"
)

func TestMigrateManagerSettingSecretMovesPlaintextAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := database.Open(ctx, filepath.Join(root, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewSecretStore(db, root)
	if err != nil {
		t.Fatal(err)
	}

	const token = "dashboard-metrics-token"
	if _, err := db.ExecContext(ctx, `INSERT INTO manager_settings(setting_key,setting_value,updated_at) VALUES(?,?,?)`, SecretPrometheusAuthToken, token, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateManagerSettingSecret(ctx, SecretPrometheusAuthToken, SecretPrometheusAuthToken); err != nil {
		t.Fatal(err)
	}
	assertNoPlaintextPrometheusSetting(t, db, token)
	got, err := store.GetSecret(ctx, SecretPrometheusAuthToken)
	if err != nil || got != token {
		t.Fatalf("migrated secret=%q err=%v", got, err)
	}
	status, err := store.SecretStatus(ctx, SecretPrometheusAuthToken)
	if err != nil || !status.Configured || status.Prefix != "dashboar" {
		t.Fatalf("migrated status=%+v err=%v", status, err)
	}

	if err := store.MigrateManagerSettingSecret(ctx, SecretPrometheusAuthToken, SecretPrometheusAuthToken); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetSecret(ctx, SecretPrometheusAuthToken)
	if err != nil || got != token {
		t.Fatalf("idempotent secret=%q err=%v", got, err)
	}
	assertNoPlaintextPrometheusSetting(t, db, token)
}

func TestMigrateManagerSettingSecretDropsEmptyPlaintextWithoutCreatingSecret(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := database.Open(ctx, filepath.Join(root, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewSecretStore(db, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO manager_settings(setting_key,setting_value,updated_at) VALUES(?,?,?)`, SecretPrometheusAuthToken, "   ", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateManagerSettingSecret(ctx, SecretPrometheusAuthToken, SecretPrometheusAuthToken); err != nil {
		t.Fatal(err)
	}
	assertNoPlaintextPrometheusSetting(t, db, "   ")
	configured, err := store.SecretConfigured(ctx, SecretPrometheusAuthToken)
	if err != nil || configured {
		t.Fatalf("configured=%v err=%v", configured, err)
	}
}

func TestMigrateManagerSettingSecretDoesNotOverwriteExistingSecret(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := database.Open(ctx, filepath.Join(root, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewSecretStore(db, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSecretWithPrefix(ctx, SecretPrometheusAuthToken, "encrypted-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO manager_settings(setting_key,setting_value,updated_at) VALUES(?,?,?)`, SecretPrometheusAuthToken, "plaintext-token", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateManagerSettingSecret(ctx, SecretPrometheusAuthToken, SecretPrometheusAuthToken); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSecret(ctx, SecretPrometheusAuthToken)
	if err != nil || got != "encrypted-token" {
		t.Fatalf("secret=%q err=%v", got, err)
	}
	assertNoPlaintextPrometheusSetting(t, db, "plaintext-token")
}

func TestMigrateManagerSettingSecretValidationAndClosedDB(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := database.Open(ctx, filepath.Join(root, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewSecretStore(db, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateManagerSettingSecret(ctx, "", SecretPrometheusAuthToken); err == nil {
		t.Fatal("expected empty name error")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateManagerSettingSecret(ctx, SecretPrometheusAuthToken, SecretPrometheusAuthToken); err == nil {
		t.Fatal("expected closed db error")
	}
}

func assertNoPlaintextPrometheusSetting(t *testing.T, db *sql.DB, token string) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM manager_settings WHERE setting_key=?`, SecretPrometheusAuthToken).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("plaintext prometheus setting still present: count=%d", count)
	}
	if strings.TrimSpace(token) == "" {
		return
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM manager_settings WHERE instr(setting_value, ?) > 0`, token).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("raw token still present in manager_settings: count=%d", count)
	}
}
