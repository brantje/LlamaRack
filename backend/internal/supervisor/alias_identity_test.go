package supervisor

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/resourceid"
)

func TestStartWithEnvSeparatesDurableInstanceIDFromPublicAlias(t *testing.T) {
	binary, argsFile := secureArgsServerScript(t)
	s := New(binary, "127.0.0.1", 29100, 5*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	const instanceID = "8c821aec-1f0d-4b8d-a332-41c582dd2c58"
	resourceid.RememberInstanceSlug(instanceID, "qwen-coder-32b")
	defer resourceid.ForgetInstanceSlug(instanceID)

	rt, err := s.StartWithEnv(ctx, instanceID, "model-1", "/tmp/model.gguf", nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop(context.Background(), instanceID) }()
	if rt.InstanceID != instanceID {
		t.Fatalf("runtime instance id=%q want durable id %q", rt.InstanceID, instanceID)
	}

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "--alias\nqwen-coder-32b\n") {
		t.Fatalf("worker did not receive public slug alias: %q", raw)
	}
	if strings.Contains(text, "--alias\n"+instanceID+"\n") {
		t.Fatalf("durable instance id leaked into worker alias: %q", raw)
	}
}
