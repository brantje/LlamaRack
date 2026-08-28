package llamacpp

import (
	"strings"
	"testing"
)

func testProfile() Profile {
	return Profile{Path: "/app/llama-server", Version: "llama.cpp test", Options: []Option{
		{Key: "ctx-size", ValueHint: "N", Kind: "integer"},
		{Key: "temperature", ValueHint: "FLOAT", Kind: "number"},
		{Key: "flash-attn", Kind: "boolean"},
		{Key: "no-flash-attn", Kind: "boolean"},
		{Key: "cache-type-k", ValueHint: "<f16|q8_0>", Kind: "enum", Choices: []string{"f16", "q8_0"}},
		{Key: "chat-template", ValueHint: "STRING", Kind: "string"},
		{Key: "tensor-split", ValueHint: "SPLIT", Kind: "string"},
		{Key: "port", ValueHint: "N", Kind: "integer"},
	}}
}

func TestValidateOptionsCanonicalizesAndValidatesTypes(t *testing.T) {
	got, err := ValidateOptions(testProfile(), map[string]string{
		"--ctx-size": " 8192 ", "temperature": "0.7", "flash-attn": "true",
		"cache-type-k": "q8_0", "chat-template": "chatml", "tensor-split": "3,1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["ctx-size"] != "8192" || got["temperature"] != "0.7" || got["flash-attn"] != "true" || got["cache-type-k"] != "q8_0" || got["tensor-split"] != "3,1" {
		t.Fatalf("validated=%+v", got)
	}
	got, err = ValidateOptions(testProfile(), map[string]string{"flash-attn": "false"})
	if err != nil || got["flash-attn"] != "false" {
		t.Fatalf("explicit false validated=%+v err=%v", got, err)
	}
}

func TestValidateOptionsRejectsUnsupportedReservedAndInvalidValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts map[string]string
		text string
	}{
		{"unsupported", map[string]string{"made-up": "1"}, "unsupported llama.cpp option"},
		{"reserved", map[string]string{"port": "9000"}, "managed by LlamaCPP Manager"},
		{"integer", map[string]string{"ctx-size": "large"}, "integer value"},
		{"number", map[string]string{"temperature": "warm"}, "numeric value"},
		{"boolean", map[string]string{"flash-attn": "yes"}, "true or false"},
		{"enum", map[string]string{"cache-type-k": "q4_0"}, "must be one of"},
		{"string", map[string]string{"chat-template": ""}, "requires STRING"},
		{"empty-key", map[string]string{"---": "x"}, "key is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateOptions(testProfile(), tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.text) {
				t.Fatalf("err=%v want text %q", err, tc.text)
			}
		})
	}
}

func TestValidateOptionsRejectsFalseWithoutInverseFlag(t *testing.T) {
	profile := Profile{Version: "test", Options: []Option{{Key: "embeddings", Kind: "boolean"}}}
	if _, err := ValidateOptions(profile, map[string]string{"embeddings": "false"}); err == nil || !strings.Contains(err.Error(), "inverse flag --no-embeddings is unavailable") {
		t.Fatalf("expected inverse flag error, got %v", err)
	}
}

func TestValidateOptionsRequiresDiscoveredSchema(t *testing.T) {
	if got, err := ValidateOptions(Profile{}, nil); err != nil || len(got) != 0 {
		t.Fatalf("empty options should not require schema: got=%v err=%v", got, err)
	}
	if _, err := ValidateOptions(Profile{}, map[string]string{"ctx-size": "1"}); err == nil || !strings.Contains(err.Error(), "schema is unavailable") {
		t.Fatalf("err=%v", err)
	}
}

func TestValidateOptionValueInfersLegacyKind(t *testing.T) {
	if err := validateOptionValue(Option{Key: "threads", ValueHint: "N"}, "4"); err != nil {
		t.Fatal(err)
	}
	if label := profileLabel(Profile{}); label != "the configured llama-server" {
		t.Fatalf("label=%q", label)
	}
	if label := profileLabel(Profile{Path: "/tmp/server"}); label != "/tmp/server" {
		t.Fatalf("path label=%q", label)
	}
}
