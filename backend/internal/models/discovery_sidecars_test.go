package models

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAvailableGGUFsHidesHelpersWithoutBroadSubstringFiltering(t *testing.T) {
	s, dir := testModelService(t)
	for _, name := range []string{
		"mmproj-F16.gguf", "mmoproj_model.gguf", "projector.vision.gguf", "mtp-model-Q4_0.gguf",
		"asda-projector-Q4_K_M.gguf", "model-MTP-Q4_K_M.gguf",
	} {
		writeGGUF(t, dir, name)
	}

	files, err := s.AvailableGGUFs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("available files = %+v", files)
	}
	seen := map[string]bool{}
	for _, file := range files {
		seen[file.Name] = true
	}
	if !seen["asda-projector-Q4_K_M.gguf"] || !seen["model-MTP-Q4_K_M.gguf"] {
		t.Fatalf("normal models containing helper words must stay visible: %+v", files)
	}
	if isProjectorGGUF("asda-projector-Q4_K_M.gguf") || isMTPGGUF("model-MTP-Q4_K_M.gguf") {
		t.Fatal("helper classification must be prefix based")
	}
}

func TestAvailableGGUFsSuggestsDownloadedSidecarOptions(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	repoDir := filepath.Join(dir, "huggingface", "org", "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mainPath := writeGGUF(t, repoDir, "model-Q4_K_M.gguf")
	projectorPath := writeGGUF(t, repoDir, "mmproj-F16.gguf")
	mtpPath := writeGGUF(t, repoDir, "mtp-model-Q4_0.gguf")

	if _, err := s.db.ExecContext(ctx, `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,state,total_bytes,downloaded_bytes) VALUES('job','huggingface','org/repo','rev','artifact','model-Q4_K_M.gguf','COMPLETED',30,30)`); err != nil {
		t.Fatal(err)
	}
	for ordinal, item := range []struct{ provider, local string }{
		{"model-Q4_K_M.gguf", filepath.ToSlash(filepath.Join("huggingface", "org", "repo", "model-Q4_K_M.gguf"))},
		{"mmproj-F16.gguf", filepath.ToSlash(filepath.Join("huggingface", "org", "repo", "mmproj-F16.gguf"))},
		{"mtp-model-Q4_0.gguf", filepath.ToSlash(filepath.Join("huggingface", "org", "repo", "mtp-model-Q4_0.gguf"))},
	} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO download_files(job_id,path,size,state,downloaded_bytes,ordinal,local_path) VALUES('job',?,10,'COMPLETED',10,?,?)`, item.provider, ordinal, item.local); err != nil {
			t.Fatal(err)
		}
	}

	files, err := s.AvailableGGUFs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != filepath.ToSlash(filepath.Join("huggingface", "org", "repo", "model-Q4_K_M.gguf")) {
		t.Fatalf("available files = %+v", files)
	}
	options := files[0].SuggestedOptions
	if options["mmproj"] != projectorPath {
		t.Fatalf("mmproj = %q want %q", options["mmproj"], projectorPath)
	}
	if options["spec-draft-model"] != mtpPath || options["spec-type"] != "draft-mtp" {
		t.Fatalf("MTP options = %+v", options)
	}
	if files[0].Path == mainPath {
		t.Fatal("public model path must remain relative to the model directory")
	}
}
