package supervisor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"syscall"
	"time"
)

var (
	staleTermTimeout = 15 * time.Second
	staleKillTimeout = 5 * time.Second
	stalePortTimeout = 5 * time.Second
)

// ReconcileResult reports startup stale-worker cleanup.
type ReconcileResult struct {
	Detected        int
	Terminated      int
	Rejected        int
	CleanedMetadata int
	Blocked         map[string]string
}

func (s *Supervisor) procScanner() ProcScanner {
	s.mu.RLock()
	scanner := s.scanner
	s.mu.RUnlock()
	if scanner != nil {
		return scanner
	}
	return LinuxProcScanner{}
}

func (s *Supervisor) liveGenerations() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]string{}
	for id, w := range s.workers {
		if w == nil || w.generation == "" || w.runtime.PID == 0 {
			continue
		}
		switch w.runtime.State {
		case Unloaded, Failed:
			continue
		default:
			out[id] = w.generation
		}
	}
	return out
}

// ReconcileStaleWorkers identifies and terminates workers owned by this
// installation that are not the current in-memory generation.
func (s *Supervisor) ReconcileStaleWorkers(ctx context.Context) ReconcileResult {
	result := ReconcileResult{Blocked: map[string]string{}}
	s.mu.RLock()
	installID := s.installationID
	store := s.store
	s.mu.RUnlock()
	if installID == "" {
		slog.Info("startup worker reconciliation skipped", "reason", "no installation identity")
		return result
	}

	scanner := s.procScanner()
	live := s.liveGenerations()
	processes, err := scanner.List()
	if err != nil {
		slog.Error("startup worker reconciliation failed to list processes", "error", err)
		if store != nil {
			records, listErr := store.List(ctx)
			if listErr == nil {
				for _, rec := range records {
					if live[rec.InstanceID] == rec.Generation {
						continue
					}
					result.Blocked[rec.InstanceID] = "process scan failed: " + err.Error()
					slog.Error("reconciliation failure blocks replacement launch", "instance_id", rec.InstanceID, "error", err)
				}
			}
		}
		return result
	}

	byPID := make(map[int]Proc, len(processes))
	var owned []Proc
	for _, proc := range processes {
		byPID[proc.PID] = proc
		if ownedProc, ok := ownedByInstallation(proc, installID); ok {
			owned = append(owned, ownedProc)
		} else if proc.Environ[EnvInstallationID] != "" || proc.Environ[EnvInstanceID] != "" || proc.Environ[EnvWorkerGeneration] != "" {
			result.Rejected++
			slog.Info("ownership verification rejected", "pid", proc.PID, "reason", "incomplete or foreign identity metadata")
		}
	}

	handled := map[string]bool{}
	if store != nil {
		records, listErr := store.List(ctx)
		if listErr != nil {
			slog.Error("startup worker reconciliation failed to load runtime metadata", "error", listErr)
		} else {
			for _, rec := range records {
				if ctx.Err() != nil {
					break
				}
				if live[rec.InstanceID] == rec.Generation {
					slog.Info("startup reconciliation skipped current-generation worker", "instance_id", rec.InstanceID, "pid", rec.PID)
					continue
				}
				proc, ok := byPID[rec.PID]
				if !ok || startIdentityMismatch(rec, proc) {
					slog.Info("stale runtime metadata cleanup", "instance_id", rec.InstanceID, "pid", rec.PID, "reason", "dead pid or start identity mismatch")
					if delErr := store.Delete(ctx, rec.InstanceID); delErr != nil {
						slog.Warn("failed to clear stale worker metadata", "instance_id", rec.InstanceID, "error", delErr)
					} else {
						result.CleanedMetadata++
					}
					if ok {
						result.Rejected++
						slog.Info("ownership verification rejected", "instance_id", rec.InstanceID, "pid", rec.PID, "reason", "start identity mismatch")
					}
					continue
				}
				if !recordOwnsProcess(rec, proc, installID) {
					slog.Info("ownership verification rejected", "instance_id", rec.InstanceID, "pid", rec.PID, "reason", "generation or installation mismatch")
					result.Rejected++
					if delErr := store.Delete(ctx, rec.InstanceID); delErr != nil {
						slog.Warn("failed to clear stale worker metadata", "instance_id", rec.InstanceID, "error", delErr)
					} else {
						result.CleanedMetadata++
					}
					continue
				}
				result.Detected++
				slog.Info("stale managed worker detected", "instance_id", rec.InstanceID, "pid", rec.PID, "port", rec.Port)
				slog.Info("ownership verification result", "instance_id", rec.InstanceID, "pid", rec.PID, "owned", true)
				if err := s.terminateOwned(ctx, scanner, rec, proc); err != nil {
					result.Blocked[rec.InstanceID] = err.Error()
					slog.Error("reconciliation failure blocks replacement launch", "instance_id", rec.InstanceID, "error", err)
					handled[rec.InstanceID+"/"+rec.Generation] = true
					continue
				}
				if delErr := store.Delete(ctx, rec.InstanceID); delErr != nil {
					slog.Warn("failed to clear stale worker metadata", "instance_id", rec.InstanceID, "error", delErr)
				} else {
					result.CleanedMetadata++
				}
				result.Terminated++
				handled[rec.InstanceID+"/"+rec.Generation] = true
			}
		}
	}

	for _, proc := range owned {
		if ctx.Err() != nil {
			break
		}
		instanceID := proc.Environ[EnvInstanceID]
		generation := proc.Environ[EnvWorkerGeneration]
		if live[instanceID] == generation {
			continue
		}
		if handled[instanceID+"/"+generation] {
			continue
		}
		rec := WorkerRecord{
			InstanceID: instanceID,
			Generation: generation,
			PID:        proc.PID,
			StartTicks: proc.StartTicks,
			Port:       parsePortEnv(proc.Environ),
		}
		result.Detected++
		slog.Info("stale managed worker detected", "instance_id", instanceID, "pid", proc.PID, "port", rec.Port)
		slog.Info("ownership verification result", "instance_id", instanceID, "pid", proc.PID, "owned", true)
		if err := s.terminateOwned(ctx, scanner, rec, proc); err != nil {
			result.Blocked[instanceID] = err.Error()
			slog.Error("reconciliation failure blocks replacement launch", "instance_id", instanceID, "error", err)
			continue
		}
		if store != nil {
			existing, getErr := store.Get(ctx, instanceID)
			if getErr == nil && existing.Generation == generation {
				if delErr := store.Delete(ctx, instanceID); delErr != nil {
					slog.Warn("failed to clear stale worker metadata", "instance_id", instanceID, "error", delErr)
				} else {
					result.CleanedMetadata++
				}
			}
		}
		result.Terminated++
		handled[instanceID+"/"+generation] = true
	}

	slog.Info("startup worker reconciliation complete",
		"detected", result.Detected,
		"terminated", result.Terminated,
		"rejected", result.Rejected,
		"cleaned_metadata", result.CleanedMetadata,
		"blocked", len(result.Blocked),
	)
	return result
}

func ownedByInstallation(proc Proc, installID string) (Proc, bool) {
	if proc.Environ == nil {
		return Proc{}, false
	}
	if proc.Environ[EnvInstallationID] != installID {
		return Proc{}, false
	}
	if proc.Environ[EnvInstanceID] == "" || proc.Environ[EnvWorkerGeneration] == "" {
		return Proc{}, false
	}
	return proc, true
}

func startIdentityMismatch(rec WorkerRecord, proc Proc) bool {
	return rec.StartTicks != 0 && proc.StartTicks != rec.StartTicks
}

func recordOwnsProcess(rec WorkerRecord, proc Proc, installID string) bool {
	if proc.Environ[EnvInstallationID] != installID {
		return false
	}
	if proc.Environ[EnvInstanceID] != rec.InstanceID {
		return false
	}
	if proc.Environ[EnvWorkerGeneration] != rec.Generation {
		return false
	}
	return true
}

func (s *Supervisor) terminateOwned(ctx context.Context, scanner ProcScanner, rec WorkerRecord, proc Proc) error {
	slog.Info("stale worker termination attempt", "instance_id", rec.InstanceID, "pid", proc.PID, "port", rec.Port)
	if err := scanner.Signal(proc.PID, syscall.SIGTERM); err != nil && !isProcessGone(err) {
		slog.Error("stale worker termination failed", "instance_id", rec.InstanceID, "pid", proc.PID, "error", err)
		return err
	}
	if err := waitProcessGone(ctx, scanner, proc.PID, proc.StartTicks, staleTermTimeout); err != nil {
		slog.Warn("stale worker did not exit after SIGTERM; killing", "instance_id", rec.InstanceID, "pid", proc.PID)
		if killErr := scanner.Signal(proc.PID, syscall.SIGKILL); killErr != nil && !isProcessGone(killErr) {
			slog.Error("stale worker termination failed", "instance_id", rec.InstanceID, "pid", proc.PID, "error", killErr)
			return killErr
		}
		if err := waitProcessGone(ctx, scanner, proc.PID, proc.StartTicks, staleKillTimeout); err != nil {
			slog.Error("stale worker termination failed", "instance_id", rec.InstanceID, "pid", proc.PID, "error", err)
			return err
		}
	}
	if rec.Port > 0 {
		if err := waitPortReleased(ctx, s.host, rec.Port, stalePortTimeout); err != nil {
			slog.Error("stale worker port still occupied", "instance_id", rec.InstanceID, "pid", proc.PID, "port", rec.Port, "error", err)
			return err
		}
	}
	slog.Info("stale worker termination succeeded", "instance_id", rec.InstanceID, "pid", proc.PID, "port", rec.Port)
	return nil
}

func waitProcessGone(ctx context.Context, scanner ProcScanner, pid int, startTicks uint64, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !scanner.Alive(pid, startTicks) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("pid %d did not exit", pid)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func waitPortReleased(ctx context.Context, host string, port int, timeout time.Duration) error {
	if port <= 0 {
		return nil
	}
	addr := net.JoinHostPort(host, fmt.Sprint(port))
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			_ = ln.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("port %d still occupied", port)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func isProcessGone(err error) bool {
	return errors.Is(err, syscall.ESRCH)
}
