package models

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/brantje/llamarack/backend/internal/database"
)

func BenchmarkListModels100(b *testing.B) {
	benchmarkListModels(b, 1)
}

func BenchmarkListModels100FourInstances(b *testing.B) {
	benchmarkListModels(b, 4)
}

func benchmarkListModels(b *testing.B, instancesPerModel int) {
	ctx := context.Background()
	root := b.TempDir()
	db, err := database.Open(ctx, filepath.Join(root, "manager.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = db.Close() })

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		modelID := fmt.Sprintf("model-%03d", i)
		if _, err := tx.ExecContext(ctx, `INSERT INTO models(id,name,gguf_path,total_bytes,quantization,context_length) VALUES(?,?,?,?,?,?)`,
			modelID, fmt.Sprintf("Model %03d", i), fmt.Sprintf("model-%03d-Q4_K_M.gguf", i), int64(4<<30), "Q4_K_M", 131072); err != nil {
			_ = tx.Rollback()
			b.Fatal(err)
		}
		for j := 0; j < instancesPerModel; j++ {
			if _, err := tx.ExecContext(ctx, `INSERT INTO instances(id,model_id,name,enabled,autoload_enabled,always_on,priority,eviction_enabled,idle_unload_seconds,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
				fmt.Sprintf("instance-%03d-%d", i, j), modelID, fmt.Sprintf("Instance %03d-%d", i, j), 1, 1, (i+j)%2, "normal", 1, 300, j+1); err != nil {
				_ = tx.Rollback()
				b.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}

	s := New(db, root)
	if _, err := s.List(ctx); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		models, err := s.List(ctx)
		if err != nil {
			b.Fatal(err)
		}
		if len(models) != 100 {
			b.Fatalf("got %d models, want 100", len(models))
		}
	}
}
