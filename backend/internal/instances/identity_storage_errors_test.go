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
	changedID := ""
	service.SetOnChange(func(_ context.Context, instanceID string) {
		changes++
		changedID = instanceID
	})
	service.NotifyChange(ctx, created.ID)
	if changes != 1 || changedID != created.ID {
		t.Fatalf("change notifications=%d id=%q want 1/%q", changes, changedID, created.ID)
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
