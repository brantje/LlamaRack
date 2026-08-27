package llamaconfig

import (
	"context"
	"database/sql"
	"sort"
	"strings"

	"github.com/brantje/llamacpp-manager/backend/internal/llamacpp"
)

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

type Effective struct {
	Global   map[string]string `json:"global"`
	Model    map[string]string `json:"model"`
	Instance map[string]string `json:"instance"`
	Values   map[string]string `json:"values"`
	Sources  map[string]string `json:"sources"`
}

func (s *Store) Global(ctx context.Context) (map[string]string, error) {
	return readOptions(ctx, s.db, `SELECT option_key,option_value FROM global_options ORDER BY option_key`)
}

func (s *Store) ReplaceGlobal(ctx context.Context, options map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM global_options`); err != nil {
		return err
	}
	keys := sortedKeys(options)
	for _, key := range keys {
		key = strings.TrimSpace(strings.TrimLeft(key, "-"))
		if key == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO global_options(option_key,option_value) VALUES(?,?)`, key, options[key]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Effective(ctx context.Context, modelID, instanceID string) (Effective, error) {
	result := Effective{
		Global: map[string]string{}, Model: map[string]string{}, Instance: map[string]string{},
		Values: map[string]string{}, Sources: map[string]string{},
	}
	var err error
	result.Global, err = s.Global(ctx)
	if err != nil {
		return Effective{}, err
	}
	apply(result.Values, result.Sources, result.Global, "global")
	if strings.TrimSpace(modelID) != "" {
		result.Model, err = readOptions(ctx, s.db, `SELECT option_key,option_value FROM model_options WHERE model_id=? ORDER BY option_key`, modelID)
		if err != nil {
			return Effective{}, err
		}
		apply(result.Values, result.Sources, result.Model, "model")
	}
	if strings.TrimSpace(instanceID) != "" {
		if strings.TrimSpace(modelID) == "" {
			if err := s.db.QueryRowContext(ctx, `SELECT model_id FROM instances WHERE id=?`, instanceID).Scan(&modelID); err != nil {
				return Effective{}, err
			}
			result.Model, err = readOptions(ctx, s.db, `SELECT option_key,option_value FROM model_options WHERE model_id=? ORDER BY option_key`, modelID)
			if err != nil {
				return Effective{}, err
			}
			apply(result.Values, result.Sources, result.Model, "model")
		}
		result.Instance, err = readOptions(ctx, s.db, `SELECT option_key,option_value FROM instance_options WHERE instance_id=? ORDER BY option_key`, instanceID)
		if err != nil {
			return Effective{}, err
		}
		apply(result.Values, result.Sources, result.Instance, "instance")
	}
	return result, nil
}

// LaunchOptions returns only flags that the currently discovered llama-server
// actually supports. Persisted options that disappeared after a binary change
// remain visible/configured but are not passed to the process.
func (s *Store) LaunchOptions(ctx context.Context, profile llamacpp.Profile, modelID, instanceID string) (map[string]string, Effective, error) {
	effective, err := s.Effective(ctx, modelID, instanceID)
	if err != nil {
		return nil, Effective{}, err
	}
	if len(profile.Options) == 0 {
		return map[string]string{}, effective, nil
	}
	supported := make(map[string]bool, len(profile.Options))
	for _, option := range profile.Options {
		supported[option.Key] = true
	}
	launch := map[string]string{}
	for key, value := range effective.Values {
		if supported[key] {
			launch[key] = value
		}
	}
	return launch, effective, nil
}

func apply(values, sources, layer map[string]string, source string) {
	for key, value := range layer {
		values[key] = value
		sources[key] = source
	}
}

func readOptions(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, query string, args ...any) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, rows.Err()
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
