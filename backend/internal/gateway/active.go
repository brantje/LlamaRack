package gateway

import (
	"net/url"
	"sync"
	"time"
)

type activeRequest struct {
	managerRequestID string
	instanceID       string
	target           *url.URL
	cancel           func()
	upstreamID       string
	endpoint         string
	startedAt        int64
	model            string
	cancelled        bool
}

type activeRegistry struct {
	mu         sync.Mutex
	byManager  map[string]*activeRequest
	byUpstream map[string]*activeRequest
}

func newActiveRegistry() *activeRegistry {
	return &activeRegistry{
		byManager:  map[string]*activeRequest{},
		byUpstream: map[string]*activeRequest{},
	}
}

func (r *activeRegistry) register(entry *activeRequest) {
	if r == nil || entry == nil || entry.managerRequestID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byManager[entry.managerRequestID] = entry
	if entry.upstreamID != "" {
		r.byUpstream[entry.upstreamID] = entry
	}
}

func (r *activeRegistry) setUpstreamID(managerID, upstreamID string) {
	if r == nil || managerID == "" || upstreamID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.byManager[managerID]
	if !ok {
		return
	}
	if entry.upstreamID != "" && entry.upstreamID != upstreamID {
		delete(r.byUpstream, entry.upstreamID)
	}
	entry.upstreamID = upstreamID
	r.byUpstream[upstreamID] = entry
}

func (r *activeRegistry) getByUpstream(id string) *activeRequest {
	if r == nil || id == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.byUpstream[id]
	if entry == nil {
		return nil
	}
	copy := *entry
	return &copy
}

func (r *activeRegistry) cancelByUpstream(id string) (*activeRequest, bool) {
	entry, cancelled, _ := r.cancelByUpstreamAuthorized(id, nil)
	return entry, cancelled
}

func (r *activeRegistry) cancelByUpstreamAuthorized(id string, allowed func(string) bool) (*activeRequest, bool, bool) {
	if r == nil || id == "" {
		return nil, false, true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.byUpstream[id]
	if entry == nil {
		return nil, false, true
	}
	copy := *entry
	if allowed != nil && !allowed(entry.instanceID) {
		return &copy, false, false
	}
	if entry.cancelled {
		return &copy, false, true
	}
	entry.cancelled = true
	if entry.cancel != nil {
		entry.cancel()
	}
	copy = *entry
	return &copy, true, true
}

func (r *activeRegistry) remove(managerID string) {
	if r == nil || managerID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.byManager[managerID]
	if !ok {
		return
	}
	delete(r.byManager, managerID)
	if entry.upstreamID != "" && r.byUpstream[entry.upstreamID] == entry {
		delete(r.byUpstream, entry.upstreamID)
	}
}

func (r *activeRegistry) waitRemoved(managerID string, timeout time.Duration) bool {
	if r == nil || managerID == "" {
		return true
	}
	deadline := time.Now().Add(timeout)
	for {
		r.mu.Lock()
		_, ok := r.byManager[managerID]
		r.mu.Unlock()
		if !ok {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}
