package litellm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type fakeLiteLLMServer struct {
	server *httptest.Server
	mu     sync.Mutex
	models map[string]ModelEntry
}

func newFakeLiteLLMServer(t *testing.T) *fakeLiteLLMServer {
	t.Helper()
	fake := &fakeLiteLLMServer{models: map[string]ModelEntry{}}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer proxy-key" && r.Header.Get("Authorization") != "Bearer sk-proxy-test-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/model/info":
			fake.mu.Lock()
			data := make([]ModelEntry, 0, len(fake.models))
			for _, entry := range fake.models {
				data = append(data, entry)
			}
			fake.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		case r.Method == http.MethodPost && r.URL.Path == "/model/new":
			var entry ModelEntry
			if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if entry.ModelInfo.ID == "" {
				entry.ModelInfo.ID = "litellm-" + entry.ModelName
			}
			fake.mu.Lock()
			fake.models[entry.ModelInfo.ID] = entry
			fake.mu.Unlock()
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/model/update":
			var entry ModelEntry
			if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			fake.mu.Lock()
			if existing, ok := fake.models[entry.ModelInfo.ID]; ok {
				if entry.ModelInfo.LlamaRackInstanceID != "" && entry.ModelInfo.LlamaRackInstanceID != existing.ModelInfo.LlamaRackInstanceID {
					delete(fake.models, entry.ModelInfo.ID)
					entry.ModelInfo.ID = "litellm-" + entry.ModelName
				}
			}
			fake.models[entry.ModelInfo.ID] = entry
			fake.mu.Unlock()
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/model/delete":
			var body struct {
				ID string `json:"id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			fake.mu.Lock()
			delete(fake.models, body.ID)
			fake.mu.Unlock()
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func TestClientReconcileLifecycle(t *testing.T) {
	fake := newFakeLiteLLMServer(t)
	client, err := NewClient(fake.server.URL, "proxy-key", fake.server.Client())
	if err != nil {
		t.Fatal(err)
	}
	entry := BuildModelEntry("alpha", "http://llamarack/v1", "sk-testsecret", "")
	if err := client.CreateModel(t.Context(), entry); err != nil {
		t.Fatal(err)
	}
	models, err := client.ListModels(t.Context())
	if err != nil || len(models) != 1 {
		t.Fatalf("list=%v err=%v", models, err)
	}
	renamed := BuildModelEntry("beta", "http://llamarack/v1", "sk-testsecret", models[0].ModelInfo.ID)
	if err := client.UpdateModel(t.Context(), renamed); err != nil {
		t.Fatal(err)
	}
	models, err = client.ListModels(t.Context())
	if err != nil || len(models) != 1 {
		t.Fatalf("after rename list=%v err=%v", models, err)
	}
	if err := client.DeleteModel(t.Context(), models[0].ModelInfo.ID); err != nil {
		t.Fatal(err)
	}
	models, err = client.ListModels(t.Context())
	if err != nil || len(models) != 0 {
		t.Fatalf("expected empty catalog, got %#v err=%v", models, err)
	}
}

func TestClientStoreModelInDBError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "STORE_MODEL_IN_DB must be enabled", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, "proxy-key", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.TestConnection(t.Context()); !strings.Contains(err.Error(), "STORE_MODEL_IN_DB") {
		t.Fatalf("expected STORE_MODEL_IN_DB error, got %v", err)
	}
}

func TestClientRejectsNonHTTPURL(t *testing.T) {
	if _, err := NewClient("ftp://example.com", "key", nil); err == nil {
		t.Fatal("expected invalid scheme error")
	}
}

func TestEntryDrifted(t *testing.T) {
	entry := BuildModelEntry("alpha", "http://llamarack/v1", "sk-one", "id-1")
	if entryDrifted(entry, "alpha", "http://llamarack/v1", "sk-one") {
		t.Fatal("identical entry should not drift")
	}
	if !entryDrifted(entry, "alpha", "http://llamarack/v1", "sk-two") {
		t.Fatal("api key change should drift")
	}
}

func TestEntryDriftedDetectsFieldChanges(t *testing.T) {
	entry := BuildModelEntry("alpha", "http://llamarack/v1", "sk-one", "id-1")
	cases := []struct {
		instanceID string
		apiBase    string
		apiKey     string
	}{
		{"beta", "http://llamarack/v1", "sk-one"},
		{"alpha", "http://other/v1", "sk-one"},
		{"alpha", "http://llamarack/v1", "sk-two"},
	}
	for _, tc := range cases {
		if !entryDrifted(entry, tc.instanceID, tc.apiBase, tc.apiKey) {
			t.Fatalf("expected drift for %+v", tc)
		}
	}
	entry.ModelInfo.LlamaRackInstanceID = "stale"
	if !entryDrifted(entry, "alpha", "http://llamarack/v1", "sk-one") {
		t.Fatal("expected instance id drift")
	}
	if entryDrifted(entry, "alpha", "http://llamarack/v1", "sk-one") && entry.ModelName == "alpha" && entry.LiteLLMParams.Model == "openai/alpha" {
		// drift only from instance id field above
	}
}
