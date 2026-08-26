# 006 — OpenAI-Compatible API

Status: Draft

Related issue: #1

## 1. Purpose

This specification defines the public inference API exposed by llamacpp-manager under `/v1/*`.

The goal is practical compatibility with commonly used OpenAI v1 client behavior while using llama.cpp as the only inference backend. The manager owns model resolution, authentication, lifecycle integration and compatibility normalization; individual workers remain private implementation details.

## 2. Compatibility scope

Initial required endpoints:

- `GET /v1/models`
- `POST /v1/chat/completions`
- `POST /v1/completions`
- `POST /v1/responses`
- `POST /v1/embeddings`

The exact request/response fields supported at runtime ultimately depend on the installed llama.cpp version and model capabilities. The manager should preserve supported OpenAI-compatible fields and avoid inventing behavior for features llama.cpp cannot implement.

## 3. Compatibility philosophy

The manager should be:

- strict enough to return clear errors for invalid requests;
- permissive enough to pass through supported llama.cpp/OpenAI-compatible extensions;
- conservative about claiming unsupported OpenAI features;
- stable in public model IDs regardless of worker details;
- compatible with standard OpenAI SDK base-URL configuration and LiteLLM.

`/v1` must not become a management API.

## 4. Authentication

All inference endpoints require a valid manager API key unless an explicit future configuration allows unauthenticated local access.

Expected header:

```text
Authorization: Bearer <key>
```

Authentication occurs before model autoload or other expensive work.

Invalid, disabled or revoked keys return an OpenAI-style authentication error and must not trigger model startup.

The plaintext API key is never logged.

## 5. `GET /v1/models`

The manager generates this response from its configured model registry.

Requirements:

- include all enabled configured models, even when currently unloaded;
- use public user-defined `model_id` as `id`;
- do not expose GGUF filesystem paths;
- do not expose worker instance IDs, PIDs or ports;
- use a stable object shape compatible with OpenAI model listing clients.

Detailed runtime state is available through `/api/v1/models`, not this endpoint.

If a model is configured but invalid/broken, product policy must decide whether it remains listed. Recommended v1 behavior: list enabled configured models even when temporarily unavailable, because `/v1/models` represents addressable configuration rather than only currently loaded processes. Requests then receive a clear availability error.

## 6. `POST /v1/chat/completions`

The manager must preserve the standard chat-completion workflow supported by llama.cpp, including where available:

- messages;
- streaming;
- temperature/sampling settings;
- max token controls;
- stop sequences;
- response formats / structured output;
- tools/function calling;
- tool choice;
- usage metadata;
- reasoning-related compatible fields provided by the worker;
- multimodal message content when the configured model/worker supports it.

The gateway resolves `model` before dispatch and may rewrite the upstream model field for the worker while preserving the public model ID externally.

Unsupported fields should follow an explicit compatibility policy:

1. pass through fields known/supported by the worker;
2. reject fields the manager knows cannot be supported when ignoring them would change semantics;
3. never silently pretend an unsupported feature was honored.

## 7. `POST /v1/completions`

Support the legacy text completion route for clients that still use it.

Requirements mirror chat routing and lifecycle behavior:

- public model resolution;
- autoload if enabled;
- streaming when supported;
- pass-through of compatible generation options;
- compatible error shape.

Do not implement completions by converting to chat in the manager unless a later compatibility requirement explicitly calls for that transformation.

## 8. `POST /v1/responses`

Support the Responses API to the extent provided by the installed llama.cpp worker.

The gateway should be intentionally thin for supported request semantics and focus on:

- authentication;
- public model resolution;
- lifecycle/autoload;
- routing;
- streaming proxying;
- public model identity normalization;
- compatible manager-level errors.

Because the Responses API evolves, avoid hard-coding only one exact field set if transparent JSON pass-through can preserve forward compatibility safely.

Manager request parsing may extract the `model` field while retaining unknown safe fields for forwarding.

## 9. `POST /v1/embeddings`

Support embedding requests for models/worker configurations capable of serving embeddings.

The manager should detect obvious incompatible configuration before dispatch where possible.

If the requested configured model is not embedding-capable:

- return a clear invalid-request/model-capability error;
- do not silently route to a different embedding model.

A model may be configured specifically for embeddings through llama.cpp options; that configuration is part of its normal model definition.

## 10. Request body handling

The gateway should avoid tightly deserializing every possible OpenAI field solely for proxying, especially for evolving endpoints.

Preferred approach conceptually:

- validate content type and body size;
- parse enough JSON to authenticate/resolve endpoint model and enforce manager policy;
- preserve the original/normalized JSON payload for upstream forwarding;
- rewrite only fields that require manager mediation.

Typed structures can still be used where required for compatibility validation, metrics or transformations.

Unknown fields should not automatically be stripped if the worker may support them.

## 11. Public model identity

Clients send:

```json
{"model":"qwen-coder"}
```

`qwen-coder` is the manager's public ID.

The internal worker may be launched with a GGUF file such as a completely different filename. The gateway hides that distinction.

If upstream responses include a model identifier, normalize it to the public model ID where practical and compatibility-safe.

This rule is especially important for applications that use the response model field for accounting or UI display.

## 12. Model availability behavior

For a valid public model with no READY instance:

### Autoload enabled

The inference request waits for lifecycle availability up to its effective deadline, then proceeds normally.

### Autoload disabled

Return a compatible model-unavailable/server error without starting a worker.

### Load in progress

Join the existing shared load wait; do not launch another duplicate instance merely because another request arrived.

### Startup failure

Return an OpenAI-style 5xx error with a safe manager error code/message.

## 13. Error envelope

Manager-originated errors should use an OpenAI-compatible JSON shape similar to:

```text
error:
  message
  type
  param
  code
```

Do not rely on exact wording from OpenAI. Stability of `type`/`code` within this project matters more than mimicking proprietary messages.

Suggested manager mappings:

| Condition | HTTP | Type/code concept |
|---|---:|---|
| Missing/invalid API key | 401 | authentication error |
| Unknown model ID | 404 | model not found |
| Disabled model | 404 or 400 | model unavailable |
| Invalid request/config field | 400 | invalid request |
| Unsupported model capability | 400 | invalid request / unsupported capability |
| Autoload disabled and no instance | 503 | model unavailable |
| Insufficient resources | 503 | insufficient resources |
| Worker startup failure | 503 | backend unavailable |
| Startup timeout | 504 | model startup timeout |
| Internal manager failure | 500 | server error |

Final code strings should be documented and covered by tests.

## 14. Upstream llama.cpp errors

When a READY worker returns an OpenAI-compatible application error, prefer preserving it with minimal normalization.

The manager may normalize:

- internal model identifiers;
- accidental internal addresses;
- malformed/non-compatible error envelopes where a known adapter is needed.

Do not convert every worker 4xx into a generic manager 500.

## 15. Streaming

Streaming is a first-class requirement.

The gateway must:

- preserve streaming content type/semantics;
- flush chunks incrementally;
- avoid full-response buffering;
- propagate cancellation on client disconnect;
- keep lifecycle/routing request accounting active through stream completion;
- avoid transparent replay/retry after any output has been sent;
- collect usage/trailing metrics when present without delaying chunks unnecessarily.

The manager may need endpoint-specific handling for final usage data, but should not reconstruct model output tokens from text if the worker already supplies authoritative usage.

## 16. HTTP behavior

The gateway should preserve appropriate upstream HTTP semantics while owning the external connection.

Requirements:

- set sane request/body/header size limits;
- preserve request IDs/correlation safely where useful;
- set manager-generated request ID if needed;
- strip hop-by-hop headers;
- never forward client Authorization header to a worker unless intentionally transformed into a separate internal credential;
- set connection/read/write timeouts that allow long-running inference and streaming;
- avoid a generic short HTTP server timeout that kills legitimate generations.

## 17. Worker authentication

Workers are local/private, so v1 does not require user-facing worker credentials.

If the manager configures an internal worker API key for defense in depth, it is manager-owned and never exposed externally. External API keys are validated at the gateway and are not forwarded as worker auth.

## 18. Request limits

The management configuration should support safe global bounds where needed, such as:

- maximum request body size;
- maximum load wait time;
- potentially maximum concurrent gateway requests if resource protection requires it.

Do not silently impose small response duration limits incompatible with local large-model inference.

Rate limiting per user/API key is not a v1 requirement unless added later.

## 19. Metrics semantics

Per inference request, record aggregate metrics such as:

- endpoint;
- public model ID;
- result status class/error code;
- total latency;
- load-wait latency;
- upstream latency;
- time to first token where measurable;
- input/output token counts when reported;
- selected instance ID for internal metrics where bounded.

Do not label Prometheus metrics by raw API key or request ID.

## 20. Logging semantics

Default inference access logs may contain:

- timestamp;
- correlation/request ID;
- endpoint;
- public model ID;
- selected instance;
- HTTP status;
- duration;
- safe error classification.

Do not log:

- Authorization header;
- full prompt/messages;
- generated response text;
- provider secrets.

Debug logging that includes payloads, if ever added, must be explicitly opt-in and clearly warned about.

## 21. LiteLLM compatibility contract

llamacpp-manager must work as a standard OpenAI-compatible base URL for LiteLLM.

Required behavior:

- stable `/v1` paths;
- Bearer key authentication;
- public model IDs accepted exactly as configured;
- standard error status codes/shapes;
- streaming compatible with LiteLLM proxy/client consumption;
- `/v1/models` usable for discovery where LiteLLM/client logic requests it.

No LiteLLM-specific proprietary API is required.

## 22. SDK compatibility matrix

Before v1, maintain automated integration tests for at least:

- OpenAI Python SDK;
- OpenAI JavaScript/TypeScript SDK;
- LiteLLM Python library;
- LiteLLM Proxy configured against the manager.

Tests should pin known versions in CI while also periodically validating newer versions.

## 23. Capability reporting

The standard `/v1/models` object has limited capability metadata. Rich manager-specific capability/state information belongs to `/api/v1/models/{id}`.

That management representation may expose:

- chat/completion support;
- embeddings mode;
- multimodal support if detected/configured;
- context information;
- current state;
- llama.cpp options affecting API behavior.

Do not overload the standard endpoint with unstable custom structure needed by the web UI.

## 24. API version separation

`/v1` follows the compatibility surface and is not the same versioning lifecycle as `/api/v1`.

A future management API v2 must not imply that OpenAI-compatible paths become `/v2`.

Likewise, adding new OpenAI-compatible endpoints should happen under `/v1` when that matches ecosystem conventions.

## 25. Security considerations

- Authenticate before autoload.
- Bound request body size.
- Validate JSON before using model IDs.
- Never treat model IDs as paths.
- Redact internal worker URLs in errors.
- Never return API-key hashes.
- Avoid SSRF: `/v1` proxy targets come only from manager-owned local worker registry, never from user-supplied URLs.
- Ensure malformed streaming requests cannot leave routing reservations permanently allocated.

## 26. Compatibility invariants

1. All inference traffic enters through manager-owned `/v1` routes.
2. Public model IDs are independent from GGUF filenames.
3. `/v1/models` can list unloaded configured models.
4. API-key failure never starts a model.
5. Streaming responses are incremental.
6. Internal worker ports/addresses are never part of the public contract.
7. Unsupported semantics are not silently claimed as supported.
8. Manager errors use stable compatible JSON envelopes.
9. The gateway does not fallback to another public model in v1.
10. Management API changes do not change the `/v1` namespace version.

## 27. Acceptance criteria

Before v1, automated tests must prove:

- model listing returns configured public IDs while workers are unloaded;
- chat completions work through an OpenAI SDK;
- chat streaming works through an OpenAI SDK;
- completions work for a compatible worker;
- Responses API works for a compatible worker/build;
- embeddings work for an embedding-configured model;
- tool/structured output fields survive gateway forwarding where llama.cpp supports them;
- a public model ID different from the GGUF filename is preserved externally;
- invalid API key returns 401 without lifecycle activity;
- unknown model returns a compatible 404 error;
- autoload-enabled request starts and waits for a model;
- startup timeout maps to a safe compatible error;
- worker/private addresses are absent from public errors;
- LiteLLM can call normal and streaming inference using the same base URL.