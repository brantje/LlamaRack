package supervisor

import (
	"os"
	"strconv"
	"strings"
)

// Managed workers are configured by LlamaRack's resolved CLI arguments.
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

func workerEnviron(extra []string) []string {
	override := make(map[string]bool, len(extra))
	for _, entry := range extra {
		key, _, _ := strings.Cut(entry, "=")
		if key != "" {
			override[key] = true
		}
	}
	out := make([]string, 0, len(os.Environ())+len(extra))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if override[key] {
			continue
		}
		out = append(out, entry)
	}
	return append(out, extra...)
}

func identityEnv(installationID, instanceID, generation string, port int) []string {
	if installationID == "" || instanceID == "" || generation == "" {
		return nil
	}
	return []string{
		EnvInstallationID + "=" + installationID,
		EnvInstanceID + "=" + instanceID,
		EnvWorkerGeneration + "=" + generation,
		EnvWorkerPort + "=" + strconv.Itoa(port),
	}
}
