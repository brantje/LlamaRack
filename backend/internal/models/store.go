package models

import (
	"context"
	"database/sql"
	"sort"
	"strings"

	"github.com/brantje/llamarack/backend/internal/database"
)

type LegacyInstanceCreate struct {
	ID                string
	Slug              string
	Name              string
	Enabled           bool
	Autoload          bool
	AlwaysOn          bool
	Priority          string
	EvictionEnabled   bool
	IdleUnloadSeconds int
}

// ModelStore owns model persistence, option replacement, and compatibility projections.
type ModelStore interface {
	PathExists(context.Context, string) (bool, error)
	Create(context.Context, Model, map[string]string, *LegacyInstanceCreate) error
	Update(context.Context, Model, map[string]string, bool) error
	List(context.Context) ([]Model, error)
	GetByID(context.Context, string) (Model, error)
	GetBySlug(context.Context, string) (Model, error)
	GetByPublicID(context.Context, string) (Model, error)
	Delete(context.Context, string) error
	Options(context.Context, string) (map[string]string, error)
	Instances(context.Context, string) ([]Instance, error)
	UpdateTotalBytes(context.Context, string, int64) error
	UpdateContextIfZero(context.Context, string, int) error
}

type sqlModelStore struct {
	db database.Store
}

func NewModelStore(db database.Store) ModelStore {
	return &sqlModelStore{db: db}
}

func (s *sqlModelStore) PathExists(ctx context.Context, path string) (bool, error) {
	var value int
	err := s.db.QueryRowContext(ctx, "SELECT 1 FROM models WHERE gguf_path=? LIMIT 1", path).Scan(&value)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, database.ClassifyError(err)
}

func (s *sqlModelStore) Create(ctx context.Context, m Model, options map[string]string, legacy *LegacyInstanceCreate) error {
	tx, err := database.Begin(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO models(id,slug,name,gguf_path,total_bytes,quantization,context_length) VALUES(?,?,?,?,?,?,?)`,
		m.ID, m.Slug, m.Name, m.GGUFPath, m.TotalBytes, m.Quantization, m.ContextLength); err != nil {
		return database.ClassifyError(err)
	}
	if err := replaceModelOptions(ctx, tx, m.ID, options); err != nil {
		return err
	}
	if legacy != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO instances(id,slug,model_id,name,enabled,autoload_enabled,always_on,priority,eviction_enabled,idle_unload_seconds) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			legacy.ID, legacy.Slug, m.ID, legacy.Name, boolInt(legacy.Enabled), boolInt(legacy.Autoload), boolInt(legacy.AlwaysOn), legacy.Priority, boolInt(legacy.EvictionEnabled), legacy.IdleUnloadSeconds); err != nil {
			return database.ClassifyError(err)
		}
	}
	return database.ClassifyError(tx.Commit())
}

func (s *sqlModelStore) Update(ctx context.Context, m Model, options map[string]string, replace bool) error {
	tx, err := database.Begin(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE models SET slug=?,name=?,context_length=?,updated_at=unixepoch() WHERE id=?`, m.Slug, m.Name, m.ContextLength, m.ID)
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
		if err := replaceModelOptions(ctx, tx, m.ID, options); err != nil {
			return err
		}
	}
	return database.ClassifyError(tx.Commit())
}

const modelColumns = `id,slug,name,gguf_path,total_bytes,quantization,context_length`

func (s *sqlModelStore) List(ctx context.Context) ([]Model, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+modelColumns+` FROM models ORDER BY name`)
	if err != nil {
		return nil, database.ClassifyError(err)
	}
	defer rows.Close()
	var out []Model
	for rows.Next() {
		m, err := scanModel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(err)
	}
	s.applyLegacyPolicies(ctx, out)
	return out, nil
}

func (s *sqlModelStore) GetByID(ctx context.Context, id string) (Model, error) {
	m, err := scanModel(s.db.QueryRowContext(ctx, `SELECT `+modelColumns+` FROM models WHERE id=?`, id))
	if err != nil {
		return Model{}, err
	}
	return s.withLegacyPolicy(ctx, m), nil
}

func (s *sqlModelStore) GetBySlug(ctx context.Context, slug string) (Model, error) {
	m, err := scanModel(s.db.QueryRowContext(ctx, `SELECT `+modelColumns+` FROM models WHERE slug=?`, slug))
	if err != nil {
		return Model{}, err
	}
	return s.withLegacyPolicy(ctx, m), nil
}

func (s *sqlModelStore) GetByPublicID(ctx context.Context, slug string) (Model, error) {
	var modelID string
	if err := s.db.QueryRowContext(ctx, `SELECT model_id FROM instances WHERE slug=?`, slug).Scan(&modelID); err != nil {
		return Model{}, database.ClassifyError(err)
	}
	return s.GetByID(ctx, modelID)
}

func (s *sqlModelStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM models WHERE id=?", id)
	return database.ClassifyError(err)
}

func (s *sqlModelStore) Options(ctx context.Context, modelID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT option_key,option_value FROM model_options WHERE model_id=? ORDER BY option_key`, modelID)
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

func (s *sqlModelStore) Instances(ctx context.Context, modelID string) ([]Instance, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,slug,model_id,name,enabled,autoload_enabled,always_on,priority,eviction_enabled,idle_unload_seconds,gpu_mode,gpu_devices,tensor_split FROM instances WHERE model_id=? ORDER BY name`, modelID)
	if err != nil {
		return nil, database.ClassifyError(err)
	}
	defer rows.Close()
	var out []Instance
	for rows.Next() {
		var i Instance
		var enabled, autoload, alwaysOn, eviction int
		var devices, tensorSplit sql.NullString
		if err := rows.Scan(&i.ID, &i.Slug, &i.ModelID, &i.Name, &enabled, &autoload, &alwaysOn, &i.Priority, &eviction, &i.IdleUnloadSeconds, &i.GPUMode, &devices, &tensorSplit); err != nil {
			return nil, database.ClassifyError(err)
		}
		i.Enabled, i.Autoload, i.AlwaysOn, i.EvictionEnabled = enabled != 0, autoload != 0, alwaysOn != 0, eviction != 0
		if devices.Valid && strings.TrimSpace(devices.String) != "" {
			for _, d := range strings.Split(devices.String, ",") {
				if d = strings.TrimSpace(d); d != "" {
					i.GPUDevices = append(i.GPUDevices, d)
				}
			}
		}
		if tensorSplit.Valid {
			i.TensorSplit = tensorSplit.String
		}
		out = append(out, i)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(err)
	}
	return out, nil
}

func (s *sqlModelStore) UpdateTotalBytes(ctx context.Context, id string, total int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE models SET total_bytes=?,updated_at=unixepoch() WHERE id=?`, total, id)
	return database.ClassifyError(err)
}

func (s *sqlModelStore) UpdateContextIfZero(ctx context.Context, id string, contextLength int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE models SET context_length=?,updated_at=unixepoch() WHERE id=? AND context_length=0`, contextLength, id)
	return database.ClassifyError(err)
}

type modelScanner interface{ Scan(...any) error }

func scanModel(row modelScanner) (Model, error) {
	var m Model
	var quantization sql.NullString
	if err := row.Scan(&m.ID, &m.Slug, &m.Name, &m.GGUFPath, &m.TotalBytes, &quantization, &m.ContextLength); err != nil {
		return Model{}, database.ClassifyError(err)
	}
	if quantization.Valid {
		m.Quantization = quantization.String
	}
	return m, nil
}

func replaceModelOptions(ctx context.Context, tx database.Querier, id string, options map[string]string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM model_options WHERE model_id=?`, id); err != nil {
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
		if _, err := tx.ExecContext(ctx, `INSERT INTO model_options(model_id,option_key,option_value) VALUES(?,?,?)`, id, trimmed, options[key]); err != nil {
			return database.ClassifyError(err)
		}
	}
	return nil
}

func (s *sqlModelStore) applyLegacyPolicies(ctx context.Context, models []Model) {
	if len(models) == 0 {
		return
	}
	rows, err := s.db.QueryContext(ctx, `SELECT model_id,slug,enabled,autoload_enabled,always_on,priority,eviction_enabled,idle_unload_seconds FROM instances ORDER BY model_id,created_at,id`)
	if err != nil {
		return
	}
	defer rows.Close()
	indexes := make(map[string]int, len(models))
	for i := range models {
		indexes[models[i].ID] = i
	}
	previousModelID := ""
	for rows.Next() {
		var modelID, publicID, priority string
		var enabled, autoload, alwaysOn, eviction, idleUnloadSeconds int
		if err := rows.Scan(&modelID, &publicID, &enabled, &autoload, &alwaysOn, &priority, &eviction, &idleUnloadSeconds); err != nil {
			return
		}
		if modelID == previousModelID {
			continue
		}
		previousModelID = modelID
		index, ok := indexes[modelID]
		if !ok {
			continue
		}
		m := &models[index]
		m.PublicID = publicID
		m.Enabled = enabled != 0
		m.Autoload = autoload != 0
		m.AlwaysOn = alwaysOn != 0
		m.Priority = priority
		m.EvictionEnabled = eviction != 0
		m.IdleUnloadSeconds = idleUnloadSeconds
		m.RoutingPolicy = "least_active"
	}
}

func (s *sqlModelStore) withLegacyPolicy(ctx context.Context, m Model) Model {
	var enabled, autoload, alwaysOn, eviction int
	err := s.db.QueryRowContext(ctx, `SELECT slug,enabled,autoload_enabled,always_on,priority,eviction_enabled,idle_unload_seconds FROM instances WHERE model_id=? ORDER BY created_at,id LIMIT 1`, m.ID).
		Scan(&m.PublicID, &enabled, &autoload, &alwaysOn, &m.Priority, &eviction, &m.IdleUnloadSeconds)
	if err == nil {
		m.Enabled = enabled != 0
		m.Autoload = autoload != 0
		m.AlwaysOn = alwaysOn != 0
		m.EvictionEnabled = eviction != 0
		m.RoutingPolicy = "least_active"
	}
	return m
}

var _ ModelStore = (*sqlModelStore)(nil)
