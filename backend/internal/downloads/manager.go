package downloads

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	pathpkg "path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/huggingface"
)

const (
	StateQueued      = "QUEUED"
	StateResolving   = "RESOLVING"
	StateDownloading = "DOWNLOADING"
	StateVerifying   = "VERIFYING"
	StateCompleted   = "COMPLETED"
	StateFailed      = "FAILED"
	StateCancelled   = "CANCELLED"
)

type Job struct {
	ID              string `json:"id"`
	Provider        string `json:"provider"`
	RepoID          string `json:"repo_id"`
	Revision        string `json:"revision"`
	ArtifactID      string `json:"artifact_id"`
	Name            string `json:"name"`
	Quantization    string `json:"quantization,omitempty"`
	State           string `json:"state"`
	TotalBytes      int64  `json:"total_bytes"`
	DownloadedBytes int64  `json:"downloaded_bytes"`
	SpeedBPS        int64  `json:"speed_bps"`
	Error           string `json:"error,omitempty"`
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
	Files           []File `json:"files,omitempty"`
}

type File struct {
	Path            string `json:"path"`
	Size            int64  `json:"size"`
	OID             string `json:"oid,omitempty"`
	State           string `json:"state"`
	DownloadedBytes int64  `json:"downloaded_bytes"`
	ETag            string `json:"etag,omitempty"`
	Ordinal         int    `json:"ordinal"`
	LocalPath       string `json:"local_path,omitempty"`
}

type Manager struct {
	ctx       context.Context
	db        *sql.DB
	modelsDir string
	hf        *huggingface.Client
	mu        sync.Mutex
	cancels   map[string]context.CancelFunc
}

func New(ctx context.Context, db *sql.DB, modelsDir string, hf *huggingface.Client) *Manager {
	return &Manager{ctx: ctx, db: db, modelsDir: modelsDir, hf: hf, cancels: map[string]context.CancelFunc{}}
}

func (m *Manager) CreateHuggingFace(ctx context.Context, detail huggingface.ModelDetail, artifact huggingface.Artifact) (Job, error) {
	if !artifact.Complete || len(artifact.Files) == 0 {
		return Job{}, errors.New("selected split GGUF artifact is incomplete")
	}
	if detail.ID == "" || detail.Revision == "" || artifact.ID == "" {
		return Job{}, errors.New("incomplete Hugging Face artifact identity")
	}
	var existing string
	err := m.db.QueryRowContext(ctx, `SELECT id FROM download_jobs WHERE provider='huggingface' AND repo_id=? AND revision=? AND artifact_id=? AND state='COMPLETED' LIMIT 1`, detail.ID, detail.Revision, artifact.ID).Scan(&existing)
	if err == nil {
		return m.Get(ctx, existing)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Job{}, err
	}
	id, err := randomID()
	if err != nil {
		return Job{}, err
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,quantization,state,total_bytes,downloaded_bytes,speed_bps,error,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,0,0,'',unixepoch(),unixepoch())`, id, "huggingface", detail.ID, detail.Revision, artifact.ID, artifact.Name, artifact.Quantization, StateQueued, artifact.TotalBytes)
	if err != nil {
		return Job{}, err
	}
	for index, file := range artifact.Files {
		if !safeProviderPath(file.Path) {
			return Job{}, fmt.Errorf("unsafe provider filename %q", file.Path)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO download_files(job_id,path,size,oid,state,downloaded_bytes,etag,ordinal,local_path)
VALUES(?,?,?,?,?,0,'',?, '')`, id, file.Path, file.Size, file.OID, StateQueued, index)
		if err != nil {
			return Job{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	m.launch(id)
	return m.Get(ctx, id)
}

func (m *Manager) List(ctx context.Context) ([]Job, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT id,provider,repo_id,revision,artifact_id,name,quantization,state,total_bytes,downloaded_bytes,speed_bps,error,created_at,updated_at FROM download_jobs ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]Job, 0)
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (m *Manager) Get(ctx context.Context, id string) (Job, error) {
	row := m.db.QueryRowContext(ctx, `SELECT id,provider,repo_id,revision,artifact_id,name,quantization,state,total_bytes,downloaded_bytes,speed_bps,error,created_at,updated_at FROM download_jobs WHERE id=?`, id)
	job, err := scanJob(row)
	if err != nil {
		return Job{}, err
	}
	files, err := m.files(ctx, id)
	if err != nil {
		return Job{}, err
	}
	job.Files = files
	return job, nil
}

func (m *Manager) ResumePending(ctx context.Context) error {
	rows, err := m.db.QueryContext(ctx, `SELECT id FROM download_jobs WHERE state IN (?,?,?,?)`, StateQueued, StateResolving, StateDownloading, StateVerifying)
	if err != nil {
		return err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		_, _ = m.db.ExecContext(ctx, "UPDATE download_jobs SET state=?,speed_bps=0,error='',updated_at=unixepoch() WHERE id=?", StateQueued, id)
		m.launch(id)
	}
	return nil
}

func (m *Manager) Retry(ctx context.Context, id string) (Job, error) {
	if err := m.waitForLaunchSlot(ctx, id); err != nil {
		return Job{}, err
	}
	result, err := m.db.ExecContext(ctx, `UPDATE download_jobs SET state=?,error='',speed_bps=0,updated_at=unixepoch() WHERE id=? AND state IN (?,?)`, StateQueued, id, StateFailed, StateCancelled)
	if err != nil {
		return Job{}, err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return Job{}, errors.New("download is not retryable")
	}
	_, _ = m.db.ExecContext(ctx, `UPDATE download_files SET state=CASE WHEN state=? THEN ? ELSE state END WHERE job_id=?`, StateFailed, StateQueued, id)
	m.launch(id)
	return m.Get(ctx, id)
}

func (m *Manager) waitForLaunchSlot(ctx context.Context, id string) error {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		m.mu.Lock()
		_, running := m.cancels[id]
		m.mu.Unlock()
		if !running {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (m *Manager) Cancel(ctx context.Context, id string) error {
	result, err := m.db.ExecContext(ctx, `UPDATE download_jobs SET state=?,speed_bps=0,updated_at=unixepoch() WHERE id=? AND state NOT IN (?,?)`, StateCancelled, id, StateCompleted, StateCancelled)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		var state string
		if err := m.db.QueryRowContext(ctx, "SELECT state FROM download_jobs WHERE id=?", id).Scan(&state); err != nil {
			return err
		}
		if state == StateCompleted || state == StateCancelled {
			return nil
		}
	}
	m.mu.Lock()
	cancel := m.cancels[id]
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (m *Manager) launch(id string) {
	m.mu.Lock()
	if _, exists := m.cancels[id]; exists {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancels[id] = cancel
	m.mu.Unlock()
	go func() {
		defer func() {
			m.mu.Lock()
			delete(m.cancels, id)
			m.mu.Unlock()
			cancel()
		}()
		if err := m.run(ctx, id); err != nil && !errors.Is(err, context.Canceled) {
			_, _ = m.db.ExecContext(context.Background(), "UPDATE download_jobs SET state=?,speed_bps=0,error=?,updated_at=unixepoch() WHERE id=? AND state<>?", StateFailed, err.Error(), id, StateCancelled)
		}
	}()
}

func (m *Manager) run(ctx context.Context, id string) error {
	job, err := m.Get(ctx, id)
	if err != nil {
		return err
	}
	if job.Provider != "huggingface" {
		return errors.New("unsupported download provider")
	}
	if err := m.setJobState(ctx, id, StateResolving, ""); err != nil {
		return err
	}
	for _, file := range job.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if file.State == StateCompleted && m.completedFileValid(job, file) {
			continue
		}
		if err := m.downloadFile(ctx, job, file); err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			_, _ = m.db.ExecContext(context.Background(), "UPDATE download_files SET state=? WHERE job_id=? AND path=?", StateFailed, id, file.Path)
			return fmt.Errorf("%s: %w", file.Path, err)
		}
	}
	if err := m.setJobState(ctx, id, StateVerifying, ""); err != nil {
		return err
	}
	files, err := m.files(ctx, id)
	if err != nil {
		return err
	}
	for _, file := range files {
		if !m.completedFileValid(job, file) {
			return fmt.Errorf("download verification failed for %s", file.Path)
		}
	}
	_, err = m.db.ExecContext(ctx, "UPDATE download_jobs SET state=?,downloaded_bytes=total_bytes,speed_bps=0,error='',updated_at=unixepoch() WHERE id=?", StateCompleted, id)
	return err
}

func (m *Manager) downloadFile(ctx context.Context, job Job, file File) error {
	finalPath, err := m.localPath(job, file.Path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return err
	}
	if info, err := os.Stat(finalPath); err == nil && (file.Size <= 0 || info.Size() == file.Size) {
		_, err = m.db.ExecContext(ctx, "UPDATE download_files SET state=?,downloaded_bytes=?,local_path=? WHERE job_id=? AND path=?", StateCompleted, info.Size(), relativeSlash(m.modelsDir, finalPath), job.ID, file.Path)
		if err == nil {
			_ = m.refreshAggregate(ctx, job.ID, 0)
		}
		return err
	}

	rawURL, err := m.hf.DownloadURL(job.RepoID, job.Revision, file.Path)
	if err != nil {
		return err
	}
	remoteETag, remoteSize, err := m.remoteIdentity(ctx, rawURL)
	if err != nil {
		return err
	}
	if file.Size > 0 && remoteSize > 0 && file.Size != remoteSize {
		return fmt.Errorf("remote size changed from %d to %d", file.Size, remoteSize)
	}
	tempPath := finalPath + ".lcm-" + job.ID + ".part"
	offset := int64(0)
	if info, err := os.Stat(tempPath); err == nil {
		offset = info.Size()
	}
	if offset > 0 && (file.ETag == "" || remoteETag == "" || file.ETag != remoteETag || (file.Size > 0 && offset > file.Size)) {
		if err := os.Remove(tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		offset = 0
	}
	_, err = m.db.ExecContext(ctx, "UPDATE download_files SET state=?,downloaded_bytes=?,etag=? WHERE job_id=? AND path=?", StateDownloading, offset, remoteETag, job.ID, file.Path)
	if err != nil {
		return err
	}
	if err := m.setJobState(ctx, job.ID, StateDownloading, ""); err != nil {
		return err
	}

	resp, err := m.get(ctx, rawURL, offset)
	if err != nil {
		return err
	}
	if offset > 0 && resp.StatusCode != http.StatusPartialContent {
		resp.Body.Close()
		offset = 0
		if err := os.Remove(tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		resp, err = m.get(ctx, rawURL, 0)
		if err != nil {
			return err
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}
	flags := os.O_CREATE | os.O_WRONLY
	if offset > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	out, err := os.OpenFile(tempPath, flags, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	started := time.Now()
	startOffset := offset
	lastPersist := time.Now()
	buffer := make([]byte, 256*1024)
	for {
		n, readErr := resp.Body.Read(buffer)
		if n > 0 {
			if _, err := out.Write(buffer[:n]); err != nil {
				return err
			}
			offset += int64(n)
			if time.Since(lastPersist) >= 250*time.Millisecond || (file.Size > 0 && offset == file.Size) {
				elapsed := time.Since(started).Seconds()
				speed := int64(0)
				if elapsed > 0 {
					speed = int64(float64(offset-startOffset) / elapsed)
				}
				if _, err := m.db.ExecContext(ctx, "UPDATE download_files SET downloaded_bytes=? WHERE job_id=? AND path=?", offset, job.ID, file.Path); err != nil {
					return err
				}
				if err := m.refreshAggregate(ctx, job.ID, speed); err != nil {
					return err
				}
				lastPersist = time.Now()
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return readErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if file.Size > 0 && offset != file.Size {
		return fmt.Errorf("expected %d bytes, received %d", file.Size, offset)
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		return err
	}
	_, err = m.db.ExecContext(ctx, "UPDATE download_files SET state=?,downloaded_bytes=?,local_path=? WHERE job_id=? AND path=?", StateCompleted, offset, relativeSlash(m.modelsDir, finalPath), job.ID, file.Path)
	if err == nil {
		err = m.refreshAggregate(ctx, job.ID, 0)
	}
	return err
}

func (m *Manager) remoteIdentity(ctx context.Context, rawURL string) (string, int64, error) {
	req, err := m.hf.NewDownloadRequest(ctx, http.MethodHead, rawURL)
	if err != nil {
		return "", 0, err
	}
	resp, err := m.hf.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return "", 0, fmt.Errorf("metadata request returned HTTP %d", resp.StatusCode)
	}
	size := resp.ContentLength
	if value := resp.Header.Get("X-Linked-Size"); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
			size = parsed
		}
	}
	etag := strings.TrimSpace(resp.Header.Get("ETag"))
	if etag == "" {
		etag = strings.TrimSpace(resp.Header.Get("X-Linked-Etag"))
	}
	return etag, size, nil
}

func (m *Manager) get(ctx context.Context, rawURL string, offset int64) (*http.Response, error) {
	req, err := m.hf.NewDownloadRequest(ctx, http.MethodGet, rawURL)
	if err != nil {
		return nil, err
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	return m.hf.Do(req)
}

func (m *Manager) setJobState(ctx context.Context, id, state, message string) error {
	_, err := m.db.ExecContext(ctx, "UPDATE download_jobs SET state=?,error=?,updated_at=unixepoch() WHERE id=? AND state<>?", state, message, id, StateCancelled)
	return err
}

func (m *Manager) refreshAggregate(ctx context.Context, id string, speed int64) error {
	var total sql.NullInt64
	if err := m.db.QueryRowContext(ctx, "SELECT SUM(downloaded_bytes) FROM download_files WHERE job_id=?", id).Scan(&total); err != nil {
		return err
	}
	_, err := m.db.ExecContext(ctx, "UPDATE download_jobs SET downloaded_bytes=?,speed_bps=?,updated_at=unixepoch() WHERE id=?", total.Int64, speed, id)
	return err
}

func (m *Manager) files(ctx context.Context, id string) ([]File, error) {
	rows, err := m.db.QueryContext(ctx, "SELECT path,size,oid,state,downloaded_bytes,etag,ordinal,local_path FROM download_files WHERE job_id=? ORDER BY ordinal,path", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []File
	for rows.Next() {
		var file File
		if err := rows.Scan(&file.Path, &file.Size, &file.OID, &file.State, &file.DownloadedBytes, &file.ETag, &file.Ordinal, &file.LocalPath); err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, rows.Err()
}

func (m *Manager) completedFileValid(job Job, file File) bool {
	localPath, err := m.localPath(job, file.Path)
	if err != nil {
		return false
	}
	info, err := os.Stat(localPath)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return file.Size <= 0 || info.Size() == file.Size
}

func (m *Manager) localPath(job Job, providerPath string) (string, error) {
	if !safeProviderPath(providerPath) {
		return "", errors.New("unsafe provider path")
	}
	parts := strings.Split(job.RepoID, "/")
	if len(parts) != 2 {
		return "", errors.New("invalid repository id")
	}
	rel := filepath.Join("huggingface", safeComponent(parts[0]), safeComponent(parts[1]), filepath.FromSlash(providerPath))
	root, err := filepath.Abs(m.modelsDir)
	if err != nil {
		return "", err
	}
	destination, err := filepath.Abs(filepath.Join(root, rel))
	if err != nil {
		return "", err
	}
	if destination == root || !strings.HasPrefix(destination, root+string(os.PathSeparator)) {
		return "", errors.New("download destination escaped models directory")
	}
	return destination, nil
}

func scanJob(scanner interface{ Scan(...any) error }) (Job, error) {
	var job Job
	err := scanner.Scan(&job.ID, &job.Provider, &job.RepoID, &job.Revision, &job.ArtifactID, &job.Name, &job.Quantization, &job.State, &job.TotalBytes, &job.DownloadedBytes, &job.SpeedBPS, &job.Error, &job.CreatedAt, &job.UpdatedAt)
	return job, err
}

func safeProviderPath(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") {
		return false
	}
	cleaned := pathpkg.Clean(value)
	return cleaned == value && cleaned != "." && !strings.HasPrefix(cleaned, "../") && strings.EqualFold(pathpkg.Ext(cleaned), ".gguf")
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
	if b.Len() == 0 || b.String() == "." || b.String() == ".." {
		return "_"
	}
	return b.String()
}

func relativeSlash(root, value string) string {
	rel, err := filepath.Rel(root, value)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

func randomID() (string, error) {
	data := make([]byte, 12)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}
