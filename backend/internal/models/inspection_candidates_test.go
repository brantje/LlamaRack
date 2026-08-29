package models

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectGGUFArtifactCandidatesExposeMetadataClassifiedAlternates(t *testing.T) {
	s, dir := testModelService(t)
	main := writeClassifiedGGUF(t, dir, "model-Q5_K_M.gguf", "qwen2", 0, true)
	projectorF16 := writeClassifiedGGUF(t, dir, "projector-F16.gguf", "clip", 0, false)
	projectorQ8 := writeClassifiedGGUF(t, dir, "projector-Q8_0.gguf", "clip", 0, false)
	draftQ4 := writeClassifiedGGUF(t, dir, "draft-Q4_0.gguf", "qwen35", 1, false)
	draftQ8 := writeClassifiedGGUF(t, dir, "draft-Q8_0.gguf", "qwen35", 1, false)
	_ = writeClassifiedGGUF(t, dir, "ordinary-helper.gguf", "qwen2", 0, false)
	_ = writeClassifiedGGUF(t, dir, "partial-projector-00001-of-00002.gguf", "clip", 0, false)
	if err := os.WriteFile(filepath.Join(dir, "broken-helper.gguf"), []byte("broken"), 0o644); err != nil {
		t.Fatal(err)
	}

	candidates, err := s.InspectGGUFArtifactCandidates(context.Background(), main)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 4 {
		t.Fatalf("candidates = %+v", candidates)
	}

	want := map[string]string{
		"mmproj:projector-F16.gguf":  projectorF16,
		"mmproj:projector-Q8_0.gguf": projectorQ8,
		"mtp:draft-Q4_0.gguf":        draftQ4,
		"mtp:draft-Q8_0.gguf":        draftQ8,
	}
	for _, candidate := range candidates {
		key := candidate.Kind + ":" + candidate.Name
		expected, ok := want[key]
		if !ok {
			t.Fatalf("unexpected candidate %+v", candidate)
		}
		if filepath.Clean(candidate.OptionPath) != filepath.Clean(expected) {
			t.Fatalf("candidate %s option path = %q want %q", key, candidate.OptionPath, expected)
		}
		if candidate.TotalBytes <= 0 || len(candidate.Files) == 0 {
			t.Fatalf("candidate %s missing artifact data: %+v", key, candidate)
		}
	}
	if candidates[0].Kind != "mmproj" || candidates[1].Kind != "mmproj" || candidates[2].Kind != "mtp" || candidates[3].Kind != "mtp" {
		t.Fatalf("candidate order = %+v", candidates)
	}
}

func TestInspectGGUFArtifactCandidatesUsesCompletedDownloadScope(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	repoDir := filepath.Join(dir, "huggingface", "org", "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	main := writeClassifiedGGUF(t, repoDir, "model-Q5_K_M.gguf", "qwen2", 0, true)
	included := writeClassifiedGGUF(t, repoDir, "included-F16.gguf", "clip", 0, false)
	_ = writeClassifiedGGUF(t, repoDir, "outside-Q8_0.gguf", "clip", 0, false)

	mainRel := filepath.ToSlash(filepath.Join("huggingface", "org", "repo", filepath.Base(main)))
	includedRel := filepath.ToSlash(filepath.Join("huggingface", "org", "repo", filepath.Base(included)))
	if _, err := s.db.ExecContext(ctx, `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,state,total_bytes,downloaded_bytes) VALUES('candidate-job','huggingface','org/repo','rev','artifact','model-Q5_K_M.gguf','COMPLETED',20,20)`); err != nil {
		t.Fatal(err)
	}
	for ordinal, item := range []struct{ provider, local string }{
		{"model-Q5_K_M.gguf", mainRel},
		{"included-F16.gguf", includedRel},
	} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO download_files(job_id,path,size,state,downloaded_bytes,ordinal,local_path) VALUES('candidate-job',?,10,'COMPLETED',10,?,?)`, item.provider, ordinal, item.local); err != nil {
			t.Fatal(err)
		}
	}

	candidates, err := s.InspectGGUFArtifactCandidates(ctx, main)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Name != "included-F16.gguf" || filepath.Clean(candidates[0].OptionPath) != filepath.Clean(included) {
		t.Fatalf("download scoped candidates = %+v", candidates)
	}
}

func TestInspectGGUFArtifactCandidatesSkipIncompleteMain(t *testing.T) {
	s, dir := testModelService(t)
	main := writeClassifiedGGUF(t, dir, "partial-Q4_K_M-00001-of-00002.gguf", "qwen2", 0, true)
	_ = writeClassifiedGGUF(t, dir, "projector-F16.gguf", "clip", 0, false)

	candidates, err := s.InspectGGUFArtifactCandidates(context.Background(), main)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("incomplete main candidates = %+v", candidates)
	}
}

func TestInspectGGUFArtifactCandidatesRejectsMissingMain(t *testing.T) {
	s, _ := testModelService(t)
	if _, err := s.InspectGGUFArtifactCandidates(context.Background(), "missing.gguf"); err == nil {
		t.Fatal("expected missing main error")
	}
}
