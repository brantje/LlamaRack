package instances

import (
	"context"
	"testing"
)

func TestIdentityMutationsSurfaceStorageFailures(t *testing.T) {
	ctx := context.Background()
	service, db := testService(t)
	created, err := service.Create(ctx, CreateInput{ModelID: "m1", Name: "Stable Identity", Slug: "stable-identity"})
	if err != nil {
		t.Fatal(err)
	}

	changes := 0
	service.SetOnChange(func() { changes++ })
	service.NotifyChange()
	if changes != 1 {
		t.Fatalf("change notifications=%d want 1", changes)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(ctx, CreateInput{ModelID: "m1", Name: "Unavailable", Slug: "unavailable"}); err == nil {
		t.Fatal("expected create to surface closed database")
	}
	if _, err := service.Update(ctx, created.ID, UpdateInput{Name: created.Name, Slug: created.Slug}); err == nil {
		t.Fatal("expected update to surface closed database")
	}
	if _, err := service.Duplicate(ctx, created.ID); err == nil {
		t.Fatal("expected duplicate to surface closed database")
	}
}
