package ggufmeta

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectFeaturesProjectorNativeMTPAndMTPOnly(t *testing.T) {
	projector := writeFeatureGGUF(t, "clip", 0, "v.blk.0.attn.weight")
	features, err := DetectFeatures(projector)
	if err != nil || !features.Projector || features.HasMTP || features.MTPOnly {
		t.Fatalf("projector=%+v err=%v", features, err)
	}

	native := writeFeatureGGUF(t, "qwen35", 1, "blk.0.attn_norm.weight", "blk.40.nextn.eh_proj.weight")
	features, err = DetectFeatures(native)
	if err != nil || !features.HasMTP || features.MTPOnly || features.NextNPredictLayers != 1 {
		t.Fatalf("native MTP=%+v err=%v", features, err)
	}
	inspection, err := Inspect(native)
	if err != nil || inspection.Features != features {
		t.Fatalf("inspect native features=%+v err=%v", inspection.Features, err)
	}
	summary, err := ReadSummary(native)
	if err != nil || summary.Features != features {
		t.Fatalf("summary native features=%+v err=%v", summary.Features, err)
	}

	draft := writeFeatureGGUF(t, "qwen35", 1, "token_embd.weight", "blk.40.nextn.eh_proj.weight")
	features, err = DetectFeatures(draft)
	if err != nil || !features.HasMTP || !features.MTPOnly {
		t.Fatalf("MTP-only=%+v err=%v", features, err)
	}
	inspection, err = Inspect(draft)
	if err != nil || inspection.Features != features {
		t.Fatalf("inspect draft features=%+v err=%v", inspection.Features, err)
	}
}

func TestFeaturesFromInspectionRejectsInvalidNextN(t *testing.T) {
	inspection := Inspection{Derived: Derived{Architecture: "qwen35"}, Metadata: []Entry{{Key: "qwen35.nextn_predict_layers", Value: "bad"}}}
	if features := FeaturesFromInspection(inspection); features.HasMTP || features.NextNPredictLayers != 0 {
		t.Fatalf("features=%+v", features)
	}
}

func writeFeatureGGUF(t *testing.T, architecture string, nextN uint32, tensors ...string) string {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("GGUF")
	featureWrite(t, &b, uint32(3))
	featureWrite(t, &b, uint64(len(tensors)))
	metadataCount := uint64(1)
	if nextN > 0 {
		metadataCount++
	}
	featureWrite(t, &b, metadataCount)
	featureString(t, &b, "general.architecture")
	featureWrite(t, &b, uint32(8))
	featureString(t, &b, architecture)
	if nextN > 0 {
		featureString(t, &b, architecture+".nextn_predict_layers")
		featureWrite(t, &b, uint32(4))
		featureWrite(t, &b, nextN)
	}
	for _, name := range tensors {
		featureString(t, &b, name)
		featureWrite(t, &b, uint32(1))
		featureWrite(t, &b, uint64(1))
		featureWrite(t, &b, uint32(0))
		featureWrite(t, &b, uint64(0))
	}
	path := filepath.Join(t.TempDir(), "features.gguf")
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func featureString(t *testing.T, b *bytes.Buffer, value string) {
	t.Helper()
	featureWrite(t, b, uint64(len(value)))
	_, _ = b.WriteString(value)
}

func featureWrite(t *testing.T, b *bytes.Buffer, value any) {
	t.Helper()
	if err := binary.Write(b, binary.LittleEndian, value); err != nil {
		t.Fatal(err)
	}
}
