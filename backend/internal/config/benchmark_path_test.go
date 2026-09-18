package config

import "testing"

func TestLlamaBenchDefaultFollowsServerDirectory(t *testing.T) {
	t.Setenv("LLAMARACK_LLAMA_SERVER", "/app/llama-server")
	t.Setenv("LLAMARACK_LLAMA_BENCH", "")
	if got := Load().LlamaBenchPath; got != "/app/llama-bench" {
		t.Fatalf("LlamaBenchPath=%q want /app/llama-bench", got)
	}
}

func TestLlamaBenchPathOverride(t *testing.T) {
	t.Setenv("LLAMARACK_LLAMA_SERVER", "/app/llama-server")
	t.Setenv("LLAMARACK_LLAMA_BENCH", "/custom/llama-bench")
	if got := Load().LlamaBenchPath; got != "/custom/llama-bench" {
		t.Fatalf("LlamaBenchPath=%q", got)
	}
}

func TestBareLlamaServerDefaultsBareLlamaBench(t *testing.T) {
	t.Setenv("LLAMARACK_LLAMA_SERVER", "llama-server")
	t.Setenv("LLAMARACK_LLAMA_BENCH", "")
	if got := Load().LlamaBenchPath; got != "llama-bench" {
		t.Fatalf("LlamaBenchPath=%q want llama-bench", got)
	}
}
