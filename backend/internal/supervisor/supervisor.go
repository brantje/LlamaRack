package supervisor

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type State string

const (
	Unloaded State = "UNLOADED"
	Starting State = "STARTING"
	Loading  State = "LOADING"
	Ready    State = "READY"
	Draining State = "DRAINING"
	Stopping State = "STOPPING"
	Failed   State = "FAILED"
)

func ShuttingDown(state State) bool { return state == Draining || state == Stopping }

type Runtime struct {
	InstanceID               string     `json:"instance_id"`
	ModelID                  string     `json:"model_id"`
	State                    State      `json:"state"`
	PID                      int        `json:"pid,omitempty"`
	Port                     int        `json:"port,omitempty"`
	StartedAt                time.Time  `json:"started_at,omitempty"`
	ReadyAt                  time.Time  `json:"ready_at,omitempty"`
	LastError                string     `json:"last_error,omitempty"`
	ConsecutiveStartFailures int        `json:"consecutive_start_failures,omitempty"`
	RetryAfter               *time.Time `json:"retry_after,omitempty"`
}

type worker struct {
	runtime     Runtime
	cmd         *exec.Cmd
	logs        *ring
	done        chan struct{}
	killed      bool
	startCancel context.CancelFunc
	generation  string
	startTicks  uint64
}

type Supervisor struct {
	mu             sync.RWMutex
	binary         string
	host           string
	portStart      int
	startupTimeout time.Duration
	workers        map[string]*worker
	logs           map[string]*ring
	installationID string
	store          RuntimeStore
	scanner        ProcScanner
}

func New(binary, host string, portStart int, startupTimeout time.Duration) *Supervisor {
	return &Supervisor{binary: binary, host: host, portStart: portStart, startupTimeout: startupTimeout, workers: map[string]*worker{}, logs: map[string]*ring{}}
}

func (s *Supervisor) SetRuntimeIdentity(installationID string, store RuntimeStore) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.installationID = strings.TrimSpace(installationID)
	s.store = store
}

func (s *Supervisor) SetProcScanner(scanner ProcScanner) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scanner = scanner
}
