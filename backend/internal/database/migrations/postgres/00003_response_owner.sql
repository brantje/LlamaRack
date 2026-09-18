-- +goose Up
ALTER TABLE inference_requests ADD COLUMN owner_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE inference_requests ADD COLUMN owner_id TEXT NOT NULL DEFAULT '';

UPDATE inference_requests
SET owner_kind='api_key', owner_id=api_key_id
WHERE api_key_id IS NOT NULL AND api_key_id<>'' AND owner_kind='';

-- +goose Down
ALTER TABLE inference_requests DROP COLUMN owner_id;
ALTER TABLE inference_requests DROP COLUMN owner_kind;
