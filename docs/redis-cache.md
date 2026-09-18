# Optional Redis cache

Redis is an optional performance cache in LlamaRack 1.1. It is never an authoritative datastore and is not required when SQLite or PostgreSQL is used.

The first production cache namespace is Hugging Face derived GGUF metadata. Those values are recomputable from the pinned repository revision and artifact, so losing Redis does not lose durable state.

## Configuration

Configure Redis with either:

```text
LLAMARACK_REDIS_URL=redis://redis:6379/0
```

or the conventional fallback:

```text
REDIS_URL=redis://redis:6379/0
```

`LLAMARACK_REDIS_URL` takes precedence. `rediss://` URLs are supported by the Redis client for TLS deployments.

Redis URLs are secrets when they contain credentials. LlamaRack does not include the configured URL in cache keys and does not log it on parse failures.

If Redis is not configured, LlamaRack uses its normal in-process memory cache. If Redis is configured but unavailable during startup, LlamaRack logs the cache failure and continues with the in-process cache. Runtime cache read/write failures are treated as misses or ignored writes and the authoritative source is used.

## Docker Compose example

The repository includes `docker-compose.redis.yml` as an optional override:

```bash
docker compose -f docker-compose.yml -f docker-compose.redis.yml up -d
```

The example intentionally disables Redis persistence because cached values are disposable. Do not use this Redis instance as a replacement for SQLite or PostgreSQL.

## What must not be cached in Redis

Redis is not used for:

- management users, passwords, sessions, CSRF state, API-key authorization or revocation;
- model or Instance configuration;
- scheduler reservations or admission correctness;
- worker ownership/lifecycle state;
- download correctness state;
- inference request durability or Responses API persistence;
- settings or encrypted provider secrets.

Those remain owned by the authoritative database or by the existing process-local lifecycle structures.

## Cache behavior

Hugging Face derived-metadata keys are versioned and hash the base URL, repository identity, immutable revision, and artifact path. Raw repository names, artifact paths, credentials, and bearer tokens are not placed in Redis keys.

Cached values use explicit JSON serialization and a seven-day TTL. LlamaRack keeps a first-level in-memory cache in front of Redis. Redis operations have bounded connection/read/write and context timeouts, with retries disabled, so a slow cache does not become an unbounded request dependency.

Malformed cached values are ignored and removed best-effort; the authoritative Hugging Face metadata path refills the cache.

## Performance qualification

CI collects benchmark evidence for the first production namespace in four modes: process-local warm L1, a synthetic uncached origin fetch, warm Redis after a fresh client, and concurrent warm Redis reads. The synthetic origin benchmark deliberately includes a documented 5 ms network floor; it is a controlled comparison and is **not** measured Hugging Face network latency or a pass/fail performance threshold.

The L1 memory cache remains first in the lookup chain and therefore stays the steady-state fast path. Redis is useful as the restart/shared-process L2: the integration test creates a fresh Hugging Face client and proves a warm Redis entry avoids another origin request entirely. A separate cold-concurrency test starts 16 identical requests together and requires local singleflight coalescing to reduce them to one authoritative origin fetch. Redis is never used for distributed locking or correctness.

## Metrics

The normal `/metrics` endpoint exposes cache counters by a low-cardinality `namespace` and `backend` label:

- `llamarack_cache_hits_total`
- `llamarack_cache_misses_total`
- `llamarack_cache_errors_total`
- `llamarack_cache_writes_total`
- `llamarack_cache_deletes_total`
- `llamarack_cache_origin_fetches_avoided_total`
- `llamarack_cache_operation_duration_seconds_total`

Cache keys and model/repository identifiers are deliberately not metrics labels.


## Real-provider qualification

The deterministic cache benchmarks above intentionally add a synthetic 5 ms
origin floor. They are useful for stable CI comparisons, but they do not by
themselves prove that Redis helps the intended shared/restart workload.

`TestLiveRedisDerivedMetadataQualification` is the real-provider qualification.
It uses the public `Qwen/Qwen2.5-0.5B-Instruct-GGUF` repository, resolves its
current immutable revision, and measures the actual bounded GGUF range-read path
used by `DerivedMetadata`. The test records:

- a cold authoritative lookup with Redis and L1 cleared;
- a warm L1 lookup on the same client;
- a warm Redis lookup from a fresh client with an empty L1, representing a
  restart/process-equivalent local cache reset;
- 16 concurrent requests from another fresh client against the warm Redis L2.

The HTTP transport counts actual Range requests and response bytes consumed.
Warm L1/L2 phases are required to perform zero origin range traffic. Latency is
logged as evidence rather than enforced with a wall-clock threshold.

Run it with a disposable Redis:

```bash
cd backend
LLAMARACK_TEST_REDIS_URL=redis://127.0.0.1:6379/0 \
LLAMARACK_LIVE_HF_QUALIFICATION=1 \
go test ./internal/huggingface \
  -run '^TestLiveRedisDerivedMetadataQualification$' \
  -v -count=1
```

Ordinary correctness CI deliberately does **not** run the live provider test:
Hugging Face availability must not gate unrelated pull requests. Instead,
`.github/workflows/redis-release-qualification.yml` is the release gate for
#162. Run it manually before treating #162 as releasable.

A valid release qualification result is:

1. the dedicated workflow completes successfully against the commit intended
   for release;
2. the cold authoritative phase performs at least one measured range request;
3. warm L1 and a fresh-client warm Redis L2 perform zero Hugging Face range
   requests and transfer zero origin bytes;
4. the fresh-client Redis L2 lookup is at least **5x faster** than the cold
   authoritative lookup in that same run;
5. the uploaded `redis-live-qualification-<sha>` artifact is retained with the
   exact test log and measurements.

Treat a successful run as current release evidence for 30 days. If the release
commit is newer than the recorded qualification or the evidence is older than
30 days, rerun the workflow. Provider/network failure produces a failed release
qualification, but it does not make ordinary correctness CI fail.

The deterministic 5 ms origin benchmark remains useful for repeatable
microbenchmark comparisons only; it is not evidence of real Hugging Face
network benefit.


### Recorded CI evidence

GitHub Actions CI run `35400373639` on final implementation commit
`bcd696991939cfcf048d8ab82bf575cf1bd9b150` reran the deterministic Redis
benchmarks after the correctness and qualification changes were complete.
Median values from three samples were:

| Mode | Median latency | Allocations |
| --- | ---: | ---: |
| Warm in-process L1 | 1,703 ns/op | 13 allocs/op |
| Synthetic authoritative origin | 5,300,530 ns/op | 156 allocs/op |
| Warm Redis from an empty L1 | 86,827 ns/op | 25 allocs/op |
| Warm Redis parallel | 27,143 ns/op | 25 allocs/op |

These measurements are deterministic CI evidence only. They continue to show
the intended hierarchy—process-local L1 is the fastest path and Redis can serve
a fresh client without the synthetic origin delay—but the exact wall-clock
numbers vary materially between CI hosts/runs. The synthetic 5 ms origin floor
must not be presented as real Hugging Face network performance or as the #162
release qualification.

For historical context, ordinary CI run `35384253391` on commit
`f25db474647acce846d0537f07eebb35a7edc09e` still contained the live-provider
test and measured:

- cold authoritative lookup: **553.017 ms**, **2** range requests,
  **5,937,634 bytes** transferred;
- warm L1 lookup: **43.115 µs**, **0** origin range requests;
- fresh-client warm Redis lookup: **264.445 µs**, **0** origin range requests;
- 16 concurrent warm-Redis requests: **17.791 ms total**, **0** origin range
  requests.

That live result is retained as historical evidence only. It predates the
current release candidate and therefore does **not** qualify a newer commit
under the validity policy above. Current #162 release evidence must come from a
successful manual `.github/workflows/redis-release-qualification.yml` run
against the commit intended for release, with its uploaded
`redis-live-qualification-<sha>` artifact.

Subsequent commits only add qualification-test coverage and refresh these
documentation records; they do not change the measured cache implementation.
