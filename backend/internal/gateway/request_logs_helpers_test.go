package gateway

import "testing"

func TestRequestLogMetadataHelperBranches(t *testing.T) {
	valid := "AEE4EF30-0D78-40A5-B71C-EF0D9D04F47F"
	if got, ok := normalizeUUID(valid); !ok || got != testTraceHeader {
		t.Fatalf("normalized=%q ok=%v", got, ok)
	}
	for _, value := range []string{
		"aee4ef300d78-40a5-b71c-ef0d9d04f47f",
		"aee4ef30-0d78-40a5-b71c-ef0d9d04f47g",
		"aee4ef30_0d78-40a5-b71c-ef0d9d04f47f",
	} {
		if _, ok := normalizeUUID(value); ok {
			t.Fatalf("invalid UUID accepted: %q", value)
		}
	}
	generated := newTraceID()
	if normalized, ok := normalizeUUID(generated); !ok || normalized != generated {
		t.Fatalf("generated UUID invalid: %q", generated)
	}
	for input, want := range map[string]string{
		"":          "",
		"unknown":   "",
		"_hidden":   "",
		"not-an-ip": "",
		"[::1]":     "::1",
	} {
		if got := canonicalIP(input); got != want {
			t.Fatalf("canonicalIP(%q)=%q want=%q", input, got, want)
		}
	}
	if got := boundedMetadata("  abc  ", 10); got != "abc" {
		t.Fatalf("metadata=%q", got)
	}
	if got := boundedMetadata("abcdef", 3); got != "abc" {
		t.Fatalf("bounded metadata=%q", got)
	}
	if got := boundedMetadata("abcdef", 0); got != "abcdef" {
		t.Fatalf("unbounded metadata=%q", got)
	}
}
