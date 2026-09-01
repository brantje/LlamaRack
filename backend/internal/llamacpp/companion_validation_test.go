package llamacpp

import (
	"strings"
	"testing"
)

func TestValidateOptionsAllowsEmptyCompanionTombstonesOnly(t *testing.T) {
	profile := Profile{Version: "test", Options: []Option{
		{Key: "mmproj", Kind: "string", ValueHint: "FNAME"},
		{Key: "spec-draft-model", Kind: "string", ValueHint: "FNAME"},
		{Key: "threads", Kind: "integer", ValueHint: "N"},
	}}
	validated, err := ValidateOptions(profile, map[string]string{"mmproj": "", "spec-draft-model": ""})
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := validated["mmproj"]; !ok || value != "" {
		t.Fatalf("projector tombstone missing: %+v", validated)
	}
	if value, ok := validated["spec-draft-model"]; !ok || value != "" {
		t.Fatalf("draft-model tombstone missing: %+v", validated)
	}
	if _, err := ValidateOptions(profile, map[string]string{"threads": ""}); err == nil || !strings.Contains(err.Error(), "requires an integer") {
		t.Fatalf("ordinary empty option unexpectedly accepted: %v", err)
	}
}
