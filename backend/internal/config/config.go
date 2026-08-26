package config

import (
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type Config struct {
	ListenAddr      string
	DataDir         string
	ModelsDir       string
	DatabasePath    string
	LlamaServerPath string
	WorkerHost      string
	WorkerPortStart int
	StartupTimeout  time.Duration
	SessionLifetime time.Duration
	AllowedOrigin   string
}

func Load() Config {
	dataDir := env("LCM_DATA_DIR", "/config")
	workerPort, _ := strconv.Atoi(env("LCM_WORKER_PORT_START", "10000"))
	startupSeconds, _ := strconv.Atoi(env("LCM_STARTUP_TIMEOUT_SECONDS", "180"))
	return Config{
		ListenAddr:      env("LCM_LISTEN_ADDR", ":8000"),
		DataDir:         dataDir,
		ModelsDir:       env("LCM_MODELS_DIR", "/models"),
		DatabasePath:    env("LCM_DATABASE_PATH", filepath.Join(dataDir, "manager.db")),
		LlamaServerPath: env("LCM_LLAMA_SERVER", "llama-server"),
		WorkerHost:      env("LCM_WORKER_HOST", "127.0.0.1"),
		WorkerPortStart: workerPort,
		StartupTimeout:  time.Duration(startupSeconds) * time.Second,
		SessionLifetime: 24 * time.Hour,
		AllowedOrigin:   env("LCM_ALLOWED_ORIGIN", "http://localhost:3000"),
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
