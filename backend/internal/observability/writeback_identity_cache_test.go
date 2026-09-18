package observability

import (
	"context"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/database"
)

func TestWritebackModelIdentityCachesPerInstance(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	store, ok := s.store.(*sqlObservabilityStore)
	if !ok {
		t.Fatalf("unexpected observability store %T", s.store)
	}
	s.startWriteback(ctx, time.Hour)
	if _, err := observabilityTestDB(t, s).ExecContext(ctx, `INSERT INTO models(id,slug,name,gguf_path,total_bytes) VALUES('model-id','model-slug','Original Model','model.gguf',1)`); err != nil {
		t.Fatal(err)
	}
	instanceID := "11111111-1111-4111-8111-111111111111"
	if _, err := observabilityTestDB(t, s).ExecContext(ctx, `INSERT INTO instances(id,slug,model_id,name) VALUES(?,?,?,?)`, instanceID, "public-instance", "model-id", "Instance"); err != nil {
		t.Fatal(err)
	}

	tx, err := database.Begin(ctx, observabilityTestDB(t, s))
	if err != nil {
		t.Fatal(err)
	}
	modelID, modelName, err := store.resolveWritebackModelIdentity(ctx, tx, instanceID, "public-instance")
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if modelID != "model-id" || modelName != "Original Model" {
		t.Fatalf("identity=%q %q", modelID, modelName)
	}
	if _, err := observabilityTestDB(t, s).ExecContext(ctx, `UPDATE models SET name='Renamed Model' WHERE id='model-id'`); err != nil {
		t.Fatal(err)
	}

	tx, err = database.Begin(ctx, observabilityTestDB(t, s))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	modelID, modelName, err = store.resolveWritebackModelIdentity(ctx, tx, instanceID, "public-instance")
	if err != nil {
		t.Fatal(err)
	}
	if modelID != "model-id" || modelName != "Original Model" {
		t.Fatalf("second lookup did not use cached historical identity: %q %q", modelID, modelName)
	}
}
