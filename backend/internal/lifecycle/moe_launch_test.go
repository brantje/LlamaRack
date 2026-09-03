package lifecycle

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/llamacpp"
	"github.com/brantje/llamarack/backend/internal/models"
)

type moeStaticHardware struct{ snapshot hardware.Snapshot }

func (m moeStaticHardware) Snapshot(context.Context) (hardware.Snapshot, error) {
	return m.snapshot, nil
}

func TestPrepareAutoMoELaunchInjectsFlagsBeforeDemand(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	path := writeLifecycleMoEGGUF(t, 40, 64)
	s := &Service{
		hardware: moeStaticHardware{snapshot: hardware.Snapshot{
			RAMAvailableBytes: 64 * gib,
			GPUs:              []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * gib}, {ID: "CUDA1", FreeBytes: 8 * gib}},
		}},
		profile: func() (llamacpp.Profile, error) {
			return llamacpp.Profile{Options: []llamacpp.Option{{Key: "n-cpu-moe"}, {Key: "cpu-moe"}, {Key: "n-gpu-layers"}, {Key: "no-kv-offload"}}}, nil
		},
	}
	model := models.Model{ID: "moe", TotalBytes: 20 * gib, ContextLength: 32768}
	instance := instances.Instance{ID: "moe", ModelID: model.ID, GPUMode: "auto"}
	plan := s.prepareAutoMoELaunch(context.Background(), instance, model, path, map[string]string{"ctx-size": "4096"}, map[string]string{"ctx-size": "4096"})
	if !plan.Applied || len(plan.Devices) != 2 {
		t.Fatalf("plan=%+v", plan)
	}
	if plan.Options["n-gpu-layers"] != "40" {
		t.Fatalf("n-gpu-layers=%q want 40", plan.Options["n-gpu-layers"])
	}
	if plan.Options["n-cpu-moe"] == "" && plan.Options["cpu-moe"] != "true" {
		t.Fatalf("expected expert spill flag, options=%v", plan.Options)
	}

	fullDemand := s.estimateDemand(model, path, map[string]string{"ctx-size": "4096"})
	moeDemand := s.estimateDemand(model, path, plan.Options)
	if moeDemand.VRAMBytes() >= fullDemand.VRAMBytes() {
		t.Fatalf("MoE launch options did not reduce reservation: full=%d moe=%d", fullDemand.VRAMBytes(), moeDemand.VRAMBytes())
	}
	if moeDemand.HostRAMBytes <= fullDemand.HostRAMBytes {
		t.Fatalf("MoE launch options did not reserve expert RAM: full=%d moe=%d", fullDemand.HostRAMBytes, moeDemand.HostRAMBytes)
	}
}

func TestPrepareAutoMoELaunchRespectsUserOverrides(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	path := writeLifecycleMoEGGUF(t, 40, 64)
	s := &Service{
		hardware: moeStaticHardware{snapshot: hardware.Snapshot{RAMAvailableBytes: 64 * gib, GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * gib}, {ID: "CUDA1", FreeBytes: 8 * gib}}}},
		profile: func() (llamacpp.Profile, error) {
			return llamacpp.Profile{Options: []llamacpp.Option{{Key: "n-cpu-moe"}, {Key: "cpu-moe"}, {Key: "n-gpu-layers"}, {Key: "no-kv-offload"}}}, nil
		},
	}
	model := models.Model{ID: "moe", TotalBytes: 20 * gib, ContextLength: 32768}
	instance := instances.Instance{ID: "moe", ModelID: model.ID, GPUMode: "auto"}
	user := map[string]string{"ctx-size": "4096", "n-gpu-layers": "12", "n-cpu-moe": "7", "no-kv-offload": "true"}
	plan := s.prepareAutoMoELaunch(context.Background(), instance, model, path, user, user)
	if !plan.Applied {
		t.Fatal("expected MoE plan")
	}
	if plan.Options["n-gpu-layers"] != "12" || plan.Options["n-cpu-moe"] != "7" || plan.Options["no-kv-offload"] != "true" {
		t.Fatalf("user overrides changed: %v", plan.Options)
	}
}

func TestPrepareAutoMoELaunchDoesNotInventManualDevices(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	path := writeLifecycleMoEGGUF(t, 40, 64)
	s := &Service{
		hardware: moeStaticHardware{snapshot: hardware.Snapshot{RAMAvailableBytes: 64 * gib, GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * gib}, {ID: "CUDA1", FreeBytes: 8 * gib}}}},
		profile: func() (llamacpp.Profile, error) {
			return llamacpp.Profile{Options: []llamacpp.Option{{Key: "n-cpu-moe"}}}, nil
		},
	}
	model := models.Model{ID: "moe", TotalBytes: 20 * gib}
	instance := instances.Instance{ID: "moe", ModelID: model.ID, GPUMode: "manual", GPUDevices: []string{"CUDA0"}}
	plan := s.prepareAutoMoELaunch(context.Background(), instance, model, path, map[string]string{}, map[string]string{})
	if plan.Applied || len(plan.Devices) != 0 {
		t.Fatalf("manual placement must remain advisory-only, plan=%+v", plan)
	}
}

func TestApplyCPUMoeLoadMode(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	path := writeLifecycleMoEGGUF(t, 40, 64)
	s := &Service{
		hardware: moeStaticHardware{snapshot: hardware.Snapshot{
			RAMAvailableBytes: 64 * gib,
			GPUs:              []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * gib}, {ID: "CUDA1", FreeBytes: 8 * gib}},
		}},
		profile: func() (llamacpp.Profile, error) {
			return llamacpp.Profile{Options: []llamacpp.Option{
				{Key: "n-cpu-moe"}, {Key: "cpu-moe"}, {Key: "n-gpu-layers"}, {Key: "no-kv-offload"},
				{Key: "load-mode", Kind: "enum", Choices: []string{"auto", "mmap", "none"}},
			}}, nil
		},
	}
	model := models.Model{ID: "moe", TotalBytes: 20 * gib, ContextLength: 32768}
	instance := instances.Instance{ID: "moe", ModelID: model.ID, GPUMode: "auto"}
	plan := s.prepareAutoMoELaunch(context.Background(), instance, model, path, map[string]string{"ctx-size": "4096"}, map[string]string{"ctx-size": "4096"})
	profile, _ := s.profile()
	got := applyCPUMoeLoadMode(plan.Options, profile)
	if got["load-mode"] != "none" {
		t.Fatalf("auto MoE launch must inject load-mode=none, options=%v", got)
	}

	for name, tc := range map[string]struct {
		options map[string]string
		profile llamacpp.Profile
		wantKey string
		wantVal string
		absent  []string
	}{
		"load-mode none": {
			options: map[string]string{"n-cpu-moe": "8"},
			profile: llamacpp.Profile{Options: []llamacpp.Option{{Key: "load-mode", Choices: []string{"auto", "mmap", "none"}}}},
			wantKey: "load-mode", wantVal: "none",
		},
		"load-mode without choices": {
			options: map[string]string{"cpu-moe": "true"},
			profile: llamacpp.Profile{Options: []llamacpp.Option{{Key: "load-mode"}}},
			wantKey: "load-mode", wantVal: "none",
		},
		"no-mmap fallback": {
			options: map[string]string{"n-cpu-moe": "4"},
			profile: llamacpp.Profile{Options: []llamacpp.Option{{Key: "no-mmap"}}},
			wantKey: "no-mmap", wantVal: "true",
		},
		"keeps user load-mode": {
			options: map[string]string{"n-cpu-moe": "8", "load-mode": "mmap"},
			profile: llamacpp.Profile{Options: []llamacpp.Option{{Key: "load-mode", Choices: []string{"auto", "mmap", "none"}}}},
			wantKey: "load-mode", wantVal: "mmap", absent: []string{"no-mmap"},
		},
		"keeps user no-mmap": {
			options: map[string]string{"n-cpu-moe": "8", "no-mmap": "true"},
			profile: llamacpp.Profile{Options: []llamacpp.Option{{Key: "load-mode"}, {Key: "no-mmap"}}},
			wantKey: "no-mmap", wantVal: "true", absent: []string{"load-mode"},
		},
		"keeps user mmap": {
			options: map[string]string{"n-cpu-moe": "8", "mmap": "true"},
			profile: llamacpp.Profile{Options: []llamacpp.Option{{Key: "load-mode"}, {Key: "mmap"}}},
			wantKey: "mmap", wantVal: "true", absent: []string{"load-mode", "no-mmap"},
		},
		"no cpu-moe flags": {
			options: map[string]string{"ctx-size": "4096"},
			profile: llamacpp.Profile{Options: []llamacpp.Option{{Key: "load-mode"}}},
			absent:  []string{"load-mode", "no-mmap"},
		},
		"n-cpu-moe zero": {
			options: map[string]string{"n-cpu-moe": "0"},
			profile: llamacpp.Profile{Options: []llamacpp.Option{{Key: "load-mode"}}},
			absent:  []string{"load-mode", "no-mmap"},
		},
		"enum without none falls back to no-mmap": {
			options: map[string]string{"n-cpu-moe": "8"},
			profile: llamacpp.Profile{Options: []llamacpp.Option{
				{Key: "load-mode", Choices: []string{"auto", "mmap"}},
				{Key: "no-mmap"},
			}},
			wantKey: "no-mmap", wantVal: "true", absent: []string{"load-mode"},
		},
		"neither flag available": {
			options: map[string]string{"n-cpu-moe": "8"},
			profile: llamacpp.Profile{Options: []llamacpp.Option{{Key: "n-cpu-moe"}}},
			absent:  []string{"load-mode", "no-mmap"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := applyCPUMoeLoadMode(tc.options, tc.profile)
			if tc.wantKey != "" && got[tc.wantKey] != tc.wantVal {
				t.Fatalf("options[%q]=%q want %q (got=%v)", tc.wantKey, got[tc.wantKey], tc.wantVal, got)
			}
			for _, key := range tc.absent {
				if _, ok := got[key]; ok && (tc.wantKey == "" || key != tc.wantKey) {
					t.Fatalf("unexpected %q in %v", key, got)
				}
			}
		})
	}
}

func writeLifecycleMoEGGUF(t *testing.T, blocks, experts uint32) string {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("GGUF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(3))
	_ = binary.Write(&b, binary.LittleEndian, uint64(0))
	items := []struct {
		key    string
		typeID uint32
		str    string
		u32    uint32
	}{
		{key: "general.architecture", typeID: 8, str: "qwen3moe"},
		{key: "qwen3moe.context_length", typeID: 4, u32: 32768},
		{key: "qwen3moe.block_count", typeID: 4, u32: blocks},
		{key: "qwen3moe.embedding_length", typeID: 4, u32: 4096},
		{key: "qwen3moe.attention.head_count", typeID: 4, u32: 32},
		{key: "qwen3moe.attention.head_count_kv", typeID: 4, u32: 8},
		{key: "qwen3moe.expert_count", typeID: 4, u32: experts},
	}
	_ = binary.Write(&b, binary.LittleEndian, uint64(len(items)))
	for _, item := range items {
		_ = binary.Write(&b, binary.LittleEndian, uint64(len(item.key)))
		b.WriteString(item.key)
		_ = binary.Write(&b, binary.LittleEndian, item.typeID)
		if item.typeID == 8 {
			_ = binary.Write(&b, binary.LittleEndian, uint64(len(item.str)))
			b.WriteString(item.str)
		} else {
			_ = binary.Write(&b, binary.LittleEndian, item.u32)
		}
	}
	path := filepath.Join(t.TempDir(), "moe.gguf")
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
