-- +goose Up
ALTER TABLE download_files ADD COLUMN temp_path TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE download_files DROP COLUMN temp_path;
