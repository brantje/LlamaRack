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

-- +goose Down
DROP TABLE inference_request_timings;
