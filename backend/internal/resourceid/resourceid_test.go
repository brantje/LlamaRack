package resourceid

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestSlugify(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		{name: "words", input: "  Qwen Coder 32B  ", want: "qwen-coder-32b"},
		{name: "punctuation", input: "Qwen___Coder///32B", want: "qwen-coder-32b"},
		{name: "unicode", input: "  Mistral Élève 7B  ", want: "mistral-élève-7b"},
		{name: "only separators", input: " --- /// ", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Slugify(tc.input); got != tc.want {
				t.Fatalf("Slugify(%q)=%q want=%q", tc.input, got, tc.want)
			}
		})
	}
}

func TestUUIDGenerationAndDeterminism(t *testing.T) {
	first, err := NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("two generated UUIDs unexpectedly matched: %s", first)
	}
	assertUUID(t, first, '4')
	assertUUID(t, second, '4')

	deterministic := DeterministicUUID("legacy-instance:alpha")
	if deterministic != DeterministicUUID("legacy-instance:alpha") {
		t.Fatal("deterministic UUID changed for the same namespace")
	}
	if deterministic == DeterministicUUID("legacy-instance:beta") {
		t.Fatal("different namespaces produced the same deterministic UUID")
	}
	assertUUID(t, deterministic, '5')
}

func TestInstanceSlugIndex(t *testing.T) {
	const id = "resourceid-test-instance"
	ForgetInstanceSlug(id)
	t.Cleanup(func() { ForgetInstanceSlug(id) })

	if got := InstanceSlug(id); got != "" {
		t.Fatalf("unexpected pre-existing slug %q", got)
	}
	RememberInstanceSlug("", "ignored")
	RememberInstanceSlug(id, "")
	if got := InstanceSlug(id); got != "" {
		t.Fatalf("empty remember mutated index: %q", got)
	}

	RememberInstanceSlug("  "+id+"  ", "  qwen-coder  ")
	if got := InstanceSlug(id); got != "qwen-coder" {
		t.Fatalf("InstanceSlug=%q want qwen-coder", got)
	}
	RememberInstanceSlug(id, "qwen-coder-32b")
	if got := InstanceSlug("  " + id + " "); got != "qwen-coder-32b" {
		t.Fatalf("updated InstanceSlug=%q", got)
	}

	ForgetInstanceSlug("  ")
	ForgetInstanceSlug(id)
	if got := InstanceSlug(id); got != "" {
		t.Fatalf("forgotten slug still present: %q", got)
	}
	if got := InstanceSlug("  "); got != "" {
		t.Fatalf("empty lookup returned %q", got)
	}
}

func assertUUID(t *testing.T, value string, version byte) {
	t.Helper()
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		t.Fatalf("invalid UUID shape %q", value)
	}
	if value[14] != version {
		t.Fatalf("UUID %q version=%c want=%c", value, value[14], version)
	}
	if !strings.ContainsRune("89ab", rune(value[19])) {
		t.Fatalf("UUID %q has invalid RFC 4122 variant", value)
	}
	if _, err := hex.DecodeString(strings.ReplaceAll(value, "-", "")); err != nil {
		t.Fatalf("UUID %q is not hex: %v", value, err)
	}
}
