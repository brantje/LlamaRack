package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	SessionLifetimeSeconds    = "session_lifetime_seconds"
	LoginProtectionEnabled   = "login_protection_enabled"
	LoginFailureThreshold    = "login_failure_threshold"
	LoginLockoutSeconds      = "login_lockout_seconds"
	TrustedProxies           = "trusted_proxies"
	AllowedOrigins           = "allowed_origins"
	ExternalURL              = "external_url"
	StartupTimeoutSeconds    = "startup_timeout_seconds"
	IdleUnloadSeconds        = "idle_unload_seconds"
	AlwaysOnReconcileSeconds = "always_on_reconcile_seconds"
)

type Defaults struct {
	SessionLifetime   time.Duration
	AllowedOrigins    string
	StartupTimeout    time.Duration
	AlwaysOnReconcile time.Duration
	DataDir           string
	ModelsDir         string
	DatabasePath      string
	ListenAddr        string
	LlamaServerPath   string
}

type Value struct {
	Value    any    `json:"value"`
	Source   string `json:"source"`
	Editable bool   `json:"editable"`
}

type RuntimeInfo struct {
	DataDir         string `json:"data_dir"`
	ModelsDir       string `json:"models_dir"`
	DatabasePath    string `json:"database_path"`
	ListenAddr      string `json:"listen_addr"`
	LlamaServerPath string `json:"llama_server_path"`
}

type General struct {
	SessionLifetime        Value       `json:"session_lifetime_seconds"`
	LoginProtection       Value       `json:"login_protection_enabled"`
	LoginFailureThreshold Value       `json:"login_failure_threshold"`
	LoginLockout          Value       `json:"login_lockout_seconds"`
	TrustedProxies        Value       `json:"trusted_proxies"`
	AllowedOrigins        Value       `json:"allowed_origins"`
	ExternalURL           Value       `json:"external_url"`
	StartupTimeout        Value       `json:"startup_timeout_seconds"`
	IdleUnloadTimeout     Value       `json:"idle_unload_seconds"`
	AlwaysOnReconcile     Value       `json:"always_on_reconcile_seconds"`
	Runtime               RuntimeInfo `json:"runtime"`
}

type definition struct {
	env                  string
	defaultValue         string
	kind                 string
	min                  int
	max                  int
	databaseOverridesEnv bool
}

type Service struct {
	db      *sql.DB
	defs    map[string]definition
	runtime RuntimeInfo
}

func New(db *sql.DB, defaults Defaults) *Service {
	return &Service{
		db: db,
		defs: map[string]definition{
			SessionLifetimeSeconds:    {env: "LCM_SESSION_LIFETIME_SECONDS", defaultValue: strconv.FormatInt(int64(defaults.SessionLifetime/time.Second), 10), kind: "int", min: 60, max: 365 * 24 * 3600},
			LoginProtectionEnabled:   {env: "LCM_LOGIN_PROTECTION_ENABLED", defaultValue: "true", kind: "bool"},
			LoginFailureThreshold:    {env: "LCM_LOGIN_FAILURE_THRESHOLD", defaultValue: "5", kind: "int", min: 2, max: 100},
			LoginLockoutSeconds:      {env: "LCM_LOGIN_LOCKOUT_SECONDS", defaultValue: "900", kind: "int", min: 1, max: 24 * 3600},
			TrustedProxies:           {env: "LCM_TRUSTED_PROXIES", defaultValue: "", kind: "string"},
			AllowedOrigins:           {env: "LCM_ALLOWED_ORIGIN", defaultValue: defaults.AllowedOrigins, kind: "string", databaseOverridesEnv: true},
			ExternalURL:              {env: "LCM_EXTERNAL_URL", defaultValue: "", kind: "string"},
			StartupTimeoutSeconds:    {env: "LCM_STARTUP_TIMEOUT_SECONDS", defaultValue: strconv.FormatInt(int64(defaults.StartupTimeout/time.Second), 10), kind: "int", min: 1, max: 3600},
			IdleUnloadSeconds:        {defaultValue: "300", kind: "int", min: 0, max: 7 * 24 * 3600},
			AlwaysOnReconcileSeconds: {env: "LCM_ALWAYS_ON_RECONCILE_SECONDS", defaultValue: strconv.FormatInt(int64(defaults.AlwaysOnReconcile/time.Second), 10), kind: "int", min: 0, max: 3600},
		},
		runtime: RuntimeInfo{DataDir: defaults.DataDir, ModelsDir: defaults.ModelsDir, DatabasePath: defaults.DatabasePath, ListenAddr: defaults.ListenAddr, LlamaServerPath: defaults.LlamaServerPath},
	}
}

func (s *Service) Resolve(ctx context.Context, key string) (Value, error) {
	def, ok := s.defs[key]
	if !ok {
		return Value{}, fmt.Errorf("unknown manager setting %q", key)
	}
	if def.databaseOverridesEnv {
		var stored string
		err := s.db.QueryRowContext(ctx, "SELECT setting_value FROM manager_settings WHERE setting_key=?", key).Scan(&stored)
		if err == nil {
			parsed, parseErr := parse(def, stored)
			if parseErr != nil {
				return Value{}, fmt.Errorf("invalid stored setting %s: %w", key, parseErr)
			}
			return Value{Value: parsed, Source: "database", Editable: true}, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return Value{}, err
		}
		if def.env != "" {
			if value, ok := os.LookupEnv(def.env); ok && strings.TrimSpace(value) != "" {
				parsed, parseErr := parse(def, value)
				if parseErr != nil {
					return Value{}, fmt.Errorf("invalid %s: %w", def.env, parseErr)
				}
				return Value{Value: parsed, Source: "environment", Editable: true}, nil
			}
		}
		parsed, parseErr := parse(def, def.defaultValue)
		if parseErr != nil {
			return Value{}, parseErr
		}
		return Value{Value: parsed, Source: "default", Editable: true}, nil
	}
	if def.env != "" {
		if value, ok := os.LookupEnv(def.env); ok && strings.TrimSpace(value) != "" {
			parsed, err := parse(def, value)
			if err != nil {
				return Value{}, fmt.Errorf("invalid %s: %w", def.env, err)
			}
			return Value{Value: parsed, Source: "environment", Editable: false}, nil
		}
	}
	var stored string
	err := s.db.QueryRowContext(ctx, "SELECT setting_value FROM manager_settings WHERE setting_key=?", key).Scan(&stored)
	if err == nil {
		parsed, parseErr := parse(def, stored)
		if parseErr != nil {
			return Value{}, fmt.Errorf("invalid stored setting %s: %w", key, parseErr)
		}
		return Value{Value: parsed, Source: "database", Editable: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Value{}, err
	}
	parsed, err := parse(def, def.defaultValue)
	if err != nil {
		return Value{}, err
	}
	return Value{Value: parsed, Source: "default", Editable: true}, nil
}

func (s *Service) Set(ctx context.Context, key string, value any) (Value, error) {
	def, ok := s.defs[key]
	if !ok {
		return Value{}, fmt.Errorf("unknown manager setting %q", key)
	}
	if def.env != "" && !def.databaseOverridesEnv {
		if envValue, ok := os.LookupEnv(def.env); ok && strings.TrimSpace(envValue) != "" {
			return Value{}, fmt.Errorf("%s is controlled by environment variable %s", key, def.env)
		}
	}
	serialized := fmt.Sprint(value)
	parsed, err := parse(def, serialized)
	if err != nil {
		return Value{}, err
	}
	serialized = serialize(parsed)
	_, err = s.db.ExecContext(ctx, `INSERT INTO manager_settings(setting_key,setting_value,updated_at) VALUES(?,?,?)
		ON CONFLICT(setting_key) DO UPDATE SET setting_value=excluded.setting_value,updated_at=excluded.updated_at`, key, serialized, time.Now().Unix())
	if err != nil {
		return Value{}, err
	}
	return Value{Value: parsed, Source: "database", Editable: true}, nil
}

func (s *Service) General(ctx context.Context) (General, error) {
	keys := []string{SessionLifetimeSeconds, LoginProtectionEnabled, LoginFailureThreshold, LoginLockoutSeconds, TrustedProxies, AllowedOrigins, ExternalURL, StartupTimeoutSeconds, IdleUnloadSeconds, AlwaysOnReconcileSeconds}
	values := make(map[string]Value, len(keys))
	for _, key := range keys {
		value, err := s.Resolve(ctx, key)
		if err != nil {
			return General{}, err
		}
		values[key] = value
	}
	return General{
		SessionLifetime: values[SessionLifetimeSeconds], LoginProtection: values[LoginProtectionEnabled], LoginFailureThreshold: values[LoginFailureThreshold], LoginLockout: values[LoginLockoutSeconds],
		TrustedProxies: values[TrustedProxies], AllowedOrigins: values[AllowedOrigins], ExternalURL: values[ExternalURL], StartupTimeout: values[StartupTimeoutSeconds], IdleUnloadTimeout: values[IdleUnloadSeconds], AlwaysOnReconcile: values[AlwaysOnReconcileSeconds], Runtime: s.runtime,
	}, nil
}

func (s *Service) String(ctx context.Context, key string) (string, error) {
	value, err := s.Resolve(ctx, key)
	if err != nil {
		return "", err
	}
	result, ok := value.Value.(string)
	if !ok {
		return "", fmt.Errorf("setting %s is not a string", key)
	}
	return result, nil
}

func (s *Service) Int(ctx context.Context, key string) (int, error) {
	value, err := s.Resolve(ctx, key)
	if err != nil {
		return 0, err
	}
	result, ok := value.Value.(int)
	if !ok {
		return 0, fmt.Errorf("setting %s is not an integer", key)
	}
	return result, nil
}

func (s *Service) Bool(ctx context.Context, key string) (bool, error) {
	value, err := s.Resolve(ctx, key)
	if err != nil {
		return false, err
	}
	result, ok := value.Value.(bool)
	if !ok {
		return false, fmt.Errorf("setting %s is not a boolean", key)
	}
	return result, nil
}

func parse(def definition, value string) (any, error) {
	value = strings.TrimSpace(value)
	switch def.kind {
	case "string":
		return value, nil
	case "bool":
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return nil, errors.New("must be true or false")
		}
		return parsed, nil
	case "int":
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return nil, errors.New("must be an integer")
		}
		if parsed < def.min || parsed > def.max {
			return nil, fmt.Errorf("must be between %d and %d", def.min, def.max)
		}
		return parsed, nil
	default:
		return nil, errors.New("unsupported setting type")
	}
}

func serialize(value any) string {
	switch typed := value.(type) {
	case bool:
		return strconv.FormatBool(typed)
	case int:
		return strconv.Itoa(typed)
	default:
		return fmt.Sprint(value)
	}
}
