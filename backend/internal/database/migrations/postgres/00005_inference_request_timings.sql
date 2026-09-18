-- +goose Up
CREATE TABLE inference_request_timings (
    request_id TEXT PRIMARY KEY REFERENCES inference_request_correlations(request_id) ON DELETE CASCADE,
    prompt_n BIGINT,
    prompt_ms DOUBLE PRECISION,
    prompt_per_second DOUBLE PRECISION,
    prompt_per_token_ms DOUBLE PRECISION,
    predicted_n BIGINT,
    predicted_ms DOUBLE PRECISION,
    predicted_per_second DOUBLE PRECISION,
    predicted_per_token_ms DOUBLE PRECISION,
    cache_n BIGINT,
    draft_n BIGINT,
    draft_n_accepted BIGINT,
    finish_reason TEXT,
    tool_call_count BIGINT
);

CREATE TABLE inference_request_timing_staging (
    request_id TEXT PRIMARY KEY,
    prompt_n BIGINT,
    prompt_ms DOUBLE PRECISION,
    prompt_per_second DOUBLE PRECISION,
    prompt_per_token_ms DOUBLE PRECISION,
    predicted_n BIGINT,
    predicted_ms DOUBLE PRECISION,
    predicted_per_second DOUBLE PRECISION,
    predicted_per_token_ms DOUBLE PRECISION,
    cache_n BIGINT,
    draft_n BIGINT,
    draft_n_accepted BIGINT,
    finish_reason TEXT,
    tool_call_count BIGINT
);

CREATE OR REPLACE FUNCTION promote_inference_request_timing(p_request_id TEXT)
RETURNS void AS $$
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
    WHERE s.request_id=p_request_id AND r.finished_at<>0
    ON CONFLICT(request_id) DO UPDATE SET
        prompt_n=EXCLUDED.prompt_n,prompt_ms=EXCLUDED.prompt_ms,prompt_per_second=EXCLUDED.prompt_per_second,prompt_per_token_ms=EXCLUDED.prompt_per_token_ms,
        predicted_n=EXCLUDED.predicted_n,predicted_ms=EXCLUDED.predicted_ms,predicted_per_second=EXCLUDED.predicted_per_second,predicted_per_token_ms=EXCLUDED.predicted_per_token_ms,
        cache_n=EXCLUDED.cache_n,draft_n=EXCLUDED.draft_n,draft_n_accepted=EXCLUDED.draft_n_accepted,finish_reason=EXCLUDED.finish_reason,tool_call_count=EXCLUDED.tool_call_count;
    DELETE FROM inference_request_timing_staging s
    WHERE s.request_id=p_request_id
      AND EXISTS (
          SELECT 1 FROM inference_request_correlations c
          JOIN inference_requests r ON r.id=c.inference_request_id
          WHERE c.request_id=p_request_id AND r.finished_at<>0
      );
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION inference_request_timing_stage_fn()
RETURNS trigger AS $$
BEGIN
    PERFORM promote_inference_request_timing(NEW.request_id);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER inference_request_timing_stage_after_insert
AFTER INSERT ON inference_request_timing_staging
FOR EACH ROW EXECUTE FUNCTION inference_request_timing_stage_fn();

CREATE TRIGGER inference_request_timing_stage_after_update
AFTER UPDATE ON inference_request_timing_staging
FOR EACH ROW EXECUTE FUNCTION inference_request_timing_stage_fn();

CREATE OR REPLACE FUNCTION inference_request_timing_request_finalize_fn()
RETURNS trigger AS $$
DECLARE request_key TEXT;
BEGIN
    IF NEW.finished_at<>0 THEN
        FOR request_key IN SELECT request_id FROM inference_request_correlations WHERE inference_request_id=NEW.id LOOP
            PERFORM promote_inference_request_timing(request_key);
        END LOOP;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER inference_request_timing_promote_after_request_finalize
AFTER UPDATE OF finished_at ON inference_requests
FOR EACH ROW EXECUTE FUNCTION inference_request_timing_request_finalize_fn();

CREATE OR REPLACE FUNCTION inference_request_timing_correlation_insert_fn()
RETURNS trigger AS $$
BEGIN
    PERFORM promote_inference_request_timing(NEW.request_id);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER inference_request_timing_promote_after_correlation_insert
AFTER INSERT ON inference_request_correlations
FOR EACH ROW EXECUTE FUNCTION inference_request_timing_correlation_insert_fn();

-- +goose Down
DROP TRIGGER IF EXISTS inference_request_timing_promote_after_correlation_insert ON inference_request_correlations;
DROP TRIGGER IF EXISTS inference_request_timing_promote_after_request_finalize ON inference_requests;
DROP TRIGGER IF EXISTS inference_request_timing_stage_after_update ON inference_request_timing_staging;
DROP TRIGGER IF EXISTS inference_request_timing_stage_after_insert ON inference_request_timing_staging;
DROP FUNCTION IF EXISTS inference_request_timing_correlation_insert_fn();
DROP FUNCTION IF EXISTS inference_request_timing_request_finalize_fn();
DROP FUNCTION IF EXISTS inference_request_timing_stage_fn();
DROP FUNCTION IF EXISTS promote_inference_request_timing(TEXT);
DROP TABLE IF EXISTS inference_request_timing_staging;
DROP TABLE IF EXISTS inference_request_timings;
