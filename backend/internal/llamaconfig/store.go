package llamaconfig

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/brantje/llamacpp-manager/backend/internal/llamacpp"
)

const DefaultContextSize = "4096"

var managerDefaults = map[string]string{
	"ctx-size": DefaultContextSize,
}

var companionOptions = map[string]bool{
	"mmproj":           true,
	"spec-draft-model": true,
}

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
	apply(result.Values, result.Sources, managerDefaults, "manager-default")

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

	if provider := detectedDefaultsProvider(s.db); provider != nil && strings.TrimSpace(modelID) != "" {
		defaults, detectErr := provider(ctx, modelID)
		if detectErr != nil {
			return Effective{}, detectErr
		}
		for key, value := range defaults {
			if _, resolved := result.Sources[key]; resolved {
				continue
			}
			result.Values[key] = value
			result.Sources[key] = "detected"
		}
	}
	return result, nil
}

func (s *Store) LaunchOptions(ctx context.Context, profile llamacpp.Profile, modelID, instanceID string) (map[string]string, Effective, error) {
	effective, err := s.Effective(ctx, modelID, instanceID)
	if err != nil {
		return nil, Effective{}, err
	}
	if len(profile.Options) == 0 {
		return map[string]string{}, effective, nil
	}
	available := make(map[string]llamacpp.Option, len(profile.Options))
	for _, option := range profile.Options {
		available[option.Key] = option
	}
	launch := map[string]string{}
	for key, value := range effective.Values {
		option, supported := available[key]
		if !supported {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(value), "false") && isBooleanOption(option) {
			inverse := inverseBooleanKey(key)
			inverseOption, ok := available[inverse]
			if !ok || !isBooleanOption(inverseOption) {
				return nil, effective, fmt.Errorf("llama.cpp option %q cannot express explicit false with the discovered %s profile", key, launchProfileLabel(profile))
			}
			launch[inverse] = "true"
			continue
		}
		launch[key] = value
	}
	return launch, effective, nil
}

func isBooleanOption(option llamacpp.Option) bool {
	if option.Kind != "" {
		return option.Kind == "boolean"
	}
	return strings.TrimSpace(option.ValueHint) == ""
}

func inverseBooleanKey(key string) string {
	if strings.HasPrefix(key, "no-") {
		return strings.TrimPrefix(key, "no-")
	}
	return "no-" + key
}

func launchProfileLabel(profile llamacpp.Profile) string {
	if strings.TrimSpace(profile.Version) != "" {
		return profile.Version
	}
	if strings.TrimSpace(profile.Path) != "" {
		return profile.Path
	}
	return "llama-server"
}

func apply(values, sources, layer map[string]string, source string) {
	for key, value := range layer {
		if companionOptions[key] && strings.TrimSpace(value) == "" {
			delete(values, key)
			sources[key] = source
			continue
		}
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