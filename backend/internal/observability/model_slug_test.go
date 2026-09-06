package observability

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/brantje/llamarack/backend/internal/database"
)

func TestRequestModelIdentityPersistsDurableIDAndHistoricalSlug(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "observability.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := New(db)

	const (
		requestID  = "lr_model_identity"
		instanceID = "8c821aec-1f0d-4b8d-a332-41c582dd2c58"
		modelSlug  = "qwen-coder-32b"
	)
	if err := service.BeginCorrelatedRequest(ctx, requestID, RequestRecord{
		StartedAt: 1, InstanceID: instanceID, Endpoint: "/v1/chat/completions",
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.SetRequestModelSlug(ctx, requestID, modelSlug); err != nil {
		t.Fatal(err)
	}

	identity, err := service.RequestModelIdentity(ctx, requestID)
	if err != nil {
		t.Fatal(err)
	}
	if identity.InstanceID != instanceID || identity.ModelSlug != modelSlug {
		t.Fatalf("identity=%+v", identity)
	}

	// The captured public identity is historical data. A later live-resource
	// rename does not implicitly rewrite this row.
	identityAgain, err := service.RequestModelIdentity(ctx, requestID)
	if err != nil {
		t.Fatal(err)
	}
	if identityAgain != identity {
		t.Fatalf("historical identity changed: before=%+v after=%+v", identity, identityAgain)
	}
}

func TestRequestModelIdentityValidationAndMissingRows(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "observability.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := New(db)

	if err := service.SetRequestModelSlug(ctx, "", "model"); err != nil {
		t.Fatalf("empty request ID should be ignored: %v", err)
	}
	if err := service.SetRequestModelSlug(ctx, "missing", ""); err != nil {
		t.Fatalf("empty model slug should be ignored: %v", err)
	}
	if err := service.SetRequestModelSlug(ctx, "missing", "model"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing request error=%v want sql.ErrNoRows", err)
	}
	if _, err := service.RequestModelIdentity(ctx, "  "); err == nil {
		t.Fatal("expected empty request ID validation error")
	}
	if _, err := service.RequestModelIdentity(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing identity error=%v want sql.ErrNoRows", err)
	}
}
