package modelimports

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/brantje/llamarack/backend/internal/downloads"
	"github.com/brantje/llamarack/backend/internal/huggingface"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/models"
)

const (
	StateDownloading = "DOWNLOADING"
	StateFailed      = "FAILED"
	StateCancelled   = "CANCELLED"
	StateCompleted   = "COMPLETED"
)

type InstanceStarter interface {
	StartInstance(context.Context, string) (string, error)
}

type Service struct {
	db        *sql.DB
	modelsDir string
	models    *models.Service
	instances *instances.Service
	downloads *downloads.Manager
	starter   InstanceStarter
}

type FirstInstanceInput struct {
	Name            string `json:"name"`
	Slug            string `json:"slug"`
	AlwaysOn        bool   `json:"always_on"`
	AutoloadEnabled bool   `json:"autoload_enabled"`
	EvictionEnabled bool   `json:"eviction_enabled"`
	Start           bool   `json:"start"`
}

type PrepareInput struct {
	Name          string             `json:"name"`
	ContextLength int                `json:"context_length"`
	Options       map[string]string  `json:"options,omitempty"`
	FirstInstance FirstInstanceInput `json:"first_instance"`
}

type Status struct {
	ID             string `json:"id"`
	JobID          string `json:"job_id"`
	ModelID        string `json:"model_id"`
	InstanceID     string `json:"instance_id,omitempty"`
	State          string `json:"state"`
	Error          string `json:"error,omitempty"`
	StartWhenReady bool   `json:"start_when_ready"`
}

type PrepareResult struct {
	Model    models.Model       `json:"model"`
	Instance instances.Instance `json:"instance"`
	Download downloads.Job      `json:"download"`
}

func (s *Service) SetInstanceOnChange(fn instances.ChangeNotifier) {
	s.instances.SetOnChange(fn)
}

func New(db *sql.DB, modelsDir string, modelService *models.Service, downloadManager *downloads.Manager, starter InstanceStarter) *Service {
	return &Service{
		db: db, modelsDir: modelsDir, models: modelService, instances: instances.New(db),
		downloads: downloadManager, starter: starter,
	}
}

func (s *Service) Prepare(ctx context.Context, detail huggingface.ModelDetail, artifact huggingface.Artifact, in PrepareInput) (PrepareResult, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return PrepareResult{}, errors.New("name is required")
	}
	if in.ContextLength < 0 {
		return PrepareResult{}, errors.New("context_length must be zero or greater")
	}
	if strings.TrimSpace(in.FirstInstance.Name) == "" {
		return PrepareResult{}, errors.New("instance name is required")
	}
	if strings.TrimSpace(in.FirstInstance.Slug) == "" {
		return PrepareResult{}, errors.New("instance slug is required")
	}
	if !artifact.Complete || artifact.ShardCount <= 0 || len(artifact.Files) < artifact.ShardCount {
		return PrepareResult{}, errors.New("selected GGUF artifact is incomplete")
	}

	job, err := s.downloads.CreateHuggingFace(ctx, detail, artifact)
	if err != nil {
		return PrepareResult{}, err
	}
	mainPath, err := expectedProviderPath(detail.ID, artifact.Files[0].Path)
	if err != nil {
		return PrepareResult{}, err
	}

	model, found, err := s.modelByPath(ctx, mainPath)
	if err != nil {
		return PrepareResult{}, err
	}
	ownsModel := false
	if !found {
		model, err = s.createPendingModel(ctx, mainPath, artifact, name, in.ContextLength, in.Options)
		if err != nil {
			return PrepareResult{}, err
		}
		ownsModel = true
	}

	enabled := job.State == downloads.StateCompleted
	autoload := in.FirstInstance.AutoloadEnabled
	eviction := in.FirstInstance.EvictionEnabled
	instance, err := s.instances.Create(ctx, instances.CreateInput{
		ModelID: model.ID, Name: in.FirstInstance.Name, Slug: in.FirstInstance.Slug,
		Enabled: &enabled, Autoload: &autoload, AlwaysOn: in.FirstInstance.AlwaysOn,
		EvictionEnabled: &eviction,
	})
	if err != nil {
		if ownsModel {
			_ = s.models.Delete(ctx, model.ID)
		}
		return PrepareResult{}, err
	}

	importID, err := randomID()
	if err != nil {
		_ = s.instances.Delete(ctx, instance.ID)
		if ownsModel {
			_ = s.models.Delete(ctx, model.ID)
		}
		return PrepareResult{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO provider_imports(id,job_id,model_id,instance_id,owns_model,start_when_ready,state,error,start_attempted,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,'',0,unixepoch(),unixepoch())`, importID, job.ID, model.ID, instance.ID, boolInt(ownsModel), boolInt(in.FirstInstance.Start), publicState(job.State))
	if err != nil {
		_ = s.instances.Delete(ctx, instance.ID)
		if ownsModel {
			_ = s.models.Delete(ctx, model.ID)
		}
		return PrepareResult{}, err
	}

	if job.State == downloads.StateCompleted {
		_ = s.Reconcile(ctx)
		instance, _ = s.instances.Get(ctx, instance.ID)
	}
	return PrepareResult{Model: model, Instance: instance, Download: job}, nil
}

func (s *Service) List(ctx context.Context) ([]Status, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT pi.id,pi.job_id,pi.model_id,COALESCE(pi.instance_id,''),dj.state,
       CASE WHEN pi.error<>'' THEN pi.error ELSE dj.error END,pi.start_when_ready
FROM provider_imports pi
JOIN download_jobs dj ON dj.id=pi.job_id
ORDER BY pi.created_at DESC,pi.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Status, 0)
	for rows.Next() {
		var item Status
		var start int
		var downloadState string
		if err := rows.Scan(&item.ID, &item.JobID, &item.ModelID, &item.InstanceID, &downloadState, &item.Error, &start); err != nil {
			return nil, err
		}
		item.State = publicState(downloadState)
		item.StartWhenReady = start != 0
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Service) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	_ = s.Reconcile(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.Reconcile(ctx); err != nil {
				slog.Warn("model import reconciliation failed", "error", err)
			}
		}
	}
}

func (s *Service) Reconcile(ctx context.Context) error {
	if err := s.reconcilePrepared(ctx); err != nil {
		return err
	}
	return s.registerUnclaimedCompleted(ctx)
}

func (s *Service) CleanupJob(ctx context.Context, jobID string) error {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT model_id FROM provider_imports WHERE job_id=? AND owns_model=1`, jobID)
	if err != nil {
		return err
	}
	var owned []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		owned = append(owned, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM provider_imports WHERE job_id=?`, jobID); err != nil {
		return err
	}
	for _, modelID := range owned {
		if err := s.models.Delete(ctx, modelID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return nil
}

func (s *Service) reconcilePrepared(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `
SELECT pi.id,pi.model_id,COALESCE(pi.instance_id,''),pi.start_when_ready,pi.start_attempted,dj.state,dj.error
FROM provider_imports pi
JOIN download_jobs dj ON dj.id=pi.job_id
WHERE pi.instance_id IS NOT NULL AND pi.instance_id<>''`)
	if err != nil {
		return err
	}
	type pending struct {
		id, modelID, instanceID, downloadState, downloadError string
		startWhenReady, startAttempted                        bool
	}
	var items []pending
	for rows.Next() {
		var item pending
		var start, attempted int
		if err := rows.Scan(&item.id, &item.modelID, &item.instanceID, &start, &attempted, &item.downloadState, &item.downloadError); err != nil {
			_ = rows.Close()
			return err
		}
		item.startWhenReady, item.startAttempted = start != 0, attempted != 0
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}

	for _, item := range items {
		switch item.downloadState {
		case downloads.StateFailed, downloads.StateCancelled:
			_, err := s.db.ExecContext(ctx, `UPDATE provider_imports SET state=?,error=?,updated_at=unixepoch() WHERE id=?`, publicState(item.downloadState), item.downloadError, item.id)
			if err != nil {
				return err
			}
		case downloads.StateCompleted:
			if err := s.ensureModelFile(item.modelID); err != nil {
				_, _ = s.db.ExecContext(ctx, `UPDATE provider_imports SET state=?,error=?,updated_at=unixepoch() WHERE id=?`, StateFailed, err.Error(), item.id)
				continue
			}
			if _, err := s.db.ExecContext(ctx, `UPDATE instances SET enabled=1,updated_at=unixepoch() WHERE id=?`, item.instanceID); err != nil {
				return err
			}
			result, err := s.db.ExecContext(ctx, `UPDATE provider_imports SET state=?,error='',updated_at=unixepoch() WHERE id=? AND state=?`, StateCompleted, item.id, StateDownloading)
			if err != nil {
				return err
			}
			claimed, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if claimed > 0 {
				s.instances.NotifyChange(ctx, item.instanceID)
			}
			if item.startWhenReady && !item.startAttempted && s.starter != nil {
				_, startErr := s.starter.StartInstance(context.Background(), item.instanceID)
				message := ""
				if startErr != nil {
					message = startErr.Error()
				}
				_, _ = s.db.ExecContext(context.Background(), `UPDATE provider_imports SET start_attempted=1,error=?,updated_at=unixepoch() WHERE id=?`, message, item.id)
			}
		default:
			_, err := s.db.ExecContext(ctx, `UPDATE provider_imports SET state=?,error='',updated_at=unixepoch() WHERE id=?`, StateDownloading, item.id)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) registerUnclaimedCompleted(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `
SELECT dj.id,dj.repo_id,dj.name,dj.quantization
FROM download_jobs dj
WHERE dj.state=? AND dj.updated_at<=unixepoch()-1
  AND NOT EXISTS (SELECT 1 FROM provider_imports pi WHERE pi.job_id=dj.id)
ORDER BY dj.updated_at,dj.id`, downloads.StateCompleted)
	if err != nil {
		return err
	}
	type completed struct{ id, repoID, name, quantization string }
	var jobs []completed
	for rows.Next() {
		var job completed
		if err := rows.Scan(&job.id, &job.repoID, &job.name, &job.quantization); err != nil {
			_ = rows.Close()
			return err
		}
		jobs = append(jobs, job)
	}
	if err := rows.Close(); err != nil {
		return err
	}

	for _, job := range jobs {
		var localPath string
		if err := s.db.QueryRowContext(ctx, `SELECT local_path FROM download_files WHERE job_id=? AND ordinal=0 AND state=?`, job.id, downloads.StateCompleted).Scan(&localPath); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return err
		}
		if strings.TrimSpace(localPath) == "" {
			continue
		}
		model, found, err := s.modelByPath(ctx, filepath.ToSlash(filepath.Clean(filepath.FromSlash(localPath))))
		if err != nil {
			return err
		}
		ownsModel := false
		if !found {
			options := map[string]string{}
			available, err := s.models.AvailableGGUFs(ctx)
			if err != nil {
				return err
			}
			for _, candidate := range available {
				if candidate.Path != filepath.ToSlash(localPath) {
					continue
				}
				for key, value := range candidate.SuggestedOptions {
					options[key] = value
				}
				break
			}
			model, err = s.models.Create(ctx, models.CreateModelInput{
				Name: defaultModelName(job.repoID, job.quantization, job.name), GGUFPath: localPath, Options: options,
			})
			if err != nil {
				// Another path may have registered it between the lookup and create.
				model, found, _ = s.modelByPath(ctx, filepath.ToSlash(localPath))
				if !found {
					slog.Warn("completed download could not be registered as a model", "job_id", job.id, "error", err)
					continue
				}
			} else {
				ownsModel = true
			}
		}
		id, err := randomID()
		if err != nil {
			return err
		}
		_, err = s.db.ExecContext(ctx, `INSERT INTO provider_imports(id,job_id,model_id,instance_id,owns_model,start_when_ready,state,error,start_attempted,created_at,updated_at)
VALUES(?,?,?,NULL,?,0,?,'',1,unixepoch(),unixepoch())`, id, job.id, model.ID, boolInt(ownsModel), StateCompleted)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) createPendingModel(ctx context.Context, mainPath string, artifact huggingface.Artifact, name string, contextLength int, userOptions map[string]string) (models.Model, error) {
	modelID, err := randomID()
	if err != nil {
		return models.Model{}, err
	}
	options, err := s.artifactOptions(artifact)
	if err != nil {
		return models.Model{}, err
	}
	for key, value := range userOptions {
		if key = strings.TrimSpace(key); key != "" {
			options[key] = value
		}
	}
	model := models.Model{
		ID: modelID, Name: name, GGUFPath: mainPath, TotalBytes: artifact.ModelBytes,
		Quantization: artifact.Quantization, ContextLength: contextLength,
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return models.Model{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO models(id,name,gguf_path,total_bytes,quantization,context_length) VALUES(?,?,?,?,?,?)`,
		model.ID, model.Name, model.GGUFPath, model.TotalBytes, nullable(model.Quantization), model.ContextLength); err != nil {
		return models.Model{}, err
	}
	for key, value := range options {
		if _, err := tx.ExecContext(ctx, `INSERT INTO model_options(model_id,option_key,option_value) VALUES(?,?,?)`, model.ID, key, value); err != nil {
			return models.Model{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return models.Model{}, err
	}
	return model, nil
}

func (s *Service) artifactOptions(artifact huggingface.Artifact) (map[string]string, error) {
	options := map[string]string{}
	for _, dependency := range artifact.Dependencies {
		if len(dependency.Files) == 0 {
			continue
		}
		rel, err := expectedProviderPathFromRelative(dependency.Files[0].Path)
		if err != nil {
			return nil, err
		}
		abs, err := filepath.Abs(filepath.Join(s.modelsDir, filepath.FromSlash(rel)))
		if err != nil {
			return nil, err
		}
		switch dependency.Kind {
		case "mmproj":
			if _, exists := options["mmproj"]; !exists {
				options["mmproj"] = abs
			}
		case "mtp":
			if _, exists := options["spec-draft-model"]; !exists {
				options["spec-draft-model"] = abs
				options["spec-type"] = "draft-mtp"
			}
		}
	}
	return options, nil
}

func (s *Service) modelByPath(ctx context.Context, path string) (models.Model, bool, error) {
	path = filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	items, err := s.models.List(ctx)
	if err != nil {
		return models.Model{}, false, err
	}
	for _, model := range items {
		if filepath.ToSlash(filepath.Clean(filepath.FromSlash(model.GGUFPath))) == path {
			return model, true, nil
		}
	}
	return models.Model{}, false, nil
}

func (s *Service) ensureModelFile(modelID string) error {
	model, err := s.models.GetByID(context.Background(), modelID)
	if err != nil {
		return err
	}
	absolute, err := s.models.ModelAbsolutePath(model)
	if err != nil {
		return err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("downloaded model path is not a regular file")
	}
	return nil
}

func expectedProviderPath(repoID, providerPath string) (string, error) {
	parts := strings.Split(strings.TrimSpace(repoID), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", errors.New("invalid Hugging Face repository id")
	}
	rel, err := expectedProviderPathFromRelative(providerPath)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(filepath.Join("huggingface", safeComponent(parts[0]), safeComponent(parts[1]), filepath.FromSlash(rel))), nil
}

// expectedProviderPathFromRelative validates a provider-owned relative GGUF path.
// The caller adds the provider/repository prefix when needed.
func expectedProviderPathFromRelative(providerPath string) (string, error) {
	providerPath = strings.TrimSpace(providerPath)
	if providerPath == "" || strings.HasPrefix(providerPath, "/") || strings.Contains(providerPath, "\\") || !strings.EqualFold(filepath.Ext(providerPath), ".gguf") {
		return "", errors.New("invalid GGUF provider path")
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(providerPath)))
	if clean != providerPath || clean == "." || strings.HasPrefix(clean, "../") {
		return "", errors.New("invalid GGUF provider path")
	}
	return clean, nil
}

func safeComponent(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

func defaultModelName(repoID, quantization, artifactName string) string {
	parts := strings.Split(repoID, "/")
	base := repoID
	if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
		base = parts[1]
	}
	if quantization = strings.TrimSpace(quantization); quantization != "" {
		return strings.TrimSpace(base + " " + quantization)
	}
	name := strings.TrimSuffix(filepath.Base(filepath.FromSlash(artifactName)), filepath.Ext(artifactName))
	if strings.TrimSpace(name) != "" {
		return name
	}
	return base
}

func publicState(downloadState string) string {
	switch downloadState {
	case downloads.StateCompleted:
		return StateCompleted
	case downloads.StateFailed:
		return StateFailed
	case downloads.StateCancelled:
		return StateCancelled
	default:
		return StateDownloading
	}
}

func randomID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate import id: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullable(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
