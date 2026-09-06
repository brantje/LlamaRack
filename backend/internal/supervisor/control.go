package supervisor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/brantje/llamarack/backend/internal/systemlog"
)

func (s *Supervisor) BeginDrain(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.workers[id]
	if w == nil || w.runtime.State != Ready { return false }
	w.runtime.State = Draining
	s.emitRuntimeLocked(w.runtime)
	slog.Info("llama-server worker draining", "instance_id", id, "model_id", w.runtime.ModelID, "pid", w.runtime.PID)
	return true
}

func (s *Supervisor) AbortDrain(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.workers[id]
	if w == nil || w.runtime.State != Draining || w.cmd == nil || w.cmd.Process == nil { return false }
	select { case <-w.done: return false; default: }
	w.runtime.State = Ready
	s.emitRuntimeLocked(w.runtime)
	slog.Info("llama-server worker drain aborted", "instance_id", id, "model_id", w.runtime.ModelID, "pid", w.runtime.PID)
	return true
}

func (s *Supervisor) WaitInactive(ctx context.Context, id string) error {
	for {
		s.mu.RLock()
		w := s.workers[id]
		if w == nil { s.mu.RUnlock(); return nil }
		state := w.runtime.State
		done := w.done
		s.mu.RUnlock()
		if state == Unloaded || state == Failed { return nil }
		select { case <-ctx.Done(): return ctx.Err(); case <-done: return nil }
	}
}

func (s *Supervisor) Stop(ctx context.Context, id string) error {
	s.mu.Lock()
	w := s.workers[id]
	if w == nil || w.cmd == nil || w.cmd.Process == nil {
		s.mu.Unlock()
		slog.Info("llama-server worker already stopped", "instance_id", id)
		return nil
	}
	done := w.done
	select { case <-done: s.mu.Unlock(); slog.Info("llama-server worker already exited", "instance_id", id); return nil; default: }
	w.runtime.State = Stopping
	s.emitRuntimeLocked(w.runtime)
	p := w.cmd.Process
	modelID := w.runtime.ModelID
	pid := w.runtime.PID
	s.mu.Unlock()
	slog.Info("stopping llama-server worker", "instance_id", id, "model_id", modelID, "pid", pid)
	_ = p.Signal(syscall.SIGTERM)
	select {
	case <-done:
		slog.Info("llama-server worker stopped", "instance_id", id, "model_id", modelID, "pid", pid)
		return nil
	case <-ctx.Done():
		_ = p.Kill()
		slog.Warn("llama-server stop cancelled; killing worker", "instance_id", id, "model_id", modelID, "pid", pid, "error", ctx.Err())
		return ctx.Err()
	case <-time.After(15 * time.Second):
		_ = p.Kill(); <-done
		slog.Warn("llama-server worker did not stop after SIGTERM; killed", "instance_id", id, "model_id", modelID, "pid", pid)
		return nil
	}
}

func (s *Supervisor) Status(id string) Runtime {
	s.mu.RLock(); defer s.mu.RUnlock()
	if w := s.workers[id]; w != nil { return w.runtime }
	return Runtime{InstanceID: id, State: Unloaded}
}

func (s *Supervisor) Endpoint(id string) (string, bool) {
	rt := s.Status(id)
	if rt.State != Ready { return "", false }
	return fmt.Sprintf("http://%s:%d", s.host, rt.Port), true
}

func (s *Supervisor) Logs(id string) []string {
	s.mu.RLock(); logRing := s.logs[id]; s.mu.RUnlock()
	if logRing == nil { return nil }
	return logRing.lines()
}

func (s *Supervisor) SubscribeLogs(id string) ([]string, <-chan string, func()) {
	s.mu.Lock(); logRing := s.logRingLocked(id); s.mu.Unlock()
	return logRing.subscribe()
}

func (s *Supervisor) logRingLocked(id string) *ring {
	logRing := s.logs[id]
	if logRing == nil { logRing = newRing(2000); s.logs[id] = logRing }
	return logRing
}

func (s *Supervisor) Shutdown(ctx context.Context) {
	s.mu.RLock()
	ids := make([]string, 0, len(s.workers))
	for id := range s.workers { ids = append(ids, id) }
	s.mu.RUnlock()
	var wg sync.WaitGroup
	for _, id := range ids { wg.Add(1); go func(id string) { defer wg.Done(); _ = s.Stop(ctx, id) }(id) }
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select { case <-done: case <-ctx.Done(): }
}

func (s *Supervisor) wait(w *worker) {
	err := w.cmd.Wait()
	s.mu.Lock()
	if s.workers[w.runtime.InstanceID] != w { s.mu.Unlock(); return }
	wasStopping := w.runtime.State == Stopping
	instanceID := w.runtime.InstanceID
	modelID := w.runtime.ModelID
	generation := w.generation
	w.runtime.PID = 0
	if wasStopping {
		w.runtime.State = Unloaded; w.runtime.LastError = ""
	} else {
		w.runtime.State = Failed
		if err != nil { w.runtime.LastError = err.Error() } else { w.runtime.LastError = "worker exited unexpectedly" }
	}
	state := w.runtime.State
	lastError := w.runtime.LastError
	stderrTail := lastStoredLogText(w.logs.lines(), "stderr")
	s.emitRuntimeLocked(w.runtime)
	close(w.done)
	s.mu.Unlock()
	s.clearRuntimeRecord(instanceID, generation)
	if wasStopping {
		slog.Info("llama-server process exited", "instance_id", instanceID, "model_id", modelID, "state", state)
	} else {
		message := lastError
		if stderrTail != "" { message += ": " + stderrTail }
		systemlog.Log(systemlog.Error, instanceID, message)
		slog.Error("llama-server process exited unexpectedly", "instance_id", instanceID, "model_id", modelID, "state", state, "error", lastError)
	}
}

func (s *Supervisor) waitReady(ctx context.Context, w *worker, port int, done <-chan struct{}) error {
	client := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(500 * time.Millisecond); defer ticker.Stop()
	url := fmt.Sprintf("http://%s:%d/health", s.host, port)
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := client.Do(req)
		if err == nil { _ = resp.Body.Close(); if resp.StatusCode >= 200 && resp.StatusCode < 300 { return nil } }
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) { return fmt.Errorf("%w: %w", ErrKilled, ctx.Err()) }
			return fmt.Errorf("worker readiness timeout: %w", ctx.Err())
		case <-done:
			s.mu.RLock(); killed := w != nil && w.killed; s.mu.RUnlock()
			if killed { return ErrKilled }
			return errors.New("worker exited during startup")
		case <-ticker.C:
		}
	}
}

func (s *Supervisor) persistWorker(instanceID, generation string, pid, port int) error {
	if s == nil || s.store == nil || instanceID == "" || generation == "" || pid <= 0 { return nil }
	ticks, err := s.lookupStartTicks(pid)
	if err != nil { return fmt.Errorf("worker start identity: %w", err) }
	if ticks == 0 { return errors.New("worker start identity is unavailable") }
	s.mu.Lock()
	if w := s.workers[instanceID]; w != nil && w.generation == generation { w.startTicks = ticks }
	s.mu.Unlock()
	rec := WorkerRecord{InstanceID: instanceID, Generation: generation, PID: pid, StartTicks: ticks, Port: port}
	if err := s.store.Upsert(context.Background(), rec); err != nil { return fmt.Errorf("persist worker runtime metadata: %w", err) }
	return nil
}

func (s *Supervisor) abortStartedWorker(instanceID string, done <-chan struct{}, cause error) (Runtime, error) {
	_ = s.Kill(instanceID)
	select { case <-done: case <-time.After(2 * time.Second): }
	s.setState(instanceID, Failed, cause.Error())
	return s.Status(instanceID), cause
}

func (s *Supervisor) clearRuntimeRecord(instanceID, generation string) {
	if s == nil || s.store == nil || instanceID == "" { return }
	if generation != "" {
		rec, err := s.store.Get(context.Background(), instanceID)
		if err != nil {
			if !errors.Is(err, ErrRuntimeNotFound) { slog.Warn("failed to load worker runtime metadata", "instance_id", instanceID, "error", err) }
			return
		}
		if rec.Generation != generation { return }
	}
	if err := s.store.Delete(context.Background(), instanceID); err != nil { slog.Warn("failed to clear worker runtime metadata", "instance_id", instanceID, "error", err) }
}

func (s *Supervisor) lookupStartTicks(pid int) (uint64, error) {
	s.mu.RLock(); scanner := s.scanner; s.mu.RUnlock()
	if scanner != nil {
		proc, err := scanner.Inspect(pid)
		if err != nil { return 0, err }
		return proc.StartTicks, nil
	}
	ticks := readStartTicks(pid)
	if ticks == 0 { return 0, errors.New("process start identity is unavailable") }
	return ticks, nil
}

func (s *Supervisor) allocatePortLocked() (int, error) {
	for p := s.portStart; p < s.portStart+2000; p++ {
		used := false
		for _, w := range s.workers {
			if w.runtime.Port == p && w.runtime.State != Unloaded && w.runtime.State != Failed { used = true; break }
		}
		if used { continue }
		ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", s.host, p))
		if err != nil { continue }
		_ = ln.Close(); return p, nil
	}
	return 0, errors.New("no worker port available")
}

func (s *Supervisor) setState(id string, state State, msg string) {
	s.mu.Lock(); defer s.mu.Unlock()
	if w := s.workers[id]; w != nil { w.runtime.State = state; w.runtime.LastError = msg; s.emitRuntimeLocked(w.runtime) }
}

var workerOwnedValueOptions = map[string]bool{"alias": true, "model": true, "host": true, "port": true, "cors-origins": true, "cors-methods": true, "cors-headers": true, "api-key": true, "api-key-file": true, "slot-save-path": true}
var workerOwnedBooleanOptions = map[string]bool{"cors-credentials": true, "no-cors-credentials": true, "slots": true, "no-slots": true}

func sanitizeWorkerOwnedArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		raw := args[i]
		key := strings.TrimLeft(raw, "-")
		if at := strings.IndexByte(key, '='); at >= 0 {
			base := key[:at]
			if workerOwnedValueOptions[base] || workerOwnedBooleanOptions[base] { continue }
		}
		if workerOwnedValueOptions[key] { if i+1 < len(args) { i++ }; continue }
		if workerOwnedBooleanOptions[key] { continue }
		out = append(out, raw)
	}
	return out
}

type ring struct { mu sync.Mutex; max int; data []string; subs map[chan string]struct{} }
func newRing(max int) *ring { return &ring{max: max, subs: map[chan string]struct{}{}} }
func (r *ring) reset() { r.mu.Lock(); r.data = nil; r.mu.Unlock() }
func (r *ring) add(line string) {
	r.mu.Lock()
	if len(r.data) >= r.max { copy(r.data, r.data[1:]); r.data[len(r.data)-1] = line } else { r.data = append(r.data, line) }
	for ch := range r.subs { select { case ch <- line: default: } }
	r.mu.Unlock()
}
func (r *ring) lines() []string { r.mu.Lock(); defer r.mu.Unlock(); out := make([]string, len(r.data)); copy(out, r.data); return out }
func (r *ring) subscribe() ([]string, <-chan string, func()) {
	r.mu.Lock(); snapshot := make([]string, len(r.data)); copy(snapshot, r.data); ch := make(chan string, 128); r.subs[ch] = struct{}{}; r.mu.Unlock()
	var once sync.Once
	cancel := func() { once.Do(func() { r.mu.Lock(); delete(r.subs, ch); close(ch); r.mu.Unlock() }) }
	return snapshot, ch, cancel
}

func lastStoredLogText(lines []string, source string) string {
	prefix := "[" + source + "]\t"
	for index := len(lines)-1; index >= 0; index-- {
		if !strings.HasPrefix(lines[index], prefix) { continue }
		parts := strings.SplitN(lines[index], "\t", 3)
		if len(parts) == 3 { return parts[2] }
	}
	return ""
}

func copyLogs(dst *ring, instanceID, modelID, source string, reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		dst.add(formatStoredLogLine(source, line))
		systemlog.Log(systemlog.Info, instanceID, line)
		slog.Info("llama-server output", "instance_id", instanceID, "model_id", modelID, "stream", source, "line", line)
	}
}
