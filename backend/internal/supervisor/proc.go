package supervisor

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// Proc is a snapshot of a candidate OS process used for ownership checks.
type Proc struct {
	PID        int
	StartTicks uint64
	Environ    map[string]string
}

// ProcScanner discovers and signals processes. Tests inject a fake implementation.
type ProcScanner interface {
	List() ([]Proc, error)
	Inspect(pid int) (Proc, error)
	Signal(pid int, sig syscall.Signal) error
	Alive(pid int, startTicks uint64) bool
}

func readStartTicks(pid int) uint64 {
	if pid <= 0 {
		return 0
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	ticks, err := parseStartTicks(string(data))
	if err != nil {
		return 0
	}
	return ticks
}

func parseStartTicks(stat string) (uint64, error) {
	i := strings.LastIndex(stat, ")")
	if i < 0 {
		return 0, fmt.Errorf("invalid stat")
	}
	fields := strings.Fields(stat[i+1:])
	if len(fields) < 20 {
		return 0, fmt.Errorf("invalid stat")
	}
	return strconv.ParseUint(fields[19], 10, 64)
}

func parseEnviron(data []byte) map[string]string {
	out := map[string]string{}
	for _, entry := range strings.Split(string(data), "\x00") {
		if entry == "" {
			continue
		}
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		out[key] = value
	}
	return out
}

func parsePortEnv(environ map[string]string) int {
	if environ == nil {
		return 0
	}
	port, err := strconv.Atoi(strings.TrimSpace(environ[EnvWorkerPort]))
	if err != nil || port <= 0 {
		return 0
	}
	return port
}

// LinuxProcScanner reads the manager PID namespace via a /proc root.
type LinuxProcScanner struct {
	Root string
}

func (s LinuxProcScanner) root() string {
	if strings.TrimSpace(s.Root) == "" {
		return "/proc"
	}
	return s.Root
}

func (s LinuxProcScanner) List() ([]Proc, error) {
	entries, err := os.ReadDir(s.root())
	if err != nil {
		return nil, err
	}
	out := make([]Proc, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		proc, err := s.Inspect(pid)
		if err != nil {
			continue
		}
		out = append(out, proc)
	}
	return out, nil
}

func (s LinuxProcScanner) Inspect(pid int) (Proc, error) {
	if pid <= 0 {
		return Proc{}, fmt.Errorf("invalid pid")
	}
	statData, err := os.ReadFile(fmt.Sprintf("%s/%d/stat", s.root(), pid))
	if err != nil {
		return Proc{}, err
	}
	ticks, err := parseStartTicks(string(statData))
	if err != nil {
		return Proc{}, err
	}
	envData, err := os.ReadFile(fmt.Sprintf("%s/%d/environ", s.root(), pid))
	if err != nil {
		return Proc{}, err
	}
	return Proc{PID: pid, StartTicks: ticks, Environ: parseEnviron(envData)}, nil
}

func (s LinuxProcScanner) Signal(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid")
	}
	return syscall.Kill(pid, sig)
}

func (s LinuxProcScanner) Alive(pid int, startTicks uint64) bool {
	proc, err := s.Inspect(pid)
	if err != nil {
		return false
	}
	if startTicks == 0 {
		return true
	}
	return proc.StartTicks == startTicks
}
