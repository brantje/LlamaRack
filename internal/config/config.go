package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type Config struct {
	ListenAddr       string
	DataDir          string
	ModelsDir        string
	DatabasePath     string
	LlamaServerPath  string
	WorkerHost       string
	WorkerPortStart  int
	StartupTimeout   time.Duration
	IdleTimeout      time.Duration
	ShutdownTimeout  time.Duration
	SessionLifetime  time.Duration
}

func Load() (Config, error) {
	dataDir := env("LCM_DATA_DIR", "/config")
	modelsDir := env("LCM_MODELS_DIR", "/models")
	portStart, err := envInt("LCM_WORKER_PORT_START", 18080)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		ListenAddr:      env("LCM_LISTEN_ADDR", ":8080"),
		DataDir:         dataDir,
		ModelsDir:       modelsDir,
		DatabasePath:    env("LCM_DATABASE_PATH", filepath.Join(dataDir, "llamacpp-manager.db")),
		LlamaServerPath: env("LCM_LLAMA_SERVER", "llama-server"),
		WorkerHost:      env("LCM_WORKER_HOST", "127.0.0.1"),
		WorkerPortStart: portStart,
		StartupTimeout:  envDuration("LCM_STARTUP_TIMEOUT", 5*time.Minute),
		IdleTimeout:     envDuration("LCM_IDLE_TIMEOUT", 15*time.Minute),
		ShutdownTimeout: envDuration("LCM_SHUTDOWN_TIMEOUT", 30*time.Second),
		SessionLifetime: envDuration("LCM_SESSION_LIFETIME", 24*time.Hour),
	}

	for _, dir := range []string{cfg.DataDir, cfg.ModelsDir} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return Config{}, fmt.Errorf("create %s: %w", dir, err)
		}
	}
	return cfg, nil
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return parsed, nil
}

func envDuration(name string, fallback time.Duration) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}
