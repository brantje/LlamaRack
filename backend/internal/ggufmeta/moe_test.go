package ggufmeta

import "testing"

func TestDeriveMoEExpertCountsFromArchitecturePrefix(t *testing.T) {
	got := derive(map[string]string{
		"general.architecture":       "qwen3moe",
		"qwen3moe.block_count":       "48",
		"qwen3moe.expert_count":      "64",
		"qwen3moe.expert_used_count": "8",
	})
	if got.ExpertCount != 64 || got.ExpertUsedCount != 8 {
		t.Fatalf("derived experts=(%d,%d) want (64,8)", got.ExpertCount, got.ExpertUsedCount)
	}
	if got.BlockCount != 48 {
		t.Fatalf("block_count=%d want 48", got.BlockCount)
	}
}

func TestSummaryMetadataKeyIncludesExpertCounts(t *testing.T) {
	for _, key := range []string{"qwen3moe.expert_count", "future_moe.expert_used_count"} {
		if !summaryMetadataKey(key) {
			t.Fatalf("summary should retain %q", key)
		}
	}
}
