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
	Key         string   `json:"key"`
	ValueHint   string   `json:"value_hint,omitempty"`
	Description string   `json:"description,omitempty"`
	Kind        string   `json:"kind"`
	Choices     []string `json:"choices,omitempty"`
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

var longFlagRE = regexp.MustCompile(`--([a-zA-Z0-9][a-zA-Z0-9_-]*)`)
var columnsRE = regexp.MustCompile(`\s{2,}`)

func parseHelp(text string) []Option {
	var out []Option
	seen := map[string]bool{}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		flagAt := strings.Index(line, "--")
		if flagAt < 0 {
			continue
		}
		prefix := strings.TrimSpace(line[:flagAt])
		if prefix != "" && !strings.HasPrefix(prefix, "-") {
			continue
		}
		rest := line[flagAt:]
		parts := columnsRE.Split(rest, 2)
		spec := strings.TrimSpace(parts[0])
		description := ""
		if len(parts) == 2 {
			description = strings.TrimSpace(parts[1])
		}
		match := longFlagRE.FindStringSubmatch(spec)
		if len(match) != 2 || seen[match[1]] {
			continue
		}
		key := match[1]
		seen[key] = true
		afterFlag := strings.TrimSpace(spec[len(match[0]):])
		afterFlag = strings.TrimSpace(strings.TrimLeft(afterFlag, ","))
		kind, choices := classifyValueHint(afterFlag)
		out = append(out, Option{
			Key: key, ValueHint: afterFlag, Description: description,
			Kind: kind, Choices: choices,
		})
	}
	return out
}

func classifyValueHint(hint string) (string, []string) {
	hint = strings.TrimSpace(hint)
	if hint == "" {
		return "boolean", nil
	}
	unwrapped := strings.Trim(hint, "[]<>")
	if strings.Contains(unwrapped, "|") && !strings.ContainsAny(unwrapped, " \t") {
		parts := strings.Split(unwrapped, "|")
		choices := make([]string, 0, len(parts))
		for _, part := range parts {
			if part = strings.TrimSpace(part); part != "" {
				choices = append(choices, part)
			}
		}
		if len(choices) > 1 {
			return "enum", choices
		}
	}
	lower := strings.ToLower(unwrapped)
	switch {
	case lower == "n", lower == "int", lower == "integer", strings.HasSuffix(lower, "_int"):
		return "integer", nil
	case lower == "f", lower == "float", lower == "number", strings.HasSuffix(lower, "_float"):
		return "number", nil
	case lower == "bool", lower == "boolean":
		return "boolean", nil
	default:
		return "string", nil
	}
}
