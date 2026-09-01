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
}

func Load() Config {
	dataDir := env("LLAMARACK_DATA_DIR", "/config")
	workerPort, _ := strconv.Atoi(env("LLAMARACK_WORKER_PORT_START", "10000"))
	startupSeconds, _ := strconv.Atoi(env("LLAMARACK_STARTUP_TIMEOUT_SECONDS", "180"))
	alwaysOnSeconds, err := strconv.Atoi(env("LLAMARACK_ALWAYS_ON_RECONCILE_SECONDS", "15"))
	if err != nil || alwaysOnSeconds < 0 {
		alwaysOnSeconds = 15
	}
	return Config{
		ListenAddr:                env("LLAMARACK_LISTEN_ADDR", ":8000"),
		DataDir:                   dataDir,
		ModelsDir:                 env("LLAMARACK_MODELS_DIR", "/models"),
		DatabasePath:              env("LLAMARACK_DATABASE_PATH", filepath.Join(dataDir, "manager.db")),
		LlamaServerPath:           env("LLAMARACK_LLAMA_SERVER", "llama-server"),
		HuggingFaceBaseURL:        env("LLAMARACK_HUGGINGFACE_BASE_URL", "https://huggingface.co"),
		WorkerHost:                env("LLAMARACK_WORKER_HOST", "127.0.0.1"),
		WorkerPortStart:           workerPort,
		StartupTimeout:            time.Duration(startupSeconds) * time.Second,
		SessionLifetime:           24 * time.Hour,
		AllowedOrigin:             env("LLAMARACK_ALLOWED_ORIGIN", "http://localhost:3000"),
		AlwaysOnReconcileInterval: time.Duration(alwaysOnSeconds) * time.Second,
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
