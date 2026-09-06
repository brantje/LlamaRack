package models

import (
	"context"
	"strings"
	"testing"
)

func TestModelSlugCollisionsAndOptionFailuresRollbackIdentity(t *testing.T) {
	ctx := context.Background()
	service, dir := testModelService(t)
	firstPath := writeGGUF(t, dir, "first-Q4_K_M.gguf")
	secondPath := writeGGUF(t, dir, "second-Q5_K_M.gguf")
	thirdPath := writeGGUF(t, dir, "third-Q6_K.gguf")

	first, err := service.Create(ctx, CreateModelInput{Name: "First Model", Slug: "first-model", GGUFPath: firstPath})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Create(ctx, CreateModelInput{Name: "Second Model", Slug: "occupied-model", GGUFPath: secondPath})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.Create(ctx, CreateModelInput{Name: "Third Model", Slug: second.Slug, GGUFPath: thirdPath}); err == nil || !strings.Contains(strings.ToLower(err.Error()), "unique") {
		t.Fatalf("expected create slug collision, got %v", err)
	}
	if _, err := service.Update(ctx, first.ID, UpdateModelInput{Name: first.Name, Slug: second.Slug}); err == nil || !strings.Contains(strings.ToLower(err.Error()), "unique") {
		t.Fatalf("expected update slug collision, got %v", err)
	}
	unchanged, err := service.GetByID(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Slug != first.Slug || unchanged.Name != first.Name {
		t.Fatalf("slug collision mutated durable resource: before=%+v after=%+v", first, unchanged)
	}

	if _, err := service.db.ExecContext(ctx, `DROP TABLE model_options`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, first.ID, UpdateModelInput{
		Name: "Should Roll Back", Slug: "rolled-back-slug", Options: map[string]string{"ctx-size": "8192"},
	}); err == nil || !strings.Contains(strings.ToLower(err.Error()), "model_options") {
		t.Fatalf("expected options update failure, got %v", err)
	}
	rolledBack, err := service.GetByID(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.Slug != first.Slug || rolledBack.Name != first.Name {
		t.Fatalf("failed options update mutated model: before=%+v after=%+v", first, rolledBack)
	}

	fourthPath := writeGGUF(t, dir, "fourth-Q8_0.gguf")
	if _, err := service.Create(ctx, CreateModelInput{
		Name: "Create Rollback", Slug: "create-rollback", GGUFPath: fourthPath, Options: map[string]string{"ctx-size": "4096"},
	}); err == nil || !strings.Contains(strings.ToLower(err.Error()), "model_options") {
		t.Fatalf("expected options create failure, got %v", err)
	}
	if _, err := service.GetBySlug(ctx, "create-rollback"); err == nil {
		t.Fatal("failed create must roll back the model row")
	}
}

func TestModelSlugValidationRejectsEmptyExplicitIdentity(t *testing.T) {
	ctx := context.Background()
	service, dir := testModelService(t)
	path := writeGGUF(t, dir, "slug-validation-Q4_K_M.gguf")
	if _, err := service.Create(ctx, CreateModelInput{Name: "Valid Name", Slug: "!!!", GGUFPath: path}); err == nil {
		t.Fatal("expected invalid explicit slug")
	}

	created, err := service.Create(ctx, CreateModelInput{Name: "Valid Name", Slug: "valid-model", GGUFPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, created.ID, UpdateModelInput{Name: created.Name, Slug: "!!!"}); err == nil {
		t.Fatal("expected invalid update slug")
	}
	current, err := service.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Slug != created.Slug {
		t.Fatalf("invalid rename changed slug: before=%q after=%q", created.Slug, current.Slug)
	}
}
