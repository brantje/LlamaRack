package supervisor

import (
	"testing"
	"time"
)

func TestLogSubscriptionSnapshotResetAndCancel(t *testing.T) {
	r := newRing(2)
	r.add("one")
	r.add("two")

	snapshot, events, cancel := r.subscribe()
	if len(snapshot) != 2 || snapshot[0] != "one" || snapshot[1] != "two" {
		t.Fatalf("snapshot=%v", snapshot)
	}

	r.reset()
	r.add("live")
	select {
	case got := <-events:
		if got != "live" {
			t.Fatalf("event=%q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live log event")
	}

	cancel()
	cancel()
	if _, ok := <-events; ok {
		t.Fatal("subscription channel should be closed")
	}
}

func TestSupervisorSubscribeBeforeWorkerExists(t *testing.T) {
	s := New("unused", "127.0.0.1", 20000, time.Second)
	snapshot, events, cancel := s.SubscribeLogs("instance-before-start")
	defer cancel()
	if len(snapshot) != 0 {
		t.Fatalf("snapshot=%v", snapshot)
	}

	s.mu.RLock()
	logRing := s.logs["instance-before-start"]
	s.mu.RUnlock()
	if logRing == nil {
		t.Fatal("expected log hub to exist before worker start")
	}
	logRing.add("startup line")

	select {
	case got := <-events:
		if got != "startup line" {
			t.Fatalf("event=%q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pre-start subscription event")
	}
	if got := s.Logs("instance-before-start"); len(got) != 1 || got[0] != "startup line" {
		t.Fatalf("logs=%v", got)
	}
}
