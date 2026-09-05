package litellm

import (
	"testing"

	"github.com/brantje/llamarack/backend/internal/instances"
)

func TestInstanceModelEntrySeparatesStableOwnershipFromPublicSlug(t *testing.T) {
	const id = "8c821aec-1f0d-4b8d-a332-41c582dd2c58"
	entry := BuildInstanceModelEntry(id, "qwen-coder-32b", "http://llamarack/v1", "sk-test", "remote-id")
	if entry.ModelName != "qwen-coder-32b" || entry.LiteLLMParams.Model != "openai/qwen-coder-32b" {
		t.Fatalf("public identity=%+v", entry)
	}
	if entry.ModelInfo.LlamaRackInstanceID != id || entry.ModelInfo.ID != "remote-id" {
		t.Fatalf("durable ownership=%+v", entry.ModelInfo)
	}
	if !instanceEntryDrifted(entry, id, "renamed-coder", "http://llamarack/v1", "sk-test") {
		t.Fatal("slug rename should update the existing LiteLLM model")
	}
}

func TestResolveManagedRemoteInstanceAdoptsLegacySlugOwnership(t *testing.T) {
	const id = "8c821aec-1f0d-4b8d-a332-41c582dd2c58"
	item := instances.Instance{ID: id, Slug: "qwen-coder-32b", Name: "Qwen Coder 32B"}
	byID := map[string]instances.Instance{id: item}
	bySlug := map[string]instances.Instance{item.Slug: item}

	legacy := BuildModelEntry(item.Slug, "http://llamarack/v1", "sk-test", "remote-id")
	resolved, ok := resolveManagedRemoteInstance(legacy, byID, bySlug)
	if !ok || resolved.ID != id {
		t.Fatalf("legacy row resolved=%+v ok=%v", resolved, ok)
	}

	updated := BuildInstanceModelEntry(id, item.Slug, "http://llamarack/v1", "sk-test", "remote-id")
	resolved, ok = resolveManagedRemoteInstance(updated, byID, bySlug)
	if !ok || resolved.ID != id {
		t.Fatalf("uuid row resolved=%+v ok=%v", resolved, ok)
	}
}
