-- +goose Up
ALTER TABLE instances ADD COLUMN system_spillover_enabled BIGINT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE instances DROP COLUMN system_spillover_enabled;
