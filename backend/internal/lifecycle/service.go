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
	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
)

type Service struct {
	models *models.Service
	sup    *supervisor.Supervisor
	mu     sync.Mutex
	loads  map[string]*loadCall
}

type loadCall struct {
	done     chan struct{}
	endpoint string
	err      error
}

func New(modelsService *models.Service, sup *supervisor.Supervisor) *Service {
	return &Service{models: modelsService, sup: sup, loads: map[string]*loadCall{}}
}

func (s *Service) EnsureReady(ctx context.Context, publicID string) (string, error) {
	m, err := s.models.GetByPublicID(ctx, publicID)
	if err != nil {
		return "", err
	}
	if !m.Enabled {
		return "", errors.New("model disabled")
	}
	if endpoint, ok := s.readyEndpoint(ctx, m); ok {
		return endpoint, nil
	}
	if !m.Autoload {
		return "", errors.New("model unloaded and autoload disabled")
	}
	slog.Info("autoload requested", "model_id", m.ID, "public_id", m.PublicID)
	return s.startSingleFlight(ctx, m)
}

func (s *Service) StartModel(ctx context.Context, id string) (string, error) {
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
	instances, err := s.models.Instances(ctx, id)
	if err != nil {
		slog.Error("model stop failed", "model_id", id, "error", err)
		return err
	}
	for _, x := range instances {
		if err := s.sup.Stop(ctx, x.ID); err != nil {
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

func (s *Service) Logs(id string) []string { return s.sup.Logs(id) }

func (s *Service) ReconcileAlwaysOn(ctx context.Context) {
	items, err := s.models.List(ctx)
	if err != nil {
		return
	}
	for _, m := range items {
		if !m.Enabled || !m.AlwaysOn {
			continue
		}
		if _, ok := s.readyEndpoint(ctx, m); ok {
			continue
		}
		go func(id string) { _, _ = s.StartModel(context.Background(), id) }(m.ID)
	}
}

func (s *Service) RunReconciler(ctx context.Context) {
	s.ReconcileAlwaysOn(ctx)
	ticker := time.NewTicker(15 * time.Second)
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
	path, err := s.models.ArtifactAbsolutePath(m)
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
