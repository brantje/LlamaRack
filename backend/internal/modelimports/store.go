package modelimports

import (
	"context"
	"database/sql"
	"strings"

	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/downloads"
	"github.com/brantje/llamarack/backend/internal/models"
)

type importCreate struct {
	ID, JobID, ModelID, InstanceID string
	OwnsModel, StartWhenReady       bool
	State                           string
	StartAttempted                  bool
}

type pendingImport struct {
	ID, ModelID, InstanceID, DownloadState, DownloadError string
	StartWhenReady                                         bool
}

type completedDownload struct {
	ID, RepoID, Name, Quantization string
}

type linkedImport struct {
	ModelID, InstanceID string
	OwnsModel           bool
}

// Store owns provider-import persistence and model-option repair state.
type Store interface {
	CreateImport(context.Context, importCreate) error
	List(context.Context, bool) ([]Status, error)
	OwnedModelIDs(context.Context, string) ([]string, error)
	Linked(context.Context, string) ([]linkedImport, error)
	DeleteByJob(context.Context, string) error
	Prepared(context.Context) ([]pendingImport, error)
	SetState(context.Context, string, string, string) error
	CompletePrepared(context.Context, string, string) (bool, error)
	ClaimStartAttempt(context.Context, string) (bool, error)
	RecordStartAttemptResult(context.Context, string, string) error
	UnclaimedCompleted(context.Context) ([]completedDownload, error)
	CompletedMainPath(context.Context, string) (string, bool, error)
	CreatePendingModel(context.Context, models.Model, map[string]string) error
	GetModelOption(context.Context, string, string) (string, bool, error)
	InsertModelOption(context.Context, string, string, string) error
	UpdateModelOption(context.Context, string, string, string) error
}

type sqlStore struct{ db database.Store }

func NewStore(db database.Store) Store { return &sqlStore{db: db} }

func (s *sqlStore) CreateImport(ctx context.Context, in importCreate) error {
	var instance any
	if in.InstanceID != "" {
		instance = in.InstanceID
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO provider_imports(
		id,job_id,model_id,instance_id,owns_model,start_when_ready,state,error,start_attempted,created_at,updated_at
	) VALUES(?,?,?,?,?,?,?,'',?,unixepoch(),unixepoch())`,
		in.ID, in.JobID, in.ModelID, instance, boolInt(in.OwnsModel), boolInt(in.StartWhenReady), in.State, boolInt(in.StartAttempted))
	return database.ClassifyError(err)
}

func (s *sqlStore) List(ctx context.Context, resolved bool) ([]Status, error) {
	stateExpr := "dj.state"
	if resolved {
		stateExpr = "pi.state"
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT pi.id,pi.job_id,COALESCE(pi.model_id,''),COALESCE(pi.instance_id,''),`+stateExpr+`,
       CASE WHEN pi.error<>'' THEN pi.error ELSE dj.error END,pi.start_when_ready
FROM provider_imports pi JOIN download_jobs dj ON dj.id=pi.job_id
ORDER BY pi.created_at DESC,pi.id DESC`)
	if err != nil {
		return nil, database.ClassifyError(err)
	}
	defer rows.Close()
	out := make([]Status, 0)
	for rows.Next() {
		var item Status
		var start int
		if err := rows.Scan(&item.ID, &item.JobID, &item.ModelID, &item.InstanceID, &item.State, &item.Error, &start); err != nil {
			return nil, database.ClassifyError(err)
		}
		item.StartWhenReady = start != 0
		if resolved {
			item.State = strings.ToUpper(strings.TrimSpace(item.State))
		} else {
			item.State = publicState(item.State)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(err)
	}
	return out, nil
}

func (s *sqlStore) OwnedModelIDs(ctx context.Context, jobID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT model_id FROM provider_imports WHERE job_id=? AND owns_model=1`, jobID)
	if err != nil {
		return nil, database.ClassifyError(err)
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, database.ClassifyError(err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(err)
	}
	return out, nil
}

func (s *sqlStore) Linked(ctx context.Context, jobID string) ([]linkedImport, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT COALESCE(model_id,''),COALESCE(instance_id,''),owns_model FROM provider_imports WHERE job_id=?`, jobID)
	if err != nil {
		return nil, database.ClassifyError(err)
	}
	defer rows.Close()
	out := make([]linkedImport, 0)
	for rows.Next() {
		var item linkedImport
		var owns int
		if err := rows.Scan(&item.ModelID, &item.InstanceID, &owns); err != nil {
			return nil, database.ClassifyError(err)
		}
		item.OwnsModel = owns != 0
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(err)
	}
	return out, nil
}

func (s *sqlStore) DeleteByJob(ctx context.Context, jobID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM provider_imports WHERE job_id=?`, jobID)
	return database.ClassifyError(err)
}

func (s *sqlStore) Prepared(ctx context.Context) ([]pendingImport, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT pi.id,pi.model_id,COALESCE(pi.instance_id,''),pi.start_when_ready,dj.state,dj.error
FROM provider_imports pi JOIN download_jobs dj ON dj.id=pi.job_id
WHERE pi.instance_id IS NOT NULL AND pi.instance_id<>''`)
	if err != nil {
		return nil, database.ClassifyError(err)
	}
	defer rows.Close()
	out := make([]pendingImport, 0)
	for rows.Next() {
		var item pendingImport
		var start int
		if err := rows.Scan(&item.ID, &item.ModelID, &item.InstanceID, &start, &item.DownloadState, &item.DownloadError); err != nil {
			return nil, database.ClassifyError(err)
		}
		item.StartWhenReady = start != 0
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(err)
	}
	return out, nil
}

func (s *sqlStore) SetState(ctx context.Context, id, state, message string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE provider_imports SET state=?,error=?,updated_at=unixepoch() WHERE id=?`, state, message, id)
	return database.ClassifyError(err)
}

func (s *sqlStore) CompletePrepared(ctx context.Context, id, instanceID string) (bool, error) {
	tx, err := database.Begin(ctx, s.db)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE instances SET enabled=1,updated_at=unixepoch() WHERE id=?`, instanceID); err != nil {
		return false, database.ClassifyError(err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE provider_imports SET state=?,error='',updated_at=unixepoch() WHERE id=? AND state=?`, StateCompleted, id, StateDownloading)
	if err != nil {
		return false, database.ClassifyError(err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, database.ClassifyError(err)
	}
	if err := tx.Commit(); err != nil {
		return false, database.ClassifyError(err)
	}
	return n > 0, nil
}

func (s *sqlStore) ClaimStartAttempt(ctx context.Context, id string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `
UPDATE provider_imports
SET start_attempted=1,updated_at=unixepoch()
WHERE id=? AND start_when_ready=1 AND start_attempted=0 AND state=?`, id, StateCompleted)
	if err != nil {
		return false, database.ClassifyError(err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, database.ClassifyError(err)
	}
	return n > 0, nil
}

func (s *sqlStore) RecordStartAttemptResult(ctx context.Context, id, message string) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE provider_imports
SET error=?,updated_at=unixepoch()
WHERE id=? AND start_attempted=1`, message, id)
	return database.ClassifyError(err)
}

func (s *sqlStore) UnclaimedCompleted(ctx context.Context) ([]completedDownload, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT dj.id,dj.repo_id,dj.name,dj.quantization
FROM download_jobs dj
WHERE dj.state=? AND dj.updated_at<=unixepoch()-1
  AND NOT EXISTS (SELECT 1 FROM provider_imports pi WHERE pi.job_id=dj.id)
ORDER BY dj.updated_at,dj.id`, downloads.StateCompleted)
	if err != nil {
		return nil, database.ClassifyError(err)
	}
	defer rows.Close()
	out := make([]completedDownload, 0)
	for rows.Next() {
		var item completedDownload
		if err := rows.Scan(&item.ID, &item.RepoID, &item.Name, &item.Quantization); err != nil {
			return nil, database.ClassifyError(err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(err)
	}
	return out, nil
}

func (s *sqlStore) CompletedMainPath(ctx context.Context, jobID string) (string, bool, error) {
	var path string
	err := s.db.QueryRowContext(ctx, `SELECT local_path FROM download_files WHERE job_id=? AND ordinal=0 AND state=?`, jobID, downloads.StateCompleted).Scan(&path)
	if err == nil {
		return path, true, nil
	}
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return "", false, database.ClassifyError(err)
}

func (s *sqlStore) CreatePendingModel(ctx context.Context, model models.Model, options map[string]string) error {
	tx, err := database.Begin(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var quant any
	if model.Quantization != "" {
		quant = model.Quantization
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO models(id,name,gguf_path,total_bytes,quantization,context_length) VALUES(?,?,?,?,?,?)`,
		model.ID, model.Name, model.GGUFPath, model.TotalBytes, quant, model.ContextLength); err != nil {
		return database.ClassifyError(err)
	}
	for key, value := range options {
		if _, err := tx.ExecContext(ctx, `INSERT INTO model_options(model_id,option_key,option_value) VALUES(?,?,?)`, model.ID, key, value); err != nil {
			return database.ClassifyError(err)
		}
	}
	return database.ClassifyError(tx.Commit())
}

func (s *sqlStore) GetModelOption(ctx context.Context, modelID, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT option_value FROM model_options WHERE model_id=? AND option_key=?`, modelID, key).Scan(&value)
	if err == nil {
		return value, true, nil
	}
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return "", false, database.ClassifyError(err)
}

func (s *sqlStore) InsertModelOption(ctx context.Context, modelID, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO model_options(model_id,option_key,option_value) VALUES(?,?,?)`, modelID, key, value)
	return database.ClassifyError(err)
}

func (s *sqlStore) UpdateModelOption(ctx context.Context, modelID, key, value string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE model_options SET option_value=? WHERE model_id=? AND option_key=?`, value, modelID, key)
	return database.ClassifyError(err)
}

var _ Store = (*sqlStore)(nil)
