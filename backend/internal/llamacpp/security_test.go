package llamacpp

import (
	"strings"
	"testing"
)

func TestValidateOptionsRejectsManagerOwnedWorkerSecurityOptions(t *testing.T) {
	profile := Profile{Options: []Option{{Key: "ctx-size", ValueHint: "N", Kind: "integer"}}}
	for _, key := range []string{
		"cors-origins",
		"cors-methods",
		"cors-headers",
		"cors-credentials",
		"no-cors-credentials",
		"api-key",
		"api-key-file",
	} {
		t.Run(key, func(t *testing.T) {
			_, err := ValidateOptions(profile, map[string]string{key: "unsafe"})
			if err == nil || !strings.Contains(err.Error(), "managed by LlamaRack") {
				t.Fatalf("ValidateOptions(%q) error = %v", key, err)
			}
		})
	}
}
