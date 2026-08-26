package llamacpp

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var managerOwnedOptions = map[string]bool{
	"model":        true,
	"host":         true,
	"port":         true,
	"device":       true,
	"tensor-split": true,
}

// ValidateOptions validates and canonicalizes free-form llama.cpp overrides
// against the option schema discovered from the configured llama-server binary.
func ValidateOptions(profile Profile, options map[string]string) (map[string]string, error) {
	if len(options) == 0 {
		return map[string]string{}, nil
	}
	if len(profile.Options) == 0 {
		return nil, errors.New("llama-server option schema is unavailable")
	}

	available := make(map[string]Option, len(profile.Options))
	for _, option := range profile.Options {
		available[option.Key] = option
	}

	out := make(map[string]string, len(options))
	for rawKey, rawValue := range options {
		key := strings.TrimSpace(strings.TrimLeft(rawKey, "-"))
		if key == "" {
			return nil, errors.New("llama.cpp option key is required")
		}
		if managerOwnedOptions[key] {
			return nil, fmt.Errorf("llama.cpp option %q is managed by LlamaCPP Manager and cannot be overridden here", key)
		}
		option, ok := available[key]
		if !ok {
			return nil, fmt.Errorf("unsupported llama.cpp option %q for %s", key, profileLabel(profile))
		}
		value := strings.TrimSpace(rawValue)
		if err := validateOptionValue(option, value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, nil
}

func validateOptionValue(option Option, value string) error {
	kind := option.Kind
	if kind == "" {
		kind, _ = classifyValueHint(option.ValueHint)
	}
	switch kind {
	case "boolean":
		if value != "true" && value != "false" {
			return fmt.Errorf("llama.cpp option %q must be true or false", option.Key)
		}
	case "integer":
		if value == "" {
			return fmt.Errorf("llama.cpp option %q requires an integer value", option.Key)
		}
		if _, err := strconv.ParseInt(value, 10, 64); err != nil {
			return fmt.Errorf("llama.cpp option %q requires an integer value", option.Key)
		}
	case "number":
		if value == "" {
			return fmt.Errorf("llama.cpp option %q requires a numeric value", option.Key)
		}
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			return fmt.Errorf("llama.cpp option %q requires a numeric value", option.Key)
		}
	case "enum":
		for _, choice := range option.Choices {
			if value == choice {
				return nil
			}
		}
		return fmt.Errorf("llama.cpp option %q must be one of: %s", option.Key, strings.Join(option.Choices, ", "))
	default:
		if value == "" {
			hint := strings.TrimSpace(option.ValueHint)
			if hint == "" {
				hint = "a value"
			}
			return fmt.Errorf("llama.cpp option %q requires %s", option.Key, hint)
		}
	}
	return nil
}

func profileLabel(profile Profile) string {
	if strings.TrimSpace(profile.Version) != "" {
		return profile.Version
	}
	if strings.TrimSpace(profile.Path) != "" {
		return profile.Path
	}
	return "the configured llama-server"
}
