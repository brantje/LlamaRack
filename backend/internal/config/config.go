package config

import (
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type Config struct {
	ListenAddr                string
	DataDir                   string
	ModelsDir                 string
	DatabasePath              string
	LlamaServerPath           string
	HuggingFaceBaseURL        string
	WorkerHost                string
	WorkerPortStart           int
	StartupTimeout            time.Duration
	SessionLifetime           time.Duration
	AllowedOrigin             string
	AlwaysOnReconcileInterval time.Duration
	IdleUnloadTimeout         time.Duration
}

func Load() Config {
	dataDir := env("LCM_DATA_DIR", "/config")
	workerPort, _ := strconv.Atoi(env("LCM_WORKER_PORT_START", "10000"))
	startupSeconds, _ := strconv.Atoi(env("LCM_STARTUP_TIMEOUT_SECONDS", "180"))
	alwaysOnSeconds, err := strconv.Atoi(env("LCM_ALWAYS_ON_RECONCILE_SECONDS", "15"))
	if err != nil || alwaysOnSeconds < 0 {
		alwaysOnSeconds = 15
	}
	idleSeconds, err := strconv.Atoi(env("LCM_IDLE_UNLOAD_SECONDS", "300"))
	if err != nil || idleSeconds < 0 {
		idleSeconds = 300
	}
	return Config{
		ListenAddr:                env("LCM_LISTEN_ADDR", ":8000"),
		DataDir:                   dataDir,
		ModelsDir:                 env("LCM_MODELS_DIR", "/models"),
		DatabasePath:              env("LCM_DATABASE_PATH", filepath.Join(dataDir, "manager.db")),
		LlamaServerPath:           env("LCM_LLAMA_SERVER", "llama-server"),
		HuggingFaceBaseURL:        env("LCM_HUGGINGFACE_BASE_URL", "https://huggingface.co"),
		WorkerHost:                env("LCM_WORKER_HOST", "127.0.0.1"),
		WorkerPortStart:           workerPort,
		StartupTimeout:            time.Duration(startupSeconds) * time.Second,
		SessionLifetime:           24 * time.Hour,
		AllowedOrigin:             env("LCM_ALLOWED_ORIGIN", "http://localhost:3000"),
		AlwaysOnReconcileInterval: time.Duration(alwaysOnSeconds) * time.Second,
		IdleUnloadTimeout:         time.Duration(idleSeconds) * time.Second,
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
