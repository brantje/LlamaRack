package litellm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/huggingface"
	"github.com/brantje/llamarack/backend/internal/settings"
)

func TestNewClientValidation(t *testing.T) {
	if _, err := NewClient("", "key", nil); err == nil {
		t.Fatal("expected empty URL error")
	}
	if _, err := NewClient("ftp://example.com", "key", nil); err == nil {
		t.Fatal("expected scheme error")
	}
}

func TestParseAPIErrorVariants(t *testing.T) {
	if !strings.Contains(parseAPIError(500, []byte("")).Error(), "HTTP 500") {
		t.Fatal("expected status-only error")
	}
	if !strings.Contains(parseAPIError(502, []byte("upstream failed")).Error(), "upstream failed") {
		t.Fatal("expected body in error")
	}
}

func TestServiceSaveRequiresProxyKeyWhenMissing(t *testing.T) {
	service, _, _, db := newLiteLLMTestEnv(t)
	insertEnabledInstance(t, db, "alpha")
	if err := service.secrets.DeleteSecret(context.Background(), SecretProxyAPIKey); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Save(context.Background(), SaveInput{ProxyURL: serviceMustURL(t, service)}); err == nil {
		t.Fatal("expected missing proxy key error")
	}
}

func TestServiceSaveUsesDefaultAPIBase(t *testing.T) {
	service, _, _, db := newLiteLLMTestEnv(t)
	insertEnabledInstance(t, db, "alpha")
	status, err := service.Save(context.Background(), SaveInput{
		ProxyURL: serviceMustURL(t, service),
		ProxyKey: "sk-proxy-test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if status.APIBase != "http://llamarack.example/v1" {
		t.Fatalf("api_base=%q", status.APIBase)
	}
}

func TestServiceBootReconcileSkipsWhenUnconfigured(t *testing.T) {
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
	service := New(db, authService, secrets, managerSettings)
	service.BootReconcile(ctx)
	status, err := service.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.LastSync != nil {
		t.Fatalf("expected no sync without configuration, got %+v", status.LastSync)
	}
}

func TestServiceReconcileRenameInstanceInPlace(t *testing.T) {
	service, fake, _, db := newLiteLLMTestEnv(t)
	insertEnabledInstance(t, db, "alpha")
	if _, err := service.Save(context.Background(), SaveInput{
		ProxyURL: serviceMustURL(t, service),
		APIBase:  "http://llamarack.example/v1",
		ProxyKey: "sk-proxy-test-secret",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), `UPDATE instances SET id='beta', name='beta' WHERE id='alpha'`); err != nil {
		t.Fatal(err)
	}
	result, err := service.Reconcile(context.Background())
	if err != nil || !result.OK {
		t.Fatalf("reconcile=%+v err=%v", result, err)
	}
	models := fake.snapshotModels()
	if _, ok := models["litellm-beta"]; !ok {
		t.Fatalf("expected renamed model, got %#v", models)
	}
}

func TestClientDoJSONUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, "bad-key", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.TestConnection(context.Background()); err == nil {
		t.Fatal("expected unauthorized error")
	}
}

func TestEnsurePrincipalBackfillsMissingInferenceSecret(t *testing.T) {
	service, _, _, db := newLiteLLMTestEnv(t)
	insertEnabledInstance(t, db, "alpha")
	if _, err := service.Save(context.Background(), SaveInput{
		ProxyURL: serviceMustURL(t, service),
		APIBase:  "http://llamarack.example/v1",
		ProxyKey: "sk-proxy-test-secret",
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.secrets.DeleteSecret(context.Background(), SecretInferenceAPIKey); err != nil {
		t.Fatal(err)
	}
	result, err := service.Reconcile(context.Background())
	if err != nil || !result.OK {
		t.Fatalf("reconcile=%+v err=%v", result, err)
	}
}

func TestServiceRotateWhenConfigured(t *testing.T) {
	service, _, _, db := newLiteLLMTestEnv(t)
	insertEnabledInstance(t, db, "alpha")
	if _, err := service.Save(context.Background(), SaveInput{
		ProxyURL: serviceMustURL(t, service),
		APIBase:  "http://llamarack.example/v1",
		ProxyKey: "sk-proxy-test-secret",
	}); err != nil {
		t.Fatal(err)
	}
	status, err := service.Rotate(context.Background())
	if err != nil || !status.Configured {
		t.Fatalf("rotate status=%+v err=%v", status, err)
	}
}

func TestServiceTestWhenProxyKeyMissing(t *testing.T) {
	service, _, _, _ := newLiteLLMTestEnv(t)
	if err := service.secrets.DeleteSecret(context.Background(), SecretProxyAPIKey); err != nil {
		t.Fatal(err)
	}
	if err := service.Test(context.Background()); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("expected missing proxy key, got %v", err)
	}
}

func TestServiceSavePropagatesConnectionFailure(t *testing.T) {
	service, fake, _, db := newLiteLLMTestEnv(t)
	insertEnabledInstance(t, db, "alpha")
	proxyURL := serviceMustURL(t, service)
	fake.server.Close()
	_, err := service.Save(context.Background(), SaveInput{
		ProxyURL: proxyURL,
		APIBase:  "http://llamarack.example/v1",
		ProxyKey: "sk-proxy-test-secret",
	})
	if err == nil {
		t.Fatal("expected save to fail when proxy is down")
	}
	status, statusErr := service.Status(context.Background())
	if statusErr != nil || status.LastSync == nil || status.LastSyncOK {
		t.Fatalf("status=%+v err=%v", status, statusErr)
	}
}

func TestServiceReconcileUsesModelNameFallback(t *testing.T) {
	service, fake, _, db := newLiteLLMTestEnv(t)
	insertEnabledInstance(t, db, "alpha")
	if _, err := service.Save(context.Background(), SaveInput{
		ProxyURL: serviceMustURL(t, service),
		APIBase:  "http://llamarack.example/v1",
		ProxyKey: "sk-proxy-test-secret",
	}); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	for id, entry := range fake.models {
		entry.ModelInfo.LlamaRackInstanceID = ""
		fake.models[id] = entry
	}
	fake.mu.Unlock()
	if _, err := db.ExecContext(context.Background(), "UPDATE instances SET enabled=0 WHERE id=?", "alpha"); err != nil {
		t.Fatal(err)
	}
	result, err := service.Reconcile(context.Background())
	if err != nil || !result.OK || result.Unpublished != 1 {
		t.Fatalf("reconcile=%+v err=%v models=%#v", result, err, fake.snapshotModels())
	}
}

func TestNewClientRejectsMalformedURL(t *testing.T) {
	if _, err := NewClient("http://[::1", "key", nil); err == nil {
		t.Fatal("expected malformed URL error")
	}
}

func TestNewClientUsesDefaultHTTPClient(t *testing.T) {
	client, err := NewClient("https://litellm.example", "key", nil)
	if err != nil || client.http == nil {
		t.Fatalf("client=%+v err=%v", client, err)
	}
}

func TestSetHTTPClientIgnoresNil(t *testing.T) {
	service, _, _, _ := newLiteLLMTestEnv(t)
	original := service.http
	service.SetHTTPClient(nil)
	if service.http != original {
		t.Fatal("nil client should be ignored")
	}
}

func TestLoadLastSyncRejectsInvalidJSON(t *testing.T) {
	service, _, _, _ := newLiteLLMTestEnv(t)
	if err := service.setSetting(context.Background(), SettingLastSync, "{"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.loadLastSync(context.Background()); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestEffectiveAPIBaseFallsBackToDefault(t *testing.T) {
	service, _, _, _ := newLiteLLMTestEnv(t)
	if err := service.deleteSetting(context.Background(), SettingAPIBase); err != nil {
		t.Fatal(err)
	}
	got, err := service.effectiveAPIBase(context.Background())
	if err != nil || got != "http://llamarack.example/v1" {
		t.Fatalf("api_base=%q err=%v", got, err)
	}
}

func TestEntryDriftedDetectsAPIKeyChange(t *testing.T) {
	entry := BuildModelEntry("alpha", "http://base/v1", "old-key", "id-1")
	if !entryDrifted(entry, "alpha", "http://base/v1", "new-key") {
		t.Fatal("expected api_key drift")
	}
	if entryDrifted(entry, "alpha", "http://base/v1", "old-key") {
		t.Fatal("expected matching entry")
	}
}

func TestUnpublishAllFailsWhenProxyDown(t *testing.T) {
	service, fake, _, db := newLiteLLMTestEnv(t)
	insertEnabledInstance(t, db, "alpha")
	if _, err := service.Save(context.Background(), SaveInput{
		ProxyURL: serviceMustURL(t, service),
		APIBase:  "http://llamarack.example/v1",
		ProxyKey: "sk-proxy-test-secret",
	}); err != nil {
		t.Fatal(err)
	}
	fake.server.Close()
	if _, err := service.unpublishAll(context.Background()); err == nil {
		t.Fatal("expected unpublish failure")
	}
}

func TestDefaultAPIBaseEmptyWithoutExternalURL(t *testing.T) {
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
	service := New(db, authService, secrets, settings.New(db, settings.Defaults{}))
	got, err := service.defaultAPIBase(ctx)
	if err != nil || got != "" {
		t.Fatalf("default api base=%q err=%v", got, err)
	}
}

func TestInferenceSecretRequiresConfiguredCopy(t *testing.T) {
	service, _, _, db := newLiteLLMTestEnv(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := service.inferenceSecret(context.Background()); err == nil {
		t.Fatal("expected inference secret failure when database is closed")
	}
}
func TestRotateRequiresManagedKey(t *testing.T) {
	service, _, _, _ := newLiteLLMTestEnv(t)
	if _, err := service.Rotate(context.Background()); err == nil {
		t.Fatal("expected rotate without managed key to fail")
	}
}

func TestRotatePropagatesReconcileFailure(t *testing.T) {
	service, fake, _, db := newLiteLLMTestEnv(t)
	insertEnabledInstance(t, db, "alpha")
	if _, err := service.Save(context.Background(), SaveInput{
		ProxyURL: serviceMustURL(t, service),
		APIBase:  "http://llamarack.example/v1",
		ProxyKey: "sk-proxy-test-secret",
	}); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	fake.failList = true
	fake.mu.Unlock()
	if _, err := service.Rotate(context.Background()); err == nil {
		t.Fatal("expected rotate to fail when catalog list fails")
	}
}

func TestReconcileCreateUpdateDeleteFailures(t *testing.T) {
	service, fake, _, db := newLiteLLMTestEnv(t)
	insertEnabledInstance(t, db, "alpha")
	if _, err := service.Save(context.Background(), SaveInput{
		ProxyURL: serviceMustURL(t, service),
		APIBase:  "http://llamarack.example/v1",
		ProxyKey: "sk-proxy-test-secret",
	}); err != nil {
		t.Fatal(err)
	}

	fake.mu.Lock()
	fake.models = map[string]ModelEntry{}
	fake.failCreate = true
	fake.mu.Unlock()
	if _, err := service.Reconcile(context.Background()); err == nil {
		t.Fatal("expected create failure")
	}

	fake.mu.Lock()
	fake.failCreate = false
	fake.failUpdate = true
	fake.models["litellm-alpha"] = BuildModelEntry("alpha", "http://stale.example/v1", "old-key", "litellm-alpha")
	fake.mu.Unlock()
	if _, err := service.Reconcile(context.Background()); err == nil {
		t.Fatal("expected update failure")
	}

	fake.mu.Lock()
	fake.failUpdate = false
	fake.failDelete = true
	fake.mu.Unlock()
	if _, err := db.ExecContext(context.Background(), "UPDATE instances SET enabled=0 WHERE id=?", "alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reconcile(context.Background()); err == nil {
		t.Fatal("expected delete failure")
	}
}

func TestUnpublishAllSkipsUnmanagedAndEmptyIDs(t *testing.T) {
	service, fake, _, db := newLiteLLMTestEnv(t)
	insertEnabledInstance(t, db, "alpha")
	if _, err := service.Save(context.Background(), SaveInput{
		ProxyURL: serviceMustURL(t, service),
		APIBase:  "http://llamarack.example/v1",
		ProxyKey: "sk-proxy-test-secret",
	}); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	fake.models["external"] = ModelEntry{ModelName: "external", ModelInfo: ModelInfo{ID: "external", LlamaRackManaged: false}}
	fake.models["orphan"] = ModelEntry{ModelName: "orphan", ModelInfo: ModelInfo{LlamaRackManaged: true}}
	fake.mu.Unlock()
	count, err := service.unpublishAll(context.Background())
	if err != nil || count != 1 {
		t.Fatalf("unpublish count=%d err=%v models=%#v", count, err, fake.snapshotModels())
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if _, ok := fake.models["external"]; !ok {
		t.Fatal("expected unmanaged model to remain")
	}
	if _, ok := fake.models["orphan"]; !ok {
		t.Fatal("expected empty-id managed model to remain")
	}
}

func TestUnpublishAllDeleteFailure(t *testing.T) {
	service, fake, _, db := newLiteLLMTestEnv(t)
	insertEnabledInstance(t, db, "alpha")
	if _, err := service.Save(context.Background(), SaveInput{
		ProxyURL: serviceMustURL(t, service),
		APIBase:  "http://llamarack.example/v1",
		ProxyKey: "sk-proxy-test-secret",
	}); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	fake.failDelete = true
	fake.mu.Unlock()
	if _, err := service.unpublishAll(context.Background()); err == nil {
		t.Fatal("expected delete failure")
	}
}

func TestNewClientRequiresProxyKey(t *testing.T) {
	service, _, _, _ := newLiteLLMTestEnv(t)
	if err := service.secrets.DeleteSecret(context.Background(), SecretProxyAPIKey); err != nil {
		t.Fatal(err)
	}
	if _, err := service.newClient(context.Background()); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("expected missing proxy key, got %v", err)
	}
}

func TestBootReconcileWarnsWhenProxyDown(t *testing.T) {
	service, fake, _, db := newLiteLLMTestEnv(t)
	insertEnabledInstance(t, db, "alpha")
	if _, err := service.Save(context.Background(), SaveInput{
		ProxyURL: serviceMustURL(t, service),
		APIBase:  "http://llamarack.example/v1",
		ProxyKey: "sk-proxy-test-secret",
	}); err != nil {
		t.Fatal(err)
	}
	fake.server.Close()
	service.BootReconcile(context.Background())
}

func TestDisconnectWithoutUnpublish(t *testing.T) {
	service, _, _, db := newLiteLLMTestEnv(t)
	insertEnabledInstance(t, db, "alpha")
	if _, err := service.Save(context.Background(), SaveInput{
		ProxyURL: serviceMustURL(t, service),
		APIBase:  "http://llamarack.example/v1",
		ProxyKey: "sk-proxy-test-secret",
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.Disconnect(context.Background(), DisconnectInput{}); err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(context.Background())
	if err != nil || status.Configured {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}

func TestSetHTTPClientReplacesClient(t *testing.T) {
	service, _, _, _ := newLiteLLMTestEnv(t)
	next := &http.Client{Timeout: time.Second}
	service.SetHTTPClient(next)
	if service.http != next {
		t.Fatal("expected http client replacement")
	}
}

func TestClientListModelsInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"data":`))
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, "key", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListModels(context.Background()); err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestClientOmitsAuthorizationWhenKeyEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			http.Error(w, "unexpected auth", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, "  ", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.TestConnection(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestEntryDriftedDetectsModelString(t *testing.T) {
	entry := BuildModelEntry("alpha", "http://base/v1", "key", "id-1")
	entry.LiteLLMParams.Model = "openai/other"
	if !entryDrifted(entry, "alpha", "http://base/v1", "key") {
		t.Fatal("expected model string drift")
	}
}
