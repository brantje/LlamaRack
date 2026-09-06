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
	mu              sync.Mutex
	enabled         bool
	started         bool
	limit           int
	entries         map[string]*writebackEntry
	activeEntries   map[string]*writebackEntry
	openAIToRequest map[string]string
	modelIdentities map[string]writebackModelIdentity
}

type writebackModelIdentity struct {
	modelID   string
	modelName string
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
		activeEntries:   map[string]*writebackEntry{},
		openAIToRequest: map[string]string{},
		modelIdentities: map[string]writebackModelIdentity{},
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
	if s == nil {
		return false
	}
	state := writebackStateFor(s)
	state.mu.Lock()
	enabled := state.enabled
	state.mu.Unlock()
	return enabled
}

func (s *Service) WritebackEnabled() bool {
	return s.writebackEnabled()
}

func (s *Service) bufferBegin(requestID string, record RequestRecord) (bool, error) {
	state := writebackStateFor(s)
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.enabled {
		return false, nil
	}
	if _, exists := state.activeEntries[requestID]; exists {
		return true, errors.New("duplicate request_id")
	}
	if len(state.entries) >= state.limit {
		return true, ErrWritebackOverflow
	}
	entry := &writebackEntry{requestID: requestID, record: cloneRequestRecord(record)}
	state.entries[requestID] = entry
	state.activeEntries[requestID] = entry
	return true, nil
}

func recoverActiveWritebackEntryLocked(state *writebackState, requestID string) (*writebackEntry, bool, error) {
	if entry, exists := state.entries[requestID]; exists {
		return entry, true, nil
	}
	entry, active := state.activeEntries[requestID]
	if !active {
		return nil, false, nil
	}
	if len(state.entries) >= state.limit {
		return nil, true, ErrWritebackOverflow
	}
	state.entries[requestID] = entry
	if entry.openAIResponseID != "" {
		state.openAIToRequest[entry.openAIResponseID] = requestID
	}
	return entry, true, nil
}

func (s *Service) bufferUpdate(requestID string, record RequestRecord) (bool, error) {
	state := writebackStateFor(s)
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.enabled {
		return false, nil
	}
	entry, handled, err := recoverActiveWritebackEntryLocked(state, requestID)
	if err != nil || !handled {
		return handled, err
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
	entry, handled, err := recoverActiveWritebackEntryLocked(state, requestID)
	if err != nil {
		return true, err
	}
	if !handled {
		if len(state.entries) >= state.limit {
			return true, ErrWritebackOverflow
		}
		entry = &writebackEntry{requestID: requestID}
		state.entries[requestID] = entry
		state.activeEntries[requestID] = entry
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
	if _, exists := state.activeEntries[requestID]; exists {
		return true, errors.New("duplicate request_id")
	}
	if len(state.entries) >= state.limit {
		return true, ErrWritebackOverflow
	}
	entry := &writebackEntry{
		requestID:    requestID,
		record:       cloneRequestRecord(record),
		promptTPS:    cloneFloat64(promptTPS),
		finalized:    true,
		contextReady: true,
	}
	state.entries[requestID] = entry
	state.activeEntries[requestID] = entry
	return true, nil
}

func (s *Service) bufferModelSlug(requestID, modelSlug string) (bool, error) {
	state := writebackStateFor(s)
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.enabled {
		return false, nil
	}
	entry, handled, err := recoverActiveWritebackEntryLocked(state, requestID)
	if err != nil || !handled {
		return handled, err
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
	entry, handled, err := recoverActiveWritebackEntryLocked(state, requestID)
	if err != nil || !handled {
		return handled, err
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
	entry, handled, err := recoverActiveWritebackEntryLocked(state, requestID)
	if err != nil || !handled {
		return handled, err
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
	if s.writebackEnabled() {
		return nil
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

	if err := s.persistWritebackBatch(ctx, batch); err == nil {
		s.finishWritebackEntries(batch)
		return len(batch), nil
	}

	processed := 0
	var firstErr error
	for i := range batch {
		entry := batch[i]
		if err := s.persistWritebackBatch(ctx, []writebackEntry{entry}); err != nil {
			if isPermanentWritebackError(err) {
				slog.Error("dropping permanently invalid inference observability writeback entry", "request_id", entry.requestID, "error", err)
				s.finishWritebackEntries([]writebackEntry{entry})
				processed++
				continue
			}
			if firstErr == nil {
				firstErr = err
			}
			state.mu.Lock()
			if _, exists := state.entries[entry.requestID]; !exists {
				copyEntry := entry
				state.entries[entry.requestID] = &copyEntry
				state.activeEntries[entry.requestID] = &copyEntry
				if entry.openAIResponseID != "" {
					state.openAIToRequest[entry.openAIResponseID] = entry.requestID
				}
			}
			state.mu.Unlock()
			continue
		}
		s.finishWritebackEntries([]writebackEntry{entry})
		processed++
	}
	return processed, firstErr
}

func (s *Service) finishWritebackEntries(batch []writebackEntry) {
	state := writebackStateFor(s)
	state.mu.Lock()
	defer state.mu.Unlock()
	for i := range batch {
		requestID := batch[i].requestID
		if _, exists := state.entries[requestID]; !exists {
			delete(state.activeEntries, requestID)
		}
	}
}

func isPermanentWritebackError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "constraint failed") || strings.Contains(message, "constraint violation")
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
	for i := range batch {
		if err := s.persistWritebackEntry(ctx, tx, batch[i]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) persistWritebackEntry(ctx context.Context, tx *sql.Tx, entry writebackEntry) error {
	record := normalizeFinalRecord(entry.record)
	keyID, keyName, keyPrefix, ttft, tps, requestBody, responseBody := requestValues(record)
	var promptTPS any
	if entry.promptTPS != nil {
		promptTPS = *entry.promptTPS
	}

	var rowID int64
	var existingFinishedAt int64
	err := tx.QueryRowContext(ctx, `SELECT r.id,r.finished_at
        FROM inference_request_correlations c
        JOIN inference_requests r ON r.id=c.inference_request_id
        WHERE c.request_id=?`, entry.requestID).Scan(&rowID, &existingFinishedAt)
	existing := err == nil
	if err != nil && err != sql.ErrNoRows {
		return err
	}

	var openAIID any
	if entry.openAIResponseID != "" {
		var ownerRequestID string
		ownerErr := tx.QueryRowContext(ctx, `SELECT COALESCE(c.request_id,'')
            FROM inference_requests r
            LEFT JOIN inference_request_correlations c ON c.inference_request_id=r.id
            WHERE r.openai_response_id=? LIMIT 1`, entry.openAIResponseID).Scan(&ownerRequestID)
		switch {
		case ownerErr == sql.ErrNoRows:
			openAIID = entry.openAIResponseID
		case ownerErr != nil:
			return ownerErr
		case ownerRequestID == entry.requestID:
			openAIID = entry.openAIResponseID
		default:
			slog.Warn("duplicate openai response id ignored during observability writeback", "request_id", entry.requestID, "openai_response_id", entry.openAIResponseID)
		}
	}

	if !existing {
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
		rowID, err = inserted.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO inference_request_correlations(request_id,inference_request_id,prompt_tokens_per_second) VALUES(?,?,?)`, entry.requestID, rowID, promptTPS); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE inference_requests SET
            started_at=?,finished_at=?,instance_id=?,endpoint=?,api_key_id=?,api_key_name=?,api_key_prefix=?,streaming=?,status_code=?,result=?,duration_ms=?,ttft_ms=?,
            prompt_tokens=?,generated_tokens=?,total_tokens=?,tokens_per_second=?,queue_duration_ms=?,load_duration_ms=?,autoloaded=?,error=?,request_body=?,response_body=?,
            trace_id=?,call_type=?,client_ip=?,user_agent=?,model_slug=?,openai_response_id=COALESCE(?,openai_response_id),
            openai_response_deleted=CASE WHEN openai_response_deleted=1 OR ?=1 THEN 1 ELSE 0 END
            WHERE id=?`,
			record.StartedAt, record.FinishedAt, record.InstanceID, record.Endpoint, keyID, keyName, keyPrefix, boolInt(record.Streaming), record.StatusCode, record.Result, record.DurationMS, ttft,
			record.PromptTokens, record.GeneratedTokens, record.TotalTokens, tps, record.QueueDurationMS, record.LoadDurationMS, boolInt(record.Autoloaded), record.Error, requestBody, responseBody,
			record.TraceID, record.CallType, record.ClientIP, record.UserAgent, entry.modelSlug, openAIID, boolInt(entry.openAIDeleted), rowID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE inference_request_correlations SET prompt_tokens_per_second=? WHERE request_id=?`, promptTPS, entry.requestID); err != nil {
			return err
		}
	}

	if entry.contextReady {
		modelID, modelName, err := s.resolveWritebackModelIdentity(ctx, tx, record.InstanceID, entry.contextInstanceID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO inference_request_log_context(request_id,session_id,model_id,model_name) VALUES(?,?,?,?)
            ON CONFLICT(request_id) DO UPDATE SET
                session_id=CASE WHEN excluded.session_id<>'' THEN excluded.session_id ELSE inference_request_log_context.session_id END,
                model_id=CASE WHEN excluded.model_id<>'' THEN excluded.model_id ELSE inference_request_log_context.model_id END,
                model_name=CASE WHEN excluded.model_name<>'' THEN excluded.model_name ELSE inference_request_log_context.model_name END`,
			entry.requestID, entry.sessionID, modelID, modelName); err != nil {
			return err
		}
	}

	if !existing || existingFinishedAt == 0 {
		if err := addFinalCounters(ctx, tx, record); err != nil {
			return err
		}
		if existing && record.Autoloaded {
			if err := addCounter(ctx, tx, Counter{Metric: "autoload_total", InstanceID: record.InstanceID, Value: 1}); err != nil {
				return err
			}
			if record.LoadDurationMS > 0 {
				if err := addCounter(ctx, tx, Counter{Metric: "load_duration_ms_total", InstanceID: record.InstanceID, Value: record.LoadDurationMS}); err != nil {
					return err
				}
			}
			if record.Result != "success" {
				if err := addCounter(ctx, tx, Counter{Metric: "failed_start_total", InstanceID: record.InstanceID, Value: 1}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Service) resolveWritebackModelIdentity(ctx context.Context, tx *sql.Tx, durableID, publicID string) (string, string, error) {
	durableID = strings.TrimSpace(durableID)
	publicID = strings.TrimSpace(publicID)
	if durableID == "" && publicID == "" {
		return "", "", nil
	}
	cacheKey := durableID
	if cacheKey == "" {
		cacheKey = "slug:" + publicID
	}
	state := writebackStateFor(s)
	state.mu.Lock()
	cached, ok := state.modelIdentities[cacheKey]
	state.mu.Unlock()
	if ok {
		return cached.modelID, cached.modelName, nil
	}
	var modelID, modelName string
	err := tx.QueryRowContext(ctx, `SELECT i.model_id,m.name FROM instances i JOIN models m ON m.id=i.model_id WHERE i.id=? OR i.slug=? LIMIT 1`, durableID, publicID).Scan(&modelID, &modelName)
	if err == sql.ErrNoRows {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	state.mu.Lock()
	state.modelIdentities[cacheKey] = writebackModelIdentity{modelID: modelID, modelName: modelName}
	state.mu.Unlock()
	return modelID, modelName, nil
}
