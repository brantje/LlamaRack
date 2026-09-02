package lifecycle

import (
	"context"
	"strconv"
	"strings"

	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/models"
	"github.com/brantje/llamarack/backend/internal/recommendations"
)

type moeLaunchPlan struct {
	Options     map[string]string
	Devices     []string
	TensorSplit string
	Applied     bool
}

func (s *Service) prepareAutoMoELaunch(ctx context.Context, i instances.Instance, m models.Model, path string, launchOptions, effectiveOptions map[string]string) moeLaunchPlan {
	plan := moeLaunchPlan{Options: launchOptions}
	if !strings.EqualFold(strings.TrimSpace(i.GPUMode), "auto") || s.profile == nil || s.hardware == nil {
		return plan
	}
	profile, err := s.profile()
	if err != nil || !profile.Has("n-cpu-moe") {
		return plan
	}
	snapshot, hardwareErr := s.hardware.Snapshot(ctx)
	if hardwareErr != nil || len(snapshot.GPUs) == 0 {
		return plan
	}
	contextLength := optionInt64(effectiveOptions, "ctx-size")
	recommendation := recommendations.AnalyzeWithCapabilities(
		m,
		path,
		snapshot,
		contextLength,
		hardwareErr,
		recommendations.Capabilities{NCPUMoe: true},
	)
	if recommendation.Offload.Mode != "moe" || len(recommendation.Offload.Devices) == 0 {
		return plan
	}

	options := cloneOptions(launchOptions)
	if !hasAnyOption(effectiveOptions, "gpu-layers", "n-gpu-layers") && recommendation.Offload.GPULayers > 0 {
		options["n-gpu-layers"] = strconv.FormatInt(recommendation.Offload.GPULayers, 10)
	}
	if !hasAnyOption(effectiveOptions, "cpu-moe", "n-cpu-moe") && recommendation.Offload.NCPUMoe > 0 {
		if recommendation.Metadata.BlockCount > 0 && recommendation.Offload.NCPUMoe >= recommendation.Metadata.BlockCount && profile.Has("cpu-moe") {
			options["cpu-moe"] = "true"
		} else {
			options["n-cpu-moe"] = strconv.FormatInt(recommendation.Offload.NCPUMoe, 10)
		}
	}
	if !recommendation.Offload.KVOnGPU && !hasAnyOption(effectiveOptions, "no-kv-offload", "kv-offload") {
		options["no-kv-offload"] = "true"
	}
	return moeLaunchPlan{
		Options:     options,
		Devices:     append([]string(nil), recommendation.Offload.Devices...),
		TensorSplit: recommendation.Offload.TensorSplit,
		Applied:     true,
	}
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
