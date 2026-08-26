package scheduler

import (
	"testing"
	"time"
)

func TestRankEvictionCandidates(t *testing.T) {
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := old.Add(time.Hour)
	items := []Candidate{
		{ModelID: "high", InstanceID: "1", Priority: "high", Ready: true, LastUsed: old, EstimatedBytes: 9},
		{ModelID: "normal-new", InstanceID: "1", Priority: "normal", Ready: true, LastUsed: newer, EstimatedBytes: 9},
		{ModelID: "low-new", InstanceID: "1", Priority: "LOW", Ready: true, LastUsed: newer, EstimatedBytes: 5},
		{ModelID: "low-old-small", InstanceID: "1", Priority: "low", Ready: true, LastUsed: old, EstimatedBytes: 3},
		{ModelID: "low-old-large", InstanceID: "1", Priority: "low", Ready: true, LastUsed: old, EstimatedBytes: 8},
		{ModelID: "low-never", InstanceID: "1", Priority: "low", Ready: true, EstimatedBytes: 1},
		{ModelID: "always", InstanceID: "1", Priority: "low", Ready: true, AlwaysOn: true, EstimatedBytes: 100},
		{ModelID: "active", InstanceID: "1", Priority: "low", Ready: true, ActiveRequests: 1, EstimatedBytes: 100},
		{ModelID: "unloaded", InstanceID: "1", Priority: "low", Ready: false, EstimatedBytes: 100},
	}

	got := RankEvictionCandidates(items)
	want := []string{"low-never", "low-old-large", "low-old-small", "low-new", "normal-new", "high"}
	if len(got) != len(want) {
		t.Fatalf("ranked=%+v", got)
	}
	for i, id := range want {
		if got[i].ModelID != id {
			t.Fatalf("ranked[%d]=%q want=%q; all=%+v", i, got[i].ModelID, id, got)
		}
	}
}

func TestRankEvictionCandidatesStableTieBreakersAndDefaultPriority(t *testing.T) {
	when := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got := RankEvictionCandidates([]Candidate{
		{ModelID: "b", InstanceID: "2", Priority: "mystery", Ready: true, LastUsed: when, EstimatedBytes: 4},
		{ModelID: "a", InstanceID: "2", Priority: "normal", Ready: true, LastUsed: when, EstimatedBytes: 4},
		{ModelID: "a", InstanceID: "1", Priority: " normal ", Ready: true, LastUsed: when, EstimatedBytes: 4},
	})
	if len(got) != 3 || got[0].InstanceID != "1" || got[1].ModelID != "a" || got[2].ModelID != "b" {
		t.Fatalf("stable ranking=%+v", got)
	}
}

func TestPlanEvictions(t *testing.T) {
	items := []Candidate{
		{ModelID: "first", Priority: "low", Ready: true, EstimatedBytes: 4},
		{ModelID: "unknown", Priority: "low", Ready: true, EstimatedBytes: 0},
		{ModelID: "second", Priority: "normal", Ready: true, EstimatedBytes: 7},
	}

	if plan := PlanEvictions(items, 0); !plan.Fits || len(plan.Evict) != 0 || plan.FreedBytes != 0 {
		t.Fatalf("zero plan=%+v", plan)
	}
	plan := PlanEvictions(items, 9)
	if !plan.Fits || plan.FreedBytes != 11 || len(plan.Evict) != 3 || plan.Evict[0].ModelID != "first" || plan.Evict[2].ModelID != "second" {
		t.Fatalf("fit plan=%+v", plan)
	}
	plan = PlanEvictions(items, 20)
	if plan.Fits || plan.FreedBytes != 11 || len(plan.Evict) != 3 {
		t.Fatalf("insufficient plan=%+v", plan)
	}
}
