package lifecycle

import (
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
)

func TestRuntimeEndpointUnavailableForStoppedInstance(t *testing.T) {
	service := &Service{sup: supervisor.New("llama-server", "127.0.0.1", 12000, time.Second)}
	if endpoint, ok := service.RuntimeEndpoint("stopped"); ok || endpoint != "" {
		t.Fatalf("endpoint=%q ok=%v", endpoint, ok)
	}
}
