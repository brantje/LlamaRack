package lifecycle

import (
	"strconv"
	"strings"

	"github.com/brantje/llamarack/backend/internal/llamacpp"
)

// applyCPUMoeLoadMode injects llama.cpp --load-mode none (or --no-mmap on older
// binaries) when CPU expert offload is active, unless the user already chose a
// load/mmap path. mmap plus CPU tensor overrides is the slow path llama.cpp warns about.
func applyCPUMoeLoadMode(options map[string]string, profile llamacpp.Profile) map[string]string {
	if !cpuMoeOffloadActive(options) || hasAnyOption(options, "load-mode", "mmap", "no-mmap") {
		return options
	}
	if loadMode, ok := profileOption(profile, "load-mode"); ok && loadModeAllowsNone(loadMode) {
		out := cloneOptions(options)
		out["load-mode"] = "none"
		return out
	}
	if profile.Has("no-mmap") {
		out := cloneOptions(options)
		out["no-mmap"] = "true"
		return out
	}
	return options
}

func cpuMoeOffloadActive(options map[string]string) bool {
	if optionFlagEnabled(options, "cpu-moe") {
		return true
	}
	return optionInt64(options, "n-cpu-moe") > 0
}

func optionFlagEnabled(options map[string]string, key string) bool {
	switch strings.ToLower(strings.TrimSpace(optionRaw(options, key))) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

func optionRaw(options map[string]string, key string) string {
	if options == nil {
		return ""
	}
	if value, ok := options[key]; ok {
		return value
	}
	return options["--"+strings.TrimLeft(key, "-")]
}

func profileOption(profile llamacpp.Profile, key string) (llamacpp.Option, bool) {
	key = strings.TrimPrefix(strings.TrimSpace(key), "--")
	if key == "" {
		return llamacpp.Option{}, false
	}
	for _, option := range profile.Options {
		if strings.TrimPrefix(strings.TrimSpace(option.Key), "--") == key {
			return option, true
		}
	}
	return llamacpp.Option{}, false
}

func loadModeAllowsNone(option llamacpp.Option) bool {
	if len(option.Choices) == 0 {
		return true
	}
	for _, choice := range option.Choices {
		if strings.EqualFold(strings.TrimSpace(choice), "none") {
			return true
		}
	}
	return false
}

func cloneOptions(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+4)
	for key, value := range in {
		out[key] = value
	}
	return out
}

func hasAnyOption(options map[string]string, keys ...string) bool {
	for _, key := range keys {
		if _, ok := options[key]; ok {
			return true
		}
		if _, ok := options["--"+strings.TrimLeft(key, "-")]; ok {
			return true
		}
	}
	return false
}

func optionInt64(options map[string]string, key string) int64 {
	if options == nil {
		return 0
	}
	value, ok := options[key]
	if !ok {
		value = options["--"+strings.TrimLeft(key, "-")]
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}
