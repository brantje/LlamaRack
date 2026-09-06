# 016 — OpenAI / LiteLLM 1.0 compatibility contract

Status: Release contract

Related issue: #121

## 1. Purpose

This specification freezes the OpenAI-compatible inference surface promised by LlamaRack 1.0. It supplements `006-openai-api.md` and `013-litellm.md` with an explicit compatibility boundary and a black-box verification policy.

The contract is intentionally narrower than “everything OpenAI supports”. A feature is part of the 1.0 contract only when it is listed here as supported or conditional and is exercised by the release conformance suite when a matching fixture is supplied.

## 2. Public identity and transport invariants

- All inference traffic enters the manager under `/v1`.
- The public OpenAI `model` value is the current LlamaRack `instance.slug`.
- The durable LlamaRack `instance.id` is an immutable UUID used for ownership, authorization scopes, runtime state, scheduler state and durable correlation; it is not the public OpenAI model name.
- `/v1/models` lists enabled/addressable Instance slugs, not registered management-plane Models or Instance UUIDs.
- Manager API keys use `Authorization: Bearer <key>` and are authenticated before lifecycle/autoload work. Instance-scoped keys are authorized against immutable Instance IDs after slug resolution.
- Manager-owned errors use an OpenAI-style `error` object with `message`, `type`, `param`, and `code` fields.
- Private worker hostnames, ports, filesystem paths, PIDs, credentials, and internal durable identifiers are never part of the public compatibility surface unless explicitly exposed by a management diagnostics endpoint.
- Streaming responses remain incremental; LlamaRack does not buffer a complete generation before forwarding it.
- A client disconnect cancels that upstream request and releases manager accounting without stopping the Instance solely because the caller disconnected.

## 3. Endpoint contract

| Surface | 1.0 status | Contract |
|---|---|---|
| `GET /v1/models` | Supported | Manager-local OpenAI model-list shape using exact current Instance slugs. |
| `GET /v1/models/{model}` | Supported | Manager-local lookup by exact enabled Instance slug; does not start a worker. |
| `POST /v1/chat/completions` | Supported | Thin llama.cpp-compatible pass-through with exact Instance-slug resolution, UUID-backed lifecycle/authorization, identity normalization, streaming, and manager error mapping. |
| `POST /v1/completions` | Supported | Legacy text-completions pass-through with the same routing/lifecycle rules. |
| `POST /v1/responses` | Supported | Thin llama.cpp Responses pass-through; response IDs may be retained by manager observability according to request-log policy. |
| Responses retrieve/delete/input-items/cancel | Supported with documented LlamaRack storage semantics | Manager-local operations described in `006-openai-api.md`; not a claim of OpenAI server-side storage parity. |
| `POST /v1/embeddings` | Conditional | Supported when the selected Instance/runtime is configured for embeddings. No silent rerouting. |
| `POST /v1/audio/transcriptions` | Conditional | Multipart pass-through when the selected Instance/runtime implements transcription. Other OpenAI audio APIs are not promised. |
| Chat/Responses multimodal image input | Conditional | Passed through when the selected model, mmproj/runtime, and llama.cpp build support it. LlamaRack does not emulate missing vision capability. |
| Tools/function calling | Conditional | Tool fields and tool-call payloads are passed through. Correct model-side tool generation depends on the selected model/runtime. |
| Structured output / JSON schema | Conditional | Compatible response-format/schema fields are passed through. Schema enforcement is the selected runtime's behavior unless LlamaRack explicitly validates a manager-owned field. |
| Chat/Completions/Responses streaming | Supported | SDK iteration plus raw SSE/content-type/framing/termination are release-tested. |
| Request cancellation/disconnect | Supported | Disconnect propagates upstream and a subsequent request must remain usable. |
| Cold Instance autoload | Supported | READY routes immediately; autoload-enabled STOPPED starts once by immutable Instance ID and waits; autoload-disabled STOPPED returns availability error. |
| llama.cpp token-count/rerank/chat-control/slots extensions | Extension | Public LlamaRack extensions, not OpenAI compatibility claims. |

## 4. Explicit non-contract / partial areas

The 1.0 label does not claim support for every OpenAI endpoint, historical SDK version, realtime API, assistants/threads, batches, files/vector stores, image generation, speech synthesis, fine-tuning, or undocumented OpenAI behavior.

Pass-through features remain bounded by the active llama.cpp build and selected Instance. If a runtime does not implement a conditional route/field, LlamaRack may return the documented unsupported/unavailable response rather than emulate OpenAI behavior.

Responses retrieval is intentionally LlamaRack-specific: retrievability depends on `request_log_mode`; metadata-only requests are not later reconstructable even when an upstream `store` field was forwarded.

## 5. Error contract asserted by conformance tests

Common manager-originated failures are stable enough for clients to branch on status plus the OpenAI-style envelope:

| Condition | HTTP |
|---|---:|
| missing/invalid/revoked inference key | 401 |
| unknown or disabled Instance slug | 404 |
| malformed/invalid manager-owned request | 400 |
| stopped Instance with autoload disabled | 503 |
| resource/admission/startup failure | 503 |
| startup timeout | 504 |
| unexpected manager failure | 500 |

Worker-originated errors may retain additional upstream detail, but must not leak private worker network/process details.

## 6. Streaming contract

For streaming Chat Completions and Responses where supported by the selected runtime:

1. response media type is compatible with SSE;
2. events are forwarded incrementally in received order;
3. each emitted `data:` payload is syntactically valid for the endpoint or is the endpoint's documented terminal marker;
4. the stream terminates cleanly and SDK iterators complete;
5. LlamaRack does not replay a stream against another Instance after output begins;
6. disconnecting a caller does not corrupt manager lifecycle/request state.

The conformance suite checks both real SDK iteration and the raw wire framing so an HTTP 200 alone is never sufficient.

## 7. Lifecycle contract through `/v1`

Release qualification prepares fixture states through the management API, then exercises inference only through `/v1`:

- READY: public slug resolves to one immutable Instance ID and routes immediately to that exact Instance.
- STOPPED + autoload enabled: concurrent cold requests for the same slug coordinate one startup keyed by immutable Instance ID and all admissible callers converge on it.
- STOPPED + autoload disabled: request returns the documented availability error and no worker is started.
- startup failure: request returns a useful client-visible error and the Instance remains recoverable/inspectable rather than entering corrupt manager state.

Authentication is evaluated before any of those lifecycle actions.

## 8. Real-client release matrix

The blocking 1.0 suite pins known-good client versions in `tests/compat/versions.json` and invokes the libraries themselves:

- OpenAI Python SDK;
- OpenAI JavaScript/TypeScript SDK;
- LiteLLM Python SDK/client;
- LiteLLM Proxy forwarding to LlamaRack.

A newer-client probe may run separately as non-blocking evidence. Updating the pinned blocking versions is an explicit compatibility-suite change, not an implicit dependency upgrade.

Capability-specific checks are enabled by fixture environment variables. A release job running in strict mode must declare which capabilities it advertises and fails if a required fixture is absent; unsupported capabilities are recorded as `not_applicable`, never as a false pass.

## 9. LiteLLM contract

Two integrations are verified:

1. LiteLLM SDK using LlamaRack's OpenAI-compatible base URL and exact Instance slug.
2. LiteLLM Proxy using the managed OpenAI-provider shape from `013-litellm.md`, where public `model_name`/`openai/...` use `instance.slug` and `llamarack_instance_id` stores immutable Instance ownership, then accepting a client request and forwarding it to LlamaRack.

The suite verifies model identity, bearer authentication, streaming, and caller tracing/request headers that LlamaRack is documented to retain/forward. LiteLLM catalog synchronization remains a management integration; it does not introduce another `/v1` inference identity.

## 10. Rename/slug compatibility boundary

- Changing Instance `name` alone is non-breaking for OpenAI clients because `instance.slug` is unchanged.
- Explicitly changing `instance.slug` is a public API break for clients that still send the old slug; the management UI must warn before saving it.
- An Instance slug change preserves immutable `instance.id`, API-key scopes, runtime ownership and durable references.
- `/v1/models` exposes the new slug after the update and the old slug no longer resolves in v1.
- Request history preserves the exact `model_slug` captured at request time; historical rows are never rewritten to the new slug.
- Changing a registered Model slug is management-only and has no OpenAI inference meaning.

## 11. Release evidence and version policy

`1.0.0-rc.*` qualification must retain machine-readable evidence containing client/runtime versions, enabled fixtures, pass/fail/not-applicable results, and the candidate image digest or externally supplied target identifier.

Versioning policy:

- compatibility fixes that preserve this contract may ship in `1.0.x`;
- additional compatible endpoints/features may be added in later `1.x` and must be documented before being advertised;
- an intentional breaking change to a documented 1.x compatibility guarantee requires a major release unless that surface was explicitly experimental before 1.0.

## 12. Acceptance gate

The 1.0 release gate is satisfied only when the repeatable live suite demonstrates the advertised contract against the release candidate and the evidence includes, at minimum: model listing/slug identity, Python and JS basic+streaming calls, Responses, authentication and invalid-model/request errors, raw streaming framing/termination, cold-autoload lifecycle behavior, direct LiteLLM client compatibility, LiteLLM Proxy forwarding, rename-safe durable ownership/scopes, and every conditional capability declared required for that candidate fixture set.
