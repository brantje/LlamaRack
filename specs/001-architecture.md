# 001 — Architecture

Status: Draft

Related issue: #1

## 1. Purpose

`llamacpp-manager` is a single-host web application that manages the complete lifecycle of llama.cpp model servers and exposes one stable OpenAI-compatible API to clients.

The application must hide individual `llama-server` processes from clients. Users interact with a Nuxt web UI and a management API; management commands and resources use REST while observed runtime lifecycle state is pushed over an authenticated WebSocket. Inference clients interact only with the unified `/v1` gateway.

## 2. Product goals

The architecture must support:

- a single-container deployment for v1;
- a Go backend and Nuxt/Vue frontend;
- local `llama-server` process management;
- user-defined public model IDs;
- automatic request routing to the correct model instance;
- optional model autoloading;
- configurable idle unloading;
- Always-On desired-state behavior;
- manual multiple instances of one model;
- NVIDIA and AMD GPU awareness;
- per-model llama.cpp configuration;
- dynamic discovery of llama.cpp CLI options;
- Hugging Face and direct URL model downloads;
- OpenAI-compatible inference endpoints;
- LiteLLM interoperability;
- local management authentication and inference API keys;
- live observed runtime lifecycle events;
- Prometheus metrics and per-instance logs.

## 3. Non-goals for v1

The following are intentionally excluded:

- remote hosts or agents;
- SSH-based worker management;
- Kubernetes orchestration;
- automatic replica scaling;
- non-llama.cpp inference providers;
- cross-model fallback chains;
- request-content-aware routing;
- storage pools or tiering;
- automatic filesystem model discovery;
- management RBAC, differentiated roles or custom permission matrices;
- OIDC;
- multiple Hugging Face identities;
- GraphQL;
- OpenTelemetry;
- centralized external log aggregation.

These features may be added later without changing the public OpenAI gateway contract.

## 4. Runtime topology

The v1 runtime consists of one `llamacpp-manager` process plus zero or more child `llama-server` processes.

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
        | Request Router      |
        | Resource Scheduler  |
        | Process Supervisor  |
        | Download Manager    |
        | Auth                |
        | Metrics             |
        +----------+----------+
                   |
          loopback-only ports
          +--------+--------+
          |                 |
          v                 v
     llama-server      llama-server
      instance A        instance B

Browser
  |
  v
Nuxt UI -> /api/v1/* -> manager
```

Workers must bind to loopback or another non-public interface controlled by the manager. The manager is the only externally exposed HTTP entry point.

## 5. Major backend components

### 5.1 HTTP server

Owns the external listener and dispatches requests into two API namespaces:

- `/v1/*` — OpenAI-compatible inference API.
- `/api/v1/*` — llamacpp-manager management API, including the authenticated runtime-event WebSocket.

It also serves the compiled Nuxt application and `/metrics`.

### 5.2 OpenAI gateway

Responsible for:

- inference API authentication;
- validating and resolving public model IDs;
- waiting for autoload where permitted;
- selecting a ready instance through the router;
- proxying normal and streaming requests;
- translating manager-level failures to OpenAI-style errors;
- recording request and token metrics;
- never exposing worker addresses to clients.

The gateway must not contain resource-placement logic. It requests capacity through the scheduler.

### 5.3 Request router

Chooses one READY instance for a resolved model.

Supported v1 policies:

- least active requests;
- round robin;
- fixed/preferred instance;
- lowest current load.

Routing is based on model ID only. The router must not inspect prompt content to choose another model.

### 5.4 Process supervisor

Owns every managed `llama-server` child process.

Responsibilities:

- construct a process launch plan from model and instance configuration;
- allocate a private worker port;
- spawn the process;
- capture stdout/stderr;
- probe readiness and health;
- terminate or kill workers when required;
- detect unexpected exits;
- expose observed runtime state and ordered runtime transitions;
- reconcile persisted configuration with actual processes after manager restart.

Only the supervisor may directly spawn or terminate workers.

### 5.5 Resource scheduler

Decides whether an instance may be started and what resources it should use.

Inputs include:

- RAM availability;
- GPU inventory and free VRAM;
- running instances;
- model memory estimate;
- model priority;
- Always-On policy;
- last-use time;
- active and queued requests;
- configured GPU assignment and tensor split.

If capacity is insufficient, the scheduler may choose eligible instances for eviction. It must not perform process operations directly; it returns a scheduling decision to the lifecycle layer/supervisor.

### 5.6 Model/lifecycle service

Coordinates desired and observed model state.

Responsibilities:

- create/update/delete model definitions;
- autoload coordination;
- Always-On reconciliation;
- idle-unload decisions;
- controlled restart after config changes;
- single-flight loading so duplicate requests do not launch duplicate workers;
- state transition validation.

### 5.7 llama.cpp capability service

Inspects the installed `llama-server` binary and stores a versioned capability description.

It must:

- identify the binary/version/build;
- execute `llama-server --help` when required;
- parse available options;
- produce a normalized option schema;
- expose the schema to validation and the UI;
- retain curated metadata for common options while preserving unknown/new options.

### 5.8 Hardware service

Collects local system and accelerator state.

V1 providers:

- system RAM/CPU;
- NVIDIA GPUs;
- AMD GPUs.

The hardware service should expose a vendor-neutral normalized model to the scheduler and UI.

### 5.9 Download manager

Coordinates model artifact acquisition.

Provider implementations:

- Hugging Face;
- direct URL.

Responsibilities:

- provider search/discovery where supported;
- download queue;
- progress and speed;
- retries;
- cancellation;
- resumable transfers when supported;
- temporary files and atomic completion;
- split-GGUF grouping;
- artifact metadata persistence.

### 5.10 Authentication

Two separate authentication surfaces exist:

1. Dashboard/management API user sessions.
2. Inference API bearer keys.

V1 intentionally has no management RBAC. Every authenticated management user has the same full management access. Role-based or capability-based authorization may be introduced after v1 as a separate feature.

### 5.11 Persistence

SQLite is the authoritative persistent store for configuration and durable application state.

SQLite stores model definitions, configuration overrides, users, API-key hashes, provider credential references/encrypted secrets, download history and other durable metadata.

High-frequency metrics do not need to be persisted in SQLite unless needed for bounded recent-history UI views.

## 6. Frontend architecture

The frontend is a Nuxt/Vue application developed independently under `web/` but compiled into static/client assets for production.

The Go application serves the built frontend. A Node.js runtime is not required in the production container.

The UI communicates only with manager-owned `/api/v1/*` endpoints; it never talks directly to a worker. REST remains the command/configuration transport. `/api/v1/ws` is an authenticated WebSocket carrying supervisor-observed runtime state. The WebSocket is authoritative while connected; REST runtime reads are used for initial hydration and disconnected recovery.

Primary UI areas:

- Dashboard;
- Models;
- Discover;
- Downloads;
- API;
- Settings.

Detailed interaction requirements are defined in `010-ui.md`.

## 7. API boundaries

### 7.1 `/v1/*`

This namespace is reserved for OpenAI compatibility. Management-specific fields must not leak into standard OpenAI response shapes unless they are explicitly supported extensions.

### 7.2 `/api/v1/*`

This namespace is owned by the management plane.

Conceptual resource groups:

- `/api/v1/models`;
- `/api/v1/models/{id}/instances`;
- `/api/v1/downloads`;
- `/api/v1/providers/huggingface`;
- `/api/v1/hardware`;
- `/api/v1/llamacpp`;
- `/api/v1/users`;
- `/api/v1/api-keys`;
- `/api/v1/settings`;
- `/api/v1/metrics`;
- `/api/v1/ws` — authenticated observed runtime lifecycle events.

The exact endpoint contract will be refined during implementation without changing component ownership.

## 8. Deployment architecture

V1 is distributed as one container image per accelerator family where needed, for example:

- CPU;
- NVIDIA/CUDA;
- AMD/ROCm.

The exact image naming is a release concern, but the runtime contract is:

- one external HTTP port;
- one persistent configuration/data directory;
- one persistent model directory;
- access to host GPUs when configured;
- bundled or otherwise known `llama-server` binary.

Docker Compose may be added later as a deployment convenience, not as a different application architecture.

## 9. Startup sequence

On manager startup:

1. initialize configuration and logging;
2. open SQLite and apply migrations;
3. validate/create bootstrap authentication state;
4. inspect the installed llama.cpp binary and option schema;
5. initialize hardware collectors;
6. recover durable model/instance definitions;
7. verify no stale runtime state is treated as live;
8. start HTTP services;
9. start lifecycle reconciliation loops;
10. restore at least one instance for every enabled Always-On model as resources permit.

The HTTP server may become available before all Always-On models finish loading, but API state must clearly report STARTING/LOADING rather than claiming readiness.

## 10. Concurrency model

The architecture must avoid globally serializing all model operations.

Required coordination scopes:

- per-model single-flight start/load operation;
- per-instance start/stop lock;
- scheduler-wide reservation/placement transaction;
- per-download state machine;
- safe round-robin counters and request counts.

A request waiting for an autoload must be cancellable when its client disconnects, but cancellation of one waiter must not automatically cancel the shared model load if other waiters or Always-On policy still require it.

## 11. Failure boundaries

### Worker failure

A `llama-server` crash must not crash the manager. The supervisor records the exit and lifecycle policy decides whether to restart.

### Model load failure

The model transitions to FAILED with a concise reason and retained logs. Waiting gateway requests fail with a compatible 5xx response.

### Download failure

A failed/incomplete artifact must never appear as a completed model file. Temporary files remain distinguishable and may be resumed or removed.

### Hardware collector failure

The manager remains usable, but scheduling that depends on unavailable resource data must fail conservatively rather than inventing capacity.

### Database failure

Durable state errors are considered manager-level failures. The application must not continue making destructive lifecycle changes if it cannot reliably persist required state.

## 12. Security boundaries

- Worker ports are private.
- Passwords use a modern password hash.
- Inference API keys are stored only as hashes after creation.
- Provider tokens are encrypted at rest and never returned in full after storage.
- Logs and API responses must redact known secrets.
- Management authentication is enforced server-side for every protected operation, including the runtime WebSocket handshake.
- User-provided model IDs, filenames and provider metadata must not be trusted as filesystem paths.
- Download destinations must remain under the configured model directory.

## 13. Observability

The manager owns operational metrics even when data originates from workers.

At minimum expose:

- manager health;
- configured/loaded model counts;
- instance state counts;
- request counts and errors;
- active and queued requests;
- load duration;
- token throughput where available;
- latency and TTFT where measurable;
- download state;
- hardware resource state.

Per-instance stdout/stderr is captured separately for UI inspection.

## 14. Repository layout

Target layout:

```text
/
├── cmd/
│   └── llamacpp-manager/
├── internal/
│   ├── api/
│   ├── auth/
│   ├── config/
│   ├── database/
│   ├── downloads/
│   ├── gateway/
│   ├── hardware/
│   ├── llamacpp/
│   ├── metrics/
│   ├── models/
│   ├── scheduler/
│   └── supervisor/
├── web/
├── migrations/
├── specs/
├── docs/
└── Dockerfile
```

Package boundaries should follow responsibilities, not mirror database tables.

## 15. Architectural invariants

The following are hard invariants:

1. Clients never need to know worker ports.
2. Only the supervisor starts/stops worker processes.
3. Only the scheduler decides resource placement/eviction plans.
4. The router chooses only among READY instances.
5. Public model IDs are stable independently of filenames and worker configuration.
6. `/v1` is compatibility-focused; `/api/v1` is management-focused.
7. Always-On is desired state, not merely a startup flag.
8. Persisted runtime state is never blindly trusted after process restart.
9. A partially downloaded model is never treated as a usable completed artifact.
10. New llama.cpp options should not require a manager release merely to become visible in Advanced configuration.
11. V1 management access is authenticated but not role-differentiated.
12. While connected, frontend runtime state is driven by supervisor-observed WebSocket events rather than optimistic lifecycle mutations.

## 16. Acceptance criteria

This architecture is considered correctly implemented when:

- one manager process can supervise multiple independent llama.cpp workers;
- the UI and clients use only manager-owned endpoints;
- model lifecycle can recover from worker crashes and manager restarts;
- observed lifecycle transitions are pushed to authenticated management clients without waiting for lifecycle command responses to finish;
- model routing and scheduling are separate services with separate responsibilities;
- Always-On and idle unload policies can operate without client awareness;
- OpenAI-compatible streaming can pass through the gateway without full-response buffering;
- a new llama.cpp option discovered from the active binary can be represented without a hard-coded backend field;
- authenticated management access works without requiring RBAC for v1;
- the system can later add remote-host support without changing public model IDs or the external `/v1` contract.
