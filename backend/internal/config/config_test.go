package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	for _, key := range []string{
		"LLAMARACK_DATA_DIR", "LLAMARACK_MODELS_DIR", "LLAMARACK_DATABASE_PATH", "LLAMARACK_LISTEN_ADDR",
		"LLAMARACK_LLAMA_SERVER", "LLAMARACK_HUGGINGFACE_BASE_URL", "LLAMARACK_WORKER_HOST", "LLAMARACK_WORKER_PORT_START",
		"LLAMARACK_STARTUP_TIMEOUT_SECONDS", "LLAMARACK_ALLOWED_ORIGIN",
	} {
		t.Setenv(key, "")
	}
	cfg := Load()
	if cfg.ListenAddr != ":8000" || cfg.DataDir != "/config" || cfg.ModelsDir != "/models" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.DatabasePath != filepath.Join("/config", "manager.db") {
		t.Fatalf("database path = %q", cfg.DatabasePath)
	}
	if cfg.LlamaServerPath != "llama-server" || cfg.WorkerHost != "127.0.0.1" || cfg.WorkerPortStart != 10000 {
		t.Fatalf("unexpected worker defaults: %+v", cfg)
	}
	if cfg.HuggingFaceBaseURL != "https://huggingface.co" {
		t.Fatalf("Hugging Face URL = %q", cfg.HuggingFaceBaseURL)
	}
	if cfg.StartupTimeout != 180*time.Second || cfg.SessionLifetime != 24*time.Hour {
		t.Fatalf("unexpected durations: %+v", cfg)
	}
	if cfg.AllowedOrigin != "http://localhost:3000" {
		t.Fatalf("origin = %q", cfg.AllowedOrigin)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("LLAMARACK_DATA_DIR", "/tmp/lcm")
	t.Setenv("LLAMARACK_MODELS_DIR", "/tmp/models")
	t.Setenv("LLAMARACK_DATABASE_PATH", "/tmp/custom.db")
	t.Setenv("LLAMARACK_LISTEN_ADDR", ":9999")
	t.Setenv("LLAMARACK_LLAMA_SERVER", "/bin/fake-llama")
	t.Setenv("LLAMARACK_HUGGINGFACE_BASE_URL", "http://huggingface.test")
	t.Setenv("LLAMARACK_WORKER_HOST", "0.0.0.0")
	t.Setenv("LLAMARACK_WORKER_PORT_START", "12000")
	t.Setenv("LLAMARACK_STARTUP_TIMEOUT_SECONDS", "7")
	t.Setenv("LLAMARACK_ALLOWED_ORIGIN", "http://example.test:3000")

	cfg := Load()
	if cfg.DataDir != "/tmp/lcm" || cfg.ModelsDir != "/tmp/models" || cfg.DatabasePath != "/tmp/custom.db" {
		t.Fatalf("unexpected path overrides: %+v", cfg)
	}
	if cfg.ListenAddr != ":9999" || cfg.LlamaServerPath != "/bin/fake-llama" || cfg.WorkerHost != "0.0.0.0" {
		t.Fatalf("unexpected runtime overrides: %+v", cfg)
	}
	if cfg.HuggingFaceBaseURL != "http://huggingface.test" {
		t.Fatalf("Hugging Face URL = %q", cfg.HuggingFaceBaseURL)
	}
	if cfg.WorkerPortStart != 12000 || cfg.StartupTimeout != 7*time.Second {
		t.Fatalf("unexpected numeric overrides: %+v", cfg)
	}
	if cfg.AllowedOrigin != "http://example.test:3000" {
		t.Fatalf("origin = %q", cfg.AllowedOrigin)
	}
}

func TestLoadIgnoresPreviousEnvPrefix(t *testing.T) {
	t.Setenv("LCM_LISTEN_ADDR", ":9999")
	t.Setenv("LCM_DATA_DIR", "/tmp/lcm")
	t.Setenv("LLAMARACK_LISTEN_ADDR", "")
	t.Setenv("LLAMARACK_DATA_DIR", "")
	cfg := Load()
	if cfg.ListenAddr != ":8000" || cfg.DataDir != "/config" {
		t.Fatalf("previous env prefix must be ignored: %+v", cfg)
	}
}

func TestEnvFallback(t *testing.T) {
	t.Setenv("LLAMARACK_TEST_ENV", "")
	if got := env("LLAMARACK_TEST_ENV", "fallback"); got != "fallback" {
		t.Fatalf("env fallback = %q", got)
	}
	t.Setenv("LLAMARACK_TEST_ENV", "value")
	if got := env("LLAMARACK_TEST_ENV", "fallback"); got != "value" {
		t.Fatalf("env value = %q", got)
	}
}
