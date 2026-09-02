package auth

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/database"
)

func TestHiddenServiceAccountStaysHiddenWhileManagedKeyIsListed(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := database.Open(ctx, filepath.Join(root, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db, time.Hour)
	admin, err := s.Bootstrap(ctx, "admin", "password1234")
	if err != nil {
		t.Fatal(err)
	}

	account, err := s.EnsureHiddenServiceAccount(ctx, ManagedPrincipalName)
	if err != nil {
		t.Fatal(err)
	}
	key, secret, err := s.EnsureManagedInferenceKey(ctx, account.ID)
	if err != nil || secret == "" {
		t.Fatalf("managed key=%+v secret empty=%t err=%v", key, secret == "", err)
	}

	accounts, err := s.ListServiceAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range accounts {
		if item.ID == account.ID {
			t.Fatal("hidden service account appeared in list")
		}
	}
	keys, err := s.ListAPIKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range keys {
		if item.ID == key.ID {
			found = true
			if !item.Managed || item.Name != ManagedPrincipalName {
				t.Fatalf("listed managed key=%+v", item)
			}
		}
	}
	if !found {
		t.Fatal("managed key missing from API key list")
	}
	if _, err := s.GetServiceAccount(ctx, account.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("get hidden service account err=%v", err)
	}
	if err := s.UpdateServiceAccount(ctx, account.ID, strPtr("nope"), nil); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("patch hidden service account err=%v", err)
	}
	if err := s.DeleteServiceAccount(ctx, account.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("delete hidden service account err=%v", err)
	}
	if _, _, err := s.RotateAPIKey(ctx, key.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("rotate managed key err=%v", err)
	}
	if err := s.UpdateAPIKey(ctx, key.ID, UpdateAPIKeyInput{Name: strPtr("renamed")}); !errors.Is(err, ErrManagedAPIKeyImmutable) {
		t.Fatalf("patch managed key name err=%v", err)
	}
	saID := account.ID
	if err := s.UpdateAPIKey(ctx, key.ID, UpdateAPIKeyInput{OwnerServiceAccountID: &saID}); err != nil {
		t.Fatalf("same owner patch err=%v", err)
	}
	disabled := false
	if err := s.UpdateAPIKey(ctx, key.ID, UpdateAPIKeyInput{Enabled: &disabled}); err != nil {
		t.Fatalf("disable managed key err=%v", err)
	}
	if err := s.UpdateAPIKey(ctx, key.ID, UpdateAPIKeyInput{OwnerUserID: &admin.ID}); !errors.Is(err, ErrManagedAPIKeyImmutable) {
		t.Fatalf("reassign managed key err=%v", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO models(id,name,gguf_path,total_bytes,quantization,context_length) VALUES('m1','M','/tmp/m.gguf',1,'Q4',0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO instances(id,model_id,name) VALUES('coder','m1','Coder')`); err != nil {
		t.Fatal(err)
	}
	allowlist := []string{"coder"}
	if err := s.UpdateAPIKey(ctx, key.ID, UpdateAPIKeyInput{InstanceIDs: &allowlist}); err != nil {
		t.Fatalf("set managed key instances err=%v", err)
	}
	updated, err := s.ManagedInferenceKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.InstanceIDs) != 1 || updated.InstanceIDs[0] != "coder" {
		t.Fatalf("managed key instances=%v", updated.InstanceIDs)
	}
	if _, _, err := s.CreateAPIKey(ctx, CreateAPIKeyInput{Name: "hidden-owned", OwnerServiceAccountID: saID}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("create key for hidden owner err=%v", err)
	}
	rotated, newSecret, err := s.RotateManagedAPIKey(ctx, key.ID)
	if err != nil || newSecret == "" || rotated.ID != key.ID {
		t.Fatalf("internal rotate failed key=%+v err=%v", rotated, err)
	}
	_ = admin
}

func TestHiddenPrincipalHelpersAreIdempotent(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	account, err := s.EnsureHiddenServiceAccount(ctx, ManagedPrincipalName)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.EnsureHiddenServiceAccount(ctx, ManagedPrincipalName)
	if err != nil || again.ID != account.ID {
		t.Fatalf("second ensure=%+v err=%v", again, err)
	}
	key, secret, err := s.EnsureManagedInferenceKey(ctx, account.ID)
	if err != nil || secret == "" {
		t.Fatalf("first key=%+v err=%v", key, err)
	}
	existing, secretAgain, err := s.EnsureManagedInferenceKey(ctx, account.ID)
	if err != nil || secretAgain != "" || existing.ID != key.ID {
		t.Fatalf("existing key=%+v secret=%q err=%v", existing, secretAgain, err)
	}
}


func TestManagedInferenceKeyLookup(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	if _, err := s.ManagedInferenceKey(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected missing managed key, got err=%v", err)
	}
	account, err := s.EnsureHiddenServiceAccount(ctx, ManagedPrincipalName)
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := s.EnsureManagedInferenceKey(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	managed, err := s.ManagedInferenceKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if managed.ID != key.ID || managed.Name != ManagedPrincipalName {
		t.Fatalf("managed key=%+v want id=%s", managed, key.ID)
	}
}

func TestRotateManagedAPIKeyRejectsNonManagedKey(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	account, err := s.CreateServiceAccount(ctx, "visible", 0)
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := s.CreateAPIKey(ctx, CreateAPIKeyInput{Name: "visible-key", OwnerServiceAccountID: account.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RotateManagedAPIKey(ctx, key.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected hidden-only rotate rejection, got %v", err)
	}
}

func TestEnsureHiddenServiceAccountRejectsEmptyName(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	if _, err := s.EnsureHiddenServiceAccount(ctx, "  "); !errors.Is(err, ErrServiceAccountNameRequired) {
		t.Fatalf("expected name required, got %v", err)
	}
}

func TestDeleteHiddenServiceAccountByName(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	if _, err := s.EnsureHiddenServiceAccount(ctx, ManagedPrincipalName); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteHiddenServiceAccountByName(ctx, ManagedPrincipalName); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteHiddenServiceAccountByName(ctx, ManagedPrincipalName); err != nil {
		t.Fatal(err)
	}
	accounts, err := s.ListServiceAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 0 {
		t.Fatalf("expected no visible accounts, got %#v", accounts)
	}
}
