package models

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Model struct {
	ID            string `json:"id"`
	PublicID      string `json:"model_id"`
	Name          string `json:"name"`
	GGUFPath      string `json:"gguf_path"`
	TotalBytes    int64  `json:"total_bytes"`
	Quantization  string `json:"quantization,omitempty"`
	Enabled       bool   `json:"enabled"`
	Autoload      bool   `json:"autoload_enabled"`
	AlwaysOn      bool   `json:"always_on"`
	Priority      string `json:"priority"`
	RoutingPolicy string `json:"routing_policy"`
}

type Instance struct {
	ID          string   `json:"id"`
	ModelID     string   `json:"model_id"`
	Name        string   `json:"name"`
	Enabled     bool     `json:"enabled"`
	Preferred   bool     `json:"preferred"`
	GPUMode     string   `json:"gpu_mode"`
	GPUDevices  []string `json:"gpu_devices,omitempty"`
	TensorSplit string   `json:"tensor_split,omitempty"`
}

type CreateModelInput struct {
	PublicID      string            `json:"model_id"`
	Name          string            `json:"name"`
	GGUFPath      string            `json:"gguf_path"`
	Enabled       *bool             `json:"enabled,omitempty"`
	Autoload      *bool             `json:"autoload_enabled,omitempty"`
	AlwaysOn      bool              `json:"always_on"`
	Priority      string            `json:"priority"`
	RoutingPolicy string            `json:"routing_policy"`
	Options       map[string]string `json:"options,omitempty"`
}

type Service struct {
	db        *sql.DB
	modelsDir string
}

func New(db *sql.DB, modelsDir string) *Service { return &Service{db: db, modelsDir: modelsDir} }

func (s *Service) Create(ctx context.Context, in CreateModelInput) (Model, error) {
	in.PublicID = strings.TrimSpace(in.PublicID)
	if in.PublicID == "" || strings.ContainsAny(in.PublicID, " /\\\t\r\n") {
		return Model{}, errors.New("invalid model_id")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return Model{}, errors.New("name is required")
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
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	autoload := true
	if in.Autoload != nil {
		autoload = *in.Autoload
	}
	priority := strings.ToLower(strings.TrimSpace(in.Priority))
	if priority == "" {
		priority = "normal"
	}
	if priority != "low" && priority != "normal" && priority != "high" {
		return Model{}, errors.New("priority must be low, normal, or high")
	}
	routing := strings.ToLower(strings.TrimSpace(in.RoutingPolicy))
	if routing == "" {
		routing = "least_active"
	}
	allowedRouting := map[string]bool{"least_active": true, "round_robin": true, "preferred": true, "fixed": true, "lowest_load": true}
	if !allowedRouting[routing] {
		return Model{}, fmt.Errorf("unsupported routing policy %q", routing)
	}
	m := Model{
		ID:            newID(),
		PublicID:      in.PublicID,
		Name:          in.Name,
		GGUFPath:      ggufPath,
		TotalBytes:    info.Size(),
		Quantization:  quantFromName(filepath.Base(ggufPath)),
		Enabled:       enabled,
		Autoload:      autoload,
		AlwaysOn:      in.AlwaysOn,
		Priority:      priority,
		RoutingPolicy: routing,
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Model{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO models(id,public_id,name,gguf_path,total_bytes,quantization,enabled,autoload_enabled,always_on,priority,routing_policy) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.PublicID, m.Name, m.GGUFPath, m.TotalBytes, m.Quantization, boolInt(m.Enabled), boolInt(m.Autoload), boolInt(m.AlwaysOn), m.Priority, m.RoutingPolicy); err != nil {
		return Model{}, err
	}
	keys := make([]string, 0, len(in.Options))
	for key := range in.Options {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO model_options(model_id,option_key,option_value) VALUES(?,?,?)", m.ID, key, in.Options[key]); err != nil {
			return Model{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO instances(id,model_id,name) VALUES(?,?,?)", newID(), m.ID, "default"); err != nil {
		return Model{}, err
	}
	if err := tx.Commit(); err != nil {
		return Model{}, err
	}
	return m, nil
}

func (s *Service) List(ctx context.Context) ([]Model, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,public_id,name,gguf_path,total_bytes,quantization,enabled,autoload_enabled,always_on,priority,routing_policy FROM models ORDER BY public_id`)
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
	return out, rows.Err()
}

func (s *Service) GetByID(ctx context.Context, id string) (Model, error) {
	return scanModel(s.db.QueryRowContext(ctx, `SELECT id,public_id,name,gguf_path,total_bytes,quantization,enabled,autoload_enabled,always_on,priority,routing_policy FROM models WHERE id=?`, id))
}

func (s *Service) GetByPublicID(ctx context.Context, id string) (Model, error) {
	return scanModel(s.db.QueryRowContext(ctx, `SELECT id,public_id,name,gguf_path,total_bytes,quantization,enabled,autoload_enabled,always_on,priority,routing_policy FROM models WHERE public_id=?`, id))
}

func (s *Service) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM models WHERE id=?", id)
	return err
}

func (s *Service) Options(ctx context.Context, modelID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT option_key,option_value FROM model_options WHERE model_id=? ORDER BY option_key", modelID)
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

func (s *Service) Instances(ctx context.Context, modelID string) ([]Instance, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,model_id,name,enabled,preferred,gpu_mode,gpu_devices,tensor_split FROM instances WHERE model_id=? ORDER BY name", modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Instance
	for rows.Next() {
		var i Instance
		var enabled, preferred int
		var devices, tensorSplit sql.NullString
		if err := rows.Scan(&i.ID, &i.ModelID, &i.Name, &enabled, &preferred, &i.GPUMode, &devices, &tensorSplit); err != nil {
			return nil, err
		}
		i.Enabled, i.Preferred = enabled != 0, preferred != 0
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

func scanModel(row scanner) (Model, error) {
	var m Model
	var quantization sql.NullString
	var en, au, ao int
	if err := row.Scan(&m.ID, &m.PublicID, &m.Name, &m.GGUFPath, &m.TotalBytes, &quantization, &en, &au, &ao, &m.Priority, &m.RoutingPolicy); err != nil {
		return Model{}, err
	}
	if quantization.Valid {
		m.Quantization = quantization.String
	}
	m.Enabled, m.Autoload, m.AlwaysOn = en != 0, au != 0, ao != 0
	return m, nil
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
