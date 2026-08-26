package llamacpp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

type Option struct {
	Key         string   `json:"key"`
	Aliases     []string `json:"aliases,omitempty"`
	ValueHint   string   `json:"value_hint,omitempty"`
	Description string   `json:"description"`
	Type        string   `json:"type"`
	Raw         string   `json:"raw"`
}

type Profile struct {
	Path        string    `json:"path"`
	Version     string    `json:"version"`
	Fingerprint string    `json:"fingerprint"`
	Discovered  time.Time `json:"discovered_at"`
	Options     []Option  `json:"options"`
	HelpText    string    `json:"-"`
}

type Discoverer struct{ path string }

func NewDiscoverer(path string) *Discoverer { return &Discoverer{path: path} }

var optionLine = regexp.MustCompile(`^\s*(?:(-[A-Za-z0-9]),?\s+)?(--[A-Za-z0-9][A-Za-z0-9-]*)(?:[ =]([A-Z0-9_<>{}|.,:/+-]+))?\s{2,}(.*)$`)

func (d *Discoverer) Discover(ctx context.Context) (Profile, error) {
	help, err := run(ctx, d.path, "--help")
	if err != nil {
		return Profile{}, fmt.Errorf("llama-server --help: %w", err)
	}
	version, _ := run(ctx, d.path, "--version")
	version = strings.TrimSpace(version)
	sum := sha256.Sum256([]byte(d.path + "\x00" + version + "\x00" + help))
	return Profile{
		Path: d.path, Version: version, Fingerprint: hex.EncodeToString(sum[:]),
		Discovered: time.Now().UTC(), Options: ParseHelp(help), HelpText: help,
	}, nil
}

func ParseHelp(help string) []Option {
	lines := strings.Split(help, "\n")
	options := make([]Option, 0, 128)
	for _, line := range lines {
		match := optionLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		aliases := []string{}
		if match[1] != "" { aliases = append(aliases, match[1]) }
		valueHint := strings.TrimSpace(match[3])
		typeName := inferType(valueHint, match[4])
		options = append(options, Option{
			Key: strings.TrimPrefix(match[2], "--"), Aliases: aliases,
			ValueHint: valueHint, Description: strings.TrimSpace(match[4]), Type: typeName,
			Raw: strings.TrimSpace(line),
		})
	}
	return options
}

func inferType(hint, description string) string {
	upper := strings.ToUpper(hint)
	lower := strings.ToLower(description)
	if hint == "" {
		return "boolean"
	}
	if strings.Contains(upper, "INT") || strings.Contains(upper, "N") || strings.Contains(upper, "COUNT") {
		return "integer"
	}
	if strings.Contains(upper, "FLOAT") || strings.Contains(upper, "F32") || strings.Contains(upper, "RATIO") {
		return "float"
	}
	if strings.Contains(lower, "comma-separated") || strings.Contains(upper, "LIST") {
		return "list"
	}
	return "string"
}

func BuildArgs(modelPath, host string, port int, options map[string]string) []string {
	args := []string{"--model", modelPath, "--host", host, "--port", fmt.Sprint(port)}
	for key, value := range options {
		flag := "--" + strings.TrimLeft(key, "-")
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true":
			args = append(args, flag)
		case "false", "":
			continue
		default:
			args = append(args, flag, value)
		}
	}
	return args
}

func run(ctx context.Context, path string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
