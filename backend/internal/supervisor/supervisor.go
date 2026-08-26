package supervisor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
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
}

func New(binary, host string, portStart int, startupTimeout time.Duration) *Supervisor {
	return &Supervisor{binary: binary, host: host, portStart: portStart, startupTimeout: startupTimeout, workers: map[string]*worker{}}
}

func (s *Supervisor) Start(ctx context.Context, instanceID, modelID, modelPath string, args []string) (Runtime, error) {
	s.mu.Lock()
	if w := s.workers[instanceID]; w != nil && w.runtime.State != Unloaded && w.runtime.State != Failed {
		rt := w.runtime
		s.mu.Unlock()
		return rt, nil
	}
	port, err := s.allocatePortLocked()
	if err != nil {
		s.mu.Unlock()
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
	w := &worker{runtime: Runtime{InstanceID: instanceID, ModelID: modelID, State: Starting, Port: port, StartedAt: time.Now().UTC()}, logs: newRing(2000), done: make(chan struct{})}
	s.workers[instanceID] = w
	if err := cmd.Start(); err != nil {
		w.runtime.State = Failed
		w.runtime.LastError = err.Error()
		s.mu.Unlock()
		return w.runtime, err
	}
	w.cmd = cmd
	w.runtime.PID = cmd.Process.Pid
	s.mu.Unlock()
	go copyLogs(w.logs, "stdout", stdout)
	go copyLogs(w.logs, "stderr", stderr)
	go s.wait(w)
	s.setState(instanceID, Loading, "")

	readyCtx, cancel := context.WithTimeout(ctx, s.startupTimeout)
	defer cancel()
	if err := s.waitReady(readyCtx, port); err != nil {
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
	return rt, nil
}

func (s *Supervisor) Stop(ctx context.Context, id string) error {
	s.mu.Lock()
	w := s.workers[id]
	if w == nil || w.cmd == nil || w.cmd.Process == nil {
		s.mu.Unlock()
		return nil
	}
	w.runtime.State = Stopping
	p := w.cmd.Process
	done := w.done
	s.mu.Unlock()
	_ = p.Signal(syscall.SIGTERM)
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		_ = p.Kill()
		return ctx.Err()
	case <-time.After(15 * time.Second):
		_ = p.Kill()
		<-done
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
	w := s.workers[id]
	s.mu.RUnlock()
	if w == nil {
		return nil
	}
	return w.logs.lines()
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
	defer s.mu.Unlock()
	if s.workers[w.runtime.InstanceID] != w {
		return
	}
	wasStopping := w.runtime.State == Stopping
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
	close(w.done)
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
}

func newRing(max int) *ring { return &ring{max: max} }
func (r *ring) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.data) >= r.max {
		copy(r.data, r.data[1:])
		r.data[len(r.data)-1] = s
	} else {
		r.data = append(r.data, s)
	}
}
func (r *ring) lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.data))
	copy(out, r.data)
	return out
}
func copyLogs(dst *ring, source string, reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		dst.add("[" + source + "] " + scanner.Text())
	}
}
