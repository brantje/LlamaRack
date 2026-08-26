# 006 — OpenAI-Compatible API

Status: Draft

Related issue: #1

## 1. Purpose

This specification defines the public inference API under `/v1/*`.

The manager exposes OpenAI-compatible endpoints while using llama.cpp workers privately. The public `model` identity is the configured **Instance ID**.

## 2. Compatibility scope

Initial required endpoints:

- `GET /v1/models`
- `POST /v1/chat/completions`
- `POST /v1/completions`
- `POST /v1/responses`
- `POST /v1/embeddings`

Supported fields ultimately depend on the active llama.cpp build and effective Instance configuration.

## 3. Instance identity contract

Every Instance has a human-entered name and a slug-derived `id`.

```text
Instance name: Qwen Coding 32B
instance.id:   qwen-coding-32b
```

Clients use that exact ID:

```json
{"model":"qwen-coding-32b"}
```

There is no separate public inference ID/model alias field for Instances.

A registered Model is a management-plane configuration resource and is not directly inferable unless an Instance exists for it.

## 4. Authentication

All inference endpoints require a valid manager-generated bearer key unless a future explicit configuration allows otherwise.

```text
Authorization: Bearer <key>
```

Authenticate before Instance resolution/autoload.

Invalid/disabled/revoked keys must not trigger lifecycle work.

## 5. `GET /v1/models`

The manager generates this response from configured addressable Instances.

Requirements:

- include configured Instances even while stopped when they are valid/addressable;
- use exact `instance.id` as the standard model object's `id`;
- do not expose registered Model database IDs;
- do not expose GGUF filesystem paths;
- do not expose PIDs/private worker ports;
- do not expose a second public alias;
- use a stable OpenAI-compatible object shape.

Detailed runtime state belongs to `/api/v1/instances`.

A registered Model with zero Instances is absent from `/v1/models`.

## 6. Request model resolution

For inference requests:

1. authenticate;
2. parse enough JSON to read `model`;
3. resolve exact `instance.id`;
4. validate endpoint capability where known;
5. if READY, proxy to that exact Instance;
6. if stopped/loading, apply that Instance's lifecycle/autoload policy;
7. never silently substitute a sibling Instance.

Unknown Instance ID returns model-not-found.

## 7. Model field rewriting upstream

`instance.id` is a manager concept and may not be the value llama.cpp expects internally.

The gateway may rewrite the worker-facing `model` field where necessary, but external responses should preserve/normalize back to the public `instance.id` when compatibility-safe.

Workers' file names/internal identifiers remain private.

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

The manager should remain thin:

- authenticate;
- resolve exact Instance ID;
- autoload when permitted;
- stream/proxy;
- normalize external Instance identity;
- map manager-level failures.

## 11. Embeddings

`POST /v1/embeddings` resolves an exact Instance.

If that Instance's effective Model/configuration cannot serve embeddings and this is known before dispatch, fail clearly.

Never silently route to a different embedding-capable Instance.

## 12. Instance availability

For a valid `instance.id`:

### READY

Proxy immediately.

### Startup already in progress

Join that Instance's shared startup wait.

### Stopped + Autoload enabled

Request startup of that exact Instance and wait up to the effective deadline.

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
| unknown Instance ID | 404 | model not found |
| invalid request/config | 400 | invalid request |
| unsupported capability | 400 | unsupported capability |
| Autoload disabled while stopped | 503 | model unavailable |
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
- parse enough JSON for manager policy and Instance resolution;
- preserve original/normalized payload for upstream forwarding;
- rewrite only manager-mediated fields.

Do not tightly hard-code every evolving OpenAI field solely for proxying.

## 16. Retry behavior

V1 never retries by switching to a sibling Instance.

Before output begins, a bounded safe retry against the **same** Instance may be allowed for transient connection setup failure.

After output begins, never transparently retry.

## 17. Client cancellation

Cancellation must:

- cancel upstream request;
- release request accounting;
- remove that caller from startup waiters;
- not stop the Instance merely because one client disconnects.

## 18. Instance rename

Instance name is slugified into `instance.id`.

Renaming therefore changes the public OpenAI model ID.

Required behavior:

- UI warns that existing clients using the old `model` value will break;
- old ID stops resolving after successful rename;
- `/v1/models` exposes the new ID;
- no compatibility alias is retained in v1.

## 19. `/v1/models` and Model registry separation

The terminology must remain clear:

- **registered Model**: management-plane resource under `/api/v1/models`;
- **OpenAI model ID**: `instance.id`, exposed under `/v1/models`.

This is intentional even though OpenAI calls the field `model`.

## 20. Worker authentication

Workers are private. External manager API keys are never forwarded as worker credentials.

If internal worker auth is used, it is manager-owned and hidden.

## 21. Metrics

Per-request metrics may record:

- endpoint;
- `instance.id`;
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
- `instance.id`;
- HTTP status;
- duration;
- safe error classification.

Do not log full prompts/completions or credentials by default.

## 23. LiteLLM compatibility

LiteLLM uses the Instance ID as the backend model identifier:

```text
LiteLLM model = qwen-coding-32b
OpenAI model  = qwen-coding-32b
Instance ID   = qwen-coding-32b
```

No LiteLLM-specific transport is required.

## 24. SDK compatibility

Before v1, automated integration tests should cover:

- OpenAI Python SDK;
- OpenAI JavaScript/TypeScript SDK;
- LiteLLM Python library;
- LiteLLM Proxy.

Test exact Instance-ID resolution, autoload and streaming.

## 25. Security

- authenticate before autoload;
- bound body sizes;
- validate Instance IDs as identifiers, never paths;
- never expose worker URLs;
- never return API-key hashes;
- proxy targets come only from manager-owned runtime registry;
- malformed streaming requests must not leak request reservations.

## 26. Invariants

1. All public inference traffic enters manager `/v1/*`.
2. OpenAI `model` is exactly `instance.id`.
3. `instance.id` is the slug derived from Instance name.
4. `/v1/models` lists Instances, not registered Models.
5. A registered Model with no Instance is not inferable.
6. Authentication failure never starts an Instance.
7. A request never silently switches to a sibling Instance.
8. Streaming is incremental.
9. Worker ports/addresses remain private.
10. Unsupported semantics are not silently claimed as supported.

## 27. Acceptance criteria

Automated tests prove:

- creating Instance name `Qwen Coding` yields ID `qwen-coding`;
- `/v1/models` returns `qwen-coding`;
- OpenAI SDK calls using `model="qwen-coding"` reach that exact Instance;
- registered Models without Instances do not appear in `/v1/models`;
- stopped configured Instances remain addressable/listed;
- autoload-enabled inference starts the exact Instance;
- autoload-disabled inference fails without process startup;
- sibling Instances are never substituted;
- public response model identity stays the Instance ID where applicable;
- Instance rename changes the accepted OpenAI model ID only after explicit management update;
- streaming works through OpenAI SDK/LiteLLM;
- private worker addresses never leak.