package api

import (
	"errors"
	"testing"

	"github.com/brantje/llamarack/backend/internal/llamacpp"
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
		return llamacpp.Profile{Options: []llamacpp.Option{{Key: "n-cpu-moe"}}}, nil
	}); !got.NCPUMoe {
		t.Fatal("profile containing n-cpu-moe must advertise the capability")
	}
}
