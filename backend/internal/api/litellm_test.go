package api

import (
	"bytes"
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
	"github.com/brantje/llamarack/backend/internal/litellm"
	"github.com/brantje/llamarack/backend/internal/settings"
)

type liteLLMFixture struct {
	handler http.Handler
	cookie  *http.Cookie
	service *litellm.Service
	fake    *litellmFakeServer
}

type litellmFakeServer struct {
	*httptest.Server
}

func newLiteLLMFixture(t *testing.T) liteLLMFixture {
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
	token, _, _, err := authService.LoginWithMetadata(ctx, "admin", "password1234", "127.0.0.1", "litellm-test")
	if err != nil {
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
	fake := newLiteLLMAPITestServer(t)
	service := litellm.New(db, authService, secrets, managerSettings)
	service.SetHTTPClient(fake.Client())
	if err := secrets.SetSecretWithPrefix(ctx, litellm.SecretProxyAPIKey, "proxy-key"); err != nil {
		t.Fatal(err)
	}
	return liteLLMFixture{
		handler: NewLiteLLMHandler(authService, service),
		cookie:  &http.Cookie{Name: sessionCookie, Value: token},
		service: service,
		fake:    fake,
	}
}

func newLiteLLMAPITestServer(t *testing.T) *litellmFakeServer {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer proxy-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/model/info" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	return &litellmFakeServer{Server: server}
}

func liteLLMRequest(t *testing.T, fixture liteLLMFixture, method, path string, body any, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, reader)
	if authenticated {
		req.AddCookie(fixture.cookie)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	fixture.handler.ServeHTTP(w, req)
	return w
}

func TestLiteLLMRequiresAuthenticationAndReturnsStatus(t *testing.T) {
	fixture := newLiteLLMFixture(t)
	if got := liteLLMRequest(t, fixture, http.MethodGet, "/api/v1/litellm", nil, false).Code; got != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", got)
	}
	w := liteLLMRequest(t, fixture, http.MethodGet, "/api/v1/litellm", nil, true)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "proxy-key") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestLiteLLMSaveTestSyncAndDisconnect(t *testing.T) {
	fixture := newLiteLLMFixture(t)
	save := liteLLMRequest(t, fixture, http.MethodPut, "/api/v1/litellm", map[string]string{
		"proxy_url": fixture.fake.URL,
		"api_base":  "http://llamarack.example/v1",
		"proxy_key": "proxy-key",
	}, true)
	if save.Code != http.StatusOK {
		t.Fatalf("save status=%d body=%s", save.Code, save.Body.String())
	}
	if strings.Contains(save.Body.String(), "proxy-key") {
		t.Fatal("save response leaked proxy key")
	}
	if got := liteLLMRequest(t, fixture, http.MethodPost, "/api/v1/litellm/test", nil, true).Code; got != http.StatusOK {
		t.Fatalf("test status=%d", got)
	}
	if got := liteLLMRequest(t, fixture, http.MethodPost, "/api/v1/litellm/sync", nil, true).Code; got != http.StatusOK {
		t.Fatalf("sync status=%d", got)
	}
	rotate := liteLLMRequest(t, fixture, http.MethodPost, "/api/v1/litellm/rotate", nil, true)
	if rotate.Code != http.StatusOK {
		t.Fatalf("rotate status=%d body=%s", rotate.Code, rotate.Body.String())
	}
	if got := liteLLMRequest(t, fixture, http.MethodDelete, "/api/v1/litellm", map[string]bool{"unpublish": true}, true).Code; got != http.StatusNoContent {
		t.Fatalf("disconnect status=%d", got)
	}
}

func TestLiteLLMStoreModelInDBError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "STORE_MODEL_IN_DB must be enabled", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	fixture := newLiteLLMFixture(t)
	save := liteLLMRequest(t, fixture, http.MethodPut, "/api/v1/litellm", map[string]string{
		"proxy_url": server.URL,
		"api_base":  "http://llamarack.example/v1",
		"proxy_key": "proxy-key",
	}, true)
	if save.Code != http.StatusBadRequest || !strings.Contains(save.Body.String(), "STORE_MODEL_IN_DB") {
		t.Fatalf("save status=%d body=%s", save.Code, save.Body.String())
	}
}

func TestLiteLLMValidationErrors(t *testing.T) {
	fixture := newLiteLLMFixture(t)
	if got := liteLLMRequest(t, fixture, http.MethodPut, "/api/v1/litellm", map[string]string{"proxy_url": ""}, true).Code; got != http.StatusBadRequest {
		t.Fatalf("empty save status=%d", got)
	}
}

func TestLiteLLMEndpointsReturnErrorsWhenNotConfigured(t *testing.T) {
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
	token, _, _, err := authService.LoginWithMetadata(ctx, "admin", "password1234", "127.0.0.1", "litellm-test")
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := huggingface.NewSecretStore(db, root)
	if err != nil {
		t.Fatal(err)
	}
	service := litellm.New(db, authService, secrets, settings.New(db, settings.Defaults{}))
	handler := NewLiteLLMHandler(authService, service)
	cookie := &http.Cookie{Name: sessionCookie, Value: token}
	fixture := liteLLMFixture{handler: handler, cookie: cookie, service: service}
	if got := liteLLMRequest(t, fixture, http.MethodPost, "/api/v1/litellm/test", nil, true).Code; got == http.StatusOK {
		t.Fatalf("expected test failure when proxy URL unset")
	}
	if got := liteLLMRequest(t, fixture, http.MethodPost, "/api/v1/litellm/sync", nil, true).Code; got == http.StatusOK {
		t.Fatalf("expected sync failure when proxy URL unset")
	}
}

func TestLiteLLMRouteNotFound(t *testing.T) {
	fixture := newLiteLLMFixture(t)
	if got := liteLLMRequest(t, fixture, http.MethodGet, "/api/v1/litellm/unknown", nil, true).Code; got != http.StatusNotFound {
		t.Fatalf("unknown route status=%d", got)
	}
}

func TestLiteLLMRotateWhenNotConfigured(t *testing.T) {
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
	token, _, _, err := authService.LoginWithMetadata(ctx, "admin", "password1234", "127.0.0.1", "litellm-test")
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := huggingface.NewSecretStore(db, root)
	if err != nil {
		t.Fatal(err)
	}
	service := litellm.New(db, authService, secrets, settings.New(db, settings.Defaults{}))
	handler := NewLiteLLMHandler(authService, service)
	fixture := liteLLMFixture{handler: handler, cookie: &http.Cookie{Name: sessionCookie, Value: token}, service: service}
	if got := liteLLMRequest(t, fixture, http.MethodPost, "/api/v1/litellm/rotate", nil, true).Code; got == http.StatusOK {
		t.Fatalf("rotate should fail when unconfigured, status=%d", got)
	}
}

func TestLiteLLMDisconnectWithoutBody(t *testing.T) {
	fixture := newLiteLLMFixture(t)
	save := liteLLMRequest(t, fixture, http.MethodPut, "/api/v1/litellm", map[string]string{
		"proxy_url": fixture.fake.URL,
		"api_base":  "http://llamarack.example/v1",
		"proxy_key": "proxy-key",
	}, true)
	if save.Code != http.StatusOK {
		t.Fatalf("save status=%d", save.Code)
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/litellm", nil)
	req.AddCookie(fixture.cookie)
	w := httptest.NewRecorder()
	fixture.handler.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("disconnect without body status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestLiteLLMSaveRejectsInvalidProxyURL(t *testing.T) {
	fixture := newLiteLLMFixture(t)
	save := liteLLMRequest(t, fixture, http.MethodPut, "/api/v1/litellm", map[string]string{
		"proxy_url": "ftp://example.com",
		"proxy_key": "proxy-key",
	}, true)
	if save.Code != http.StatusBadRequest {
		t.Fatalf("invalid proxy URL status=%d body=%s", save.Code, save.Body.String())
	}
}

func TestLiteLLMSaveInvalidJSON(t *testing.T) {
	fixture := newLiteLLMFixture(t)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/litellm", strings.NewReader("{"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(fixture.cookie)
	w := httptest.NewRecorder()
	fixture.handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid json status=%d", w.Code)
	}
}

func TestLiteLLMProxyConnectionFailureIsBadGateway(t *testing.T) {
	fixture := newLiteLLMFixture(t)
	fixture.fake.Close()
	save := liteLLMRequest(t, fixture, http.MethodPut, "/api/v1/litellm", map[string]string{
		"proxy_url": fixture.fake.URL,
		"api_base":  "http://llamarack.example/v1",
		"proxy_key": "proxy-key",
	}, true)
	if save.Code != http.StatusBadGateway {
		t.Fatalf("expected bad gateway, got %d body=%s", save.Code, save.Body.String())
	}
}
