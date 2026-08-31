package systemlog

import (
	"strconv"
	"testing"
	"time"
)

func TestStoreBoundsTimestampsAndSubscriptions(t *testing.T) {
	store := New(2)
	store.now = func() time.Time { return time.Date(2026, 8, 30, 12, 4, 58, 999, time.UTC) }

	store.Add(Info, "manager", "first")
	store.Add(Warn, "gateway", "second")
	initial, events, cancel := store.Subscribe(1)
	defer cancel()
	if len(initial) != 2 || initial[0].Message != "first" || initial[1].Message != "second" {
		t.Fatalf("initial=%+v", initial)
	}
	store.Add(Debug, "telemetry", "third")
	select {
	case entry := <-events:
		if entry.Timestamp != "2026-08-30T12:04:58Z" || entry.Level != Debug || entry.Source != "telemetry" || entry.Message != "third" {
			t.Fatalf("entry=%+v", entry)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for log event")
	}
	got := store.Snapshot(10)
	if len(got) != 2 || got[0].Message != "second" || got[1].Message != "third" {
		t.Fatalf("snapshot=%+v", got)
	}
}

func TestStoreValidationResetAndDurationFormatting(t *testing.T) {
	store := New(0)
	store.Add(Level("TRACE"), "manager", "ignored")
	store.Add(Info, "", "ignored")
	store.Add(Info, "manager", "   ")
	if got := store.Snapshot(10); len(got) != 0 {
		t.Fatalf("invalid entries=%+v", got)
	}
	store.Add(Error, "instance-a", "failed")
	store.Reset()
	if got := store.Snapshot(10); len(got) != 0 {
		t.Fatalf("reset entries=%+v", got)
	}
	if got := FormatDuration(41 * time.Millisecond); got != "41ms" {
		t.Fatalf("duration=%q", got)
	}
	if got := FormatDuration(1842 * time.Millisecond); got != "1.84s" {
		t.Fatalf("duration=%q", got)
	}
	if got := (*Store)(nil).Snapshot(10); len(got) != 0 {
		t.Fatalf("nil snapshot=%+v", got)
	}
	_, ch, cancel := (*Store)(nil).Subscribe(10)
	cancel()
	if _, open := <-ch; open {
		t.Fatal("nil subscription should be closed")
	}
}

func TestStoreSnapshotKeepsLastNPerSource(t *testing.T) {
	store := New(20)
	for i := 1; i <= 4; i++ {
		store.Add(Info, "manager", "m"+strconv.Itoa(i))
		store.Add(Warn, "gateway", "g"+strconv.Itoa(i))
	}
	got := store.Snapshot(2)
	if len(got) != 4 || got[0].Message != "m3" || got[1].Message != "g3" || got[2].Message != "m4" || got[3].Message != "g4" {
		t.Fatalf("snapshot=%+v", got)
	}
	initial, _, cancel := store.Subscribe(2)
	defer cancel()
	if len(initial) != 4 || initial[0].Message != "m3" || initial[3].Message != "g4" {
		t.Fatalf("subscribe=%+v", initial)
	}
	if got := store.Snapshot(0); len(got) != 0 {
		t.Fatalf("zero limit=%+v", got)
	}
	full, _, cancelAll := store.Subscribe(0)
	cancelAll()
	if len(full) != 8 || full[0].Message != "m1" || full[7].Message != "g4" {
		t.Fatalf("unlimited subscribe=%+v", full)
	}
}
