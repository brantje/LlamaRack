package supervisor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"github.com/brantje/llamarack/backend/internal/resourceid"
	"github.com/brantje/llamarack/backend/internal/systemlog"
)

func (s *Supervisor) Start(ctx context.Context, instanceID, modelID, modelPath string, args []string) (Runtime, error) {
	return s.StartWithAliasEnv(ctx, instanceID, instanceID, modelID, modelPath, args, nil, "")
}

func (s *Supervisor) StartWithEnv(ctx context.Context, instanceID, modelID, modelPath string, args, env []string, slotSavePath string) (Runtime, error) {
	publicAlias := resourceid.InstanceSlug(instanceID)
	if publicAlias == "" {
		publicAlias = instanceID
	}
	return s.StartWithAliasEnv(ctx, instanceID, publicAlias, modelID, modelPath, args, env, slotSavePath)
}

// StartWithAliasEnv keeps durable worker ownership keyed by instanceID while
// exposing publicAlias to llama-server/OpenAI clients through --alias.
func (s *Supervisor) StartWithAliasEnv(ctx context.Context, instanceID, publicAlias, modelID, modelPath string, args, env []string, slotSavePath string) (Runtime, error) {
	publicAlias = strings.TrimSpace(publicAlias)
	if publicAlias == "" { publicAlias = instanceID }
	s.mu.Lock()
	if w := s.workers[instanceID]; w != nil && w.runtime.State != Unloaded && w.runtime.State != Failed {
		rt := w.runtime; s.mu.Unlock()
		if ShuttingDown(rt.State) { return rt, errors.New("instance is shutting down") }
		slog.Info("llama-server worker already active", "instance_id", instanceID, "model_id", modelID, "state", rt.State, "pid", rt.PID)
		return rt, nil
	}
	port, err := s.allocatePortLocked()
	if err != nil { s.mu.Unlock(); slog.Error("unable to allocate llama-server port", "instance_id", instanceID, "model_id", modelID, "error", err); return Runtime{}, err }
	resolvedArgs := sanitizeWorkerOwnedArgs(args)
	workerArgs := []string{"--model", modelPath, "--alias", publicAlias, "--host", s.host, "--port", fmt.Sprint(port)}
	workerArgs = append(workerArgs, resolvedArgs...)
	workerArgs = append(workerArgs, "--cors-origins", "localhost")
	if strings.TrimSpace(slotSavePath) != "" { workerArgs = append(workerArgs, "--slot-save-path", slotSavePath) }
	generation := ""
	if s.installationID != "" {
		id, err := randomIdentity(); if err != nil { s.mu.Unlock(); return Runtime{}, fmt.Errorf("worker identity: %w", err) }; generation = id
	}
	identity := identityEnv(s.installationID, instanceID, generation, port)
	cmd := exec.Command(s.binary, workerArgs...)
	if len(env) > 0 || len(identity) > 0 { cmd.Env = workerEnviron(append(append([]string{}, env...), identity...)) }
	stdout, err := cmd.StdoutPipe(); if err != nil { s.mu.Unlock(); return Runtime{}, err }
	stderr, err := cmd.StderrPipe(); if err != nil { s.mu.Unlock(); return Runtime{}, err }
	logRing := s.logRingLocked(instanceID); logRing.reset(); logRing.add(formatStoredLogLine("manager", "launch command: "+formatLaunchCommand(s.binary, workerArgs)))
	launchLine := "start " + instanceID; if len(resolvedArgs) > 0 { launchLine += " " + strings.Join(resolvedArgs, " ") }; systemlog.Log(systemlog.Info, "manager", launchLine)
	w := &worker{runtime: Runtime{InstanceID: instanceID, ModelID: modelID, State: Starting, Port: port, StartedAt: time.Now().UTC()}, logs: logRing, done: make(chan struct{}), generation: generation}
	s.workers[instanceID] = w; s.emitRuntimeLocked(w.runtime)
	slog.Info("starting llama-server worker", "instance_id", instanceID, "instance_slug", publicAlias, "model_id", modelID, "binary", s.binary, "model_path", modelPath, "host", s.host, "port", port, "args", workerArgs)
	if err := cmd.Start(); err != nil { w.runtime.State = Failed; w.runtime.LastError = err.Error(); s.emitRuntimeLocked(w.runtime); s.mu.Unlock(); systemlog.Log(systemlog.Error, instanceID, "failed to start: "+err.Error()); slog.Error("failed to start llama-server worker", "instance_id", instanceID, "model_id", modelID, "error", err); return w.runtime, err }
	w.cmd = cmd; w.runtime.PID = cmd.Process.Pid; w.runtime.State = Loading; s.emitRuntimeLocked(w.runtime); pid := w.runtime.PID
	readyCtx, cancel := context.WithTimeout(ctx, s.startupTimeout); w.startCancel = cancel; done := w.done; s.mu.Unlock()
	go copyLogs(w.logs, instanceID, modelID, "stdout", stdout); go copyLogs(w.logs, instanceID, modelID, "stderr", stderr); go s.wait(w); defer cancel()
	if err := s.persistWorker(instanceID, generation, pid, port); err != nil { slog.Error("failed to persist llama-server worker identity", "instance_id", instanceID, "model_id", modelID, "pid", pid, "port", port, "error", err); return s.abortStartedWorker(instanceID, done, err) }
	slog.Info("llama-server process started", "instance_id", instanceID, "model_id", modelID, "pid", pid, "port", port)
	if err := s.waitReady(readyCtx, w, port, done); err != nil {
		slog.Error("llama-server worker readiness failed", "instance_id", instanceID, "model_id", modelID, "pid", pid, "port", port, "error", err)
		if errors.Is(err, ErrKilled) || errors.Is(err, context.Canceled) { _ = s.Kill(instanceID) } else { _ = s.Stop(context.Background(), instanceID) }
		s.setState(instanceID, Failed, err.Error()); return s.Status(instanceID), err
	}
	s.mu.Lock(); current := s.workers[instanceID]
	if current != w || current.killed || current.runtime.State != Loading || current.runtime.PID == 0 {
		rt := w.runtime; killed := current != nil && current.killed; s.mu.Unlock(); if killed { _ = s.Kill(instanceID); return rt, ErrKilled }; return rt, errors.New("worker exited during startup")
	}
	current.runtime.State = Ready; current.runtime.ReadyAt = time.Now().UTC(); s.emitRuntimeLocked(current.runtime); rt := current.runtime; s.mu.Unlock()
	slog.Info("llama-server worker ready", "instance_id", instanceID, "instance_slug", publicAlias, "model_id", modelID, "pid", rt.PID, "port", rt.Port)
	return rt, nil
}
