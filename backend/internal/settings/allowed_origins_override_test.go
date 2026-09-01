package settings

import (
	"context"
	"testing"
)

func TestAllowedOriginsDatabaseOverrideBeatsEnvironment(t *testing.T) {
	ctx := context.Background()
	s := testSettings(t)
	t.Setenv("LLAMARACK_ALLOWED_ORIGIN", "http://env.example:3000")

	value, err := s.Resolve(ctx, AllowedOrigins)
	if err != nil {
		t.Fatal(err)
	}
	if value.Value != "http://env.example:3000" || value.Source != "environment" || !value.Editable {
		t.Fatalf("environment fallback=%+v", value)
	}

	value, err = s.Set(ctx, AllowedOrigins, "http://override.example:3000")
	if err != nil {
		t.Fatalf("allowed origins override should be writable: %v", err)
	}
	if value.Value != "http://override.example:3000" || value.Source != "database" || !value.Editable {
		t.Fatalf("stored override=%+v", value)
	}

	value, err = s.Resolve(ctx, AllowedOrigins)
	if err != nil {
		t.Fatal(err)
	}
	if value.Value != "http://override.example:3000" || value.Source != "database" || !value.Editable {
		t.Fatalf("database must win over environment: %+v", value)
	}
}
