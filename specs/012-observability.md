# Phase 11 — Observability

## Status

Approved implementation contract for Phase 11, including the dedicated inference request-log explorer added by issue #60.

Phase 11 provides persistent inference/request observability, historical hardware telemetry, session-only structured Instance logs, Prometheus exposition, the operational Dashboard, and a dedicated `/logs` request-history surface. It reuses the existing SQLite observability store, runtime WebSocket, hardware/telemetry pipeline, llama.cpp metrics scraping, supervisor log stream, and exact `instance.id` gateway routing.

Phase 11 does not add OpenTelemetry span export or a second request-log database.

## Product goals

The manager must make it possible to answer:

- which Instances are running, starting, failed, or unloaded;
- current and historical RAM/VRAM/GPU usage and Instance attribution;
- what OpenAI-compatible traffic passed through the gateway;
- request result/status, duration, queue/load time, TTFT, token usage, and throughput;
- which API keys are using the gateway without exposing secrets;
- which requests belong to the same flat multi-request trace;
- which requests belong to the same LiteLLM session, independently of tracing;
- the client IP/User-Agent associated with a request for operational debugging;
- whether autoloading, scheduling, eviction, or idle-unload activity is occurring;
- what currently needs operator attention;
- live per-Instance llama-server output;
- Prometheus-compatible metrics for external monitoring.

## Persistence and retention

Observability request and hardware history survives manager restarts and uses the existing SQLite database. During active development, update the current schema directly; do not introduce a migration framework solely for Phase 11. Development databases created with an incompatible earlier schema may be recreated rather than silently ALTERed/backfilled at runtime.

Persist individual inference request records rather than only aggregate buckets. Retention is configurable and defaults to **30 days**. Retention applies to request history, request network metadata, and historical hardware samples. Cleanup runs incrementally in the background and must not block inference traffic.

Long-lived cumulative Prometheus counters must remain monotonic when retained request/history rows are pruned. Keep cumulative state separately where required.

## Gateway request records

Every attempted OpenAI-compatible `/v1/...` gateway request must receive a stable manager request ID and produce one request-log record, including failures before Instance resolution.

This includes, at minimum:

- invalid or missing API keys;
- malformed JSON;
- missing model IDs;
- unsupported `/v1/...` endpoints;
- Instance/model resolution failures;
- autoload/acquire failures;
- worker/proxy failures;
- non-success llama-server responses;
- other manager-side gateway errors.

Request identity and durable request logging begin before authentication/body validation can return. The gateway centralizes finalization so every outcome is finalized once. Observability persistence is best-effort relative to inference: persistence failures are logged but do not turn an otherwise successful inference into an inference failure. If the early insert fails and persistence recovers before completion, finalization may create the completed row as a recovery path.

Potentially large or chunked unauthenticated request bodies must not be buffered to the full normal request-size limit before API-key validation. The gateway may use a small bounded pre-authentication metadata budget while still finalizing failed-auth request records through the normal observability path.

A request record contains, where available:

- stable manager request ID;
- trace ID;
- session ID (supplied or generated);
- normalized call type;
- request start and finish timestamps;
- requested/canonical `instance.id` (may be unavailable for early failures);
- captured model ID/name where resolvable;
- endpoint;
- safe API-key identity (ID/name/prefix, never the secret; may be unavailable);
- streaming mode;
- HTTP status and success/error result;
- end-to-end, queue, load/autoload, and TTFT timing;
- prompt/input, generated/output, and total tokens;
- prompt and generation throughput where measurable;
- autoload state;
- sanitized bounded error detail;
- client IP;
- User-Agent.

The stable request ID is returned in:

```text
X-LlamaCPP-Manager-Request-ID
```

The resolved trace ID is returned in:

```text
X-LiteLLM-Trace-ID
```

Both headers must be present on successful and failed gateway responses once the request reaches the gateway handler.

### Call type

Persist a normalized `call_type` for supported endpoints:

| Endpoint | Call type | UI label |
| --- | --- | --- |
| `/v1/chat/completions` | `chat_completion` | Chat Completion |
| `/v1/completions` | `completion` | Completion |
| `/v1/responses` | `response` | Responses |
| `/v1/embeddings` | `embedding` | Embedding |

Unsupported endpoints are still logged but have an empty/null call type. Do not invent a generic `other` call type.

## Flat multi-request tracing

Tracing is first-class flat grouping: related requests share one UUID `trace_id`. There is no parent/child request tree in this feature.

Trace resolution precedence is:

1. `X-LiteLLM-Trace-ID`;
2. `litellm_metadata.trace_id` from the request JSON body;
3. a newly generated UUID.

A LiteLLM session ID is **not** a trace ID and must never be used as a trace fallback. The trace header takes precedence over body trace metadata. A valid supplied trace ID is preserved on failures. If no valid trace ID is supplied, the manager generates a UUID.

Issue #60 intentionally does **not** add W3C `traceparent`/`tracestate` support or parent-request headers.

`GET /api/v1/observability/requests` supports `trace_id` filtering. Normal history is newest-first. A trace-filtered result is oldest-first/chronological so chained requests are understandable.

## LiteLLM session grouping

Session identity is persisted separately from `trace_id` and is used only to relate requests in the request-detail sidepanel. It does not change request routing and is not a security boundary.

Session resolution precedence is:

1. `X-LiteLLM-Session-ID`;
2. another bounded valid `X-*-Session-ID` header, excluding the LiteLLM trace header;
3. supported Codex session/thread headers for Codex clients;
4. `litellm_metadata.session_id` from the request JSON body;
5. `metadata.session_id` from the request JSON body, including Home Assistant Local OpenAI LLM;
6. top-level `session_id` from the request JSON body;
7. a newly generated UUID when none of the supported sources provides a valid session ID.

Normal request-history queries return **every request as its own row** and paginate individual requests. Requests sharing one `session_id` are not collapsed or deduplicated in the main `/logs` table.

A `session_id=<id>` request-history query returns the individual retained requests in that session for the session sidepanel. The individual request-detail response also carries its session identity and retained session request count, allowing a `/logs?request_id=<id>` deep link to discover and open the full session without requiring `session_id` in the incoming URL.

## Client network metadata

Persist `client_ip` and `user_agent` for request debugging.

Resolve client IP in this precedence order:

1. `Forwarded`;
2. `X-Forwarded-For` (first/leftmost address);
3. `X-Real-IP`;
4. direct TCP peer address.

Strip source ports and store valid IPv6 in canonical parsed form. Forwarding headers are accepted without a configured trusted-proxy list for this observability feature.

Forwarding metadata is **not a security boundary**. A directly connected client can spoof forwarding headers, so `client_ip` must never be used by this feature to authorize requests or change routing decisions.

## Request-content logging policy

Request content is sensitive and is **not stored by default**.

Each Instance has a request logging mode:

- `metadata` — default; persist metadata/metrics only;
- `full` — additionally persist request and response content needed for debugging, including prompts/messages, generated content, embedding payloads, tool definitions/arguments, and other OpenAI-compatible fields.

The setting remains Instance-scoped because inference is Instance-targeted. New/edit Instance UI must explain the privacy/storage impact of `full` logging.

When `full` mode is enabled, persist the request body after the Instance/logging policy is resolved and before worker acquisition where possible, so acquire/load failures can still retain the input. Persist response content at finalization when available.

Never persist API-key secrets, Authorization headers, management bearer tokens, provider secrets, session secrets, or worker-internal credentials.

### Summary vs detail payloads

Full request/response bodies are returned **only** by the individual request-detail endpoint.

Request-history lists and periodic WebSocket observability snapshots must contain metadata only, even when the underlying record was captured in full mode. This prevents repeated broadcast of large/sensitive payloads.

## Percentiles and counters

Dashboard/API summaries expose at least p50, p95, and p99 for completed request latency and TTFT. Incomplete/pending rows do not contribute. Null TTFT values are excluded.

Track cumulative gateway/token metrics and lifecycle/scheduler activity including autoload, load duration, eviction, idle-unload, and failed starts. Completion/finalization counter updates must be exactly-once/idempotent.

Prometheus labels must not contain request IDs, trace IDs, session IDs, API-key IDs/prefixes, client IPs, User-Agent strings, prompt text, arbitrary errors, or other unbounded/high-cardinality values.

## Hardware history and llama.cpp metrics

Persist manager-owned hardware telemetry from the existing hardware/telemetry pipeline, including host RAM, per-GPU VRAM/utilization, per-Instance/process VRAM attribution, and process CPU/RAM where available.

Use llama.cpp native metrics opportunistically through the existing runtime telemetry integration. Manager-side request counts, routing/autoload/scheduler events, API-key attribution, and request persistence remain authoritative. Native metric scrape failures are non-fatal.

## Management API

Authenticated management APIs under `/api/v1/observability` include:

- `GET /api/v1/observability/summary`;
- `GET /api/v1/observability/requests`;
- `GET /api/v1/observability/requests/{request_id}`;
- `GET /api/v1/observability/timeseries`.

Request-history filters include, where applicable:

- time range (`since`/`before`);
- `instance_id`;
- endpoint;
- API key ID;
- result;
- HTTP status;
- streaming mode;
- exact request ID lookup;
- trace ID;
- session ID for individual session expansion;
- bounded text search across useful request metadata.

History queries are bounded and paginated by individual request rows. The list API returns metadata summaries only. The detail API returns retained request/response bodies when full logging captured them.

## Request logs UI (`/logs`)

`/logs` is the dedicated persistent inference/API request-history explorer and appears in the main navigation. It is separate from raw Instance/llama-server logs and admin/system logs.

The main request table exposes:

- Time;
- Status;
- Model name;
- Instance ID;
- Key alias;
- Duration;
- TTFT;
- Tokens;
- Call Type;
- Request ID;
- Session;
- Endpoint.

Trace identity remains available in request detail and via the `trace_id` query/filter instead of consuming a permanent wide table column.

Normal history is newest-first and every request remains visible as an individual row, even when several requests share one session ID. Opening any request in a multi-request session loads the other retained requests from that session into the right-side detail sidepanel. Session expansion remains individually paginated/bounded by the management API rather than loading arbitrary retained history into the browser.

A trace-filtered view may use:

```text
/logs?trace_id=<uuid>
```

That view shows only requests matching the trace and orders them chronologically. Do not add a separate trace summary panel.

The page exposes server-side filters for the management API fields and bounded pagination; it must not load all retained history into the browser.

Selecting a request opens a **right-side Nuxt UI `USlideover`** while keeping the logs page in place. A deep link may use `request_id` in the `/logs` query string to open the same slideover. If that request belongs to a multi-request session, the detail response is authoritative for the session identity; the UI loads the session requests and may canonicalize the URL with `session_id`.

Rapid selection changes must be race-safe: a slower response for an older selection must not overwrite the latest selected request/detail.

Request detail shows:

1. status/error summary;
2. request identity/details, including request/session/trace identity;
3. timings and metrics;
4. token usage/throughput;
5. request/response content when retained;
6. metadata including model/Instance, safe key alias, client IP, and User-Agent.

Failures get prominent HTTP status + sanitized error treatment. Metadata-only requests explicitly state that request/response content was not recorded.

For full logging, the UI fetches content only from the detail endpoint and offers structured/pretty and JSON representations. Messages, tools, and tool calls should be rendered readably where the stored JSON shape allows it. Do not create duplicate normalized message/tool tables solely for this UI.

Do not add API Base, cost/accounting, teams, providers, environment, end-user accounting, or arbitrary LiteLLM tags to the request-log UI.

## Dashboard integration

The Dashboard remains the compact live operational overview. It provides an **Open logs** action to `/logs`.

Recent gateway traffic and request-error attention items deep-link into `/logs?request_id=<request_id>` when a stable request ID is available. The dedicated logs explorer discovers session context from request detail, so Dashboard does not need to know or append a session ID.

## Live updates

Continue using the authenticated `/api/v1/ws` runtime WebSocket for shared live observability/dashboard snapshots. Adding browser clients must not multiply hardware/process/llama.cpp probes.

WebSocket request summaries must never serialize full request/response bodies. `/logs` may use bounded on-demand HTTP refresh/pagination because it is a historical/debugging surface, not a second high-frequency telemetry collector.

## Per-Instance raw logs

Raw llama-server logs remain in memory only and do not survive manager restart. The bounded supervisor ring remains the retention basis for a manager session. Entries distinguish stdout, stderr, and manager lifecycle events and support live tail, source filtering, and text search.

These raw process logs are distinct from persistent inference request records in `/logs`.

## Prometheus

Expose Prometheus text exposition at `GET /metrics` using the `llamacpp_manager_` prefix. The endpoint remains unauthenticated by default and may be protected by the configurable Prometheus bearer token.

Expose bounded gateway, token, latency/TTFT, lifecycle, hardware, and Instance-state metrics without high-cardinality request/log metadata labels.

## Performance and privacy constraints

- Streaming inference must remain streaming; do not buffer streaming responses before forwarding.
- Potentially large unauthenticated request bodies are authenticated before full-size buffering; failed-auth observability remains bounded.
- Request identity/logging begins early, but persistence errors remain non-fatal to inference.
- Finalization and cumulative counter updates are exactly-once/idempotent.
- Short best-effort persistence after a client cancellation must not inherit the cancelled request context.
- Full body capture remains explicit per-Instance opt-in.
- Full bodies are never serialized into history-list/WebSocket summaries.
- Historical queries are indexed, bounded, and paginated.
- Main request history paginates individual requests; session grouping is sidepanel-only.
- Retention cleanup runs in the background.
- Forwarded IP metadata is spoofable and must remain observability-only.

## Implementation slices

### Slice 11.1 — Request observability foundation

Request persistence, Instance logging policy, safe API-key attribution, gateway instrumentation, active/queued counters, management request APIs, Prometheus request metrics, retention, and tests.

### Slice 11.2 — Lifecycle + hardware history

Shared manager-owned sampling, persisted hardware telemetry, lifecycle/scheduler counters/events, llama.cpp metrics integration, WebSocket observability, Prometheus gauges, and tests.

### Slice 11.3 — Dashboard

Operational KPIs, VRAM allocation, recent gateway traffic, Needs attention, shared live updates, `/logs` integration, and frontend coverage.

### Slice 11.4 — Structured Instance logs

Session-only raw worker logs with source/search filtering and live tail.

### Slice 11.5 — Request-log explorer and flat tracing

- request/trace identity before authentication/validation;
- all gateway error attempts persisted;
- distinct LiteLLM trace and session compatibility plus UUID generation for missing trace/session identity;
- per-request history pagination plus session-aware sidepanel expansion;
- call type + client IP/User-Agent persistence;
- trace/request/session/search filters and bounded pagination;
- metadata-only list/WebSocket DTO behavior;
- full-content detail endpoint behavior;
- `/logs` individual request table, session-aware detail slideover, pretty/JSON rendering;
- Dashboard and navigation integration;
- backend/frontend regression and coverage tests.

## Acceptance criteria

- request/history data survives manager restarts and obeys configured retention;
- every gateway `/v1/...` request attempt is represented, including early auth/validation/unsupported-endpoint errors;
- every logged request has a stable manager request ID, UUID trace ID, and session ID;
- LiteLLM trace header/body metadata is accepted with the specified precedence;
- session identity follows the specified precedence and falls back to a generated UUID when absent;
- LiteLLM session identity remains separate from trace identity and never becomes a trace fallback;
- W3C `traceparent` support is not added by issue #60;
- request and trace IDs are returned on successful and failed gateway responses;
- potentially large unauthenticated bodies cannot consume the full normal request-body buffer before API-key validation;
- client IP precedence and IPv4/IPv6 normalization follow this specification;
- call type is stored for the four supported inference endpoints;
- unresolved Instance/API-key/model metadata can remain unavailable on early failures;
- default logging is metadata-only; full logging remains explicit per Instance;
- list/WebSocket payloads never expose retained full bodies;
- detail returns full bodies only when retained;
- normal `/logs` pagination counts individual requests and never collapses rows that share a session ID;
- request-only deep links discover and load their session when applicable;
- slower stale request-detail responses cannot overwrite the latest selection;
- `/logs` exists in main navigation with required table fields, filters, bounded pagination, session-aware sidepanel navigation, and right-side slideover detail;
- `/logs?trace_id=<uuid>` is trace-only and chronological;
- metadata-only detail explains why content is absent;
- full detail offers structured/pretty and JSON content including messages/tools/tool calls where possible;
- Dashboard links into `/logs` and request detail where practical;
- inference streaming remains intact;
- Prometheus remains free of high-cardinality request/log labels;
- backend and frontend quality/coverage gates remain at least 90%.
