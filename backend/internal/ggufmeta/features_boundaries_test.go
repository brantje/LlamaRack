package ggufmeta

import "testing"

// Metadata validation must reject malformed or overflowing speculative
// layer counts rather than misclassifying arbitrary GGUFs as draft models.
func TestFeaturesFromInspectionMTPBoundaries(t *testing.T) {
	const key = "qwen35.nextn_predict_layers"
	for _, tc := range []struct {
		name, architecture, key, value string
		wantLayers int64
		wantHasMTP, wantOnly, wantProjector bool
	}{
		{"valid", "qwen35", key, " 2 ", 2, true, false, false},
		{"zero", "qwen35", key, "0", 0, false, false, false},
		{"negative", "qwen35", key, "-2", 0, false, false, false},
		{"non-numeric", "qwen35", key, "unavailable", 0, false, false, false},
		{"overflow", "qwen35", key, "18446744073709551615", 0, false, false, false},
		{"incorrect-architecture-key", "qwen35", "llama.nextn_predict_layers", "2", 0, false, false, false},
		{"projector", "clip", key, "2", 0, false, false, true},
		{"no-architecture", "", key, "2", 0, false, false, false},
		{"standalone-draft", "gemma4-assistant", key, "2", 0, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := FeaturesFromInspection(Inspection{
				Derived: Derived{Architecture: tc.architecture},
				Metadata: []Entry{{Key: tc.key, Value: tc.value}},
			})
			if got.NextNPredictLayers != tc.wantLayers || got.HasMTP != tc.wantHasMTP ||
				got.MTPOnly != tc.wantOnly || got.Projector != tc.wantProjector {
				t.Fatalf("features=%+v, want layers=%d MTP=%v only=%v projector=%v",
					got, tc.wantLayers, tc.wantHasMTP, tc.wantOnly, tc.wantProjector)
			}
		})
	}
	for _, architecture := range []string{" gemma4_assistant ", "GEMMA4-ASSISTANT"} {
		if !IsStandaloneMTPArchitecture(architecture) {
			t.Fatalf("standalone MTP architecture %q not recognized", architecture)
		}
	}
}
