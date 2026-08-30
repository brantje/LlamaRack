package observability

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	playgroundSchemaReady sync.Map
	playgroundSchemaMu    sync.Mutex
)

type PlaygroundDiagnostics struct {
	Request            RequestLogDetail `json:"request"`
	StateTrace         []string         `json:"state_trace"`
	EvictionsTriggered []string         `json:"evictions_triggered"`
}

func (s *Service) ensurePlaygroundSchema(ctx context.Context) error {
	if _, ok := playgroundSchemaReady.Load(s.db); ok {
		return nil
	}
	playgroundSchemaMu.Lock()
	defer playgroundSchemaMu.Unlock()
	if _, ok := playgroundSchemaReady.Load(s.db); ok {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS playground_lifecycle_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		recorded_at INTEGER NOT NULL,
		event TEXT NOT NULL,
		instance_id TEXT NOT NULL
	)`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS playground_lifecycle_events_recorded_idx ON playground_lifecycle_events(recorded_at,event)`); err != nil {
		return err
	}
	playgroundSchemaReady.Store(s.db, struct{}{})
	return nil
}

func (s *Service) recordPlaygroundLifecycleEvent(ctx context.Context, event, instanceID string) error {
	if event != LifecycleEviction {
		return nil
	}
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		return nil
	}
	if err := s.ensurePlaygroundSchema(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO playground_lifecycle_events(recorded_at,event,instance_id) VALUES(?,?,?)`, time.Now().UnixMilli(), event, instanceID)
	return err
}

func requestStateTrace(record RequestRecord) []string {
	if strings.TrimSpace(record.InstanceID) == "" {
		return nil
	}
	if !record.Autoloaded {
		return []string{"READY"}
	}
	if record.Result == "error" || record.StatusCode >= http.StatusBadRequest {
		return []string{"UNLOADED", "STARTING", "FAILED"}
	}
	return []string{"UNLOADED", "STARTING", "READY"}
}

func (s *Service) playgroundEvictions(ctx context.Context, record RequestRecord) ([]string, error) {
	if err := s.ensurePlaygroundSchema(ctx); err != nil {
		return nil, err
	}
	finishedAt := record.FinishedAt
	if finishedAt <= 0 {
		finishedAt = time.Now().UnixMilli()
	}
	rows, err := s.db.QueryContext(ctx, `SELECT instance_id FROM playground_lifecycle_events
		WHERE event=? AND recorded_at>=? AND recorded_at<=? AND instance_id<>?
		ORDER BY id`, LifecycleEviction, record.StartedAt, finishedAt, record.InstanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	var out []string
	for rows.Next() {
		var instanceID string
		if err := rows.Scan(&instanceID); err != nil {
			return nil, err
		}
		instanceID = strings.TrimSpace(instanceID)
		if instanceID == "" || seen[instanceID] {
			continue
		}
		seen[instanceID] = true
		out = append(out, instanceID)
	}
	return out, rows.Err()
}

func (s *Service) PlaygroundDiagnostics(ctx context.Context, requestID string) (PlaygroundDiagnostics, error) {
	request, err := s.GetRequestLogByRequestID(ctx, requestID)
	if err != nil {
		return PlaygroundDiagnostics{}, err
	}
	evictions, err := s.playgroundEvictions(ctx, request.RequestRecord)
	if err != nil {
		return PlaygroundDiagnostics{}, err
	}
	return PlaygroundDiagnostics{
		Request:            request,
		StateTrace:         requestStateTrace(request.RequestRecord),
		EvictionsTriggered: evictions,
	}, nil
}

func NewPlaygroundDiagnosticsHandler(service *Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		requestID := strings.TrimSpace(r.PathValue("request_id"))
		if requestID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request_id is required"})
			return
		}
		diagnostics, err := service.PlaygroundDiagnostics(r.Context(), requestID)
		if err != nil {
			if err == sql.ErrNoRows {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "request not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, diagnostics)
	})
}
