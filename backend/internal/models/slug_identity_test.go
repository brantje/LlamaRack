package models

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestModelSlugLookupAndRenamePreserveDurableID(t *testing.T) {
	ctx := context.Background()
	service, dir := testModelService(t)
	path := writeGGUF(t, dir, "slug-Q4_K_M.gguf")

	created, err := service.Create(ctx, CreateModelInput{
		Name: "Qwen Coder", Slug: "Qwen Coder Public", GGUFPath: path,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Slug != "qwen-coder-public" {
		t.Fatalf("created slug=%q", created.Slug)
	}

	bySlug, err := service.GetBySlug(ctx, "  QWEN CODER PUBLIC  ")
	if err != nil {
		t.Fatal(err)
	}
	if bySlug.ID != created.ID || bySlug.Slug != created.Slug {
		t.Fatalf("slug lookup=%+v created=%+v", bySlug, created)
	}

	renamed, err := service.Update(ctx, created.ID, UpdateModelInput{
		Name: "Qwen Coder", Slug: "Qwen Coder Management", ContextLength: created.ContextLength,
	})
	if err != nil {
		t.Fatal(err)
	}
	if renamed.ID != created.ID || renamed.Slug != "qwen-coder-management" {
		t.Fatalf("slug rename changed durable identity: created=%+v renamed=%+v", created, renamed)
	}
	if _, err := service.GetBySlug(ctx, created.Slug); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old slug lookup error=%v want sql.ErrNoRows", err)
	}
	current, err := service.GetBySlug(ctx, renamed.Slug)
	if err != nil || current.ID != created.ID {
		t.Fatalf("new slug lookup=%+v err=%v", current, err)
	}
}
