package instances

import "sync"

var instanceHotCaches sync.Map

type instanceHotCache struct {
	mu       sync.RWMutex
	enabled  bool
	byID     map[string]Instance
	slugToID map[string]string
}

func hotCacheFor(s *Service) *instanceHotCache {
	if value, ok := instanceHotCaches.Load(s); ok {
		return value.(*instanceHotCache)
	}
	state := &instanceHotCache{byID: map[string]Instance{}, slugToID: map[string]string{}}
	actual, _ := instanceHotCaches.LoadOrStore(s, state)
	return actual.(*instanceHotCache)
}

func cloneInstance(item Instance) Instance {
	item.GPUDevices = append([]string(nil), item.GPUDevices...)
	return item
}

func (s *Service) EnableHotCache() {
	state := hotCacheFor(s)
	state.mu.Lock()
	state.enabled = true
	state.mu.Unlock()
}

func (s *Service) cachedByID(id string) (Instance, bool) {
	state := hotCacheFor(s)
	state.mu.RLock()
	defer state.mu.RUnlock()
	if !state.enabled {
		return Instance{}, false
	}
	item, ok := state.byID[id]
	return cloneInstance(item), ok
}

func (s *Service) cachedBySlug(slug string) (Instance, bool) {
	state := hotCacheFor(s)
	state.mu.RLock()
	defer state.mu.RUnlock()
	if !state.enabled {
		return Instance{}, false
	}
	id, ok := state.slugToID[slug]
	if !ok {
		return Instance{}, false
	}
	item, ok := state.byID[id]
	return cloneInstance(item), ok
}

func (s *Service) rememberHot(item Instance) {
	state := hotCacheFor(s)
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.enabled {
		return
	}
	copyItem := cloneInstance(item)
	if old, ok := state.byID[item.ID]; ok && old.Slug != item.Slug {
		delete(state.slugToID, old.Slug)
	}
	state.byID[item.ID] = copyItem
	state.slugToID[item.Slug] = item.ID
}

func (s *Service) forgetHot(id string) {
	state := hotCacheFor(s)
	state.mu.Lock()
	defer state.mu.Unlock()
	if item, ok := state.byID[id]; ok {
		delete(state.slugToID, item.Slug)
	}
	delete(state.byID, id)
}
