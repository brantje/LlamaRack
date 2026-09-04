package buildinfo

import (
	"runtime/debug"
	"strings"
)

var (
	version         string
	commit          string
	buildTime       string
	channel         string
	variant         string
	llamaCppRelease string
	llamaCppBuild   string
	llamaCppImage   string
)

// RuntimeIdentity identifies the immutable llama.cpp runtime bundled into a
// LlamaRack artifact.
type RuntimeIdentity struct {
	Release string `json:"release,omitempty"`
	Build   string `json:"build,omitempty"`
	Image   string `json:"image,omitempty"`
}

// Identity is the canonical, read-only build identity exposed to operators.
type Identity struct {
	Version   string          `json:"version"`
	Commit    string          `json:"commit,omitempty"`
	BuildTime string          `json:"build_time,omitempty"`
	Channel   string          `json:"channel"`
	Variant   string          `json:"variant"`
	Dirty     bool            `json:"dirty,omitempty"`
	LlamaCpp  RuntimeIdentity `json:"llama_cpp"`
}

type injectedMetadata struct {
	version         string
	commit          string
	buildTime       string
	channel         string
	variant         string
	llamaCppRelease string
	llamaCppBuild   string
	llamaCppImage   string
}

// Current returns the canonical identity for the running binary. Official
// builds inject release metadata with -ldflags; source builds fall back to the
// VCS information embedded by the Go toolchain.
func Current() Identity {
	info, _ := debug.ReadBuildInfo()
	return resolve(injectedMetadata{
		version:         version,
		commit:          commit,
		buildTime:       buildTime,
		channel:         channel,
		variant:         variant,
		llamaCppRelease: llamaCppRelease,
		llamaCppBuild:   llamaCppBuild,
		llamaCppImage:   llamaCppImage,
	}, info)
}

func resolve(in injectedMetadata, info *debug.BuildInfo) Identity {
	settings := buildSettings(info)

	resolvedVersion := normalizeVersion(in.version)
	if resolvedVersion == "" && info != nil {
		resolvedVersion = normalizeVersion(info.Main.Version)
	}
	if resolvedVersion == "" || resolvedVersion == "(devel)" {
		resolvedVersion = "development"
	}

	resolvedCommit := strings.TrimSpace(in.commit)
	if resolvedCommit == "" {
		resolvedCommit = strings.TrimSpace(settings["vcs.revision"])
	}

	resolvedChannel := strings.ToLower(strings.TrimSpace(in.channel))
	if resolvedChannel == "" {
		if resolvedVersion == "development" {
			resolvedChannel = "development"
		} else {
			resolvedChannel = "custom"
		}
	}

	resolvedVariant := strings.ToLower(strings.TrimSpace(in.variant))
	if resolvedVariant == "" {
		resolvedVariant = "unknown"
	}

	return Identity{
		Version:   resolvedVersion,
		Commit:    resolvedCommit,
		BuildTime: strings.TrimSpace(in.buildTime),
		Channel:   resolvedChannel,
		Variant:   resolvedVariant,
		Dirty:     strings.EqualFold(strings.TrimSpace(settings["vcs.modified"]), "true"),
		LlamaCpp: RuntimeIdentity{
			Release: strings.TrimSpace(in.llamaCppRelease),
			Build:   strings.TrimSpace(in.llamaCppBuild),
			Image:   strings.TrimSpace(in.llamaCppImage),
		},
	}
}

func buildSettings(info *debug.BuildInfo) map[string]string {
	settings := make(map[string]string)
	if info == nil {
		return settings
	}
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	return settings
}

func normalizeVersion(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 1 && value[0] == 'v' && value[1] >= '0' && value[1] <= '9' {
		return value[1:]
	}
	return value
}
