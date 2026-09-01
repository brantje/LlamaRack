package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/observability"
)

func TestSessionIDInputs(t *testing.T) {
	if got := normalizeSessionID("  session-123  "); got != "session-123" {
		t.Fatalf("normalized=%q", got)
	}
	if got := normalizeSessionID("bad\nvalue"); got != "" {
		t.Fatalf("control chars should be rejected: %q", got)
	}
	if got := normalizeSessionID(strings.Repeat("x", 513)); got != "" {
		t.Fatalf("oversized session should be rejected")
	}
	if got := sessionIDFromEnvelope(sessionEnvelope{SessionID: "root-session"}); got != "root-session" {
		t.Fatalf("root session=%q", got)
	}
	var nested sessionEnvelope
	nested.Metadata.SessionID = "metadata-session"
	if got := sessionIDFromEnvelope(nested); got != "metadata-session" {
		t.Fatalf("metadata session=%q", got)
	}
	nested.LiteLLMMetadata.SessionID = "litellm-session"
	if got := sessionIDFromEnvelope(nested); got != "litellm-session" {
		t.Fatalf("litellm session=%q", got)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set(headerSessionID, "header-session")
	if got := sessionIDFromHeaders(req); got != "header-session" {
		t.Fatalf("header session=%q", got)
	}
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("X-OpenAI-Session-ID", "vendor-session")
	req.Header.Set("X-LiteLLM-Trace-ID", "trace-not-session")
	if got := sessionIDFromHeaders(req); got != "vendor-session" {
		t.Fatalf("vendor session=%q", got)
	}
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Thread-ID", "codex-thread")
	if got := sessionIDFromHeaders(req); got != "" {
		t.Fatalf("non-Codex thread header should be ignored: %q", got)
	}
	req.Header.Set("User-Agent", "codex-cli/1.0")
	if got := sessionIDFromHeaders(req); got != "codex-thread" {
		t.Fatalf("Codex thread=%q", got)
	}
}

func TestWithRequestLogContextPersistsBodyAndHeaderSessions(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := observability.New(db)

	seed := func(requestID string) {
		t.Helper()
		if err := service.RecordCorrelatedRequest(ctx, requestID, nil, observability.RequestRecord{
			StartedAt: 1, FinishedAt: 2, InstanceID: "test-instance", Endpoint: "/v1/chat/completions", StatusCode: http.StatusOK, Result: "success",
		}); err != nil {
			t.Fatal(err)
		}
	}
	seed("lcm_body_session")
	seed("lcm_header_session")

	downstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if strings.Contains(r.Header.Get("X-Test-Request"), "header") {
			w.Header().Set(headerRequestID, "lcm_header_session")
		} else {
			w.Header().Set(headerRequestID, "lcm_body_session")
		}
		w.WriteHeader(http.StatusOK)
	})
	handler := WithRequestLogContext(downstream, service)

	bodyReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"coder","litellm_metadata":{"session_id":"body-session"}}`))
	bodyW := httptest.NewRecorder()
	handler.ServeHTTP(bodyW, bodyReq)
	bodyDetail, err := service.GetRequestLogByRequestID(ctx, "lcm_body_session")
	if err != nil {
		t.Fatal(err)
	}
	if bodyDetail.SessionID != "body-session" {
		t.Fatalf("body session detail=%+v", bodyDetail)
	}

	headerReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"coder"}`))
	headerReq.Header.Set("X-Test-Request", "header")
	headerReq.Header.Set(headerSessionID, "header-session")
	headerW := httptest.NewRecorder()
	handler.ServeHTTP(headerW, headerReq)
	if headerW.Header().Get(headerSessionID) != "header-session" {
		t.Fatalf("response session header=%q", headerW.Header().Get(headerSessionID))
	}
	headerDetail, err := service.GetRequestLogByRequestID(ctx, "lcm_header_session")
	if err != nil {
		t.Fatal(err)
	}
	if headerDetail.SessionID != "header-session" {
		t.Fatalf("header session detail=%+v", headerDetail)
	}
}

func TestHomeAssistantMetadataSessionGroupsRequests(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := observability.New(db)

	requestIDs := []string{"lcm_ha_1", "lcm_ha_2"}
	traceIDs := []string{
		"11111111-2222-4333-8444-555555555555",
		"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee",
	}
	for i, requestID := range requestIDs {
		if err := service.RecordCorrelatedRequest(ctx, requestID, nil, observability.RequestRecord{
			StartedAt:  int64(i + 1),
			FinishedAt: int64(i + 2),
			InstanceID: "test-instance",
			Endpoint:   "/v1/chat/completions",
			StatusCode: http.StatusOK,
			Result:     "success",
			TraceID:    traceIDs[i],
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
	const sessionID = "ha-conversation-test"
	for i, requestID := range requestIDs {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"coder","metadata":{"session_id":"`+sessionID+`"}}`))
		req.Header.Set("X-Test-Request-ID", requestID)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %s status=%d", requestID, w.Code)
		}
		detail, err := service.GetRequestLogByRequestID(ctx, requestID)
		if err != nil {
			t.Fatal(err)
		}
		if detail.SessionID != sessionID || detail.TraceID != traceIDs[i] {
			t.Fatalf("request %s detail=%+v", requestID, detail)
		}
	}

	history, err := service.ListRequestLogs(ctx, observability.RequestFilters{Limit: 10}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].SessionID != sessionID || history[1].SessionID != sessionID || history[0].SessionTotalCount != 2 || history[1].SessionTotalCount != 2 {
		t.Fatalf("history=%+v", history)
	}
	sessionRows, err := service.ListRequestLogs(ctx, observability.RequestFilters{Limit: 10}, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessionRows) != 2 || sessionRows[0].TraceID == sessionRows[1].TraceID {
		t.Fatalf("session rows=%+v", sessionRows)
	}
}

func TestWithRequestLogContextNilService(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	w := httptest.NewRecorder()
	WithRequestLogContext(next, nil).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if !called || w.Code != http.StatusNoContent {
		t.Fatalf("called=%v status=%d", called, w.Code)
	}
}
