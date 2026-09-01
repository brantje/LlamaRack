package observability

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestOpenAIResponsePersistenceAndDelete(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	body := `{"id":"resp_keep","object":"response"}`
	if err := s.RecordCorrelatedRequest(ctx, "lr_keep", nil, RequestRecord{
		StartedAt: 10, FinishedAt: 20, InstanceID: "one", Endpoint: "/v1/responses",
		StatusCode: 200, Result: "success", RequestBody: strPtr(`{"input":"hi"}`), ResponseBody: &body,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOpenAIResponseID(ctx, "lr_keep", "resp_keep"); err != nil {
		t.Fatal(err)
	}
	stored, err := s.GetStoredOpenAIResponse(ctx, "resp_keep")
	if err != nil || stored.Deleted || stored.ResponseBody == nil || *stored.ResponseBody != body {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	if err := s.MarkOpenAIResponseDeleted(ctx, "resp_keep"); err != nil {
		t.Fatal(err)
	}
	stored, err = s.GetStoredOpenAIResponse(ctx, "resp_keep")
	if err != nil || !stored.Deleted || stored.ResponseBody == nil {
		t.Fatalf("deleted flag should keep bodies=%+v err=%v", stored, err)
	}
	if err := s.MarkOpenAIResponseDeleted(ctx, "resp_keep"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("second delete=%v", err)
	}
}

func TestDuplicateOpenAIResponseID(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	for i, requestID := range []string{"lr_a", "lr_b"} {
		if err := s.RecordCorrelatedRequest(ctx, requestID, nil, RequestRecord{
			StartedAt: int64(i + 1), FinishedAt: int64(i + 2), InstanceID: "one", Endpoint: "/v1/responses",
			StatusCode: 200, Result: "success",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetOpenAIResponseID(ctx, "lr_a", "resp_shared"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOpenAIResponseID(ctx, "lr_b", "resp_shared"); !errors.Is(err, ErrDuplicateOpenAIResponseID) {
		t.Fatalf("duplicate=%v", err)
	}
}

func TestOpenAIResponseEdgeErrors(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	if err := s.SetOpenAIResponseID(ctx, "", "resp"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetStoredOpenAIResponse(ctx, ""); err == nil {
		t.Fatal("empty get")
	}
	if err := s.MarkOpenAIResponseDeleted(ctx, ""); err == nil {
		t.Fatal("empty delete")
	}
	if err := s.MarkOpenAIResponseDeleted(ctx, "missing"); err == nil {
		t.Fatal("missing delete")
	}
}

func strPtr(value string) *string { return &value }
