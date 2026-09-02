package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/llamaconfig"
	"github.com/brantje/llamarack/backend/internal/llamacpp"
	"github.com/brantje/llamarack/backend/internal/models"
	"github.com/brantje/llamarack/backend/internal/recommendations"
	"github.com/brantje/llamarack/backend/internal/scheduler"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

const resourcePressureReason = "resource_pressure"

// maxEvictionPlanAttempts is also the maximum number of Instances one placement
// request may stop. Keep it small; raising it increases disruption.
const maxEvictionPlanAttempts = 8

var (
	errResourcePressureBlocked = errors.New("insufficient resources without resource-pressure eviction")
	errEvictionIneligible      = errors.New("instance is no longer eligible for resource-pressure eviction")
	errStartupKilled           = errors.New("startup killed")
)

type startupGenContextKey struct{}

func isStartupInterrupt(err error) bool {
	return errors.Is(err, errStartupKilled) || errors.Is(err, supervisor.ErrKilled) || errors.Is(err, context.Canceled)
}

type Activity struct {
	PendingRequests int       `json:"pending_requests"`
	ActiveRequests  int       `json:"active_requests"`
	LastUsed        time.Time `json:"last_used,omitempty"`
}

type Service struct {
	models                *models.Service
	instances             *instances.Service
	sup                   *supervisor.Supervisor
	hardware              hardware.Snapshotter
	profile               func() (llamacpp.Profile, error)
	observabilityRecorder ObservabilityRecorder
	reservations          *scheduler.Ledger
	mu                    sync.Mutex
	loads                 map[string]*loadCall
	manuallyStopped       map[string]bool
	resourceBlocked       map[string]string
	activities            map[string]Activity
	startFailures         map[string]StartFailureState
	idleLocks             map[string]*sync.Mutex
	operationGates        map[string]chan struct{}
	drainWaits            map[string]chan struct{}
	startupGen            map[string]uint64
	startupCancel         map[string]context.CancelFunc
	pendingLimits         func(context.Context) (perInstance, global int)
	holdLoad              func(id string)
	beforeEvictionLock    func(id string)
	afterEvictionClaim    func(id string)
	afterIdleDrainClaim   func(id string)
	now                   func() time.Time
}

type loadCall struct {
	done     chan struct{}
	complete sync.Once
	endpoint string
	err      error
	autoload bool
}

func New(modelsService *models.Service, sup *supervisor.Supervisor) *Service {
	return &Service{
		models: modelsService, instances: instances.New(modelsService.DB()), sup: sup, hardware: hardware.New(),
		reservations: scheduler.NewLedger(),
		loads:        map[string]*loadCall{}, manuallyStopped: map[string]bool{}, resourceBlocked: map[string]string{},
		activities: map[string]Activity{}, startFailures: map[string]StartFailureState{}, idleLocks: map[string]*sync.Mutex{},
		operationGates: map[string]chan struct{}{}, drainWaits: map[string]chan struct{}{},
		startupGen: map[string]uint64{}, startupCancel: map[string]context.CancelFunc{}, now: time.Now,
	}
}

func (s *Service) Instances() *instances.Service                            { return s.instances }
func (s *Service) SetProfileGetter(getter func() (llamacpp.Profile, error)) { s.profile = getter }
func (s *Service) HardwareSnapshot(ctx context.Context) (hardware.Snapshot, error) {
	return s.hardware.Snapshot(ctx)
}

// Acquire resolves exactly one addressable Instance. The OpenAI model field is
// the stored instance.id; sibling Instances are never selected as fallback.
func (s *Service) Acquire(ctx context.Context, instanceID string) (string, func(), error) {
	i, err := s.instances.Get(ctx, instanceID)
	if err != nil {
		return "", nil, err
	}
	if err := s.reservePending(ctx, i); err != nil {
		return "", nil, err
	}
	if err := s.admitRequest(ctx, i.ID); err != nil {
		s.releasePending(i.ID)
		return "", nil, err
	}
	endpoint, err := s.ensureReadyInstance(ctx, i, true)
	if err != nil {
		s.releasePending(i.ID)
		return "", nil, err
	}
	s.activateRequest(i.ID)
	var once sync.Once
	return endpoint, func() { once.Do(func() { s.finishActive(i.ID) }) }, nil
}

func (s *Service) EnsureReady(ctx context.Context, instanceID string) (string, error) {
	i, err := s.instances.Get(ctx, instanceID)
	if err != nil {
		return "", err
	}
	return s.ensureReadyInstance(ctx, i, false)
}

func (s *Service) ensureReadyInstance(ctx context.Context, i instances.Instance, inferenceRequest ...bool) (string, error) {
	if !i.Enabled {
		return "", errors.New("instance disabled")
	}
	if err := s.waitDrain(ctx, i.ID); err != nil {
		return "", err
	}
	if endpoint, ok := s.sup.Endpoint(i.ID); ok {
		return endpoint, nil
	}
	if !i.Autoload {
		return "", errors.New("instance unloaded and autoload disabled")
	}
	if s.inStartBackoff(i.ID) {
		return "", s.startBackoffError(i.ID)
	}
	if s.isManuallyStopped(i.ID) {
		s.clearManualStop(i.ID)
		slog.Info("manual stop overridden by inference request", "instance_id", i.ID)
	}
	if s.resourceBlockReason(i.ID) != "" {
		s.clearResourceBlock(i.ID)
		slog.Info("resource-pressure block overridden by inference request", "instance_id", i.ID)
	}
	autoload := len(inferenceRequest) > 0 && inferenceRequest[0]
	return s.startSingleFlight(ctx, i, autoload)
}

func (s *Service) StartInstance(ctx context.Context, id string) (string, error) {
	return s.startInstance(ctx, id, true)
}

func (s *Service) startInstance(ctx context.Context, id string, explicit bool) (string, error) {
	i, err := s.instances.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if !i.Enabled {
		return "", errors.New("instance disabled")
	}
	if explicit {
		s.clearManualStop(id)
		s.clearResourceBlock(id)
		s.overrideStartBackoff(id)
	} else if s.isManuallyStopped(id) {
		return "", errors.New("instance manually stopped until manager restart")
	} else if s.inStartBackoff(id) {
		return "", s.startBackoffError(id)
	}
	if err := s.waitDrain(ctx, id); err != nil {
		return "", err
	}
	if endpoint, ok := s.sup.Endpoint(id); ok {
		return endpoint, nil
	}
	return s.startSingleFlight(ctx, i, false)
}

func (s *Service) StopInstance(ctx context.Context, id string) error {
	i, err := s.instances.Get(ctx, id)
	if err != nil {
		return err
	}
	s.claimDrain(id)
	if i.AlwaysOn {
		s.clearResourceBlock(id)
		s.markManualStop(id)
	}
	if err := s.stopWorker(ctx, id); err != nil {
		if i.AlwaysOn {
			s.clearManualStop(id)
		}
		return err
	}
	s.AddManagerLog(id, "worker stopped")
	return nil
}

func (s *Service) RestartInstance(ctx context.Context, id string) (string, error) {
	i, err := s.instances.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if !i.Enabled {
		return "", errors.New("instance disabled")
	}
	s.claimDrain(id)
	release, err := s.acquireOperation(ctx, id)
	if err != nil {
		s.abortDrainClaim(id)
		return "", err
	}
	defer release()

	s.cancelStartup(id)
	s.clearManualStop(id)
	s.clearResourceBlock(id)
	s.overrideStartBackoff(id)
	if err := s.sup.Stop(ctx, id); err != nil {
		s.abortDrainClaim(id)
		return "", err
	}
	if s.reservations != nil {
		s.reservations.ReleaseInstance(id)
	}
	s.finishDrainWait(id)
	startCtx, gen := s.beginStartup(id)
	release()
	endpoint, err := s.startOne(startCtx, i)
	if !s.startupLive(id, gen) {
		if _, ok := s.sup.Endpoint(id); ok {
			_ = s.sup.Kill(id)
		}
		return "", errStartupKilled
	}
	s.finishStartup(id, gen)
	if err == nil {
		s.AddManagerLog(id, "restart completed")
	}
	return endpoint, err
}

func (s *Service) KillInstance(ctx context.Context, id string) error {
	if _, err := s.instances.Get(ctx, id); err != nil {
		return err
	}
	s.claimDrain(id)
	release, err := s.acquireOperation(ctx, id)
	if err != nil {
		s.abortDrainClaim(id)
		return err
	}

	s.cancelStartup(id)
	s.clearResourceBlock(id)
	s.markManualStop(id)
	if err := s.sup.Kill(id); err != nil {
		release()
		s.abortDrainClaim(id)
		return err
	}
	release()
	_ = s.waitLoad(ctx, id)
	_ = s.sup.WaitInactive(ctx, id)
	s.releaseReservation(id)
	s.finishDrainWait(id)
	s.AddManagerLog(id, "worker killed")
	return nil
}

func (s *Service) RuntimeInstance(ctx context.Context, id string) (supervisor.Runtime, error) {
	i, err := s.instances.Get(ctx, id)
	if err != nil {
		return supervisor.Runtime{}, err
	}
	rt := s.sup.Status(id)
	if rt.ModelID == "" {
		rt.ModelID = i.ModelID
	}
	return s.attachStartFailure(rt), nil
}

// Compatibility wrappers for pre-Phase-5.5 internal callers. Management routes
// no longer expose Model lifecycle operations.
func (s *Service) StartModel(ctx context.Context, modelID string) (string, error) {
	return s.startModel(ctx, modelID, true)
}
func (s *Service) startModel(ctx context.Context, modelID string, explicit bool) (string, error) {
	items, err := s.instances.ListByModel(ctx, modelID)
	if err != nil {
		return "", err
	}
	for _, i := range items {
		if i.Enabled {
			return s.startInstance(ctx, i.ID, explicit)
		}
	}
	return "", errors.New("no enabled instance")
}
func (s *Service) StopModel(ctx context.Context, modelID string) error {
	items, err := s.instances.ListByModel(ctx, modelID)
	if err != nil {
		return err
	}
	for _, i := range items {
		if err := s.StopInstance(ctx, i.ID); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) Runtime(ctx context.Context, modelID string) ([]supervisor.Runtime, error) {
	items, err := s.instances.ListByModel(ctx, modelID)
	if err != nil {
		return nil, err
	}
	out := make([]supervisor.Runtime, 0, len(items))
	for _, i := range items {
		rt := s.sup.Status(i.ID)
		if rt.ModelID == "" {
			rt.ModelID = i.ModelID
		}
		out = append(out, s.attachStartFailure(rt))
	}
	return out, nil
}

func (s *Service) Activity(id string) Activity {
	s.mu.Lock()
	activity, ok := s.activities[id]
	s.mu.Unlock()
	if ok {
		return activity
	}
	// Compatibility aggregation when an older caller supplies a database Model ID.
	items, err := s.instances.ListByModel(context.Background(), id)
	if err != nil {
		return Activity{}
	}
	var out Activity
	for _, i := range items {
		s.mu.Lock()
		a := s.activities[i.ID]
		s.mu.Unlock()
		out.PendingRequests += a.PendingRequests
		out.ActiveRequests += a.ActiveRequests
		if a.LastUsed.After(out.LastUsed) {
			out.LastUsed = a.LastUsed
		}
	}
	return out
}

func (s *Service) EvictionPlan(ctx context.Context, requiredBytes int64) (scheduler.Plan, error) {
	snapshot, _ := s.hardwareSnapshot(ctx)
	return s.planEvictions(ctx, snapshot, scheduler.PlacementRequest{RequiredBytes: requiredBytes}, "", nil)
}

func (s *Service) hardwareSnapshot(ctx context.Context) (hardware.Snapshot, error) {
	if s == nil || s.hardware == nil {
		return hardware.Snapshot{}, nil
	}
	return s.hardware.Snapshot(ctx)
}

func (s *Service) planEvictions(ctx context.Context, snapshot hardware.Snapshot, request scheduler.PlacementRequest, excludeInstance string, skip map[string]bool) (scheduler.Plan, error) {
	candidates, err := s.evictionCandidates(ctx, snapshot, excludeInstance, skip)
	if err != nil {
		return scheduler.Plan{}, err
	}
	return scheduler.PlanEvictions(candidates, snapshot, request), nil
}

func (s *Service) evictionCandidates(ctx context.Context, snapshot hardware.Snapshot, excludeInstance string, skip map[string]bool) ([]scheduler.Candidate, error) {
	items, err := s.instances.List(ctx)
	if err != nil {
		return nil, err
	}
	candidates := make([]scheduler.Candidate, 0, len(items))
	for _, i := range items {
		if i.ID == excludeInstance || skip[i.ID] {
			continue
		}
		m, err := s.models.GetByID(ctx, i.ModelID)
		if err != nil {
			return nil, err
		}
		activity := s.Activity(i.ID)
		estimated := m.TotalBytes
		hostRAM := int64(0)
		if path, err := s.models.ModelAbsolutePath(m); err == nil {
			options, optErr := s.resolveLaunchOptions(ctx, m.ID, i.ID)
			if optErr == nil {
				demand := s.estimateDemand(m, path, options)
				estimated = demand.VRAMBytes()
				hostRAM = demand.HostRAMBytes
			}
		}
		runtime := s.sup.Status(i.ID)
		var leaseGPUs []scheduler.GPUReservation
		if s.reservations != nil {
			if lease, ok := s.reservations.GetByInstance(i.ID); ok {
				leaseGPUs = lease.GPUs
			}
		}
		resources := scheduler.AttributeResources(scheduler.ResourceAttribution{
			EstimatedBytes: estimated,
			HostRAMBytes:   hostRAM,
			Devices:        i.GPUDevices,
			TensorSplit:    i.TensorSplit,
			LeaseGPUs:      leaseGPUs,
			PID:            runtime.PID,
			Processes:      snapshot.Processes,
			SnapshotGPUs:   snapshot.GPUs,
		})
		if attributed := candidateResourceBytes(resources); attributed > 0 {
			estimated = attributed
		}
		candidates = append(candidates, scheduler.Candidate{
			ModelID: i.ModelID, InstanceID: i.ID, Priority: i.Priority, AlwaysOn: i.AlwaysOn,
			EvictionEnabled: i.EvictionEnabled, ActiveRequests: activity.demand(), LastUsed: activity.LastUsed,
			EstimatedBytes: estimated, Resources: resources, Ready: runtime.State == supervisor.Ready,
		})
	}
	return candidates, nil
}

func candidateResourceBytes(resources scheduler.CandidateResources) int64 {
	total := int64(0)
	for _, gpu := range resources.GPU {
		if gpu.Bytes > 0 {
			total += gpu.Bytes
		}
	}
	return total
}

func (s *Service) Logs(id string) []string { return s.sup.Logs(id) }
func (s *Service) SubscribeLogs(id string) ([]string, <-chan string, func()) {
	return s.sup.SubscribeLogs(id)
}

func (s *Service) ReconcileAlwaysOn(ctx context.Context) {
	items, err := s.instances.List(ctx)
	if err != nil {
		return
	}
	satisfied := 0
	for _, i := range items {
		if !i.Enabled || !i.AlwaysOn || s.isManuallyStopped(i.ID) || s.inStartBackoff(i.ID) {
			continue
		}
		if _, ok := s.sup.Endpoint(i.ID); ok {
			satisfied++
			s.clearResourceBlock(i.ID)
			continue
		}
		if s.resourceBlockReason(i.ID) != "" {
			go s.reconcileResourceBlocked(i.ID)
			continue
		}
		go func(id string) { _, _ = s.startInstance(context.Background(), id, false) }(i.ID)
	}
	s.logAlwaysOnReconcile(satisfied)
}

func (s *Service) reconcileResourceBlocked(id string) {
	release, err := s.acquireOperation(context.Background(), id)
	if err != nil {
		return
	}
	defer release()
	if s.resourceBlockReason(id) == "" || s.isManuallyStopped(id) {
		return
	}
	i, err := s.instances.Get(context.Background(), id)
	if err != nil || !i.Enabled || !i.AlwaysOn {
		s.clearResourceBlock(id)
		return
	}
	if _, ok := s.sup.Endpoint(id); ok {
		s.clearResourceBlock(id)
		return
	}
	startCtx, gen := s.beginStartup(id)
	release()
	_, err = s.startOneWithEviction(startCtx, i, false)
	if !s.startupLive(id, gen) {
		return
	}
	s.finishStartup(id, gen)
	if err == nil {
		s.clearResourceBlock(id)
		return
	}
	if errors.Is(err, errResourcePressureBlocked) {
		return
	}
	// Configuration/process failures are not resource-pressure blocks. Clear the
	// reason so normal Always-On reconciliation can apply its ordinary retry path.
	s.clearResourceBlock(id)
}

func (s *Service) ReconcileIdle(ctx context.Context, globalIdleTimeout time.Duration) {
	items, err := s.instances.List(ctx)
	if err != nil {
		return
	}
	now := s.now().UTC()
	for _, i := range items {
		if !i.Enabled || i.AlwaysOn {
			continue
		}
		idleTimeout := globalIdleTimeout
		if i.IdleUnloadSeconds > 0 {
			idleTimeout = time.Duration(i.IdleUnloadSeconds) * time.Second
		}
		if idleTimeout <= 0 {
			continue
		}
		lock := s.idleLock(i.ID)
		lock.Lock()
		activity := s.Activity(i.ID)
		if activity.demand() > 0 || activity.LastUsed.IsZero() || now.Sub(activity.LastUsed) < idleTimeout {
			lock.Unlock()
			continue
		}
		if _, ok := s.sup.Endpoint(i.ID); !ok {
			lock.Unlock()
			continue
		}
		s.claimDrainLocked(i.ID)
		lock.Unlock()
		if s.afterIdleDrainClaim != nil {
			s.afterIdleDrainClaim(i.ID)
		}
		err := s.StopInstance(ctx, i.ID)
		if err != nil {
			slog.Warn("idle unload failed", "instance_id", i.ID, "error", err)
			continue
		}
		s.recordObservabilityEvent(ctx, ObservabilityIdleUnload, i.ID, 0)
		s.AddManagerLog(i.ID, fmt.Sprintf("idle-unloaded after %s without active requests", idleTimeout))
	}
}

func (s *Service) RunReconciler(ctx context.Context, interval time.Duration) {
	s.ReconcileAlwaysOn(ctx)
	if interval <= 0 {
		<-ctx.Done()
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.ReconcileAlwaysOn(ctx)
		}
	}
}

func (s *Service) RunIdleReconciler(ctx context.Context, globalIdleTimeout time.Duration) {
	interval := 15 * time.Second
	if globalIdleTimeout > 0 {
		interval = globalIdleTimeout / 2
		if interval > 15*time.Second {
			interval = 15 * time.Second
		}
		if interval < time.Second {
			interval = time.Second
		}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.ReconcileIdle(ctx, globalIdleTimeout)
		}
	}
}

func (s *Service) admitRequest(ctx context.Context, id string) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		lock := s.idleLock(id)
		lock.Lock()
		if ch := s.drainWaitLocked(id); ch != nil {
			lock.Unlock()
			if err := waitChan(ctx, ch); err != nil {
				return err
			}
			continue
		}
		if supervisor.ShuttingDown(s.sup.Status(id).State) {
			lock.Unlock()
			if err := s.sup.WaitInactive(ctx, id); err != nil {
				return err
			}
			continue
		}
		s.admitReady(id)
		lock.Unlock()
		return nil
	}
}

func (s *Service) waitDrain(ctx context.Context, id string) error {
	for {
		if ch := s.drainWaitLocked(id); ch != nil {
			if err := waitChan(ctx, ch); err != nil {
				return err
			}
			continue
		}
		if supervisor.ShuttingDown(s.sup.Status(id).State) {
			return s.sup.WaitInactive(ctx, id)
		}
		return nil
	}
}

func waitChan(ctx context.Context, ch <-chan struct{}) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ch:
		return nil
	}
}

func (s *Service) claimDrain(id string) {
	lock := s.idleLock(id)
	lock.Lock()
	s.claimDrainLocked(id)
	lock.Unlock()
}

func (s *Service) claimDrainLocked(id string) {
	if s.drainWaitLocked(id) != nil {
		return
	}
	_ = s.sup.BeginDrain(id)
	if supervisor.ShuttingDown(s.sup.Status(id).State) {
		s.registerDrainWaitLocked(id)
	}
}

func (s *Service) claimEviction(ctx context.Context, id string) error {
	if s.beforeEvictionLock != nil {
		s.beforeEvictionLock(id)
	}
	i, err := s.instances.Get(ctx, id)
	if err != nil {
		return err
	}
	lock := s.idleLock(id)
	lock.Lock()
	defer lock.Unlock()
	activity := s.Activity(id)
	if !i.EvictionEnabled || activity.demand() > 0 || s.sup.Status(id).State != supervisor.Ready {
		return errEvictionIneligible
	}
	if !s.sup.BeginDrain(id) {
		return errEvictionIneligible
	}
	s.registerDrainWaitLocked(id)
	return nil
}

func (s *Service) stopWorker(ctx context.Context, id string) error {
	release, err := s.acquireOperation(ctx, id)
	if err != nil {
		s.abortDrainClaim(id)
		return err
	}
	defer release()
	s.cancelStartup(id)
	if err := s.sup.Stop(ctx, id); err != nil {
		s.abortDrainClaim(id)
		return err
	}
	s.releaseReservation(id)
	s.finishDrainWait(id)
	return nil
}

func (s *Service) abortDrainClaim(id string) {
	s.sup.AbortDrain(id)
	s.finishDrainWait(id)
}

func (s *Service) drainWaitLocked(id string) <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.drainWaits[id]
}

func (s *Service) registerDrainWaitLocked(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.drainWaits[id] == nil {
		s.drainWaits[id] = make(chan struct{})
	}
}

func (s *Service) finishDrainWait(id string) {
	s.mu.Lock()
	ch := s.drainWaits[id]
	delete(s.drainWaits, id)
	s.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

func (s *Service) admitReady(id string) {
	s.mu.Lock()
	a := s.activities[id]
	a.LastUsed = s.now().UTC()
	s.activities[id] = a
	s.mu.Unlock()
}
func (s *Service) touch(id string) {
	s.mu.Lock()
	a := s.activities[id]
	a.LastUsed = s.now().UTC()
	s.activities[id] = a
	s.mu.Unlock()
}
func (s *Service) idleLock(id string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock := s.idleLocks[id]
	if lock == nil {
		lock = &sync.Mutex{}
		s.idleLocks[id] = lock
	}
	return lock
}
func (s *Service) operationGate(id string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	gate := s.operationGates[id]
	if gate == nil {
		gate = make(chan struct{}, 1)
		s.operationGates[id] = gate
	}
	return gate
}
func (s *Service) acquireOperation(ctx context.Context, id string) (func(), error) {
	gate := s.operationGate(id)
	select {
	case gate <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-gate }) }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Service) beginStartup(id string) (context.Context, uint64) {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	prev := s.startupCancel[id]
	s.startupGen[id]++
	gen := s.startupGen[id]
	s.startupCancel[id] = cancel
	s.mu.Unlock()
	if prev != nil {
		prev()
	}
	return context.WithValue(ctx, startupGenContextKey{}, gen), gen
}

func (s *Service) cancelStartup(id string) {
	s.mu.Lock()
	s.startupGen[id]++
	cancel := s.startupCancel[id]
	delete(s.startupCancel, id)
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Service) finishStartup(id string, gen uint64) {
	s.mu.Lock()
	if s.startupGen[id] != gen {
		s.mu.Unlock()
		return
	}
	cancel := s.startupCancel[id]
	delete(s.startupCancel, id)
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Service) startupLive(id string, gen uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startupGen[id] == gen
}

func (s *Service) generationLive(ctx context.Context, id string) bool {
	gen, ok := ctx.Value(startupGenContextKey{}).(uint64)
	if !ok || gen == 0 {
		return true
	}
	return s.startupLive(id, gen)
}

func (s *Service) waitLoad(ctx context.Context, id string) error {
	s.mu.Lock()
	c := s.loads[id]
	s.mu.Unlock()
	if c == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return nil
	}
}
func (s *Service) markManualStop(id string) {
	s.mu.Lock()
	s.manuallyStopped[id] = true
	s.mu.Unlock()
}
func (s *Service) clearManualStop(id string) {
	s.mu.Lock()
	delete(s.manuallyStopped, id)
	s.mu.Unlock()
}
func (s *Service) isManuallyStopped(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.manuallyStopped[id]
}
func (s *Service) markResourceBlock(id string) {
	s.mu.Lock()
	s.resourceBlocked[id] = resourcePressureReason
	s.mu.Unlock()
}
func (s *Service) clearResourceBlock(id string) {
	s.mu.Lock()
	delete(s.resourceBlocked, id)
	s.mu.Unlock()
}
func (s *Service) resourceBlockReason(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resourceBlocked[id]
}
func (s *Service) startSingleFlight(ctx context.Context, i instances.Instance, autoload ...bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	requestedAutoload := len(autoload) > 0 && autoload[0]
	created := false
	s.mu.Lock()
	c := s.loads[i.ID]
	if c == nil {
		c = &loadCall{done: make(chan struct{}), autoload: requestedAutoload}
		s.loads[i.ID] = c
		created = true
	}
	s.mu.Unlock()
	if created {
		if requestedAutoload {
			s.recordObservabilityEvent(ctx, ObservabilityAutoload, i.ID, 0)
		}
		go s.runLoad(i.ID, c)
	}

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-c.done:
		return c.endpoint, c.err
	}
}

func (s *Service) runLoad(id string, c *loadCall) {
	if s.holdLoad != nil {
		s.holdLoad(id)
	}
	if err := s.waitDrain(context.Background(), id); err != nil {
		if c.autoload {
			s.AddManagerLog(id, "autoload triggered by inference request")
		}
		s.completeLoad(id, c, "", err)
		return
	}
	release, err := s.acquireOperation(context.Background(), id)
	if err != nil {
		if c.autoload {
			s.AddManagerLog(id, "autoload triggered by inference request")
		}
		s.completeLoad(id, c, "", err)
		return
	}
	defer release()

	var endpoint string
	if s.inStartBackoff(id) {
		err = s.startBackoffError(id)
	} else if s.isManuallyStopped(id) {
		err = errors.New("instance manually stopped until manager restart")
	} else {
		var i instances.Instance
		i, err = s.instances.Get(context.Background(), id)
		if err == nil && !i.Enabled {
			err = errors.New("instance disabled")
		}
		if err == nil {
			if readyEndpoint, ok := s.sup.Endpoint(id); ok {
				endpoint = readyEndpoint
			} else {
				startCtx, gen := s.beginStartup(id)
				release()
				endpoint, err = s.startOne(startCtx, i)
				if !s.startupLive(id, gen) {
					if _, ok := s.sup.Endpoint(id); ok {
						_ = s.sup.Kill(id)
					}
					endpoint = ""
					err = errStartupKilled
				} else {
					s.finishStartup(id, gen)
				}
			}
		}
	}
	if c.autoload {
		s.AddManagerLog(id, "autoload triggered by inference request")
	}
	s.completeLoad(id, c, endpoint, err)
}

func (s *Service) completeLoad(id string, c *loadCall, endpoint string, err error) {
	c.complete.Do(func() {
		s.mu.Lock()
		c.endpoint = endpoint
		c.err = err
		if s.loads[id] == c {
			delete(s.loads, id)
		}
		close(c.done)
		s.mu.Unlock()
	})
}

func (s *Service) startOne(ctx context.Context, i instances.Instance) (string, error) {
	return s.startOneWithEviction(ctx, i, true)
}

func (s *Service) startOneWithEviction(ctx context.Context, i instances.Instance, allowEviction bool) (endpoint string, err error) {
	started := time.Now()
	committed := false
	defer func() {
		if !committed {
			s.releaseReservation(i.ID)
		}
		duration := time.Since(started)
		if err == nil {
			s.resetStartFailures(i.ID)
			s.recordObservabilityEvent(ctx, ObservabilityLoad, i.ID, duration)
			s.AddManagerLog(i.ID, fmt.Sprintf("worker ready after %s", duration.Round(time.Millisecond)))
			return
		}
		if errors.Is(err, errResourcePressureBlocked) || isStartupInterrupt(err) {
			if errors.Is(err, errStartupKilled) || errors.Is(err, supervisor.ErrKilled) {
				s.AddManagerLog(i.ID, "startup killed")
			}
			return
		}
		if !errors.Is(err, context.Canceled) {
			s.recordStartFailure(i.ID, err.Error())
		}
		s.recordObservabilityEvent(ctx, ObservabilityFailedStart, i.ID, duration)
		s.AddManagerLog(i.ID, "worker failed to start: "+err.Error())
	}()

	if err := ctx.Err(); err != nil {
		return "", err
	}

	m, err := s.models.GetByID(ctx, i.ModelID)
	if err != nil {
		return "", err
	}
	path, err := s.models.ModelAbsolutePath(m)
	if err != nil {
		return "", err
	}

	store := llamaconfig.New(s.models.DB())
	effective, err := store.Effective(ctx, m.ID, i.ID)
	if err != nil {
		return "", err
	}
	launchOptions := effective.Values
	if s.profile != nil {
		if profile, profileErr := s.profile(); profileErr == nil {
			launchOptions, _, err = store.LaunchOptions(ctx, profile, m.ID, i.ID)
			if err != nil {
				return "", err
			}
		}
	}
	args := optionArgs(launchOptions)
	_, hasTensorSplitOverride := launchOptions["tensor-split"]

	demand := s.estimateDemand(m, path, effective.Values)
	placement, placementErr := s.preparePlacementWithDemand(ctx, i, demand, allowEviction)
	if placementErr != nil {
		return "", placementErr
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var workerEnv []string
	if len(placement.Devices) > 0 {
		args, workerEnv = appendPlacementLaunchArgs(args, placement.Devices, placement.TensorSplit, hasTensorSplitOverride)
	} else if i.GPUMode == "manual" && len(i.GPUDevices) > 0 {
		// Preserve explicitly configured non-NVIDIA/ROCm backends when this Phase 7
		// detector cannot observe them.
		args, workerEnv = appendPlacementLaunchArgs(args, i.GPUDevices, i.TensorSplit, hasTensorSplitOverride)
	}

	_, err = s.sup.StartWithEnv(ctx, i.ID, m.ID, path, args, workerEnv)
	if err != nil {
		if isStartupInterrupt(err) {
			return "", fmt.Errorf("%w: %w", errStartupKilled, err)
		}
		return "", err
	}
	if err := ctx.Err(); err != nil || !s.generationLive(ctx, i.ID) {
		_ = s.sup.Kill(i.ID)
		if err == nil {
			err = errStartupKilled
		}
		return "", fmt.Errorf("%w: %w", errStartupKilled, err)
	}
	endpoint, ok := s.sup.Endpoint(i.ID)
	if !ok {
		return "", errors.New("worker did not reach ready state")
	}
	if err := s.commitReservation(i.ID); err != nil {
		return "", err
	}
	committed = true
	s.touch(i.ID)
	return endpoint, nil
}

func (s *Service) preparePlacement(ctx context.Context, i instances.Instance, requiredBytes int64) (scheduler.Placement, error) {
	return s.preparePlacementWithEviction(ctx, i, requiredBytes, true)
}

func (s *Service) preparePlacementWithEviction(ctx context.Context, i instances.Instance, requiredBytes int64, allowEviction bool) (scheduler.Placement, error) {
	return s.preparePlacementWithDemand(ctx, i, scheduler.ResourceDemand{GPU: []scheduler.GPUResourceDemand{{Bytes: requiredBytes}}}, allowEviction)
}

func (s *Service) preparePlacementWithDemand(ctx context.Context, i instances.Instance, demand scheduler.ResourceDemand, allowEviction bool) (scheduler.Placement, error) {
	requiredBytes := demand.VRAMBytes()
	snapshot, err := s.hardware.Snapshot(ctx)
	if err != nil {
		slog.Warn("hardware snapshot unavailable; preserving compatibility placement", "instance_id", i.ID, "error", err)
		return scheduler.Placement{}, nil
	}
	if len(snapshot.GPUs) == 0 {
		return scheduler.Placement{}, nil
	}
	request := scheduler.PlacementRequest{RequiredBytes: requiredBytes, Mode: i.GPUMode, Devices: i.GPUDevices, TensorSplit: i.TensorSplit}
	lease, err := s.reservations.Acquire(scheduler.AcquireRequest{InstanceID: i.ID, Snapshot: snapshot, Placement: request, HostRAM: demand.HostRAMBytes})
	if err != nil {
		return scheduler.Placement{}, err
	}
	if lease.Placement.Fits {
		s.logReservation("reserved", i.ID, lease)
		return lease.Placement, nil
	}
	if !allowEviction {
		return scheduler.Placement{}, fmt.Errorf("%w: need %d bytes, have %d", errResourcePressureBlocked, requiredBytes, lease.Placement.AvailableBytes)
	}

	shortfall := requiredBytes - lease.Placement.AvailableBytes
	if shortfall < 1 {
		shortfall = requiredBytes
	}
	insufficient := func(plan scheduler.Plan) error {
		return fmt.Errorf("insufficient usable VRAM: need %d more bytes and eligible eviction victims can free only %d", shortfall, plan.FreedBytes)
	}

	var stopped []scheduler.Candidate
	skip := map[string]bool{}
	for attempt := 0; attempt < maxEvictionPlanAttempts; attempt++ {
		planning := scheduler.ApplyCandidateCredits(snapshot, stopped)
		plan, err := s.planEvictions(ctx, planning, request, i.ID, skip)
		if err != nil {
			return scheduler.Placement{}, err
		}
		if !plan.Fits {
			return scheduler.Placement{}, insufficient(plan)
		}

		credits := append(scheduler.CreditsFromCandidates(stopped), scheduler.CreditsFromCandidates(plan.Evict)...)
		lease, err = s.reservations.Acquire(scheduler.AcquireRequest{InstanceID: i.ID, Snapshot: snapshot, Placement: request, Credits: credits, HostRAM: demand.HostRAMBytes})
		if err != nil {
			return scheduler.Placement{}, err
		}
		if !lease.Placement.Fits {
			return scheduler.Placement{}, insufficient(plan)
		}
		s.logReservation("reserved", i.ID, lease)

		var victim scheduler.Candidate
		for _, candidate := range plan.Evict {
			if candidate.InstanceID == i.ID {
				continue
			}
			victim = candidate
			break
		}
		if victim.InstanceID == "" {
			if err := placementDevicesPresent(snapshot, lease.Placement.Devices); err != nil {
				s.releaseReservation(i.ID)
				return scheduler.Placement{}, err
			}
			return lease.Placement, nil
		}

		if !s.victimPlacementCurrent(victim, snapshot) {
			s.releaseReservation(i.ID)
			skip[victim.InstanceID] = true
			continue
		}
		if err := s.evictInstance(ctx, victim.InstanceID); err != nil {
			if errors.Is(err, errEvictionIneligible) {
				s.releaseReservation(i.ID)
				skip[victim.InstanceID] = true
				continue
			}
			s.releaseReservation(i.ID)
			return scheduler.Placement{}, fmt.Errorf("evict %s: %w", victim.InstanceID, err)
		}
		stopped = append(stopped, victim)

		snapshot, err = s.hardware.Snapshot(ctx)
		if err != nil {
			s.releaseReservation(i.ID)
			return scheduler.Placement{}, fmt.Errorf("refresh hardware after eviction: %w", err)
		}
		if err := placementDevicesPresent(snapshot, lease.Placement.Devices); err != nil {
			s.releaseReservation(i.ID)
			return scheduler.Placement{}, err
		}

		ghost, err := s.reservations.Acquire(scheduler.AcquireRequest{InstanceID: i.ID, Snapshot: snapshot, Placement: request, Credits: scheduler.CreditsFromCandidates(stopped), HostRAM: demand.HostRAMBytes})
		if err != nil {
			return scheduler.Placement{}, err
		}
		if ghost.Placement.Fits {
			s.logReservation("reserved", i.ID, ghost)
			return ghost.Placement, nil
		}
	}
	return scheduler.Placement{}, fmt.Errorf("evict: %w", errEvictionIneligible)
}

func (s *Service) victimPlacementCurrent(candidate scheduler.Candidate, snapshot hardware.Snapshot) bool {
	planned := map[string]bool{}
	for _, gpu := range candidate.Resources.GPU {
		id := strings.TrimSpace(gpu.DeviceID)
		if id != "" {
			planned[id] = true
		}
	}
	if len(planned) == 0 {
		return true
	}
	pid := 0
	if s != nil && s.sup != nil {
		pid = s.sup.Status(candidate.InstanceID).PID
	}
	if pid <= 0 {
		return true
	}
	matched := false
	onPlanned := false
	for _, process := range snapshot.Processes {
		if process.PID != pid {
			continue
		}
		matched = true
		if planned[strings.TrimSpace(process.DeviceID)] {
			onPlanned = true
		}
	}
	if !matched {
		return true
	}
	return onPlanned
}

func placementDevicesPresent(snapshot hardware.Snapshot, devices []string) error {
	if len(devices) == 0 {
		return nil
	}
	byID := make(map[string]bool, len(snapshot.GPUs))
	for _, gpu := range snapshot.GPUs {
		byID[gpu.ID] = true
	}
	for _, id := range devices {
		if !byID[id] {
			return fmt.Errorf("configured GPU device is not available: %s", id)
		}
	}
	return nil
}

func (s *Service) logReservation(action, instanceID string, lease scheduler.ResourceLease) {
	if s == nil || strings.TrimSpace(instanceID) == "" {
		return
	}
	parts := make([]string, 0, len(lease.GPUs))
	for _, gpu := range lease.GPUs {
		parts = append(parts, fmt.Sprintf("%s=%d", gpu.DeviceID, gpu.Bytes))
	}
	s.AddManagerLog(instanceID, fmt.Sprintf("%s lease %s state=%s devices=%s", action, lease.ID, lease.State, strings.Join(parts, ",")))
}

func (s *Service) commitReservation(instanceID string) error {
	if s == nil || s.reservations == nil {
		return nil
	}
	if err := s.reservations.CommitInstance(instanceID); err != nil {
		return err
	}
	if lease, ok := s.reservations.GetByInstance(instanceID); ok {
		s.logReservation("committed", instanceID, lease)
	}
	return nil
}

func (s *Service) releaseReservation(instanceID string) {
	if s == nil || s.reservations == nil {
		return
	}
	lease, ok := s.reservations.GetByInstance(instanceID)
	s.reservations.ReleaseInstance(instanceID)
	if ok {
		s.logReservation("released", instanceID, lease)
	}
}

func (s *Service) evictInstance(ctx context.Context, id string) error {
	if err := s.claimEviction(ctx, id); err != nil {
		return err
	}
	if s.afterEvictionClaim != nil {
		s.afterEvictionClaim(id)
	}
	i, err := s.instances.Get(ctx, id)
	if err != nil {
		s.abortDrainClaim(id)
		return err
	}
	blocked := i.AlwaysOn
	if blocked {
		s.markResourceBlock(id)
	}
	if err := s.stopWorker(ctx, id); err != nil {
		if blocked {
			s.clearResourceBlock(id)
		}
		return err
	}
	s.recordObservabilityEvent(ctx, ObservabilityEviction, id, 0)
	s.AddManagerLog(id, "evicted for resource pressure")
	slog.Info("evicted instance for resource pressure", "instance_id", id, "always_on", i.AlwaysOn)
	return nil
}

func (s *Service) resolveLaunchOptions(ctx context.Context, modelID, instanceID string) (map[string]string, error) {
	effective, err := llamaconfig.New(s.models.DB()).Effective(ctx, modelID, instanceID)
	if err != nil {
		return nil, err
	}
	return effective.Values, nil
}

func (s *Service) estimateDemand(m models.Model, path string, options map[string]string) scheduler.ResourceDemand {
	metadata, metaErr := recommendations.ReadMetadata(path)
	return scheduler.EstimateDemand(scheduler.DemandInput{
		WeightsBytes: m.TotalBytes + companionBytes(options),
		Metadata: scheduler.KVMetadata{
			Architecture: metadata.Architecture, ContextLength: metadata.ContextLength, BlockCount: metadata.BlockCount,
			Embedding: metadata.Embedding, HeadCount: metadata.HeadCount, KVHeadCount: metadata.KVHeadCount,
			KeyLength: metadata.KeyLength, ValueLength: metadata.ValueLength,
		},
		MetadataErr: metaErr,
		Options:     options,
	})
}

func companionBytes(options map[string]string) int64 {
	if options == nil {
		return 0
	}
	total := int64(0)
	for _, key := range []string{"mmproj", "spec-draft-model"} {
		path := strings.TrimSpace(options[key])
		if path == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		total += info.Size()
	}
	return total
}

func appendPlacementLaunchArgs(args []string, devices []string, tensorSplit string, hasTensorSplitOverride bool) ([]string, []string) {
	if len(devices) == 0 {
		return args, nil
	}
	launchDevices := append([]string(nil), devices...)
	var env []string
	if len(devices) == 1 {
		if isolated, extraEnv, ok := isolateSingleVisibleGPU(devices[0]); ok {
			launchDevices[0] = isolated
			env = extraEnv
		}
		args = append(args, "--device", launchDevices[0], "--split-mode", "none")
		return args, env
	}
	args = append(args, "--device", strings.Join(launchDevices, ","))
	if !hasTensorSplitOverride && tensorSplit != "" {
		args = append(args, "--tensor-split", tensorSplit)
	}
	return args, nil
}

// isolateSingleVisibleGPU hides every other GPU from a single-device worker so
// llama.cpp cannot create a CUDA/HIP context on cards that are not in the placement.
func isolateSingleVisibleGPU(deviceID string) (llamaDevice string, env []string, ok bool) {
	index, backend, ok := parseIndexedGPU(deviceID)
	if !ok {
		return "", nil, false
	}
	visible := strconv.Itoa(index)
	switch backend {
	case "cuda":
		return "CUDA0", []string{"CUDA_VISIBLE_DEVICES=" + visible}, true
	case "rocm":
		return "ROCm0", []string{"HIP_VISIBLE_DEVICES=" + visible, "ROCR_VISIBLE_DEVICES=" + visible}, true
	default:
		return "", nil, false
	}
}

func parseIndexedGPU(deviceID string) (int, string, bool) {
	deviceID = strings.TrimSpace(deviceID)
	switch {
	case strings.HasPrefix(deviceID, "CUDA"):
		index, err := strconv.Atoi(strings.TrimPrefix(deviceID, "CUDA"))
		if err != nil || index < 0 {
			return 0, "", false
		}
		return index, "cuda", true
	case strings.HasPrefix(deviceID, "ROCm"):
		index, err := strconv.Atoi(strings.TrimPrefix(deviceID, "ROCm"))
		if err != nil || index < 0 {
			return 0, "", false
		}
		return index, "rocm", true
	default:
		return 0, "", false
	}
}

func optionArgs(options map[string]string) []string {
	keys := make([]string, 0, len(options))
	for k := range options {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		v := strings.TrimSpace(options[k])
		flag := "--" + strings.TrimLeft(k, "-")
		switch strings.ToLower(v) {
		case "true":
			out = append(out, flag)
		case "false", "":
			continue
		default:
			out = append(out, flag, v)
		}
	}
	return out
}
