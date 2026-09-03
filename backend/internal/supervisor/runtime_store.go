package supervisor

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	// EnvInstallationID is injected into every managed worker and is the
	// primary proof that a process belongs to this LlamaRack installation.
	EnvInstallationID = "LLAMARACK_INSTALLATION_ID"
	// EnvInstanceID identifies the logical Instance the worker was started for.
	EnvInstanceID = "LLAMARACK_INSTANCE_ID"
	// EnvWorkerGeneration is a per-start token that distinguishes current
	// workers from stale ones after restart or replacement.
	EnvWorkerGeneration = "LLAMARACK_WORKER_GENERATION"
	// EnvWorkerPort records the private listen port for cleanup/port release.
	// It is corroborating metadata, not an ownership proof.
	EnvWorkerPort = "LLAMARACK_WORKER_PORT"

	// InstallationSettingKey is an internal manager_settings row. It is not
	// part of the admin General settings surface.
	InstallationSettingKey = "installation_id"
)

// ErrRuntimeNotFound is returned when no persisted worker record exists.
var ErrRuntimeNotFound = errors.New("worker runtime record not found")

// WorkerRecord is the minimum runtime metadata needed to prove prior ownership
// after a manager crash. It is not desired Instance configuration.
type WorkerRecord struct {
	InstanceID string
	Generation string
	PID        int
	StartTicks uint64
	Port       int
}

// RuntimeStore persists live worker identity across manager process death.
type RuntimeStore interface {
	Upsert(ctx context.Context, rec WorkerRecord) error
	Get(ctx context.Context, instanceID string) (WorkerRecord, error)
	Delete(ctx context.Context, instanceID string) error
	List(ctx context.Context) ([]WorkerRecord, error)
}

// SQLStore stores worker runtime rows in SQLite.
type SQLStore struct {
	db *sql.DB
}

// NewSQLStore returns a SQLite-backed runtime store.
func NewSQLStore(db *sql.DB) *SQLStore {
	return &SQLStore{db: db}
}

func (s *SQLStore) Upsert(ctx context.Context, rec WorkerRecord) error {
	if s == nil || s.db == nil {
		return errors.New("worker runtime store is not configured")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO worker_runtime(instance_id,generation,pid,start_ticks,port,updated_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(instance_id) DO UPDATE SET
			generation=excluded.generation,
			pid=excluded.pid,
			start_ticks=excluded.start_ticks,
			port=excluded.port,
			updated_at=excluded.updated_at`,
		rec.InstanceID, rec.Generation, rec.PID, rec.StartTicks, rec.Port, time.Now().Unix())
	return err
}

func (s *SQLStore) Get(ctx context.Context, instanceID string) (WorkerRecord, error) {
	if s == nil || s.db == nil {
		return WorkerRecord{}, errors.New("worker runtime store is not configured")
	}
	var rec WorkerRecord
	var startTicks int64
	err := s.db.QueryRowContext(ctx, `SELECT instance_id,generation,pid,start_ticks,port FROM worker_runtime WHERE instance_id=?`, instanceID).
		Scan(&rec.InstanceID, &rec.Generation, &rec.PID, &startTicks, &rec.Port)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkerRecord{}, ErrRuntimeNotFound
	}
	if err != nil {
		return WorkerRecord{}, err
	}
	rec.StartTicks = uint64(startTicks)
	return rec, nil
}

func (s *SQLStore) Delete(ctx context.Context, instanceID string) error {
	if s == nil || s.db == nil {
		return errors.New("worker runtime store is not configured")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM worker_runtime WHERE instance_id=?`, instanceID)
	return err
}

func (s *SQLStore) List(ctx context.Context) ([]WorkerRecord, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("worker runtime store is not configured")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT instance_id,generation,pid,start_ticks,port FROM worker_runtime`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkerRecord
	for rows.Next() {
		var rec WorkerRecord
		var startTicks int64
		if err := rows.Scan(&rec.InstanceID, &rec.Generation, &rec.PID, &startTicks, &rec.Port); err != nil {
			return nil, err
		}
		rec.StartTicks = uint64(startTicks)
		out = append(out, rec)
	}
	return out, rows.Err()
}

// MemoryStore is an in-memory RuntimeStore for tests.
type MemoryStore struct {
	mu      sync.Mutex
	records map[string]WorkerRecord
}

// NewMemoryStore returns an empty in-memory runtime store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{records: map[string]WorkerRecord{}}
}

func (s *MemoryStore) Upsert(_ context.Context, rec WorkerRecord) error {
	if s == nil {
		return errors.New("worker runtime store is not configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.records == nil {
		s.records = map[string]WorkerRecord{}
	}
	s.records[rec.InstanceID] = rec
	return nil
}

func (s *MemoryStore) Get(_ context.Context, instanceID string) (WorkerRecord, error) {
	if s == nil {
		return WorkerRecord{}, errors.New("worker runtime store is not configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[instanceID]
	if !ok {
		return WorkerRecord{}, ErrRuntimeNotFound
	}
	return rec, nil
}

func (s *MemoryStore) Delete(_ context.Context, instanceID string) error {
	if s == nil {
		return errors.New("worker runtime store is not configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, instanceID)
	return nil
}

func (s *MemoryStore) List(context.Context) ([]WorkerRecord, error) {
	if s == nil {
		return nil, errors.New("worker runtime store is not configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]WorkerRecord, 0, len(s.records))
	for _, rec := range s.records {
		out = append(out, rec)
	}
	return out, nil
}

// EnsureInstallationID returns the durable installation UUID, creating it once.
func EnsureInstallationID(ctx context.Context, db *sql.DB) (string, error) {
	if db == nil {
		return "", errors.New("database is required")
	}
	var existing string
	err := db.QueryRowContext(ctx, `SELECT setting_value FROM manager_settings WHERE setting_key=?`, InstallationSettingKey).Scan(&existing)
	if err == nil {
		existing = strings.TrimSpace(existing)
		if existing != "" {
			return existing, nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	id, err := randomIdentity()
	if err != nil {
		return "", err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO manager_settings(setting_key,setting_value,updated_at) VALUES(?,?,?)
		ON CONFLICT(setting_key) DO NOTHING`, InstallationSettingKey, id, time.Now().Unix())
	if err != nil {
		return "", err
	}
	if err := db.QueryRowContext(ctx, `SELECT setting_value FROM manager_settings WHERE setting_key=?`, InstallationSettingKey).Scan(&existing); err != nil {
		return "", err
	}
	existing = strings.TrimSpace(existing)
	if existing == "" {
		return "", errors.New("installation id missing after insert")
	}
	return existing, nil
}

var randomIdentity = func() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
