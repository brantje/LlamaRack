package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/database"
	"github.com/brantje/llamacpp-manager/backend/internal/observability"
)

func TestWithRequestLogContextGeneratesDistinctSessionFallbacks(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := observability.New(db)

	requestIDs := []string{"lcm_generated_session_1", "lcm_generated_session_2"}
	for _, requestID := range requestIDs {
		if err := service.RecordCorrelatedRequest(ctx, requestID, nil, observability.RequestRecord{
			StartedAt: 1, FinishedAt: 2, InstanceID: "test-instance", Endpoint: "/v1/chat/completions", StatusCode: http.StatusOK, Result: "success",
		}); err != nil {
			t.Fatal(err)
		}
	}

	downstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set(headerRequestID, r.Header.Get("X-Test-Request-ID"))
		w.WriteHeader(http.StatusOK)
	})
	handler := WithRequestLogContext(downstream, service)

	generated := make([]string, 0, len(requestIDs))
	for _, requestID := range requestIDs {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"coder"}`))
		req.Header.Set("X-Test-Request-ID", requestID)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		detail, err := service.GetRequestLogByRequestID(ctx, requestID)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := normalizeUUID(detail.SessionID); !ok {
			t.Fatalf("generated session_id is not UUID-shaped: %q", detail.SessionID)
		}
		generated = append(generated, detail.SessionID)
	}

	if generated[0] == generated[1] {
		t.Fatalf("generated session IDs must be distinct: %q", generated[0])
	}
}
