package litellm

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/modelimports"
)

// LiteLLMStore is the persistence boundary needed to synchronize LlamaRack
// instances with LiteLLM and persist LiteLLM-specific manager settings.
type LiteLLMStore interface {
	SyncInstances(context.Context) ([]instances.Instance, error)
	Setting(context.Context, string) (string, bool, error)
	SetSetting(context.Context, string, string) error
	DeleteSetting(context.Context, string) error
}

type sqlLiteLLMStore struct {
	db database.Store
}

func NewLiteLLMStore(db database.Store) LiteLLMStore {
	return &sqlLiteLLMStore{db: db}
}

func (s *sqlLiteLLMStore) SyncInstances(ctx context.Context) ([]instances.Instance, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id,slug,model_id,name,enabled,autoload_enabled,always_on,priority,eviction_enabled,
       system_spillover_enabled,idle_unload_seconds,max_pending_requests,gpu_mode,gpu_devices,
       tensor_split,request_log_mode
FROM instances
WHERE enabled=1
  AND NOT EXISTS (
	SELECT 1 FROM provider_imports pi
	WHERE pi.instance_id=instances.id AND pi.state=?
  )
ORDER BY name,id`, modelimports.StateDownloading)
	if err != nil {
		return nil, database.ClassifyError(err)
	}
	defer rows.Close()

	out := make([]instances.Instance, 0)
	for rows.Next() {
		var item instances.Instance
		var enabled, autoload, alwaysOn, eviction, spillover int
		var devices, split sql.NullString
		if err := rows.Scan(
			&item.ID, &item.Slug, &item.ModelID, &item.Name, &enabled, &autoload, &alwaysOn,
			&item.Priority, &eviction, &spillover, &item.IdleUnloadSeconds, &item.MaxPendingRequests,
			&item.GPUMode, &devices, &split, &item.RequestLogMode,
		); err != nil {
			return nil, database.ClassifyError(err)
		}
		item.Enabled = enabled != 0
		item.Autoload = autoload != 0
		item.AlwaysOn = alwaysOn != 0
		item.EvictionEnabled = eviction != 0
		item.SystemSpilloverEnabled = spillover != 0
		if devices.Valid && strings.TrimSpace(devices.String) != "" {
			for _, device := range strings.Split(devices.String, ",") {
				if device = strings.TrimSpace(device); device != "" {
					item.GPUDevices = append(item.GPUDevices, device)
				}
			}
		}
		if split.Valid {
			item.TensorSplit = split.String
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(err)
	}
	return out, nil
}

func (s *sqlLiteLLMStore) Setting(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT setting_value FROM manager_settings WHERE setting_key=?`, key).Scan(&value)
	if err == nil {
		return value, true, nil
	}
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return "", false, database.ClassifyError(err)
}

func (s *sqlLiteLLMStore) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO manager_settings(setting_key,setting_value,updated_at) VALUES(?,?,?)
ON CONFLICT(setting_key) DO UPDATE
SET setting_value=excluded.setting_value,updated_at=excluded.updated_at`,
		key, value, time.Now().Unix())
	return database.ClassifyError(err)
}

func (s *sqlLiteLLMStore) DeleteSetting(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM manager_settings WHERE setting_key=?`, key)
	return database.ClassifyError(err)
}

var _ LiteLLMStore = (*sqlLiteLLMStore)(nil)
