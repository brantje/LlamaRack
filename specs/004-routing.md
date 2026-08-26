# 004 — Request Routing

Status: Draft

Related issue: #1

## 1. Purpose

This specification defines how inference requests arriving at the unified `/v1` gateway resolve a public model ID, wait for model availability when permitted, select a READY instance, and proxy traffic to a private llama.cpp worker.

Routing must remain independent from resource placement. The router selects among already usable instances; the scheduler decides whether an instance may be started and what resources it receives.

## 2. Goals

Routing must provide:

- one stable client endpoint regardless of worker count or ports;
- routing by user-defined public model ID;
- transparent autoload integration;
- support for multiple manually configured instances per model;
- deterministic routing policies;
- request accounting for scheduling/metrics;
- streaming proxy support;
- client cancellation propagation;
- correct handling of instance failure during requests;
- LiteLLM/OpenAI client compatibility.

## 3. Non-goals for v1

Routing does not:

- inspect prompt content to choose a different model;
- automatically fallback to another model ID;
- autoscale instance count;
- load-balance across remote hosts;
- apply user-specific model routing policies;
- implement semantic routing;
- rewrite requests to external providers.

## 4. Public model resolution

Every inference request that requires a model resolves its `model` field against the configured Model registry.

Rules:

1. Match the exact public `model_id`.
2. The model must exist and be enabled.
3. The endpoint must be compatible with the configured model/worker capability where such compatibility can be known before dispatch.
4. Internal GGUF filenames, provider repository names and worker identifiers are not accepted as implicit alternate IDs unless explicitly configured as the public model ID.

V1 supports one canonical public ID per Model. Additional alias fan-out can be added later if needed, but it is not required for the initial model.

## 5. `/v1/models`

The gateway owns `/v1/models` rather than forwarding it to an individual worker.

It lists all enabled configured models, including unloaded models.

The returned ID is the public `model_id`.

Runtime state such as READY/UNLOADED is management-plane information and should not be added to the standard OpenAI model object unless exposed through a clearly namespaced compatibility-safe extension. The management UI retrieves state from `/api/v1/models` instead.

## 6. Request pipeline

Conceptual request flow:

```text
HTTP request
   |
   v
Authenticate inference API key
   |
   v
Parse enough request data to identify endpoint + model
   |
   v
Resolve configured model ID
   |
   v
Check enabled/capability policy
   |
   v
Find READY instances
   |
   +-- available --> route policy --> reserve request slot --> proxy
   |
   +-- unavailable
          |
          v
     model loading already?
          |
          +-- yes --> wait for shared load result
          |
          +-- no
                |
                v
          autoload enabled?
             |      |
            no     yes
             |      |
             v      v
           error  lifecycle start request
                    |
                    v
                 wait up to deadline
                    |
                    v
                route when READY
```

## 7. Routing candidate set

An instance is eligible only if all are true:

- instance definition is enabled;
- runtime state is READY;
- instance is healthy according to supervisor/lifecycle state;
- instance is not DRAINING or STOPPING;
- instance belongs to the resolved model;
- instance is not administratively excluded from routing;
- any explicit fixed/preferred routing constraint permits it.

Candidate evaluation must use a coherent snapshot sufficient to avoid intentionally selecting a worker already transitioning out of READY.

A race may still occur after selection; failure handling is defined below.

## 8. Routing policies

The Model selects one routing policy.

### 8.1 Least active requests — default

Choose the READY instance with the lowest current active request count.

Tie-break using stable ordering or round-robin among equals to avoid permanently favoring one instance.

This should be the default because generation requests vary widely in duration and simple round-robin can produce uneven concurrent load.

### 8.2 Round robin

Select each eligible instance in turn.

Requirements:

- counter/order is concurrency-safe;
- removed/unhealthy instances are skipped;
- adding/removing an instance does not require preserving perfect historical sequence.

### 8.3 Fixed/preferred instance

A model can select one configured preferred instance.

Recommended behavior:

- if the preferred instance is READY, route to it;
- if it is unavailable and other instances are READY, v1 should expose a model-level setting deciding strict vs soft preference only if needed;
- absent such a setting, use **soft preference**: use the preferred instance when available, otherwise use the normal least-active selection among remaining READY instances.

This prevents an unnecessary outage when a manually preferred worker is restarting.

### 8.4 Lowest current load

Choose based on normalized runtime load signals available to the manager.

Initial signal set can include:

- active request count;
- worker queue depth if available;
- recent generation throughput/latency;
- assigned GPU utilization if trustworthy.

The exact score is implementation-specific and must be documented once chosen. It must not cause rapid oscillation based on noisy single-sample GPU utilization.

If reliable load telemetry is unavailable, degrade to least-active requests.

## 9. Request reservations

Selection and active-request accounting must be coordinated so a burst of concurrent requests does not all observe the same zero-load instance before counters update.

Before proxying:

1. select candidate;
2. atomically increment/reserve its active request count;
3. verify it remains routable;
4. dispatch;
5. decrement the reservation exactly once when the request finishes, fails or is cancelled.

For streaming requests, the request remains active until the stream ends or disconnects.

## 10. Autoload integration

If no READY instance exists, the router does not itself spawn a worker.

It asks the lifecycle service for model availability.

Possible outcomes:

- READY instance becomes available;
- model is already loading and caller waits;
- autoload disabled;
- startup timeout;
- insufficient resources;
- invalid model configuration;
- worker startup failure;
- caller deadline/cancellation.

After lifecycle reports availability, candidate selection runs again from fresh state rather than assuming the newly started instance is the only option.

## 11. Queueing semantics

There are two distinct forms of waiting:

### 11.1 Load waiters

Requests waiting for a model to reach READY.

They share a per-model lifecycle start operation but retain individual cancellation/deadlines.

### 11.2 Worker/internal inference queue

Once dispatched to a READY worker, llama.cpp may queue internally based on slots/parallelism.

The manager may expose worker queue metrics where available, but v1 does not require a separate centralized inference queue for already-ready models.

A future manager-level queue may be added without changing public routing semantics.

## 12. Model startup deadline

The effective wait deadline is the earliest of:

- client/request context deadline;
- configured model startup timeout;
- manager hard upper bound if one exists.

If the deadline expires before a READY instance is available, return the appropriate gateway error and remove that request as a waiter.

The shared load may continue if required by other waiters or Always-On policy.

## 13. Proxying behavior

The gateway is an HTTP-aware reverse proxy, not a raw TCP tunnel.

It may need to:

- rewrite the upstream URL/port;
- normalize the `model` field where required by a single-model worker;
- remove manager-only headers;
- inject worker authentication if the internal worker is configured to require it;
- preserve content type and OpenAI-relevant headers;
- stream response bytes promptly;
- record timing and token metadata when observable;
- translate only errors that originate at the manager layer or require compatibility normalization.

The worker's private address must never be copied into external error text, headers or response bodies.

## 14. Model field rewriting

The public `model_id` is a manager concept. A worker may not need or recognize the same value.

The gateway may rewrite the upstream request `model` field to the value expected by the worker, but external responses should preserve the manager's public identity where the OpenAI response includes a model ID.

This rewrite must be endpoint-aware and covered by compatibility tests.

## 15. Streaming

Streaming responses must be forwarded incrementally.

Requirements:

- do not buffer the full response;
- flush SSE/data chunks promptly;
- propagate client disconnect/cancellation to the upstream request;
- keep the instance active-request reservation until stream closure;
- record final metrics if the worker supplies usage at end-of-stream;
- handle abrupt worker termination without inventing a syntactically successful stream ending.

Once response bytes have been sent to the client, the manager must not transparently retry the request on another instance because doing so can duplicate output and corrupt the stream.

## 16. Retry policy

Automatic inference retries are intentionally conservative.

### Before any upstream response bytes

A request may be retried on another READY instance only for clearly safe connection/setup failures and only when the operation is considered retry-safe by implementation policy.

### After response bytes begin

Never transparently retry.

### Worker returns an application error

Do not automatically reroute merely because another instance exists unless the error is a known worker-unavailable transport condition. Model/content errors must pass back to the caller.

V1 should favor predictable errors over aggressive hidden retries.

## 17. Instance failure during selection/dispatch

If an instance leaves READY between selection and request dispatch:

- release any reservation;
- refresh candidates;
- choose another READY instance if safe and no response has started;
- if none exists, optionally enter normal availability/autoload waiting if the request deadline permits and model policy allows;
- otherwise fail.

The failed instance is reported to lifecycle/supervisor health handling; the router does not restart it itself.

## 18. Client cancellation

Client disconnect or cancellation must:

- cancel the upstream request;
- release active-request accounting;
- remove the caller from any load-wait set;
- not stop the model merely because one request ended;
- allow idle-time tracking to proceed after request cleanup.

For long-running generation, cancellation should reach llama.cpp as promptly as HTTP transport permits.

## 19. Authentication ordering

Inference API key authentication happens before expensive model startup work.

Invalid/disabled keys must not be able to trigger autoload or consume model resources.

The gateway should parse only the minimum body needed for routing after basic request-size/security checks.

## 20. Capability validation

Some endpoints require a worker/model mode, such as embeddings.

Where capability is known from configuration, fail before startup if the requested endpoint cannot be served by the model.

Where capability can only be confirmed by the worker, the gateway may rely on upstream validation but must preserve a compatible error shape.

The manager must not silently route an embedding request to a different public model because the requested model lacks embedding support.

## 21. Metrics

Routing emits metrics such as:

- requests by public model ID and endpoint;
- success/error count;
- active request gauge;
- load-wait count and duration;
- route selection counts by instance;
- upstream connection failures;
- request duration;
- TTFT where measurable;
- input/output token counters when available.

Do not use request IDs, prompt content or API key plaintext as metric labels.

## 22. Logging

Gateway logs should include a generated request correlation ID and safe identifiers such as:

- endpoint;
- public model ID;
- selected instance ID;
- status;
- timing;
- error classification.

Do not log full prompts/completions by default.

API key values and authorization headers must be redacted.

## 23. Concurrency and consistency

The router must tolerate:

- instances starting/stopping concurrently;
- model configuration updates;
- API requests arriving during manager reconciliation;
- instance health changing after candidate snapshot;
- simultaneous high request volume.

No global lock should be held across a full inference request or model load.

## 24. LiteLLM compatibility

Compatibility target:

```text
LiteLLM -> OpenAI-compatible base URL -> llamacpp-manager -> llama.cpp
```

The manager should require no special LiteLLM-only transport path. LiteLLM should interact through normal model IDs and `/v1` endpoints.

Test cases must cover:

- model listing;
- chat completion;
- streaming;
- Responses API where supported by the LiteLLM version under test;
- tools/structured outputs where supported;
- model-not-found errors;
- backend unavailable/startup errors.

## 25. Routing invariants

1. Only READY instances receive new requests.
2. The client never chooses or sees a worker port.
3. The resolved public model ID never routes to a different model as a fallback in v1.
4. Autoload is coordinated by lifecycle, not by the router spawning workers.
5. Active request accounting is released exactly once.
6. DRAINING instances are excluded immediately from new routing.
7. Streaming responses are not fully buffered.
8. Requests are never transparently retried after response bytes start.
9. Authentication failure cannot trigger model startup.
10. Routing policy changes do not alter public model IDs.

## 26. Acceptance criteria

Routing is complete when tests demonstrate:

- requests to two public model IDs reach their respective workers through one external port;
- `/v1/models` includes an unloaded configured model;
- least-active routing distributes concurrent traffic toward the less-busy instance;
- round-robin routing cycles across two READY instances;
- fixed/preferred routing uses the preferred instance and degrades safely when unavailable;
- no request is routed to DRAINING or FAILED instances;
- a request to an unloaded autoload-enabled model waits for startup and then succeeds;
- a request to an unloaded autoload-disabled model fails without spawning anything;
- concurrent autoload callers share lifecycle startup;
- client cancellation releases routing counters;
- streaming data is forwarded incrementally;
- a worker crash before response start can safely select another READY instance where policy permits;
- a worker crash after stream start is surfaced without hidden replay;
- internal addresses do not appear in external responses.