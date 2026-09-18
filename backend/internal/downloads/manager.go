package downloads

import (
		"context"
	"crypto/rand"
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

	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/huggingface"
)

const (
	StateQueued      = "QUEUED"
	StateResolving   = "RESOLVING"
	StateDownloading = "DOWNLOADING"
	StateVerifying   = "VERIFYING"
	StateCompleted   = "COMPLETED"
	StateFailed      = "FAILED"
	StateCancelled   = "CANCELLED"

	defaultMaxDownloadBytes int64 = 1 << 40
)

type SizeLimitFunc func(ctx context.Context) (int64, error)

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
	TempPath        string `json:"-"`
}

type Manager struct {
	ctx       context.Context
	store     DownloadStore
	modelsDir string
	hf        *huggingface.Client
	limit     SizeLimitFunc
	mu        sync.Mutex
	cancels   map[string]context.CancelFunc
}

func New(ctx context.Context, db database.Store, modelsDir string, hf *huggingface.Client, limits ...SizeLimitFunc) *Manager {
	return NewWithStore(ctx, NewDownloadStore(db), modelsDir, hf, limits...)
}

func NewWithStore(ctx context.Context, store DownloadStore, modelsDir string, hf *huggingface.Client, limits ...SizeLimitFunc) *Manager {
	var limit SizeLimitFunc
	if len(limits) > 0 {
		limit = limits[0]
	}
	return &Manager{ctx: ctx, store: store, modelsDir: modelsDir, hf: hf, limit: limit, cancels: map[string]context.CancelFunc{}}
}

func (m *Manager) CreateHuggingFace(ctx context.Context, detail huggingface.ModelDetail, artifact huggingface.Artifact) (Job, error) {
	if !artifact.Complete || len(artifact.Files) == 0 {
		return Job{}, errors.New("selected split GGUF artifact is incomplete")
	}
	if detail.ID == "" || detail.Revision == "" || artifact.ID == "" {
		return Job{}, errors.New("incomplete Hugging Face artifact identity")
	}
	limit, err := m.maxDownloadBytes(ctx)
	if err != nil {
		return Job{}, err
	}
	if knownDownloadBytes(artifact) > limit {
		return Job{}, errDownloadExceedsLimit
	}
	existing, found, err := m.store.FindCompleted(ctx, detail.ID, detail.Revision, artifact.ID)
	if err != nil {
		return Job{}, err
	}
	if found {
		return m.Get(ctx, existing)
	}
	id, err := randomID()
	if err != nil {
		return Job{}, err
	}
	files := make([]File, 0, len(artifact.Files))
	for index, file := range artifact.Files {
		if !safeProviderPath(file.Path) {
			return Job{}, fmt.Errorf("unsafe provider filename %q", file.Path)
		}
		files = append(files, File{Path: file.Path, Size: file.Size, OID: file.OID, State: StateQueued, Ordinal: index})
	}
	job := Job{ID: id, Provider: "huggingface", RepoID: detail.ID, Revision: detail.Revision, ArtifactID: artifact.ID, Name: artifact.Name, Quantization: artifact.Quantization, State: StateQueued, TotalBytes: artifact.TotalBytes}
	if err := m.store.Create(ctx, job, files); err != nil {
		return Job{}, err
	}
	m.launch(id)
	return m.Get(ctx, id)
}

func (m *Manager) List(ctx context.Context) ([]Job, error) {
	return m.store.List(ctx)
}

func (m *Manager) Get(ctx context.Context, id string) (Job, error) {
	job, err := m.store.Get(ctx, id)
	if err != nil {
		return Job{}, err
	}
	files, err := m.store.Files(ctx, id)
	if err != nil {
		return Job{}, err
	}
	job.Files = files
	return job, nil
}

func (m *Manager) ResumePending(ctx context.Context) error {
	ids, err := m.store.PendingIDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := m.store.Requeue(ctx, id); err != nil {
			return err
		}
		m.launch(id)
	}
	return nil
}

func (m *Manager) Retry(ctx context.Context, id string) (Job, error) {
	job, err := m.Get(ctx, id)
	if err != nil {
		return Job{}, err
	}
	if job.State != StateFailed && job.State != StateCancelled {
		return Job{}, errors.New("download is not retryable")
	}
	if err := m.waitForLaunchSlot(ctx, id); err != nil {
		return Job{}, err
	}
	retried, err := m.store.Retry(ctx, id)
	if err != nil {
		return Job{}, err
	}
	if !retried {
		return Job{}, errors.New("download is not retryable")
	}
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
	changed, state, err := m.store.Cancel(ctx, id)
	if err != nil {
		return err
	}
	if !changed && (state == StateCompleted || state == StateCancelled) {
		return nil
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
			_ = m.store.MarkJobFailedUnlessCancelled(context.Background(), id, err.Error())
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
			_ = m.store.SetFileState(context.Background(), id, file.Path, StateFailed)
			return fmt.Errorf("%s: %w", file.Path, err)
		}
	}
	if err := m.setJobState(ctx, id, StateVerifying, ""); err != nil {
		return err
	}
	files, err := m.store.Files(ctx, id)
	if err != nil {
		return err
	}
	for _, file := range files {
		if !m.completedFileValid(job, file) {
			return fmt.Errorf("download verification failed for %s", file.Path)
		}
	}
	return m.store.CompleteJob(ctx, id)
}

func knownDownloadBytes(artifact huggingface.Artifact) int64 {
	var sum int64
	for _, file := range artifact.Files {
		if file.Size > 0 {
			sum += file.Size
		}
	}
	if artifact.TotalBytes > sum {
		return artifact.TotalBytes
	}
	return sum
}

func (m *Manager) downloadFile(ctx context.Context, job Job, file File) error {
	finalPath, err := m.localPath(job, file.Path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return err
	}
	if info, err := os.Stat(finalPath); err == nil && info.Mode().IsRegular() && (file.Size <= 0 || info.Size() == file.Size) {
		err = m.store.CompleteFile(ctx, job.ID, file.Path, info.Size(), relativeSlash(m.modelsDir, finalPath))
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

	limit, err := m.maxDownloadBytes(ctx)
	if err != nil {
		return err
	}
	siblings, err := m.jobDownloadedExcept(ctx, job.ID, file.Path)
	if err != nil {
		return err
	}
	if remoteSize > 0 && siblings+remoteSize > limit {
		return errDownloadExceedsLimit
	}

	restart := false
	tempPath, offset, err := m.resolveTempFile(ctx, job, file, finalPath, false)
	if err != nil {
		return err
	}
	file.TempPath = relativeSlash(m.modelsDir, tempPath)
	if offset > 0 && (file.ETag == "" || remoteETag == "" || file.ETag != remoteETag || (file.Size > 0 && offset > file.Size)) {
		restart = true
	}
	if restart {
		tempPath, offset, err = m.resolveTempFile(ctx, job, file, finalPath, true)
		if err != nil {
			return err
		}
		file.TempPath = relativeSlash(m.modelsDir, tempPath)
	}
	if siblings+offset > limit {
		return errDownloadExceedsLimit
	}

	err = m.store.SetFileDownloading(ctx, job.ID, file.Path, offset, remoteETag)
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
		tempPath, offset, err = m.resolveTempFile(ctx, job, file, finalPath, true)
		if err != nil {
			return err
		}
		file.TempPath = relativeSlash(m.modelsDir, tempPath)
		resp, err = m.get(ctx, rawURL, 0)
		if err != nil {
			return err
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}
	out, err := openPartial(tempPath, offset)
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
			if siblings+offset+int64(n) > limit {
				return errDownloadExceedsLimit
			}
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
				if err := m.store.SetFileDownloaded(ctx, job.ID, file.Path, offset); err != nil {
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
	err = m.store.CompleteFile(ctx, job.ID, file.Path, offset, relativeSlash(m.modelsDir, finalPath))
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
	return m.store.SetJobState(ctx, id, state, message)
}

func (m *Manager) refreshAggregate(ctx context.Context, id string, speed int64) error {
	return m.store.RefreshAggregate(ctx, id, speed)
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
