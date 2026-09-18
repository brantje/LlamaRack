package instances

import (
	"context"
	"database/sql"
	"sort"
	"strings"

	"github.com/brantje/llamarack/backend/internal/database"
)

// InstanceStore owns durable Instance persistence and option replacement.
type InstanceStore interface {
	RequireModel(context.Context, string) error
	Create(context.Context, Instance, map[string]string) error
	Update(context.Context, string, Instance, map[string]string, bool) error
	GetByID(context.Context, string) (Instance, error)
	GetBySlug(context.Context, string) (Instance, error)
	List(context.Context) ([]Instance, error)
	ListByModel(context.Context, string) ([]Instance, error)
	Options(context.Context, string) (map[string]string, error)
	Delete(context.Context, string) error
}

type sqlInstanceStore struct {
	db database.Store
}

func NewInstanceStore(db database.Store) InstanceStore {
	return &sqlInstanceStore{db: db}
}

func (s *sqlInstanceStore) RequireModel(ctx context.Context, id string) error {
	var exists int
	return database.ClassifyError(s.db.QueryRowContext(ctx, "SELECT 1 FROM models WHERE id=?", id).Scan(&exists))
}

func (s *sqlInstanceStore) Create(ctx context.Context, i Instance, options map[string]string) error {
	tx, err := database.Begin(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO instances(id,slug,model_id,name,enabled,autoload_enabled,always_on,priority,eviction_enabled,system_spillover_enabled,idle_unload_seconds,max_pending_requests,gpu_mode,gpu_devices,tensor_split,request_log_mode) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		i.ID, i.Slug, i.ModelID, i.Name, boolInt(i.Enabled), boolInt(i.Autoload), boolInt(i.AlwaysOn), i.Priority, boolInt(i.EvictionEnabled), boolInt(i.SystemSpilloverEnabled), i.IdleUnloadSeconds, i.MaxPendingRequests, i.GPUMode, joinDevices(i.GPUDevices), nullString(i.TensorSplit), i.RequestLogMode); err != nil {
		return database.ClassifyError(err)
	}
	if err := replaceInstanceOptions(ctx, tx, i.ID, options); err != nil {
		return err
	}
	return database.ClassifyError(tx.Commit())
}

func (s *sqlInstanceStore) Update(ctx context.Context, id string, i Instance, options map[string]string, replace bool) error {
	tx, err := database.Begin(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE instances SET slug=?,model_id=?,name=?,enabled=?,autoload_enabled=?,always_on=?,priority=?,eviction_enabled=?,system_spillover_enabled=?,idle_unload_seconds=?,max_pending_requests=?,gpu_mode=?,gpu_devices=?,tensor_split=?,request_log_mode=?,updated_at=unixepoch() WHERE id=?`,
		i.Slug, i.ModelID, i.Name, boolInt(i.Enabled), boolInt(i.Autoload), boolInt(i.AlwaysOn), i.Priority, boolInt(i.EvictionEnabled), boolInt(i.SystemSpilloverEnabled), i.IdleUnloadSeconds, i.MaxPendingRequests, i.GPUMode, joinDevices(i.GPUDevices), nullString(i.TensorSplit), i.RequestLogMode, id)
	if err != nil {
		return database.ClassifyError(err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return database.ClassifyError(err)
	}
	if n == 0 {
		return database.ClassifyError(sql.ErrNoRows)
	}
	if replace {
		if err := replaceInstanceOptions(ctx, tx, id, options); err != nil {
			return err
		}
	}
	return database.ClassifyError(tx.Commit())
}

const instanceColumns = `id,slug,model_id,name,enabled,autoload_enabled,always_on,priority,eviction_enabled,system_spillover_enabled,idle_unload_seconds,max_pending_requests,gpu_mode,gpu_devices,tensor_split,request_log_mode`

func (s *sqlInstanceStore) GetByID(ctx context.Context, id string) (Instance, error) {
	return scanInstance(s.db.QueryRowContext(ctx, `SELECT `+instanceColumns+` FROM instances WHERE id=?`, id))
}

func (s *sqlInstanceStore) GetBySlug(ctx context.Context, slug string) (Instance, error) {
	return scanInstance(s.db.QueryRowContext(ctx, `SELECT `+instanceColumns+` FROM instances WHERE slug=?`, slug))
}

func (s *sqlInstanceStore) List(ctx context.Context) ([]Instance, error) {
	return s.list(ctx, `SELECT `+instanceColumns+` FROM instances ORDER BY name,id`)
}

func (s *sqlInstanceStore) ListByModel(ctx context.Context, modelID string) ([]Instance, error) {
	return s.list(ctx, `SELECT `+instanceColumns+` FROM instances WHERE model_id=? ORDER BY name,id`, modelID)
}

func (s *sqlInstanceStore) list(ctx context.Context, query string, args ...any) ([]Instance, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, database.ClassifyError(err)
	}
	defer rows.Close()
	var out []Instance
	for rows.Next() {
		i, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(err)
	}
	return out, nil
}

func (s *sqlInstanceStore) Options(ctx context.Context, id string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT option_key,option_value FROM instance_options WHERE instance_id=? ORDER BY option_key`, id)
	if err != nil {
		return nil, database.ClassifyError(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, database.ClassifyError(err)
		}
		out[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(err)
	}
	return out, nil
}

func (s *sqlInstanceStore) Delete(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM instances WHERE id=?`, id)
	if err != nil {
		return database.ClassifyError(err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return database.ClassifyError(err)
	}
	if n == 0 {
		return database.ClassifyError(sql.ErrNoRows)
	}
	return nil
}

type instanceScanner interface{ Scan(...any) error }

func scanInstance(row instanceScanner) (Instance, error) {
	var i Instance
	var enabled, autoload, alwaysOn, eviction, spillover int
	var devices, split sql.NullString
	if err := row.Scan(&i.ID, &i.Slug, &i.ModelID, &i.Name, &enabled, &autoload, &alwaysOn, &i.Priority, &eviction, &spillover, &i.IdleUnloadSeconds, &i.MaxPendingRequests, &i.GPUMode, &devices, &split, &i.RequestLogMode); err != nil {
		return Instance{}, database.ClassifyError(err)
	}
	i.Enabled = enabled != 0
	i.Autoload = autoload != 0
	i.AlwaysOn = alwaysOn != 0
	i.EvictionEnabled = eviction != 0
	i.SystemSpilloverEnabled = spillover != 0
	if devices.Valid {
		for _, value := range strings.Split(devices.String, ",") {
			if value = strings.TrimSpace(value); value != "" {
				i.GPUDevices = append(i.GPUDevices, value)
			}
		}
	}
	if split.Valid {
		i.TensorSplit = split.String
	}
	return i, nil
}

func replaceInstanceOptions(ctx context.Context, tx database.Querier, id string, options map[string]string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM instance_options WHERE instance_id=?`, id); err != nil {
		return database.ClassifyError(err)
	}
	keys := make([]string, 0, len(options))
	for key := range options {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO instance_options(instance_id,option_key,option_value) VALUES(?,?,?)`, id, trimmed, options[key]); err != nil {
			return database.ClassifyError(err)
		}
	}
	return nil
}

var _ InstanceStore = (*sqlInstanceStore)(nil)
