package litellm

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type fakeLiteLLMServer struct {
	server     *httptest.Server
	mu         sync.Mutex
	models     map[string]ModelEntry
	failList   bool
	failCreate bool
	failUpdate bool
	failDelete bool
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
			failList := fake.failList
			if failList {
				fake.mu.Unlock()
				http.Error(w, "list failed", http.StatusBadGateway)
				return
			}
			data := make([]ModelEntry, 0, len(fake.models))
			for _, entry := range fake.models {
				data = append(data, entry)
			}
			fake.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		case r.Method == http.MethodPost && r.URL.Path == "/model/new":
			fake.mu.Lock()
			failCreate := fake.failCreate
			fake.mu.Unlock()
			if failCreate {
				http.Error(w, "create failed", http.StatusBadGateway)
				return
			}
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(raw, &payload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if modelInfoRaw, ok := payload["model_info"]; ok {
				var modelInfo map[string]json.RawMessage
				if err := json.Unmarshal(modelInfoRaw, &modelInfo); err == nil {
					if idRaw, hasID := modelInfo["id"]; hasID && string(idRaw) == `""` {
						http.Error(w, "empty model_info.id rejected", http.StatusBadRequest)
						return
					}
				}
			}
			var entry ModelEntry
			if err := json.Unmarshal(raw, &entry); err != nil {
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
			fake.mu.Lock()
			failUpdate := fake.failUpdate
			fake.mu.Unlock()
			if failUpdate {
				http.Error(w, "update failed", http.StatusBadGateway)
				return
			}
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
			fake.mu.Lock()
			failDelete := fake.failDelete
			fake.mu.Unlock()
			if failDelete {
				http.Error(w, "delete failed", http.StatusBadGateway)
				return
			}
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

func (fake *fakeLiteLLMServer) snapshotModels() map[string]ModelEntry {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	out := make(map[string]ModelEntry, len(fake.models))
	for id, entry := range fake.models {
		out[id] = entry
	}
	return out
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

func TestClientListModelsReadsCatalogLargerThanOneMiB(t *testing.T) {
	entry := BuildModelEntry("alpha", "http://llamarack/v1", "sk-testsecret", "litellm-alpha")
	payload, err := json.Marshal(map[string]any{
		"data": []ModelEntry{entry},
		"pad":  strings.Repeat("x", (1<<20)+64),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) <= 1<<20 {
		t.Fatalf("expected oversized catalog fixture, got %d bytes", len(payload))
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer proxy-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, "proxy-key", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	models, err := client.ListModels(t.Context())
	if err != nil {
		t.Fatalf("list oversized catalog: %v", err)
	}
	if len(models) != 1 || models[0].ModelName != "alpha" {
		t.Fatalf("models=%#v", models)
	}
}

func TestReadBoundedProxyResponseRejectsTruncation(t *testing.T) {
	raw, err := readBoundedProxyResponse(nil, 64)
	if err != nil || raw != nil {
		t.Fatalf("nil body raw=%q err=%v", raw, err)
	}
	raw, err = readBoundedProxyResponse(strings.NewReader(strings.Repeat("x", 8)), 7)
	if err == nil || !strings.Contains(err.Error(), "exceeds 7 bytes") {
		t.Fatalf("raw=%q err=%v", raw, err)
	}
	raw, err = readBoundedProxyResponse(strings.NewReader(`{"data":[]}`), 64)
	if err != nil || string(raw) != `{"data":[]}` {
		t.Fatalf("raw=%q err=%v", raw, err)
	}
	if _, err = readBoundedProxyResponse(errReader{io.ErrUnexpectedEOF}, 64); err != io.ErrUnexpectedEOF {
		t.Fatalf("expected read error, got %v", err)
	}
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) {
	return 0, r.err
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

func TestParseAPIErrorSanitizesSecretsAndBoundsLength(t *testing.T) {
	err := parseAPIError(500, []byte(`{"api_key":"sk-supersecretvalue","detail":"nope"}`))
	if err == nil || strings.Contains(err.Error(), "sk-supersecretvalue") || strings.Contains(err.Error(), `"api_key":"sk-`) {
		t.Fatalf("expected redacted error, got %v", err)
	}
	long := strings.Repeat("x", 4000)
	bounded := parseAPIError(502, []byte(long))
	if bounded == nil || len([]rune(bounded.Error())) > 600 {
		t.Fatalf("expected truncated error, got len=%d err=%v", len(bounded.Error()), bounded)
	}
}

func TestEntryDriftedIgnoresRedactedRemoteAPIKey(t *testing.T) {
	entry := BuildModelEntry("alpha", "http://llamarack/v1", "sk-one", "id-1")
	entry.LiteLLMParams.APIKey = ""
	if entryDrifted(entry, "alpha", "http://llamarack/v1", "sk-one") {
		t.Fatal("empty remote api_key should not count as drift")
	}
	entry.LiteLLMParams.APIKey = "****"
	if entryDrifted(entry, "alpha", "http://llamarack/v1", "sk-one") {
		t.Fatal("masked remote api_key should not count as drift")
	}
	entry.LiteLLMParams.APIKey = "[REDACTED]"
	if entryDrifted(entry, "alpha", "http://llamarack/v1", "sk-one") {
		t.Fatal("redacted remote api_key should not count as drift")
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
}

func TestBuildModelEntryOmitsEmptyID(t *testing.T) {
	entry := BuildModelEntry("alpha", "http://llamarack/v1", "sk-testsecret", "")
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"id":""`) || strings.Contains(string(raw), `"id": ""`) {
		t.Fatalf("create payload must omit empty id, got %s", raw)
	}
	update := BuildModelEntry("alpha", "http://llamarack/v1", "sk-testsecret", "litellm-alpha")
	raw, err = json.Marshal(update)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"id":"litellm-alpha"`) && !strings.Contains(string(raw), `"id": "litellm-alpha"`) {
		t.Fatalf("update payload must include id, got %s", raw)
	}
}

func TestCreateModelRejectsExplicitEmptyID(t *testing.T) {
	fake := newFakeLiteLLMServer(t)
	client, err := NewClient(fake.server.URL, "proxy-key", fake.server.Client())
	if err != nil {
		t.Fatal(err)
	}
	body := `{"model_name":"alpha","litellm_params":{"model":"openai/alpha","api_base":"http://llamarack/v1","api_key":"sk-test"},"model_info":{"id":"","llamarack_managed":true,"llamarack_instance_id":"alpha"}}`
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fake.server.URL+"/model/new", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer proxy-key")
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for explicit empty id, got %d", resp.StatusCode)
	}
	entry := BuildModelEntry("alpha", "http://llamarack/v1", "sk-testsecret", "")
	if err := client.CreateModel(t.Context(), entry); err != nil {
		t.Fatal(err)
	}
}
