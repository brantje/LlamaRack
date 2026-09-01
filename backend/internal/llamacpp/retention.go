package llamacpp

import (
	"fmt"
	"strings"
)

// ValidateOptionsRetaining validates supported options normally while allowing
// an already persisted option that disappeared from a newer llama-server help
// schema to remain unchanged. New or changed unsupported options are rejected.
func ValidateOptionsRetaining(profile Profile, options, existing map[string]string) (map[string]string, error) {
	if options == nil {
		return nil, nil
	}
	supported := make(map[string]bool, len(profile.Options))
	for _, option := range profile.Options {
		supported[option.Key] = true
	}
	out := make(map[string]string, len(options))
	for rawKey, rawValue := range options {
		key := strings.TrimSpace(strings.TrimLeft(rawKey, "-"))
		if key == "" {
			return nil, fmt.Errorf("llama.cpp option key is required")
		}
		if managerOwnedOptions[key] {
			return nil, fmt.Errorf("llama.cpp option %q is managed by LlamaRack and cannot be overridden here", key)
		}
		if supported[key] {
			validated, err := ValidateOptions(profile, map[string]string{key: rawValue})
			if err != nil {
				return nil, err
			}
			out[key] = validated[key]
			continue
		}
		if previous, ok := existing[key]; ok && previous == rawValue {
			out[key] = rawValue
			continue
		}
		return nil, fmt.Errorf("unsupported llama.cpp option %q for %s", key, profileLabel(profile))
	}
	return out, nil
}

func IsManagerOwnedOption(key string) bool {
	key = strings.TrimSpace(strings.TrimLeft(key, "-"))
	return managerOwnedOptions[key]
}
