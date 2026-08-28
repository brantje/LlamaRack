package models

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectGGUFArtifactSelectsMetadataHelpersLikeProvider(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	main := writeClassifiedGGUF(t, dir, "model-Q5_K_M.gguf", "qwen2", 0, true)
	projectorF16 := writeClassifiedGGUF(t, dir, "totally-unrelated-name-F16.gguf", "clip", 0, false)
	_ = writeClassifiedGGUF(t, dir, "another-helper-Q8_0.gguf", "clip", 0, false)
	mtpQ4 := writeClassifiedGGUF(t, dir, "draft-without-mtp-name-Q4_0.gguf", "qwen35", 1, false)
	_ = writeClassifiedGGUF(t, dir, "another-draft-Q8_0.gguf", "qwen35", 1, false)

	inspection, err := s.InspectGGUFArtifact(ctx, main)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.ID != "model-Q5_K_M.gguf" || inspection.Name != "model-Q5_K_M.gguf" || inspection.Quantization != "Q5_K_M" {
		t.Fatalf("identity = %+v", inspection)
	}
	if !inspection.Complete || inspection.ShardCount != 1 || inspection.ExpectedShards != 1 {
		t.Fatalf("main grouping = %+v", inspection)
	}
	if inspection.Architecture != "qwen2" || inspection.GGUFVersion != 3 {
		t.Fatalf("metadata = %+v", inspection)
	}
	if len(inspection.Dependencies) != 2 {
		t.Fatalf("dependencies = %+v", inspection.Dependencies)
	}
	if got := inspection.Dependencies[0]; got.Kind != "mmproj" || got.Name != filepath.Base(projectorF16) || got.Quantization != "F16" {
		t.Fatalf("projector = %+v", got)
	}
	if got := inspection.Dependencies[1]; got.Kind != "mtp" || got.Name != filepath.Base(mtpQ4) || got.Quantization != "Q4_0" {
		t.Fatalf("mtp = %+v", got)
	}
	if len(inspection.Files) != 3 {
		t.Fatalf("artifact files = %+v", inspection.Files)
	}
	if inspection.SuggestedOptions["mmproj"] != projectorF16 {
		t.Fatalf("mmproj option = %q want %q", inspection.SuggestedOptions["mmproj"], projectorF16)
	}
	if inspection.SuggestedOptions["spec-draft-model"] != mtpQ4 || inspection.SuggestedOptions["spec-type"] != "draft-mtp" || inspection.SuggestedOptions["spec-draft-n-max"] != "16" || inspection.SuggestedOptions["spec-draft-p-min"] != "0.8" {
		t.Fatalf("MTP options = %+v", inspection.SuggestedOptions)
	}
	mainInfo, _ := os.Stat(main)
	projectorInfo, _ := os.Stat(projectorF16)
	mtpInfo, _ := os.Stat(mtpQ4)
	if inspection.ModelBytes != mainInfo.Size() || inspection.TotalBytes != mainInfo.Size()+projectorInfo.Size()+mtpInfo.Size() {
		t.Fatalf("bytes model=%d total=%d", inspection.ModelBytes, inspection.TotalBytes)
	}
}

func TestInspectGGUFArtifactUsesExactCompletedDownloadScope(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	repoDir := filepath.Join(dir, "huggingface", "org", "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	main := writeClassifiedGGUF(t, repoDir, "model-Q5_K_M.gguf", "qwen2", 0, true)
	selected := writeClassifiedGGUF(t, repoDir, "selected-Q8_0.gguf", "clip", 0, false)
	_ = writeClassifiedGGUF(t, repoDir, "unrelated-F16.gguf", "clip", 0, false)

	mainRel := filepath.ToSlash(filepath.Join("huggingface", "org", "repo", filepath.Base(main)))
	selectedRel := filepath.ToSlash(filepath.Join("huggingface", "org", "repo", filepath.Base(selected)))
	if _, err := s.db.ExecContext(ctx, `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,state,total_bytes,downloaded_bytes) VALUES('job','huggingface','org/repo','rev','artifact','model-Q5_K_M.gguf','COMPLETED',20,20)`); err != nil {
		t.Fatal(err)
	}
	for ordinal, item := range []struct{ provider, local string }{
		{"model-Q5_K_M.gguf", mainRel},
		{"selected-Q8_0.gguf", selectedRel},
	} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO download_files(job_id,path,size,state,downloaded_bytes,ordinal,local_path) VALUES('job',?,10,'COMPLETED',10,?,?)`, item.provider, ordinal, item.local); err != nil {
			t.Fatal(err)
		}
	}

	inspection, err := s.InspectGGUFArtifact(ctx, main)
	if err != nil {
		t.Fatal(err)
	}
	if len(inspection.Dependencies) != 1 {
		t.Fatalf("dependencies = %+v", inspection.Dependencies)
	}
	if got := inspection.Dependencies[0]; got.Kind != "mmproj" || got.Name != filepath.Base(selected) || got.Quantization != "Q8_0" {
		t.Fatalf("download-scoped projector = %+v", got)
	}
	if inspection.SuggestedOptions["mmproj"] != selected {
		t.Fatalf("mmproj option = %q want %q", inspection.SuggestedOptions["mmproj"], selected)
	}
}

func TestInspectGGUFArtifactGroupsSplitFiles(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	main1 := writeClassifiedGGUF(t, dir, "model-Q4_K_M-00001-of-00002.gguf", "qwen2", 0, true)
	main2 := writeClassifiedGGUF(t, dir, "model-Q4_K_M-00002-of-00002.gguf", "qwen2", 0, true)
	projector1 := writeClassifiedGGUF(t, dir, "vision-F16-00001-of-00002.gguf", "clip", 0, false)
	projector2 := writeClassifiedGGUF(t, dir, "vision-F16-00002-of-00002.gguf", "clip", 0, false)
	_ = writeClassifiedGGUF(t, dir, "incomplete-Q8_0-00001-of-00002.gguf", "clip", 0, false)

	inspection, err := s.InspectGGUFArtifact(ctx, main1)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Name != "model-Q4_K_M.gguf" || inspection.ShardCount != 2 || inspection.ExpectedShards != 2 || !inspection.Complete {
		t.Fatalf("split main = %+v", inspection)
	}
	if len(inspection.Dependencies) != 1 || inspection.Dependencies[0].Name != "vision-F16.gguf" || len(inspection.Dependencies[0].Files) != 2 {
		t.Fatalf("split projector = %+v", inspection.Dependencies)
	}
	if len(inspection.Files) != 4 {
		t.Fatalf("combined files = %+v", inspection.Files)
	}
	mainInfo1, _ := os.Stat(main1)
	mainInfo2, _ := os.Stat(main2)
	projectorInfo1, _ := os.Stat(projector1)
	projectorInfo2, _ := os.Stat(projector2)
	if inspection.ModelBytes != mainInfo1.Size()+mainInfo2.Size() || inspection.TotalBytes != mainInfo1.Size()+mainInfo2.Size()+projectorInfo1.Size()+projectorInfo2.Size() {
		t.Fatalf("split bytes model=%d total=%d", inspection.ModelBytes, inspection.TotalBytes)
	}
}
