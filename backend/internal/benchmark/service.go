package benchmark

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brantje/llamarack/backend/internal/buildinfo"
	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/llamaconfig"
	"github.com/brantje/llamarack/backend/internal/models"
	"github.com/brantje/llamarack/backend/internal/recommendations"
	"github.com/brantje/llamarack/backend/internal/resourceid"
	"github.com/brantje/llamarack/backend/internal/scheduler"
)

type benchmarkExecutor interface {
	Run(context.Context, []string) (RunOutput, error)
}

type Service struct {
	root         context.Context
	store        Store
	instances    instanceReader
	models       modelReader
	config       configReader
	hardware     hardware.Snapshotter
	reservations *scheduler.Ledger
	executor     benchmarkExecutor
	binaryPath   string

	discoverCapabilities func(context.Context, string) (Capabilities, error)
	readMetadata         func(string) (recommendations.Metadata, error)
	buildIdentity        func() buildinfo.Identity
	newID                func() (string, error)
	now                  func() time.Time

	capMu      sync.Mutex
	cachedCaps *Capabilities
	activeMu   sync.Mutex
	active     map[string]context.CancelFunc
	wg         sync.WaitGroup
}

func NewService(root context.Context, store Store, instanceService *instances.Service, modelService *models.Service, configStore *llamaconfig.Store, snapshotter hardware.Snapshotter, reservations *scheduler.Ledger, binaryPath string) *Service {
	if root == nil {
		root = context.Background()
	}
	if snapshotter == nil {
		snapshotter = hardware.New()
	}
	if reservations == nil {
		reservations = scheduler.NewLedger()
	}
	return &Service{
		root: root, store: store, instances: instanceService, models: modelService, config: configStore,
		hardware: snapshotter, reservations: reservations, executor: NewRunner(binaryPath), binaryPath: strings.TrimSpace(binaryPath),
		discoverCapabilities: DiscoverCapabilities, readMetadata: recommendations.ReadMetadata,
		buildIdentity: buildinfo.Current, newID: resourceid.NewUUID, now: time.Now,
		active: map[string]context.CancelFunc{},
	}
}

func (s *Service) Capabilities(ctx context.Context) (Capabilities, error) {
	if s == nil {
		return Capabilities{Reason: "benchmark service is not configured"}, nil
	}
	s.capMu.Lock()
	if s.cachedCaps != nil && s.cachedCaps.Available {
		cached := *s.cachedCaps
		s.capMu.Unlock()
		return cached, nil
	}
	s.capMu.Unlock()
	caps, err := s.discoverCapabilities(ctx, s.binaryPath)
	if err != nil {
		return Capabilities{}, err
	}
	if caps.Available {
		s.capMu.Lock()
		copy := caps
		s.cachedCaps = &copy
		s.capMu.Unlock()
	}
	return caps, nil
}

func (s *Service) Create(ctx context.Context, instanceID string, workloadInput *WorkloadProfile) (Run, error) {
	return s.CreateWithOverrides(ctx, instanceID, workloadInput, RuntimeOverrides{})
}

func (s *Service) CreateWithOverrides(ctx context.Context, instanceID string, workloadInput *WorkloadProfile, requestedOverrides RuntimeOverrides) (Run, error) {
	if s == nil || s.store == nil || s.instances == nil || s.models == nil || s.config == nil || s.hardware == nil || s.reservations == nil || s.executor == nil {
		return Run{}, errors.New("benchmark service is not fully configured")
	}
	workload, err := NormalizeWorkload(workloadInput)
	if err != nil {
		return Run{}, err
	}
	target, err := captureTarget(ctx, instanceID, s.instances, s.models, s.config)
	if err != nil {
		return Run{}, err
	}
	caps, err := s.Capabilities(ctx)
	if err != nil {
		return Run{}, err
	}
	if !caps.Available {
		return Run{}, fmt.Errorf("%w: %s", ErrUnavailable, caps.Reason)
	}
	benchmarkOverrides, effectiveConfig, err := ResolveRuntimeConfig(target.Config, requestedOverrides, caps)
	if err != nil {
		return Run{}, err
	}
	if err := ValidateWorkloadContext(workload, effectiveConfig); err != nil {
		return Run{}, err
	}
	cpu, err := captureCPU(effectiveConfig, caps)
	if err != nil {
		return Run{}, err
	}
	// Pin the executable's reported default in the effective snapshot as well as
	// argv. This is not a user override; it records the actual execution value.
	if strings.TrimSpace(effectiveConfig.Options["threads"]) == "" {
		effectiveConfig.Options["threads"] = strconv.Itoa(cpu.EffectiveThreads)
		effectiveConfig.Sources["threads"] = "benchmark-default"
	}
	snapshot, err := s.hardware.Snapshot(ctx)
	if err != nil {
		return Run{}, fmt.Errorf("benchmark hardware snapshot: %w", err)
	}
	runID, err := s.newID()
	if err != nil {
		return Run{}, err
	}
	demand := s.estimateDemandForConfig(target, effectiveConfig)
	owner := scheduler.ResourceOwner{Kind: scheduler.ResourceOwnerBenchmark, ID: runID}
	lease, err := s.reservations.Acquire(scheduler.AcquireRequest{
		Owner: owner, Snapshot: snapshot,
		Placement: scheduler.PlacementRequest{RequiredBytes: demand.VRAMBytes(), Mode: effectiveConfig.GPUMode, Devices: effectiveConfig.GPUDevices, TensorSplit: effectiveConfig.TensorSplit},
		HostRAM:   demand.HostRAMBytes,
	})
	if err != nil {
		return Run{}, err
	}
	if !lease.Placement.Fits {
		return Run{}, fmt.Errorf("%w: effective benchmark configuration does not fit currently available resources", ErrInsufficientResources)
	}
	defer func() {
		if err != nil {
			s.reservations.ReleaseOwner(owner)
		}
	}()

	// Capture auto placement in the immutable effective config. The Instance
	// snapshot remains untouched and records that placement was originally auto.
	executionConfig := cloneConfigSnapshot(effectiveConfig)
	if len(lease.Placement.Devices) > 0 {
		executionConfig.GPUDevices = append([]string(nil), lease.Placement.Devices...)
	}
	if strings.TrimSpace(executionConfig.TensorSplit) == "" && strings.TrimSpace(lease.Placement.TensorSplit) != "" {
		executionConfig.TensorSplit = lease.Placement.TensorSplit
	}
	mapped, err := MapInstanceConfig(executionConfig, lease.Placement, caps)
	if err != nil {
		return Run{}, err
	}
	argv, err := BuildArgv(s.binaryPath, target.ModelPath, mapped, workload, caps)
	if err != nil {
		return Run{}, err
	}
	if err = s.reservations.Commit(lease.ID); err != nil {
		return Run{}, fmt.Errorf("commit benchmark reservation: %w", err)
	}
	identity := s.buildIdentity()
	now := s.now().UTC()
	run := Run{
		ID:         runID,
		InstanceID: target.Instance.ID, InstanceSlugSnapshot: target.Instance.Slug, InstanceNameSnapshot: target.Instance.Name,
		InstanceConfig: target.Config, BenchmarkOverrides: benchmarkOverrides, EffectiveConfig: executionConfig,
		ModelID: target.Model.ID, ModelSlugSnapshot: target.Model.Slug, ModelNameSnapshot: target.Model.Name, Artifact: target.Artifact,
		Workload: workload, ResolvedArgv: append([]string(nil), argv...), MappingDifferences: append([]MappingDifference(nil), mapped.Differences...), Status: StatusQueued,
		CreatedAt: now,
		Build: BuildSnapshot{
			LlamaRackVersion: identity.Version, LlamaRackCommit: identity.Commit, RuntimeVariant: identity.Variant,
			LlamaCppRelease: identity.LlamaCpp.Release, LlamaCppBuild: identity.LlamaCpp.Build,
			LlamaBenchVersion: caps.Version, LlamaBenchFingerprint: caps.Fingerprint,
		},
		Hardware:               HardwareSnapshot{Observed: snapshot, CPU: cpu, SelectedDevices: append([]string(nil), lease.Placement.Devices...)},
		BenchmarkSchemaVersion: BenchmarkSchemaVersion, ParserSchemaVersion: ParserSchemaVersion,
	}
	if err = s.store.CreateRun(ctx, run); err != nil {
		return Run{}, err
	}
	jobCtx, cancel := context.WithCancel(s.root)
	s.activeMu.Lock()
	s.active[runID] = cancel
	s.activeMu.Unlock()
	s.wg.Add(1)
	go s.runJob(jobCtx, runID, argv, workload, owner)
	return run, nil
}

func (s *Service) estimateDemand(target capturedTarget) scheduler.ResourceDemand {
	return s.estimateDemandForConfig(target, target.Config)
}

func (s *Service) estimateDemandForConfig(target capturedTarget, config InstanceConfigSnapshot) scheduler.ResourceDemand {
	metadata, metadataErr := s.readMetadata(target.ModelPath)
	weights := target.Artifact.Size
	for _, dependency := range target.Artifact.Dependencies {
		for _, file := range dependency.Files {
			weights += file.Size
		}
	}
	if weights <= 0 {
		weights = target.Model.TotalBytes
	}
	return scheduler.EstimateDemand(scheduler.DemandInput{
		WeightsBytes: weights,
		Metadata: scheduler.KVMetadata{
			Architecture: metadata.Architecture, ContextLength: metadata.ContextLength, BlockCount: metadata.BlockCount,
			Embedding: metadata.Embedding, HeadCount: metadata.HeadCount, KVHeadCount: metadata.KVHeadCount,
			KeyLength: metadata.KeyLength, ValueLength: metadata.ValueLength, ExpertCount: metadata.ExpertCount,
		},
		MetadataErr: metadataErr,
		Options:     config.Options,
	})
}

func (s *Service) runJob(ctx context.Context, runID string, argv []string, workload WorkloadProfile, owner scheduler.ResourceOwner) {
	defer s.wg.Done()
	defer s.reservations.ReleaseOwner(owner)
	defer s.removeActive(runID)

	now := s.now().UTC()
	if err := ctx.Err(); err != nil {
		_, _ = s.store.TransitionRun(context.Background(), runID, StatusQueued, StatusCancelled, TransitionUpdate{CompletedAt: &now, Failure: "benchmark cancelled before execution"})
		return
	}
	if _, err := s.store.TransitionRun(context.Background(), runID, StatusQueued, StatusRunning, TransitionUpdate{StartedAt: &now}); err != nil {
		if errors.Is(err, ErrTransitionConflict) {
			return
		}
		slog.Error("benchmark transition to running failed", "benchmark_run_id", runID, "error", err)
		return
	}

	output, runErr := s.executor.Run(ctx, argv)
	diagnostic := boundedDiagnostic(output)
	completedAt := s.now().UTC()
	if ctx.Err() != nil {
		_, _ = s.store.TransitionRun(context.Background(), runID, StatusRunning, StatusCancelled, TransitionUpdate{CompletedAt: &completedAt, Failure: "benchmark cancelled", DiagnosticOutput: diagnostic})
		return
	}
	if runErr != nil {
		_, _ = s.store.TransitionRun(context.Background(), runID, StatusRunning, StatusFailed, TransitionUpdate{CompletedAt: &completedAt, Failure: sanitizeFailure(runErr), DiagnosticOutput: diagnostic})
		return
	}
	if output.MachineTruncated {
		_, _ = s.store.TransitionRun(context.Background(), runID, StatusRunning, StatusFailed, TransitionUpdate{CompletedAt: &completedAt, Failure: "llama-bench machine-readable output exceeded the manager limit", DiagnosticOutput: diagnostic})
		return
	}
	results, err := ParseMachineOutput(output.MachineOutput, workload)
	if err != nil {
		_, _ = s.store.TransitionRun(context.Background(), runID, StatusRunning, StatusFailed, TransitionUpdate{CompletedAt: &completedAt, Failure: sanitizeFailure(err), DiagnosticOutput: diagnostic})
		return
	}
	if _, err := s.store.CompleteRun(context.Background(), runID, Completion{CompletedAt: completedAt, DiagnosticOutput: diagnostic}, results); err != nil {
		failure := fmt.Sprintf("persist benchmark results: %v", err)
		_, _ = s.store.TransitionRun(context.Background(), runID, StatusRunning, StatusFailed, TransitionUpdate{CompletedAt: &completedAt, Failure: sanitizeFailure(errors.New(failure)), DiagnosticOutput: diagnostic})
		slog.Error("benchmark completion persistence failed", "benchmark_run_id", runID, "error", err)
		return
	}
	slog.Info("benchmark completed", "benchmark_run_id", runID)
}

func boundedDiagnostic(output RunOutput) string {
	diagnostic := strings.TrimSpace(output.DiagnosticOutput)
	if output.DiagnosticTruncated {
		if diagnostic != "" {
			diagnostic += "\n"
		}
		diagnostic += "[diagnostic output truncated]"
	}
	return diagnostic
}

func sanitizeFailure(err error) string {
	if err == nil {
		return ""
	}
	message := strings.Join(strings.Fields(err.Error()), " ")
	const maxFailureBytes = 2048
	if len(message) > maxFailureBytes {
		message = message[:maxFailureBytes] + "…"
	}
	return message
}

func (s *Service) removeActive(id string) {
	s.activeMu.Lock()
	delete(s.active, id)
	s.activeMu.Unlock()
}

func (s *Service) Get(ctx context.Context, id string) (Run, error) {
	return s.store.GetRun(ctx, id)
}

func (s *Service) List(ctx context.Context, filter Filter) (Page, error) {
	return s.store.ListRuns(ctx, filter)
}

func (s *Service) Cancel(ctx context.Context, id string) (Run, error) {
	run, err := s.store.GetRun(ctx, id)
	if err != nil || run.Status.Terminal() {
		return run, err
	}
	s.activeMu.Lock()
	cancel := s.active[id]
	s.activeMu.Unlock()
	if cancel != nil {
		cancel()
		return run, nil
	}
	completedAt := s.now().UTC()
	return s.store.TransitionRun(ctx, id, run.Status, StatusCancelled, TransitionUpdate{CompletedAt: &completedAt, Failure: "benchmark cancelled before execution"})
}

func (s *Service) Delete(ctx context.Context, id string) error {
	run, err := s.store.GetRun(ctx, id)
	if err != nil {
		return err
	}
	if !run.Status.Terminal() {
		return fmt.Errorf("%w: cancel a queued or running benchmark before deleting it", ErrTransitionConflict)
	}
	return s.store.DeleteRun(ctx, id)
}

func (s *Service) ReconcileInterrupted(ctx context.Context) error {
	for _, status := range []Status{StatusRunning, StatusQueued} {
		for {
			page, err := s.store.ListRuns(ctx, Filter{Status: status, Limit: 100})
			if err != nil {
				return err
			}
			if len(page.Items) == 0 {
				break
			}
			transitioned := 0
			for _, run := range page.Items {
				completedAt := s.now().UTC()
				to := StatusFailed
				reason := "manager interrupted benchmark execution"
				if status == StatusQueued {
					to = StatusCancelled
					reason = "manager restarted before benchmark execution began"
				}
				if _, err := s.store.TransitionRun(ctx, run.ID, status, to, TransitionUpdate{CompletedAt: &completedAt, Failure: reason}); err != nil && !errors.Is(err, ErrTransitionConflict) {
					return err
				} else if err == nil {
					transitioned++
				}
				s.reservations.ReleaseOwner(scheduler.ResourceOwner{Kind: scheduler.ResourceOwnerBenchmark, ID: run.ID})
			}
			if transitioned == 0 {
				break
			}
		}
	}
	return nil
}

func (s *Service) Shutdown(ctx context.Context) error {
	s.activeMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.active))
	for _, cancel := range s.active {
		cancels = append(cancels, cancel)
	}
	s.activeMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
