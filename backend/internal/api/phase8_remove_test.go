package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/downloads"
	"github.com/brantje/llamacpp-manager/backend/internal/huggingface"
)

func TestPhase8RemoveDownloadRoutes(t *testing.T) {
	fixture := newPhase8Fixture(t)
	if got := phase8Request(t, fixture, http.MethodDelete, "/api/v1/downloads/missing", nil, true).Code; got != http.StatusNotFound {
		t.Fatalf("missing removal status = %d", got)
	}

	w := phase8Request(t, fixture, http.MethodGet, "/api/v1/huggingface/model?repo=acme%2Fdemo", nil, true)
	var detail huggingface.ModelDetail
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || len(detail.Artifacts) != 1 {
		t.Fatalf("detail = %+v err=%v", detail, err)
	}
	w = phase8Request(t, fixture, http.MethodPost, "/api/v1/downloads", map[string]string{
		"repo_id": "acme/demo", "artifact_id": detail.Artifacts[0].ID,
	}, true)
	var job downloads.Job
	if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil || job.ID == "" {
		t.Fatalf("job = %+v err=%v", job, err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		w = phase8Request(t, fixture, http.MethodGet, "/api/v1/downloads/"+job.ID, nil, true)
		if strings.Contains(w.Body.String(), downloads.StateCompleted) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	w = phase8Request(t, fixture, http.MethodDelete, "/api/v1/downloads/"+job.ID, nil, true)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "only cancelled downloads") {
		t.Fatalf("completed removal status=%d body=%s", w.Code, w.Body.String())
	}

	if got := phase8Request(t, fixture, http.MethodPatch, "/api/v1/downloads/"+job.ID, nil, true).Code; got != http.StatusMethodNotAllowed {
		t.Fatalf("single-item method status = %d", got)
	}
}
