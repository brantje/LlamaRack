package huggingface

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDetailConsumesHubGGUFSummary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/models/empero-ai/Qwen3.8-27B-Ridge-GGUF" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("blobs") != "true" {
			t.Fatalf("model-info request did not ask for file metadata: %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "empero-ai/Qwen3.8-27B-Ridge-GGUF",
			"sha": "ridge-rev",
			"gguf": map[string]any{
				"total": int64(27_315_000_000),
				"architecture": "qwen35",
				"context_length": int64(262144),
			},
			"siblings": []map[string]any{{
				"rfilename": "Qwen3.8-27B-Ridge-3.7bpw.gguf",
				"size": int64(12_599_187_008),
			}},
		})
	}))
	defer server.Close()

	client, err := NewClientWithHTTP(server.URL, nil, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	detail, err := client.Detail(context.Background(), "empero-ai/Qwen3.8-27B-Ridge-GGUF")
	if err != nil {
		t.Fatal(err)
	}
	if detail.GGUF == nil || detail.GGUF.Architecture != "qwen35" || detail.GGUF.ContextLength != 262144 || detail.ParameterCount != 27_315_000_000 {
		t.Fatalf("detail=%+v", detail)
	}
	if len(detail.Artifacts) != 1 || detail.Artifacts[0].BitsPerWeight != 3.69 || detail.Artifacts[0].ProfileQuantization() != "3.69BPW" {
		t.Fatalf("artifacts=%+v", detail.Artifacts)
	}
}
