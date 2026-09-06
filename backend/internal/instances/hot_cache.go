package instances

import "sync"

type instanceHotCache struct {
	mu         sync.RWMutex
	enabled    bool
	generation uint64
	byID       map[string]Instance
	slugToID   map[string]string
}

func hotCacheFor(s *Service) *instanceHotCache {
	return &s.hotCache
}

func cloneInstance(item Instance) Instance {
	item.GPUDevices = append([]string(nil), item.GPUDevices...)
	return item
}

func (s *Service) EnableHotCache() {
	state := hotCacheFor(s)
	state.mu.Lock()
	if !state.enabled {
		state.enabled = true
		state.generation++
	}
	state.mu.Unlock()
}

func (s *Service) cachedByIDAtGeneration(id string) (Instance, uint64, bool) {
	state := hotCacheFor(s)
	state.mu.RLock()
	defer state.mu.RUnlock()
	generation := state.generation
	if !state.enabled {
		return Instance{}, generation, false
	}
	item, ok := state.byID[id]
	return cloneInstance(item), generation, ok
}

func (s *Service) cachedByID(id string) (Instance, bool) {
	item, _, ok := s.cachedByIDAtGeneration(id)
	return item, ok
}

func (s *Service) cachedBySlugAtGeneration(slug string) (Instance, uint64, bool) {
	state := hotCacheFor(s)
	state.mu.RLock()
	defer state.mu.RUnlock()
	generation := state.generation
	if !state.enabled {
		return Instance{}, generation, false
	}
	id, ok := state.slugToID[slug]
	if !ok {
		return Instance{}, generation, false
	}
	item, ok := state.byID[id]
	return cloneInstance(item), generation, ok
}

func (s *Service) cachedBySlug(slug string) (Instance, bool) {
	item, _, ok := s.cachedBySlugAtGeneration(slug)
	return item, ok
}

func rememberHotLocked(state *instanceHotCache, item Instance) {
	copyItem := cloneInstance(item)
	if old, ok := state.byID[item.ID]; ok && old.Slug != item.Slug {
		delete(state.slugToID, old.Slug)
	}
	state.byID[item.ID] = copyItem
	state.slugToID[item.Slug] = item.ID
}

func (s *Service) rememberHotIfGeneration(item Instance, generation uint64) bool {
	state := hotCacheFor(s)
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.enabled {
		return true
	}
	if state.generation != generation {
		return false
	}
	rememberHotLocked(state, item)
	return true
}

func (s *Service) rememberHot(item Instance) {
	state := hotCacheFor(s)
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.enabled {
		return
	}
	state.generation++
	rememberHotLocked(state, item)
}

func (s *Service) forgetHot(id string) {
	state := hotCacheFor(s)
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.enabled {
		return
	}
	state.generation++
	if item, ok := state.byID[id]; ok {
		delete(state.slugToID, item.Slug)
	}
	delete(state.byID, id)
}
