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

func (s *Service) RegisterArtifact(ctx context.Context, path, displayName string) (Artifact, error) {
	path = strings.TrimSpace(path)
	if path == "" { return Artifact{}, errors.New("path is required") }
	root, err := filepath.Abs(s.modelsDir)
	if err != nil { return Artifact{}, err }
	candidate := path
	if !filepath.IsAbs(candidate) { candidate = filepath.Join(root, candidate) }
	candidate, err = filepath.Abs(candidate)
	if err != nil { return Artifact{}, err }
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) { return Artifact{}, errors.New("artifact must be inside models directory") }
	info, err := os.Stat(candidate)
	if err != nil { return Artifact{}, err }
	if info.IsDir() { return Artifact{}, errors.New("artifact path is a directory") }
	if !strings.EqualFold(filepath.Ext(candidate), ".gguf") { return Artifact{}, errors.New("artifact must be a GGUF file") }
	if strings.TrimSpace(displayName) == "" { displayName = filepath.Base(candidate) }
	a := Artifact{ID: newID(), DisplayName: strings.TrimSpace(displayName), LocalPath: rel, TotalBytes: info.Size(), Quantization: quantFromName(filepath.Base(candidate))}
	_, err = s.db.ExecContext(ctx, "INSERT INTO artifacts(id,display_name,local_path,total_bytes,quantization) VALUES(?,?,?,?,?)", a.ID, a.DisplayName, a.LocalPath, a.TotalBytes, a.Quantization)
	return a, err
}

func (s *Service) ListArtifacts(ctx context.Context) ([]Artifact, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,display_name,local_path,total_bytes,quantization FROM artifacts ORDER BY display_name")
	if err != nil { return nil, err }
	defer rows.Close()
	var out []Artifact
	for rows.Next() {
		var a Artifact
		if err := rows.Scan(&a.ID, &a.DisplayName, &a.LocalPath, &a.TotalBytes, &a.Quantization); err != nil { return nil, err }
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Service) Create(ctx context.Context, in CreateModelInput) (Model, error) {
	in.PublicID = strings.TrimSpace(in.PublicID)
	if in.PublicID == "" || strings.ContainsAny(in.PublicID, " /\\\t\r\n") { return Model{}, errors.New("invalid model_id") }
	if strings.TrimSpace(in.ArtifactID) == "" { return Model{}, errors.New("artifact_id is required") }
	var artifact Artifact
	if err := s.db.QueryRowContext(ctx, "SELECT id,display_name,local_path,total_bytes,quantization FROM artifacts WHERE id=?", in.ArtifactID).Scan(&artifact.ID, &artifact.DisplayName, &artifact.LocalPath, &artifact.TotalBytes, &artifact.Quantization); err != nil { return Model{}, err }
	enabled := true
	if in.Enabled != nil { enabled = *in.Enabled }
	autoload := true
	if in.Autoload != nil { autoload = *in.Autoload }
	priority := strings.ToLower(strings.TrimSpace(in.Priority))
	if priority == "" { priority = "normal" }
	if priority != "low" && priority != "normal" && priority != "high" { return Model{}, errors.New("priority must be low, normal, or high") }
	routing := strings.ToLower(strings.TrimSpace(in.RoutingPolicy))
	if routing == "" { routing = "least_active" }
	allowedRouting := map[string]bool{"least_active": true, "round_robin": true, "preferred": true, "fixed": true, "lowest_load": true}
	if !allowedRouting[routing] { return Model{}, fmt.Errorf("unsupported routing policy %q", routing) }
	m := Model{ID: newID(), PublicID: in.PublicID, DisplayName: strings.TrimSpace(in.DisplayName), ArtifactID: artifact.ID, ArtifactPath: artifact.LocalPath, Enabled: enabled, Autoload: autoload, AlwaysOn: in.AlwaysOn, Priority: priority, RoutingPolicy: routing}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil { return Model{}, err }
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "INSERT INTO models(id,public_id,display_name,artifact_id,enabled,autoload_enabled,always_on,priority,routing_policy) VALUES(?,?,?,?,?,?,?,?,?)", m.ID, m.PublicID, m.DisplayName, m.ArtifactID, boolInt(m.Enabled), boolInt(m.Autoload), boolInt(m.AlwaysOn), m.Priority, m.RoutingPolicy); err != nil { return Model{}, err }
	keys := make([]string, 0, len(in.Options))
	for key := range in.Options { keys = append(keys, key) }
	sort.Strings(keys)
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" { continue }
		if _, err := tx.ExecContext(ctx, "INSERT INTO model_options(model_id,option_key,option_value) VALUES(?,?,?)", m.ID, key, in.Options[key]); err != nil { return Model{}, err }
	}
	instanceID := newID()
	if _, err := tx.ExecContext(ctx, "INSERT INTO instances(id,model_id,name) VALUES(?,?,?)", instanceID, m.ID, "default"); err != nil { return Model{}, err }
	if err := tx.Commit(); err != nil { return Model{}, err }
	return m, nil
}

func (s *Service) List(ctx context.Context) ([]Model, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.id,m.public_id,m.display_name,m.artifact_id,a.local_path,m.enabled,m.autoload_enabled,m.always_on,m.priority,m.routing_policy FROM models m JOIN artifacts a ON a.id=m.artifact_id ORDER BY m.public_id`)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []Model
	for rows.Next() {
		m, err := scanModel(rows)
		if err != nil { return nil, err }
		out = append(out, m)
	}
	return out, rows.Err()
}
func (s *Service) GetByID(ctx context.Context, id string) (Model, error) {
	return scanModel(s.db.QueryRowContext(ctx, `SELECT m.id,m.public_id,m.display_name,m.artifact_id,a.local_path,m.enabled,m.autoload_enabled,m.always_on,m.priority,m.routing_policy FROM models m JOIN artifacts a ON a.id=m.artifact_id WHERE m.id=?`, id))
}
func (s *Service) GetByPublicID(ctx context.Context, id string) (Model, error) {
	return scanModel(s.db.QueryRowContext(ctx, `SELECT m.id,m.public_id,m.display_name,m.artifact_id,a.local_path,m.enabled,m.autoload_enabled,m.always_on,m.priority,m.routing_policy FROM models m JOIN artifacts a ON a.id=m.artifact_id WHERE m.public_id=?`, id))
}
func (s *Service) Delete(ctx context.Context, id string) error { _, err := s.db.ExecContext(ctx, "DELETE FROM models WHERE id=?", id); return err }
func (s *Service) Options(ctx context.Context, modelID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT option_key,option_value FROM model_options WHERE model_id=? ORDER BY option_key", modelID)
	if err != nil { return nil, err }
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() { var k, v string; if err := rows.Scan(&k, &v); err != nil { return nil, err }; out[k] = v }
	return out, rows.Err()
}
func (s *Service) Instances(ctx context.Context, modelID string) ([]Instance, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,model_id,name,enabled,preferred,gpu_mode,gpu_devices,tensor_split FROM instances WHERE model_id=? ORDER BY name", modelID)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []Instance
	for rows.Next() {
		var i Instance
		var enabled, preferred int
		var devices string
		if err := rows.Scan(&i.ID, &i.ModelID, &i.Name, &enabled, &preferred, &i.GPUMode, &devices, &i.TensorSplit); err != nil { return nil, err }
		i.Enabled, i.Preferred = enabled != 0, preferred != 0
		if strings.TrimSpace(devices) != "" { for _, d := range strings.Split(devices, ",") { if d = strings.TrimSpace(d); d != "" { i.GPUDevices = append(i.GPUDevices, d) } } }
		out = append(out, i)
	}
	return out, rows.Err()
}
func (s *Service) ArtifactAbsolutePath(m Model) (string, error) {
	root, err := filepath.Abs(s.modelsDir)
	if err != nil { return "", err }
	abs, err := filepath.Abs(filepath.Join(root, m.ArtifactPath))
	if err != nil { return "", err }
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) { return "", errors.New("artifact path escapes models directory") }
	return abs, nil
}

type scanner interface{ Scan(...any) error }
func scanModel(row scanner) (Model, error) {
	var m Model
	var en, au, ao int
	if err := row.Scan(&m.ID, &m.PublicID, &m.DisplayName, &m.ArtifactID, &m.ArtifactPath, &en, &au, &ao, &m.Priority, &m.RoutingPolicy); err != nil { return Model{}, err }
	m.Enabled, m.Autoload, m.AlwaysOn = en != 0, au != 0, ao != 0
	return m, nil
}
func newID() string { b := make([]byte, 16); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func boolInt(v bool) int { if v { return 1 }; return 0 }

var quantRE = regexp.MustCompile(`(?i)(IQ[1-4]_[A-Z0-9]+|Q[2-8](?:_K(?:_[SML])?|_[01])?|BF16|F16|F32)`)
func quantFromName(name string) string { m := quantRE.FindString(name); return strings.ToUpper(m) }
