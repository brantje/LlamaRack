# LlamaRack Specifications

These design specifications define the initial architecture and product contract for issue #1. They are intentionally implementation-independent and should be reviewed before feature code is added.

- [001 — Architecture](001-architecture.md)
- [002 — Data Model](002-data-model.md)
- [003 — Model Lifecycle](003-model-lifecycle.md)
- [004 — Request Routing](004-routing.md)
- [005 — Resource Scheduler](005-resource-scheduler.md)
- [006 — OpenAI-Compatible API](006-openai-api.md)
- [007 — llama.cpp Configuration and Option Discovery](007-llamacpp-options.md)
- [008 — Model Providers and Downloads](008-model-providers.md)
- [009 — Authentication, API Keys and Secret Storage](009-auth.md)
- [010 — Web UI](010-ui.md)
- [011 — GGUF Metadata and Hardware Recommendations](011-hardware-recommendations.md)
- [012 — Observability](012-observability.md)
- [013 — LiteLLM Proxy catalog sync](013-litellm.md)

## Review order

The recommended review order is the numeric order above. Architecture, data model and lifecycle establish the invariants used by routing and scheduling. API/options/providers/auth/UI build on those contracts. The GGUF metadata/recommendation specification extends the Model artifact, provider, UI and hardware-estimation contracts and should be read alongside 002, 005, 008 and 010. Observability builds on routing, lifecycle and the UI contract.

## Model configuration UI parity

Any specification or implementation change that introduces or changes a **model-scoped configuration option** must update `/models/new` in the same change. The model creation workflow must expose the same model-level controls, defaults, allowed values and validation as the post-creation model configuration surfaces.

This includes lifecycle, scheduler and routing settings such as Autoload, Always On, idle/startup timeout, priority and routing policy, as well as model-level llama.cpp overrides. A model configuration feature is incomplete if a user must create the model first and only then can configure that option.

## Implementation rule

If implementation requires violating an invariant in these specs, update and review the relevant spec first rather than silently diverging from the documented architecture.