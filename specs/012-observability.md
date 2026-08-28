# Phase 11 — Observability

## Status

Approved implementation contract for Phase 11.

This phase adds persistent request/inference observability, historical hardware telemetry, improved in-memory per-Instance logs, Prometheus exposition and a real-time Dashboard. It builds on the existing runtime WebSocket, hardware telemetry, llama.cpp metrics scraping, supervisor log stream and exact `instance.id` gateway routing.

Phase 11 does not add OpenTelemetry or a separate observability application/page.

## Product goals

Phase 11 must make the manager answer, from one operational Dashboard:

- which Instances are running, starting, failed or unloaded;
- how much RAM/VRAM is committed and which Instances are using each GPU;
- what inference traffic has passed through the gateway recently;
- request latency, TTFT, token usage and generation throughput;
- which API keys are actively using the gateway;
- whether autoloading, scheduling, eviction or idle-unload activity is occurring;
- what currently needs operator attention;
- live per-Instance llama-server output with useful source filtering;
- Prometheus-compatible metrics for external monitoring.

## Persistence and retention

Observability history survives manager restarts.

Use the existing SQLite database. During active development the current schema is updated directly; do not introduce migration files solely for Phase 11.

Persist individual inference request records rather than only aggregate buckets.

Persist historical hardware telemetry used by the Dashboard, including RAM/VRAM and GPU utilization/allocation data that is available from the existing hardware/telemetry pipeline.

Retention is configurable and defaults to **30 days**. Retention applies to historical request and hardware samples. Cleanup runs incrementally in the background and must not block inference traffic.

Long-lived cumulative Prometheus counters must not become non-monotonic merely because retained history is pruned. If persisted counter state is required for correct Prometheus counter semantics, keep that state separately from retention-limited history.

## Inference request records

Every authenticated OpenAI-compatible inference request that resolves an Instance should produce one request record, including failed/unavailable requests once the target Instance is known.

Metadata fields include, where applicable:

- request start and finish timestamps;
- target `instance.id`;
- OpenAI-compatible endpoint;
- API key identity suitable for management display (ID/name/prefix, never the secret);
- streaming vs non-streaming;
- request result and HTTP status;
- active/queued state contribution;
- end-to-end duration;
- queue/wait duration;
- load/autoload duration when an unloaded Instance had to be started;
- TTFT (time to first response byte/token where measurable);
- prompt/input tokens;
- generated/output tokens;
- total tokens;
- generation tokens/second;
- sanitized error detail;
- whether autoload was required.

The manager should use request/response usage information where available and may supplement it with llama.cpp-native metrics/timings. Missing metrics are nullable; absence of a worker-provided value must not make an otherwise successful request fail.

### Percentiles

Dashboard/API summaries expose at least p50, p95 and p99 for:

- request latency;
- TTFT.

Percentiles are computed for the requested reporting window from equivalent request records. Null TTFT samples are excluded from TTFT percentiles.

## Request-content logging policy

Request content is sensitive and is **not stored by default**.

Each Instance has a request logging mode:

- `metadata` — default; persist request metadata/metrics only;
- `full` — additionally persist request and response content needed for debugging, including prompts/messages, generated content, embedding request payloads, tool arguments and other OpenAI-compatible request/response fields.

The setting belongs to the Instance because inference is Instance-targeted. New and edited Instances must expose this choice with clear copy explaining the privacy/storage impact of `full` logging.

The manager must never persist API key secrets, management session tokens, provider secrets or worker-internal credentials in observability records.

## API-key usage

Phase 11 includes API-key usage observability.

Management APIs/Dashboard may show safe key identity such as key name and configured prefix. Never display or persist the raw API-key secret.

Prometheus labels must **not** include API key ID, key prefix, request ID, prompt text or other high-cardinality/sensitive request values.

## Lifecycle and scheduler metrics

Track at least:

- autoload count;
- request queue/wait duration;
- model/Instance load duration;
- eviction count;
- idle-unload count;
- failed start count.

These events are Instance-scoped where possible and survive manager restarts when used for historical reporting/cumulative counters.

## Hardware history

Persist hardware telemetry so the Dashboard can show historical/current resource usage across restarts.

Use the existing hardware/telemetry implementation as the source of truth. Do not create a competing GPU detector.

Record, where available:

- host RAM usage;
- per-GPU total/used/free VRAM;
- per-GPU utilization;
- per-Instance/process VRAM attribution;
- per-Instance CPU/RAM process telemetry;
- timestamp and device identity.

Sampling must be bounded and must not create one hardware probe per connected browser. Collection is manager-owned; WebSocket clients consume shared current state.

## llama.cpp native metrics

Use llama.cpp native metrics whenever the running worker exposes them. Existing `FetchLlamaMetrics`/runtime telemetry is the integration point.

Manager-side metrics remain authoritative for gateway request counts, routing/autoload/scheduler events, API-key attribution and request persistence. llama.cpp metrics supplement rather than replace them.

A native metrics scrape failure is non-fatal and must not break the Dashboard or inference.

## Management API

Provide authenticated management APIs under `/api/v1/observability` for the Dashboard and external management clients.

Minimum resources:

- `GET /api/v1/observability/summary` — current/windowed KPI summary, percentiles, key activity and lifecycle counters;
- `GET /api/v1/observability/requests` — paginated/filterable individual request history;
- `GET /api/v1/observability/timeseries` — bounded historical series/buckets for supported request and hardware metrics.

Supported request-history filters should include time range and, where applicable:

- `instance_id`;
- endpoint;
- API key;
- result/status;
- streaming mode.

Do not require the frontend to query the Prometheus endpoint for its own Dashboard.

## Live updates

Use the existing authenticated runtime WebSocket at `/api/v1/ws`.

Extend that socket with observability/dashboard messages rather than adding a second polling loop for live state.

The Dashboard may use management HTTP APIs for its initial/historical snapshot, then receive near-real-time changes over the WebSocket.

WebSocket fan-out must use shared manager-side collection/state; adding browser clients must not multiply expensive GPU/process/llama.cpp probes.

## Prometheus

Expose Prometheus text exposition at:

```text
GET /metrics
```

Metric names use the prefix:

```text
llamacpp_manager_
```

The endpoint is unauthenticated by default.

A configurable Prometheus auth token may be set. When configured, `/metrics` requires the token; a Bearer token is the canonical HTTP mechanism. The default token is empty/no authentication.

Prometheus dimensions may include bounded labels such as:

- `instance_id`;
- endpoint;
- result/status class where useful;
- streaming mode;
- GPU/device ID.

Do not label metrics with request IDs, API-key IDs/prefixes, arbitrary errors, prompts, paths or other unbounded/high-cardinality values.

Prometheus should expose at least:

- gateway requests total;
- current active and queued requests;
- request success/error counts;
- latency/TTFT distributions or equivalent bounded summary metrics;
- prompt/generated/total token counters;
- generation throughput where meaningful;
- autoload/load/eviction/idle-unload/failed-start metrics;
- current RAM/VRAM/GPU utilization/allocation gauges;
- Instance lifecycle state gauges.

## Per-Instance logs

Raw llama-server logs remain **in memory only** and do not survive a manager restart. No file rotation/persistent log store is introduced in Phase 11.

The existing bounded supervisor ring remains the basis for log retention during a manager session.

Log entries become structured enough to distinguish:

- `stdout`;
- `stderr`;
- manager lifecycle events.

The Instance log UI provides:

- live tail;
- source filtering;
- text search/filter;
- a clear/resettable current-session view where appropriate.

Phase 11 does not add a separate per-Instance metrics summary page/card. Runtime telemetry already shown on Instance surfaces may remain, but the new historical request metrics stay Dashboard-centric.

## Dashboard

The Dashboard is the only new observability overview surface. Do not add a separate Metrics/Observability page.

Follow the approved mockup structure and the existing application theme. Use Nuxt UI components first and Tailwind only for composition.

### Header

Title: **Dashboard**.

Provide an **Open logs** action. The existing/Phase 11 Playground route may be linked when available, but Phase 11 must not implement Playground behavior merely to satisfy the Dashboard mockup.

### KPI row

Show compact cards matching the mockup intent:

1. **Running** — running/total Instances, with starting/error context.
2. **VRAM committed** — used/available aggregate and utilization context.
3. **Gateway · 15 min** — request count and active API-key count.
4. **Idle unload** — global idle-unload setting and number of Instance overrides.

Use Instance terminology internally/API-wise even if compact UI copy uses “models” only where referring to OpenAI addressable model IDs would be clearer to users.

### VRAM allocation

Show one allocation row per GPU, including:

- device name/ID;
- used / total VRAM;
- GPU utilization;
- a visual utilization/allocation bar;
- per-process/Instance attribution when available.

Automatic/manual placement data and observed process attribution should be visually distinguishable where useful, but observed telemetry is the source of truth for current usage.

### Gateway traffic · last 15 min

Show recent individual request rows with the mockup columns:

- Time;
- Model (the exact `instance.id`);
- Endpoint;
- Key (safe key prefix/name only);
- Tokens;
- Latency;
- Result.

Streaming and error state should be available via row detail/status where useful without making the table excessively wide.

### Needs attention

Show actionable current problems such as:

- failed/failed-to-start Instances;
- recent inference errors;
- manually stopped Always-On Instances;
- high RAM/VRAM pressure;
- scheduler/resource-pressure blocks.

Each item should link to the most relevant existing control surface (Instance, logs, etc.).

The panel is operational guidance, not an alert-management subsystem.

## Error storage and privacy

Request-level errors may be retained, but store sanitized, bounded error text only.

Never persist raw authorization headers, API-key secrets, session cookies, provider tokens or encryption material.

Default `metadata` request logging must not persist prompt/message/generated/tool/embedding content.

`full` logging is an explicit per-Instance opt-in and should be presented as potentially sensitive/high-volume storage.

## Performance and concurrency constraints

Observability must not materially slow the inference path.

- Gateway response streaming must remain streaming; do not buffer a response before forwarding it to the client.
- Persistence occurs after/alongside response forwarding and should minimize time spent on the critical path.
- WebSocket clients share sampled telemetry rather than triggering their own hardware probes.
- Historical queries are indexed, bounded and paginated.
- Retention cleanup is incremental/background work.
- High-cardinality Prometheus label sets are prohibited.
- Errors in observability persistence are logged but must not turn a successful inference into a failed inference.

## Implementation slices

### Slice 11.1 — Request observability foundation

- direct development-schema additions;
- per-Instance `metadata` / `full` request logging mode;
- safe API-key attribution;
- gateway request instrumentation;
- individual request persistence;
- active/queued counters;
- summary/request/timeseries management APIs;
- `/metrics` request metrics and optional token auth;
- retention setting/cleanup;
- tests.

### Slice 11.2 — Lifecycle + hardware history

- shared manager-owned hardware sampling/history;
- persisted GPU/RAM/VRAM telemetry;
- lifecycle/scheduler/autoload/eviction/idle-unload/failed-start events and counters;
- llama.cpp-native metric integration into shared current snapshots;
- WebSocket observability events;
- Prometheus lifecycle/hardware gauges/counters;
- tests.

### Slice 11.3 — Dashboard

- replace the current generic Overview with the approved Dashboard layout;
- KPI row;
- VRAM allocation;
- last-15-minute gateway traffic;
- Needs attention;
- live WebSocket updates with HTTP initial/history loading;
- responsive Nuxt UI implementation;
- frontend tests and coverage.

### Slice 11.4 — Structured Instance logs

- retain raw worker logs in memory only;
- structured stdout/stderr/manager lifecycle source;
- live tail plus source/text filtering;
- preserve bounded session-only retention;
- backend/frontend tests.

## Acceptance criteria

- request/history data survives manager restarts;
- default observability retention is 30 days and is configurable;
- every resolvable authenticated inference request is stored individually;
- default Instance logging stores metadata only;
- an Instance may explicitly opt into full request/response content storage;
- full logging never stores API/session/provider secrets;
- active, queued, success/error, duration, TTFT, token and generation-throughput metrics are available when measurable;
- p50/p95/p99 latency and TTFT are available from management summary APIs;
- request metrics can be broken down by Instance, endpoint, result/status and streaming mode;
- API-key usage is visible in management observability without exposing secrets;
- scheduler/autoload/load/eviction/idle-unload/failed-start activity is tracked;
- RAM/VRAM/GPU history survives restart for the configured retention period;
- llama.cpp native metrics are consumed opportunistically and failures remain non-fatal;
- `/metrics` uses `llamacpp_manager_` names and works unauthenticated by default;
- configuring the Prometheus token protects `/metrics`;
- Prometheus does not expose high-cardinality API-key/request/content labels;
- Dashboard follows the approved layout and uses the existing runtime WebSocket for live updates;
- no separate observability page is introduced;
- Instance logs remain session-only, distinguish stdout/stderr/manager lifecycle, and support live tail + search/filter;
- inference streaming behavior remains intact;
- backend and frontend quality/coverage gates remain at least 90%.
