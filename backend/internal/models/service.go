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
	"strings"
)

type Artifact struct {
	ID           string `json:"id"`
	DisplayName  string `json:"display_name"`
	LocalPath    string `json:"local_path"`
	TotalBytes   int64  `json:"total_bytes"`
	Quantization string `json:"quantization,omitempty"`
}

type Model struct {
	ID            string `json:"id"`
	PublicID      string `json:"model_id"`
	DisplayName   string `json:"display_name,omitempty"`
	ArtifactID    string `json:"artifact_id"`
	ArtifactPath  string `json:"artifact_path"`
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
	DisplayName   string            `json:"display_name"`
	ArtifactID    string            `json:"artifact_id"`
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

func (s *Service) RegisterArtifact(ctx context.Context, path, displayName string) (Artifact, error) {
	if strings.TrimSpace(path) == "" {
		return Artifact{}, errors.New("path is required")
	}
	root, err := filepath.Abs(s.modelsDir)
	if err != nil {
		return Artifact{}, err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return Artifact{}, err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Artifact{}, errors.New("artifact must be inside models directory")
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Artifact{}, fmt.Errorf("artifact file: %w", err)
	}
	if info.IsDir() {
		return Artifact{}, errors.New("artifact path is a directory")
	}
	if !strings.HasSuffix(strings.ToLower(info.Name()), ".gguf") {
		return Artifact{}, errors.New("artifact must be a .gguf file")
	}
	if displayName == "" {
		displayName = info.Name()
	}
	id := newID()
	q := quantFromName(info.Name())
	_, err = s.db.ExecContext(ctx, "INSERT INTO artifacts(id,display_name,local_path,total_bytes,quantization) VALUES(?,?,?,?,?)", id, displayName, rel, info.Size(), q)
	if err != nil {
		return Artifact{}, err
	}
	return Artifact{ID: id, DisplayName: displayName, LocalPath: rel, TotalBytes: info.Size(), Quantization: q}, nil
}

func (s *Service) ListArtifacts(ctx context.Context) ([]Artifact, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,display_name,local_path,total_bytes,COALESCE(quantization,'') FROM artifacts ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Artifact
	for rows.Next() {
		var a Artifact
		if err := rows.Scan(&a.ID, &a.DisplayName, &a.LocalPath, &a.TotalBytes, &a.Quantization); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Service) Create(ctx context.Context, in CreateModelInput) (Model, error) {
	in.PublicID = strings.TrimSpace(in.PublicID)
	if in.PublicID == "" || strings.ContainsAny(in.PublicID, " \\//\t\r\n") {
		return Model{}, errors.New("invalid model_id")
	}
	if in.ArtifactID == "" {
		return Model{}, errors.New("artifact_id is required")
	}
	if in.Priority == "" {
		in.Priority = "normal"
	}
	if in.Priority != "low" && in.Priority != "normal" && in.Priority != "high" {
		return Model{}, errors.New("invalid priority")
	}
	if in.RoutingPolicy == "" {
		in.RoutingPolicy = "least_active"
	}
	autoload := true
	if in.Autoload != nil {
		autoload = *in.Autoload
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Model{}, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM artifacts WHERE id=?", in.ArtifactID).Scan(&exists); err != nil || exists != 1 {
		return Model{}, errors.New("artifact not found")
	}
	id := newID()
	_, err = tx.ExecContext(ctx, "INSERT INTO models(id,public_id,display_name,artifact_id,autoload,always_on,priority,routing_policy) VALUES(?,?,?,?,?,?,?,?)", id, in.PublicID, in.DisplayName, in.ArtifactID, boolInt(autoload), boolInt(in.AlwaysOn), in.Priority, in.RoutingPolicy)
	if err != nil {
		return Model{}, err
	}
	for k, v := range in.Options {
		if strings.TrimSpace(k) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO model_options(model_id,option_key,option_value) VALUES(?,?,?)", id, k, v); err != nil {
			return Model{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO instances(id,model_id,name) VALUES(?,?,?)", newID(), id, "default"); err != nil {
		return Model{}, err
	}
	if err := tx.Commit(); err != nil {
		return Model{}, err
	}
	return s.GetByID(ctx, id)
}

const modelSelect = `SELECT m.id,m.public_id,COALESCE(m.display_name,''),m.artifact_id,a.local_path,m.enabled,m.autoload,m.always_on,m.priority,m.routing_policy FROM models m JOIN artifacts a ON a.id=m.artifact_id`

func (s *Service) List(ctx context.Context) ([]Model, error) {
	rows, err := s.db.QueryContext(ctx, modelSelect+" ORDER BY m.created_at DESC")
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
	return scanModel(s.db.QueryRowContext(ctx, modelSelect+" WHERE m.id=?", id))
}
func (s *Service) GetByPublicID(ctx context.Context, id string) (Model, error) {
	return scanModel(s.db.QueryRowContext(ctx, modelSelect+" WHERE m.public_id=?", id))
}
func (s *Service) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM models WHERE id=?", id)
	return err
}
func (s *Service) Options(ctx context.Context, id string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT option_key,option_value FROM model_options WHERE model_id=?", id)
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
func (s *Service) Instances(ctx context.Context, id string) ([]Instance, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,model_id,name,enabled,preferred,gpu_mode,COALESCE(gpu_devices,''),COALESCE(tensor_split,'') FROM instances WHERE model_id=? ORDER BY created_at", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Instance
	for rows.Next() {
		var x Instance
		var en, pr int
		var devices string
		if err := rows.Scan(&x.ID, &x.ModelID, &x.Name, &en, &pr, &x.GPUMode, &devices, &x.TensorSplit); err != nil {
			return nil, err
		}
		x.Enabled = en != 0
		x.Preferred = pr != 0
		if devices != "" {
			x.GPUDevices = strings.Split(devices, ",")
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Service) ArtifactAbsolutePath(m Model) (string, error) {
	root, err := filepath.Abs(s.modelsDir)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(filepath.Join(root, m.ArtifactPath))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("artifact path escapes models directory")
	}
	return abs, nil
}

type scanner interface{ Scan(...any) error }

func scanModel(row scanner) (Model, error) {
	var m Model
	var en, au, ao int
	if err := row.Scan(&m.ID, &m.PublicID, &m.DisplayName, &m.ArtifactID, &m.ArtifactPath, &en, &au, &ao, &m.Priority, &m.RoutingPolicy); err != nil {
		return Model{}, err
	}
	m.Enabled = en != 0
	m.Autoload = au != 0
	m.AlwaysOn = ao != 0
	return m, nil
}
func newID() string { b := make([]byte, 16); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

var quantRE = regexp.MustCompile(`(?i)(IQ[1-4]_[A-Z0-9]+|Q[2-8](?:_K_[SML]|_[01])?|BF16|F16|F32)`)

func quantFromName(name string) string { m := quantRE.FindString(name); return strings.ToUpper(m) }
