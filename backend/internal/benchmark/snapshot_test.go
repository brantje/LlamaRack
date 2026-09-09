package benchmark

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/models"
)

type selectedArtifactSource struct {
	*benchmarkTestModelSource
	inspections map[string]models.GGUFInspection
}

func (s selectedArtifactSource) InspectGGUFArtifact(_ context.Context, path string) (models.GGUFInspection, error) {
	if value, ok := s.inspections[path]; ok {
		return value, nil
	}
	return models.GGUFInspection{}, os.ErrNotExist
}

func TestCaptureTargetUsesOnlyEffectiveDependencies(t *testing.T) {
	s, _, _, _, cfg := testBenchmarkService(t, benchmarkTestExecutor{})
	base := s.models.(*benchmarkTestModelSource)
	main := base.inspection
	// The inspection offers a missing companion. It is not selected and must
	// neither block the run nor affect the snapshot or resource demand.
	main.Dependencies = []models.GGUFArtifactDependency{{Kind: "mmproj", Files: []models.GGUFArtifactFile{{Path: "unused.gguf", Size: 999}}}}
	main.Files = append(main.Files, main.Dependencies[0].Files...)
	source := selectedArtifactSource{benchmarkTestModelSource: base, inspections: map[string]models.GGUFInspection{"model.gguf": main}}
	for _, path := range []string{"selected.gguf", "draft-00001-of-00002.gguf", "draft-00002-of-00002.gguf"} {
		if err := os.WriteFile(filepath.Join(base.root, path), []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	selectedPath := filepath.Join(base.root, "selected.gguf")
	source.inspections[selectedPath] = models.GGUFInspection{Complete: true, ShardCount: 1, ExpectedShards: 1, ModelBytes: 4, Quantization: "F16", Files: []models.GGUFArtifactFile{{Path: "selected.gguf", Size: 4}}}
	source.inspections["draft-00001-of-00002.gguf"] = models.GGUFInspection{Complete: true, ShardCount: 2, ExpectedShards: 2, ModelBytes: 8, Files: []models.GGUFArtifactFile{{Path: "draft-00001-of-00002.gguf", Size: 4}, {Path: "draft-00002-of-00002.gguf", Size: 4}}}
	s.models = source
	capture := func() capturedTarget {
		t.Helper()
		target, err := captureTarget(context.Background(), "instance-1", s.instances, s.models, s.config)
		if err != nil {
			t.Fatal(err)
		}
		return target
	}
	disabled := capture()
	if len(disabled.Artifact.Dependencies) != 0 || len(disabled.Artifact.Files) != 1 || disabled.Artifact.Size != 11 {
		t.Fatalf("unselected files captured: %+v", disabled.Artifact)
	}
	cfg.effective.Values["mmproj"] = selectedPath
	cfg.effective.Values["spec-draft-model"] = "draft-00001-of-00002.gguf"
	enabled := capture()
	deps := enabled.Artifact.Dependencies
	if len(deps) != 2 || deps[0].Kind != "mmproj" || deps[0].Quantization != "F16" || deps[0].Files[0].Path != "selected.gguf" || deps[1].Kind != "draft" || len(deps[1].Files) != 2 {
		t.Fatalf("selected dependencies = %+v", deps)
	}
	if enabled.Artifact.Fingerprint == disabled.Artifact.Fingerprint {
		t.Fatal("selected dependencies did not affect fingerprint")
	}
	if err := os.WriteFile(filepath.Join(base.root, "draft-00002-of-00002.gguf"), []byte("edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if capture().Artifact.Fingerprint == enabled.Artifact.Fingerprint {
		t.Fatal("changed dependency shard did not affect fingerprint")
	}
	cfg.effective.Values["mmproj"] = ""
	delete(cfg.effective.Values, "spec-draft-model")
	if capture().Artifact.Fingerprint != disabled.Artifact.Fingerprint {
		t.Fatal("disabled companions still affected fingerprint")
	}
	cfg.effective.Values["mmproj"] = "missing.gguf"
	if _, err := captureTarget(context.Background(), "instance-1", s.instances, s.models, s.config); !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "--mmproj") {
		t.Fatalf("selected missing dependency error = %v", err)
	}
	cfg.effective.Values["mmproj"] = selectedPath
	incomplete := source.inspections[selectedPath]
	incomplete.Complete = false
	source.inspections[selectedPath] = incomplete
	if _, err := captureTarget(context.Background(), "instance-1", s.instances, s.models, s.config); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("incomplete dependency error = %v", err)
	}
}
