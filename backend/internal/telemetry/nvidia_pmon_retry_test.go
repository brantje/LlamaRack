package telemetry

import (
	"context"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/systemlog"
)

func TestNVIDIAProcessUtilizationRetriesPlainPMon(t *testing.T) {
	systemlog.Default.Reset()
	defer systemlog.Default.Reset()
	collector := New(nil)
	calls := make([]string, 0, 2)
	collector.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if len(calls) == 1 {
			return []byte("# gpu pid type sm mem enc dec command\n"), nil
		}
		return []byte("# gpu pid type sm mem enc dec jpg ofa command\n0 2708472 C 97 100 - - - - llama-server\n"), nil
	}

	got := collector.nvidiaProcessUtilization(context.Background())
	if len(calls) != 2 {
		t.Fatalf("calls=%v", calls)
	}
	if calls[0] != "nvidia-smi pmon -c 1 -s u" || calls[1] != "nvidia-smi pmon -c 1" {
		t.Fatalf("unexpected pmon attempts: %v", calls)
	}
	if got[gpuProcessKey{pid: 2708472, deviceID: "CUDA0"}] != 97 {
		t.Fatalf("plain pmon result=%+v", got)
	}
	logs := systemlog.Default.Snapshot(10)
	if len(logs) != 1 || logs[0].Level != systemlog.Debug || logs[0].Source != "telemetry" || logs[0].Message != "nvidia-smi pmon -s u returned no process rows, retrying plain pmon" {
		t.Fatalf("diagnostics=%+v", logs)
	}
}

func TestParseNVIDIAPMonAcceptsDefaultOutput(t *testing.T) {
	got := parseNVIDIAPMon([]byte("# gpu pid type sm mem enc dec jpg ofa command\n0 2708472 C 96 100 - - - - llama-server\n1 - - - - - - - - -\n"))
	if len(got) != 1 || got[gpuProcessKey{pid: 2708472, deviceID: "CUDA0"}] != 96 {
		t.Fatalf("parsed=%+v", got)
	}
}
