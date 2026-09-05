package api

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
	"github.com/brantje/llamarack/backend/internal/downloads"
	"github.com/brantje/llamarack/backend/internal/huggingface"
	"github.com/brantje/llamarack/backend/internal/modelimports"
	"github.com/brantje/llamarack/backend/internal/models"
)

func newHuggingFaceImportFixture(t *testing.T) huggingFaceFixture {
	t.Helper()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/models/acme/demo":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "acme/demo", "author": "acme", "sha": "rev1",
				"siblings": []map[string]any{{"rfilename": "demo-Q4_K_M.gguf", "size": 1, "blobId": "oid1"}},
			})
		case r.URL.Path == "/acme/demo/resolve/rev1/demo-Q4_K_M.gguf":
			w.Header().Set("ETag", "v1")
			w.Header().Set("X-Linked-Size", "1")
			if r.Method != http.MethodHead {
				_, _ = w.Write([]byte("x"))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(provider.Close)

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
	token, _, _, err := authService.LoginWithMetadata(ctx, "admin", "password1234", "127.0.0.1", "huggingface-import-test")
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := huggingface.NewSecretStore(db, root)
	if err != nil {
		t.Fatal(err)
	}
	hf, err := huggingface.NewClientWithHTTP(provider.URL, secrets.GetToken, provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	modelsDir := filepath.Join(root, "models")
	modelService := models.New(db, modelsDir)
	downloadManager := downloads.New(context.Background(), db, modelsDir, hf)
	imports := modelimports.New(db, modelsDir, modelService, downloadManager, nil)
	return huggingFaceFixture{
		handler: NewHuggingFaceHandler(authService, hf, secrets, downloadManager, imports),
		cookie:  &http.Cookie{Name: sessionCookie, Value: token}, server: provider,
	}
}

func TestHuggingFaceImportCreatesPendingModelAndInstance(t *testing.T) {
	fixture := newHuggingFaceImportFixture(t)
	detail := huggingFaceRequest(t, fixture, http.MethodGet, "/api/v1/huggingface/model?repo=acme%2Fdemo", nil, true)
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	var modelDetail huggingface.ModelDetail
	if err := json.Unmarshal(detail.Body.Bytes(), &modelDetail); err != nil || len(modelDetail.Artifacts) != 1 {
		t.Fatalf("detail=%+v err=%v", modelDetail, err)
	}
	body := map[string]any{
		"repo_id": "acme/demo", "artifact_id": modelDetail.Artifacts[0].ID, "name": "Demo Q4", "context_length": 8192,
		"first_instance": map[string]any{
			"name": "Demo API", "slug": "demo-api", "always_on": false,
			"autoload_enabled": true, "eviction_enabled": true, "start": false,
		},
	}
	w := huggingFaceRequest(t, fixture, http.MethodPost, "/api/v1/huggingface/import", body, true)
	if w.Code != http.StatusCreated {
		t.Fatalf("import status=%d body=%s", w.Code, w.Body.String())
	}
	var prepared modelimports.PrepareResult
	if err := json.Unmarshal(w.Body.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}
	if prepared.Instance.ID == "" || prepared.Instance.ID == prepared.Instance.Slug || prepared.Instance.Slug != "demo-api" || prepared.Model.Name != "Demo Q4" {
		t.Fatalf("prepared import=%+v", prepared)
	}

	w = huggingFaceRequest(t, fixture, http.MethodGet, "/api/v1/imports", nil, true)
	if w.Code != http.StatusOK {
		t.Fatalf("statuses status=%d body=%s", w.Code, w.Body.String())
	}
	var statuses []modelimports.Status
	if err := json.Unmarshal(w.Body.Bytes(), &statuses); err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].InstanceID != prepared.Instance.ID {
		t.Fatalf("provider import did not retain durable Instance link: %+v", statuses)
	}
	if strings.Contains(w.Body.String(), `"instance_id":"demo-api"`) {
		t.Fatalf("provider import persisted mutable slug as durable identity: %s", w.Body.String())
	}
}

func TestHuggingFaceImportValidationAndMethodBranches(t *testing.T) {
	fixture := newHuggingFaceImportFixture(t)
	if got := huggingFaceRequest(t, fixture, http.MethodGet, "/api/v1/huggingface/import", nil, true).Code; got != http.StatusMethodNotAllowed {
		t.Fatalf("import GET status=%d", got)
	}
	if got := huggingFaceRequest(t, fixture, http.MethodPost, "/api/v1/imports", nil, true).Code; got != http.StatusMethodNotAllowed {
		t.Fatalf("imports POST status=%d", got)
	}
	w := huggingFaceRequest(t, fixture, http.MethodPost, "/api/v1/huggingface/import", map[string]any{
		"repo_id": "acme/demo", "artifact_id": "not-current", "name": "Demo",
		"first_instance": map[string]any{"name": "Demo", "slug": "demo"},
	}, true)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "artifact") {
		t.Fatalf("invalid artifact status=%d body=%s", w.Code, w.Body.String())
	}
	w = huggingFaceRequest(t, fixture, http.MethodPost, "/api/v1/huggingface/import", map[string]any{
		"repo_id": "invalid", "artifact_id": "x", "name": "Demo",
		"first_instance": map[string]any{"name": "Demo", "slug": "demo"},
	}, true)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("provider error status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestHuggingFaceImportRoutesWithoutImportService(t *testing.T) {
	fixture := newHuggingFaceFixture(t)
	w := huggingFaceRequest(t, fixture, http.MethodGet, "/api/v1/imports", nil, true)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("nil import statuses=%d body=%q", w.Code, w.Body.String())
	}
	w = huggingFaceRequest(t, fixture, http.MethodPost, "/api/v1/huggingface/import", map[string]any{}, true)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("nil import create=%d body=%s", w.Code, w.Body.String())
	}
}
