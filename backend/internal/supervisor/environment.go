package supervisor

import (
	"os"
	"strings"
)

// Managed workers are configured by LlamaCPP Manager's resolved CLI arguments.
// Remove inherited llama.cpp argument environment variables so an external
// LLAMA_ARG_* value cannot silently change worker behavior behind the manager's
// effective configuration.
func init() {
	sanitizeLlamaArgEnvironment()
}

func sanitizeLlamaArgEnvironment() {
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "LLAMA_ARG_") {
			_ = os.Unsetenv(key)
		}
	}
}
