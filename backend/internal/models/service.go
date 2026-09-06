package models

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/brantje/llamarack/backend/internal/resourceid"
)

type Model struct {
	ID            string `json:"id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	GGUFPath      string `json:"gguf_path"`
	TotalBytes    int64  `json:"total_bytes"`
	Quantization  string `json:"quantization,omitempty"`
	ContextLength int    `json:"context_length"`

	// Deprecated compatibility fields are intentionally not part of the public
	// management API. Runtime policy is owned by Instance in Phase 5.5.
	PublicID          string `json:"-"`
	Enabled           bool   `json:"-"`
	Autoload          bool   `json:"-"`
	AlwaysOn          bool   `json:"-"`
	Priority          string `json:"-"`
	EvictionEnabled   bool   `json:"-"`
	IdleUnloadSeconds int    `json:"-"`
	RoutingPolicy     string `json:"-"`
}

type Instance struct {
	ID                string   `json:"id"`
	Slug              string   `json:"slug"`
	ModelID           string   `json:"model_id"`
	Name              string   `json:"name"`
	Enabled           bool     `json:"enabled"`
	Autoload          bool     `json:"autoload_enabled"`
	AlwaysOn          bool     `json:"always_on"`
	Priority          string   `json:"priority"`
	EvictionEnabled   bool     `json:"eviction_enabled"`
	IdleUnloadSeconds int      `json:"idle_unload_seconds"`
	GPUMode           string   `json:"gpu_mode"`
	GPUDevices        []string `json:"gpu_devices,omitempty"`
	TensorSplit       string   `json:"tensor_split,omitempty"`
	Preferred         bool     `json:"-"`
}

type CreateModelInput struct {
	Name          string            `json:"name"`
	Slug          string            `json:"slug,omitempty"`
	GGUFPath      string            `json:"gguf_path"`
	ContextLength int               `json:"context_length,omitempty"`
	Options       map[string]string `json:"options,omitempty"`

	// Deprecated request fields retained only so older direct callers/tests keep
	// compiling. The management UI/API no longer uses them as Model policy.
	PublicID          string `json:"model_id,omitempty"`
	Enabled           *bool  `json:"enabled,omitempty"`
	Autoload          *bool  `json:"autoload_enabled,omitempty"`
	AlwaysOn          bool   `json:"always_on,omitempty"`
	Priority          string `json:"priority,omitempty"`
	EvictionEnabled   *bool  `json:"eviction_enabled,omitempty"`
	IdleUnloadSeconds int    `json:"idle_unload_seconds,omitempty"`
	RoutingPolicy     string `json:"routing_policy,omitempty"`
}

type UpdateModelInput struct {
	Name          string            `json:"name"`
	Slug          string            `json:"slug,omitempty"`
	ContextLength int               `json:"context_length,omitempty"`
	Options       map[string]string `json:"options,omitempty"`
}

type Service struct {
	db        *sql.DB
	modelsDir string
}

func New(db *sql.DB, modelsDir string) *Service { return &Service{db: db, modelsDir: modelsDir} }
func (s *Service) DB() *sql.DB                  { return s.db }

func (s *Service) Create(ctx context.Context, in CreateModelInput) (Model, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return Model{}, errors.New("name is required")
	}
	slugSource := strings.TrimSpace(in.Slug)
	if slugSource == "" {
		slugSource = in.Name
	}
	slug := resourceid.Slugify(slugSource)
	if slug == "" {
		return Model{}, errors.New("slug must contain at least one letter or number")
	}
	if in.ContextLength < 0 {
		return Model{}, errors.New("context_length must be zero or greater")
	}
	ggufPath, info, err := s.resolveGGUF(in.GGUFPath)
	if err != nil {
		return Model{}, err
	}
	var existing int
	err = s.db.QueryRowContext(ctx, "SELECT 1 FROM models WHERE gguf_path=? LIMIT 1", ggufPath).Scan(&existing)
	if err == nil {
		return Model{}, errors.New("GGUF file has already been added")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Model{}, err
	}
	m := Model{
		ID:            newID(),
		Slug:          slug,
		Name:          in.Name,
		GGUFPath:      ggufPath,
		TotalBytes:    info.Size(),
		Quantization:  quantFromName(filepath.Base(ggufPath)),
		ContextLength: in.ContextLength,
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Model{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO models(id,slug,name,gguf_path,total_bytes,quantization,context_length) VALUES(?,?,?,?,?,?,?)`,
		m.ID, m.Slug, m.Name, m.GGUFPath, m.TotalBytes, m.Quantization, m.ContextLength); err != nil {
		return Model{}, err
	}
	if err := replaceOptions(ctx, tx, "model_options", "model_id", m.ID, in.Options); err != nil {
		return Model{}, err
	}

	// Compatibility only: pre-Phase-5.5 callers supplied model_id and expected a
	// default worker. Keep the public value as the Instance slug while assigning
	// the compatibility Instance its own durable UUID.
	if legacy := strings.TrimSpace(in.PublicID); legacy != "" {
		legacySlug := resourceid.Slugify(legacy)
		if legacySlug == "" || strings.ContainsAny(legacy, " /\\\t\r\n") {
			return Model{}, errors.New("invalid model_id")
		}
		enabled := true
		if in.Enabled != nil {
			enabled = *in.Enabled
		}
		autoload := true
		if in.Autoload != nil {
			autoload = *in.Autoload
		}
		eviction := true
		if in.EvictionEnabled != nil {
			eviction = *in.EvictionEnabled
		}
		priority := normalizePriority(in.Priority)
		if in.IdleUnloadSeconds < 0 {
			return Model{}, errors.New("idle_unload_seconds must be zero or greater")
		}
		instanceID, err := resourceid.NewUUID()
		if err != nil {
			return Model{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO instances(id,slug,model_id,name,enabled,autoload_enabled,always_on,priority,eviction_enabled,idle_unload_seconds) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			instanceID, legacySlug, m.ID, legacy, boolInt(enabled), boolInt(autoload), boolInt(in.AlwaysOn), priority, boolInt(eviction), in.IdleUnloadSeconds); err != nil {
			return Model{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Model{}, err
	}
	return s.withLegacyPolicy(ctx, m), nil
}

func (s *Service) Update(ctx context.Context, id string, in UpdateModelInput) (Model, error) {
	current, err := s.GetByID(ctx, id)
	if err != nil {
		return Model{}, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return Model{}, errors.New("name is required")
	}
	slug := current.Slug
	if strings.TrimSpace(in.Slug) != "" {
		slug = resourceid.Slugify(in.Slug)
		if slug == "" {
			return Model{}, errors.New("slug must contain at least one letter or number")
		}
	}
	if in.ContextLength < 0 {
		return Model{}, errors.New("context_length must be zero or greater")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Model{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE models SET slug=?,name=?,context_length=?,updated_at=unixepoch() WHERE id=?`, slug, name, in.ContextLength, id)
	if err != nil {
		return Model{}, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return Model{}, sql.ErrNoRows
	}
	if in.Options != nil {
		if err := replaceOptions(ctx, tx, "model_options", "model_id", id, in.Options); err != nil {
			return Model{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Model{}, err
	}
	return s.GetByID(ctx, id)
}

const modelColumns = `id,slug,name,gguf_path,total_bytes,quantization,context_length`

func (s *Service) List(ctx context.Context) ([]Model, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+modelColumns+` FROM models ORDER BY name`)
	if err != nil {
		return nil, err
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
		return nil, err
	}
	s.applyLegacyPolicies(ctx, out)
	return out, nil
}

// applyLegacyPolicies preserves the deprecated Model compatibility fields while
// avoiding one SQL query per model. Query/scan failures are intentionally
// ignored, matching withLegacyPolicy's historical best-effort behavior.
func (s *Service) applyLegacyPolicies(ctx context.Context, models []Model) {
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

func (s *Service) GetByID(ctx context.Context, id string) (Model, error) {
	m, err := scanModel(s.db.QueryRowContext(ctx, `SELECT `+modelColumns+` FROM models WHERE id=?`, id))
	if err != nil {
		return Model{}, err
	}
	return s.withLegacyPolicy(ctx, m), nil
}

func (s *Service) GetBySlug(ctx context.Context, slug string) (Model, error) {
	m, err := scanModel(s.db.QueryRowContext(ctx, `SELECT `+modelColumns+` FROM models WHERE slug=?`, resourceid.Slugify(slug)))
	if err != nil {
		return Model{}, err
	}
	return s.withLegacyPolicy(ctx, m), nil
}

// GetByPublicID is retained for source compatibility. Public inference identity
// is the Instance slug; durable Instance IDs never leak into this compatibility path.
func (s *Service) GetByPublicID(ctx context.Context, id string) (Model, error) {
	var modelID string
	if err := s.db.QueryRowContext(ctx, `SELECT model_id FROM instances WHERE slug=?`, resourceid.Slugify(id)).Scan(&modelID); err != nil {
		return Model{}, err
	}
	m, err := s.GetByID(ctx, modelID)
	if err == nil {
		m.PublicID = resourceid.Slugify(id)
	}
	return m, err
}

func (s *Service) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM models WHERE id=?", id)
	return err
}

func (s *Service) Options(ctx context.Context, modelID string) (map[string]string, error) {
	return readOptions(ctx, s.db, "model_options", "model_id", modelID)
}

// Instances is a compatibility read helper. Instance CRUD/policy ownership lives
// in internal/instances; callers should prefer that service for new code.
func (s *Service) Instances(ctx context.Context, modelID string) ([]Instance, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,slug,model_id,name,enabled,autoload_enabled,always_on,priority,eviction_enabled,idle_unload_seconds,gpu_mode,gpu_devices,tensor_split FROM instances WHERE model_id=? ORDER BY name`, modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Instance
	for rows.Next() {
		var i Instance
		var enabled, autoload, alwaysOn, eviction int
		var devices, tensorSplit sql.NullString
		if err := rows.Scan(&i.ID, &i.Slug, &i.ModelID, &i.Name, &enabled, &autoload, &alwaysOn, &i.Priority, &eviction, &i.IdleUnloadSeconds, &i.GPUMode, &devices, &tensorSplit); err != nil {
			return nil, err
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
	return out, rows.Err()
}

func (s *Service) ModelAbsolutePath(m Model) (string, error) {
	root, err := filepath.Abs(s.modelsDir)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(filepath.Join(root, m.GGUFPath))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("GGUF path escapes models directory")
	}
	return abs, nil
}

func (s *Service) resolveGGUF(path string) (string, os.FileInfo, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil, errors.New("gguf_path is required")
	}
	root, err := filepath.Abs(s.modelsDir)
	if err != nil {
		return "", nil, err
	}
	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return "", nil, err
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", nil, errors.New("GGUF must be inside models directory")
	}
	info, err := os.Stat(candidate)
	if err != nil {
		return "", nil, err
	}
	if info.IsDir() {
		return "", nil, errors.New("GGUF path is a directory")
	}
	if !strings.EqualFold(filepath.Ext(candidate), ".gguf") {
		return "", nil, errors.New("model file must be a GGUF file")
	}
	return rel, info, nil
}

type scanner interface{ Scan(...any) error }
type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}
type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func scanModel(row scanner) (Model, error) {
	var m Model
	var quantization sql.NullString
	if err := row.Scan(&m.ID, &m.Slug, &m.Name, &m.GGUFPath, &m.TotalBytes, &quantization, &m.ContextLength); err != nil {
		return Model{}, err
	}
	if quantization.Valid {
		m.Quantization = quantization.String
	}
	return m, nil
}

func replaceOptions(ctx context.Context, tx *sql.Tx, table, idColumn, id string, options map[string]string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE "+idColumn+"=?", id); err != nil {
		return err
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
		if _, err := tx.ExecContext(ctx, "INSERT INTO "+table+"("+idColumn+",option_key,option_value) VALUES(?,?,?)", id, trimmed, options[key]); err != nil {
			return err
		}
	}
	return nil
}

func readOptions(ctx context.Context, q queryer, table, idColumn, id string) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, "SELECT option_key,option_value FROM "+table+" WHERE "+idColumn+"=? ORDER BY option_key", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (s *Service) withLegacyPolicy(ctx context.Context, m Model) Model {
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

func normalizePriority(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "low":
		return "low"
	case "high":
		return "high"
	default:
		return "normal"
	}
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

var quantRE = regexp.MustCompile(`(?i)(IQ[1-4]_[A-Z0-9]+|Q[2-8](?:_K(?:_[SML])?|_[01])?|BF16|F16|F32)`)

func quantFromName(name string) string {
	m := quantRE.FindString(name)
	return strings.ToUpper(m)
}
