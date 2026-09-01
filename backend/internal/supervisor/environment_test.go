package supervisor

import (
	"os"
	"strings"
	"testing"
)

func TestSanitizeLlamaArgEnvironment(t *testing.T) {
	if err := os.Setenv("LLAMA_ARG_CTX_SIZE", "131072"); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("LLAMA_ARG_FLASH_ATTN", "1"); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("LCM_TEST_KEEP", "yes"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Unsetenv("LLAMA_ARG_CTX_SIZE")
		_ = os.Unsetenv("LLAMA_ARG_FLASH_ATTN")
		_ = os.Unsetenv("LCM_TEST_KEEP")
	})

	sanitizeLlamaArgEnvironment()

	if _, ok := os.LookupEnv("LLAMA_ARG_CTX_SIZE"); ok {
		t.Fatal("LLAMA_ARG_CTX_SIZE should be removed")
	}
	if _, ok := os.LookupEnv("LLAMA_ARG_FLASH_ATTN"); ok {
		t.Fatal("LLAMA_ARG_FLASH_ATTN should be removed")
	}
	if got := os.Getenv("LCM_TEST_KEEP"); got != "yes" {
		t.Fatalf("unrelated environment changed: %q", got)
	}
}

func TestWorkerEnvironOverridesVisibleDevices(t *testing.T) {
	t.Setenv("CUDA_VISIBLE_DEVICES", "all")
	t.Setenv("LCM_TEST_KEEP", "yes")
	got := workerEnviron([]string{"CUDA_VISIBLE_DEVICES=1"})
	found := false
	keep := false
	for _, entry := range got {
		key, value, _ := strings.Cut(entry, "=")
		switch key {
		case "CUDA_VISIBLE_DEVICES":
			if found {
				t.Fatal("CUDA_VISIBLE_DEVICES duplicated")
			}
			found = true
			if value != "1" {
				t.Fatalf("CUDA_VISIBLE_DEVICES=%q want 1", value)
			}
		case "LCM_TEST_KEEP":
			keep = value == "yes"
		}
	}
	if !found || !keep {
		t.Fatalf("workerEnviron missing override or keep flag: %v", got)
	}
}
