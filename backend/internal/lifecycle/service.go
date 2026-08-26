package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/models"
	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
)

type loadCall struct {
	done     chan struct{}
	endpoint string
	err      error
}

type Service struct {
	models *models.Service
	sup    *supervisor.Supervisor
	mu     sync.Mutex
	loads  map[string]*loadCall
}

func New(m *models.Service, sup *supervisor.Supervisor) *Service {
	return &Service{models: m, sup: sup, loads: map[string]*loadCall{}}
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
	m, err := s.models.GetByID(ctx, id)
	if err != nil {
		return "", err
	}
	if !m.Enabled {
		return "", errors.New("model disabled")
	}
	if endpoint, ok := s.readyEndpoint(ctx, m); ok {
		return endpoint, nil
	}
	return s.startSingleFlight(ctx, m)
}

func (s *Service) StopModel(ctx context.Context, id string) error {
	instances, err := s.models.Instances(ctx, id)
	if err != nil {
		return err
	}
	for _, instance := range instances {
		if err := s.sup.Stop(ctx, instance.ID); err != nil {
			return err
		}
	}
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

func (s *Service) SubscribeLogs(id string) ([]string, <-chan string, func()) {
	return s.sup.SubscribeLogs(id)
}

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
	for idx := range instances {
		if instances[idx].Enabled {
			selected = &instances[idx]
			break
		}
	}
	if selected == nil {
		return "", errors.New("no enabled instance")
	}
	modelPath, err := s.models.ModelAbsolutePath(m)
	if err != nil {
		return "", err
	}
	options, err := s.models.Options(ctx, m.ID)
	if err != nil {
		return "", err
	}
	args := []string{"--model", modelPath}
	if selected.GPUMode == "manual" && len(selected.GPUDevices) > 0 {
		args = append(args, "--device", strings.Join(selected.GPUDevices, ","))
	}
	if selected.TensorSplit != "" {
		args = append(args, "--tensor-split", selected.TensorSplit)
	}
	args = append(args, optionArgs(options)...)
	slog.Info("starting model worker", "model_id", m.ID, "public_id", m.PublicID, "instance_id", selected.ID)
	runtime, err := s.sup.Start(ctx, selected.ID, m.ID, modelPath, args)
	if err != nil {
		slog.Error("model worker start failed", "model_id", m.ID, "public_id", m.PublicID, "instance_id", selected.ID, "error", err)
		return "", err
	}
	slog.Info("model worker ready", "model_id", m.ID, "public_id", m.PublicID, "instance_id", selected.ID, "pid", runtime.PID, "port", runtime.Port)
	return s.sup.Endpoint(selected.ID), nil
}

func (s *Service) readyEndpoint(ctx context.Context, m models.Model) (string, bool) {
	instances, err := s.models.Instances(ctx, m.ID)
	if err != nil {
		return "", false
	}
	for _, instance := range instances {
		if !instance.Enabled {
			continue
		}
		if status := s.sup.Status(instance.ID); status.State == supervisor.Ready {
			return s.sup.Endpoint(instance.ID), true
		}
	}
	return "", false
}

func optionArgs(options map[string]string) []string {
	keys := make([]string, 0, len(options))
	for key := range options {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(options)*2)
	for _, key := range keys {
		value := strings.TrimSpace(options[key])
		key = strings.TrimSpace(key)
		if key == "" || value == "" || strings.EqualFold(value, "false") {
			continue
		}
		if !strings.HasPrefix(key, "--") {
			key = "--" + key
		}
		out = append(out, key)
		if !strings.EqualFold(value, "true") {
			out = append(out, value)
		}
	}
	return out
}

func modelDescription(m models.Model) string {
	return fmt.Sprintf("%s (%s)", m.PublicID, m.ID)
}
