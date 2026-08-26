# 004 — Request Routing

Status: Draft

Related issue: #1

## 1. Purpose

This specification defines how inference requests arriving at `/v1/*` resolve the OpenAI `model` field to one exact configured Instance and proxy traffic to that Instance's private `llama-server` worker.

Routing is intentionally simple in v1: **the client-selected model value is the Instance ID**.

## 2. Identity contract

Each Instance has:

- a human-entered `name`;
- an `id` created by slugifying that name.

The stored `instance.id` is exactly the OpenAI-compatible model identifier.

Example:

```text
Instance name: Qwen Coding 32B
instance.id:   qwen-coding-32b
```

Client request:

```json
{"model":"qwen-coding-32b"}
```

There is no separate public alias/inference-ID field for Instances.

## 3. Goals

Routing must provide:

- one stable manager endpoint regardless of worker ports;
- exact Instance resolution from the OpenAI `model` field;
- transparent autoload of that exact Instance when permitted;
- streaming proxy support;
- request accounting;
- client cancellation propagation;
- safe handling of Instance failure;
- OpenAI SDK and LiteLLM compatibility.

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

1. authenticate inference API key;
2. parse the `model` value;
3. look up the exact `instance.id`;
4. validate that the referenced Model/artifact/configuration can serve the requested endpoint where known;
5. evaluate Instance availability;
6. proxy only to that Instance's worker.

If no matching Instance exists, return model-not-found.

A matching registered Model name does not count as an inference target unless it is also the ID of an actual Instance by coincidence.

## 6. `/v1/models`

The gateway generates `/v1/models` from configured addressable Instances.

Requirements:

- include configured Instances that are valid/addressable even when currently stopped;
- return `instance.id` as the standard model object's `id`;
- do not expose Model database IDs, GGUF paths, PIDs or private ports;
- do not add a second public identifier;
- runtime state remains management-plane information.

Registered Models are listed through `/api/v1/models` and the Models UI, not as inferable `/v1/models` entries unless an Instance exists.

## 7. Request pipeline

```text
HTTP /v1 request
      |
      v
Authenticate API key
      |
      v
Read model=<instance.id>
      |
      v
Resolve exact Instance
      |
      +-- READY ---------------------> reserve/account -> proxy
      |
      +-- QUEUED/STARTING/LOADING --> join shared wait
      |
      +-- stopped
             |
             +-- autoload=true  --> lifecycle start exact Instance -> wait -> proxy
             |
             +-- autoload=false --> model-unavailable error
```

After autoload, re-check that the same Instance is READY before dispatch.

## 8. No sibling substitution

Suppose:

```text
Model: Qwen 32B
  Instance A: qwen-fast
  Instance B: qwen-large-context
```

A request for:

```json
{"model":"qwen-fast"}
```

may only use `qwen-fast`.

If `qwen-fast` is unavailable and cannot autoload, return an availability error. Do not silently use `qwen-large-context`.

This is required because sibling Instances may have different context, GPU placement, llama.cpp options and operational policies.

## 9. Request accounting

Before proxying to a READY Instance:

1. reserve/increment active request accounting for that exact Instance;
2. verify it remains routable;
3. dispatch;
4. release accounting exactly once on completion/failure/cancellation.

For streaming requests, the reservation remains active until the stream ends or disconnects.

## 10. Autoload integration

The gateway never spawns a process directly.

It asks lifecycle for availability of the exact Instance.

Possible outcomes:

- already READY;
- existing startup joined;
- startup initiated;
- autoload disabled;
- startup timeout;
- insufficient resources;
- invalid configuration;
- worker startup failure;
- client cancellation/deadline.

Concurrent callers for the same `instance.id` share the lifecycle startup operation.

## 11. Load waiters

Each waiter retains independent cancellation/deadline.

Client disconnect removes that waiter but does not cancel startup needed by:

- other waiters;
- Always-On policy;
- another explicit lifecycle operation.

## 12. Startup deadline

Effective wait deadline is the earliest applicable bound among:

- client/request deadline;
- Instance startup timeout;
- manager hard upper bound if configured.

Timeout returns a compatible manager error and removes that request from the waiter set.

## 13. Proxying behavior

The gateway is an HTTP-aware reverse proxy.

It may:

- rewrite internal worker URL/port;
- normalize the upstream `model` field if llama.cpp requires another internal value;
- strip manager-only/hop-by-hop headers;
- inject manager-owned worker auth if used;
- preserve supported OpenAI fields;
- stream bytes promptly;
- record metrics.

External responses should preserve the client-facing Instance identity where a model ID is returned.

Private worker addresses must never leak.

## 14. Streaming

Requirements:

- incremental forwarding;
- no full-response buffering;
- prompt flushing of SSE/data chunks;
- client cancellation propagated upstream;
- active accounting held until stream completion;
- never replay/retry after response bytes have been sent.

## 15. Retry policy

Because v1 targets one exact Instance, retries do not switch to sibling Instances.

Before response bytes begin, the manager may retry a transient connection/setup operation against the **same** Instance only when safe and bounded.

After bytes begin, never transparently retry.

## 16. Instance failure during dispatch

If the target Instance leaves READY before dispatch:

- release the reservation;
- re-evaluate the same Instance;
- if lifecycle/autoload policy and deadline permit, wait for that same Instance to recover/start;
- otherwise fail.

Do not choose a sibling Instance.

## 17. Client cancellation

Cancellation must:

- cancel the upstream request;
- release active accounting;
- remove the caller from startup waiters;
- not stop the Instance merely because one request ended.

## 18. Authentication ordering

Inference API-key authentication occurs before any expensive lifecycle/scheduler work.

Invalid keys must not trigger Instance autoload.

## 19. Capability validation

Where known before dispatch, validate that the target Instance's effective Model/configuration can serve the requested endpoint, e.g. embeddings.

Never route to a different Instance/Model to compensate for unsupported capability.

## 20. Rename behavior

Instance name determines `instance.id` by slugification.

Renaming an Instance therefore changes the OpenAI model identifier.

Consequences:

- old ID stops resolving after rename;
- no automatic compatibility alias is retained in v1;
- management UI must warn before rename;
- `/v1/models` reflects the new `instance.id` after the durable update.

## 21. Metrics and logging

Metrics/logs may include bounded safe identifiers:

- endpoint;
- `instance.id`;
- referenced Model ID internally;
- result/error status;
- active request count;
- load-wait duration;
- latency/TTFT/token counts where measurable.

Do not use request IDs, prompt text or API-key plaintext as Prometheus labels.

Do not log prompts/completions by default.

## 22. LiteLLM compatibility

Compatibility target:

```text
LiteLLM
   -> OpenAI base URL
   -> model=<instance.id>
   -> llamacpp-manager
   -> exact Instance worker
```

LiteLLM configuration uses the Instance ID as the backend model name exposed by this manager.

## 23. Concurrency

Routing must tolerate:

- Instance start/stop while requests arrive;
- Instance config updates;
- exact Instance rename operations;
- health changes after availability checks;
- simultaneous high request volume.

No global lock is held across an inference request or model load.

## 24. Invariants

1. OpenAI `model` resolves directly to `instance.id`.
2. `instance.id` is the slug derived from Instance name.
3. `/v1/models` returns Instance IDs, not registered Model IDs.
4. Only READY Instances receive new requests.
5. The client never sees a worker port.
6. A request never silently switches to a sibling Instance.
7. Autoload is coordinated by lifecycle.
8. Authentication failure cannot trigger startup.
9. Request accounting is released exactly once.
10. Streaming is not fully buffered or replayed after output begins.

## 25. Acceptance criteria

Tests demonstrate:

- Instance name `Qwen Coding` creates ID `qwen-coding`;
- `/v1/models` exposes `qwen-coding` as a model ID;
- chat/completion requests using `model=qwen-coding` reach that exact worker;
- a registered Model without any Instance is absent from `/v1/models`;
- stopped configured Instances remain listed in `/v1/models` when addressable;
- autoload-enabled requests start the exact Instance and wait;
- autoload-disabled requests fail without spawning;
- concurrent requests share one startup for that Instance;
- sibling Instances are never fallback targets;
- client cancellation releases accounting;
- streaming forwards incrementally;
- Instance rename changes the accepted `model` value only after explicit management update;
- worker/private addresses do not appear externally.