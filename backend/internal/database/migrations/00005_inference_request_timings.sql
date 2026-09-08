-- +goose Up
CREATE TABLE inference_request_timings (
    request_id TEXT PRIMARY KEY REFERENCES inference_request_correlations(request_id) ON DELETE CASCADE,
    prompt_n INTEGER,
    prompt_ms REAL,
    prompt_per_second REAL,
    prompt_per_token_ms REAL,
    predicted_n INTEGER,
    predicted_ms REAL,
    predicted_per_second REAL,
    predicted_per_token_ms REAL,
    cache_n INTEGER,
    draft_n INTEGER,
    draft_n_accepted INTEGER,
    finish_reason TEXT,
    tool_call_count INTEGER
);

-- Stats are staged before request finalization. This keeps the writeback path
-- non-blocking while the triggers below promote the stats in the same SQLite
-- transaction that makes the correlated request final.
CREATE TABLE inference_request_timing_staging (
    request_id TEXT PRIMARY KEY,
    prompt_n INTEGER,
    prompt_ms REAL,
    prompt_per_second REAL,
    prompt_per_token_ms REAL,
    predicted_n INTEGER,
    predicted_ms REAL,
    predicted_per_second REAL,
    predicted_per_token_ms REAL,
    cache_n INTEGER,
    draft_n INTEGER,
    draft_n_accepted INTEGER,
    finish_reason TEXT,
    tool_call_count INTEGER
);

-- +goose StatementBegin
CREATE TRIGGER inference_request_timing_stage_after_insert
AFTER INSERT ON inference_request_timing_staging
BEGIN
    INSERT INTO inference_request_timings(
        request_id,prompt_n,prompt_ms,prompt_per_second,prompt_per_token_ms,
        predicted_n,predicted_ms,predicted_per_second,predicted_per_token_ms,
        cache_n,draft_n,draft_n_accepted,finish_reason,tool_call_count
    )
    SELECT s.request_id,s.prompt_n,s.prompt_ms,s.prompt_per_second,s.prompt_per_token_ms,
           s.predicted_n,s.predicted_ms,s.predicted_per_second,s.predicted_per_token_ms,
           s.cache_n,s.draft_n,s.draft_n_accepted,s.finish_reason,s.tool_call_count
    FROM inference_request_timing_staging s
    JOIN inference_request_correlations c ON c.request_id=s.request_id
    JOIN inference_requests r ON r.id=c.inference_request_id
    WHERE s.request_id=NEW.request_id AND r.finished_at<>0
    ON CONFLICT(request_id) DO UPDATE SET
        prompt_n=excluded.prompt_n,prompt_ms=excluded.prompt_ms,prompt_per_second=excluded.prompt_per_second,prompt_per_token_ms=excluded.prompt_per_token_ms,
        predicted_n=excluded.predicted_n,predicted_ms=excluded.predicted_ms,predicted_per_second=excluded.predicted_per_second,predicted_per_token_ms=excluded.predicted_per_token_ms,
        cache_n=excluded.cache_n,draft_n=excluded.draft_n,draft_n_accepted=excluded.draft_n_accepted,finish_reason=excluded.finish_reason,tool_call_count=excluded.tool_call_count;
    DELETE FROM inference_request_timing_staging
    WHERE request_id=NEW.request_id
      AND EXISTS (
          SELECT 1 FROM inference_request_correlations c
          JOIN inference_requests r ON r.id=c.inference_request_id
          WHERE c.request_id=NEW.request_id AND r.finished_at<>0
      );
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER inference_request_timing_stage_after_update
AFTER UPDATE ON inference_request_timing_staging
BEGIN
    INSERT INTO inference_request_timings(
        request_id,prompt_n,prompt_ms,prompt_per_second,prompt_per_token_ms,
        predicted_n,predicted_ms,predicted_per_second,predicted_per_token_ms,
        cache_n,draft_n,draft_n_accepted,finish_reason,tool_call_count
    )
    SELECT s.request_id,s.prompt_n,s.prompt_ms,s.prompt_per_second,s.prompt_per_token_ms,
           s.predicted_n,s.predicted_ms,s.predicted_per_second,s.predicted_per_token_ms,
           s.cache_n,s.draft_n,s.draft_n_accepted,s.finish_reason,s.tool_call_count
    FROM inference_request_timing_staging s
    JOIN inference_request_correlations c ON c.request_id=s.request_id
    JOIN inference_requests r ON r.id=c.inference_request_id
    WHERE s.request_id=NEW.request_id AND r.finished_at<>0
    ON CONFLICT(request_id) DO UPDATE SET
        prompt_n=excluded.prompt_n,prompt_ms=excluded.prompt_ms,prompt_per_second=excluded.prompt_per_second,prompt_per_token_ms=excluded.prompt_per_token_ms,
        predicted_n=excluded.predicted_n,predicted_ms=excluded.predicted_ms,predicted_per_second=excluded.predicted_per_second,predicted_per_token_ms=excluded.predicted_per_token_ms,
        cache_n=excluded.cache_n,draft_n=excluded.draft_n,draft_n_accepted=excluded.draft_n_accepted,finish_reason=excluded.finish_reason,tool_call_count=excluded.tool_call_count;
    DELETE FROM inference_request_timing_staging
    WHERE request_id=NEW.request_id
      AND EXISTS (
          SELECT 1 FROM inference_request_correlations c
          JOIN inference_requests r ON r.id=c.inference_request_id
          WHERE c.request_id=NEW.request_id AND r.finished_at<>0
      );
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER inference_request_timing_promote_after_request_finalize
AFTER UPDATE OF finished_at ON inference_requests
WHEN NEW.finished_at<>0
BEGIN
    INSERT INTO inference_request_timings(
        request_id,prompt_n,prompt_ms,prompt_per_second,prompt_per_token_ms,
        predicted_n,predicted_ms,predicted_per_second,predicted_per_token_ms,
        cache_n,draft_n,draft_n_accepted,finish_reason,tool_call_count
    )
    SELECT s.request_id,s.prompt_n,s.prompt_ms,s.prompt_per_second,s.prompt_per_token_ms,
           s.predicted_n,s.predicted_ms,s.predicted_per_second,s.predicted_per_token_ms,
           s.cache_n,s.draft_n,s.draft_n_accepted,s.finish_reason,s.tool_call_count
    FROM inference_request_timing_staging s
    JOIN inference_request_correlations c ON c.request_id=s.request_id
    WHERE c.inference_request_id=NEW.id
    ON CONFLICT(request_id) DO UPDATE SET
        prompt_n=excluded.prompt_n,prompt_ms=excluded.prompt_ms,prompt_per_second=excluded.prompt_per_second,prompt_per_token_ms=excluded.prompt_per_token_ms,
        predicted_n=excluded.predicted_n,predicted_ms=excluded.predicted_ms,predicted_per_second=excluded.predicted_per_second,predicted_per_token_ms=excluded.predicted_per_token_ms,
        cache_n=excluded.cache_n,draft_n=excluded.draft_n,draft_n_accepted=excluded.draft_n_accepted,finish_reason=excluded.finish_reason,tool_call_count=excluded.tool_call_count;
    DELETE FROM inference_request_timing_staging
    WHERE request_id IN (
        SELECT request_id FROM inference_request_correlations WHERE inference_request_id=NEW.id
    );
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER inference_request_timing_promote_after_correlation_insert
AFTER INSERT ON inference_request_correlations
BEGIN
    INSERT INTO inference_request_timings(
        request_id,prompt_n,prompt_ms,prompt_per_second,prompt_per_token_ms,
        predicted_n,predicted_ms,predicted_per_second,predicted_per_token_ms,
        cache_n,draft_n,draft_n_accepted,finish_reason,tool_call_count
    )
    SELECT s.request_id,s.prompt_n,s.prompt_ms,s.prompt_per_second,s.prompt_per_token_ms,
           s.predicted_n,s.predicted_ms,s.predicted_per_second,s.predicted_per_token_ms,
           s.cache_n,s.draft_n,s.draft_n_accepted,s.finish_reason,s.tool_call_count
    FROM inference_request_timing_staging s
    JOIN inference_requests r ON r.id=NEW.inference_request_id
    WHERE s.request_id=NEW.request_id AND r.finished_at<>0
    ON CONFLICT(request_id) DO UPDATE SET
        prompt_n=excluded.prompt_n,prompt_ms=excluded.prompt_ms,prompt_per_second=excluded.prompt_per_second,prompt_per_token_ms=excluded.prompt_per_token_ms,
        predicted_n=excluded.predicted_n,predicted_ms=excluded.predicted_ms,predicted_per_second=excluded.predicted_per_second,predicted_per_token_ms=excluded.predicted_per_token_ms,
        cache_n=excluded.cache_n,draft_n=excluded.draft_n,draft_n_accepted=excluded.draft_n_accepted,finish_reason=excluded.finish_reason,tool_call_count=excluded.tool_call_count;
    DELETE FROM inference_request_timing_staging
    WHERE request_id=NEW.request_id
      AND EXISTS (SELECT 1 FROM inference_requests WHERE id=NEW.inference_request_id AND finished_at<>0);
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER inference_request_timing_promote_after_correlation_insert;
DROP TRIGGER inference_request_timing_promote_after_request_finalize;
DROP TRIGGER inference_request_timing_stage_after_update;
DROP TRIGGER inference_request_timing_stage_after_insert;
DROP TABLE inference_request_timing_staging;
DROP TABLE inference_request_timings;
