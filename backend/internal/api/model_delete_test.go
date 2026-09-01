package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/models"
)

func TestDeleteModelFilesRoute(t *testing.T) {
	t.Run("rejects invalid destructive flag without changing the Model", func(t *testing.T) {
		f := newAPIFixture(t, nil)
		cookie := bootstrapAndLogin(t, f)
		model := createModel(t, f, cookie)
		path := filepath.Join(f.dir, model.GGUFPath)

		w := doRequest(t, f.server, http.MethodDelete, "/api/v1/models/"+model.ID+"?delete_files=definitely", nil, cookie)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "delete_files must be true or false") {
			t.Fatalf("invalid delete_files=%d body=%s", w.Code, w.Body.String())
		}
		if _, err := f.models.GetByID(context.Background(), model.ID); err != nil {
			t.Fatalf("invalid flag removed Model: %v", err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("invalid flag touched model file: %v", err)
		}
	})

	t.Run("deletes the exact backing file after explicit opt in", func(t *testing.T) {
		f := newAPIFixture(t, nil)
		cookie := bootstrapAndLogin(t, f)
		model := createModel(t, f, cookie)
		path := filepath.Join(f.dir, model.GGUFPath)

		w := doRequest(t, f.server, http.MethodDelete, "/api/v1/models/"+model.ID+"?delete_files=true", nil, cookie)
		if w.Code != http.StatusNoContent {
			t.Fatalf("destructive delete=%d body=%s", w.Code, w.Body.String())
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("model file still exists: %v", err)
		}
		if _, err := f.models.GetByID(context.Background(), model.ID); err == nil {
			t.Fatal("Model row still exists after destructive deletion")
		}
	})

	t.Run("returns not found before lifecycle changes for a missing Model", func(t *testing.T) {
		f := newAPIFixture(t, nil)
		cookie := bootstrapAndLogin(t, f)
		w := doRequest(t, f.server, http.MethodDelete, "/api/v1/models/missing?delete_files=true", nil, cookie)
		if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "model not found") {
			t.Fatalf("missing destructive delete=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("rejects an unsafe stored artifact path and keeps the Model", func(t *testing.T) {
		f := newAPIFixture(t, nil)
		cookie := bootstrapAndLogin(t, f)
		model := createModel(t, f, cookie)
		f.dbExec(`UPDATE models SET gguf_path='../outside.gguf' WHERE id=?`, model.ID)

		w := doRequest(t, f.server, http.MethodDelete, "/api/v1/models/"+model.ID+"?delete_files=true", nil, cookie)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "unsafe model artifact path") {
			t.Fatalf("unsafe destructive delete=%d body=%s", w.Code, w.Body.String())
		}
		if _, err := f.models.GetByID(context.Background(), model.ID); err != nil {
			t.Fatalf("unsafe path removed Model: %v", err)
		}
	})

	t.Run("refuses deletion when an explicit helper is shared", func(t *testing.T) {
		f := newAPIFixture(t, nil)
		cookie := bootstrapAndLogin(t, f)
		shared := filepath.Join(f.dir, "shared-mmproj.gguf")
		if err := os.WriteFile(shared, []byte("gguf"), 0o644); err != nil {
			t.Fatal(err)
		}
		firstPath := filepath.Join(f.dir, "first.gguf")
		secondPath := filepath.Join(f.dir, "second.gguf")
		for _, path := range []string{firstPath, secondPath} {
			if err := os.WriteFile(path, []byte("gguf"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		first, err := f.models.Create(context.Background(), models.CreateModelInput{Name: "First", GGUFPath: firstPath, Options: map[string]string{"mmproj": shared}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.models.Create(context.Background(), models.CreateModelInput{Name: "Second", GGUFPath: secondPath, Options: map[string]string{"mmproj": shared}}); err != nil {
			t.Fatal(err)
		}

		w := doRequest(t, f.server, http.MethodDelete, "/api/v1/models/"+first.ID+"?delete_files=true", nil, cookie)
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "referenced by Model") {
			t.Fatalf("shared destructive delete=%d body=%s", w.Code, w.Body.String())
		}
		if _, err := f.models.GetByID(context.Background(), first.ID); err != nil {
			t.Fatalf("shared conflict removed Model: %v", err)
		}
		if _, err := os.Stat(shared); err != nil {
			t.Fatalf("shared helper was touched: %v", err)
		}
	})
}

func TestWriteModelDeleteErrorInternalFailure(t *testing.T) {
	w := httptest.NewRecorder()
	writeModelDeleteError(w, errors.New("disk failed"))
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "disk failed") {
		t.Fatalf("internal delete error=%d body=%s", w.Code, w.Body.String())
	}
}
