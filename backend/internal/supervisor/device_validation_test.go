package supervisor

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLaunchDeviceIDs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		env  []string
		want []string
	}{
		{name: "none", args: []string{"--ctx-size", "1024"}, want: nil},
		{name: "multi", args: []string{"--device", "CUDA0,CUDA1"}, want: []string{"CUDA0", "CUDA1"}},
		{name: "equals", args: []string{"--device=Vulkan0"}, want: []string{"Vulkan0"}},
		{name: "isolated cuda", args: []string{"--device", "CUDA0"}, env: []string{"CUDA_VISIBLE_DEVICES=3"}, want: []string{"CUDA3"}},
		{name: "isolated rocm", args: []string{"--device", "ROCm0"}, env: []string{"HIP_VISIBLE_DEVICES=2", "ROCR_VISIBLE_DEVICES=2"}, want: []string{"ROCm2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := launchDeviceIDs(tc.args, tc.env); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("launchDeviceIDs()=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestStartWithAliasEnvValidatesDeviceBeforeSpawn(t *testing.T) {
	s := New("/definitely/missing/llama-server", "127.0.0.1", 39000, time.Second)
	var got []string
	s.SetDeviceValidator(func(devices []string) error {
		got = append([]string(nil), devices...)
		return errors.New("unsupported runtime device")
	})
	_, err := s.StartWithAliasEnv(
		context.Background(), "instance", "alias", "model", "/tmp/model.gguf",
		[]string{"--device", "CUDA0", "--split-mode", "none"},
		[]string{"CUDA_VISIBLE_DEVICES=3"}, "",
	)
	if err == nil || !strings.Contains(err.Error(), "unsupported runtime device") {
		t.Fatalf("expected validator error, got %v", err)
	}
	if !reflect.DeepEqual(got, []string{"CUDA3"}) {
		t.Fatalf("validated devices=%v", got)
	}
	if state := s.Status("instance").State; state != Unloaded {
		t.Fatalf("validator failure must not create a worker, state=%s", state)
	}
}
