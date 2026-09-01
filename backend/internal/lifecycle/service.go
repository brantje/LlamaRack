package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	"github.com/brantje/llamarack/backend/internal/scheduler"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

const resourcePressureReason = "resource_pressure"
const maxEvictionPlanAttempts = 2

var (
	errResourcePressureBlocked = errors.New("insufficient resources without resource-pressure eviction")
	errEvictionIneligible      = errors.New("instance is no longer eligible for resource-pressure eviction")
)

type Activity struct {
	ActiveRequests int       `json:"active_requests"`
	LastUsed       time.Time `json:"last_used,omitempty"`
}

type Service struct {
	models                *models.Service
	instances             *instances.Service
	sup                   *supervisor.Supervisor
	hardware              hardware.Snapshotter
	profile               func() (llamacpp.Profile, error)
	observabilityRecorder ObservabilityRecorder
	mu                    sync.Mutex
	loads                 map[string]*loadCall
	manuallyStopped       map[string]bool
	resourceBlocked       map[string]string
	resourceStarts        int
	activities            map[string]Activity
	idleLocks             map[string]*sync.Mutex
	operationGates        map[string]chan struct{}
	drainWaits            map[string]chan struct{}
	beforeEvictionLock    func(id string)
	afterEvictionClaim    func(id string)
	afterIdleDrainClaim   func(id string)
	now                   func() time.Time
}

type loadCall struct {
	done     chan struct{}
	endpoint string
	err      error
	autoload bool
}

func New(modelsService *models.Service, sup *supervisor.Supervisor) *Service {
	return &Service{
		models: modelsService, instances: instances.New(modelsService.DB()), sup: sup, hardware: hardware.New(),
		loads: map[string]*loadCall{}, manuallyStopped: map[string]bool{}, resourceBlocked: map[string]string{},
		activities: map[string]Activity{}, idleLocks: map[string]*sync.Mutex{},
		operationGates: map[string]chan struct{}{}, drainWaits: map[string]chan struct{}{}, now: time.Now,
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
	if err := s.admitRequest(ctx, i.ID); err != nil {
		return "", nil, err
	}
	endpoint, err := s.ensureReadyInstance(ctx, i, true)
	if err != nil {
		s.finishRequest(i.ID)
		return "", nil, err
	}
	var once sync.Once
	return endpoint, func() { once.Do(func() { s.finishRequest(i.ID) }) }, nil
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
	} else if s.isManuallyStopped(id) {
		return "", errors.New("instance manually stopped until manager restart")
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

	s.clearManualStop(id)
	s.clearResourceBlock(id)
	if err := s.sup.Stop(ctx, id); err != nil {
		s.abortDrainClaim(id)
		return "", err
	}
	s.finishDrainWait(id)
	endpoint, err := s.startOne(ctx, i)
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
	defer release()

	s.clearResourceBlock(id)
	s.markManualStop(id)
	if err := s.sup.Kill(id); err != nil {
		s.abortDrainClaim(id)
		return err
	}
	_ = s.sup.WaitInactive(ctx, id)
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
	return rt, nil
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
		out = append(out, rt)
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
		out.ActiveRequests += a.ActiveRequests
		if a.LastUsed.After(out.LastUsed) {
			out.LastUsed = a.LastUsed
		}
	}
	return out
}

func (s *Service) EvictionPlan(ctx context.Context, requiredBytes int64) (scheduler.Plan, error) {
	items, err := s.instances.List(ctx)
	if err != nil {
		return scheduler.Plan{}, err
	}
	candidates := make([]scheduler.Candidate, 0, len(items))
	for _, i := range items {
		m, err := s.models.GetByID(ctx, i.ModelID)
		if err != nil {
			return scheduler.Plan{}, err
		}
		activity := s.Activity(i.ID)
		candidates = append(candidates, scheduler.Candidate{
			ModelID: i.ModelID, InstanceID: i.ID, Priority: i.Priority, AlwaysOn: i.AlwaysOn,
			EvictionEnabled: i.EvictionEnabled, ActiveRequests: activity.ActiveRequests, LastUsed: activity.LastUsed,
			EstimatedBytes: m.TotalBytes, Ready: s.sup.Status(i.ID).State == supervisor.Ready,
		})
	}
	return scheduler.PlanEvictions(candidates, requiredBytes), nil
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
		if !i.Enabled || !i.AlwaysOn || s.isManuallyStopped(i.ID) {
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
	if s.resourceStartActive() {
		return
	}
	release, err := s.acquireOperation(context.Background(), id)
	if err != nil {
		return
	}
	defer release()
	if s.resourceStartActive() || s.resourceBlockReason(id) == "" || s.isManuallyStopped(id) {
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
	_, err = s.startOneWithEviction(context.Background(), i, false)
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
		if activity.ActiveRequests > 0 || activity.LastUsed.IsZero() || now.Sub(activity.LastUsed) < idleTimeout {
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
		s.beginRequest(id)
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
	if !i.EvictionEnabled || activity.ActiveRequests > 0 || s.sup.Status(id).State != supervisor.Ready {
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
	if err := s.sup.Stop(ctx, id); err != nil {
		s.abortDrainClaim(id)
		return err
	}
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

func (s *Service) beginRequest(id string) {
	s.mu.Lock()
	a := s.activities[id]
	a.ActiveRequests++
	a.LastUsed = s.now().UTC()
	s.activities[id] = a
	s.mu.Unlock()
}
func (s *Service) finishRequest(id string) {
	s.mu.Lock()
	a := s.activities[id]
	if a.ActiveRequests > 0 {
		a.ActiveRequests--
	}
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
func (s *Service) beginResourceStart() {
	s.mu.Lock()
	s.resourceStarts++
	s.mu.Unlock()
}
func (s *Service) endResourceStart() {
	s.mu.Lock()
	if s.resourceStarts > 0 {
		s.resourceStarts--
	}
	s.mu.Unlock()
}
func (s *Service) resourceStartActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resourceStarts > 0
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
	if s.isManuallyStopped(id) {
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
				endpoint, err = s.startOne(context.Background(), i)
			}
		}
	}
	if c.autoload {
		s.AddManagerLog(id, "autoload triggered by inference request")
	}
	s.completeLoad(id, c, endpoint, err)
}

func (s *Service) completeLoad(id string, c *loadCall, endpoint string, err error) {
	s.mu.Lock()
	c.endpoint = endpoint
	c.err = err
	if s.loads[id] == c {
		delete(s.loads, id)
	}
	close(c.done)
	s.mu.Unlock()
}

func (s *Service) startOne(ctx context.Context, i instances.Instance) (string, error) {
	return s.startOneWithEviction(ctx, i, true)
}

func (s *Service) startOneWithEviction(ctx context.Context, i instances.Instance, allowEviction bool) (endpoint string, err error) {
	started := time.Now()
	defer func() {
		duration := time.Since(started)
		if err == nil {
			s.recordObservabilityEvent(ctx, ObservabilityLoad, i.ID, duration)
			s.AddManagerLog(i.ID, fmt.Sprintf("worker ready after %s", duration.Round(time.Millisecond)))
			return
		}
		if errors.Is(err, errResourcePressureBlocked) {
			return
		}
		s.recordObservabilityEvent(ctx, ObservabilityFailedStart, i.ID, duration)
		s.AddManagerLog(i.ID, "worker failed to start: "+err.Error())
	}()

	if allowEviction {
		s.beginResourceStart()
		defer s.endResourceStart()
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

	placement, placementErr := s.preparePlacementWithEviction(ctx, i, m.TotalBytes, allowEviction)
	if placementErr != nil {
		return "", placementErr
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
		return "", err
	}
	endpoint, ok := s.sup.Endpoint(i.ID)
	if !ok {
		return "", errors.New("worker did not reach ready state")
	}
	s.touch(i.ID)
	return endpoint, nil
}

func (s *Service) preparePlacement(ctx context.Context, i instances.Instance, requiredBytes int64) (scheduler.Placement, error) {
	return s.preparePlacementWithEviction(ctx, i, requiredBytes, true)
}

func (s *Service) preparePlacementWithEviction(ctx context.Context, i instances.Instance, requiredBytes int64, allowEviction bool) (scheduler.Placement, error) {
	snapshot, err := s.hardware.Snapshot(ctx)
	if err != nil {
		slog.Warn("hardware snapshot unavailable; preserving compatibility placement", "instance_id", i.ID, "error", err)
		return scheduler.Placement{}, nil
	}
	if len(snapshot.GPUs) == 0 {
		return scheduler.Placement{}, nil
	}
	request := scheduler.PlacementRequest{RequiredBytes: requiredBytes, Mode: i.GPUMode, Devices: i.GPUDevices, TensorSplit: i.TensorSplit}
	placement, err := scheduler.PlanPlacement(snapshot, request)
	if err != nil {
		return scheduler.Placement{}, err
	}
	if placement.Fits {
		return placement, nil
	}
	if !allowEviction {
		return scheduler.Placement{}, fmt.Errorf("%w: need %d bytes, have %d", errResourcePressureBlocked, requiredBytes, placement.AvailableBytes)
	}

	shortfall := requiredBytes - placement.AvailableBytes
	if shortfall < 1 {
		shortfall = requiredBytes
	}
	plan, err := s.EvictionPlan(ctx, shortfall)
	if err != nil {
		return scheduler.Placement{}, err
	}
	if !plan.Fits {
		return scheduler.Placement{}, fmt.Errorf("insufficient usable VRAM: need %d more bytes and eligible eviction victims can free only %d", shortfall, plan.FreedBytes)
	}
	for attempt := 0; attempt < maxEvictionPlanAttempts; attempt++ {
		if attempt > 0 {
			plan, err = s.EvictionPlan(ctx, shortfall)
			if err != nil {
				return scheduler.Placement{}, err
			}
			if !plan.Fits {
				return scheduler.Placement{}, fmt.Errorf("insufficient usable VRAM: need %d more bytes and eligible eviction victims can free only %d", shortfall, plan.FreedBytes)
			}
		}
		stale := false
		for _, candidate := range plan.Evict {
			if candidate.InstanceID == i.ID {
				continue
			}
			if err := s.evictInstance(ctx, candidate.InstanceID); err != nil {
				if errors.Is(err, errEvictionIneligible) {
					stale = true
					break
				}
				return scheduler.Placement{}, fmt.Errorf("evict %s: %w", candidate.InstanceID, err)
			}
		}
		if !stale {
			break
		}
		if attempt == maxEvictionPlanAttempts-1 {
			return scheduler.Placement{}, fmt.Errorf("evict: %w", errEvictionIneligible)
		}
	}

	refreshed, err := s.hardware.Snapshot(ctx)
	if err != nil {
		return scheduler.Placement{}, fmt.Errorf("refresh hardware after eviction: %w", err)
	}
	placement, err = scheduler.PlanPlacement(refreshed, request)
	if err != nil {
		return scheduler.Placement{}, err
	}
	if !placement.Fits {
		return scheduler.Placement{}, fmt.Errorf("insufficient usable VRAM after eviction: need %d bytes, have %d", requiredBytes, placement.AvailableBytes)
	}
	return placement, nil
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
