package llamacpp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

type Option struct {
	Key          string   `json:"key"`
	ValueHint    string   `json:"value_hint,omitempty"`
	Description  string   `json:"description,omitempty"`
	Kind         string   `json:"kind"`
	Choices      []string `json:"choices,omitempty"`
	ManagerOwned bool     `json:"manager_owned,omitempty"`
}

type Profile struct {
	Path                     string   `json:"path"`
	Version                  string   `json:"version,omitempty"`
	Fingerprint              string   `json:"fingerprint"`
	Options                  []Option `json:"options"`
	Devices                  []string `json:"devices,omitempty"`
	DeviceDiscoveryAvailable bool     `json:"device_discovery_available"`
	DeviceDiscoveryError     string   `json:"-"`
}

func (p Profile) Has(key string) bool {
	key = strings.TrimPrefix(strings.TrimSpace(key), "--")
	if key == "" {
		return false
	}
	for _, option := range p.Options {
		if strings.TrimPrefix(strings.TrimSpace(option.Key), "--") == key {
			return true
		}
	}
	return false
}

func Discover(ctx context.Context, path string) (Profile, error) {
	versionOut, _ := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	help, err := exec.CommandContext(ctx, path, "--help").CombinedOutput()
	if err != nil {
		return Profile{}, err
	}
	profile := Profile{Path: path, Version: firstLine(string(versionOut)), Options: parseHelp(string(help))}
	deviceOut, deviceErr := exec.CommandContext(ctx, path, "--list-devices").CombinedOutput()
	if deviceErr == nil {
		profile.Devices, deviceErr = parseListDevices(string(deviceOut))
	}
	if deviceErr == nil {
		profile.DeviceDiscoveryAvailable = true
	} else {
		profile.DeviceDiscoveryError = deviceErr.Error()
	}

	fingerprintInput := strings.TrimSpace(string(versionOut)) + "\n" + string(help) + "\n"
	if profile.DeviceDiscoveryAvailable {
		fingerprintInput += "devices:\n" + strings.Join(profile.Devices, "\n")
	} else {
		fingerprintInput += "devices:unavailable"
	}
	sum := sha256.Sum256([]byte(fingerprintInput))
	profile.Fingerprint = hex.EncodeToString(sum[:])
	return profile, nil
}

func parseListDevices(text string) ([]string, error) {
	lines := strings.Split(text, "\n")
	marker := -1
	for index, raw := range lines {
		if strings.TrimSpace(raw) == "Available devices:" {
			marker = index
			break
		}
	}
	if marker < 0 {
		return nil, errors.New("llama-server --list-devices output missing Available devices marker")
	}
	seen := map[string]bool{}
	var devices []string
	for _, raw := range lines[marker+1:] {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			continue
		}
		device := strings.TrimSpace(line[:colon])
		if device == "" || strings.ContainsAny(device, " \t") || seen[device] {
			continue
		}
		seen[device] = true
		devices = append(devices, device)
	}
	sort.Strings(devices)
	return devices, nil
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

		matches := longFlagRE.FindAllStringSubmatchIndex(spec, -1)
		if len(matches) == 0 {
			continue
		}
		last := matches[len(matches)-1]
		valueHint := strings.TrimSpace(spec[last[1]:])
		valueHint = strings.TrimSpace(strings.TrimLeft(valueHint, ","))
		kind, choices := classifyValueHint(valueHint)

		// llama.cpp commonly documents paired switches on one line, e.g.
		// --kv-offload, --no-kv-offload. Every long flag is a real accepted
		// spelling and must be discovered so explicit false can resolve to the
		// inverse switch.
		for _, match := range matches {
			key := spec[match[2]:match[3]]
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, Option{
				Key: key, ValueHint: valueHint, Description: description,
				Kind: kind, Choices: choices, ManagerOwned: IsManagerOwnedOption(key),
			})
		}
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
