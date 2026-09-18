package downloads

import (
	"context"
	"database/sql"

	"github.com/brantje/llamarack/backend/internal/database"
)

// DownloadStore owns durable download jobs, files, and state transitions.
type DownloadStore interface {
	FindCompleted(context.Context, string, string, string) (string, bool, error)
	Create(context.Context, Job, []File) error
	List(context.Context) ([]Job, error)
	Get(context.Context, string) (Job, error)
	Files(context.Context, string) ([]File, error)
	PendingIDs(context.Context) ([]string, error)
	Requeue(context.Context, string) error
	Retry(context.Context, string) (bool, error)
	Cancel(context.Context, string) (bool, string, error)
	MarkJobFailedUnlessCancelled(context.Context, string, string) error
	SetFileState(context.Context, string, string, string) error
	CompleteJob(context.Context, string) error
	CompleteFile(context.Context, string, string, int64, string) error
	SetFileDownloading(context.Context, string, string, int64, string) error
	SetFileDownloaded(context.Context, string, string, int64) error
	SetJobState(context.Context, string, string, string) error
	RefreshAggregate(context.Context, string, int64) error
	SetTempPath(context.Context, string, string, string) error
	DownloadedExcept(context.Context, string, string) (int64, error)
	RemoveCancelled(context.Context, string) (bool, string, error)
}

type sqlDownloadStore struct{ db database.Store }

func NewDownloadStore(db database.Store) DownloadStore { return &sqlDownloadStore{db: db} }

func (s *sqlDownloadStore) FindCompleted(ctx context.Context, repoID, revision, artifactID string) (string, bool, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM download_jobs WHERE provider='huggingface' AND repo_id=? AND revision=? AND artifact_id=? AND state='COMPLETED' LIMIT 1`, repoID, revision, artifactID).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return "", false, database.ClassifyError(err)
}

func (s *sqlDownloadStore) Create(ctx context.Context, job Job, files []File) error {
	tx, err := database.Begin(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,quantization,state,total_bytes,downloaded_bytes,speed_bps,error,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,0,0,'',unixepoch(),unixepoch())`, job.ID, job.Provider, job.RepoID, job.Revision, job.ArtifactID, job.Name, job.Quantization, job.State, job.TotalBytes); err != nil {
		return database.ClassifyError(err)
	}
	for _, file := range files {
		if _, err := tx.ExecContext(ctx, `INSERT INTO download_files(job_id,path,size,oid,state,downloaded_bytes,etag,ordinal,local_path)
VALUES(?,?,?,?,?,0,'',?, '')`, job.ID, file.Path, file.Size, file.OID, file.State, file.Ordinal); err != nil {
			return database.ClassifyError(err)
		}
	}
	return database.ClassifyError(tx.Commit())
}

const jobColumns = `id,provider,repo_id,revision,artifact_id,name,quantization,state,total_bytes,downloaded_bytes,speed_bps,error,created_at,updated_at`

func (s *sqlDownloadStore) List(ctx context.Context) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobColumns+` FROM download_jobs ORDER BY created_at DESC,id DESC`)
	if err != nil {
		return nil, database.ClassifyError(err)
	}
	defer rows.Close()
	jobs := make([]Job, 0)
	for rows.Next() {
		job, err := scanDownloadJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(err)
	}
	return jobs, nil
}

func (s *sqlDownloadStore) Get(ctx context.Context, id string) (Job, error) {
	return scanDownloadJob(s.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM download_jobs WHERE id=?`, id))
}

func (s *sqlDownloadStore) Files(ctx context.Context, id string) ([]File, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path,size,oid,state,downloaded_bytes,etag,ordinal,local_path,temp_path FROM download_files WHERE job_id=? ORDER BY ordinal,path`, id)
	if err != nil {
		return nil, database.ClassifyError(err)
	}
	defer rows.Close()
	files := make([]File, 0)
	for rows.Next() {
		var file File
		if err := rows.Scan(&file.Path, &file.Size, &file.OID, &file.State, &file.DownloadedBytes, &file.ETag, &file.Ordinal, &file.LocalPath, &file.TempPath); err != nil {
			return nil, database.ClassifyError(err)
		}
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(err)
	}
	return files, nil
}

func (s *sqlDownloadStore) PendingIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM download_jobs WHERE state IN (?,?,?,?)`, StateQueued, StateResolving, StateDownloading, StateVerifying)
	if err != nil {
		return nil, database.ClassifyError(err)
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, database.ClassifyError(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(err)
	}
	return ids, nil
}

func (s *sqlDownloadStore) Requeue(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE download_jobs SET state=?,speed_bps=0,error='',updated_at=unixepoch() WHERE id=?`, StateQueued, id)
	return database.ClassifyError(err)
}

func (s *sqlDownloadStore) Retry(ctx context.Context, id string) (bool, error) {
	tx, err := database.Begin(ctx, s.db)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE download_jobs SET state=?,error='',speed_bps=0,updated_at=unixepoch() WHERE id=? AND state IN (?,?)`, StateQueued, id, StateFailed, StateCancelled)
	if err != nil {
		return false, database.ClassifyError(err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, database.ClassifyError(err)
	}
	if n != 1 {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE download_files SET state=CASE WHEN state=? THEN ? ELSE state END WHERE job_id=?`, StateFailed, StateQueued, id); err != nil {
		return false, database.ClassifyError(err)
	}
	if err := tx.Commit(); err != nil {
		return false, database.ClassifyError(err)
	}
	return true, nil
}

func (s *sqlDownloadStore) Cancel(ctx context.Context, id string) (bool, string, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE download_jobs SET state=?,speed_bps=0,updated_at=unixepoch() WHERE id=? AND state NOT IN (?,?)`, StateCancelled, id, StateCompleted, StateCancelled)
	if err != nil {
		return false, "", database.ClassifyError(err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, "", database.ClassifyError(err)
	}
	if n == 1 {
		return true, StateCancelled, nil
	}
	var state string
	if err := s.db.QueryRowContext(ctx, `SELECT state FROM download_jobs WHERE id=?`, id).Scan(&state); err != nil {
		return false, "", database.ClassifyError(err)
	}
	return false, state, nil
}

func (s *sqlDownloadStore) MarkJobFailedUnlessCancelled(ctx context.Context, id, message string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE download_jobs SET state=?,speed_bps=0,error=?,updated_at=unixepoch() WHERE id=? AND state<>?`, StateFailed, message, id, StateCancelled)
	return database.ClassifyError(err)
}

func (s *sqlDownloadStore) SetFileState(ctx context.Context, jobID, path, state string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE download_files SET state=? WHERE job_id=? AND path=?`, state, jobID, path)
	return database.ClassifyError(err)
}

func (s *sqlDownloadStore) CompleteJob(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE download_jobs SET state=?,downloaded_bytes=total_bytes,speed_bps=0,error='',updated_at=unixepoch() WHERE id=?`, StateCompleted, id)
	return database.ClassifyError(err)
}

func (s *sqlDownloadStore) CompleteFile(ctx context.Context, jobID, path string, downloaded int64, localPath string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE download_files SET state=?,downloaded_bytes=?,local_path=?,temp_path='' WHERE job_id=? AND path=?`, StateCompleted, downloaded, localPath, jobID, path)
	return database.ClassifyError(err)
}

func (s *sqlDownloadStore) SetFileDownloading(ctx context.Context, jobID, path string, downloaded int64, etag string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE download_files SET state=?,downloaded_bytes=?,etag=? WHERE job_id=? AND path=?`, StateDownloading, downloaded, etag, jobID, path)
	return database.ClassifyError(err)
}

func (s *sqlDownloadStore) SetFileDownloaded(ctx context.Context, jobID, path string, downloaded int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE download_files SET downloaded_bytes=? WHERE job_id=? AND path=?`, downloaded, jobID, path)
	return database.ClassifyError(err)
}

func (s *sqlDownloadStore) SetJobState(ctx context.Context, id, state, message string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE download_jobs SET state=?,error=?,updated_at=unixepoch() WHERE id=? AND state<>?`, state, message, id, StateCancelled)
	return database.ClassifyError(err)
}

func (s *sqlDownloadStore) RefreshAggregate(ctx context.Context, id string, speed int64) error {
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(downloaded_bytes),0) FROM download_files WHERE job_id=?`, id).Scan(&total); err != nil {
		return database.ClassifyError(err)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE download_jobs SET downloaded_bytes=?,speed_bps=?,updated_at=unixepoch() WHERE id=?`, total, speed, id)
	return database.ClassifyError(err)
}

func (s *sqlDownloadStore) SetTempPath(ctx context.Context, jobID, path, tempPath string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE download_files SET temp_path=? WHERE job_id=? AND path=?`, tempPath, jobID, path)
	return database.ClassifyError(err)
}

func (s *sqlDownloadStore) DownloadedExcept(ctx context.Context, jobID, path string) (int64, error) {
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(downloaded_bytes),0) FROM download_files WHERE job_id=? AND path<>?`, jobID, path).Scan(&total); err != nil {
		return 0, database.ClassifyError(err)
	}
	return total, nil
}

func (s *sqlDownloadStore) RemoveCancelled(ctx context.Context, id string) (bool, string, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM download_jobs WHERE id=? AND state=?`, id, StateCancelled)
	if err != nil {
		return false, "", database.ClassifyError(err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, "", database.ClassifyError(err)
	}
	if n == 1 {
		return true, "", nil
	}
	var state string
	if err := s.db.QueryRowContext(ctx, `SELECT state FROM download_jobs WHERE id=?`, id).Scan(&state); err != nil {
		return false, "", database.ClassifyError(err)
	}
	return false, state, nil
}

type downloadScanner interface{ Scan(...any) error }

func scanDownloadJob(row downloadScanner) (Job, error) {
	var job Job
	if err := row.Scan(&job.ID, &job.Provider, &job.RepoID, &job.Revision, &job.ArtifactID, &job.Name, &job.Quantization, &job.State, &job.TotalBytes, &job.DownloadedBytes, &job.SpeedBPS, &job.Error, &job.CreatedAt, &job.UpdatedAt); err != nil {
		return Job{}, database.ClassifyError(err)
	}
	return job, nil
}

var _ DownloadStore = (*sqlDownloadStore)(nil)
