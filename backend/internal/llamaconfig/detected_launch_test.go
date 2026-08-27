package llamaconfig

import (
	"context"
	"errors"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/llamacpp"
)

func TestLaunchOptionsAddsDetectedDefaultsBelowExplicitLayers(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	unregister := RegisterDetectedDefaultsProvider(store.db, func(context.Context, string) (map[string]string, error) {
		return map[string]string{
			"spec-type":        "draft-mtp",
			"spec-draft-n-max": "16",
			"spec-draft-p-min": "0.8",
		}, nil
	})
	defer unregister()

	profile := llamacpp.Profile{Options: []llamacpp.Option{
		{Key: "spec-type"}, {Key: "spec-draft-n-max"}, {Key: "spec-draft-p-min"},
	}}
	launch, effective, err := store.LaunchOptions(ctx, profile, "m1", "i1")
	if err != nil {
		t.Fatal(err)
	}
	if launch["spec-type"] != "draft-mtp" || launch["spec-draft-n-max"] != "16" || launch["spec-draft-p-min"] != "0.8" {
		t.Fatalf("detected launch=%+v", launch)
	}
	if effective.Sources["spec-type"] != "detected" {
		t.Fatalf("detected source=%+v", effective.Sources)
	}

	if err := store.ReplaceGlobal(ctx, map[string]string{"spec-draft-n-max": "8"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO model_options(model_id,option_key,option_value) VALUES('m1','spec-draft-p-min','0.4')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO instance_options(instance_id,option_key,option_value) VALUES('i1','spec-type','none')`); err != nil {
		t.Fatal(err)
	}
	launch, effective, err = store.LaunchOptions(ctx, profile, "m1", "i1")
	if err != nil {
		t.Fatal(err)
	}
	if launch["spec-type"] != "none" || launch["spec-draft-n-max"] != "8" || launch["spec-draft-p-min"] != "0.4" {
		t.Fatalf("explicit precedence=%+v", launch)
	}
	if effective.Sources["spec-type"] != "instance" || effective.Sources["spec-draft-n-max"] != "global" || effective.Sources["spec-draft-p-min"] != "model" {
		t.Fatalf("explicit sources=%+v", effective.Sources)
	}
}

func TestDetectedDefaultsProviderErrorsAndUnregisters(t *testing.T) {
	store := testStore(t)
	if provider := detectedDefaultsProvider(store.db); provider != nil {
		t.Fatal("unexpected provider")
	}
	unregister := RegisterDetectedDefaultsProvider(store.db, func(context.Context, string) (map[string]string, error) {
		return nil, errors.New("detect failed")
	})
	profile := llamacpp.Profile{Options: []llamacpp.Option{{Key: "spec-type"}}}
	if _, _, err := store.LaunchOptions(context.Background(), profile, "m1", "i1"); err == nil {
		t.Fatal("expected provider error")
	}
	unregister()
	if provider := detectedDefaultsProvider(store.db); provider != nil {
		t.Fatal("provider should be unregistered")
	}
	RegisterDetectedDefaultsProvider(nil, nil)()
}
