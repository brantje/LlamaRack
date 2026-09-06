package observability

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/database"
)

func TestSetRequestModelSlugSurfacesSchemaAndWriteFailures(t *testing.T) {
	ctx := context.Background()

	if err := New(nil).SetRequestModelSlug(ctx, "request-id", "public-slug"); err == nil || !strings.Contains(err.Error(), "database unavailable") {
		t.Fatalf("nil database error=%v", err)
	}

	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "observability.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := New(db)
	if _, err := db.ExecContext(ctx, `DROP TABLE inference_requests`); err != nil {
		t.Fatal(err)
	}
	if err := service.SetRequestModelSlug(ctx, "request-id", "public-slug"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "inference_requests") {
		t.Fatalf("write failure=%v", err)
	}
}
