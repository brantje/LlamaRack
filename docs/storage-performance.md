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


## Recorded CI evidence

GitHub Actions CI run `35399621025` on source commit
`25cb655caf7cf4a8fabfb55abd075890c8c9805c` reran the hot-path benchmark
after the model-import start-claim fix was in place. The persistence benchmark
target itself was unchanged by the later model-import/CI cleanup. The table
below reports the median of five samples from that run.

| Hot path | Raw main-equivalent | GORM store adapter | Raw → adapter allocations |
| --- | ---: | ---: | ---: |
| Inference logging/writeback | 652,297 ns/op, 4,002 B/op | 610,861 ns/op, 4,024 B/op | 80 → 81 allocs/op |
| Counter update | 457,873 ns/op, 944 B/op | 432,748 ns/op, 968 B/op | 17 → 18 allocs/op |
| Hardware sample persistence | 414,347 ns/op, 2,329 B/op | 395,109 ns/op, 2,354 B/op | 61 → 62 allocs/op |
| API-key last-used update | 436,497 ns/op, 204 B/op | 393,055 ns/op, 204 B/op | 7 → 7 allocs/op |
| Runtime upsert | 439,741 ns/op, 452 B/op | 412,372 ns/op, 452 B/op | 13 → 13 allocs/op |
| 100-model/100-instance lists | 492,945 ns/op, 71,808 B/op | 498,650 ns/op, 71,808 B/op | 4,266 → 4,266 allocs/op |
| 50-row request pagination | 283,929 ns/op, 10,488 B/op | 282,753 ns/op, 10,488 B/op | 725 → 725 allocs/op |

The important result is allocation and query-shape stability, not the direction
of small wall-clock differences on a shared CI host. In this run the adapter is
allocation-neutral on four measured paths and adds one allocation on the other
three, with only small byte differences. The raw and adapter variants still
execute the same explicit SQL and transaction boundaries, so there is no
adapter-induced N+1 query pattern or extra per-row ORM loading.

Recent CI samples have moved in both latency directions while retaining this
allocation profile. That makes the earlier claim of substantial adapter
CPU/allocation overhead stale: the current explicit-SQL adapter does not show a
repeatable material regression or allocation explosion in these measured
paths. Continue using multiple samples and avoid treating small `ns/op`
differences as a release threshold.

The same CI run reported total backend coverage of exactly `90.0%`, satisfying
the repository threshold.
