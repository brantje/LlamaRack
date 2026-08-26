package lifecycle

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/instances"
	"github.com/brantje/llamacpp-manager/backend/internal/models"
	"github.com/brantje/llamacpp-manager/backend/internal/scheduler"
	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
)

type Activity struct {
	ActiveRequests int       `json:"active_requests"`
	LastUsed       time.Time `json:"last_used,omitempty"`
}

type Service struct {
	models          *models.Service
	instances       *instances.Service
	sup             *supervisor.Supervisor
	mu              sync.Mutex
	loads           map[string]*loadCall
	manuallyStopped map[string]bool
	activities      map[string]Activity
	idleLocks       map[string]*sync.Mutex
	operationGates  map[string]chan struct{}
	now             func() time.Time
}

type loadCall struct {
	done     chan struct{}
	endpoint string
	err      error
}

func New(modelsService *models.Service, sup *supervisor.Supervisor) *Service {
	return &Service{
		models: modelsService, instances: instances.New(modelsService.DB()), sup: sup,
		loads: map[string]*loadCall{}, manuallyStopped: map[string]bool{},
		activities: map[string]Activity{}, idleLocks: map[string]*sync.Mutex{},
		operationGates: map[string]chan struct{}{}, now: time.Now,
	}
}

func (s *Service) Instances() *instances.Service { return s.instances }

// Acquire resolves exactly one addressable Instance. The OpenAI model field is
// the stored instance.id; sibling Instances are never selected as fallback.
func (s *Service) Acquire(ctx context.Context, instanceID string) (string, func(), error) {
	i, err := s.instances.Get(ctx, instanceID)
	if err != nil {
		return "", nil, err
	}
	lock := s.idleLock(i.ID)
	lock.Lock()
	s.beginRequest(i.ID)
	lock.Unlock()
	endpoint, err := s.ensureReadyInstance(ctx, i)
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
	return s.ensureReadyInstance(ctx, i)
}

func (s *Service) ensureReadyInstance(ctx context.Context, i instances.Instance) (string, error) {
	if !i.Enabled {
		return "", errors.New("instance disabled")
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
	return s.startSingleFlight(ctx, i)
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
	} else if s.isManuallyStopped(id) {
		return "", errors.New("instance manually stopped until manager restart")
	}
	if endpoint, ok := s.sup.Endpoint(id); ok {
		return endpoint, nil
	}
	return s.startSingleFlight(ctx, i)
}

func (s *Service) StopInstance(ctx context.Context, id string) error {
	release, err := s.acquireOperation(ctx, id)
	if err != nil {
		return err
	}
	defer release()

	i, err := s.instances.Get(ctx, id)
	if err != nil {
		return err
	}
	if i.AlwaysOn {
		s.markManualStop(id)
	}
	if err := s.sup.Stop(ctx, id); err != nil {
		if i.AlwaysOn {
			s.clearManualStop(id)
		}
		return err
	}
	return nil
}

func (s *Service) RestartInstance(ctx context.Context, id string) (string, error) {
	release, err := s.acquireOperation(ctx, id)
	if err != nil {
		return "", err
	}
	defer release()

	s.clearManualStop(id)
	i, err := s.instances.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if !i.Enabled {
		return "", errors.New("instance disabled")
	}
	if err := s.sup.Stop(ctx, id); err != nil {
		return "", err
	}
	return s.startOne(ctx, i)
}

func (s *Service) KillInstance(ctx context.Context, id string) error {
	release, err := s.acquireOperation(ctx, id)
	if err != nil {
		return err
	}
	defer release()

	if _, err := s.instances.Get(ctx, id); err != nil {
		return err
	}
	s.markManualStop(id)
	return s.sup.Kill(id)
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
		if !i.EvictionEnabled {
			continue
		}
		m, err := s.models.GetByID(ctx, i.ModelID)
		if err != nil {
			return scheduler.Plan{}, err
		}
		activity := s.Activity(i.ID)
		candidates = append(candidates, scheduler.Candidate{
			ModelID: i.ModelID, InstanceID: i.ID, Priority: i.Priority, AlwaysOn: i.AlwaysOn,
			ActiveRequests: activity.ActiveRequests, LastUsed: activity.LastUsed,
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
	for _, i := range items {
		if !i.Enabled || !i.AlwaysOn || s.isManuallyStopped(i.ID) {
			continue
		}
		if _, ok := s.sup.Endpoint(i.ID); ok {
			continue
		}
		go func(id string) { _, _ = s.startInstance(context.Background(), id, false) }(i.ID)
	}
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
		err := s.StopInstance(ctx, i.ID)
		lock.Unlock()
		if err != nil {
			slog.Warn("idle unload failed", "instance_id", i.ID, "error", err)
		}
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

func (s *Service) startSingleFlight(ctx context.Context, i instances.Instance) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	s.mu.Lock()
	c := s.loads[i.ID]
	if c == nil {
		c = &loadCall{done: make(chan struct{})}
		s.loads[i.ID] = c
		go s.runLoad(i.ID, c)
	}
	s.mu.Unlock()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-c.done:
		return c.endpoint, c.err
	}
}

func (s *Service) runLoad(id string, c *loadCall) {
	release, err := s.acquireOperation(context.Background(), id)
	if err != nil {
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
	m, err := s.models.GetByID(ctx, i.ModelID)
	if err != nil {
		return "", err
	}
	path, err := s.models.ModelAbsolutePath(m)
	if err != nil {
		return "", err
	}
	modelOptions, err := s.models.Options(ctx, m.ID)
	if err != nil {
		return "", err
	}
	instanceOptions, err := s.instances.Options(ctx, i.ID)
	if err != nil {
		return "", err
	}
	for key, value := range instanceOptions {
		modelOptions[key] = value
	}
	args := optionArgs(modelOptions)
	if i.GPUMode == "manual" && len(i.GPUDevices) > 0 {
		args = append(args, "--device", strings.Join(i.GPUDevices, ","))
	}
	if i.TensorSplit != "" {
		args = append(args, "--tensor-split", i.TensorSplit)
	}
	_, err = s.sup.Start(ctx, i.ID, m.ID, path, args)
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