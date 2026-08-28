package supervisor

import (
	"strings"
	"testing"
	"time"
)

func assertTimestampedLog(t *testing.T, line, source, text string) {
	t.Helper()
	parts := strings.SplitN(line, "\t", 3)
	if len(parts) != 3 || parts[0] != "["+source+"]" || parts[2] != text {
		t.Fatalf("log=%q", line)
	}
	if _, err := time.Parse(time.RFC3339Nano, parts[1]); err != nil {
		t.Fatalf("timestamp=%q error=%v", parts[1], err)
	}
}

func TestAddManagerLogUsesBoundedSessionRing(t *testing.T) {
	s := New("llama-server", "127.0.0.1", 18080, time.Second)

	s.AddManagerLog("", "ignored")
	s.AddManagerLog("one", "   ")
	if got := s.Logs("one"); len(got) != 0 {
		t.Fatalf("empty manager lines should be ignored: %v", got)
	}

	s.AddManagerLog("one", "  autoload triggered  ")
	logs := s.Logs("one")
	if len(logs) != 1 { t.Fatalf("logs=%v", logs) }
	assertTimestampedLog(t, logs[0], "manager", "autoload triggered")

	initial, ch, cancel := s.SubscribeLogs("one")
	defer cancel()
	if len(initial) != 1 || !strings.Contains(initial[0], "autoload triggered") {
		t.Fatalf("initial=%v", initial)
	}
	s.AddManagerLog("one", "worker ready")
	select {
	case line := <-ch:
		assertTimestampedLog(t, line, "manager", "worker ready")
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for manager log subscriber")
	}
}

func TestFormatLaunchCommandQuotesEveryArgument(t *testing.T) {
	got := formatLaunchCommand("/opt/llama server", []string{"--model", "/models/my model.gguf", "--ctx-size", "8192"})
	want := `"/opt/llama server" "--model" "/models/my model.gguf" "--ctx-size" "8192"`
	if got != want { t.Fatalf("launch command=%q want=%q", got, want) }
}
