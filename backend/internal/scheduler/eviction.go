package scheduler

import (
	"sort"
	"strings"
	"time"
)

// Candidate describes a currently loaded instance that could potentially be
// stopped to free resources for another model. Resource measurements are
// intentionally generic here; Phase 7 will supply per-GPU VRAM snapshots.
type Candidate struct {
	ModelID         string
	InstanceID      string
	Priority        string
	AlwaysOn        bool
	EvictionEnabled bool
	ActiveRequests  int
	LastUsed        time.Time
	EstimatedBytes  int64
	Ready           bool
}

// Plan is a deterministic eviction decision for a requested amount of
// capacity. Fits is false when the eligible candidates cannot free enough.
type Plan struct {
	Evict      []Candidate
	FreedBytes int64
	Fits       bool
}

// RankEvictionCandidates returns only normally evictable instances ordered by
// priority first (Low before Normal before High), then least-recently-used,
// then largest estimated resource release, with stable ID tie-breakers.
// Always On expresses desired lifecycle state and does not affect normal
// resource-pressure eviction eligibility; EvictionEnabled is the source of
// truth for that policy.
func RankEvictionCandidates(candidates []Candidate) []Candidate {
	out := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if !candidate.Ready || !candidate.EvictionEnabled || candidate.ActiveRequests > 0 {
			continue
		}
		out = append(out, candidate)
	}

	sort.Slice(out, func(i, j int) bool {
		left, right := out[i], out[j]
		if lp, rp := priorityRank(left.Priority), priorityRank(right.Priority); lp != rp {
			return lp < rp
		}
		if !left.LastUsed.Equal(right.LastUsed) {
			if left.LastUsed.IsZero() {
				return true
			}
			if right.LastUsed.IsZero() {
				return false
			}
			return left.LastUsed.Before(right.LastUsed)
		}
		if left.EstimatedBytes != right.EstimatedBytes {
			return left.EstimatedBytes > right.EstimatedBytes
		}
		if left.ModelID != right.ModelID {
			return left.ModelID < right.ModelID
		}
		return left.InstanceID < right.InstanceID
	})
	return out
}

// PlanEvictions chooses the smallest prefix of ranked candidates that can free
// requiredBytes. Unknown/non-positive estimates do not contribute capacity.
func PlanEvictions(candidates []Candidate, requiredBytes int64) Plan {
	if requiredBytes <= 0 {
		return Plan{Fits: true}
	}

	plan := Plan{}
	for _, candidate := range RankEvictionCandidates(candidates) {
		plan.Evict = append(plan.Evict, candidate)
		if candidate.EstimatedBytes > 0 {
			plan.FreedBytes += candidate.EstimatedBytes
		}
		if plan.FreedBytes >= requiredBytes {
			plan.Fits = true
			return plan
		}
	}
	return plan
}

func priorityRank(priority string) int {
	switch strings.ToLower(strings.TrimSpace(priority)) {
	case "low":
		return 0
	case "high":
		return 2
	default:
		return 1
	}
}
