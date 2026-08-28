package supervisor

import (
	"strings"
	"testing"
	"time"
)

func TestAddManagerLogUsesBoundedSessionRing(t *testing.T) {
	s := New("llama-server", "127.0.0.1", 18080, time.Second)

	s.AddManagerLog("", "ignored")
	s.AddManagerLog("one", "   ")
	if got := s.Logs("one"); len(got) != 0 {
		t.Fatalf("empty manager lines should be ignored: %v", got)
	}

	s.AddManagerLog("one", "  autoload triggered  ")
	logs := s.Logs("one")
	if len(logs) != 1 || logs[0] != "[manager] autoload triggered" {
		t.Fatalf("logs=%v", logs)
	}

	initial, ch, cancel := s.SubscribeLogs("one")
	defer cancel()
	if len(initial) != 1 || !strings.Contains(initial[0], "autoload triggered") {
		t.Fatalf("initial=%v", initial)
	}
	s.AddManagerLog("one", "worker ready")
	select {
	case line := <-ch:
		if line != "[manager] worker ready" {
			t.Fatalf("line=%q", line)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for manager log subscriber")
	}
}
