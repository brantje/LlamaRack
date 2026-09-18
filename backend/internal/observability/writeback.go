package observability

import (
	"github.com/brantje/llamarack/backend/internal/database"
	"context"
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
		OwnerKind:    entry.record.OwnerKind,
		OwnerID:      entry.record.OwnerID,
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
		return true, database.ErrNotFound
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
	return errors.Is(err, database.ErrIntegrity)
}

func (s *Service) persistWritebackBatch(ctx context.Context, batch []writebackEntry) error {
	if err := s.EnsureCorrelationSchema(ctx); err != nil {
		return err
	}
	return s.store.PersistWritebackBatch(ctx, batch)
}
