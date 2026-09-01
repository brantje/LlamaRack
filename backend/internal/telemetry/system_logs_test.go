package telemetry

import (
	"context"
	"errors"
	"testing"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/supervisor"
	"github.com/brantje/llamarack/backend/internal/systemlog"
)

func TestCollectLogsHostToContainerPIDMapping(t *testing.T) {
	systemlog.Default.Reset()
	defer systemlog.Default.Reset()
	collector := New(func(context.Context) (hardware.Snapshot, error) {
		return hardware.Snapshot{Processes: []hardware.GPUProcess{{PID: 2554129, DeviceID: "CUDA0", UsedBytes: 1}}}, nil
	})
	collector.hostProcRoot = "/host/proc"
	collector.run = func(_ context.Context, name string, _ ...string) ([]byte, error) {
		if name == "nvidia-smi" {
			return []byte("# gpu pid type sm mem enc dec command\n0 2554129 C 97 1 - - llama-server\n"), nil
		}
		return nil, errors.New("not installed")
	}
	collector.readFile = func(path string) ([]byte, error) {
		if path == "/host/proc/2554129/status" {
			return []byte("Name:\tllama-server\nNSpid:\t2554129\t1652\n"), nil
		}
		return nil, errors.New("missing")
	}

	collector.Collect(context.Background(), []supervisor.Runtime{{InstanceID: "llama-70b-long", State: supervisor.Ready, PID: 1652}})
	logs := systemlog.Default.Snapshot(20)
	found := false
	for _, entry := range logs {
		if entry.Level == systemlog.Debug && entry.Source == "telemetry" && entry.Message == "NSpid map: host 2554129 -> container 1652 (CUDA0)" {
			found = true
		}
	}
	if !found {
		t.Fatalf("mapping diagnostic missing: %+v", logs)
	}
}
