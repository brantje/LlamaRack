package supervisor

import (
	"context"
	"testing"
	"time"
)

func TestRuntimeSubscriptionTracksStartAndStopTransitions(t *testing.T) {
	binary := fakeServerScript(t)
	s := New(binary, "127.0.0.1", 29000, 5*time.Second)
	snapshot, events, cancel := s.SubscribeRuntimes()
	defer cancel()
	if len(snapshot) != 0 {
		t.Fatalf("initial snapshot=%+v", snapshot)
	}

	ctx, ctxCancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer ctxCancel()
	if _, err := s.Start(ctx, "instance-events", "model-events", "/tmp/model.gguf", nil); err != nil {
		t.Fatal(err)
	}

	for _, want := range []State{Starting, Loading, Ready} {
		select {
		case runtime := <-events:
			if runtime.InstanceID != "instance-events" || runtime.ModelID != "model-events" || runtime.State != want {
				t.Fatalf("runtime=%+v want state=%s", runtime, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for %s", want)
		}
	}

	readySnapshot, _, cancelSnapshot := s.SubscribeRuntimes()
	cancelSnapshot()
	if len(readySnapshot) != 1 || readySnapshot[0].State != Ready {
		t.Fatalf("ready snapshot=%+v", readySnapshot)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopCancel()
	if err := s.Stop(stopCtx, "instance-events"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []State{Stopping, Unloaded} {
		select {
		case runtime := <-events:
			if runtime.State != want {
				t.Fatalf("runtime=%+v want state=%s", runtime, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for %s", want)
		}
	}
}

func TestRuntimeSubscriptionIsNonBlockingAndCancelable(t *testing.T) {
	s := New("unused", "127.0.0.1", 30000, time.Second)
	_, events, cancel := s.SubscribeRuntimes()

	s.mu.Lock()
	for i := 0; i < 80; i++ {
		s.emitRuntimeLocked(Runtime{InstanceID: "slow", ModelID: "model", State: Loading, PID: i})
	}
	s.mu.Unlock()

	latestPID := -1
	for {
		select {
		case runtime := <-events:
			latestPID = runtime.PID
		default:
			if latestPID != 79 {
				t.Fatalf("latest delivered pid=%d want=79", latestPID)
			}
			cancel()
			cancel()
			if _, open := <-events; open {
				t.Fatal("runtime event channel should close after cancel")
			}
			return
		}
	}
}
