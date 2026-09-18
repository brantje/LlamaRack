package settings

import (
	"context"

	"github.com/brantje/llamarack/backend/internal/database"
)

// SettingStore is the persistence boundary for manager settings.
type SettingStore interface {
	Get(context.Context, string) (string, error)
	Set(context.Context, string, string, int64) error
}

type sqlSettingStore struct {
	db database.Store
}

func NewSettingStore(db database.Store) SettingStore {
	return &sqlSettingStore{db: db}
}

func (s *sqlSettingStore) Get(ctx context.Context, key string) (string, error) {
	var value string
	if err := s.db.QueryRowContext(ctx, "SELECT setting_value FROM manager_settings WHERE setting_key=?", key).Scan(&value); err != nil {
		return "", database.ClassifyError(err)
	}
	return value, nil
}

func (s *sqlSettingStore) Set(ctx context.Context, key, value string, updatedAt int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO manager_settings(setting_key,setting_value,updated_at) VALUES(?,?,?)
		ON CONFLICT(setting_key) DO UPDATE SET setting_value=excluded.setting_value,updated_at=excluded.updated_at`, key, value, updatedAt)
	return database.ClassifyError(err)
}

var _ SettingStore = (*sqlSettingStore)(nil)
