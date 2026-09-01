package observability

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"sync"
)

import "github.com/brantje/llamarack/backend/internal/lifecycle"

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
		event TEXT NOT NULL,
		instance_id TEXT NOT NULL,
		correlation_id TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		return err
	}

	// The Playground diagnostics table was introduced on the redesign branch.
	// Keep branch-local databases created by the earlier draft compatible rather
	// than requiring a manual reset.
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(playground_lifecycle_events)`)
	if err != nil {
		return err
	}
	hasCorrelation := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		if name == "correlation_id" {
			hasCorrelation = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if !hasCorrelation {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE playground_lifecycle_events ADD COLUMN correlation_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS playground_lifecycle_events_correlation_idx ON playground_lifecycle_events(correlation_id,event,id)`); err != nil {
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
	correlationID := lifecycle.RequestCorrelationFromContext(ctx)
	if instanceID == "" || correlationID == "" {
		return nil
	}
	if err := s.ensurePlaygroundSchema(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO playground_lifecycle_events(event,instance_id,correlation_id) VALUES(?,?,?)`, event, instanceID, correlationID)
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
	correlationID := strings.TrimSpace(record.TraceID)
	if correlationID == "" {
		return nil, nil
	}
	if err := s.ensurePlaygroundSchema(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT instance_id FROM playground_lifecycle_events
		WHERE event=? AND correlation_id=? AND instance_id<>?
		ORDER BY id`, LifecycleEviction, correlationID, record.InstanceID)
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
