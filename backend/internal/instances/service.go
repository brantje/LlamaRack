package instances

import (
	"github.com/brantje/llamarack/backend/internal/database"
	"context"
	"errors"
	"regexp"
	"sort"
	"strings"

	"github.com/brantje/llamarack/backend/internal/resourceid"
)

type Instance struct {
	ID                 string   `json:"id"`
	Slug               string   `json:"slug"`
	ModelID            string   `json:"model_id"`
	Name               string   `json:"name"`
	Enabled            bool     `json:"enabled"`
	Autoload           bool     `json:"autoload_enabled"`
	AlwaysOn           bool     `json:"always_on"`
	Priority           string   `json:"priority"`
	EvictionEnabled        bool     `json:"eviction_enabled"`
	SystemSpilloverEnabled bool     `json:"system_spillover_enabled"`
	IdleUnloadSeconds      int      `json:"idle_unload_seconds"`
	MaxPendingRequests int      `json:"max_pending_requests"`
	GPUMode            string   `json:"gpu_mode"`
	GPUDevices         []string `json:"gpu_devices,omitempty"`
	TensorSplit        string   `json:"tensor_split,omitempty"`
	RequestLogMode     string   `json:"request_log_mode"`
}

type CreateInput struct {
	ModelID            string            `json:"model_id"`
	Name               string            `json:"name"`
	Slug               string            `json:"slug,omitempty"`
	Enabled            *bool             `json:"enabled,omitempty"`
	Autoload           *bool             `json:"autoload_enabled,omitempty"`
	AlwaysOn           bool              `json:"always_on"`
	Priority           string            `json:"priority,omitempty"`
	EvictionEnabled        *bool             `json:"eviction_enabled,omitempty"`
	SystemSpilloverEnabled *bool             `json:"system_spillover_enabled,omitempty"`
	IdleUnloadSeconds      int               `json:"idle_unload_seconds,omitempty"`
	MaxPendingRequests *int              `json:"max_pending_requests,omitempty"`
	GPUMode            string            `json:"gpu_mode,omitempty"`
	GPUDevices         []string          `json:"gpu_devices,omitempty"`
	TensorSplit        string            `json:"tensor_split,omitempty"`
	RequestLogMode     string            `json:"request_log_mode,omitempty"`
	Options            map[string]string `json:"options,omitempty"`
}
type UpdateInput struct {
	ModelID            string            `json:"model_id,omitempty"`
	Name               string            `json:"name"`
	Slug               string            `json:"slug,omitempty"`
	Enabled            *bool             `json:"enabled,omitempty"`
	Autoload           *bool             `json:"autoload_enabled,omitempty"`
	AlwaysOn           bool              `json:"always_on"`
	Priority           string            `json:"priority,omitempty"`
	EvictionEnabled        *bool             `json:"eviction_enabled,omitempty"`
	SystemSpilloverEnabled *bool             `json:"system_spillover_enabled,omitempty"`
	IdleUnloadSeconds      int               `json:"idle_unload_seconds,omitempty"`
	MaxPendingRequests *int              `json:"max_pending_requests,omitempty"`
	GPUMode            string            `json:"gpu_mode,omitempty"`
	GPUDevices         []string          `json:"gpu_devices,omitempty"`
	TensorSplit        string            `json:"tensor_split,omitempty"`
	RequestLogMode     string            `json:"request_log_mode,omitempty"`
	Options            map[string]string `json:"options,omitempty"`
}

type ChangeNotifier func(ctx context.Context, instanceID string)
type Service struct {
	store InstanceStore
	onChange ChangeNotifier
	hotCache instanceHotCache
}

func New(db database.Store) *Service {
	return &Service{store: NewInstanceStore(db), hotCache: instanceHotCache{byID: map[string]Instance{}, slugToID: map[string]string{}}}
}
func (s *Service) SetOnChange(fn ChangeNotifier) { s.onChange = fn }
func (s *Service) notifyChange(ctx context.Context, instanceID string) {
	if s.onChange != nil {
		s.onChange(ctx, instanceID)
	}
}
func (s *Service) NotifyChange(ctx context.Context, instanceID string) {
	s.forgetHot(instanceID)
	s.notifyChange(ctx, instanceID)
}
func Slugify(name string) string { return resourceid.Slugify(name) }

func (s *Service) Create(ctx context.Context, in CreateInput) (Instance, error) {
	i, err := normalizeCreate(in)
	if err != nil {
		return Instance{}, err
	}
	if err := s.store.RequireModel(ctx, i.ModelID); err != nil {
		return Instance{}, err
	}
	i.ID, err = resourceid.NewUUID()
	if err != nil {
		return Instance{}, err
	}
	if err := s.store.Create(ctx, i, in.Options); err != nil {
		return Instance{}, err
	}
	resourceid.RememberInstanceSlug(i.ID, i.Slug)
	s.rememberHot(i)
	s.notifyChange(ctx, i.ID)
	return cloneInstance(i), nil
}

func (s *Service) Update(ctx context.Context, currentID string, in UpdateInput) (Instance, error) {
	current, err := s.GetByID(ctx, currentID)
	if err != nil {
		return Instance{}, err
	}
	i, err := normalizeUpdate(current, in)
	if err != nil {
		return Instance{}, err
	}
	if err := s.store.Update(ctx, currentID, i, in.Options, in.Options != nil); err != nil {
		return Instance{}, err
	}
	s.forgetHot(currentID)
	resourceid.RememberInstanceSlug(i.ID, i.Slug)
	s.rememberHot(i)
	s.notifyChange(ctx, currentID)
	return cloneInstance(i), nil
}

func (s *Service) Duplicate(ctx context.Context, id string) (Instance, error) {
	base, err := s.GetByID(ctx, id)
	if err != nil {
		return Instance{}, err
	}
	opts, err := s.Options(ctx, id)
	if err != nil {
		return Instance{}, err
	}
	for n := 1; n < 1000; n++ {
		name := base.Name + " copy"
		if n > 1 {
			name += " " + itoa(n)
		}
		enabled, autoload, eviction, spillover := base.Enabled, base.Autoload, base.EvictionEnabled, base.SystemSpilloverEnabled
		copy, err := s.Create(ctx, CreateInput{ModelID: base.ModelID, Name: name, Enabled: &enabled, Autoload: &autoload, AlwaysOn: base.AlwaysOn, Priority: base.Priority, EvictionEnabled: &eviction, SystemSpilloverEnabled: &spillover, IdleUnloadSeconds: base.IdleUnloadSeconds, MaxPendingRequests: &base.MaxPendingRequests, GPUMode: base.GPUMode, GPUDevices: append([]string(nil), base.GPUDevices...), TensorSplit: base.TensorSplit, RequestLogMode: base.RequestLogMode, Options: opts})
		if err == nil {
			return copy, nil
		}
		if !errors.Is(err, database.ErrConflict) {
			return Instance{}, err
		}
	}
	return Instance{}, errors.New("unable to generate unique instance copy name")
}

func (s *Service) Get(ctx context.Context, id string) (Instance, error) { return s.GetByID(ctx, id) }

func (s *Service) GetByID(ctx context.Context, id string) (Instance, error) {
	id = strings.TrimSpace(id)
	for {
		if item, generation, ok := s.cachedByIDAtGeneration(id); ok {
			return item, nil
		}
		item, err := s.store.GetByID(ctx, id)
		if err != nil {
			return Instance{}, err
		}
		if s.rememberHotIfGeneration(item, generation) {
			return cloneInstance(item), nil
		}
	}
}

func (s *Service) GetBySlug(ctx context.Context, slug string) (Instance, error) {
	slug = resourceid.Slugify(slug)
	for {
		if item, generation, ok := s.cachedBySlugAtGeneration(slug); ok {
			return item, nil
		}
		item, err := s.store.GetBySlug(ctx, slug)
		if err != nil {
			return Instance{}, err
		}
		if s.rememberHotIfGeneration(item, generation) {
			return cloneInstance(item), nil
		}
	}
}

func (s *Service) List(ctx context.Context) ([]Instance, error) {
	return s.store.List(ctx)
}

func (s *Service) ListByModel(ctx context.Context, modelID string) ([]Instance, error) {
	return s.store.ListByModel(ctx, modelID)
}

func (s *Service) Options(ctx context.Context, id string) (map[string]string, error) {
	return s.store.Options(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	if err := s.store.Delete(ctx, id); err != nil {
		return err
	}
	s.forgetHot(id)
	resourceid.ForgetInstanceSlug(id)
	s.notifyChange(ctx, id)
	return nil
}

func normalizeCreate(in CreateInput) (Instance, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return Instance{}, errors.New("name is required")
	}
	slugSource := strings.TrimSpace(in.Slug)
	if slugSource == "" {
		slugSource = name
	}
	slug := Slugify(slugSource)
	if slug == "" {
		if strings.TrimSpace(in.Slug) != "" {
			return Instance{}, errors.New("slug must contain at least one letter or number")
		}
		return Instance{}, errors.New("name must contain at least one letter or number")
	}
	return normalizeValues(Instance{Slug: slug}, in.ModelID, name, in.Enabled, in.Autoload, in.AlwaysOn, in.Priority, in.EvictionEnabled, in.SystemSpilloverEnabled, in.IdleUnloadSeconds, in.MaxPendingRequests, in.GPUMode, in.GPUDevices, in.TensorSplit, in.RequestLogMode)
}
func normalizeUpdate(current Instance, in UpdateInput) (Instance, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return Instance{}, errors.New("name is required")
	}
	slug := current.Slug
	if strings.TrimSpace(in.Slug) != "" {
		slug = Slugify(in.Slug)
		if slug == "" {
			return Instance{}, errors.New("slug must contain at least one letter or number")
		}
	}
	modelID := strings.TrimSpace(in.ModelID)
	if modelID == "" {
		modelID = current.ModelID
	}
	requestLogMode := in.RequestLogMode
	if strings.TrimSpace(requestLogMode) == "" {
		requestLogMode = current.RequestLogMode
	}
	maxPending := in.MaxPendingRequests
	if maxPending == nil {
		value := current.MaxPendingRequests
		maxPending = &value
	}
	spillover := in.SystemSpilloverEnabled
	if spillover == nil {
		value := current.SystemSpilloverEnabled
		spillover = &value
	}
	item, err := normalizeValues(Instance{ID: current.ID, Slug: slug}, modelID, name, in.Enabled, in.Autoload, in.AlwaysOn, in.Priority, in.EvictionEnabled, spillover, in.IdleUnloadSeconds, maxPending, in.GPUMode, in.GPUDevices, in.TensorSplit, requestLogMode)
	if err != nil {
		return Instance{}, err
	}
	item.ID = current.ID
	return item, nil
}

func normalizeValues(base Instance, modelID, name string, enabledInput, autoloadInput *bool, alwaysOn bool, priorityInput string, evictionInput, spilloverInput *bool, idleUnloadSeconds int, maxPendingInput *int, gpuModeInput string, gpuDevices []string, tensorSplit, requestLogModeInput string) (Instance, error) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return Instance{}, errors.New("model_id is required")
	}
	if idleUnloadSeconds < 0 {
		return Instance{}, errors.New("idle_unload_seconds must be zero or greater")
	}
	pending := 0
	if maxPendingInput != nil {
		if *maxPendingInput < 0 {
			return Instance{}, errors.New("max_pending_requests must be zero or greater")
		}
		pending = *maxPendingInput
	}
	enabled := true
	if enabledInput != nil {
		enabled = *enabledInput
	}
	autoload := true
	if autoloadInput != nil {
		autoload = *autoloadInput
	}
	eviction := true
	if evictionInput != nil {
		eviction = *evictionInput
	}
	spillover := false
	if spilloverInput != nil {
		spillover = *spilloverInput
	}
	priority := strings.ToLower(strings.TrimSpace(priorityInput))
	if priority == "" {
		priority = "normal"
	}
	if priority != "low" && priority != "normal" && priority != "high" {
		return Instance{}, errors.New("priority must be low, normal, or high")
	}
	gpuMode := strings.ToLower(strings.TrimSpace(gpuModeInput))
	if gpuMode == "" {
		gpuMode = "auto"
	}
	if gpuMode != "auto" && gpuMode != "manual" {
		return Instance{}, errors.New("gpu_mode must be auto or manual")
	}
	requestLogMode := strings.ToLower(strings.TrimSpace(requestLogModeInput))
	if requestLogMode == "" {
		requestLogMode = "metadata"
	}
	if requestLogMode != "metadata" && requestLogMode != "full" {
		return Instance{}, errors.New("request_log_mode must be metadata or full")
	}
	devices := make([]string, 0, len(gpuDevices))
	seen := map[string]bool{}
	for _, device := range gpuDevices {
		device = strings.TrimSpace(device)
		if device != "" && !seen[device] {
			devices = append(devices, device)
			seen[device] = true
		}
	}
	base.ModelID = modelID
	base.Name = name
	base.Enabled = enabled
	base.Autoload = autoload
	base.AlwaysOn = alwaysOn
	base.Priority = priority
	base.EvictionEnabled = eviction
	base.SystemSpilloverEnabled = spillover
	base.IdleUnloadSeconds = idleUnloadSeconds
	base.MaxPendingRequests = pending
	base.GPUMode = gpuMode
	base.GPUDevices = devices
	base.TensorSplit = strings.TrimSpace(tensorSplit)
	base.RequestLogMode = requestLogMode
	return base, nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func joinDevices(values []string) any {
	if len(values) == 0 {
		return nil
	}
	return strings.Join(values, ",")
}
func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

var digitsOnly = regexp.MustCompile(`^[0-9]+$`)

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	value := string(buf[pos:])
	if !digitsOnly.MatchString(value) {
		return ""
	}
	return value
}
