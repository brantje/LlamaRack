package lifecycle

import (
	"context"
	"log/slog"
)

// ArmStartupReconcile blocks Always-On, autoload, and explicit launches until
// ReconcileStaleWorkers completes. Tests that never arm keep the existing
// immediate-start behavior.
func (s *Service) ArmStartupReconcile() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.startupReady != nil {
		select {
		case <-s.startupReady:
		default:
			return
		}
	}
	s.startupReady = make(chan struct{})
}

func (s *Service) markStartupReady() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.startupReady == nil {
		return
	}
	select {
	case <-s.startupReady:
	default:
		close(s.startupReady)
	}
}

func (s *Service) waitStartupReady(ctx context.Context) error {
	s.mu.Lock()
	ch := s.startupReady
	s.mu.Unlock()
	if ch == nil {
		return nil
	}
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) orphanBlockReason(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.orphanCleanupBlocked[id]
}

// ReconcileStaleWorkers terminates proven orphans, refreshes hardware, then
// allows replacement launches. Cleanup failures block only the affected Instance.
func (s *Service) ReconcileStaleWorkers(ctx context.Context) error {
	defer s.markStartupReady()
	result := s.sup.ReconcileStaleWorkers(ctx)
	s.mu.Lock()
	if s.orphanCleanupBlocked == nil {
		s.orphanCleanupBlocked = map[string]string{}
	}
	for id, reason := range result.Blocked {
		s.orphanCleanupBlocked[id] = reason
	}
	s.mu.Unlock()
	for id, reason := range result.Blocked {
		s.AddManagerLog(id, "stale worker cleanup failed: "+reason)
		slog.Error("reconciliation failure blocks replacement launch", "instance_id", id, "error", reason)
	}
	if _, err := s.hardware.Snapshot(ctx); err != nil {
		slog.Warn("hardware refresh after stale-worker cleanup failed", "error", err)
	} else {
		slog.Info("hardware state refreshed after stale-worker cleanup")
	}
	return nil
}
