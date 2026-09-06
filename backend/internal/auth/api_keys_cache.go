package auth

import (
	"sync"
	"time"
)

var apiKeyCaches sync.Map

type apiKeyCacheState struct {
	mu     sync.RWMutex
	byHash map[string]APIKey
}

func apiKeyCacheFor(s *Service) *apiKeyCacheState {
	if value, ok := apiKeyCaches.Load(s); ok {
		return value.(*apiKeyCacheState)
	}
	state := &apiKeyCacheState{byHash: map[string]APIKey{}}
	actual, _ := apiKeyCaches.LoadOrStore(s, state)
	return actual.(*apiKeyCacheState)
}

func cloneAPIKey(item APIKey) APIKey {
	item.InstanceIDs = append([]string(nil), item.InstanceIDs...)
	item.MissingInstanceIDs = append([]string(nil), item.MissingInstanceIDs...)
	if item.ExpiresOn != nil {
		value := *item.ExpiresOn
		item.ExpiresOn = &value
	}
	if item.CreatedByUserID != nil {
		value := *item.CreatedByUserID
		item.CreatedByUserID = &value
	}
	if item.LastUsedAt != nil {
		value := *item.LastUsedAt
		item.LastUsedAt = &value
	}
	return item
}

func (s *Service) cachedAPIKey(hash string) (APIKey, bool) {
	state := apiKeyCacheFor(s)
	state.mu.RLock()
	item, ok := state.byHash[hash]
	state.mu.RUnlock()
	if !ok {
		return APIKey{}, false
	}
	return cloneAPIKey(item), true
}

func (s *Service) rememberAPIKey(hash string, item APIKey) {
	state := apiKeyCacheFor(s)
	state.mu.Lock()
	state.byHash[hash] = cloneAPIKey(item)
	state.mu.Unlock()
}

func (s *Service) stampCachedAPIKey(hash string, at int64) APIKey {
	state := apiKeyCacheFor(s)
	state.mu.Lock()
	item := state.byHash[hash]
	value := at
	item.LastUsedAt = &value
	state.byHash[hash] = item
	state.mu.Unlock()
	return cloneAPIKey(item)
}

func (s *Service) clearAPIKeyCache() {
	state := apiKeyCacheFor(s)
	state.mu.Lock()
	state.byHash = map[string]APIKey{}
	state.mu.Unlock()
}

func (s *Service) seedAPIUseWrite(id string, lastUsedAt *int64) {
	if lastUsedAt == nil || *lastUsedAt <= 0 {
		return
	}
	seeded := time.Unix(*lastUsedAt, 0)
	s.mu.Lock()
	if current, ok := s.lastAPIKeyWrite[id]; !ok || seeded.After(current) {
		s.lastAPIKeyWrite[id] = seeded
	}
	s.mu.Unlock()
}
