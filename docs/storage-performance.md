# Storage performance qualification

Issue #164 routes production SQL through a GORM-backed adapter while retaining
explicit SQL in domain adapters. This qualification checks whether that
boundary introduces material overhead on persistence-sensitive operations.

## Method

`backend/internal/database/hotpath_benchmark_test.go` executes the same
database schema, SQL, transaction boundaries, result scanning, and workload
against two SQLite handles:

- **RawMainEquivalent** uses `database.Open`, the raw `*sql.DB` path used on
  `main` before the #164 store refactor.
- **GORMStoreAdapter** uses `database.OpenStore`, the #164 production boundary
  that wraps the already-open pool with GORM.

The benchmark covers inference logging/writeback plus counter updates,
standalone observability counter updates, hardware sample persistence, API-key
`last_used_at`, supervisor runtime upsert, 100-model/100-instance list scans,
and request-log pagination over 500 rows.

Run:

```bash
cd backend
go test ./internal/database \
  -run '^$' \
  -bench '^BenchmarkPersistenceHotPaths$' \
  -benchmem \
  -benchtime=200ms \
  -count=5
```

Use several samples rather than a single run. Compare matching
`RawMainEquivalent` and `GORMStoreAdapter` sub-benchmarks for `ns/op`,
`B/op`, and `allocs/op`. CI records this evidence but deliberately has no
wall-clock pass/fail threshold: host noise makes small latency deltas unsuitable
as a release gate. The correctness suite remains the release gate; this
benchmark is the regression evidence.

No production optimization should be made from this benchmark unless a
repeatable material regression is observed. Query shape remains explicit SQL,
so list/pagination paths issue a fixed query count rather than per-row ORM
loads.
