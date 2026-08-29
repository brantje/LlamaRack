package observability

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestListMaximumPagePreservesHasMore(t *testing.T) {
	s := testService(t)
	ctx := t.Context()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO inference_requests(started_at,finished_at,instance_id,endpoint,status_code,result) VALUES(?,?,?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for i := 0; i < 501; i++ {
		if _, err := stmt.ExecContext(ctx, int64(i+1), int64(i+2), "coder", "/v1/completions", 200, "success"); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	NewManagementHandler(s).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/observability/requests?limit=500", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var payload struct {
		Items   []RequestRecord `json:"items"`
		HasMore bool            `json:"has_more"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 500 || !payload.HasMore {
		t.Fatalf("items=%d has_more=%v", len(payload.Items), payload.HasMore)
	}

	w = httptest.NewRecorder()
	NewManagementHandler(s).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/observability/requests?limit=500&offset=1", nil))
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 500 || payload.HasMore {
		t.Fatalf("last page items=%d has_more=%v", len(payload.Items), payload.HasMore)
	}
}
