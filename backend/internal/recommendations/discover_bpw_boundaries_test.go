package recommendations

import "testing"

// Artifact quality ordering and memory warnings depend on the actual bits per
// weight, including boundaries used by ultra-low-bit IQ quantizations.
func TestDiscoverBitsPerWeightQualityAndMemoryBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		bpw float64
		label string
		rank int
	}{
		{"very-low", 2.5, "Very low", 25},
		{"low-start", 3.0, "Low", 35},
		{"low-end", 3.99, "Low", 35},
		{"moderate", 4.0, "Moderate", 45},
		{"moderate-high", 5.0, "Moderate-high", 55},
		{"moderate-high-mid", 6.0, "Moderate-high", 65},
		{"very-high", 7.0, "Very high", 80},
		{"very-high-rank", 9.0, "Very high", 90},
		{"extreme", 10.0, "Extreme", 90},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := bpwMemoryLabel(tc.bpw); got != tc.label {
				t.Fatalf("bits per weight %.2f: memory label %q, want %q", tc.bpw, got, tc.label)
			}
			if got := bpwRank(tc.bpw); got != tc.rank {
				t.Fatalf("bits per weight %.2f: quality rank %d, want %d", tc.bpw, got, tc.rank)
			}
		})
	}
}
