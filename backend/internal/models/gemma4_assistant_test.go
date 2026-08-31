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
	projector := writeClassifiedGGUF(t, dir, "mmproj-gemma-4-12b-it-F16.gguf", "clip", 0, false)

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
	if available[0].SuggestedOptions["spec-draft-model"] != mtp || available[0].SuggestedOptions["spec-type"] != "draft-mtp" {
		t.Fatalf("available MTP options = %+v", available[0].SuggestedOptions)
	}
	if available[0].SuggestedOptions["mmproj"] != projector {
		t.Fatalf("available mmproj = %q want %q", available[0].SuggestedOptions["mmproj"], projector)
	}
	if kind := localSidecarKind(mtp); kind != "mtp" {
		t.Fatalf("sidecar kind = %q", kind)
	}

	inspection, err := s.InspectGGUFArtifact(ctx, main)
	if err != nil {
		t.Fatal(err)
	}
	if len(inspection.Dependencies) != 2 {
		t.Fatalf("dependencies = %+v", inspection.Dependencies)
	}
	kinds := map[string]string{}
	for _, dep := range inspection.Dependencies {
		kinds[dep.Kind] = dep.Name
	}
	if kinds["mtp"] != filepath.Base(mtp) || kinds["mmproj"] != filepath.Base(projector) {
		t.Fatalf("dependency kinds = %+v", kinds)
	}
	if inspection.SuggestedOptions["spec-draft-model"] != mtp || inspection.SuggestedOptions["spec-type"] != "draft-mtp" {
		t.Fatalf("suggested options = %+v", inspection.SuggestedOptions)
	}
	if inspection.SuggestedOptions["mmproj"] != projector {
		t.Fatalf("inspect mmproj = %q want %q", inspection.SuggestedOptions["mmproj"], projector)
	}
}
