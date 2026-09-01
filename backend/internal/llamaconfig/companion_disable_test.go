package llamaconfig

import (
	"context"
	"testing"

	"github.com/brantje/llamarack/backend/internal/llamacpp"
)

func TestEmptyCompanionOverrideSuppressesInheritedLaunchValue(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	if _, err := store.db.ExecContext(ctx, `INSERT INTO model_options(model_id,option_key,option_value) VALUES('m1','mmproj','/models/projector.gguf'),('m1','spec-draft-model','/models/draft.gguf')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO instance_options(instance_id,option_key,option_value) VALUES('i1','mmproj','')`); err != nil {
		t.Fatal(err)
	}
	profile := llamacpp.Profile{Options: []llamacpp.Option{
		{Key: "ctx-size", Kind: "integer"},
		{Key: "mmproj", Kind: "string", ValueHint: "FNAME"},
		{Key: "spec-draft-model", Kind: "string", ValueHint: "FNAME"},
	}}

	launch, effective, err := store.LaunchOptions(ctx, profile, "m1", "i1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := effective.Values["mmproj"]; ok {
		t.Fatalf("disabled projector remained effective: %+v", effective)
	}
	if effective.Sources["mmproj"] != "instance" || effective.Instance["mmproj"] != "" {
		t.Fatalf("disabled projector tombstone was not retained: %+v", effective)
	}
	if _, ok := launch["mmproj"]; ok {
		t.Fatalf("disabled projector leaked into launch options: %+v", launch)
	}
	if launch["spec-draft-model"] != "/models/draft.gguf" {
		t.Fatalf("unrelated inherited companion was lost: %+v", launch)
	}
}
