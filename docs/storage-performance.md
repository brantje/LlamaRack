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

GitHub Actions CI run `35384253391` on source commit
`f25db474647acce846d0537f07eebb35a7edc09e` ran the benchmark with five
samples per sub-benchmark. The table below reports the median sample.

| Hot path | Raw main-equivalent | GORM store adapter | Median delta | Raw → adapter allocations |
| --- | ---: | ---: | ---: | ---: |
| Inference logging/writeback | 631,628 ns/op | 740,141 ns/op | +17.2% (+108,513 ns) | 80 → 248 allocs/op |
| Counter update | 433,111 ns/op | 479,591 ns/op | +10.7% (+46,480 ns) | 17 → 64 allocs/op |
| Hardware sample persistence | 419,628 ns/op | 463,048 ns/op | +10.3% (+43,420 ns) | 61 → 179 allocs/op |
| API-key last-used update | 398,294 ns/op | 448,378 ns/op | +12.6% (+50,084 ns) | 7 → 26 allocs/op |
| Runtime upsert | 405,570 ns/op | 457,034 ns/op | +12.7% (+51,464 ns) | 13 → 40 allocs/op |
| 100-model/100-instance lists | 501,813 ns/op | 527,195 ns/op | +5.1% (+25,382 ns) | 4,266 → 4,305 allocs/op |
| 50-row request pagination | 287,145 ns/op | 292,937 ns/op | +2.0% (+5,792 ns) | 725 → 749 allocs/op |

The adapter therefore has visible CPU/allocation overhead in this SQLite
microbenchmark, especially for short write operations. The absolute median
latency cost is bounded to about 0.11 ms/op in the measured set, while the
larger list/pagination paths remain within 2–5%. The adapter does not introduce
additional application queries or N+1 loading: both variants execute the same
explicit SQL and transaction boundaries.

For the 1.1 storage foundation this is accepted as implementation overhead
rather than a material user-visible regression. The explicit-SQL adapters are
retained instead of converting these paths into ORM object loading, and the
benchmark remains in CI so a future increase can be compared against this
recorded baseline.

The same CI run reported total backend coverage of exactly `90.0%`, satisfying
the repository threshold.
