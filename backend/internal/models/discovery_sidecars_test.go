package models

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAvailableGGUFsHidesMetadataClassifiedHelpersButKeepsNativeMTP(t *testing.T) {
	s, dir := testModelService(t)
	writeClassifiedGGUF(t, dir, "arbitrary-vision.gguf", "clip", 0, false)
	writeClassifiedGGUF(t, dir, "arbitrary-draft.gguf", "qwen35", 1, false)
	native := writeClassifiedGGUF(t, dir, "model-MTP-Q4_K_M.gguf", "qwen35", 1, true)

	files, err := s.AvailableGGUFs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name != "model-MTP-Q4_K_M.gguf" {
		t.Fatalf("available files = %+v", files)
	}
	if isMTPGGUF(native) {
		t.Fatal("native MTP main model must not be hidden as an MTP-only helper")
	}
	if files[0].SuggestedOptions["spec-type"] != "draft-mtp" || files[0].SuggestedOptions["spec-draft-n-max"] != "16" || files[0].SuggestedOptions["spec-draft-p-min"] != "0.8" {
		t.Fatalf("native MTP defaults = %+v", files[0].SuggestedOptions)
	}
}

func TestAvailableGGUFsSuggestsDownloadedSidecarOptionsFromMetadata(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	repoDir := filepath.Join(dir, "huggingface", "org", "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeClassifiedGGUF(t, repoDir, "model-Q4_K_M.gguf", "qwen2", 0, true)
	projectorPath := writeClassifiedGGUF(t, repoDir, "anything.gguf", "clip", 0, false)
	mtpPath := writeClassifiedGGUF(t, repoDir, "also-anything.gguf", "qwen35", 1, false)

	if _, err := s.db.ExecContext(ctx, `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,state,total_bytes,downloaded_bytes) VALUES('job','huggingface','org/repo','rev','artifact','model-Q4_K_M.gguf','COMPLETED',30,30)`); err != nil {
		t.Fatal(err)
	}
	for ordinal, item := range []struct{ provider, local string }{
		{"model-Q4_K_M.gguf", filepath.ToSlash(filepath.Join("huggingface", "org", "repo", "model-Q4_K_M.gguf"))},
		{"anything.gguf", filepath.ToSlash(filepath.Join("huggingface", "org", "repo", "anything.gguf"))},
		{"also-anything.gguf", filepath.ToSlash(filepath.Join("huggingface", "org", "repo", "also-anything.gguf"))},
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
	if options["spec-draft-model"] != mtpPath || options["spec-type"] != "draft-mtp" || options["spec-draft-n-max"] != "16" || options["spec-draft-p-min"] != "0.8" {
		t.Fatalf("MTP options = %+v", options)
	}
}
