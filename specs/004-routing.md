# 004 — Request Routing

Status: Draft

Related issue: #1

## 1. Purpose

This specification defines how inference requests arriving at `/v1/*` resolve the OpenAI `model` field to one exact configured Instance and proxy traffic to that Instance's private `llama-server` worker.

Routing is intentionally simple in v1: **the client-selected model value is the Instance slug**.

## 2. Identity contract

Each Instance has three distinct identities:

- `id` — immutable UUID used for durable ownership, foreign keys, API-key scopes, runtime state and history correlation;
- `slug` — unique mutable public identifier used by OpenAI-compatible clients and management/UI routes;
- `name` — mutable display label.

On create, `slug` defaults from `name`. Name changes do not implicitly change the slug later.

Example:

```text
Instance name: Qwen Coding 32B
instance.slug: qwen-coding-32b
instance.id:   550e8400-e29b-41d4-a716-446655440000
```

Client request:

```json
{"model":"qwen-coding-32b"}
```

At the gateway boundary the manager resolves `slug -> Instance`, then immediately uses the immutable Instance ID for authorization, lifecycle, scheduling, runtime ownership and persistence.

## 3. Goals

Routing must provide:

- one stable manager endpoint regardless of worker ports;
- exact Instance resolution from the OpenAI `model` slug;
- transparent autoload of that exact Instance when permitted;
- streaming proxy support;
- request accounting;
- client cancellation propagation;
- safe handling of Instance failure;
- OpenAI SDK and LiteLLM compatibility;
- rename-safe internal ownership and API-key scopes.

## 4. Non-goals for v1

Routing does not:

- choose among sibling Instances of the same Model;
- load-balance between Instances;
- use least-active/round-robin/fixed/load-aware Model routing policies;
- inspect prompt content to choose another target;
- fallback to another Instance or Model;
- autoscale Instance count;
- route across remote hosts;
- proxy to external inference providers.

Those behaviors require a future explicit routing layer and must not be implied by Model/Instance relationships.

## 5. Instance resolution

For every inference endpoint that includes `model`:

1. authenticate a typed API key (`sk-`); management keys receive 403; JWTs are not inference credentials;
2. parse the requested model slug;
3. resolve the exact Instance by `instance.slug`;
4. enforce an inference-key allowlist against the resolved immutable `instance.id`;
5. capture the exact requested slug as request-history `model_slug`;
6. validate that the referenced Model/artifact/configuration can serve the requested endpoint where known;
7. evaluate Instance availability by immutable ID;
8. proxy only to that Instance's worker.

If no matching Instance slug exists, return model-not-found.

A matching registered Model name or Model slug does not count as an inference target.

## 6. `/v1/models`

The gateway generates `/v1/models` from configured addressable Instances.

Inference keys with a non-empty Instance-ID allowlist see only those still-existing enabled Instances. An empty allowlist means all enabled Instances. If every allowlisted ID is missing, the gateway returns 403. Full Access keys are not allowlisted.

Requirements:

- include configured Instances that are valid/addressable even when currently stopped;
- return `instance.slug` as the standard model object's `id`;
- authorize/filter by immutable Instance ID;
- do not expose registered Model IDs, Instance UUIDs, GGUF paths, PIDs or private ports as OpenAI model IDs;
- runtime state remains management-plane information.

Registered Models are listed through `/api/v1/models` and the Models UI, not as inferable `/v1/models` entries unless an Instance exists.

### Query-parameter Instance resolution

Some llama.cpp extensions do not include an OpenAI JSON `model` field. For those routes, resolve the Instance from a required `model` query parameter whose value is the exact `instance.slug`.

Current routes:

- `GET /v1/slots?model=<instance.slug>`
- `POST /v1/slots/{slot_id}?model=<instance.slug>&action=save|restore|erase`

These routes require a **READY** worker, do not autoload, and do not reserve pending-admission capacity. Rewrite `/v1/slots` to worker `/slots` before proxying and drop `model` from the forwarded query.

## 7. Request pipeline

```text
HTTP /v1 request
      |
      v
Authenticate API key (`sk-`; reject management keys)
      |
      v
Read model=<instance.slug>
      |
      v
Resolve slug -> immutable Instance ID
      |
      v
Apply ID-based inference allowlist
      |
      +-- READY ---------------------> reserve/account -> proxy
      |
      +-- QUEUED/STARTING/LOADING --> join shared ID-keyed wait
      |
      +-- stopped
             |
             +-- autoload=true  --> lifecycle start exact Instance ID -> wait -> proxy
             |
             +-- autoload=false --> model-unavailable error
```

After autoload, re-check that the same immutable Instance is READY before dispatch.

## 8. No sibling substitution

Suppose:

```text
Model: Qwen 32B
  Instance A slug: qwen-fast
  Instance B slug: qwen-large-context
```

A request for:

```json
{"model":"qwen-fast"}
```

may only use the Instance whose current slug is `qwen-fast`.

If it is unavailable and cannot autoload, return an availability error. Do not silently use `qwen-large-context`.

## 9. Request accounting

Distinguish pending waiters from in-flight proxy work:

- a request is **pending** after it is admitted to wait for drain/startup and before it has acquired a worker endpoint;
- it becomes **active** exactly once when `Acquire()` is about to return that endpoint and the gateway is about to proxy;
- streaming requests remain **active** until proxy completion or disconnect.

All accounting keys use immutable Instance IDs, not slugs.

Pending admission is bounded:

- manager settings `max_pending_requests_per_instance` (default 32) and `max_pending_requests_global` (default 128);
- `0` on either manager setting means unlimited for that bound;
- Instance `max_pending_requests` of `0` inherits the manager per-Instance default; a positive value overrides that default only;
- the manager-wide global ceiling still applies even when an Instance override is higher.

When a bound is exceeded, reject immediately with HTTP 503 `server_error` / `overloaded`. Do not start another worker. Already admitted waiters continue to wait and remain context-cancellable; cancellation and startup failure must release pending accounting.

## 10. Autoload integration

The gateway never spawns a process directly. It asks lifecycle for availability of the resolved immutable Instance ID.

Possible outcomes:

- already READY;
- existing startup joined;
- startup initiated;
- pending-request admission limit exceeded;
- autoload disabled;
- startup timeout;
- insufficient resources;
- invalid configuration;
- worker startup failure;
- client cancellation/deadline.

Concurrent callers for the same Instance ID share the lifecycle startup operation even if a later management action changes that Instance's slug.

## 11. Load waiters

Each waiter retains independent cancellation/deadline.

Client disconnect removes that waiter but does not cancel startup needed by other waiters, Always-On policy, or another explicit lifecycle operation.

## 12. Startup deadline

Effective wait deadline is the earliest applicable bound among the client/request deadline, Instance startup timeout, and manager hard upper bound if configured.

## 13. Proxying behavior

The gateway is an HTTP-aware reverse proxy.

It may:

- rewrite internal worker URL/port;
- strip manager-only/hop-by-hop headers;
- inject manager-owned worker auth if used;
- preserve supported OpenAI fields;
- stream bytes promptly;
- record metrics.

Managed workers receive the current `instance.slug` as `llama-server --alias`, while process/runtime ownership remains the immutable Instance ID. External responses preserve the client-facing slug where a model ID is returned. Private worker addresses must never leak.

## 14. Streaming

Requirements:

- incremental forwarding;
- no full-response buffering;
- prompt flushing of SSE/data chunks;
- client cancellation propagated upstream;
- active accounting held until stream completion;
- never replay/retry after response bytes have been sent.

## 15. Retry policy

Because v1 targets one exact Instance, retries do not switch to sibling Instances. Before response bytes begin, the manager may retry a transient connection/setup operation against the **same immutable Instance ID** only when safe and bounded.

## 16. Instance failure during dispatch

If the target Instance leaves READY before dispatch, re-evaluate that same immutable Instance. Do not choose a sibling Instance.

## 17. Client cancellation

Cancellation must cancel the upstream request, release active accounting, remove the caller from startup waiters, and not stop the Instance merely because one request ended.

## 18. Authentication ordering

Inference API-key authentication occurs before expensive lifecycle/scheduler work. Instance-scoped authorization occurs after slug resolution because scopes store immutable IDs. Invalid keys must not trigger Instance autoload.

## 19. Capability validation

Where known before dispatch, validate that the target Instance's effective Model/configuration can serve the requested endpoint. Never route to a different Instance/Model to compensate for unsupported capability.

## 20. Name and slug changes

- changing Instance `name` alone does not change `slug`, `id`, routing or scopes;
- changing Instance `slug` explicitly changes the accepted OpenAI `model` value;
- old slugs stop resolving after the update;
- immutable Instance ID, API-key scopes, runtime ownership and durable references are not rewritten;
- management UI must warn before an Instance slug change;
- `/v1/models` reflects the new slug after the durable update;
- historical request rows keep their captured `model_slug` and are not rewritten.

## 21. Metrics and logging

Metrics/logs may include bounded safe identifiers:

- endpoint;
- immutable `instance.id` for internal correlation;
- captured `model_slug` where historical public identity is needed;
- referenced Model ID internally;
- result/error status;
- active request count;
- load-wait duration;
- latency/TTFT/token counts where measurable.

Do not use request IDs, prompt text or API-key plaintext as Prometheus labels. Do not log prompts/completions by default.

## 22. LiteLLM compatibility

Compatibility target:

```text
LiteLLM public model_name = instance.slug
            |
            v
OpenAI model=<instance.slug>
            |
            v
LlamaRack resolves slug -> immutable instance.id
            |
            v
exact Instance worker
```

LlamaRack-owned LiteLLM rows store immutable Instance ID in ownership metadata. Reconciliation publishes the current slug as the public name and may adopt legacy rows whose old ownership metadata contains the pre-migration slug.

## 23. Concurrency

Routing must tolerate Instance start/stop while requests arrive, Instance config updates, explicit slug changes, health changes after availability checks, and simultaneous high request volume. No global lock is held across an inference request or model load.

## 24. Invariants

1. OpenAI `model` resolves directly to `instance.slug`, then to one immutable Instance ID.
2. `instance.id` is immutable and never used as the public OpenAI model name.
3. `/v1/models` returns Instance slugs, not registered Model IDs or Instance UUIDs.
4. API-key Instance allowlists contain immutable Instance IDs.
5. Only READY Instances receive new requests.
6. The client never sees a worker port.
7. A request never silently switches to a sibling Instance.
8. Autoload/runtime ownership is coordinated by immutable Instance ID.
9. Authentication failure cannot trigger startup.
10. Request accounting is released exactly once.
11. Streaming is not fully buffered or replayed after output begins.
12. Historical `model_slug` is captured per request and is not rewritten by later slug changes.

## 25. Acceptance criteria

Tests demonstrate:

- Instance name `Qwen Coding` can create slug `qwen-coding` plus an independent UUID ID;
- `/v1/models` exposes `qwen-coding` as a model ID;
- chat/completion requests using `model=qwen-coding` reach that exact UUID-owned worker;
- a registered Model without any Instance is absent from `/v1/models`;
- stopped configured Instances remain listed in `/v1/models` when addressable;
- scoped API keys continue to authorize the same Instance after a slug change;
- autoload-enabled requests start the exact Instance and wait;
- autoload-disabled requests fail without spawning;
- concurrent requests share one startup for that Instance;
- sibling Instances are never fallback targets;
- client cancellation releases accounting;
- streaming forwards incrementally;
- a name-only edit leaves the OpenAI model slug unchanged;
- an explicit Instance slug change changes the accepted `model` value without changing runtime/API-key ownership;
- request history preserves the old captured slug after a later rename;
- worker/private addresses do not appear externally.
