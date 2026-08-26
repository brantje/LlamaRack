# llamacpp-manager Specifications

These design specifications define the initial architecture and product contract for issue #1. They are intentionally implementation-independent and should be reviewed before feature code is added.

- [001 — Architecture](001-architecture.md)
- [002 — Data Model](002-data-model.md)
- [003 — Model Lifecycle](003-model-lifecycle.md)
- [004 — Request Routing](004-routing.md)
- [005 — Resource Scheduler](005-resource-scheduler.md)
- [006 — OpenAI-Compatible API](006-openai-api.md)
- [007 — llama.cpp Configuration and Option Discovery](007-llamacpp-options.md)
- [008 — Model Providers and Downloads](008-model-providers.md)
- [009 — Authentication, API Keys and RBAC](009-auth-rbac.md)
- [010 — Web UI](010-ui.md)

## Review order

The recommended review order is the numeric order above. Architecture, data model and lifecycle establish the invariants used by routing and scheduling. API/options/providers/auth/UI build on those contracts.

## Implementation rule

If implementation requires violating an invariant in these specs, update and review the relevant spec first rather than silently diverging from the documented architecture.