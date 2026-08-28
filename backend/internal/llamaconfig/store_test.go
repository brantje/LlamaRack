package llamaconfig

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/database"
	"github.com/brantje/llamacpp-manager/backend/internal/llamacpp"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(ctx, `INSERT INTO models(id,name,gguf_path,total_bytes) VALUES('m1','Model','model.gguf',1024)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO instances(id,model_id,name) VALUES('i1','m1','Instance')`); err != nil {
		t.Fatal(err)
	}
	return New(db)
}

func TestEffectiveIncludesManagerContextDefault(t *testing.T) {
	store := testStore(t)
	effective, err := store.Effective(context.Background(), "m1", "i1")
	if err != nil {
		t.Fatal(err)
	}
	if effective.Values["ctx-size"] != DefaultContextSize || effective.Sources["ctx-size"] != "manager-default" {
		t.Fatalf("manager context default missing: %+v", effective)
	}
	if len(effective.Global) != 0 || len(effective.Model) != 0 || len(effective.Instance) != 0 {
		t.Fatalf("manager defaults must not be persisted as scoped overrides: %+v", effective)
	}
}

func TestEffectiveConfigurationLayersAndSources(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	if err := store.ReplaceGlobal(ctx, map[string]string{"threads": "4", "ctx-size": "4096"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO model_options(model_id,option_key,option_value) VALUES('m1','ctx-size','8192'),('m1','flash-attn','true')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO instance_options(instance_id,option_key,option_value) VALUES('i1','threads','8')`); err != nil {
		t.Fatal(err)
	}

	effective, err := store.Effective(ctx, "m1", "i1")
	if err != nil {
		t.Fatal(err)
	}
	if effective.Values["ctx-size"] != "8192" || effective.Sources["ctx-size"] != "model" {
		t.Fatalf("model override not effective: %+v", effective)
	}
	if effective.Values["threads"] != "8" || effective.Sources["threads"] != "instance" {
		t.Fatalf("instance override not effective: %+v", effective)
	}
	if effective.Values["flash-attn"] != "true" || effective.Sources["flash-attn"] != "model" {
		t.Fatalf("model-only value missing: %+v", effective)
	}
}

func TestEffectiveResolvesModelFromInstance(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	if err := store.ReplaceGlobal(ctx, map[string]string{"threads": "4"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO model_options(model_id,option_key,option_value) VALUES('m1','ctx-size','8192')`); err != nil {
		t.Fatal(err)
	}

	effective, err := store.Effective(ctx, "", "i1")
	if err != nil {
		t.Fatal(err)
	}
	if effective.Model["ctx-size"] != "8192" || effective.Values["threads"] != "4" {
		t.Fatalf("unexpected resolved configuration: %+v", effective)
	}
}

func TestLaunchOptionsExcludeUnsupportedRetainedValues(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	if err := store.ReplaceGlobal(ctx, map[string]string{"threads": "4", "removed-option": "legacy"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO model_options(model_id,option_key,option_value) VALUES('m1','ctx-size','8192')`); err != nil {
		t.Fatal(err)
	}
	profile := llamacpp.Profile{Version: "test", Options: []llamacpp.Option{{Key: "threads", Kind: "integer"}, {Key: "ctx-size", Kind: "integer"}}}
	launch, effective, err := store.LaunchOptions(ctx, profile, "m1", "i1")
	if err != nil {
		t.Fatal(err)
	}
	if launch["threads"] != "4" || launch["ctx-size"] != "8192" {
		t.Fatalf("supported values missing: %+v", launch)
	}
	if _, ok := launch["removed-option"]; ok {
		t.Fatalf("unsupported option leaked into launch argv: %+v", launch)
	}
	if effective.Values["removed-option"] != "legacy" {
		t.Fatalf("unsupported option was not retained: %+v", effective)
	}
}

func TestLaunchOptionsUsesManagerContextDefault(t *testing.T) {
	store := testStore(t)
	profile := llamacpp.Profile{Options: []llamacpp.Option{{Key: "ctx-size", Kind: "integer"}}}
	launch, effective, err := store.LaunchOptions(context.Background(), profile, "m1", "i1")
	if err != nil {
		t.Fatal(err)
	}
	if launch["ctx-size"] != "4096" || effective.Sources["ctx-size"] != "manager-default" {
		t.Fatalf("default launch=%+v effective=%+v", launch, effective)
	}
}

func TestLaunchOptionsConvertsExplicitFalseToInverseFlag(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	if _, err := store.db.ExecContext(ctx, `INSERT INTO instance_options(instance_id,option_key,option_value) VALUES('i1','flash-attn','false')`); err != nil {
		t.Fatal(err)
	}
	profile := llamacpp.Profile{Version: "test", Options: []llamacpp.Option{
		{Key: "ctx-size", Kind: "integer"},
		{Key: "flash-attn", Kind: "boolean"},
		{Key: "no-flash-attn", Kind: "boolean"},
	}}
	launch, effective, err := store.LaunchOptions(ctx, profile, "m1", "i1")
	if err != nil {
		t.Fatal(err)
	}
	if launch["no-flash-attn"] != "true" {
		t.Fatalf("false override did not use inverse flag: %+v", launch)
	}
	if _, ok := launch["flash-attn"]; ok {
		t.Fatalf("positive flag leaked for false override: %+v", launch)
	}
	if effective.Values["flash-attn"] != "false" || effective.Sources["flash-attn"] != "instance" {
		t.Fatalf("canonical effective value changed: %+v", effective)
	}
}

func TestLaunchOptionsRejectsUnrepresentableFalse(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	if _, err := store.db.ExecContext(ctx, `INSERT INTO instance_options(instance_id,option_key,option_value) VALUES('i1','embeddings','false')`); err != nil {
		t.Fatal(err)
	}
	profile := llamacpp.Profile{Version: "test", Options: []llamacpp.Option{
		{Key: "ctx-size", Kind: "integer"},
		{Key: "embeddings", Kind: "boolean"},
	}}
	if _, _, err := store.LaunchOptions(ctx, profile, "m1", "i1"); err == nil || !strings.Contains(err.Error(), "cannot express explicit false") {
		t.Fatalf("expected explicit false error, got %v", err)
	}
}

func TestLaunchResolverHelperBranches(t *testing.T) {
	store := testStore(t)
	launch, effective, err := store.LaunchOptions(context.Background(), llamacpp.Profile{}, "m1", "i1")
	if err != nil || len(launch) != 0 || effective.Values["ctx-size"] != DefaultContextSize {
		t.Fatalf("empty profile launch=%+v effective=%+v err=%v", launch, effective, err)
	}

	if !isBooleanOption(llamacpp.Option{ValueHint: ""}) || isBooleanOption(llamacpp.Option{ValueHint: "N"}) {
		t.Fatal("legacy boolean option classification mismatch")
	}
	if got := inverseBooleanKey("no-mmap"); got != "mmap" {
		t.Fatalf("inverse no-*=%q", got)
	}
	if got := launchProfileLabel(llamacpp.Profile{Path: "/tmp/llama-server"}); got != "/tmp/llama-server" {
		t.Fatalf("path label=%q", got)
	}
	if got := launchProfileLabel(llamacpp.Profile{}); got != "llama-server" {
		t.Fatalf("default label=%q", got)
	}
}

func TestReplaceGlobalReplacesPriorSet(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	if err := store.ReplaceGlobal(ctx, map[string]string{"threads": "4", "ctx-size": "4096"}); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceGlobal(ctx, map[string]string{"threads": "6"}); err != nil {
		t.Fatal(err)
	}
	global, err := store.Global(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(global) != 1 || global["threads"] != "6" {
		t.Fatalf("global replacement failed: %+v", global)
	}
}
