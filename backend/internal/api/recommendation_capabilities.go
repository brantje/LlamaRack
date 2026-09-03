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
	return recommendations.Capabilities{NCPUMoe: profile.Has("n-cpu-moe")}
}
