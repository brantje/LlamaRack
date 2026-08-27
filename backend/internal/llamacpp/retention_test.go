package llamacpp

import (
	"strings"
	"testing"
)

func TestValidateOptionsRetainingKeepsUnchangedUnsupportedValue(t *testing.T) {
	profile := testProfile()
	got, err := ValidateOptionsRetaining(profile, map[string]string{
		"ctx-size": "8192",
		"removed":  "legacy",
	}, map[string]string{"removed": "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	if got["ctx-size"] != "8192" || got["removed"] != "legacy" {
		t.Fatalf("unexpected retained options: %+v", got)
	}
}

func TestValidateOptionsRetainingRejectsChangedUnsupportedAndManagerOwned(t *testing.T) {
	profile := testProfile()
	if _, err := ValidateOptionsRetaining(profile, map[string]string{"removed": "changed"}, map[string]string{"removed": "legacy"}); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("changed unsupported option should fail: %v", err)
	}
	if _, err := ValidateOptionsRetaining(profile, map[string]string{"device": "CUDA0"}, map[string]string{}); err == nil || !strings.Contains(err.Error(), "managed by LlamaCPP Manager") {
		t.Fatalf("manager-owned option should fail: %v", err)
	}
	if _, err := ValidateOptionsRetaining(profile, map[string]string{"---": "x"}, map[string]string{}); err == nil || !strings.Contains(err.Error(), "key is required") {
		t.Fatalf("empty canonical key should fail: %v", err)
	}
}

func TestValidateOptionsRetainingNilAndManagerOwnedHelper(t *testing.T) {
	got, err := ValidateOptionsRetaining(testProfile(), nil, nil)
	if err != nil || got != nil {
		t.Fatalf("nil input: got=%v err=%v", got, err)
	}
	if IsManagerOwnedOption("--tensor-split") || !IsManagerOwnedOption("main-gpu") || IsManagerOwnedOption("ctx-size") {
		t.Fatal("unexpected manager-owned classification")
	}
}
