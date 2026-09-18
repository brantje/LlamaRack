package models

import (
	"github.com/brantje/llamarack/backend/internal/database"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
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
	db       database.Store
	store    ModelStore
	modelsDir string
}

func New(db database.Store, modelsDir string) *Service {
	return &Service{db: db, store: NewModelStore(db), modelsDir: modelsDir}
}
func (s *Service) DB() database.Store { return s.db }

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
	exists, err := s.store.PathExists(ctx, ggufPath)
	if err != nil {
		return Model{}, err
	}
	if exists {
		return Model{}, errors.New("GGUF file has already been added")
	}
	m := Model{
		ID: newID(), Slug: slug, Name: in.Name, GGUFPath: ggufPath, TotalBytes: info.Size(),
		Quantization: quantFromName(filepath.Base(ggufPath)), ContextLength: in.ContextLength,
	}
	var legacy *LegacyInstanceCreate
	if publicID := strings.TrimSpace(in.PublicID); publicID != "" {
		legacySlug := resourceid.Slugify(publicID)
		if legacySlug == "" || strings.ContainsAny(publicID, " /\\\t\r\n") {
			return Model{}, errors.New("invalid model_id")
		}
		enabled, autoload, eviction := true, true, true
		if in.Enabled != nil {
			enabled = *in.Enabled
		}
		if in.Autoload != nil {
			autoload = *in.Autoload
		}
		if in.EvictionEnabled != nil {
			eviction = *in.EvictionEnabled
		}
		if in.IdleUnloadSeconds < 0 {
			return Model{}, errors.New("idle_unload_seconds must be zero or greater")
		}
		instanceID, err := resourceid.NewUUID()
		if err != nil {
			return Model{}, err
		}
		legacy = &LegacyInstanceCreate{
			ID: instanceID, Slug: legacySlug, Name: publicID, Enabled: enabled, Autoload: autoload,
			AlwaysOn: in.AlwaysOn, Priority: normalizePriority(in.Priority), EvictionEnabled: eviction,
			IdleUnloadSeconds: in.IdleUnloadSeconds,
		}
	}
	if err := s.store.Create(ctx, m, in.Options, legacy); err != nil {
		return Model{}, err
	}
	return s.store.GetByID(ctx, m.ID)
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
	current.Name, current.Slug, current.ContextLength = name, slug, in.ContextLength
	if err := s.store.Update(ctx, current, in.Options, in.Options != nil); err != nil {
		return Model{}, err
	}
	return s.store.GetByID(ctx, id)
}

func (s *Service) List(ctx context.Context) ([]Model, error) {
	return s.store.List(ctx)
}

func (s *Service) GetByID(ctx context.Context, id string) (Model, error) {
	return s.store.GetByID(ctx, id)
}

func (s *Service) GetBySlug(ctx context.Context, slug string) (Model, error) {
	return s.store.GetBySlug(ctx, resourceid.Slugify(slug))
}

// GetByPublicID is retained for source compatibility. Public inference identity
// is the Instance slug; durable Instance IDs never leak into this compatibility path.
func (s *Service) GetByPublicID(ctx context.Context, id string) (Model, error) {
	slug := resourceid.Slugify(id)
	m, err := s.store.GetByPublicID(ctx, slug)
	if err == nil {
		m.PublicID = slug
	}
	return m, err
}

func (s *Service) Delete(ctx context.Context, id string) error {
	return s.store.Delete(ctx, id)
}

func (s *Service) Options(ctx context.Context, modelID string) (map[string]string, error) {
	return s.store.Options(ctx, modelID)
}

// Instances is a compatibility read helper. Instance CRUD/policy ownership lives
// in internal/instances; callers should prefer that service for new code.
func (s *Service) Instances(ctx context.Context, modelID string) ([]Instance, error) {
	return s.store.Instances(ctx, modelID)
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
