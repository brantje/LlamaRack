package models

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type Priority string

type Model struct {
	ID                    string   `json:"id"`
	ModelID               string   `json:"model_id"`
	DisplayName           string   `json:"display_name,omitempty"`
	ArtifactID            string   `json:"artifact_id"`
	ArtifactPath          string   `json:"artifact_path,omitempty"`
	Enabled               bool     `json:"enabled"`
	AutoloadEnabled       bool     `json:"autoload_enabled"`
	AlwaysOn              bool     `json:"always_on"`
	IdleTimeoutSeconds    *int64   `json:"idle_timeout_seconds,omitempty"`
	StartupTimeoutSeconds *int64   `json:"startup_timeout_seconds,omitempty"`
	Priority              Priority `json:"priority"`
	RoutingPolicy         string   `json:"routing_policy"`
	CreatedAt             string   `json:"created_at,omitempty"`
	UpdatedAt             string   `json:"updated_at,omitempty"`
}

type Artifact struct {
	ID           string `json:"id"`
	DisplayName  string `json:"display_name"`
	LocalPath    string `json:"local_path"`
	TotalBytes   int64  `json:"total_bytes"`
	Quantization string `json:"quantization,omitempty"`
	Provider     string `json:"provider"`
	Source       string `json:"source,omitempty"`
	Revision     string `json:"revision,omitempty"`
	Completed    bool   `json:"completed"`
	CreatedAt    string `json:"created_at,omitempty"`
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
	ModelID               string            `json:"model_id"`
	DisplayName           string            `json:"display_name"`
	ArtifactID            string            `json:"artifact_id"`
	AutoloadEnabled       *bool             `json:"autoload_enabled,omitempty"`
	AlwaysOn              bool              `json:"always_on"`
	IdleTimeoutSeconds    *int64            `json:"idle_timeout_seconds,omitempty"`
	StartupTimeoutSeconds *int64            `json:"startup_timeout_seconds,omitempty"`
	Priority              Priority          `json:"priority"`
	RoutingPolicy         string            `json:"routing_policy"`
	Options               map[string]string `json:"options,omitempty"`
}

type Service struct {
	db        *sql.DB
	modelsDir string
}

func New(db *sql.DB, modelsDir string) *Service { return &Service{db: db, modelsDir: modelsDir} }

func (s *Service) RegisterLocalArtifact(ctx context.Context, path, displayName string) (Artifact, error) {
	if path == "" {
		return Artifact{}, errors.New("path is required")
	}
	cleanRoot, err := filepath.Abs(s.modelsDir)
	if err != nil {
		return Artifact{}, err
	}
	cleanPath, err := filepath.Abs(path)
	if err != nil {
		return Artifact{}, err
	}
	rel, err := filepath.Rel(cleanRoot, cleanPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Artifact{}, errors.New("artifact must be inside configured models directory")
	}
	if displayName == "" {
		displayName = filepath.Base(cleanPath)
	}
	id := newID()
	_, err = s.db.ExecContext(ctx, `INSERT INTO artifacts(id,display_name,local_path,provider,completed) VALUES(?,?,?,?,1)`, id, displayName, rel, "local")
	if err != nil {
		return Artifact{}, err
	}
	return Artifact{ID: id, DisplayName: displayName, LocalPath: rel, Provider: "local", Completed: true}, nil
}

func (s *Service) ListArtifacts(ctx context.Context) ([]Artifact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,display_name,local_path,total_bytes,COALESCE(quantization,''),provider,COALESCE(source,''),COALESCE(revision,''),completed,created_at FROM artifacts ORDER BY created_at DESC`)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []Artifact
	for rows.Next() {
		var a Artifact
		var completed int
		if err := rows.Scan(&a.ID,&a.DisplayName,&a.LocalPath,&a.TotalBytes,&a.Quantization,&a.Provider,&a.Source,&a.Revision,&completed,&a.CreatedAt); err != nil { return nil, err }
		a.Completed = completed != 0
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Service) Create(ctx context.Context, in CreateModelInput) (Model, error) {
	in.ModelID = strings.TrimSpace(in.ModelID)
	if in.ModelID == "" || strings.ContainsAny(in.ModelID, "\\/\t\r\n ") {
		return Model{}, errors.New("model_id must be non-empty and may not contain whitespace or path separators")
	}
	if in.ArtifactID == "" { return Model{}, errors.New("artifact_id is required") }
	if in.Priority == "" { in.Priority = "normal" }
	if in.Priority != "low" && in.Priority != "normal" && in.Priority != "high" { return Model{}, errors.New("priority must be low, normal, or high") }
	if in.RoutingPolicy == "" { in.RoutingPolicy = "least_active" }
	autoload := true
	if in.AutoloadEnabled != nil { autoload = *in.AutoloadEnabled }

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil { return Model{}, err }
	defer tx.Rollback()
	var completed int
	if err := tx.QueryRowContext(ctx, "SELECT completed FROM artifacts WHERE id=?", in.ArtifactID).Scan(&completed); err != nil || completed == 0 {
		return Model{}, errors.New("artifact does not exist or is incomplete")
	}
	id := newID()
	_, err = tx.ExecContext(ctx, `INSERT INTO models(id,model_id,display_name,artifact_id,autoload_enabled,always_on,idle_timeout_seconds,startup_timeout_seconds,priority,routing_policy) VALUES(?,?,?,?,?,?,?,?,?,?)`, id,in.ModelID,in.DisplayName,in.ArtifactID,boolInt(autoload),boolInt(in.AlwaysOn),in.IdleTimeoutSeconds,in.StartupTimeoutSeconds,in.Priority,in.RoutingPolicy)
	if err != nil { return Model{}, err }
	for key, value := range in.Options {
		if _, err := tx.ExecContext(ctx, "INSERT INTO model_options(model_id,option_key,option_value) VALUES(?,?,?)", id,key,value); err != nil { return Model{}, err }
	}
	instanceID := newID()
	if _, err := tx.ExecContext(ctx, `INSERT INTO instances(id,model_id,name) VALUES(?,?,?)`, instanceID,id,"default"); err != nil { return Model{}, err }
	if err := tx.Commit(); err != nil { return Model{}, err }
	return s.GetByID(ctx, id)
}

func (s *Service) List(ctx context.Context) ([]Model, error) {
	rows, err := s.db.QueryContext(ctx, modelSelect+" ORDER BY m.created_at DESC")
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
	return scanModel(s.db.QueryRowContext(ctx, modelSelect+" WHERE m.id=?", id))
}

func (s *Service) GetByPublicID(ctx context.Context, modelID string) (Model, error) {
	return scanModel(s.db.QueryRowContext(ctx, modelSelect+" WHERE m.model_id=?", modelID))
}

func (s *Service) Options(ctx context.Context, modelID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT option_key,option_value FROM model_options WHERE model_id=?", modelID)
	if err != nil { return nil, err }
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key,value string
		if err := rows.Scan(&key,&value); err != nil { return nil, err }
		out[key] = value
	}
	return out, rows.Err()
}

func (s *Service) ReplaceOptions(ctx context.Context, modelID string, options map[string]string) error {
	tx, err := s.db.BeginTx(ctx,nil)
	if err != nil { return err }
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,"DELETE FROM model_options WHERE model_id=?",modelID); err != nil { return err }
	for key,value := range options {
		if strings.TrimSpace(key)=="" { return errors.New("empty option key") }
		if _, err := tx.ExecContext(ctx,"INSERT INTO model_options(model_id,option_key,option_value) VALUES(?,?,?)",modelID,key,value); err != nil { return err }
	}
	_, err = tx.ExecContext(ctx,"UPDATE models SET updated_at=CURRENT_TIMESTAMP WHERE id=?",modelID)
	if err != nil { return err }
	return tx.Commit()
}

func (s *Service) Instances(ctx context.Context, modelID string) ([]Instance,error) {
	rows, err := s.db.QueryContext(ctx,`SELECT id,model_id,name,enabled,preferred,gpu_mode,COALESCE(gpu_devices,''),COALESCE(tensor_split,'') FROM instances WHERE model_id=? ORDER BY created_at`,modelID)
	if err != nil { return nil,err }
	defer rows.Close()
	var out []Instance
	for rows.Next() {
		var item Instance
		var enabled, preferred int
		var devices string
		if err := rows.Scan(&item.ID,&item.ModelID,&item.Name,&enabled,&preferred,&item.GPUMode,&devices,&item.TensorSplit); err != nil { return nil,err }
		item.Enabled=enabled!=0; item.Preferred=preferred!=0
		if devices!="" { item.GPUDevices=strings.Split(devices,",") }
		out=append(out,item)
	}
	return out,rows.Err()
}

func (s *Service) Delete(ctx context.Context,id string) error {
	_,err:=s.db.ExecContext(ctx,"DELETE FROM models WHERE id=?",id)
	return err
}

func (s *Service) ArtifactAbsolutePath(m Model) (string,error) {
	root,err:=filepath.Abs(s.modelsDir); if err!=nil{return "",err}
	path,err:=filepath.Abs(filepath.Join(root,m.ArtifactPath)); if err!=nil{return "",err}
	rel,err:=filepath.Rel(root,path); if err!=nil||rel==".."||strings.HasPrefix(rel,".."+string(filepath.Separator)){return "",errors.New("artifact path escapes model directory")}
	return path,nil
}

const modelSelect = `SELECT m.id,m.model_id,COALESCE(m.display_name,''),m.artifact_id,a.local_path,m.enabled,m.autoload_enabled,m.always_on,m.idle_timeout_seconds,m.startup_timeout_seconds,m.priority,m.routing_policy,m.created_at,m.updated_at FROM models m JOIN artifacts a ON a.id=m.artifact_id`

type scanner interface{ Scan(dest ...any) error }
func scanModel(row scanner)(Model,error){
	var m Model; var enabled,autoload,always int; var idle,startup sql.NullInt64
	if err:=row.Scan(&m.ID,&m.ModelID,&m.DisplayName,&m.ArtifactID,&m.ArtifactPath,&enabled,&autoload,&always,&idle,&startup,&m.Priority,&m.RoutingPolicy,&m.CreatedAt,&m.UpdatedAt);err!=nil{return Model{},err}
	m.Enabled=enabled!=0;m.AutoloadEnabled=autoload!=0;m.AlwaysOn=always!=0
	if idle.Valid { v:=idle.Int64; m.IdleTimeoutSeconds=&v }; if startup.Valid {v:=startup.Int64;m.StartupTimeoutSeconds=&v}
	return m,nil
}
func boolInt(v bool)int{if v{return 1};return 0}
func newID() string { b:=make([]byte,16); if _,err:=rand.Read(b);err!=nil{return fmt.Sprintf("%d",time.Now().UnixNano())}; return hex.EncodeToString(b) }
