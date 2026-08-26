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
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type State string

const (
	Unloaded State = "UNLOADED"
	Starting State = "STARTING"
	Loading  State = "LOADING"
	Ready    State = "READY"
	Stopping State = "STOPPING"
	Failed   State = "FAILED"
)

type Runtime struct {
	InstanceID string    `json:"instance_id"`
	ModelID    string    `json:"model_id"`
	State      State     `json:"state"`
	PID        int       `json:"pid,omitempty"`
	Port       int       `json:"port,omitempty"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	ReadyAt    time.Time `json:"ready_at,omitempty"`
	LastError  string    `json:"last_error,omitempty"`
}

type worker struct {
	runtime Runtime
	cmd     *exec.Cmd
	logs    *ring
	done    chan struct{}
}

type Supervisor struct {
	mu             sync.RWMutex
	binary         string
	host           string
	portStart      int
	startupTimeout time.Duration
	workers        map[string]*worker
	logs           map[string]*ring
}

func New(binary, host string, portStart int, startupTimeout time.Duration) *Supervisor {
	return &Supervisor{
		binary: binary, host: host, portStart: portStart, startupTimeout: startupTimeout,
		workers: map[string]*worker{}, logs: map[string]*ring{},
	}
}

func (s *Supervisor) Start(ctx context.Context, instanceID, modelID, modelPath string, args []string) (Runtime, error) {
	s.mu.Lock()
	if w := s.workers[instanceID]; w != nil && w.runtime.State != Unloaded && w.runtime.State != Failed {
		rt := w.runtime
		s.mu.Unlock()
		slog.Info("llama-server worker already active", "instance_id", instanceID, "model_id", modelID, "state", rt.State, "pid", rt.PID)
		return rt, nil
	}
	port, err := s.allocatePortLocked()
	if err != nil {
		s.mu.Unlock()
		slog.Error("unable to allocate llama-server port", "instance_id", instanceID, "model_id", modelID, "error", err)
		return Runtime{}, err
	}
	workerArgs := []string{"--model", modelPath, "--host", s.host, "--port", fmt.Sprint(port)}
	workerArgs = append(workerArgs, args...)
	cmd := exec.Command(s.binary, workerArgs...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		s.mu.Unlock()
		return Runtime{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		s.mu.Unlock()
		return Runtime{}, err
	}
	logRing := s.logRingLocked(instanceID)
	logRing.reset()
	w := &worker{runtime: Runtime{InstanceID: instanceID, ModelID: modelID, State: Starting, Port: port, StartedAt: time.Now().UTC()}, logs: logRing, done: make(chan struct{})}
	s.workers[instanceID] = w
	slog.Info("starting llama-server worker", "instance_id", instanceID, "model_id", modelID, "binary", s.binary, "model_path", modelPath, "host", s.host, "port", port, "args", workerArgs)
	if err := cmd.Start(); err != nil {
		w.runtime.State = Failed
		w.runtime.LastError = err.Error()
		s.mu.Unlock()
		slog.Error("failed to start llama-server worker", "instance_id", instanceID, "model_id", modelID, "error", err)
		return w.runtime, err
	}
	w.cmd = cmd
	w.runtime.PID = cmd.Process.Pid
	pid := w.runtime.PID
	s.mu.Unlock()
	slog.Info("llama-server process started", "instance_id", instanceID, "model_id", modelID, "pid", pid, "port", port)
	go copyLogs(w.logs, instanceID, modelID, "stdout", stdout)
	go copyLogs(w.logs, instanceID, modelID, "stderr", stderr)
	go s.wait(w)
	s.setState(instanceID, Loading, "")

	readyCtx, cancel := context.WithTimeout(ctx, s.startupTimeout)
	defer cancel()
	if err := s.waitReady(readyCtx, port); err != nil {
		slog.Error("llama-server worker readiness failed", "instance_id", instanceID, "model_id", modelID, "pid", pid, "port", port, "error", err)
		_ = s.Stop(context.Background(), instanceID)
		s.setState(instanceID, Failed, err.Error())
		return s.Status(instanceID), err
	}
	s.mu.Lock()
	if current := s.workers[instanceID]; current != nil {
		current.runtime.State = Ready
		current.runtime.ReadyAt = time.Now().UTC()
	}
	rt := s.workers[instanceID].runtime
	s.mu.Unlock()
	slog.Info("llama-server worker ready", "instance_id", instanceID, "model_id", modelID, "pid", rt.PID, "port", rt.Port)
	return rt, nil
}

func (s *Supervisor) Stop(ctx context.Context, id string) error {
	s.mu.Lock()
	w := s.workers[id]
	if w == nil || w.cmd == nil || w.cmd.Process == nil {
		s.mu.Unlock()
		slog.Info("llama-server worker already stopped", "instance_id", id)
		return nil
	}
	w.runtime.State = Stopping
	p := w.cmd.Process
	done := w.done
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
		_ = p.Kill()
		<-done
		slog.Warn("llama-server worker did not stop after SIGTERM; killed", "instance_id", id, "model_id", modelID, "pid", pid)
		return nil
	}
}

func (s *Supervisor) Status(id string) Runtime {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if w := s.workers[id]; w != nil {
		return w.runtime
	}
	return Runtime{InstanceID: id, State: Unloaded}
}

func (s *Supervisor) Endpoint(id string) (string, bool) {
	rt := s.Status(id)
	if rt.State != Ready {
		return "", false
	}
	return fmt.Sprintf("http://%s:%d", s.host, rt.Port), true
}

func (s *Supervisor) Logs(id string) []string {
	s.mu.RLock()
	logRing := s.logs[id]
	s.mu.RUnlock()
	if logRing == nil {
		return nil
	}
	return logRing.lines()
}

// SubscribeLogs returns an atomic snapshot plus a live, non-blocking stream of
// lines added after that snapshot. The subscription may be created before a
// worker starts, allowing clients to observe the complete startup sequence.
func (s *Supervisor) SubscribeLogs(id string) ([]string, <-chan string, func()) {
	s.mu.Lock()
	logRing := s.logRingLocked(id)
	s.mu.Unlock()
	return logRing.subscribe()
}

func (s *Supervisor) logRingLocked(id string) *ring {
	logRing := s.logs[id]
	if logRing == nil {
		logRing = newRing(2000)
		s.logs[id] = logRing
	}
	return logRing
}

func (s *Supervisor) Shutdown(ctx context.Context) {
	s.mu.RLock()
	ids := make([]string, 0, len(s.workers))
	for id := range s.workers {
		ids = append(ids, id)
	}
	s.mu.RUnlock()
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) { defer wg.Done(); _ = s.Stop(ctx, id) }(id)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func (s *Supervisor) wait(w *worker) {
	err := w.cmd.Wait()
	s.mu.Lock()
	if s.workers[w.runtime.InstanceID] != w {
		s.mu.Unlock()
		return
	}
	wasStopping := w.runtime.State == Stopping
	instanceID := w.runtime.InstanceID
	modelID := w.runtime.ModelID
	w.runtime.PID = 0
	if wasStopping {
		w.runtime.State = Unloaded
		w.runtime.LastError = ""
	} else {
		w.runtime.State = Failed
		if err != nil {
			w.runtime.LastError = err.Error()
		} else {
			w.runtime.LastError = "worker exited unexpectedly"
		}
	}
	state := w.runtime.State
	lastError := w.runtime.LastError
	close(w.done)
	s.mu.Unlock()
	if wasStopping {
		slog.Info("llama-server process exited", "instance_id", instanceID, "model_id", modelID, "state", state)
	} else {
		slog.Error("llama-server process exited unexpectedly", "instance_id", instanceID, "model_id", modelID, "state", state, "error", lastError)
	}
}

func (s *Supervisor) waitReady(ctx context.Context, port int) error {
	client := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	url := fmt.Sprintf("http://%s:%d/health", s.host, port)
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("worker readiness timeout: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (s *Supervisor) allocatePortLocked() (int, error) {
	for p := s.portStart; p < s.portStart+2000; p++ {
		used := false
		for _, w := range s.workers {
			if w.runtime.Port == p && w.runtime.State != Unloaded && w.runtime.State != Failed {
				used = true
				break
			}
		}
		if used {
			continue
		}
		ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", s.host, p))
		if err != nil {
			continue
		}
		_ = ln.Close()
		return p, nil
	}
	return 0, errors.New("no worker port available")
}

func (s *Supervisor) setState(id string, state State, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w := s.workers[id]; w != nil {
		w.runtime.State = state
		w.runtime.LastError = msg
	}
}

type ring struct {
	mu   sync.Mutex
	max  int
	data []string
	subs map[chan string]struct{}
}

func newRing(max int) *ring { return &ring{max: max, subs: map[chan string]struct{}{}} }
func (r *ring) reset() {
	r.mu.Lock()
	r.data = nil
	r.mu.Unlock()
}
func (r *ring) add(line string) {
	r.mu.Lock()
	if len(r.data) >= r.max {
		copy(r.data, r.data[1:])
		r.data[len(r.data)-1] = line
	} else {
		r.data = append(r.data, line)
	}
	for ch := range r.subs {
		select {
		case ch <- line:
		default:
		}
	}
	r.mu.Unlock()
}
func (r *ring) lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.data))
	copy(out, r.data)
	return out
}
func (r *ring) subscribe() ([]string, <-chan string, func()) {
	r.mu.Lock()
	snapshot := make([]string, len(r.data))
	copy(snapshot, r.data)
	ch := make(chan string, 128)
	r.subs[ch] = struct{}{}
	r.mu.Unlock()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			r.mu.Lock()
			delete(r.subs, ch)
			close(ch)
			r.mu.Unlock()
		})
	}
	return snapshot, ch, cancel
}
func copyLogs(dst *ring, instanceID, modelID, source string, reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		dst.add("[" + source + "] " + line)
		slog.Info("llama-server output", "instance_id", instanceID, "model_id", modelID, "stream", source, "line", line)
	}
}
