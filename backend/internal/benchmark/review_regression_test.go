package benchmark

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/llamacpp"
	"github.com/brantje/llamarack/backend/internal/scheduler"
)

func TestKVOffloadSemanticAliasWithRealisticSavedConfig(t *testing.T) {
	caps := testCapabilities(
		llamacpp.Option{Key: "batch-size", Kind: "integer"},
		llamacpp.Option{Key: "flash-attn", Kind: "boolean"},
		llamacpp.Option{Key: "no-kv-offload", Kind: "enum", Choices: []string{"0", "1"}},
	)
	for _, tc := range []struct {
		name, value, want string
	}{
		{name: "enabled", value: "true", want: "--no-kv-offload 0"},
		{name: "disabled", value: "false", want: "--no-kv-offload 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := InstanceConfigSnapshot{
				SchemaVersion: ConfigSchemaVersion,
				GPUMode:       "manual",
				GPUDevices:    []string{"CUDA0"},
				Options: map[string]string{
					"ctx-size": "4096", "batch-size": "512", "flash-attn": "true",
					"kv-offload": tc.value, "host": "127.0.0.1", "port": "8080",
				},
				Sources: map[string]string{
					"ctx-size": "instance", "batch-size": "instance", "flash-attn": "instance",
					"kv-offload": "instance", "host": "global", "port": "global",
				},
			}
			mapped, err := MapInstanceConfig(config, scheduler.Placement{}, caps)
			if err != nil {
				t.Fatal(err)
			}
			args := strings.Join(mapped.Args, " ")
			for _, want := range []string{"--batch-size 512", "--flash-attn", tc.want} {
				if !strings.Contains(args, want) {
					t.Fatalf("mapped args %q missing %q", args, want)
				}
			}
			if strings.Contains(args, "--kv-offload") {
				t.Fatalf("server-side kv-offload leaked into llama-bench argv: %q", args)
			}
		})
	}
}

func TestParseMachineOutputRejectsSemanticallyInvalidRows(t *testing.T) {
	workload := DefaultWorkload()
	for _, tc := range []struct {
		name string
		data string
	}{
		{name: "empty object", data: `[{}]`},
		{name: "missing measurement", data: `[{"n_prompt":512}]`},
		{name: "wrong type avg ts", data: `[{"n_prompt":512,"avg_ts":"123.4"}]`},
		{name: "missing identity", data: `[{"avg_ts":123.4}]`},
		{name: "wrong type identity", data: `[{"n_prompt":"512","avg_ts":123.4}]`},
		{name: "negative identity", data: `[{"n_prompt":-1,"avg_ts":123.4}]`},
		{name: "all zero identity", data: `[{"n_prompt":0,"n_gen":0,"avg_ts":123.4}]`},
		{name: "negative measurement", data: `[{"n_gen":128,"avg_ts":-1}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if results, err := ParseMachineOutput([]byte(tc.data), workload); err == nil {
				t.Fatalf("invalid row produced successful results: %+v", results)
			}
		})
	}
}

func TestParseMachineOutputAcceptsZeroMeasurementAndFutureFields(t *testing.T) {
	results, err := ParseMachineOutput([]byte(`[{"n_prompt":512,"n_gen":0,"avg_ns":1000,"stddev_ns":50,"avg_ts":0,"stddev_ts":0,"samples_ts":[0,0],"future_field":{"version":2}}]`), DefaultWorkload())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].CaseID != "pp-512" || results[0].AverageTokensPS != 0 {
		t.Fatalf("zero-valued measurement was not preserved: %+v", results)
	}
	if !strings.Contains(string(results[0].RawFields), "future_field") {
		t.Fatalf("additive upstream field was lost: %s", results[0].RawFields)
	}

	jsonl := []byte("{\"n_prompt\":16,\"n_gen\":0,\"avg_ts\":9.5}\n{\"n_prompt\":0,\"n_gen\":8,\"avg_ts\":4.25}\n")
	results, err = ParseMachineOutput(jsonl, WorkloadProfile{Repetitions: 7})
	if err != nil || len(results) != 2 || results[0].CaseID != "pp-16" || results[1].CaseID != "tg-8" {
		t.Fatalf("valid JSONL results=%+v err=%v", results, err)
	}
}

func TestServiceMarksSemanticallyInvalidMachineOutputFailed(t *testing.T) {
	s, _, ledger, _, _ := testBenchmarkService(t, benchmarkTestExecutor{output: RunOutput{MachineOutput: []byte(`[{}]`)}})
	run, err := s.Create(context.Background(), "instance-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.wg.Wait()
	got, err := s.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusFailed || len(got.Results) != 0 || !strings.Contains(got.Failure, "benchmark parser") {
		t.Fatalf("semantic parser failure completed benchmark: %+v", got)
	}
	waitBenchmarkReservationReleased(t, ledger, run.ID)
}

func TestSQLStorePersistsAcceleratorDriverCompatibilityIdentity(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewSQLStore(db)

	run := testRun(time.Now().UTC())
	run.Hardware.SelectedDevices = []string{"CUDA0"}
	run.Hardware.Observed.GPUs[0].DriverVersion = "590.44.01"
	run.Hardware.Observed.GPUs[0].MaxCUDAVersion = "13.1"
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	gpu := got.Hardware.Observed.GPUs[0]
	if gpu.DriverVersion != "590.44.01" || gpu.MaxCUDAVersion != "13.1" || len(got.Hardware.SelectedDevices) != 1 || got.Hardware.SelectedDevices[0] != "CUDA0" {
		t.Fatalf("immutable accelerator identity did not round-trip: %+v", got.Hardware)
	}

	old := testRun(time.Now().UTC().Add(time.Second))
	old.ID = "old-run-without-driver-fields"
	old.Hardware = HardwareSnapshot{Observed: hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", Name: "Older GPU", TotalBytes: 8 << 30}}}, CPU: old.Hardware.CPU}
	if err := store.CreateRun(ctx, old); err != nil {
		t.Fatal(err)
	}
	legacy, err := store.GetRun(ctx, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Hardware.Observed.GPUs[0].DriverVersion != "" || legacy.Hardware.Observed.GPUs[0].MaxCUDAVersion != "" {
		t.Fatalf("legacy history gained fabricated driver identity: %+v", legacy.Hardware.Observed.GPUs[0])
	}
}
