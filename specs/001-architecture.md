# 001 — Architecture

Status: Draft

Related issue: #1

## 1. Purpose

`llamarack` is a single-host web application that manages `llama-server` processes and exposes one stable OpenAI-compatible API.

The architecture separates **registered Models** from **runtime Instances**:

- a **Model** is management-plane configuration for a GGUF artifact plus reusable llama.cpp defaults;
- an **Instance** is one durable configured `llama-server` process definition and is the unit of lifecycle, routing, scheduling and inference identity.

Workers remain private. The UI talks to `/api/v1/*` with a management JWT. Inference clients talk only to `/v1/*` with an inference or Full Access API key. Management and Full Access API keys may call `/api/v1/*` as non-user principals, except session/Playground routes. Service-account administration is allowed for a management JWT or a Full Access key (any owner); management keys cannot.

## 2. Product goals

The architecture must support:

- single-container deployment for v1;
- Go backend and Nuxt/Vue frontend;
- local `llama-server` process management;
- registered Models independent from runtime state;
- durable Instances that remain visible while stopped;
- multiple Instances referencing one Model;
- Instance-specific lifecycle/scheduler policy;
- global + Model + Instance llama.cpp configuration layers;
- stable resource IDs separated from mutable human/public slugs;
- Instance slugs as OpenAI-compatible inference identity;
- automatic Instance autoloading;
- Always-On desired state per Instance;
- NVIDIA and AMD GPU awareness;
- automatic single-GPU-first placement;
- resource-pressure eviction;
- dynamic discovery of llama.cpp CLI options;
- Hugging Face and direct URL model downloads;
- OpenAI-compatible inference endpoints;
- LiteLLM interoperability;
- local management authentication and typed owner-bound API keys;
- Prometheus metrics and per-instance logs.

## 3. Non-goals for v1

- remote hosts/agents;
- SSH/Kubernetes orchestration;
- automatic replica scaling;
- non-llama.cpp inference providers;
- cross-instance/model fallback;
- request-content-aware routing;
- storage pools/tiering;
- automatic filesystem model discovery;
- management RBAC;
- OIDC;
- GraphQL;
- management WebSockets;
- OpenTelemetry;
- centralized external log aggregation.

## 4. Runtime topology

```text
OpenAI clients / LiteLLM / applications
                  |
                  v
             HTTP /v1/*
                  |
        +---------------------+
        | LlamaRack           |
        |                     |
        | OpenAI Gateway      |
        | Instance Resolver   |
        | Lifecycle Service   |
        | Resource Scheduler  |
        | Process Supervisor  |
        | Download Manager    |
        | Auth / Metrics      |
        +----------+----------+
                   |
          loopback-only ports
          +--------+--------+
          |                 |
          v                 v
     llama-server      llama-server
      Instance A        Instance B

Browser
  |
  v
Nuxt UI -> /api/v1/* -> manager
```

Workers bind only to manager-controlled private interfaces/ports.

## 5. Core domain ownership

### 5.1 Resource identity convention

Human-addressable first-class resources use distinct fields:

- `id` — immutable durable machine identity and foreign-key target;
- `slug` — unique mutable human/public route identity;
- `name` — mutable display label.

Models and Instances follow this convention. Future human-routable first-class resources such as Nodes should follow it as well. Event rows, request IDs, jobs and metrics do not need slugs merely to conform to the pattern.

A name edit never implicitly changes an existing slug. A slug edit is explicit, collision-checked and carries a resource-specific impact warning.

### 5.2 Model

A Model is a registered management-plane resource.

It owns:

- immutable `id`;
- mutable management-route `slug`;
- mutable display `name`;
- one backing Model artifact;
- reusable llama.cpp overrides/defaults;
- model metadata such as path, size, quantization and context capability.

A Model slug has no OpenAI inference meaning. Changing it changes management URLs/bookmarks only.

A Model does **not** own:

- READY/UNLOADED state;
- Always On;
- Autoload on request;
- resource-pressure eviction policy;
- GPU placement;
- process lifecycle actions.

A Model may exist with zero Instances.

### 5.3 Instance

An Instance is a durable configured `llama-server` process definition.

It owns:

- immutable UUID `id`;
- mutable public `slug`;
- human-entered `name`;
- immutable-ID Model reference;
- Always On;
- Autoload on request;
- resource-pressure eviction policy;
- priority and applicable timing policy;
- GPU placement/tensor split;
- Instance-level llama.cpp overrides;
- observed runtime state.

Stopped Instances remain durable and visible in `/instances`.

### 5.4 Instance inference identity

Instance creation defaults its slug from the name:

```text
Instance name
   -> slugify on create
   -> instance.slug
   -> OpenAI request "model" value

instance.id
   -> immutable UUID
   -> runtime ownership / foreign keys / API-key scopes
```

Example:

```text
Name: Qwen Coding 32B
Slug: qwen-coding-32b
ID:   550e8400-e29b-41d4-a716-446655440000

POST /v1/chat/completions
{"model":"qwen-coding-32b", ...}
```

Rules:

- `instance.id` is immutable;
- `instance.slug` is unique and uses a conservative URL/JSON-safe format;
- changing `name` alone preserves both `id` and `slug`;
- changing `slug` explicitly changes the OpenAI model identifier but preserves durable ID references;
- the UI must warn that an Instance slug change is API-breaking for clients using the old model slug;
- no hidden old-slug compatibility alias is retained in v1 unless explicitly added later.

## 6. Configuration hierarchy

Effective worker configuration is:

```text
Global llama.cpp defaults
        +
Model overrides/defaults
        +
Instance overrides
        +
manager-owned protected launch values
        =
Effective Instance launch configuration
```

Manager-owned values include worker bind address, private port, model path, current Instance slug as `--alias`, and generated placement flags. Runtime/process ownership still uses immutable Instance ID.

## 7. Major backend components

### 7.1 HTTP server

Owns:

- `/v1/*` OpenAI-compatible API;
- `/api/v1/*` management API;
- Nuxt assets;
- `/metrics`.

### 7.2 OpenAI gateway / Instance resolver

Responsibilities:

- authenticate typed API keys (`sk-`); management keys are rejected on `/v1/*`;
- read the OpenAI `model` field as an Instance slug;
- resolve slug to exactly one Instance;
- enforce inference allowlists against immutable Instance IDs (Full Access keys are not allowlisted);
- capture the request's exact public model slug for historical logs;
- request autoload of that exact immutable Instance when allowed;
- proxy to the exact READY worker;
- preserve streaming;
- never expose private worker addresses.

The gateway must not silently substitute a sibling Instance that references the same Model.

### 7.3 Lifecycle service

Coordinates desired and observed Instance state by immutable Instance ID:

- start/stop/restart/kill;
- Autoload on request;
- Always-On reconciliation;
- idle unloading where enabled;
- controlled restart after Instance configuration changes;
- per-Instance single-flight startup;
- temporary manual-stop suppression for Always-On Instances.

### 7.4 Process supervisor

Only the supervisor directly spawns or terminates `llama-server` processes.

Responsibilities:

- construct launch plans from effective Instance configuration;
- use current Instance slug for managed worker alias;
- keep process/runtime ownership keyed by immutable Instance ID;
- allocate private ports;
- spawn processes;
- capture stdout/stderr;
- probe readiness/health;
- graceful terminate and hard kill;
- detect unexpected exits;
- expose observed runtime state.

### 7.5 Resource scheduler

The scheduler decides whether and where an Instance may start.

Inputs include:

- system RAM;
- GPU inventory/free VRAM;
- effective Instance memory-affecting configuration;
- Instance priority;
- Instance Always-On and eviction policy;
- last-used/active request state;
- placement/tensor split configuration;
- pending reservations.

Scheduler reservations and candidate ownership use immutable Instance IDs. The scheduler returns plans. It never directly starts/stops processes.

### 7.6 Model service

Owns registered Model CRUD, stable IDs, management slugs, artifact association, Model metadata and reusable llama.cpp configuration.

It does not own process lifecycle.

### 7.7 llama.cpp capability service

Discovers `llama-server --help`, stores versioned option metadata, validates configuration and generates deterministic argv.

### 7.8 Hardware service

Provides normalized CPU/RAM/NVIDIA/AMD state to scheduler/UI.

### 7.9 Download manager

Handles Hugging Face/direct URL discovery and downloads, resumability, split GGUFs and artifact persistence.

## 8. Management API boundaries

Human-facing management routes use resource slugs. Handlers resolve the slug at the HTTP boundary and immediately continue with immutable IDs internally.

Conceptual resource groups:

- `/api/v1/models` — registered Models;
- `/api/v1/models/{model_slug}`;
- `/api/v1/instances` — durable Instance control plane;
- `/api/v1/instances/{instance_slug}/start`;
- `/api/v1/instances/{instance_slug}/stop`;
- `/api/v1/instances/{instance_slug}/restart`;
- `/api/v1/instances/{instance_slug}/kill`;
- `/api/v1/instances/{instance_slug}/duplicate`;
- `/api/v1/downloads`;
- `/api/v1/providers/huggingface`;
- `/api/v1/hardware`;
- `/api/v1/llamacpp`;
- `/api/v1/users`;
- `/api/v1/api-keys`;
- `/api/v1/admin/service-accounts`;
- `/api/v1/settings`.

Durable API payload relationships such as `model_id`, `instance_id`, runtime ownership and API-key Instance scopes continue to use immutable IDs.

## 9. OpenAI API boundary

`GET /v1/models` represents addressable inference Instances, not registered Models.

Each returned model object's `id` is exactly `instance.slug`.

For inference endpoints, the request `model` field resolves to `instance.slug`, then to immutable `instance.id` before authorization/lifecycle work.

Registered Models remain management-plane concepts and are not directly inferable unless an Instance exists for them.

## 10. Frontend architecture

Primary navigation:

- Dashboard;
- Models;
- Instances;
- Discover;
- Downloads;
- API;
- Settings.

`/models` is inventory/configuration only. Model detail/edit navigation uses Model slugs.

`/instances` is the operational control plane. Instance detail/edit navigation uses Instance slugs. UI state that represents runtime ownership, history filters, API-key scopes or telemetry continues to carry immutable IDs.

## 11. Model creation bootstrap

`/models/new` may optionally create/start a first Instance after creating the Model.

The first-Instance section exposes:

- Instance name;
- Instance slug, defaulted from the name;
- Always On;
- Autoload on request;
- Allow resource-pressure eviction;
- whether to launch immediately.

The backend generates an immutable Instance UUID independently from the slug.

Full Instance configuration belongs to `/instances/new` and `/instances/:slug/edit`.

## 12. Running Instance edits

Runtime-affecting Instance edits require confirmation and then automatically perform a controlled restart.

```text
save configuration
-> drain by immutable Instance ID
-> stop
-> start with current slug as --alias
-> READY
```

A name-only edit does not affect inference identity. An explicit Instance slug edit additionally requires an API-breaking-change warning because it changes the OpenAI model value while preserving immutable runtime ownership.

Model slug edits require a management-bookmark warning only; they do not affect Instance inference identity.

## 13. Schema policy

LlamaRack uses embedded Goose migrations for durable schema evolution. Stable-resource-identity changes are applied transactionally and preserve durable references while introducing UUID Instance IDs and separate slugs.

Migration qualification must verify foreign-key integrity, preserved API-key scopes, runtime/provider/observability references, deterministic Model slug backfill, request-history model-slug capture, and rollback/upgrade behavior.

## 14. Startup/recovery

On manager startup:

1. initialize configuration/logging;
2. open/upgrade the Goose-managed database;
3. initialize auth state;
4. inspect llama.cpp binary/options;
5. initialize hardware collectors;
6. load registered Models and durable Instances;
7. treat old runtime observations as stale;
8. positively identify and terminate stale workers owned by this installation using immutable Instance ownership;
9. refresh hardware/resource state after orphan cleanup;
10. start HTTP services;
11. reconcile Always-On Instances unless temporarily suppressed only within the current session.

Full adoption of a surviving `llama-server` into the new manager process is not required. Ownership is proven from manager-injected identity (installation ID, immutable Instance ID, worker generation) plus process start identity. Process name, executable, model path, public slug, port, or PID alone must never cause termination. Unrelated user-run `llama-server` processes are left untouched.

If a positively identified stale worker cannot be terminated, that Instance must not receive a replacement launch. Other Instances may start.

Normal container replacement tears down all processes in the container, so startup reconciliation is a no-op. Native or process-only manager restarts can leave children alive and must run the same identify-then-terminate path. `LLAMARACK_HOST_PROC` remains telemetry PID mapping; it is not a license to kill by host PID alone.

## 15. Capability boundaries

### Model / Instance control-plane separation

Introduces durable Model/Instance separation, stable IDs with mutable slugs, `/instances`, Instance-owned lifecycle/scheduler configuration, Model defaults + Instance overrides, and exact Instance-slug routing.

### Multi-instance support

Builds remaining concurrent multi-Instance behavior on the durable Instance model.

### Hardware integration

The first task remains completing the llama.cpp options GUI. Then implement real NVIDIA/AMD hardware state, single-GPU-first placement, tensor split and actual pre-load resource-pressure eviction.

## 16. Architectural invariants

1. Clients never see worker ports.
2. Only the supervisor starts/stops worker processes.
3. Only the scheduler decides placement/eviction plans.
4. Only READY Instances receive new inference requests.
5. OpenAI `model` resolves exactly to `instance.slug`, then to one immutable Instance ID.
6. Model and Instance `id` values are immutable durable identity; slugs are mutable human/public identity.
7. API-key Instance scopes, runtime ownership, scheduler state and durable references use immutable IDs.
8. Requests are never silently rerouted to a sibling Instance.
9. Models contain no runtime lifecycle state.
10. Always On, Autoload and eviction policy are Instance-owned.
11. Persisted runtime observations are never blindly trusted after restart.
12. Historical inference rows preserve the exact captured `model_slug` independently from later Instance slug changes.
13. New llama.cpp options can appear without a manager release.
14. V1 JWT management access is authenticated but not role-differentiated. API-key principals remain typed (inference / management / full).

## 17. Acceptance criteria

The architecture is correctly implemented when:

- `/models` is registered inventory/configuration only;
- `/instances` controls durable `llama-server` Instances;
- stopped Instances remain listed;
- one Model can back multiple differently configured Instances;
- new Instance names can default public slugs while receiving independent UUID IDs;
- Instance slug is exactly the OpenAI `model` value and `/v1/models` ID;
- name-only edits preserve public slug and durable ID;
- explicit slug edits preserve durable ID/references and carry the correct impact warning;
- API-key Instance scopes survive slug changes;
- request history preserves the slug used at request time;
- a stopped autoload-enabled Instance can be started by an inference request;
- an Always-On Instance is reconciled independently of sibling Instances;
- a manual Stop can suppress Always-On reconciliation until manual Launch, inference need or manager restart;
- running Instance edits confirm and automatically restart;
- hardware integration performs real GPU-aware placement/eviction;
- management routes are slug-based while durable internals remain ID-based.
