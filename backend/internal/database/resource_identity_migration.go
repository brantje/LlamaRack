package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/brantje/llamarack/backend/internal/resourceid"
	"github.com/pressly/goose/v3"
)

const resourceIdentityMigrationVersion int64 = 2

var resourceIdentityMigration = goose.NewGoMigration(resourceIdentityMigrationVersion, &goose.GoFunc{RunTx: upResourceIdentity}, nil)

type legacyInstanceRow struct {
	ID                 string
	ModelID            string
	Name               string
	Enabled            int
	AutoloadEnabled    int
	AlwaysOn           int
	Priority           string
	EvictionEnabled    int
	IdleUnloadSeconds  int
	MaxPendingRequests int
	GPUMode            string
	GPUDevices         sql.NullString
	TensorSplit        sql.NullString
	RequestLogMode     string
	CreatedAt          int64
	UpdatedAt          int64
}

type apiKeyInstanceScope struct {
	ID  string
	IDs []string
}

func upResourceIdentity(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`ALTER TABLE instances ADD COLUMN slug TEXT`,
		`ALTER TABLE models ADD COLUMN slug TEXT`,
		`ALTER TABLE inference_requests ADD COLUMN model_slug TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}

	instances, err := readLegacyInstances(ctx, tx)
	if err != nil {
		return err
	}
	mapping := make(map[string]string, len(instances))
	usedUUIDs := make(map[string]struct{}, len(instances))
	for _, instance := range instances {
		id, err := uniqueRandomUUID(usedUUIDs)
		if err != nil {
			return err
		}
		mapping[instance.ID] = id
		usedUUIDs[id] = struct{}{}
	}

	scopes, err := readAPIKeyScopes(ctx, tx)
	if err != nil {
		return err
	}
	legacyIDs, err := collectLegacyInstanceIDs(ctx, tx)
	if err != nil {
		return err
	}
	for _, scope := range scopes {
		legacyIDs = append(legacyIDs, scope.IDs...)
	}
	for _, oldID := range legacyIDs {
		oldID = strings.TrimSpace(oldID)
		if oldID == "" {
			continue
		}
		if _, ok := mapping[oldID]; ok {
			continue
		}
		id := resourceid.DeterministicUUID("llamarack:legacy-instance:" + oldID)
		if _, collision := usedUUIDs[id]; collision {
			id, err = uniqueRandomUUID(usedUUIDs)
			if err != nil {
				return err
			}
		}
		mapping[oldID] = id
		usedUUIDs[id] = struct{}{}
	}

	if err := backfillModelSlugs(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE inference_requests SET model_slug=instance_id WHERE model_slug=''`); err != nil {
		return err
	}

	for _, instance := range instances {
		newID := mapping[instance.ID]
		if _, err := tx.ExecContext(ctx, `INSERT INTO instances(
			id,slug,model_id,name,enabled,autoload_enabled,always_on,priority,eviction_enabled,
			idle_unload_seconds,max_pending_requests,gpu_mode,gpu_devices,tensor_split,request_log_mode,created_at,updated_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			newID, instance.ID, instance.ModelID, instance.Name, instance.Enabled, instance.AutoloadEnabled,
			instance.AlwaysOn, instance.Priority, instance.EvictionEnabled, instance.IdleUnloadSeconds,
			instance.MaxPendingRequests, instance.GPUMode, nullableValue(instance.GPUDevices), nullableValue(instance.TensorSplit),
			instance.RequestLogMode, instance.CreatedAt, instance.UpdatedAt); err != nil {
			return err
		}
	}

	for oldID, newID := range mapping {
		for _, table := range []string{
			"instance_options",
			"inference_requests",
			"observability_counters",
			"hardware_metric_samples",
			"provider_imports",
			"worker_runtime",
			"playground_lifecycle_events",
		} {
			query := `UPDATE ` + table + ` SET instance_id=? WHERE instance_id=?`
			if _, err := tx.ExecContext(ctx, query, newID, oldID); err != nil {
				return fmt.Errorf("migrate %s instance references: %w", table, err)
			}
		}
	}

	for _, scope := range scopes {
		changed := false
		for index, oldID := range scope.IDs {
			if newID, ok := mapping[strings.TrimSpace(oldID)]; ok {
				scope.IDs[index] = newID
				changed = true
			}
		}
		if !changed {
			continue
		}
		encoded, err := json.Marshal(scope.IDs)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE api_keys SET instance_ids=? WHERE id=?`, string(encoded), scope.ID); err != nil {
			return err
		}
	}

	for _, instance := range instances {
		if _, err := tx.ExecContext(ctx, `DELETE FROM instances WHERE id=?`, instance.ID); err != nil {
			return err
		}
	}

	for _, statement := range []string{
		`CREATE UNIQUE INDEX instances_slug_uidx ON instances(slug)`,
		`CREATE UNIQUE INDEX models_slug_uidx ON models(slug)`,
		`CREATE TRIGGER instances_slug_default_insert AFTER INSERT ON instances WHEN NEW.slug IS NULL OR trim(NEW.slug)='' BEGIN UPDATE instances SET slug=NEW.id WHERE id=NEW.id; END`,
		`CREATE TRIGGER instances_slug_required_update BEFORE UPDATE OF slug ON instances WHEN NEW.slug IS NULL OR trim(NEW.slug)='' BEGIN SELECT RAISE(ABORT,'instance slug is required'); END`,
		`CREATE TRIGGER models_slug_default_insert AFTER INSERT ON models WHEN NEW.slug IS NULL OR trim(NEW.slug)='' BEGIN UPDATE models SET slug=NEW.id WHERE id=NEW.id; END`,
		`CREATE TRIGGER models_slug_required_update BEFORE UPDATE OF slug ON models WHEN NEW.slug IS NULL OR trim(NEW.slug)='' BEGIN SELECT RAISE(ABORT,'model slug is required'); END`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}

	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		var table string
		var rowID sql.NullInt64
		var parent string
		var fkID int
		if err := rows.Scan(&table, &rowID, &parent, &fkID); err != nil {
			return err
		}
		return fmt.Errorf("foreign key check failed: table=%s rowid=%v parent=%s fk=%d", table, rowID, parent, fkID)
	}
	return rows.Err()
}

func readLegacyInstances(ctx context.Context, tx *sql.Tx) ([]legacyInstanceRow, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,model_id,name,enabled,autoload_enabled,always_on,priority,eviction_enabled,
		idle_unload_seconds,max_pending_requests,gpu_mode,gpu_devices,tensor_split,request_log_mode,created_at,updated_at
		FROM instances ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []legacyInstanceRow
	for rows.Next() {
		var item legacyInstanceRow
		if err := rows.Scan(&item.ID, &item.ModelID, &item.Name, &item.Enabled, &item.AutoloadEnabled, &item.AlwaysOn,
			&item.Priority, &item.EvictionEnabled, &item.IdleUnloadSeconds, &item.MaxPendingRequests, &item.GPUMode,
			&item.GPUDevices, &item.TensorSplit, &item.RequestLogMode, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func readAPIKeyScopes(ctx context.Context, tx *sql.Tx) ([]apiKeyInstanceScope, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,instance_ids FROM api_keys ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var scopes []apiKeyInstanceScope
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var ids []string
		if err := json.Unmarshal([]byte(raw), &ids); err != nil {
			return nil, fmt.Errorf("decode api key %s instance_ids: %w", id, err)
		}
		scopes = append(scopes, apiKeyInstanceScope{ID: id, IDs: ids})
	}
	return scopes, rows.Err()
}

func collectLegacyInstanceIDs(ctx context.Context, tx *sql.Tx) ([]string, error) {
	tables := []string{
		"instance_options",
		"inference_requests",
		"observability_counters",
		"hardware_metric_samples",
		"provider_imports",
		"worker_runtime",
		"playground_lifecycle_events",
	}
	seen := map[string]struct{}{}
	for _, table := range tables {
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT instance_id FROM `+table+` WHERE instance_id IS NOT NULL AND instance_id<>''`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			seen[id] = struct{}{}
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

func backfillModelSlugs(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,name FROM models ORDER BY created_at,id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type modelRef struct{ id, name string }
	var models []modelRef
	for rows.Next() {
		var model modelRef
		if err := rows.Scan(&model.id, &model.name); err != nil {
			return err
		}
		models = append(models, model)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	used := map[string]struct{}{}
	for _, model := range models {
		base := resourceid.Slugify(model.name)
		if base == "" {
			base = "model"
		}
		slug := base
		for suffix := 2; ; suffix++ {
			if _, exists := used[slug]; !exists {
				break
			}
			slug = fmt.Sprintf("%s-%d", base, suffix)
		}
		used[slug] = struct{}{}
		if _, err := tx.ExecContext(ctx, `UPDATE models SET slug=? WHERE id=?`, slug, model.id); err != nil {
			return err
		}
	}
	return nil
}

func uniqueRandomUUID(used map[string]struct{}) (string, error) {
	for {
		id, err := resourceid.NewUUID()
		if err != nil {
			return "", err
		}
		if _, exists := used[id]; !exists {
			return id, nil
		}
	}
}

func nullableValue(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}
