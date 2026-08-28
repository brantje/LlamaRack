package supervisor

import (
	"os"
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
