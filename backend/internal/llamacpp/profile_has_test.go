package llamacpp

import "testing"

func TestProfileHasNormalizesFlagPrefix(t *testing.T) {
	profile := Profile{Options: []Option{{Key: "n-cpu-moe"}, {Key: "--tensor-split"}}}
	for _, key := range []string{"n-cpu-moe", "--n-cpu-moe", "tensor-split", "--tensor-split"} {
		if !profile.Has(key) {
			t.Fatalf("expected profile to contain %q", key)
		}
	}
	if profile.Has("") || profile.Has("cpu-moe") {
		t.Fatal("unexpected capability match")
	}
}
