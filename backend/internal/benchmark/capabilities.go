package benchmark

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/brantje/llamarack/backend/internal/llamacpp"
)

type WorkloadField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Kind        string `json:"kind"`
	Minimum     int    `json:"minimum,omitempty"`
	Maximum     int    `json:"maximum,omitempty"`
	Advanced    bool   `json:"advanced,omitempty"`
	Description string `json:"description,omitempty"`
}
type WorkloadSchema struct {
	Version int               `json:"version"`
	Default WorkloadProfile   `json:"default"`
	Presets []WorkloadProfile `json:"presets"`
	Fields  []WorkloadField   `json:"fields"`
}
type Capabilities struct {
	Available        bool                 `json:"available"`
	Reason           string               `json:"reason,omitempty"`
	Version          string               `json:"version,omitempty"`
	Fingerprint      string               `json:"fingerprint,omitempty"`
	OutputFormat     string               `json:"output_format,omitempty"`
	SupportedOptions []string             `json:"supported_options,omitempty"`
	RuntimeOptions   []RuntimeOptionField `json:"runtime_options,omitempty"`
	Workload         WorkloadSchema       `json:"workload"`
	profile          llamacpp.Profile
}

func DiscoverCapabilities(ctx context.Context, path string) (Capabilities, error) {
	base := Capabilities{Workload: DefaultWorkloadSchema()}
	if strings.TrimSpace(path) == "" {
		base.Reason = "llama-bench path is not configured"
		return base, nil
	}
	profile, err := llamacpp.Discover(ctx, path)
	if err != nil {
		base.Reason = fmt.Sprintf("llama-bench discovery failed: %v", err)
		return base, nil
	}
	base.Version = profile.Version
	base.Fingerprint = profile.Fingerprint
	base.profile = profile
	base.RuntimeOptions = runtimeFieldsForProfile(profile)
	base.SupportedOptions = make([]string, 0, len(profile.Options)+len(profile.ShortOptions))
	for _, option := range profile.Options {
		base.SupportedOptions = append(base.SupportedOptions, option.Key)
	}
	for _, option := range profile.ShortOptions {
		if option == "pg" {
			base.SupportedOptions = append(base.SupportedOptions, option)
		}
	}
	sort.Strings(base.SupportedOptions)
	var missing []string
	for _, key := range []string{"model", "output", "repetitions", "n-prompt", "n-gen"} {
		if !profile.Has(key) {
			missing = append(missing, "--"+key)
		}
	}
	if len(missing) != 0 {
		base.Reason = "llama-bench is missing required options: " + strings.Join(missing, ", ")
		return base, nil
	}
	if output, ok := profileOption(profile, "output"); ok && len(output.Choices) > 0 {
		hasJSON := false
		for _, choice := range output.Choices {
			if strings.EqualFold(strings.TrimSpace(choice), "json") {
				hasJSON = true
				break
			}
		}
		if !hasJSON {
			base.Reason = "llama-bench does not advertise JSON machine-readable output"
			return base, nil
		}
	}
	base.Workload = workloadSchemaForProfile(base.Workload, profile)
	base.Available = true
	base.OutputFormat = "json"
	return base, nil
}

func workloadSchemaForProfile(schema WorkloadSchema, profile llamacpp.Profile) WorkloadSchema {
	supportsDepth := profile.Has("n-depth")
	supportsCombined := profile.HasShort("pg")
	fields := make([]WorkloadField, 0, len(schema.Fields))
	for _, field := range schema.Fields {
		if field.Key == "context_depths" && !supportsDepth {
			continue
		}
		if field.Key == "combined_cases" && !supportsCombined {
			continue
		}
		fields = append(fields, field)
	}
	schema.Fields = fields
	presets := make([]WorkloadProfile, 0, len(schema.Presets))
	for _, preset := range schema.Presets {
		if len(preset.ContextDepths) > 0 && !supportsDepth {
			continue
		}
		if len(preset.CombinedCases) > 0 && !supportsCombined {
			continue
		}
		presets = append(presets, preset)
	}
	schema.Presets = presets
	return schema
}
func (c Capabilities) profileForMapping() llamacpp.Profile { return c.profile }
