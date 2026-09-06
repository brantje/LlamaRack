package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAPIKeyTypedAuthAndOwnerLifecycle(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	admin, err := s.Bootstrap(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	operator, err := s.CreateUser(ctx, "operator", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.CreateAPIKey(ctx, CreateAPIKeyInput{Name: "both", OwnerUserID: &admin.ID, OwnerServiceAccountID: "sa"}); !errors.Is(err, ErrAPIKeyOwnerRequired) {
		t.Fatalf("both owners=%v", err)
	}
	if _, _, err := s.CreateAPIKey(ctx, CreateAPIKeyInput{Name: "typed", KeyType: "other", OwnerUserID: &admin.ID}); !errors.Is(err, ErrAPIKeyTypeInvalid) {
		t.Fatalf("bad type=%v", err)
	}
	if _, _, err := s.CreateAPIKey(ctx, CreateAPIKeyInput{Name: "instances", KeyType: APIKeyTypeManagement, OwnerUserID: &admin.ID, InstanceIDs: []string{"coder"}}); !errors.Is(err, ErrAPIKeyInstancesNotAllowed) {
		t.Fatalf("management instances=%v", err)
	}

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format(time.DateOnly)
	if _, _, err := s.CreateAPIKey(ctx, CreateAPIKeyInput{Name: "past", OwnerUserID: &admin.ID, ExpiresOn: yesterday}); !errors.Is(err, ErrAPIKeyExpiresOnPast) {
		t.Fatalf("past expiry=%v", err)
	}
	if _, _, err := s.CreateAPIKey(ctx, CreateAPIKeyInput{Name: "bad-date", OwnerUserID: &admin.ID, ExpiresOn: "02-09-2026"}); !errors.Is(err, ErrAPIKeyExpiresOnInvalid) {
		t.Fatalf("invalid date=%v", err)
	}

	today := time.Now().UTC().Format(time.DateOnly)
	key, secret, err := s.CreateAPIKey(ctx, CreateAPIKeyInput{
		Name: "today", KeyType: APIKeyTypeFull, OwnerUserID: &admin.ID, ExpiresOn: today, CreatedByUserID: &admin.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if key.KeyType != APIKeyTypeFull || key.ExpiresOn == nil || *key.ExpiresOn != today {
		t.Fatalf("unexpected key %+v", key)
	}
	if err := s.AuthenticateAPIKey(ctx, secret); err != nil {
		t.Fatalf("valid through UTC EOD: %v", err)
	}

	if _, err := s.db.ExecContext(ctx, "UPDATE api_keys SET expires_on=? WHERE id=?", yesterday, key.ID); err != nil {
		t.Fatal(err)
	}
	// Raw SQL bypasses the service mutation hooks that normally invalidate the
	// token-hash cache, so this storage-level fixture must reset it explicitly.
	s.clearAPIKeyCache()
	if err := s.AuthenticateAPIKey(ctx, secret); !errors.Is(err, ErrAPIKeyInvalid) {
		t.Fatalf("expired key=%v", err)
	}
	listed, err := s.ListAPIKeys(ctx)
	if err != nil || len(listed) != 1 || listed[0].Status != APIKeyStatusExpired {
		t.Fatalf("expired status=%+v err=%v", listed, err)
	}

	if err := s.SetUserEnabled(ctx, operator.ID, false); err != nil {
		t.Fatal(err)
	}
	owned, ownedSecret, err := s.CreateAPIKeyForUser(ctx, "disabled-owner", operator.ID)
	if !errors.Is(err, ErrAPIKeyOwnerDisabled) {
		t.Fatalf("create for disabled owner=%v key=%+v secret=%q", err, owned, ownedSecret)
	}
	if err := s.SetUserEnabled(ctx, operator.ID, true); err != nil {
		t.Fatal(err)
	}
	owned, ownedSecret, err = s.CreateAPIKeyForUser(ctx, "operator-key", operator.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserEnabled(ctx, operator.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthenticateAPIKey(ctx, ownedSecret); !errors.Is(err, ErrAPIKeyInvalid) {
		t.Fatalf("owner disabled auth=%v", err)
	}
	keys, err := s.ListAPIKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundOwnerDisabled := false
	for _, item := range keys {
		if item.ID == owned.ID && item.Status == APIKeyStatusOwnerDisabled {
			foundOwnerDisabled = true
		}
	}
	if !foundOwnerDisabled {
		t.Fatalf("expected owner_disabled status in %+v", keys)
	}

	if err := s.DeleteUser(ctx, admin.ID, operator.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM api_keys WHERE id=?", owned.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("owner delete should cascade keys")
	}
}

func TestServiceAccountCRUDAndOwnedKeys(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	admin, err := s.Bootstrap(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateServiceAccount(ctx, "   ", admin.ID); !errors.Is(err, ErrServiceAccountNameRequired) {
		t.Fatalf("blank SA name=%v", err)
	}
	account, err := s.CreateServiceAccount(ctx, "automation", admin.ID)
	if err != nil || account.ID == "" || !account.Enabled {
		t.Fatalf("create SA=%+v err=%v", account, err)
	}
	key, secret, err := s.CreateAPIKey(ctx, CreateAPIKeyInput{
		Name: "sa-mgmt", KeyType: APIKeyTypeManagement, OwnerServiceAccountID: account.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if key.OwnerKind != OwnerKindServiceAccount || key.OwnerID != account.ID || key.CreatedByUserID != nil {
		t.Fatalf("unexpected SA key %+v", key)
	}
	if err := s.AuthenticateAPIKey(ctx, secret); err != nil {
		t.Fatal(err)
	}

	if err := s.UpdateServiceAccount(ctx, account.ID, nil, boolPtr(false)); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthenticateAPIKey(ctx, secret); !errors.Is(err, ErrAPIKeyInvalid) {
		t.Fatalf("disabled SA owner=%v", err)
	}
	if err := s.UpdateServiceAccount(ctx, account.ID, strPtr("bots"), boolPtr(true)); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetServiceAccount(ctx, account.ID)
	if err != nil || got.Name != "bots" || len(got.Keys) != 1 || got.Keys[0].ID != key.ID {
		t.Fatalf("get SA=%+v err=%v", got, err)
	}

	if err := s.DeleteServiceAccount(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetServiceAccount(ctx, account.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted SA lookup=%v", err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM api_keys WHERE id=?", key.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("SA delete should cascade keys")
	}
}

func TestAPIKeyUnknownInstanceAndPrefix(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	admin, err := s.Bootstrap(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	operator, err := s.CreateUser(ctx, "operator", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateAPIKey(ctx, CreateAPIKeyInput{Name: "allow", OwnerUserID: &admin.ID, InstanceIDs: []string{"missing"}}); !errors.Is(err, ErrUnknownInstanceID) {
		t.Fatalf("unknown instance=%v", err)
	}
	key, secret, err := s.CreateAPIKeyForUser(ctx, "plain", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, "sk-") || !strings.HasPrefix(key.Prefix, "sk-") || len(key.Prefix) != len("sk-")+8 {
		t.Fatalf("secret prefix contract secret=%q prefix=%q", secret, key.Prefix)
	}
	if err := s.AuthenticateAPIKey(ctx, strings.TrimPrefix(secret, "sk-")); !errors.Is(err, ErrAPIKeyInvalid) {
		t.Fatalf("non-sk secret=%v", err)
	}

	if _, err := s.db.ExecContext(ctx, `INSERT INTO models(id,name,gguf_path,total_bytes,quantization,context_length) VALUES('m1','M','/tmp/m.gguf',1,'Q4',0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO instances(id,model_id,name) VALUES('coder','m1','Coder')`); err != nil {
		t.Fatal(err)
	}
	scoped, _, err := s.CreateAPIKey(ctx, CreateAPIKeyInput{Name: "scoped", OwnerUserID: &operator.ID, InstanceIDs: []string{"coder"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM instances WHERE id='coder'`); err != nil {
		t.Fatal(err)
	}
	listed, err := s.ListAPIKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundMissing := false
	for _, item := range listed {
		if item.ID == scoped.ID && len(item.MissingInstanceIDs) == 1 && item.MissingInstanceIDs[0] == "coder" {
			foundMissing = true
		}
	}
	if !foundMissing {
		t.Fatalf("expected missing_instance_ids in %+v", listed)
	}
	if err := s.UpdateAPIKey(ctx, scoped.ID, UpdateAPIKeyInput{Name: strPtr("scoped-renamed")}); err != nil {
		t.Fatalf("rename with stale instance ids=%v", err)
	}
	if err := s.UpdateAPIKey(ctx, scoped.ID, UpdateAPIKeyInput{InstanceIDs: &[]string{"coder"}}); !errors.Is(err, ErrUnknownInstanceID) {
		t.Fatalf("re-supplying deleted instance=%v", err)
	}
	if err := s.SetUserEnabled(ctx, operator.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAPIKey(ctx, scoped.ID, UpdateAPIKeyInput{Enabled: boolPtr(false)}); err != nil {
		t.Fatalf("disable key with disabled owner=%v", err)
	}
	if err := s.UpdateAPIKey(ctx, scoped.ID, UpdateAPIKeyInput{OwnerUserID: &operator.ID}); !errors.Is(err, ErrAPIKeyOwnerDisabled) {
		t.Fatalf("reassign to disabled owner=%v", err)
	}
}

func TestAPIKeyStatusPriorityAndServiceAccountEdges(t *testing.T) {
	if got := computeAPIKeyStatus(false, false, strPtr("1999-01-01"), time.Now().UTC()); got != APIKeyStatusDisabled {
		t.Fatalf("disabled should win: %s", got)
	}
	if got := computeAPIKeyStatus(true, false, strPtr("1999-01-01"), time.Now().UTC()); got != APIKeyStatusOwnerDisabled {
		t.Fatalf("owner disabled should beat expiry: %s", got)
	}
	if got := computeAPIKeyStatus(true, true, strPtr("1999-01-01"), time.Now().UTC()); got != APIKeyStatusExpired {
		t.Fatalf("expired=%s", got)
	}
	if got := computeAPIKeyStatus(true, true, nil, time.Now().UTC()); got != APIKeyStatusEnabled {
		t.Fatalf("enabled=%s", got)
	}

	ctx := context.Background()
	s := testService(t)
	admin, err := s.Bootstrap(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetServiceAccount(ctx, ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("empty SA get=%v", err)
	}
	if err := s.UpdateServiceAccount(ctx, "missing", nil, nil); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing SA update=%v", err)
	}
	if err := s.DeleteServiceAccount(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing SA delete=%v", err)
	}
	account, err := s.CreateServiceAccount(ctx, "bots", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateServiceAccount(ctx, account.ID, strPtr("  "), nil); !errors.Is(err, ErrServiceAccountNameRequired) {
		t.Fatalf("blank rename=%v", err)
	}
	items, err := s.ListServiceAccounts(ctx)
	if err != nil || len(items) != 1 || items[0].ID != account.ID || len(items[0].Keys) != 0 {
		t.Fatalf("list SA=%+v err=%v", items, err)
	}
	key, _, err := s.CreateAPIKey(ctx, CreateAPIKeyInput{Name: "no-creator", OwnerUserID: &admin.ID})
	if err != nil {
		t.Fatal(err)
	}
	if key.CreatedByUserID != nil {
		t.Fatalf("non-JWT create should omit creator: %+v", key)
	}
	if err := s.UpdateAPIKey(ctx, key.ID, UpdateAPIKeyInput{ClearExpiresOn: true}); err != nil {
		t.Fatal(err)
	}

	if err := s.UpdateServiceAccount(ctx, "", nil, nil); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("empty SA update=%v", err)
	}
	if err := s.DeleteServiceAccount(ctx, ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("empty SA delete=%v", err)
	}
	if _, err := s.ListAPIKeysForServiceAccount(ctx, ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("empty SA key list=%v", err)
	}
	if err := s.SetAPIKeyEnabled(ctx, "missing", true); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing enable=%v", err)
	}

	saKey, _, err := s.CreateAPIKey(ctx, CreateAPIKeyInput{Name: "owned", KeyType: APIKeyTypeManagement, OwnerServiceAccountID: account.ID})
	if err != nil {
		t.Fatal(err)
	}
	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format(time.DateOnly)
	if err := s.UpdateAPIKey(ctx, saKey.ID, UpdateAPIKeyInput{Name: strPtr("owned-renamed"), OwnerUserID: &admin.ID, ExpiresOn: &tomorrow, Enabled: boolPtr(true)}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAPIKey(ctx, saKey.ID, UpdateAPIKeyInput{OwnerServiceAccountID: &account.ID, InstanceIDs: &[]string{}}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateServiceAccount(ctx, account.ID, nil, boolPtr(false)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateAPIKey(ctx, CreateAPIKeyInput{Name: "disabled-sa", OwnerServiceAccountID: account.ID}); !errors.Is(err, ErrAPIKeyOwnerDisabled) {
		t.Fatalf("disabled SA owner create=%v", err)
	}
	if _, _, err := s.CreateAPIKey(ctx, CreateAPIKeyInput{Name: "missing-sa", OwnerServiceAccountID: "missing"}); !errors.Is(err, ErrAPIKeyOwnerNotFound) {
		t.Fatalf("missing SA owner create=%v", err)
	}
}

func boolPtr(value bool) *bool    { return &value }
func strPtr(value string) *string { return &value }
