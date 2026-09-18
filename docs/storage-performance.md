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

GitHub Actions CI run `35400373639` on final implementation commit
`bcd696991939cfcf048d8ab82bf575cf1bd9b150` ran the hot-path benchmark with
five samples per sub-benchmark after the correctness and qualification changes
were complete. The table below reports the median sample.

| Hot path | Raw main-equivalent | GORM store adapter | Raw → adapter allocations |
| --- | ---: | ---: | ---: |
| Inference logging/writeback | 676,603 ns/op, 4,009 B/op | 644,300 ns/op, 4,020 B/op | 80 → 81 allocs/op |
| Counter update | 433,247 ns/op, 944 B/op | 429,448 ns/op, 968 B/op | 17 → 18 allocs/op |
| Hardware sample persistence | 424,248 ns/op, 2,329 B/op | 428,780 ns/op, 2,351 B/op | 61 → 61 allocs/op |
| API-key last-used update | 399,312 ns/op, 204 B/op | 392,334 ns/op, 204 B/op | 7 → 7 allocs/op |
| Runtime upsert | 395,540 ns/op, 452 B/op | 395,244 ns/op, 453 B/op | 13 → 13 allocs/op |
| 100-model/100-instance lists | 500,037 ns/op, 71,808 B/op | 500,457 ns/op, 71,808 B/op | 4,266 → 4,266 allocs/op |
| 50-row request pagination | 286,119 ns/op, 10,488 B/op | 282,142 ns/op, 10,488 B/op | 725 → 725 allocs/op |

The important result is allocation and query-shape stability, not the direction
of small wall-clock differences on a shared CI host. In this run the adapter is
allocation-neutral on five measured paths and adds one allocation on the other
two, with only small byte differences. The raw and adapter variants still
execute the same explicit SQL and transaction boundaries, so there is no
adapter-induced N+1 query pattern or extra per-row ORM loading.

Recent CI samples have moved in both latency directions while retaining this
allocation profile. The evidence therefore does not show a repeatable material
adapter regression or allocation explosion. Continue using multiple samples and
avoid treating small `ns/op` differences as a release threshold.

The same CI run reported total backend coverage of exactly `90.0%`, satisfying
the repository threshold. Subsequent commits only add qualification-test coverage and refresh these
documentation records; they do not change the measured persistence code.
