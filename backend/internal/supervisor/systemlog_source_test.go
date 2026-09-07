package supervisor

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/resourceid"
	"github.com/brantje/llamarack/backend/internal/systemlog"
)

const (
	testSystemLogInstanceID   = "8c821aec-1f0d-4b8d-a332-41c582dd2c58"
	testSystemLogInstanceSlug = "qwen-coder-32b"
)

func TestCopyLogsUsesPublicSlugAsSystemLogSource(t *testing.T) {
	systemlog.Default.Reset()
	defer systemlog.Default.Reset()

	copyLogs(newRing(8), testSystemLogInstanceID, "model", "stdout", testSystemLogInstanceSlug, strings.NewReader("fake worker online\nstderr line\n"))
	assertSystemLogSource(t, testSystemLogInstanceSlug, "fake worker online")
	assertSystemLogSource(t, testSystemLogInstanceSlug, "stderr line")
	assertSystemLogSourceUnused(t, testSystemLogInstanceID)
}

func TestStartWithEnvSystemLogsUseSlugNotDurableID(t *testing.T) {
	systemlog.Default.Reset()
	defer systemlog.Default.Reset()
	resourceid.RememberInstanceSlug(testSystemLogInstanceID, testSystemLogInstanceSlug)
	defer resourceid.ForgetInstanceSlug(testSystemLogInstanceID)

	s := New(fakeServerScript(t), "127.0.0.1", 29600, 5*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	rt, err := s.StartWithEnv(ctx, testSystemLogInstanceID, "model-1", "/tmp/model.gguf", nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop(context.Background(), testSystemLogInstanceID) }()
	if rt.InstanceID != testSystemLogInstanceID {
		t.Fatalf("runtime instance id=%q want durable id %q", rt.InstanceID, testSystemLogInstanceID)
	}

	s.mu.RLock()
	_, ownedByID := s.workers[testSystemLogInstanceID]
	_, ownedBySlug := s.workers[testSystemLogInstanceSlug]
	s.mu.RUnlock()
	if !ownedByID || ownedBySlug {
		t.Fatalf("worker ownership keyed incorrectly id=%v slug=%v", ownedByID, ownedBySlug)
	}

	waitForSystemLogSource(t, testSystemLogInstanceSlug, "fake worker online")
	assertSystemLogSourceUnused(t, testSystemLogInstanceID)
}

func TestStartFailureSystemLogUsesSlug(t *testing.T) {
	systemlog.Default.Reset()
	defer systemlog.Default.Reset()

	s := New(filepath.Join(t.TempDir(), "missing-binary"), "127.0.0.1", 29610, 100*time.Millisecond)
	_, err := s.StartWithAliasEnv(context.Background(), testSystemLogInstanceID, testSystemLogInstanceSlug, "model", "/tmp/model.gguf", nil, nil, "")
	if err == nil {
		t.Fatal("expected missing binary error")
	}
	assertSystemLogSource(t, testSystemLogInstanceSlug, "failed to start:")
	assertSystemLogSourceUnused(t, testSystemLogInstanceID)
}

func TestUnexpectedExitSystemLogUsesSlug(t *testing.T) {
	systemlog.Default.Reset()
	defer systemlog.Default.Reset()
	resourceid.RememberInstanceSlug(testSystemLogInstanceID, testSystemLogInstanceSlug)
	defer resourceid.ForgetInstanceSlug(testSystemLogInstanceID)

	s := New(fakeServerScript(t), "127.0.0.1", 29620, 150*time.Millisecond)
	_, err := s.StartWithEnv(context.Background(), testSystemLogInstanceID, "model", "/tmp/exit-immediately.gguf", nil, nil, "")
	if err == nil {
		t.Fatal("expected readiness failure")
	}
	waitForSystemLogErrorSource(t, testSystemLogInstanceSlug)
	assertSystemLogSourceUnused(t, testSystemLogInstanceID)
}

func TestStartWithoutSlugIndexLogsInstanceID(t *testing.T) {
	systemlog.Default.Reset()
	defer systemlog.Default.Reset()

	s := New(fakeServerScript(t), "127.0.0.1", 29630, 5*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if _, err := s.Start(ctx, "a", "m", "/tmp/a.gguf", nil); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop(context.Background(), "a") }()
	waitForSystemLogSource(t, "a", "fake worker online")
}

func assertSystemLogSource(t *testing.T, source, fragment string) {
	t.Helper()
	if !systemLogHasSource(source, fragment) {
		t.Fatalf("missing system-log source=%q fragment=%q entries=%+v", source, fragment, systemlog.Default.Snapshot(50))
	}
}

func assertSystemLogSourceUnused(t *testing.T, source string) {
	t.Helper()
	for _, entry := range systemlog.Default.Snapshot(50) {
		if entry.Source == source {
			t.Fatalf("durable instance id leaked as system-log source: %+v", entry)
		}
	}
}

func waitForSystemLogSource(t *testing.T, source, fragment string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if systemLogHasSource(source, fragment) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for source=%q fragment=%q entries=%+v", source, fragment, systemlog.Default.Snapshot(50))
}

func waitForSystemLogErrorSource(t *testing.T, source string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, entry := range systemlog.Default.Snapshot(50) {
			if entry.Source == source && entry.Level == systemlog.Error {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for error source=%q entries=%+v", source, systemlog.Default.Snapshot(50))
}

func systemLogHasSource(source, fragment string) bool {
	for _, entry := range systemlog.Default.Snapshot(50) {
		if entry.Source == source && strings.Contains(entry.Message, fragment) {
			return true
		}
	}
	return false
}
