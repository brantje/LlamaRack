package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/llamacpp"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

func TestSlotSavePathInjectedWhenSupported(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, ms, m, _, exec := setupLifecycle(t, true, false)
	instances, err := ms.Instances(ctx, m.ID)
	if err != nil || len(instances) != 1 {
		t.Fatalf("instances=%+v err=%v", instances, err)
	}
	instanceID := instances[0].ID

	binary, argsFile := lifecycleCaptureBinary(t)
	sup := supervisor.New(binary, "127.0.0.1", freeLifecyclePort(t), 5*time.Second)
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		sup.Shutdown(stopCtx)
	})

	dataDir := filepath.Join(t.TempDir(), "data")
	s := New(ms, sup)
	s.SetDataDir(dataDir)
	s.SetProfileGetter(func() (llamacpp.Profile, error) {
		return llamacpp.Profile{Version: "test", Options: []llamacpp.Option{
			{Key: "slot-save-path", Kind: "string"},
		}}, nil
	})

	if _, err := s.StartInstance(ctx, instanceID); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(dataDir, "slots", instanceID)
	if !strings.Contains(string(raw), "--slot-save-path\n"+wantDir+"\n") {
		t.Fatalf("slot save path missing from argv: %q", raw)
	}
	if info, err := os.Stat(wantDir); err != nil || !info.IsDir() {
		t.Fatalf("slot dir=%v err=%v", info, err)
	}
	_ = exec
}

func TestSlotSavePathSkippedWhenUnsupported(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, ms, m, _, _ := setupLifecycle(t, true, false)
	instances, err := ms.Instances(ctx, m.ID)
	if err != nil || len(instances) != 1 {
		t.Fatalf("instances=%+v err=%v", instances, err)
	}
	instanceID := instances[0].ID

	binary, argsFile := lifecycleCaptureBinary(t)
	sup := supervisor.New(binary, "127.0.0.1", freeLifecyclePort(t), 5*time.Second)
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		sup.Shutdown(stopCtx)
	})

	s := New(ms, sup)
	s.SetDataDir(filepath.Join(t.TempDir(), "data"))
	s.SetProfileGetter(func() (llamacpp.Profile, error) {
		return llamacpp.Profile{Version: "test", Options: []llamacpp.Option{
			{Key: "ctx-size", Kind: "integer"},
		}}, nil
	})

	if _, err := s.StartInstance(ctx, instanceID); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "--slot-save-path") {
		t.Fatalf("unsupported schema should not inject slot save path: %q", raw)
	}
}
