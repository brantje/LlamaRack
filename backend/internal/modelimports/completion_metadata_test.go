package modelimports

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/downloads"
	"github.com/brantje/llamarack/backend/internal/models"
)

func TestListResolvedDetectsHuggingFaceContextAfterCompletion(t *testing.T) {
	ctx, modelsDir, db, modelService, _, service := newImportFixture(t, http.NotFoundHandler())
	rel := "huggingface/acme/demo/model-Q4_K_M.gguf"
	path := filepath.Join(modelsDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeImportMetadataGGUF(t, path, "gemma3", 131072)
	model, err := modelService.Create(ctx, models.CreateModelInput{Name: "HF", GGUFPath: rel})
	if err != nil {
		t.Fatal(err)
	}
	insertCompletedImport(t, ctx, db, "job-context", "import-context", model.ID)

	statuses, err := service.ListResolved(ctx)
	if err != nil || len(statuses) != 1 {
		t.Fatalf("statuses=%+v err=%v", statuses, err)
	}
	if statuses[0].Error != "" {
		t.Fatalf("unexpected warning=%q", statuses[0].Error)
	}
	stored, err := modelService.GetByID(ctx, model.ID)
	if err != nil || stored.ContextLength != 131072 {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}

func TestListResolvedShowsHuggingFaceContextDetectionWarning(t *testing.T) {
	ctx, modelsDir, db, modelService, _, service := newImportFixture(t, http.NotFoundHandler())
	rel := "huggingface/acme/demo/broken.gguf"
	path := filepath.Join(modelsDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not-a-gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	model, err := modelService.Create(ctx, models.CreateModelInput{Name: "Broken HF", GGUFPath: rel})
	if err != nil {
		t.Fatal(err)
	}
	insertCompletedImport(t, ctx, db, "job-warning", "import-warning", model.ID)

	statuses, err := service.ListResolved(ctx)
	if err != nil || len(statuses) != 1 {
		t.Fatalf("statuses=%+v err=%v", statuses, err)
	}
	if !strings.Contains(statuses[0].Error, "Context capability could not be detected automatically") {
		t.Fatalf("warning=%q", statuses[0].Error)
	}
}

func insertCompletedImport(t *testing.T, ctx context.Context, db execDB, jobID, importID, modelID string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,state,total_bytes,downloaded_bytes) VALUES(?,?,?,?,?,?,?,1,1)`, jobID, "huggingface", "acme/demo", "rev", "artifact", "model.gguf", downloads.StateCompleted); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO provider_imports(id,job_id,model_id,owns_model,start_when_ready,state,error,start_attempted) VALUES(?,?,?,1,0,?,'',1)`, importID, jobID, modelID, StateCompleted); err != nil {
		t.Fatal(err)
	}
}

type execDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func writeImportMetadataGGUF(t *testing.T, path, architecture string, contextLength int64) {
	t.Helper()
	var body bytes.Buffer
	body.WriteString("GGUF")
	mustImportWrite(t, &body, uint32(3))
	mustImportWrite(t, &body, uint64(0))
	mustImportWrite(t, &body, uint64(2))
	writeImportString(t, &body, "general.architecture")
	mustImportWrite(t, &body, uint32(8))
	writeImportString(t, &body, architecture)
	writeImportString(t, &body, architecture+".context_length")
	mustImportWrite(t, &body, uint32(11))
	mustImportWrite(t, &body, contextLength)
	if err := os.WriteFile(path, body.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeImportString(t *testing.T, body *bytes.Buffer, value string) {
	t.Helper()
	mustImportWrite(t, body, uint64(len(value)))
	_, _ = body.WriteString(value)
}

func mustImportWrite(t *testing.T, body *bytes.Buffer, value any) {
	t.Helper()
	if err := binary.Write(body, binary.LittleEndian, value); err != nil {
		t.Fatal(err)
	}
}
