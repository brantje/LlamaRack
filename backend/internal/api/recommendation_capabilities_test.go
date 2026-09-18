package api

import (
	"errors"
	"testing"

	"github.com/brantje/llamarack/backend/internal/llamacpp"
	"github.com/brantje/llamarack/backend/internal/recommendations"
)

func TestRecommendationCapabilities(t *testing.T) {
	if got := recommendationCapabilities(nil); got.NCPUMoe {
		t.Fatal("nil profile getter must not advertise n-cpu-moe")
	}
	if got := recommendationCapabilities(func() (llamacpp.Profile, error) {
		return llamacpp.Profile{}, errors.New("profile unavailable")
	}); got.NCPUMoe {
		t.Fatal("failed profile lookup must not advertise n-cpu-moe")
	}
	if got := recommendationCapabilities(func() (llamacpp.Profile, error) {
		return llamacpp.Profile{Options: []llamacpp.Option{{Key: "threads"}}}, nil
	}); got.NCPUMoe {
		t.Fatal("profile without n-cpu-moe must not advertise it")
	}
	if got := recommendationCapabilities(func() (llamacpp.Profile, error) {
		return llamacpp.Profile{Options: []llamacpp.Option{
			{Key: "n-cpu-moe"}, {Key: "cpu-moe"}, {Key: "n-gpu-layers"}, {Key: "no-kv-offload"},
		}}, nil
	}); !got.NCPUMoe || !got.CPUMoe || !got.GPULayers || !got.NoKVOffload || got.GPULayersOption != "n-gpu-layers" {
		t.Fatalf("runtime capabilities not advertised: %+v", got)
	}
	if got := recommendationCapabilitiesFromProfile(llamacpp.Profile{Options: []llamacpp.Option{{Key: "gpu-layers"}}}); !got.GPULayers || got.GPULayersOption != "gpu-layers" {
		t.Fatalf("gpu-layers alias capability=%+v", got)
	}
	if got := recommendationCapabilitiesFromProfile(llamacpp.Profile{}); got != (recommendations.Capabilities{}) {
		t.Fatalf("empty profile capabilities=%+v", got)
	}
}
