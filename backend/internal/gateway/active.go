package gateway

import (
	"net/url"
	"sync"
	"time"
)

type activeRequest struct {
	managerRequestID string
	instanceID       string
	ownerKind        string
	ownerID          string
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

type cancelAuthResult int

const (
	cancelAuthOK        cancelAuthResult = 0
	cancelAuthNotFound  cancelAuthResult = 1
	cancelAuthForbidden cancelAuthResult = 2
)

func (r *activeRegistry) cancelByUpstream(id string) (*activeRequest, bool) {
	entry, cancelled, _ := r.cancelByUpstreamAuthorized(id, nil, nil)
	return entry, cancelled
}

func (r *activeRegistry) cancelByUpstreamAuthorized(id string, ownerAllowed func(ownerKind, ownerID string) bool, instanceAllowed func(instanceID string) bool) (*activeRequest, bool, cancelAuthResult) {
	if r == nil || id == "" {
		return nil, false, cancelAuthNotFound
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.byUpstream[id]
	if entry == nil {
		return nil, false, cancelAuthNotFound
	}
	copy := *entry
	if ownerAllowed != nil && !ownerAllowed(entry.ownerKind, entry.ownerID) {
		return &copy, false, cancelAuthNotFound
	}
	if instanceAllowed != nil && !instanceAllowed(entry.instanceID) {
		return &copy, false, cancelAuthForbidden
	}
	if entry.cancelled {
		return &copy, false, cancelAuthOK
	}
	entry.cancelled = true
	if entry.cancel != nil {
		entry.cancel()
	}
	copy = *entry
	return &copy, true, cancelAuthOK
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
