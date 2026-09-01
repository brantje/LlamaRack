package instances

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

type Instance struct {
	ID                 string   `json:"id"`
	ModelID            string   `json:"model_id"`
	Name               string   `json:"name"`
	Enabled            bool     `json:"enabled"`
	Autoload           bool     `json:"autoload_enabled"`
	AlwaysOn           bool     `json:"always_on"`
	Priority           string   `json:"priority"`
	EvictionEnabled    bool     `json:"eviction_enabled"`
	IdleUnloadSeconds  int      `json:"idle_unload_seconds"`
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
	EvictionEnabled    *bool             `json:"eviction_enabled,omitempty"`
	IdleUnloadSeconds  int               `json:"idle_unload_seconds,omitempty"`
	MaxPendingRequests int               `json:"max_pending_requests,omitempty"`
	GPUMode            string            `json:"gpu_mode,omitempty"`
	GPUDevices         []string          `json:"gpu_devices,omitempty"`
	TensorSplit        string            `json:"tensor_split,omitempty"`
	RequestLogMode     string            `json:"request_log_mode,omitempty"`
	Options            map[string]string `json:"options,omitempty"`
}

type UpdateInput = CreateInput

type Service struct{ db *sql.DB }

func New(db *sql.DB) *Service { return &Service{db: db} }

func Slugify(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if b.Len() > 0 && !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func (s *Service) Create(ctx context.Context, in CreateInput) (Instance, error) {
	i, err := normalize(in)
	if err != nil {
		return Instance{}, err
	}
	var modelExists int
	if err := s.db.QueryRowContext(ctx, "SELECT 1 FROM models WHERE id=?", i.ModelID).Scan(&modelExists); err != nil {
		return Instance{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Instance{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO instances(id,model_id,name,enabled,autoload_enabled,always_on,priority,eviction_enabled,idle_unload_seconds,max_pending_requests,gpu_mode,gpu_devices,tensor_split,request_log_mode) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		i.ID, i.ModelID, i.Name, boolInt(i.Enabled), boolInt(i.Autoload), boolInt(i.AlwaysOn), i.Priority, boolInt(i.EvictionEnabled), i.IdleUnloadSeconds, i.MaxPendingRequests, i.GPUMode, joinDevices(i.GPUDevices), nullString(i.TensorSplit), i.RequestLogMode); err != nil {
		return Instance{}, err
	}
	if err := replaceOptions(ctx, tx, i.ID, in.Options); err != nil {
		return Instance{}, err
	}
	if err := tx.Commit(); err != nil {
		return Instance{}, err
	}
	return i, nil
}

func (s *Service) Update(ctx context.Context, currentID string, in UpdateInput) (Instance, error) {
	current, err := s.Get(ctx, currentID)
	if err != nil {
		return Instance{}, err
	}
	if strings.TrimSpace(in.ModelID) == "" {
		in.ModelID = current.ModelID
	}
	if strings.TrimSpace(in.RequestLogMode) == "" {
		in.RequestLogMode = current.RequestLogMode
	}
	i, err := normalize(in)
	if err != nil {
		return Instance{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Instance{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE instances SET id=?,model_id=?,name=?,enabled=?,autoload_enabled=?,always_on=?,priority=?,eviction_enabled=?,idle_unload_seconds=?,max_pending_requests=?,gpu_mode=?,gpu_devices=?,tensor_split=?,request_log_mode=?,updated_at=unixepoch() WHERE id=?`,
		i.ID, i.ModelID, i.Name, boolInt(i.Enabled), boolInt(i.Autoload), boolInt(i.AlwaysOn), i.Priority, boolInt(i.EvictionEnabled), i.IdleUnloadSeconds, i.MaxPendingRequests, i.GPUMode, joinDevices(i.GPUDevices), nullString(i.TensorSplit), i.RequestLogMode, currentID)
	if err != nil {
		return Instance{}, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return Instance{}, sql.ErrNoRows
	}
	if in.Options != nil {
		if err := replaceOptions(ctx, tx, i.ID, in.Options); err != nil {
			return Instance{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Instance{}, err
	}
	return i, nil
}

func (s *Service) Duplicate(ctx context.Context, id string) (Instance, error) {
	base, err := s.Get(ctx, id)
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
		enabled, autoload, eviction := base.Enabled, base.Autoload, base.EvictionEnabled
		copy, err := s.Create(ctx, CreateInput{
			ModelID: base.ModelID, Name: name, Enabled: &enabled, Autoload: &autoload,
			AlwaysOn: base.AlwaysOn, Priority: base.Priority, EvictionEnabled: &eviction,
			IdleUnloadSeconds: base.IdleUnloadSeconds, MaxPendingRequests: base.MaxPendingRequests, GPUMode: base.GPUMode,
			GPUDevices: append([]string(nil), base.GPUDevices...), TensorSplit: base.TensorSplit, RequestLogMode: base.RequestLogMode, Options: opts,
		})
		if err == nil {
			return copy, nil
		}
		if !strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Instance{}, err
		}
	}
	return Instance{}, errors.New("unable to generate unique instance copy name")
}

func (s *Service) Get(ctx context.Context, id string) (Instance, error) {
	return scan(s.db.QueryRowContext(ctx, `SELECT id,model_id,name,enabled,autoload_enabled,always_on,priority,eviction_enabled,idle_unload_seconds,max_pending_requests,gpu_mode,gpu_devices,tensor_split,request_log_mode FROM instances WHERE id=?`, id))
}

func (s *Service) List(ctx context.Context) ([]Instance, error) {
	return s.list(ctx, `SELECT id,model_id,name,enabled,autoload_enabled,always_on,priority,eviction_enabled,idle_unload_seconds,max_pending_requests,gpu_mode,gpu_devices,tensor_split,request_log_mode FROM instances ORDER BY name,id`)
}

func (s *Service) ListByModel(ctx context.Context, modelID string) ([]Instance, error) {
	return s.list(ctx, `SELECT id,model_id,name,enabled,autoload_enabled,always_on,priority,eviction_enabled,idle_unload_seconds,max_pending_requests,gpu_mode,gpu_devices,tensor_split,request_log_mode FROM instances WHERE model_id=? ORDER BY name,id`, modelID)
}

func (s *Service) list(ctx context.Context, query string, args ...any) ([]Instance, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Instance
	for rows.Next() {
		i, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Service) Options(ctx context.Context, id string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT option_key,option_value FROM instance_options WHERE instance_id=? ORDER BY option_key`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, rows.Err()
}

func (s *Service) Delete(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM instances WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

type scanner interface{ Scan(...any) error }

func scan(row scanner) (Instance, error) {
	var i Instance
	var enabled, autoload, alwaysOn, eviction int
	var devices, split sql.NullString
	if err := row.Scan(&i.ID, &i.ModelID, &i.Name, &enabled, &autoload, &alwaysOn, &i.Priority, &eviction, &i.IdleUnloadSeconds, &i.MaxPendingRequests, &i.GPUMode, &devices, &split, &i.RequestLogMode); err != nil {
		return Instance{}, err
	}
	i.Enabled = enabled != 0
	i.Autoload = autoload != 0
	i.AlwaysOn = alwaysOn != 0
	i.EvictionEnabled = eviction != 0
	if devices.Valid {
		for _, value := range strings.Split(devices.String, ",") {
			if value = strings.TrimSpace(value); value != "" {
				i.GPUDevices = append(i.GPUDevices, value)
			}
		}
	}
	if split.Valid {
		i.TensorSplit = split.String
	}
	return i, nil
}

func normalize(in CreateInput) (Instance, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return Instance{}, errors.New("name is required")
	}
	slugSource := strings.TrimSpace(in.Slug)
	if slugSource == "" {
		slugSource = name
	}
	id := Slugify(slugSource)
	if id == "" {
		if strings.TrimSpace(in.Slug) != "" {
			return Instance{}, errors.New("slug must contain at least one letter or number")
		}
		return Instance{}, errors.New("name must contain at least one letter or number")
	}
	modelID := strings.TrimSpace(in.ModelID)
	if modelID == "" {
		return Instance{}, errors.New("model_id is required")
	}
	if in.IdleUnloadSeconds < 0 {
		return Instance{}, errors.New("idle_unload_seconds must be zero or greater")
	}
	if in.MaxPendingRequests < 0 {
		return Instance{}, errors.New("max_pending_requests must be zero or greater")
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	autoload := true
	if in.Autoload != nil {
		autoload = *in.Autoload
	}
	eviction := true
	if in.EvictionEnabled != nil {
		eviction = *in.EvictionEnabled
	}
	priority := strings.ToLower(strings.TrimSpace(in.Priority))
	if priority == "" {
		priority = "normal"
	}
	if priority != "low" && priority != "normal" && priority != "high" {
		return Instance{}, errors.New("priority must be low, normal, or high")
	}
	gpuMode := strings.ToLower(strings.TrimSpace(in.GPUMode))
	if gpuMode == "" {
		gpuMode = "auto"
	}
	if gpuMode != "auto" && gpuMode != "manual" {
		return Instance{}, errors.New("gpu_mode must be auto or manual")
	}
	requestLogMode := strings.ToLower(strings.TrimSpace(in.RequestLogMode))
	if requestLogMode == "" {
		requestLogMode = "metadata"
	}
	if requestLogMode != "metadata" && requestLogMode != "full" {
		return Instance{}, errors.New("request_log_mode must be metadata or full")
	}
	devices := make([]string, 0, len(in.GPUDevices))
	seen := map[string]bool{}
	for _, device := range in.GPUDevices {
		device = strings.TrimSpace(device)
		if device != "" && !seen[device] {
			devices = append(devices, device)
			seen[device] = true
		}
	}
	return Instance{
		ID: id, ModelID: modelID, Name: name, Enabled: enabled, Autoload: autoload,
		AlwaysOn: in.AlwaysOn, Priority: priority, EvictionEnabled: eviction,
		IdleUnloadSeconds: in.IdleUnloadSeconds, MaxPendingRequests: in.MaxPendingRequests, GPUMode: gpuMode, GPUDevices: devices,
		TensorSplit: strings.TrimSpace(in.TensorSplit), RequestLogMode: requestLogMode,
	}, nil
}

func replaceOptions(ctx context.Context, tx *sql.Tx, id string, options map[string]string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM instance_options WHERE instance_id=?`, id); err != nil {
		return err
	}
	keys := make([]string, 0, len(options))
	for key := range options {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO instance_options(instance_id,option_key,option_value) VALUES(?,?,?)`, id, trimmed, options[key]); err != nil {
			return err
		}
	}
	return nil
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
