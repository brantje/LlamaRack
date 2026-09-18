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
