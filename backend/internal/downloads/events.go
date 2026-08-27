package downloads

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"time"
)

type Event struct {
	Type string `json:"type"`
	Job  *Job   `json:"job,omitempty"`
	ID   string `json:"id,omitempty"`
}

func (m *Manager) Subscribe(ctx context.Context) ([]Job, <-chan Event, context.CancelFunc, error) {
	snapshot, err := m.detailedList(ctx)
	if err != nil {
		return nil, nil, nil, err
	}

	watchCtx, cancel := context.WithCancel(ctx)
	events := make(chan Event, 32)
	baseline := jobFingerprints(snapshot)
	go func() {
		defer close(events)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-ticker.C:
				jobs, err := m.detailedList(watchCtx)
				if err != nil {
					continue
				}
				next := jobFingerprints(jobs)
				for index := range jobs {
					job := jobs[index]
					if baseline[job.ID] == next[job.ID] {
						continue
					}
					copy := job
					select {
					case events <- Event{Type: "download", Job: &copy}:
					case <-watchCtx.Done():
						return
					}
				}
				for id := range baseline {
					if _, exists := next[id]; exists {
						continue
					}
					select {
					case events <- Event{Type: "download_deleted", ID: id}:
					case <-watchCtx.Done():
						return
					}
				}
				baseline = next
			}
		}
	}()
	return snapshot, events, cancel, nil
}

func (m *Manager) Remove(ctx context.Context, id string) error {
	job, err := m.Get(ctx, id)
	if err != nil {
		return err
	}
	if job.State != StateCancelled {
		return errors.New("only cancelled downloads can be removed")
	}

	m.mu.Lock()
	cancel := m.cancels[id]
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}

	for _, file := range job.Files {
		finalPath, err := m.localPath(job, file.Path)
		if err != nil {
			continue
		}
		if err := os.Remove(finalPath + ".lcm-" + job.ID + ".part"); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	result, err := m.db.ExecContext(ctx, "DELETE FROM download_jobs WHERE id=? AND state=?", id, StateCancelled)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 1 {
		return nil
	}
	var state string
	if err := m.db.QueryRowContext(ctx, "SELECT state FROM download_jobs WHERE id=?", id).Scan(&state); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sql.ErrNoRows
		}
		return err
	}
	return errors.New("only cancelled downloads can be removed")
}

func (m *Manager) detailedList(ctx context.Context) ([]Job, error) {
	jobs, err := m.List(ctx)
	if err != nil {
		return nil, err
	}
	for index := range jobs {
		files, err := m.files(ctx, jobs[index].ID)
		if err != nil {
			return nil, err
		}
		jobs[index].Files = files
	}
	return jobs, nil
}

func jobFingerprints(jobs []Job) map[string]string {
	out := make(map[string]string, len(jobs))
	for _, job := range jobs {
		encoded, _ := json.Marshal(job)
		out[job.ID] = string(encoded)
	}
	return out
}
