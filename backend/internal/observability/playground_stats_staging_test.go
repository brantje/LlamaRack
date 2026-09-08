package observability

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func stagedStatsFixture() InferenceTurnStats {
	promptN := int64(9)
	predictedN := int64(11)
	cacheN := int64(5)
	predictedMS := 220.0
	finishReason := "stop"
	return InferenceTurnStats{
		PromptN:      &promptN,
		PredictedN:   &predictedN,
		PredictedMS:  &predictedMS,
		CacheN:       &cacheN,
		FinishReason: &finishReason,
	}
}

func finalStatsRecord(now int64) RequestRecord {
	return RequestRecord{
		StartedAt:       now,
		FinishedAt:      now + 10,
		InstanceID:      "target",
		Endpoint:        "/v1/chat/completions",
		StatusCode:      http.StatusOK,
		Result:          "success",
		PromptTokens:    9,
		GeneratedTokens: 11,
		TotalTokens:     20,
	}
}

func assertPromotedStats(t *testing.T, service *Service, requestID string) {
	t.Helper()
	stats, err := service.inferenceTurnStats(context.Background(), requestID)
	if err != nil {
		t.Fatal(err)
	}
	if stats == nil || stats.PredictedMS == nil || *stats.PredictedMS != 220 || stats.CacheN == nil || *stats.CacheN != 5 {
		t.Fatalf("promoted stats=%+v", stats)
	}
	var staged int
	if err := service.db.QueryRow(`SELECT COUNT(*) FROM inference_request_timing_staging WHERE request_id=?`, requestID).Scan(&staged); err != nil {
		t.Fatal(err)
	}
	if staged != 0 {
		t.Fatalf("staged row was not removed for %s", requestID)
	}
}

func TestStageInferenceTurnStatsPromotesWithPendingRequestFinalization(t *testing.T) {
	service := playgroundTestService(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	record := finalStatsRecord(now)
	record.FinishedAt = 0
	if err := service.BeginCorrelatedRequest(ctx, "req-pending-stats", record); err != nil {
		t.Fatal(err)
	}
	if err := service.StageInferenceTurnStats(ctx, "req-pending-stats", stagedStatsFixture()); err != nil {
		t.Fatal(err)
	}
	if stats, err := service.inferenceTurnStats(ctx, "req-pending-stats"); err != nil || stats != nil {
		t.Fatalf("stats must remain staged before finalization: stats=%+v err=%v", stats, err)
	}
	record.FinishedAt = now + 10
	if err := service.FinalizeCorrelatedRequest(ctx, "req-pending-stats", nil, record); err != nil {
		t.Fatal(err)
	}
	assertPromotedStats(t, service, "req-pending-stats")
}

func TestStageInferenceTurnStatsPromotesOnRecoveryCorrelationInsert(t *testing.T) {
	service := playgroundTestService(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	if err := service.StageInferenceTurnStats(ctx, "req-recovery-stats", stagedStatsFixture()); err != nil {
		t.Fatal(err)
	}
	if err := service.FinalizeCorrelatedRequest(ctx, "req-recovery-stats", nil, finalStatsRecord(now)); err != nil {
		t.Fatal(err)
	}
	assertPromotedStats(t, service, "req-recovery-stats")
}

func TestStageInferenceTurnStatsSurvivesBufferedWriteback(t *testing.T) {
	service := playgroundTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.startWriteback(ctx, time.Hour)
	now := time.Now().UnixMilli()
	record := finalStatsRecord(now)
	record.FinishedAt = 0
	if err := service.BeginCorrelatedRequest(ctx, "req-writeback-stats", record); err != nil {
		t.Fatal(err)
	}
	if err := service.StageInferenceTurnStats(ctx, "req-writeback-stats", stagedStatsFixture()); err != nil {
		t.Fatal(err)
	}
	record.FinishedAt = now + 10
	if err := service.FinalizeCorrelatedRequest(ctx, "req-writeback-stats", nil, record); err != nil {
		t.Fatal(err)
	}
	if err := service.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	assertPromotedStats(t, service, "req-writeback-stats")
}
