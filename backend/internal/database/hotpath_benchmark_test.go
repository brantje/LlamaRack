package database

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

type persistenceBenchOpen func(context.Context, string) (Store, error)

func openRawPersistenceBench(ctx context.Context, path string) (Store, error) {
	return Open(ctx, path)
}

func openAdapterPersistenceBench(ctx context.Context, path string) (Store, error) {
	return OpenStore(ctx, path)
}

// BenchmarkPersistenceHotPaths compares the current GORM-backed Store adapter
// against the raw database/sql SQLite path that main used before #164. Both
// variants execute the same schema, statements, transaction boundaries and
// result scanning so the benchmark isolates adapter overhead rather than
// application behavior.
func BenchmarkPersistenceHotPaths(b *testing.B) {
	variants := []struct {
		name string
		open persistenceBenchOpen
	}{
		{name: "RawMainEquivalent", open: openRawPersistenceBench},
		{name: "GORMStoreAdapter", open: openAdapterPersistenceBench},
	}
	for _, variant := range variants {
		variant := variant
		b.Run(variant.name, func(b *testing.B) {
			b.Run("InferenceLoggingWriteback", func(b *testing.B) { benchmarkInferenceLoggingWriteback(b, variant.open) })
			b.Run("CounterUpdate", func(b *testing.B) { benchmarkCounterUpdate(b, variant.open) })
			b.Run("HardwareSamplePersistence", func(b *testing.B) { benchmarkHardwareSamplePersistence(b, variant.open) })
			b.Run("APIKeyLastUsed", func(b *testing.B) { benchmarkAPIKeyLastUsed(b, variant.open) })
			b.Run("RuntimeUpsert", func(b *testing.B) { benchmarkRuntimeUpsert(b, variant.open) })
			b.Run("ModelInstanceLists", func(b *testing.B) { benchmarkModelInstanceLists(b, variant.open) })
			b.Run("RequestPagination", func(b *testing.B) { benchmarkRequestPagination(b, variant.open) })
		})
	}
}

func openBenchStore(b *testing.B, open persistenceBenchOpen) Store {
	b.Helper()
	db, err := open(context.Background(), filepath.Join(b.TempDir(), "manager.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = db.Close() })
	return db
}

func benchmarkInferenceLoggingWriteback(b *testing.B, open persistenceBenchOpen) {
	ctx := context.Background()
	db := openBenchStore(b, open)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tx, err := Begin(ctx, db)
		if err != nil {
			b.Fatal(err)
		}
		var id int64
		err = tx.QueryRowContext(ctx, `INSERT INTO inference_requests(
			started_at,finished_at,instance_id,model_slug,endpoint,status_code,result,
			prompt_tokens,generated_tokens,total_tokens
		) VALUES(?,?,?,?,?,?,?,?,?,?) RETURNING id`,
			int64(i+1), int64(i+2), "bench-instance", "bench-model",
			"/v1/chat/completions", 200, "success", 8, 4, 12).Scan(&id)
		if err == nil {
			for _, item := range []struct {
				metric string
				value  float64
			}{
				{"gateway_requests_total", 1},
				{"prompt_tokens_total", 8},
				{"generated_tokens_total", 4},
				{"tokens_total", 12},
			} {
				_, err = tx.ExecContext(ctx, `INSERT INTO observability_counters(
					metric,instance_id,endpoint,status_code,result,streaming,value
				) VALUES(?,?,?,?,?,?,?)
				ON CONFLICT(metric,instance_id,endpoint,status_code,result,streaming)
				DO UPDATE SET value=observability_counters.value+excluded.value`,
					item.metric, "bench-instance", "/v1/chat/completions", 200, "success", 0, item.value)
				if err != nil {
					break
				}
			}
		}
		if err != nil {
			_ = tx.Rollback()
			b.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkCounterUpdate(b *testing.B, open persistenceBenchOpen) {
	ctx := context.Background()
	db := openBenchStore(b, open)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tx, err := Begin(ctx, db)
		if err != nil {
			b.Fatal(err)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO observability_counters(
			metric,instance_id,endpoint,status_code,result,streaming,value
		) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(metric,instance_id,endpoint,status_code,result,streaming)
		DO UPDATE SET value=observability_counters.value+excluded.value`,
			"gateway_requests_total", "bench-instance", "/v1/chat/completions", 200, "success", 0, 1)
		if err != nil {
			_ = tx.Rollback()
			b.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkHardwareSamplePersistence(b *testing.B, open persistenceBenchOpen) {
	ctx := context.Background()
	db := openBenchStore(b, open)
	samples := []struct {
		metric, device, instance string
		value                    float64
	}{
		{"ram_used_bytes", "", "", 8 << 30},
		{"vram_used_bytes", "CUDA0", "", 4 << 30},
		{"gpu_utilization_pct", "CUDA0", "", 77},
		{"instance_vram_used_bytes", "CUDA0", "bench-instance", 2 << 30},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tx, err := Begin(ctx, db)
		if err != nil {
			b.Fatal(err)
		}
		for _, sample := range samples {
			if _, err = tx.ExecContext(ctx, `INSERT INTO hardware_metric_samples(
				collected_at,metric,device_id,instance_id,value
			) VALUES(?,?,?,?,?)`, int64(i+1), sample.metric, sample.device, sample.instance, sample.value); err != nil {
				break
			}
		}
		if err != nil {
			_ = tx.Rollback()
			b.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkAPIKeyLastUsed(b *testing.B, open persistenceBenchOpen) {
	ctx := context.Background()
	db := openBenchStore(b, open)
	if _, err := db.ExecContext(ctx, `INSERT INTO users(id,username,password_hash)
		VALUES(1,'bench-user','hash')`); err != nil {
		b.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO api_keys(
		id,name,prefix,token_hash,key_type,owner_user_id,enabled,instance_ids
	) VALUES('bench-key','Bench','sk-bench','hash-bench','inference',1,1,'[]')`); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.ExecContext(ctx,
			`UPDATE api_keys SET last_used_at=? WHERE id=? AND enabled=1`,
			int64(i+1), "bench-key"); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkRuntimeUpsert(b *testing.B, open persistenceBenchOpen) {
	ctx := context.Background()
	db := openBenchStore(b, open)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO worker_runtime(
			instance_id,generation,pid,start_ticks,port,updated_at
		) VALUES(?,?,?,?,?,unixepoch())
		ON CONFLICT(instance_id) DO UPDATE SET
		generation=excluded.generation,pid=excluded.pid,start_ticks=excluded.start_ticks,
		port=excluded.port,updated_at=excluded.updated_at`,
			"bench-instance", fmt.Sprintf("generation-%d", i), 1234, 5678, 10001); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkModelInstanceLists(b *testing.B, open persistenceBenchOpen) {
	ctx := context.Background()
	db := openBenchStore(b, open)
	for i := 0; i < 100; i++ {
		modelID := fmt.Sprintf("model-%03d", i)
		instanceID := fmt.Sprintf("00000000-0000-4000-8000-%012d", i)
		if _, err := db.ExecContext(ctx, `INSERT INTO models(
			id,slug,name,gguf_path,total_bytes,context_length
		) VALUES(?,?,?,?,?,?)`, modelID, modelID, "Model "+modelID, modelID+".gguf", 1, 4096); err != nil {
			b.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO instances(
			id,slug,model_id,name,enabled,autoload_enabled,always_on,priority,
			eviction_enabled,idle_unload_seconds,max_pending_requests,gpu_mode,request_log_mode
		) VALUES(?,?,?,?,1,1,0,'normal',1,0,0,'auto','metadata')`,
			instanceID, "instance-"+modelID, modelID, "Instance "+modelID); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := db.QueryContext(ctx, `SELECT id,slug,name,gguf_path,total_bytes,quantization,context_length
			FROM models ORDER BY name,id`)
		if err != nil {
			b.Fatal(err)
		}
		modelsSeen := scanModelBenchRows(b, rows)
		rows, err = db.QueryContext(ctx, `SELECT id,slug,model_id,name,enabled,autoload_enabled,
			always_on,priority,eviction_enabled,idle_unload_seconds,max_pending_requests,
			gpu_mode,gpu_devices,tensor_split,request_log_mode
			FROM instances ORDER BY name,id`)
		if err != nil {
			b.Fatal(err)
		}
		instancesSeen := scanInstanceBenchRows(b, rows)
		if modelsSeen != 100 || instancesSeen != 100 {
			b.Fatalf("list counts models=%d instances=%d", modelsSeen, instancesSeen)
		}
	}
}

func scanModelBenchRows(b *testing.B, rows *sql.Rows) int {
	b.Helper()
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id, slug, name, path string
		var total int64
		var quant sql.NullString
		var contextLength int
		if err := rows.Scan(&id, &slug, &name, &path, &total, &quant, &contextLength); err != nil {
			b.Fatal(err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		b.Fatal(err)
	}
	return count
}

func scanInstanceBenchRows(b *testing.B, rows *sql.Rows) int {
	b.Helper()
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id, slug, modelID, name, priority, gpuMode, requestLogMode string
		var enabled, autoload, alwaysOn, eviction, idle, pending int
		var devices, split sql.NullString
		if err := rows.Scan(&id, &slug, &modelID, &name, &enabled, &autoload, &alwaysOn,
			&priority, &eviction, &idle, &pending, &gpuMode, &devices, &split, &requestLogMode); err != nil {
			b.Fatal(err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		b.Fatal(err)
	}
	return count
}

func benchmarkRequestPagination(b *testing.B, open persistenceBenchOpen) {
	ctx := context.Background()
	db := openBenchStore(b, open)
	for i := 0; i < 500; i++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO inference_requests(
			started_at,finished_at,instance_id,model_slug,endpoint,status_code,result
		) VALUES(?,?,?,?,?,?,?)`,
			int64(i+1), int64(i+2), "bench-instance", "bench-model",
			"/v1/chat/completions", 200, "success"); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := db.QueryContext(ctx, `SELECT id,started_at,instance_id,endpoint,status_code,result
			FROM inference_requests ORDER BY started_at DESC,id DESC LIMIT ? OFFSET ?`, 50, 100)
		if err != nil {
			b.Fatal(err)
		}
		count := 0
		for rows.Next() {
			var id, started int64
			var instanceID, endpoint, result string
			var status int
			if err := rows.Scan(&id, &started, &instanceID, &endpoint, &status, &result); err != nil {
				rows.Close()
				b.Fatal(err)
			}
			count++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			b.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			b.Fatal(err)
		}
		if count != 50 {
			b.Fatalf("page rows=%d", count)
		}
	}
}
