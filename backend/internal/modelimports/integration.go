package modelimports

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"

	"github.com/brantje/llamacpp-manager/backend/internal/huggingface"
)

// RepairArtifactOptions replaces only manager-generated pending helper paths.
// User-provided llama.cpp option values are deliberately left untouched.
func (s *Service) RepairArtifactOptions(ctx context.Context, modelID, repoID string, artifact huggingface.Artifact) error {
	for _, dependency := range artifact.Dependencies {
		if len(dependency.Files) == 0 {
			continue
		}
		key := ""
		switch dependency.Kind {
		case "mmproj":
			key = "mmproj"
		case "mtp":
			key = "spec-draft-model"
		default:
			continue
		}

		providerPath, err := expectedProviderPathFromRelative(dependency.Files[0].Path)
		if err != nil {
			return err
		}
		correctRel, err := expectedProviderPath(repoID, dependency.Files[0].Path)
		if err != nil {
			return err
		}
		wrongAbs, err := filepath.Abs(filepath.Join(s.modelsDir, filepath.FromSlash(providerPath)))
		if err != nil {
			return err
		}
		correctAbs, err := filepath.Abs(filepath.Join(s.modelsDir, filepath.FromSlash(correctRel)))
		if err != nil {
			return err
		}

		var current string
		err = s.db.QueryRowContext(ctx, `SELECT option_value FROM model_options WHERE model_id=? AND option_key=?`, modelID, key).Scan(&current)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if _, err := s.db.ExecContext(ctx, `INSERT INTO model_options(model_id,option_key,option_value) VALUES(?,?,?)`, modelID, key, correctAbs); err != nil {
				return err
			}
		case err != nil:
			return err
		case filepath.Clean(current) == filepath.Clean(wrongAbs):
			if _, err := s.db.ExecContext(ctx, `UPDATE model_options SET option_value=? WHERE model_id=? AND option_key=?`, correctAbs, modelID, key); err != nil {
				return err
			}
		}

		if dependency.Kind == "mtp" {
			var specType string
			err := s.db.QueryRowContext(ctx, `SELECT option_value FROM model_options WHERE model_id=? AND option_key='spec-type'`, modelID).Scan(&specType)
			if errors.Is(err, sql.ErrNoRows) {
				if _, err := s.db.ExecContext(ctx, `INSERT INTO model_options(model_id,option_key,option_value) VALUES(?,'spec-type','draft-mtp')`, modelID); err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
		}
	}
	return nil
}

// ListResolved reports the import lifecycle maintained by the reconciler rather
// than merely mirroring the underlying download job state.
func (s *Service) ListResolved(ctx context.Context) ([]Status, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT pi.id,pi.job_id,pi.model_id,COALESCE(pi.instance_id,''),pi.state,
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
		if err := rows.Scan(&item.ID, &item.JobID, &item.ModelID, &item.InstanceID, &item.State, &item.Error, &start); err != nil {
			return nil, err
		}
		item.State = strings.ToUpper(strings.TrimSpace(item.State))
		item.StartWhenReady = start != 0
		out = append(out, item)
	}
	return out, rows.Err()
}

// CleanupJobSafe removes the Instance created for a pending provider import even
// when the selected artifact was already represented by a pre-existing Model.
func (s *Service) CleanupJobSafe(ctx context.Context, jobID string) error {
	rows, err := s.db.QueryContext(ctx, `SELECT COALESCE(instance_id,'') FROM provider_imports WHERE job_id=?`, jobID)
	if err != nil {
		return err
	}
	var instanceIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		if strings.TrimSpace(id) != "" {
			instanceIDs = append(instanceIDs, id)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range instanceIDs {
		if err := s.instances.Delete(ctx, id); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return s.CleanupJob(ctx, jobID)
}
