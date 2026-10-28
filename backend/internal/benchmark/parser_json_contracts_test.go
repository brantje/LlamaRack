package benchmark

import (
	"encoding/json"
	"testing"
)

// Benchmark output spans different llama.cpp versions that encode counters as
// integers, floating numbers, numeric strings, or omitted optional fields.
// Parsing must preserve useful numeric data without failing closed on extras.
func TestBenchmarkNumericFieldEncodingCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw string
		wantInt int64
		wantFloat float64
	}{
		{"integer", "123", 123, 123},
		{"negative", "-5", -5, -5},
		{"floating", "12.5", 12, 12.5},
		{"numeric-string", `"42"`, 42, 42},
		{"floating-string", `"3.75"`, 3, 3.75},
		{"invalid-string", `"unavailable"`, 0, 0},
		{"boolean", "true", 0, 0},
		{"null", "null", 0, 0},
		{"missing", "", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := map[string]json.RawMessage{}
			if tc.raw != "" {
				row["value"] = json.RawMessage(tc.raw)
			}
			if got := jsonInt64(row, "value"); got != tc.wantInt {
				t.Fatalf("int64(%s) = %d, want %d", tc.raw, got, tc.wantInt)
			}
			if got := jsonFloat64(row, "value"); got != tc.wantFloat {
				t.Fatalf("float64(%s) = %g, want %g", tc.raw, got, tc.wantFloat)
			}
		})
	}
}
