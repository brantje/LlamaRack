package lifecycle

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

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
	sup             *supervisor.Supervisor
	mu              sync.Mutex
	loads           map[string]*loadCall
	manuallyStopped map[string]bool
	activities      map[string]Activity
	idleLocks       map[string]*sync.Mutex
	now             func() time.Time
}

type loadCall struct {
	done     chan struct{}
	endpoint string
	err      error
}

func New(modelsService *models.Service, sup *supervisor.Supervisor) *Service {
	return &Service{
		models:          modelsService,
		sup:             sup,
		loads:           map[string]*loadCall{},
		manuallyStopped: map[string]bool{},
		activities:      map[string]Activity{},
		idleLocks:       map[string]*sync.Mutex{},
		now:             time.Now,
	}
}

// Acquire resolves/loads a model for an inference request and marks that model
// active until the returned release function is called. The gateway holds the
// lease for the complete proxied response, including streaming responses.
func (s *Service) Acquire(ctx context.Context, publicID string) (string, func(), error) {
	m, err := s.models.GetByPublicID(ctx, publicID)
	if err != nil {
		return "", nil, err
	}

	// Coordinate request admission with idle shutdown. If idle reconciliation is
	// already stopping this model, admission waits for that stop to finish and
	// normal autoload can start it again deterministically.
	idleLock := s.idleLock(m.ID)
	idleLock.Lock()
	s.beginRequest(m.ID)
	idleLock.Unlock()

	endpoint, err := s.ensureReadyModel(ctx, m)
	if err != nil {
		s.finishRequest(m.ID)
		return "", nil, err
	}

	var once sync.Once
	release := func() {
		once.Do(func() { s.finishRequest(m.ID) })
	}
	return endpoint, release, nil
}

func (s *Service) EnsureReady(ctx context.Context, publicID string) (string, error) {
	m, err := s.models.GetByPublicID(ctx, publicID)
	if err != nil {
		return "", err
	}
	return s.ensureReadyModel(ctx, m)
}

func (s *Service) ensureReadyModel(ctx context.Context, m models.Model) (string, error) {
	if !m.Enabled {
		return "", errors.New("model disabled")
	}
	if endpoint, ok := s.readyEndpoint(ctx, m); ok {
		return endpoint, nil
	}
	if !m.Autoload {
		return "", errors.New("model unloaded and autoload disabled")
	}
	if s.isManuallyStopped(m.ID) {
		s.clearManualStop(m.ID)
		slog.Info("manual stop overridden by inference request", "model_id", m.ID, "public_id", m.PublicID)
	}
	slog.Info("autoload requested", "model_id", m.ID, "public_id", m.PublicID)
	return s.startSingleFlight(ctx, m)
}

func (s *Service) StartModel(ctx context.Context, id string) (string, error) {
	return s.startModel(ctx, id, true)
}

func (s *Service) startModel(ctx context.Context, id string, explicit bool) (string, error) {
	slog.Info("model start requested", "model_id", id)
	m, err := s.models.GetByID(ctx, id)
	if err != nil {
		slog.Error("model start failed", "model_id", id, "error", err)
		return "", err
	}
	if !m.Enabled {
		err := errors.New("model disabled")
		slog.Warn("model start rejected", "model_id", id, "public_id", m.PublicID, "error", err)
		return "", err
	}
	if explicit {
		s.clearManualStop(id)
	} else if s.isManuallyStopped(id) {
		return "", errors.New("model manually stopped until manager restart")
	}
	if endpoint, ok := s.readyEndpoint(ctx, m); ok {
		slog.Info("model already ready", "model_id", id, "public_id", m.PublicID, "endpoint", endpoint)
		return endpoint, nil
	}
	endpoint, err := s.startSingleFlight(ctx, m)
	if err != nil {
		slog.Error("model start failed", "model_id", id, "public_id", m.PublicID, "error", err)
		return "", err
	}
	slog.Info("model start completed", "model_id", id, "public_id", m.PublicID, "endpoint", endpoint)
	return endpoint, nil
}

func (s *Service) StopModel(ctx context.Context, id string) error {
	slog.Info("model stop requested", "model_id", id)
	m, err := s.models.GetByID(ctx, id)
	if err != nil {
		slog.Error("model stop failed", "model_id", id, "error", err)
		return err
	}
	instances, err := s.models.Instances(ctx, id)
	if err != nil {
		slog.Error("model stop failed", "model_id", id, "error", err)
		return err
	}
	if m.AlwaysOn {
		s.markManualStop(id)
		slog.Info("always-on model manually suppressed until manager restart", "model_id", id, "public_id", m.PublicID)
	}
	for _, x := range instances {
		if err := s.sup.Stop(ctx, x.ID); err != nil {
			if m.AlwaysOn {
				s.clearManualStop(id)
			}
			slog.Error("model stop failed", "model_id", id, "instance_id", x.ID, "error", err)
			return err
		}
	}
	slog.Info("model stop completed", "model_id", id, "instances", len(instances))
	return nil
}

func (s *Service) Runtime(ctx context.Context, id string) ([]supervisor.Runtime, error) {
	instances, err := s.models.Instances(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]supervisor.Runtime, 0, len(instances))
	for _, x := range instances {
		out = append(out, s.sup.Status(x.ID))
	}
	return out, nil
}

func (s *Service) Activity(id string) Activity {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activities[id]
}

// EvictionPlan applies the Phase 5 eviction policy to currently loaded
// instances. Model file size is the Level-1 resource estimate; Phase 7 will
// replace/augment it with actual per-device VRAM demand and availability.
func (s *Service) EvictionPlan(ctx context.Context, requiredBytes int64) (scheduler.Plan, error) {
	items, err := s.models.List(ctx)
	if err != nil {
		return scheduler.Plan{}, err
	}
	candidates := make([]scheduler.Candidate, 0)
	for _, m := range items {
		instances, err := s.models.Instances(ctx, m.ID)
		if err != nil {
			return scheduler.Plan{}, err
		}
		activity := s.Activity(m.ID)
		for _, instance := range instances {
			runtime := s.sup.Status(instance.ID)
			candidates = append(candidates, scheduler.Candidate{
				ModelID:        m.ID,
				InstanceID:     instance.ID,
				Priority:       m.Priority,
				AlwaysOn:       m.AlwaysOn,
				ActiveRequests: activity.ActiveRequests,
				LastUsed:       activity.LastUsed,
				EstimatedBytes: m.TotalBytes,
				Ready:          runtime.State == supervisor.Ready,
			})
		}
	}
	return scheduler.PlanEvictions(candidates, requiredBytes), nil
}

func (s *Service) Logs(id string) []string { return s.sup.Logs(id) }

func (s *Service) SubscribeLogs(id string) ([]string, <-chan string, func()) {
	return s.sup.SubscribeLogs(id)
}

func (s *Service) ReconcileAlwaysOn(ctx context.Context) {
	items, err := s.models.List(ctx)
	if err != nil {
		return
	}
	for _, m := range items {
		if !m.Enabled || !m.AlwaysOn || s.isManuallyStopped(m.ID) {
			continue
		}
		if _, ok := s.readyEndpoint(ctx, m); ok {
			continue
		}
		go func(id string) { _, _ = s.startModel(context.Background(), id, false) }(m.ID)
	}
}

func (s *Service) ReconcileIdle(ctx context.Context, idleTimeout time.Duration) {
	if idleTimeout <= 0 {
		return
	}
	items, err := s.models.List(ctx)
	if err != nil {
		return
	}
	now := s.now().UTC()
	for _, m := range items {
		if !m.Enabled || m.AlwaysOn {
			continue
		}

		// Admission and the final idle check share a per-model lock. A request
		// either becomes active before this check (and prevents the stop), or it
		// waits until the stop is complete and then follows normal autoload.
		idleLock := s.idleLock(m.ID)
		idleLock.Lock()
		activity := s.Activity(m.ID)
		if activity.ActiveRequests > 0 || activity.LastUsed.IsZero() || now.Sub(activity.LastUsed) < idleTimeout {
			idleLock.Unlock()
			continue
		}
		if _, ok := s.readyEndpoint(ctx, m); !ok {
			idleLock.Unlock()
			continue
		}
		slog.Info("idle timeout reached; unloading model", "model_id", m.ID, "public_id", m.PublicID, "idle_for", now.Sub(activity.LastUsed))
		err := s.StopModel(ctx, m.ID)
		idleLock.Unlock()
		if err != nil {
			slog.Warn("idle unload failed", "model_id", m.ID, "public_id", m.PublicID, "error", err)
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

func (s *Service) RunIdleReconciler(ctx context.Context, idleTimeout time.Duration) {
	if idleTimeout <= 0 {
		<-ctx.Done()
		return
	}
	interval := idleTimeout / 2
	if interval > 15*time.Second {
		interval = 15 * time.Second
	}
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.ReconcileIdle(ctx, idleTimeout)
		}
	}
}

func (s *Service) beginRequest(id string) {
	s.mu.Lock()
	activity := s.activities[id]
	activity.ActiveRequests++
	activity.LastUsed = s.now().UTC()
	s.activities[id] = activity
	s.mu.Unlock()
}

func (s *Service) finishRequest(id string) {
	s.mu.Lock()
	activity := s.activities[id]
	if activity.ActiveRequests > 0 {
		activity.ActiveRequests--
	}
	activity.LastUsed = s.now().UTC()
	s.activities[id] = activity
	s.mu.Unlock()
}

func (s *Service) touch(id string) {
	s.mu.Lock()
	activity := s.activities[id]
	activity.LastUsed = s.now().UTC()
	s.activities[id] = activity
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

func (s *Service) startSingleFlight(ctx context.Context, m models.Model) (string, error) {
	s.mu.Lock()
	if c := s.loads[m.ID]; c != nil {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-c.done:
			return c.endpoint, c.err
		}
	}
	c := &loadCall{done: make(chan struct{})}
	s.loads[m.ID] = c
	s.mu.Unlock()
	endpoint, err := s.startOne(ctx, m)
	c.endpoint, c.err = endpoint, err
	close(c.done)
	s.mu.Lock()
	delete(s.loads, m.ID)
	s.mu.Unlock()
	return endpoint, err
}

func (s *Service) startOne(ctx context.Context, m models.Model) (string, error) {
	instances, err := s.models.Instances(ctx, m.ID)
	if err != nil {
		return "", err
	}
	var selected *models.Instance
	for i := range instances {
		if instances[i].Enabled {
			selected = &instances[i]
			break
		}
	}
	if selected == nil {
		return "", errors.New("no enabled instance")
	}
	path, err := s.models.ModelAbsolutePath(m)
	if err != nil {
		return "", err
	}
	opts, err := s.models.Options(ctx, m.ID)
	if err != nil {
		return "", err
	}
	args := optionArgs(opts)
	if selected.GPUMode == "manual" && len(selected.GPUDevices) > 0 {
		args = append(args, "--device", strings.Join(selected.GPUDevices, ","))
	}
	if selected.TensorSplit != "" {
		args = append(args, "--tensor-split", selected.TensorSplit)
	}
	slog.Info("starting model instance", "model_id", m.ID, "public_id", m.PublicID, "instance_id", selected.ID, "model_path", path)
	_, err = s.sup.Start(ctx, selected.ID, m.ID, path, args)
	if err != nil {
		return "", err
	}
	endpoint, ok := s.sup.Endpoint(selected.ID)
	if !ok {
		return "", errors.New("worker did not reach ready state")
	}
	s.touch(m.ID)
	return endpoint, nil
}

func (s *Service) readyEndpoint(ctx context.Context, m models.Model) (string, bool) {
	instances, err := s.models.Instances(ctx, m.ID)
	if err != nil {
		return "", false
	}
	for _, x := range instances {
		if endpoint, ok := s.sup.Endpoint(x.ID); ok {
			return endpoint, true
		}
	}
	return "", false
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
