package llamacpp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os/exec"
	"regexp"
	"strings"
)

type Option struct {
	Key         string `json:"key"`
	ValueHint   string `json:"value_hint,omitempty"`
	Description string `json:"description,omitempty"`
}

type Profile struct {
	Path        string   `json:"path"`
	Version     string   `json:"version,omitempty"`
	Fingerprint string   `json:"fingerprint"`
	Options     []Option `json:"options"`
}

func Discover(ctx context.Context, path string) (Profile, error) {
	versionOut, _ := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	help, err := exec.CommandContext(ctx, path, "--help").CombinedOutput()
	if err != nil {
		return Profile{}, err
	}
	sum := sha256.Sum256(append([]byte(strings.TrimSpace(string(versionOut))+"\n"), help...))
	return Profile{Path: path, Version: firstLine(string(versionOut)), Fingerprint: hex.EncodeToString(sum[:]), Options: parseHelp(string(help))}, nil
}

func firstLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[0])
}

var flagRE = regexp.MustCompile(`^\s*(--[a-zA-Z0-9][a-zA-Z0-9_-]*)(?:\s+([^\s].*?))?\s{2,}(.+)$`)

func parseHelp(text string) []Option {
	var out []Option
	for _, line := range strings.Split(text, "\n") {
		m := flagRE.FindStringSubmatch(line)
		if len(m) == 4 {
			out = append(out, Option{Key: strings.TrimPrefix(m[1], "--"), ValueHint: strings.TrimSpace(m[2]), Description: strings.TrimSpace(m[3])})
		}
	}
	return out
}
