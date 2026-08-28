package models

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestGemma4AssistantIsExcludedFromAvailableAndAttachedByInspect(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	main := writeClassifiedGGUF(t, dir, "gemma-4-12b-it-UD-Q5_K_XL.gguf", "gemma4", 0, true)
	mtp := writeClassifiedGGUF(t, dir, "mtp-gemma-4-12b-it.gguf", "gemma4-assistant", 4, true)

	// Reproduce an index row created by the old classifier: the file fingerprint
	// is unchanged and the architecture is known, but mtp_only was cached false.
	mtpInfo, err := os.Stat(mtp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO gguf_index(path,size_bytes,mtime_ns,architecture,has_mtp,mtp_only)
VALUES(?,?,?,?,1,0)`, filepath.Base(mtp), mtpInfo.Size(), mtpInfo.ModTime().UnixNano(), "gemma4-assistant"); err != nil {
		t.Fatal(err)
	}

	available, err := s.AvailableGGUFs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(available) != 1 || available[0].Path != filepath.Base(main) {
		t.Fatalf("available = %+v", available)
	}
	if kind := localSidecarKind(mtp); kind != "mtp" {
		t.Fatalf("sidecar kind = %q", kind)
	}

	inspection, err := s.InspectGGUFArtifact(ctx, main)
	if err != nil {
		t.Fatal(err)
	}
	if len(inspection.Dependencies) != 1 {
		t.Fatalf("dependencies = %+v", inspection.Dependencies)
	}
	if got := inspection.Dependencies[0]; got.Kind != "mtp" || got.Name != filepath.Base(mtp) {
		t.Fatalf("mtp dependency = %+v", got)
	}
	if inspection.SuggestedOptions["spec-draft-model"] != mtp || inspection.SuggestedOptions["spec-type"] != "draft-mtp" {
		t.Fatalf("suggested options = %+v", inspection.SuggestedOptions)
	}
}
