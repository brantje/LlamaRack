package api

import (
	"github.com/brantje/llamarack/backend/internal/llamacpp"
	"github.com/brantje/llamarack/backend/internal/recommendations"
)

func recommendationCapabilities(getter func() (llamacpp.Profile, error)) recommendations.Capabilities {
	if getter == nil {
		return recommendations.Capabilities{}
	}
	profile, err := getter()
	if err != nil {
		return recommendations.Capabilities{}
	}
	return recommendationCapabilitiesFromProfile(profile)
}

func recommendationCapabilitiesFromProfile(profile llamacpp.Profile) recommendations.Capabilities {
	return recommendations.Capabilities{
		NCPUMoe:         profile.Has("n-cpu-moe"),
		CPUMoe:          profile.Has("cpu-moe"),
		NoKVOffload:     profile.Has("no-kv-offload"),
		GPULayers:       profile.Has("n-gpu-layers") || profile.Has("gpu-layers"),
		GPULayersOption: gpuLayerOptionFromProfile(profile),
	}
}

func gpuLayerOptionFromProfile(profile llamacpp.Profile) string {
	if profile.Has("n-gpu-layers") {
		return "n-gpu-layers"
	}
	if profile.Has("gpu-layers") {
		return "gpu-layers"
	}
	return ""
}
