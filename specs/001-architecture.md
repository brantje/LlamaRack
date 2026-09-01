# 001 — Architecture

Status: Draft

Related issue: #1

## 1. Purpose

`llamacpp-manager` is a single-host web application that manages `llama-server` processes and exposes one stable OpenAI-compatible API.

The architecture separates **registered Models** from **runtime Instances**:

- a **Model** is management-plane configuration for a GGUF artifact plus reusable llama.cpp defaults;
- an **Instance** is one durable configured `llama-server` process definition and is the unit of lifecycle, routing, scheduling and inference identity.

Workers remain private. The UI talks to `/api/v1/*`; inference clients talk only to `/v1/*`.

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
- Instance-name-derived inference identity;
- automatic Instance autoloading;
- Always-On desired state per Instance;
- NVIDIA and AMD GPU awareness;
- automatic single-GPU-first placement;
- resource-pressure eviction;
- dynamic discovery of llama.cpp CLI options;
- Hugging Face and direct URL model downloads;
- OpenAI-compatible inference endpoints;
- LiteLLM interoperability;
- local management authentication and inference API keys;
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
        | llamacpp-manager    |
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

### 5.1 Model

A Model is a registered management-plane resource.

It owns:

- name/identity used in the management UI;
- one backing Model artifact;
- reusable llama.cpp overrides/defaults;
- model metadata such as path, size, quantization and context capability.

A Model does **not** own:

- READY/UNLOADED state;
- Always On;
- Autoload on request;
- resource-pressure eviction policy;
- GPU placement;
- process lifecycle actions.

A Model may exist with zero Instances.

### 5.2 Instance

An Instance is a durable configured `llama-server` process definition.

It owns:

- `id`;
- human-entered name;
- Model reference;
- Always On;
- Autoload on request;
- resource-pressure eviction policy;
- priority and applicable timing policy;
- GPU placement/tensor split;
- Instance-level llama.cpp overrides;
- observed runtime state.

Stopped Instances remain durable and visible in `/instances`.

### 5.3 Instance identity

Instance identity intentionally mirrors model-style slug creation:

```text
Instance name
   -> slugify
   -> instance.id
   -> OpenAI request "model" value
```

Example:

```text
Name: Qwen Coding 32B
ID:   qwen-coding-32b

POST /v1/chat/completions
{"model":"qwen-coding-32b", ...}
```

There is no separate public Instance alias or inference-ID field.

Rules:

- `instance.id` is unique;
- it uses a conservative URL/JSON-safe slug format;
- renaming an Instance changes its slug/ID and therefore changes the OpenAI model identifier;
- the UI must warn that renaming is API-breaking for clients.

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

Manager-owned values include worker bind address, private port, model path and generated placement flags.

## 7. Major backend components

### 7.1 HTTP server

Owns:

- `/v1/*` OpenAI-compatible API;
- `/api/v1/*` management API;
- Nuxt assets;
- `/metrics`.

### 7.2 OpenAI gateway / Instance resolver

Responsibilities:

- authenticate inference API keys;
- read the OpenAI `model` field;
- resolve it directly to `instance.id`;
- request autoload of that exact Instance when allowed;
- proxy to the exact READY worker;
- preserve streaming;
- never expose private worker addresses.

The gateway must not silently substitute a sibling Instance that references the same Model.

### 7.3 Lifecycle service

Coordinates desired and observed Instance state:

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

The scheduler returns plans. It never directly starts/stops processes.

Actual hardware-aware pre-load eviction is a hardware-integration requirement.

### 7.6 Model service

Owns registered Model CRUD, artifact association, Model metadata and reusable llama.cpp configuration.

It does not own process lifecycle.

### 7.7 llama.cpp capability service

Discovers `llama-server --help`, stores versioned option metadata, validates configuration and generates deterministic argv.

### 7.8 Hardware service

Provides normalized CPU/RAM/NVIDIA/AMD state to scheduler/UI.

### 7.9 Download manager

Handles Hugging Face/direct URL discovery and downloads, resumability, split GGUFs and artifact persistence.

## 8. Management API boundaries

Conceptual resource groups:

- `/api/v1/models` — registered Models;
- `/api/v1/instances` — durable Instance control plane;
- `/api/v1/instances/{id}/start`;
- `/api/v1/instances/{id}/stop`;
- `/api/v1/instances/{id}/restart`;
- `/api/v1/instances/{id}/kill`;
- `/api/v1/instances/{id}/duplicate`;
- `/api/v1/downloads`;
- `/api/v1/providers/huggingface`;
- `/api/v1/hardware`;
- `/api/v1/llamacpp`;
- `/api/v1/users`;
- `/api/v1/api-keys`;
- `/api/v1/settings`.

## 9. OpenAI API boundary

`GET /v1/models` represents addressable inference Instances, not registered Models.

Each returned model object's `id` is exactly `instance.id`.

For all inference endpoints, the request `model` field resolves directly to `instance.id`.

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

`/models` is inventory/configuration only.

`/instances` is the operational control plane.

## 11. Model creation bootstrap

`/models/new` may optionally create/start a first Instance after creating the Model.

The first-Instance section exposes only:

- Instance name;
- Always On;
- Autoload on request;
- Allow resource-pressure eviction;
- whether to launch immediately.

The Instance name is slugified to `instance.id`.

Full Instance configuration belongs to `/instances/new` and `/instances/:id/edit`.

## 12. Running Instance edits

Runtime-affecting Instance edits require confirmation and then automatically perform a controlled restart.

```text
save configuration
-> drain
-> stop
-> start
-> READY
```

Instance rename additionally requires an API-breaking-change warning because it changes `instance.id`.

## 13. Development schema policy

The project is still in active development.

For Model/Instance control-plane separation and related development-only schema restructuring:

- modify the current schema directly;
- update fixtures/seeds/tests directly;
- development databases may be recreated;
- **do not add migration files for this work**.

A release migration policy can be established before schema compatibility becomes a product requirement.

## 14. Startup/recovery

On manager startup:

1. initialize configuration/logging;
2. open/create current development schema;
3. initialize auth state;
4. inspect llama.cpp binary/options;
5. initialize hardware collectors;
6. load registered Models and durable Instances;
7. treat old runtime observations as stale;
8. start HTTP services;
9. reconcile Always-On Instances unless temporarily suppressed only within the current session (suppression therefore does not survive restart).

## 15. Capability boundaries

### Model / Instance control-plane separation

Introduces the durable separation, Instance identity, `/instances`, Instance-owned lifecycle/scheduler configuration, Model defaults + Instance overrides, and direct Instance routing.

No migrations are required.

### Multi-instance support

Builds remaining concurrent multi-Instance behavior on the durable Instance model.

### Hardware integration

The first task remains completing the llama.cpp options GUI. Then implement real NVIDIA/AMD hardware state, single-GPU-first placement, tensor split and actual pre-load resource-pressure eviction.

## 16. Architectural invariants

1. Clients never see worker ports.
2. Only the supervisor starts/stops worker processes.
3. Only the scheduler decides placement/eviction plans.
4. Only READY Instances receive new inference requests.
5. OpenAI `model` resolves exactly to `instance.id`.
6. `instance.id` is the slug derived from Instance name; there is no second public alias field.
7. Requests are never silently rerouted to a sibling Instance.
8. Models contain no runtime lifecycle state.
9. Always On, Autoload and eviction policy are Instance-owned.
10. Persisted runtime observations are never blindly trusted after restart.
11. New llama.cpp options can appear without a manager release.
12. V1 management access is authenticated but not role-differentiated.

## 17. Acceptance criteria

The architecture is correctly implemented when:

- `/models` is registered inventory/configuration only;
- `/instances` controls durable `llama-server` Instances;
- stopped Instances remain listed;
- one Model can back multiple differently configured Instances;
- Instance names produce unique slug IDs;
- that slug is exactly the OpenAI `model` value and `/v1/models` ID;
- a stopped autoload-enabled Instance can be started by an inference request;
- an Always-On Instance is reconciled independently of sibling Instances;
- a manual Stop can suppress Always-On reconciliation until manual Launch, inference need or manager restart;
- running Instance edits confirm and automatically restart;
- hardware integration performs real GPU-aware placement/eviction;
- no control-plane-separation migration files are introduced.