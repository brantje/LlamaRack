package observability

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
)

const (
	writebackFlushEvery = 50 * time.Millisecond
	writebackBatchSize  = 256
	writebackMaxEntries = 100000
)

var ErrWritebackOverflow = errors.New("observability writeback buffer full")

var writebackStates sync.Map

type writebackState struct {
	mu                sync.Mutex
	enabled           bool
	started           bool
	limit             int
	entries           map[string]*writebackEntry
	openAIToRequest   map[string]string
}

type writebackEntry struct {
	requestID         string
	record            RequestRecord
	promptTPS         *float64
	finalized         bool
	contextReady      bool
	sessionID         string
	contextInstanceID string
	modelSlug         string
	openAIResponseID  string
	openAIDeleted     bool
}

func writebackStateFor(s *Service) *writebackState {
	if value, ok := writebackStates.Load(s); ok {
		return value.(*writebackState)
	}
	state := &writebackState{
		limit:           writebackMaxEntries,
		entries:         map[string]*writebackEntry{},
		openAIToRequest: map[string]string{},
	}
	actual, _ := writebackStates.LoadOrStore(s, state)
	return actual.(*writebackState)
}

func cloneRequestRecord(record RequestRecord) RequestRecord {
	if record.APIKey != nil {
		value := *record.APIKey
		record.APIKey = &value
	}
	if record.TTFTMS != nil {
		value := *record.TTFTMS
		record.TTFTMS = &value
	}
	if record.TokensPerSecond != nil {
		value := *record.TokensPerSecond
		record.TokensPerSecond = &value
	}
	if record.PromptTokensPerSecond != nil {
		value := *record.PromptTokensPerSecond
		record.PromptTokensPerSecond = &value
	}
	if record.GenerationTokensPerSecond != nil {
		value := *record.GenerationTokensPerSecond
		record.GenerationTokensPerSecond = &value
	}
	if record.RequestBody != nil {
		value := *record.RequestBody
		record.RequestBody = &value
	}
	if record.ResponseBody != nil {
		value := *record.ResponseBody
		record.ResponseBody = &value
	}
	return record
}

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneWritebackEntry(entry *writebackEntry) writebackEntry {
	copyEntry := *entry
	copyEntry.record = cloneRequestRecord(entry.record)
	copyEntry.promptTPS = cloneFloat64(entry.promptTPS)
	return copyEntry
}

func (s *Service) StartWriteback(ctx context.Context) {
	s.startWriteback(ctx, writebackFlushEvery)
}

func (s *Service) startWriteback(ctx context.Context, interval time.Duration) {
	if s == nil {
		return
	}
	state := writebackStateFor(s)
	state.mu.Lock()
	state.enabled = true
	if state.started {
		state.mu.Unlock()
		return
	}
	state.started = true
	state.mu.Unlock()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_, err := s.flushWriteback(flushCtx, true)
				cancel()
				if err != nil {
					slog.Error("flush inference observability writeback failed", "error", err)
				}
			}
		}
	}()
}

func (s *Service) Flush(ctx context.Context) error {
	for {
		count, err := s.flushWriteback(ctx, false)
		if err != nil {
			return err
		}
		if count == 0 {
			return nil
		}
	}
}

func (s *Service) writebackEnabled() bool {
	state := writebackStateFor(s)
	state.mu.Lock()
	enabled := state.enabled
	state.mu.Unlock()
	return enabled
}

func (s *Service) bufferBegin(requestID string, record RequestRecord) (bool, error) {
	state := writebackStateFor(s)
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.enabled {
		return false, nil
	}
	if _, exists := state.entries[requestID]; exists {
		return true, errors.New("duplicate request_id")
	}
	if len(state.entries) >= state.limit {
		return true, ErrWritebackOverflow
	}
	state.entries[requestID] = &writebackEntry{requestID: requestID, record: cloneRequestRecord(record)}
	return true, nil
}

func (s *Service) bufferUpdate(requestID string, record RequestRecord) (bool, error) {
	state := writebackStateFor(s)
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.enabled {
		return false, nil
	}
	entry, exists := state.entries[requestID]
	if !exists {
		return false, nil
	}
	entry.record = cloneRequestRecord(record)
	return true, nil
}

func (s *Service) bufferFinalize(requestID string, promptTPS *float64, record RequestRecord) (bool, error) {
	state := writebackStateFor(s)
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.enabled {
		return false, nil
	}
	entry, exists := state.entries[requestID]
	if !exists {
		if len(state.entries) >= state.limit {
			return true, ErrWritebackOverflow
		}
		entry = &writebackEntry{requestID: requestID}
		state.entries[requestID] = entry
	}
	entry.record = cloneRequestRecord(record)
	entry.promptTPS = cloneFloat64(promptTPS)
	entry.finalized = true
	return true, nil
}

func (s *Service) bufferRecordCorrelated(requestID string, promptTPS *float64, record RequestRecord) (bool, error) {
	state := writebackStateFor(s)
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.enabled {
		return false, nil
	}
	if _, exists := state.entries[requestID]; exists {
		return true, errors.New("duplicate request_id")
	}
	if len(state.entries) >= state.limit {
		return true, ErrWritebackOverflow
	}
	state.entries[requestID] = &writebackEntry{
		requestID:    requestID,
		record:       cloneRequestRecord(record),
		promptTPS:    cloneFloat64(promptTPS),
		finalized:    true,
		contextReady: true,
	}
	return true, nil
}

func (s *Service) bufferModelSlug(requestID, modelSlug string) (bool, error) {
	state := writebackStateFor(s)
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.enabled {
		return false, nil
	}
	entry, exists := state.entries[requestID]
	if !exists {
		return false, nil
	}
	entry.modelSlug = modelSlug
	return true, nil
}

func (s *Service) bufferOpenAIResponseID(requestID, openAIID string) (bool, error) {
	state := writebackStateFor(s)
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.enabled {
		return false, nil
	}
	entry, exists := state.entries[requestID]
	if !exists {
		return false, nil
	}
	if owner, exists := state.openAIToRequest[openAIID]; exists && owner != requestID {
		return true, ErrDuplicateOpenAIResponseID
	}
	if entry.openAIResponseID != "" && entry.openAIResponseID != openAIID {
		delete(state.openAIToRequest, entry.openAIResponseID)
	}
	entry.openAIResponseID = openAIID
	state.openAIToRequest[openAIID] = requestID
	return true, nil
}

func (s *Service) bufferRequestLogContext(requestID, sessionID, instanceID string) (bool, error) {
	state := writebackStateFor(s)
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.enabled {
		return false, nil
	}
	entry, exists := state.entries[requestID]
	if !exists {
		return false, nil
	}
	entry.sessionID = strings.TrimSpace(sessionID)
	entry.contextInstanceID = strings.TrimSpace(instanceID)
	entry.contextReady = true
	return true, nil
}

func (s *Service) AttachRequestLogContext(ctx context.Context, requestID, sessionID, instanceID string) error {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return errors.New("request_id is required")
	}
	if handled, err := s.bufferRequestLogContext(requestID, sessionID, instanceID); handled {
		return err
	}
	return s.UpdateRequestLogContext(ctx, requestID, sessionID, instanceID)
}

func (s *Service) bufferedRequestModelIdentity(requestID string) (RequestModelIdentity, bool) {
	state := writebackStateFor(s)
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.enabled {
		return RequestModelIdentity{}, false
	}
	entry, exists := state.entries[requestID]
	if !exists {
		return RequestModelIdentity{}, false
	}
	return RequestModelIdentity{InstanceID: entry.record.InstanceID, ModelSlug: entry.modelSlug}, true
}

func (s *Service) bufferedStoredOpenAIResponse(openAIID string) (StoredOpenAIResponse, bool) {
	state := writebackStateFor(s)
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.enabled {
		return StoredOpenAIResponse{}, false
	}
	requestID, exists := state.openAIToRequest[openAIID]
	if !exists {
		return StoredOpenAIResponse{}, false
	}
	entry, exists := state.entries[requestID]
	if !exists {
		return StoredOpenAIResponse{}, false
	}
	return StoredOpenAIResponse{
		InstanceID:   entry.record.InstanceID,
		Endpoint:     entry.record.Endpoint,
		Streaming:    entry.record.Streaming,
		Deleted:      entry.openAIDeleted,
		StartedAt:    entry.record.StartedAt,
		RequestBody:  cloneString(entry.record.RequestBody),
		ResponseBody: cloneString(entry.record.ResponseBody),
	}, true
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func (s *Service) bufferMarkOpenAIResponseDeleted(openAIID string) (bool, error) {
	state := writebackStateFor(s)
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.enabled {
		return false, nil
	}
	requestID, exists := state.openAIToRequest[openAIID]
	if !exists {
		return false, nil
	}
	entry, exists := state.entries[requestID]
	if !exists {
		return false, nil
	}
	if entry.openAIDeleted {
		return true, sql.ErrNoRows
	}
	entry.openAIDeleted = true
	return true, nil
}

func (s *Service) flushWriteback(ctx context.Context, onlyReady bool) (int, error) {
	state := writebackStateFor(s)
	state.mu.Lock()
	if !state.enabled {
		state.mu.Unlock()
		return 0, nil
	}
	batch := make([]writebackEntry, 0, writebackBatchSize)
	for requestID, entry := range state.entries {
		if !entry.finalized || (onlyReady && !entry.contextReady) {
			continue
		}
		batch = append(batch, cloneWritebackEntry(entry))
		delete(state.entries, requestID)
		if entry.openAIResponseID != "" {
			delete(state.openAIToRequest, entry.openAIResponseID)
		}
		if len(batch) >= writebackBatchSize {
			break
		}
	}
	state.mu.Unlock()
	if len(batch) == 0 {
		return 0, nil
	}

	if err := s.persistWritebackBatch(ctx, batch); err != nil {
		state.mu.Lock()
		for i := range batch {
			entry := batch[i]
			if _, exists := state.entries[entry.requestID]; !exists {
				copyEntry := entry
				state.entries[entry.requestID] = &copyEntry
				if entry.openAIResponseID != "" {
					state.openAIToRequest[entry.openAIResponseID] = entry.requestID
				}
			}
		}
		state.mu.Unlock()
		return 0, err
	}
	return len(batch), nil
}

func (s *Service) persistWritebackBatch(ctx context.Context, batch []writebackEntry) error {
	if err := s.EnsureCorrelationSchema(ctx); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	seenOpenAI := map[string]struct{}{}
	for i := range batch {
		entry := batch[i]
		record := normalizeFinalRecord(entry.record)
		keyID, keyName, keyPrefix, ttft, tps, requestBody, responseBody := requestValues(record)
		var promptTPS any
		if entry.promptTPS != nil {
			promptTPS = *entry.promptTPS
		}
		var openAIID any
		if entry.openAIResponseID != "" {
			duplicate := false
			if _, exists := seenOpenAI[entry.openAIResponseID]; exists {
				duplicate = true
			} else {
				var existing int
				err := tx.QueryRowContext(ctx, `SELECT 1 FROM inference_requests WHERE openai_response_id=?`, entry.openAIResponseID).Scan(&existing)
				if err == nil {
					duplicate = true
				} else if err != sql.ErrNoRows {
					return err
				}
			}
			if duplicate {
				slog.Warn("duplicate openai response id ignored during observability writeback", "request_id", entry.requestID, "openai_response_id", entry.openAIResponseID)
			} else {
				seenOpenAI[entry.openAIResponseID] = struct{}{}
				openAIID = entry.openAIResponseID
			}
		}

		inserted, err := tx.ExecContext(ctx, `INSERT INTO inference_requests(
			started_at,finished_at,instance_id,endpoint,api_key_id,api_key_name,api_key_prefix,streaming,status_code,result,
			duration_ms,ttft_ms,prompt_tokens,generated_tokens,total_tokens,tokens_per_second,queue_duration_ms,load_duration_ms,autoloaded,error,request_body,response_body,
			trace_id,call_type,client_ip,user_agent,model_slug,openai_response_id,openai_response_deleted
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			record.StartedAt, record.FinishedAt, record.InstanceID, record.Endpoint, keyID, keyName, keyPrefix, boolInt(record.Streaming), record.StatusCode, record.Result,
			record.DurationMS, ttft, record.PromptTokens, record.GeneratedTokens, record.TotalTokens, tps, record.QueueDurationMS, record.LoadDurationMS, boolInt(record.Autoloaded), record.Error, requestBody, responseBody,
			record.TraceID, record.CallType, record.ClientIP, record.UserAgent, entry.modelSlug, openAIID, boolInt(entry.openAIDeleted))
		if err != nil {
			return err
		}
		rowID, err := inserted.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO inference_request_correlations(request_id,inference_request_id,prompt_tokens_per_second) VALUES(?,?,?)`, entry.requestID, rowID, promptTPS); err != nil {
			return err
		}
		if entry.contextReady {
			modelID, modelName, err := resolveWritebackModelIdentity(ctx, tx, record.InstanceID, entry.contextInstanceID)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO inference_request_log_context(request_id,session_id,model_id,model_name) VALUES(?,?,?,?)`, entry.requestID, entry.sessionID, modelID, modelName); err != nil {
				return err
			}
		}
		if err := addFinalCounters(ctx, tx, record); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func resolveWritebackModelIdentity(ctx context.Context, tx *sql.Tx, durableID, publicID string) (string, string, error) {
	durableID = strings.TrimSpace(durableID)
	publicID = strings.TrimSpace(publicID)
	if durableID == "" && publicID == "" {
		return "", "", nil
	}
	var modelID, modelName string
	err := tx.QueryRowContext(ctx, `SELECT i.model_id,m.name FROM instances i JOIN models m ON m.id=i.model_id WHERE i.id=? OR i.slug=? LIMIT 1`, durableID, publicID).Scan(&modelID, &modelName)
	if err == sql.ErrNoRows {
		return "", "", nil
	}
	return modelID, modelName, err
}
