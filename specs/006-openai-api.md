# 006 — OpenAI-Compatible API

Status: Draft

Related issue: #1

## 1. Purpose

This specification defines the public inference API under `/v1/*`.

The manager exposes OpenAI-compatible endpoints while using llama.cpp workers privately. The public `model` identity is the configured **Instance slug**. Immutable Instance IDs remain internal durable identities.

## 2. Compatibility scope

Required endpoints:

- `GET /v1/models`
- `GET /v1/models/{model}`
- `POST /v1/chat/completions`
- `POST /v1/completions`
- `POST /v1/responses`
- `GET /v1/responses/{response_id}`
- `DELETE /v1/responses/{response_id}`
- `GET /v1/responses/{response_id}/input_items`
- `POST /v1/responses/{response_id}/cancel`
- `POST /v1/responses/input_tokens`
- `POST /v1/embeddings`
- `POST /v1/audio/transcriptions`

llama.cpp extensions:

- `POST /v1/chat/completions/input_tokens`
- `POST /v1/chat/completions/control`
- `POST /v1/rerank`
- `POST /v1/reranking`
- `GET /v1/slots`
- `POST /v1/slots/{slot_id}`

Supported fields ultimately depend on the active llama.cpp build and effective Instance configuration. Token-count and slots routes that the worker does not implement return Manager `501`.

`previous_response_id` is forwarded to llama.cpp. Manager does not reconstruct prior turns from stored Responses.

## 3. Instance identity contract

Every Instance has:

- `id` — immutable UUID used for durable ownership, foreign keys, API-key scopes, runtime state and history correlation;
- `slug` — unique mutable public identifier and exact OpenAI-compatible model ID;
- `name` — mutable display label.

Creation defaults `slug` from `name`, but later name edits do not implicitly change the slug.

```text
Instance name: Qwen Coding 32B
instance.slug: qwen-coding-32b
instance.id:   550e8400-e29b-41d4-a716-446655440000
```

Clients use the slug:

```json
{"model":"qwen-coding-32b"}
```

A registered Model is a management-plane configuration resource and is not directly inferable unless an Instance exists for it. Model slugs have no OpenAI inference meaning.

## 4. Authentication

All inference endpoints require a valid manager-generated bearer key unless a future explicit configuration allows otherwise.

```text
Authorization: Bearer <key>
```

Authenticate the key before lifecycle/autoload work. For Instance-scoped inference keys, resolve the supplied model slug to an Instance, then authorize against that Instance's immutable ID. Invalid/disabled keys must not trigger lifecycle work.

## 5. `GET /v1/models`

The manager generates this response from configured addressable Instances.

Requirements:

- include configured Instances even while stopped when they are valid/addressable;
- use exact `instance.slug` as the standard model object's `id`;
- enforce scoped-key visibility with immutable Instance IDs;
- do not expose registered Model database IDs;
- do not expose Instance UUIDs as OpenAI model IDs;
- do not expose GGUF filesystem paths;
- do not expose PIDs/private worker ports;
- use a stable OpenAI-compatible object shape.

Detailed runtime state belongs to `/api/v1/instances`.

A registered Model with zero Instances is absent from `/v1/models`.

`GET /v1/models/{model}` uses the same public namespace: enabled Instance slugs. Absent or disabled slugs return an OpenAI-style `404`. Model retrieve must not start or acquire llama.cpp.

## 6. Request model resolution

For inference requests:

1. authenticate;
2. parse enough request data to read `model`;
3. resolve exact `instance.slug` to one Instance;
4. enforce an inference-key allowlist against immutable `instance.id`;
5. capture the exact supplied slug as historical `model_slug`;
6. validate endpoint capability where known;
7. if READY, proxy to that exact Instance by immutable ID;
8. if stopped/loading, apply that Instance's lifecycle/autoload policy;
9. never silently substitute a sibling Instance.

Unknown Instance slug returns model-not-found.

## 7. Worker-facing model identity

The manager owns the worker process. Runtime ownership, reservations, process identity environment and worker registry keys use immutable `instance.id`.

The current `instance.slug` is supplied as managed `llama-server --alias`, so worker-visible model identity matches the public OpenAI model value without making the slug a process-ownership key.

External responses should preserve the public Instance slug where a model ID is returned. Worker filenames, UUID ownership metadata and private addresses remain private unless explicitly exposed through management diagnostics.

## 8. Chat completions

`POST /v1/chat/completions` should preserve llama.cpp-supported OpenAI-compatible semantics, including where available:

- messages;
- streaming;
- sampling controls;
- max token controls;
- stop sequences;
- structured output/response formats;
- tools/function calling;
- tool choice;
- usage metadata;
- reasoning-related compatible fields;
- multimodal content supported by the configured Instance.

Unknown safe fields should not be stripped merely because the manager does not understand them.

## 9. Completions

`POST /v1/completions` uses the same exact Instance resolution, lifecycle and streaming behavior.

Do not transform text completions into chat unless a future compatibility requirement explicitly introduces that behavior.

## 10. Responses API

`POST /v1/responses` is supported to the extent provided by active llama.cpp.

The manager should remain thin for generation:

- authenticate;
- resolve exact Instance slug to immutable Instance ID;
- autoload when permitted;
- stream/proxy;
- capture the upstream `resp_*` ID even when request logging is metadata-only;
- persist both durable `instance_id` and exact historical `model_slug`;
- map manager-level failures.

Stored Responses reuse `inference_requests` rather than a second table.

Manager-side retrievability follows the Instance `request_log_mode` at request time:

- `full` retains request/response bodies and later `GET /v1/responses/{id}` can return the Response JSON (streaming rows return the final embedded object, not raw SSE);
- `metadata` does not retain bodies, so later GET returns `404` even if `store=true` was forwarded to llama.cpp.

`DELETE /v1/responses/{id}` sets `openai_response_deleted` only. It must not erase `/logs` or observability data. Deleted, expired, metadata-only, and unknown IDs all return `404` from GET.

`GET /v1/responses/{id}/input_items` reconstructs OpenAI input items from the retained original request body and honors `limit`/`after`.

In-flight Responses may be retrieved as `status=in_progress` from the active-request registry and cancelled with `POST /v1/responses/{id}/cancel`. The registry stores immutable Instance ownership separately from the public model slug used in the OpenAI response. Completed Responses return `400` on cancel; unknown IDs return `404`.

Normal observability retention is the maximum lifetime of a retrievable Response.

## 11. Embeddings

`POST /v1/embeddings` resolves an exact Instance by slug.

If that Instance's effective Model/configuration cannot serve embeddings and this is known before dispatch, fail clearly. Never silently route to a different embedding-capable Instance.

## 11.1 Audio transcription

`POST /v1/audio/transcriptions` is multipart form data. Authenticate before accepting a large body. The `model` form field is the addressable Instance slug. Proxy the original multipart bytes intact and never forward Manager `Authorization`. Full request logging stores filename, content type, and size — never raw audio as SQLite TEXT.

## 11.2 Token counting, rerank, and chat control

`POST /v1/responses/input_tokens` and `POST /v1/chat/completions/input_tokens` use normal Instance slug resolution/autoload. Map returned input-token counts into observability `prompt_tokens`. Do not expose generation-token metrics. A worker `404` for these routes is rewritten to Manager `501`.

`POST /v1/rerank` and `POST /v1/reranking` are equivalent llama.cpp extensions.

`POST /v1/chat/completions/control` routes an in-flight completion ID through the shared active-request registry to the owning immutable Instance ID. It does not resolve a new Instance from a `model` field.

## 11.3 Slots

`GET /v1/slots?model=<instance.slug>` and `POST /v1/slots/{slot_id}?model=<instance.slug>&action=save|restore|erase` are llama.cpp extensions proxied to the worker's native `/slots` API.

Requirements:

- resolve the Instance from the public `model` slug because these routes have no OpenAI JSON `model` field;
- enforce scoped-key authorization against immutable Instance ID;
- require the selected Instance to already be **READY**; do not autoload and do not consume pending-admission slots;
- rewrite `/v1/slots` to worker `/slots` and `/v1/slots/{slot_id}` to worker `/slots/{slot_id}`;
- drop `model` from the forwarded query and forward remaining query parameters such as `action`;
- allowlist `action` to `save`, `restore`, and `erase`; reject anything else with `400`;
- for `save` and `restore`, reject empty or path-escaping `filename` values in the JSON body with `400`;
- map worker `404` to Manager `501`.

Security: `GET /slots` can include in-flight prompts and slot state from other concurrent traffic on the same worker. Any Inference key allowed for that Instance can read that data. This is accepted for v1 because the gateway already shares one worker per Instance among allowlisted keys.

## 12. Instance availability

For a valid current Instance slug:

### READY

Proxy immediately to the resolved immutable Instance ID.

### Startup already in progress

Join that Instance ID's shared startup wait.

### Stopped + Autoload enabled

Request startup of that exact Instance ID and wait up to the effective deadline.

### Stopped + Autoload disabled

Return model-unavailable without spawning.

### Startup/resource failure

Return an OpenAI-compatible manager error.

## 13. Error envelope

Manager-originated errors use a stable OpenAI-compatible shape:

```text
error:
  message
  type
  param
  code
```

Suggested mappings:

| Condition | HTTP | Concept |
|---|---:|---|
| invalid API key | 401 | authentication error |
| unknown Instance slug | 404 | model not found |
| invalid request/config | 400 | invalid request |
| unsupported capability | 400 | unsupported capability |
| Autoload disabled while stopped | 503 | model unavailable |
| pending-request admission limit exceeded | 503 | overloaded |
| insufficient resources | 503 | insufficient resources |
| worker startup failure | 503 | backend unavailable |
| startup timeout | 504 | model startup timeout |
| internal failure | 500 | server error |

Internal worker addresses must not appear in errors.

## 14. Streaming

Streaming is first-class.

Requirements:

- incremental forwarding;
- no full-response buffering;
- prompt chunk flushing;
- client disconnect cancellation propagated upstream;
- active request accounting retained to stream end;
- no transparent replay/retry after output begins.

## 15. Request body handling

Preferred approach:

- validate content type/body size;
- parse enough data for manager policy and Instance slug resolution;
- preserve original/normalized payload for upstream forwarding;
- rewrite only manager-mediated fields.

Do not tightly hard-code every evolving OpenAI field solely for proxying.

## 16. Retry behavior

V1 never retries by switching to a sibling Instance.

Before output begins, a bounded safe retry against the **same immutable Instance ID** may be allowed for transient connection setup failure. After output begins, never transparently retry.

## 17. Client cancellation

Cancellation must cancel the upstream request, release request accounting, remove that caller from startup waiters, and not stop the Instance merely because one client disconnects.

## 18. Instance name and slug changes

Changing Instance `name` alone preserves `id`, `slug`, OpenAI model identity, API-key scopes and runtime ownership.

Changing Instance `slug` explicitly:

- preserves immutable `instance.id` and all ID-based durable references;
- changes the accepted public OpenAI `model` value;
- requires an explicit UI warning because existing clients using the old slug will break;
- causes the old slug to stop resolving after a successful update;
- changes `/v1/models` to expose the new slug;
- does not rewrite historical request `model_slug` values;
- does not retain a compatibility alias in v1.

## 19. `/v1/models` and Model registry separation

The terminology must remain clear:

- **registered Model**: management-plane resource under `/api/v1/models`, addressed by Model slug in human-facing management routes;
- **OpenAI model ID**: `instance.slug`, exposed under `/v1/models`.

Registered Model IDs/slugs are not OpenAI model identifiers. This is intentional even though OpenAI calls the field `model`.

## 20. Worker authentication

Workers are private. External manager API keys are never forwarded as worker credentials.

If internal worker auth is used, it is manager-owned and hidden.

## 21. Metrics

Per-request metrics may record:

- endpoint;
- immutable `instance.id` for stable grouping;
- historical `model_slug` where public request identity is useful;
- status/error code;
- latency;
- load-wait latency;
- TTFT;
- token counts where reported;
- selected internal worker details only through bounded safe labels.

Do not label by raw API key/request ID/prompt.

## 22. Logging

Default access logs may include:

- correlation ID;
- endpoint;
- immutable `instance.id` for durable correlation;
- captured `model_slug` for the public identity used by that request;
- HTTP status;
- duration;
- safe error classification.

Do not log full prompts/completions or credentials by default.

## 23. LiteLLM compatibility

LiteLLM uses the Instance slug as the public model identifier while LlamaRack ownership metadata uses the immutable Instance ID:

```text
LiteLLM model_name            = qwen-coding-32b
OpenAI model                  = qwen-coding-32b
LlamaRack Instance slug       = qwen-coding-32b
LlamaRack durable Instance ID = 550e8400-e29b-41d4-a716-446655440000
```

No LiteLLM-specific transport is required. Managed LiteLLM reconciliation updates public names when slugs change without changing durable ownership.

## 24. SDK compatibility

Before v1, automated integration tests should cover:

- OpenAI Python SDK;
- OpenAI JavaScript/TypeScript SDK;
- LiteLLM Python library;
- LiteLLM Proxy.

Test exact Instance-slug resolution, immutable scoped authorization, autoload and streaming.

## 25. Security

- authenticate before autoload;
- bound body sizes;
- validate Instance slugs as identifiers, never paths;
- authorize Instance-scoped keys with immutable Instance IDs;
- never expose worker URLs;
- never return API-key hashes;
- proxy targets come only from manager-owned runtime registry;
- malformed streaming requests must not leak request reservations.

## 26. Invariants

1. All public inference traffic enters manager `/v1/*`.
2. OpenAI `model` is exactly the current `instance.slug`.
3. `instance.id` is immutable durable identity and is not the public model name.
4. `/v1/models` lists Instance slugs, not registered Models or Instance UUIDs.
5. A registered Model with no Instance is not inferable.
6. Authentication failure never starts an Instance.
7. Instance-scoped API keys remain attached to the same Instance across slug changes because scopes store immutable IDs.
8. A request never silently switches to a sibling Instance.
9. Streaming is incremental.
10. Worker ports/addresses remain private.
11. Unsupported semantics are not silently claimed as supported.
12. Historical `model_slug` is immutable request context.

## 27. Acceptance criteria

Automated tests prove:

- creating Instance name `Qwen Coding` can yield slug `qwen-coding` plus an independent UUID;
- `/v1/models` returns `qwen-coding`;
- OpenAI SDK calls using `model="qwen-coding"` reach that exact UUID-owned Instance;
- registered Models without Instances do not appear in `/v1/models`;
- stopped configured Instances remain addressable/listed;
- autoload-enabled inference starts the exact Instance;
- autoload-disabled inference fails without process startup;
- sibling Instances are never substituted;
- public response model identity stays the Instance slug where applicable;
- name-only edits do not change accepted OpenAI model identity;
- explicit Instance slug changes change the accepted OpenAI model while preserving UUID ownership and API-key scope;
- historical request rows keep their original `model_slug` after a later rename;
- streaming works through OpenAI SDK/LiteLLM;
- private worker addresses never leak.
