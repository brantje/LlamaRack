package litellm

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/downloads"
	"github.com/brantje/llamarack/backend/internal/huggingface"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/modelimports"
	"github.com/brantje/llamarack/backend/internal/settings"
)

func newLiteLLMTestEnv(t *testing.T) (*Service, *fakeLiteLLMServer, *auth.Service, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	db, err := database.Open(ctx, filepath.Join(root, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	authService := auth.New(db, time.Hour)
	if _, err := authService.Bootstrap(ctx, "admin", "password1234"); err != nil {
		t.Fatal(err)
	}
	secrets, err := huggingface.NewSecretStore(db, root)
	if err != nil {
		t.Fatal(err)
	}
	managerSettings := settings.New(db, settings.Defaults{})
	if _, err := managerSettings.Set(ctx, settings.ExternalURL, "http://llamarack.example"); err != nil {
		t.Fatal(err)
	}
	fake := newFakeLiteLLMServer(t)
	service := New(db, authService, secrets, managerSettings)
	service.http = fake.server.Client()
	if err := service.setSetting(ctx, SettingProxyURL, fake.server.URL); err != nil {
		t.Fatal(err)
	}
	if err := secrets.SetSecretWithPrefix(ctx, SecretProxyAPIKey, "sk-proxy-test-secret"); err != nil {
		t.Fatal(err)
	}
	return service, fake, authService, db
}

func insertEnabledInstance(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), `INSERT INTO models(id,name,gguf_path,total_bytes) VALUES(?,?,?,?)`, "model-1", "Model", "/tmp/model.gguf", 1); err != nil {
		t.Fatal(err)
	}
	store := instances.New(db)
	enabled := true
	instance, err := store.Create(context.Background(), instances.CreateInput{ModelID: "model-1", Name: id, Slug: id, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	return instance.ID
}

func TestServiceSaveReconcileAndStatus(t *testing.T) {
	service, fake, _, db := newLiteLLMTestEnv(t)
	insertEnabledInstance(t, db, "alpha")
	status, err := service.Save(context.Background(), SaveInput{ProxyURL: serviceMustURL(t, service), APIBase: "http://llamarack.example/v1", ProxyKey: "sk-proxy-test-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if !status.Configured || status.GeneratedKey.Name != auth.ManagedPrincipalName {
		t.Fatalf("status=%+v", status)
	}
	encoded := mustJSON(t, status)
	if strings.Contains(encoded, "sk-proxy-test-secret") {
		t.Fatal("status leaked proxy key material")
	}
	if status.LastSync == nil || !status.LastSyncOK {
		t.Fatalf("expected successful sync after save, status=%+v", status)
	}
	result, err := service.Reconcile(context.Background())
	if err != nil || !result.OK {
		t.Fatalf("reconcile=%+v err=%v", result, err)
	}
	fake.mu.Lock()
	modelCount := len(fake.models)
	fake.mu.Unlock()
	if modelCount != 1 {
		t.Fatalf("expected one published model, got %d", modelCount)
	}
}

func TestServiceRotateAndDisconnect(t *testing.T) {
	service, fake, authService, db := newLiteLLMTestEnv(t)
	insertEnabledInstance(t, db, "alpha")
	if _, err := service.Save(context.Background(), SaveInput{ProxyURL: serviceMustURL(t, service), APIBase: "http://llamarack.example/v1"}); err != nil {
		t.Fatal(err)
	}
	before, err := service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	after, err := service.Rotate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after.GeneratedKey.Prefix == before.GeneratedKey.Prefix {
		t.Fatal("expected prefix to change after rotate")
	}
	if err := service.Disconnect(context.Background(), DisconnectInput{Unpublish: true}); err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Configured {
		t.Fatalf("expected disconnected status, got %+v", status)
	}
	if remaining := len(fake.snapshotModels()); remaining != 0 {
		t.Fatalf("expected unpublish to clear fake models, got %d", remaining)
	}
	accounts, err := authService.ListServiceAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 0 {
		t.Fatalf("hidden account should not be listed, got %#v", accounts)
	}
}

func TestServiceBootReconcileWhenConfigured(t *testing.T) {
	service, _, _, db := newLiteLLMTestEnv(t)
	insertEnabledInstance(t, db, "boot-instance")
	if _, err := service.Save(context.Background(), SaveInput{ProxyURL: serviceMustURL(t, service), APIBase: "http://llamarack.example/v1"}); err != nil {
		t.Fatal(err)
	}
	service.BootReconcile(context.Background())
	status, err := service.Status(context.Background())
	if err != nil || status.LastSync == nil || !status.LastSyncOK {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}

func TestServiceReconcileUpdateDeleteAndStoreModelError(t *testing.T) {
	service, fake, _, db := newLiteLLMTestEnv(t)
	instanceID := insertEnabledInstance(t, db, "alpha")
	if _, err := service.Save(context.Background(), SaveInput{ProxyURL: serviceMustURL(t, service), APIBase: "http://llamarack.example/v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), `UPDATE instances SET enabled=0 WHERE id=?`, instanceID); err != nil {
		t.Fatal(err)
	}
	result, err := service.Reconcile(context.Background())
	if err != nil || !result.OK || result.Unpublished != 1 {
		t.Fatalf("reconcile=%+v err=%v", result, err)
	}
	fake.mu.Lock()
	count := len(fake.models)
	fake.mu.Unlock()
	if count != 0 {
		t.Fatalf("expected disabled instance to be unpublished, got %d models", count)
	}
}

func TestServiceTestConnection(t *testing.T) {
	service, _, _, _ := newLiteLLMTestEnv(t)
	if err := service.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestServiceSaveValidationErrors(t *testing.T) {
	service, _, _, _ := newLiteLLMTestEnv(t)
	if _, err := service.Save(context.Background(), SaveInput{ProxyURL: "   "}); err == nil {
		t.Fatal("expected proxy URL validation error")
	}
}

func TestServiceSetHTTPClient(t *testing.T) {
	service, _, _, _ := newLiteLLMTestEnv(t)
	if service.http == nil {
		t.Fatal("expected default http client")
	}
	client := &http.Client{}
	service.SetHTTPClient(client)
	if service.http != client {
		t.Fatal("expected injected http client")
	}
}

func TestLoadLastSyncInvalidJSON(t *testing.T) {
	service, _, _, _ := newLiteLLMTestEnv(t)
	if err := service.setSetting(context.Background(), SettingLastSync, "{not-json"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.loadLastSync(context.Background()); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestServiceReconcileUpdatesDriftedAPIBase(t *testing.T) {
	service, fake, _, db := newLiteLLMTestEnv(t)
	insertEnabledInstance(t, db, "alpha")
	if _, err := service.Save(context.Background(), SaveInput{ProxyURL: serviceMustURL(t, service), APIBase: "http://llamarack.example/v1"}); err != nil {
		t.Fatal(err)
	}
	if err := service.setSetting(context.Background(), SettingAPIBase, "http://updated.example/v1"); err != nil {
		t.Fatal(err)
	}
	result, err := service.Reconcile(context.Background())
	if err != nil || !result.OK || result.Published != 1 {
		t.Fatalf("reconcile=%+v err=%v", result, err)
	}
	fake.mu.Lock()
	entry := fake.models["litellm-alpha"]
	fake.mu.Unlock()
	if entry.LiteLLMParams.APIBase != "http://updated.example/v1" {
		t.Fatalf("expected updated api_base, got %#v", entry.LiteLLMParams)
	}
}

func TestServiceSaveKeepsExistingProxyKey(t *testing.T) {
	service, _, _, db := newLiteLLMTestEnv(t)
	insertEnabledInstance(t, db, "alpha")
	if _, err := service.Save(context.Background(), SaveInput{ProxyURL: serviceMustURL(t, service), APIBase: "http://llamarack.example/v1", ProxyKey: "proxy-key"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Save(context.Background(), SaveInput{ProxyURL: serviceMustURL(t, service), APIBase: "http://llamarack.example/v1"}); err != nil {
		t.Fatal(err)
	}
}

func TestServiceDisconnectWithoutUnpublish(t *testing.T) {
	service, fake, _, db := newLiteLLMTestEnv(t)
	insertEnabledInstance(t, db, "alpha")
	if _, err := service.Save(context.Background(), SaveInput{ProxyURL: serviceMustURL(t, service), APIBase: "http://llamarack.example/v1"}); err != nil {
		t.Fatal(err)
	}
	if err := service.Disconnect(context.Background(), DisconnectInput{Unpublish: false}); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	count := len(fake.models)
	fake.mu.Unlock()
	if count != 1 {
		t.Fatalf("expected models to remain when unpublish=false, got %d", count)
	}
}

func TestServiceReconcileRenamesInstanceSlugInPlace(t *testing.T) {
	service, fake, _, db := newLiteLLMTestEnv(t)
	instanceID := insertEnabledInstance(t, db, "alpha")
	if _, err := service.Save(context.Background(), SaveInput{ProxyURL: serviceMustURL(t, service), APIBase: "http://llamarack.example/v1"}); err != nil {
		t.Fatal(err)
	}
	store := instances.New(db)
	enabled := true
	if _, err := store.Update(context.Background(), instanceID, instances.UpdateInput{ModelID: "model-1", Name: "Beta", Slug: "beta", Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	result, err := service.Reconcile(context.Background())
	if err != nil || !result.OK || result.Published != 1 {
		t.Fatalf("reconcile=%+v err=%v", result, err)
	}
	fake.mu.Lock()
	entry, ok := fake.models["litellm-alpha"]
	fake.mu.Unlock()
	if !ok || entry.ModelName != "beta" || entry.LiteLLMParams.Model != "openai/beta" || entry.ModelInfo.LlamaRackInstanceID != instanceID {
		t.Fatalf("expected in-place slug rename, got %#v", fake.snapshotModels())
	}
}

func TestEnsurePrincipalRecoversMissingInferenceSecret(t *testing.T) {
	service, _, _, db := newLiteLLMTestEnv(t)
	insertEnabledInstance(t, db, "alpha")
	if _, err := service.Save(context.Background(), SaveInput{ProxyURL: serviceMustURL(t, service), APIBase: "http://llamarack.example/v1"}); err != nil {
		t.Fatal(err)
	}
	if err := service.secrets.DeleteSecret(context.Background(), SecretInferenceAPIKey); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	configured, err := service.secrets.SecretConfigured(context.Background(), SecretInferenceAPIKey)
	if err != nil || !configured {
		t.Fatalf("configured=%v err=%v", configured, err)
	}
}

func TestServiceReconcilePropagatesProxyErrors(t *testing.T) {
	service, fake, _, db := newLiteLLMTestEnv(t)
	insertEnabledInstance(t, db, "alpha")
	if _, err := service.Save(context.Background(), SaveInput{ProxyURL: serviceMustURL(t, service), APIBase: "http://llamarack.example/v1"}); err != nil {
		t.Fatal(err)
	}
	fake.server.Close()
	result, err := service.Reconcile(context.Background())
	if err == nil || result.OK {
		t.Fatalf("expected reconcile failure, result=%+v err=%v", result, err)
	}
}

func TestNotifyInstanceChangeDoesNotPanicWhenProxyDown(t *testing.T) {
	service, fake, _, db := newLiteLLMTestEnv(t)
	insertEnabledInstance(t, db, "alpha")
	if _, err := service.Save(context.Background(), SaveInput{ProxyURL: serviceMustURL(t, service), APIBase: "http://llamarack.example/v1"}); err != nil {
		t.Fatal(err)
	}
	fake.server.Close()
	service.NotifyInstanceChange(context.Background(), "alpha")
	time.Sleep(100 * time.Millisecond)
}

func TestServiceReconcileUnpublishesDisabledInstance(t *testing.T) {
	service, fake, _, db := newLiteLLMTestEnv(t)
	instanceID := insertEnabledInstance(t, db, "alpha")
	if _, err := service.Save(context.Background(), SaveInput{ProxyURL: serviceMustURL(t, service), APIBase: "http://llamarack.example/v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), "UPDATE instances SET enabled=0 WHERE id=?", instanceID); err != nil {
		t.Fatal(err)
	}
	result, err := service.Reconcile(context.Background())
	if err != nil || !result.OK || result.Unpublished != 1 {
		t.Fatalf("reconcile=%+v err=%v models=%#v", result, err, fake.snapshotModels())
	}
}

func TestServiceReconcileIgnoresUnmanagedModels(t *testing.T) {
	service, fake, _, db := newLiteLLMTestEnv(t)
	insertEnabledInstance(t, db, "alpha")
	if _, err := service.Save(context.Background(), SaveInput{ProxyURL: serviceMustURL(t, service), APIBase: "http://llamarack.example/v1"}); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	fake.models["foreign"] = ModelEntry{ModelName: "foreign", ModelInfo: ModelInfo{ID: "foreign", LlamaRackManaged: false}}
	fake.mu.Unlock()
	if result, err := service.Reconcile(context.Background()); err != nil || !result.OK {
		t.Fatalf("reconcile=%+v err=%v", result, err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if _, ok := fake.models["foreign"]; !ok {
		t.Fatal("unmanaged model should remain untouched")
	}
}

func TestServiceReconcileSkipsDownloadingImport(t *testing.T) {
	service, fake, _, db := newLiteLLMTestEnv(t)
	if _, err := db.ExecContext(context.Background(), `INSERT INTO models(id,name,gguf_path,total_bytes) VALUES(?,?,?,?)`, "model-1", "Model", "/tmp/model.gguf", 1); err != nil {
		t.Fatal(err)
	}
	store := instances.New(db)
	enabled := true
	instance, err := store.Create(context.Background(), instances.CreateInput{ModelID: "model-1", Name: "Downloading", Slug: "downloading", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,quantization,state,total_bytes,downloaded_bytes,speed_bps,error,created_at,updated_at)
VALUES('job-1','huggingface','acme/demo','rev','artifact','demo.gguf','Q4_K_M',?,0,0,0,'',unixepoch(),unixepoch())`, downloads.StateDownloading); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), `INSERT INTO provider_imports(id,job_id,model_id,instance_id,owns_model,start_when_ready,state,error,start_attempted,created_at,updated_at)
VALUES('import-1','job-1','model-1',?,0,0,?,'',0,unixepoch(),unixepoch())`, instance.ID, modelimports.StateDownloading); err != nil {
		t.Fatal(err)
	}
	if err := service.setSetting(context.Background(), SettingProxyURL, serviceMustURL(t, service)); err != nil {
		t.Fatal(err)
	}
	if err := service.secrets.SetSecretWithPrefix(context.Background(), SecretProxyAPIKey, "sk-proxy-test-secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fake.snapshotModels()) != 0 {
		t.Fatalf("downloading instance should not be published, got %#v", fake.snapshotModels())
	}
	if _, err := db.ExecContext(context.Background(), `UPDATE provider_imports SET state=?,updated_at=unixepoch() WHERE id=?`, modelimports.StateCompleted, "import-1"); err != nil {
		t.Fatal(err)
	}
	result, err := service.Reconcile(context.Background())
	if err != nil || !result.OK || result.Published != 1 {
		t.Fatalf("reconcile=%+v err=%v models=%#v", result, err, fake.snapshotModels())
	}
}

func serviceMustURL(t *testing.T, service *Service) string {
	t.Helper()
	url, err := service.getSetting(context.Background(), SettingProxyURL)
	if err != nil {
		t.Fatal(err)
	}
	return url
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
