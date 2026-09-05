package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestResolveInjectedRelease(t *testing.T) {
	got := resolve(injectedMetadata{
		version:         "v1.0.0-rc.1",
		commit:          "abc123",
		buildTime:       "2026-09-04T20:04:23Z",
		channel:         "release",
		variant:         "CUDA",
		llamaCppRelease: "b6124",
		llamaCppBuild:   "b6124",
		llamaCppImage:   "ghcr.io/ggml-org/llama.cpp:server-cuda-b6124",
	}, &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "false"}}})

	if got.Version != "1.0.0-rc.1" || got.Commit != "abc123" || got.BuildTime != "2026-09-04T20:04:23Z" {
		t.Fatalf("unexpected release identity: %+v", got)
	}
	if got.Channel != "release" || got.Variant != "cuda" || got.Dirty {
		t.Fatalf("unexpected release classification: %+v", got)
	}
	if got.LlamaCpp.Release != "b6124" || got.LlamaCpp.Build != "b6124" || got.LlamaCpp.Image == "" {
		t.Fatalf("unexpected llama.cpp identity: %+v", got.LlamaCpp)
	}
}

func TestResolveDevelopmentFallsBackToVCS(t *testing.T) {
	got := resolve(injectedMetadata{}, &debug.BuildInfo{
		Main: debug.Module{Version: "(devel)"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "deadbeef"},
			{Key: "vcs.modified", Value: "true"},
		},
	})

	if got.Version != "development" || got.Channel != "development" {
		t.Fatalf("unexpected development classification: %+v", got)
	}
	if got.Commit != "deadbeef" || !got.Dirty || got.Variant != "unknown" {
		t.Fatalf("unexpected VCS fallback: %+v", got)
	}
}

func TestResolveModuleVersionIsCustom(t *testing.T) {
	got := resolve(injectedMetadata{}, &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}})
	if got.Version != "1.2.3" || got.Channel != "custom" {
		t.Fatalf("unexpected module version fallback: %+v", got)
	}
}

func TestResolveInjectedMetadataOverridesVCS(t *testing.T) {
	got := resolve(injectedMetadata{
		version: "2.0.0",
		commit:  "release-commit",
		channel: "release",
		variant: "cpu",
	}, &debug.BuildInfo{
		Main: debug.Module{Version: "v9.9.9"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "vcs-commit"},
			{Key: "vcs.modified", Value: "false"},
		},
	})

	if got.Version != "2.0.0" || got.Commit != "release-commit" || got.Channel != "release" || got.Variant != "cpu" {
		t.Fatalf("injected metadata did not win: %+v", got)
	}
}
