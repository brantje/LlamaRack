package litellm

import (
	"context"
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
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if _, ok := fake.models["litellm-beta"]; !ok {
		t.Fatalf("expected renamed model, got %#v", fake.models)
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
		t.Fatalf("reconcile=%+v err=%v models=%#v", result, err, fake.models)
	}
}

func TestNewClientRejectsMalformedURL(t *testing.T) {
	if _, err := NewClient("http://[::1", "key", nil); err == nil {
		t.Fatal("expected malformed URL error")
	}
}
