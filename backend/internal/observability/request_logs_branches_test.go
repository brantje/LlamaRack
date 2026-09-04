package observability

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestLogLifecycleValidationAndRecoveryBranches(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	base := RequestRecord{StartedAt: 10, Endpoint: "/v1/completions", TraceID: "11111111-2222-4333-8444-555555555555", CallType: "completion"}

	if err := s.BeginCorrelatedRequest(ctx, "", base); err == nil {
		t.Fatal("expected empty request ID validation error")
	}
	missingEndpoint := base
	missingEndpoint.Endpoint = ""
	if err := s.BeginCorrelatedRequest(ctx, "lcm_no_endpoint", missingEndpoint); err == nil {
		t.Fatal("expected endpoint validation error")
	}
	missingStart := base
	missingStart.StartedAt = 0
	if err := s.BeginCorrelatedRequest(ctx, "lcm_no_start", missingStart); err == nil {
		t.Fatal("expected started_at validation error")
	}
	if err := s.UpdateCorrelatedRequest(ctx, "", base); err == nil {
		t.Fatal("expected update request ID validation error")
	}
	if err := s.UpdateCorrelatedRequest(ctx, "lcm_missing", base); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing update err=%v", err)
	}
	if err := s.FinalizeCorrelatedRequest(ctx, "", nil, base); err == nil {
		t.Fatal("expected finalize request ID validation error")
	}
	if err := s.FinalizeCorrelatedRequest(ctx, "lcm_no_endpoint", nil, missingEndpoint); err == nil {
		t.Fatal("expected finalize endpoint validation error")
	}
	if _, err := s.GetRequestByRequestID(ctx, ""); err == nil {
		t.Fatal("expected get request ID validation error")
	}

	promptTPS, generationTPS, ttft := 25.5, 12.25, 7.5
	requestBody, responseBody := `{"prompt":"hello"}`, `{"text":"world"}`
	final := base
	final.InstanceID = "coder"
	final.StatusCode = http.StatusCreated
	final.APIKey = &APIKeyRef{ID: "key-2", Name: "secondary", Prefix: "pk_2"}
	final.TTFTMS = &ttft
	final.TokensPerSecond = &generationTPS
	final.PromptTokens = 2
	final.GeneratedTokens = 3
	final.TotalTokens = 5
	final.RequestBody = &requestBody
	final.ResponseBody = &responseBody
	if err := s.FinalizeCorrelatedRequest(ctx, "lcm_complete_recovery", &promptTPS, final); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetRequestByRequestID(ctx, "lcm_complete_recovery")
	if err != nil {
		t.Fatal(err)
	}
	if got.Result != "success" || got.FinishedAt == 0 || got.PromptTokensPerSecond == nil || *got.PromptTokensPerSecond != promptTPS || got.GenerationTokensPerSecond == nil || *got.GenerationTokensPerSecond != generationTPS {
		t.Fatalf("recovered=%+v", got)
	}
	if got.RequestBody == nil || got.ResponseBody == nil {
		t.Fatalf("full bodies missing: %+v", got)
	}

	failed := base
	failed.StatusCode = http.StatusInternalServerError
	if err := s.FinalizeCorrelatedRequest(ctx, "lcm_failed_recovery", nil, failed); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetRequestByRequestID(ctx, "lcm_failed_recovery")
	if err != nil || got.Result != "error" {
		t.Fatalf("failed recovery=%+v err=%v", got, err)
	}
}

func TestRequestLogDetailHandlerValidationBranches(t *testing.T) {
	s := testService(t)
	h := NewCorrelatedRequestHandler(s)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/observability/requests/anything", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method=%d", w.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/observability/requests/", nil)
	req.SetPathValue("request_id", "")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty ID=%d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/observability/requests/missing", nil)
	req.SetPathValue("request_id", "missing")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing=%d body=%s", w.Code, w.Body.String())
	}
}

func TestRequestLogSchemaExistingColumnsAndFailure(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	if err := s.EnsureCorrelationSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureCorrelationSchema(ctx); err != nil {
		t.Fatalf("idempotent schema: %v", err)
	}
	fresh := New(s.db)
	if err := fresh.EnsureCorrelationSchema(ctx); err != nil {
		t.Fatalf("existing schema: %v", err)
	}
}
