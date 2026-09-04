# LlamaRack

**A self-hosted control plane and OpenAI-compatible gateway for `llama.cpp`.**

LlamaRack manages GGUF models, durable `llama-server` Instances, GPU placement, automatic loading and unloading, Hugging Face downloads, request observability, and a stable OpenAI-compatible API from one web interface.

[![CI](https://github.com/brantje/LlamaRack/actions/workflows/ci.yml/badge.svg)](https://github.com/brantje/LlamaRack/actions/workflows/ci.yml)
[![Container](https://img.shields.io/badge/container-GHCR-blue)](https://github.com/brantje/LlamaRack/pkgs/container/llamarack)
[![Go](https://img.shields.io/badge/backend-Go-00ADD8?logo=go&logoColor=white)](./backend)
[![Nuxt](https://img.shields.io/badge/frontend-Nuxt-00DC82?logo=nuxtdotjs&logoColor=white)](./frontend)

> [!NOTE]
> LlamaRack is under active development. Interfaces, configuration and persistence may still change before a stable release.

---

## Why LlamaRack?

`llama-server` is excellent at serving a model.

LlamaRack handles the control plane around it.

Instead of manually starting individual servers, assigning ports, tracking GPU memory, downloading artifacts and pointing clients at different endpoints, you run one manager:

```text
                    OpenAI SDK
                       LiteLLM
                     other clients
                          │
                          ▼
                ┌──────────────────┐
                │    LlamaRack     │
                │      /v1/*       │
                └────────┬─────────┘
                         │
          ┌──────────────┼──────────────┐
          ▼              ▼              ▼
    llama-server    llama-server    llama-server
      Instance A      Instance B      Instance C
          │              │              │
          └──────────────┼──────────────┘
                         ▼
                    CPU / GPU(s)
```

The manager resolves the requested Instance, starts it when necessary, waits for readiness, coordinates lifecycle and resource pressure, tracks inference activity, and proxies the response through the same stable API.

`llama.cpp` remains the inference engine. LlamaRack is the control plane around it.

---

## Features

### Model management

Register and inspect local GGUF models without loading them into `llama-server`.

- Register existing GGUF files.
- Recursively discover GGUFs in the configured model directory.
- Keep **Models** separate from running **Instances**.
- Read GGUF metadata without loading the model.
- Detect architecture and context capability where metadata permits.
- Browse complete GGUF metadata as generic key / type / value data.
- Support split GGUF artifacts.
- Detect companion artifacts such as vision projectors (`mmproj`) and MTP / draft models.
- Configure reusable llama.cpp defaults per Model.

A Model represents an artifact and its reusable configuration. It does not have runtime state.

### Hugging Face discovery

Search, inspect and download GGUF models without leaving LlamaRack.

- Search Hugging Face repositories.
- Open repositories through URL-addressable routes.
- Filter by author.
- Sort by trending, likes, downloads, creation date or update date.
- Browse results with infinite scrolling.
- Inspect GGUF quantizations and shards.
- Detect incomplete split artifacts.
- Detect companion files.
- Authenticate for gated or private repositories.

LlamaRack also turns quantization names such as `Q4_K_M` and `Q6_K` into hardware-oriented guidance.

Depending on available metadata and hardware information, an artifact can show:

- quality / memory trade-offs;
- estimated memory requirements;
- single-GPU fit;
- multi-GPU requirements;
- GPU + CPU offload options;
- CPU-only fallback;
- suggested GPU placement;
- estimated generation-speed ranges;
- a recommended artifact for the current machine.

Recommendations can be recalculated for different context sizes before downloading. These are estimates, not controlled benchmark results.

### Managed downloads

Model downloads run in the manager rather than the browser.

- Background GGUF downloads.
- Multi-file and split-GGUF jobs.
- Live progress and per-file progress.
- Download speed and ETA.
- Cancellation.
- Retry of failed or cancelled jobs.
- Resume when the remote transfer supports it.
- Partial files kept separate from usable model artifacts.
- Download history.

You can start an import from Discover and continue using the rest of the UI while it runs.

### Durable llama.cpp Instances

A **Model** describes a GGUF artifact.

An **Instance** describes one durable `llama-server` configuration using that Model.

One Model can therefore be exposed through several independently configured Instances:

```text
Model
└── Qwen3-30B-A3B-Q4_K_M.gguf
    ├── qwen-chat
    │   ├── 32K context
    │   ├── GPU 0
    │   └── Always On
    │
    └── qwen-long-context
        ├── 128K context
        ├── GPU 0 + GPU 1
        └── Autoload on request
```

Instance lifecycle operations include:

- Launch
- Stop
- Restart
- Kill
- Duplicate
- Reconfigure
- View logs
- Delete

Stopped Instances remain registered and addressable. The Instance slug is also its exact public OpenAI `model` ID.

### Automatic loading and idle unload

Instances do not have to remain in memory permanently.

An Instance can automatically start when an inference request targets it:

```text
POST /v1/chat/completions
model=qwen-chat
        │
        ▼
Is qwen-chat READY?
   │            │
  yes           no
   │            │
   │      Autoload enabled?
   │         │        │
   │        yes       no
   │         │        │
   │         ▼        └── error
   │    start Instance
   │         │
   │    wait for READY
   │         │
   └─────────┴──────────────► proxy request
```

Concurrent requests for the same stopped Instance share the same startup operation rather than spawning duplicate workers.

Instances can be configured as:

- **Always On** — keep the Instance running when resources permit.
- **On demand** — launch manually or through autoload.
- **Idle unloaded** — stop after a configurable period without inference traffic.

A manual stop of an Always-On Instance is respected instead of immediately being undone by reconciliation.

### Resource-aware scheduling

LlamaRack manages GPU placement and resource-pressure eviction around durable Instances.

Scheduling includes:

- RAM and VRAM hardware snapshots;
- automatic GPU placement;
- single-GPU-first placement when an Instance fits;
- manual GPU selection;
- multi-GPU placement;
- tensor split;
- GPU/CPU offloading;
- Instance priority;
- configurable resource-pressure eviction;
- active-request protection;
- lifecycle coordination around placement and eviction.

When another Instance needs memory, eligible idle Instances may be stopped according to scheduler policy.

**Always On and eviction protection are intentionally separate settings.**

| Always On | Evictable | Behaviour |
| --- | --- | --- |
| No | Yes | On demand and may be evicted |
| No | No | On demand but protected once loaded |
| Yes | Yes | Keep running when possible, but yield under pressure |
| Yes | No | Keep running and protect from normal pressure eviction |

Active inference requests are not selected as normal eviction victims.

### OpenAI-compatible inference gateway

Applications talk to LlamaRack rather than to private `llama-server` worker ports.

Supported public routes include:

```text
GET    /v1/models
GET    /v1/models/{model}

POST   /v1/chat/completions
POST   /v1/completions

POST   /v1/responses
GET    /v1/responses/{response_id}
DELETE /v1/responses/{response_id}
GET    /v1/responses/{response_id}/input_items
POST   /v1/responses/{response_id}/cancel
POST   /v1/responses/input_tokens

POST   /v1/embeddings
POST   /v1/audio/transcriptions
```

llama.cpp-compatible extensions include:

```text
POST /v1/chat/completions/input_tokens
POST /v1/chat/completions/control
POST /v1/rerank
POST /v1/reranking
```

Endpoint capabilities ultimately depend on the active llama.cpp build and effective Instance configuration.

The `model` value always resolves one exact Instance:

```json
{
  "model": "qwen-chat",
  "messages": [
    {
      "role": "user",
      "content": "Why is the sky blue?"
    }
  ]
}
```

LlamaRack never silently redirects a request to another Instance merely because it uses the same underlying Model.

Streaming responses are forwarded incrementally, and client cancellation propagates to the active request.

### Responses API persistence and control

LlamaRack supports manager-side tracking for Responses API operations while continuing to use llama.cpp for generation.

For Instances using full request logging, LlamaRack can retain enough request/response data to support response retrieval, deletion and input-item inspection. In-flight Responses can be cancelled through the public API.

Metadata-only logging deliberately does not retain response bodies merely to make later Responses retrieval possible. Stored Responses reuse the normal inference request history rather than maintaining a second persistence system.

### Inference API keys

The public inference API uses manager-generated Bearer keys.

Keys can be:

- created;
- named;
- disabled;
- re-enabled;
- rotated;
- revoked.

Plaintext secrets are only returned when a key is created or rotated.

Inference keys authenticate **clients**. They are separate from management-user authentication.

### OpenAI SDK example

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:8888/v1",
    api_key="your-api-key",
)

response = client.chat.completions.create(
    model="qwen-chat",
    messages=[
        {
            "role": "user",
            "content": "Explain speculative decoding in two sentences."
        }
    ],
)

print(response.choices[0].message.content)
```

The same endpoint can be used by LiteLLM and other OpenAI-compatible clients.

### Playground

LlamaRack includes an operator Playground for sending real inference requests through the public gateway.

The Playground deliberately uses the same `/v1/chat/completions` path and API-key authentication as an external client. It does not talk directly to a private worker, so requests exercise the real Instance resolution, autoload, scheduling, logging and metrics paths.

The Playground provides:

- Instance selection;
- editable inference parameters;
- a conversation thread;
- streaming generation;
- in-flight Stop;
- raw request and response inspection;
- equivalent curl generation;
- SDK examples;
- lifecycle and inference diagnostics.

The Playground is **not a benchmark tool**. Its measurements describe real requests through the control plane. Controlled performance testing belongs in the planned benchmarking facility.

### Observability

LlamaRack observes both control-plane lifecycle and inference traffic.

The Dashboard includes live information about:

- Instance states;
- loaded, loading and failed Instances;
- RAM usage;
- GPU and VRAM usage;
- per-Instance VRAM attribution;
- active and queued requests;
- request volume;
- prompt and generated tokens;
- autoloads;
- model-load duration;
- request failures;
- recent inference traffic.

Per-Instance runtime telemetry can include:

- PID;
- private worker port;
- lifecycle state;
- uptime;
- assigned GPUs;
- GPU utilization;
- VRAM;
- CPU;
- RAM;
- llama.cpp runtime metrics.

### Request logs and tracing

Inference request history is kept separately from raw `llama-server` process output.

Request records can include:

- request ID;
- trace ID;
- session ID;
- Instance;
- endpoint / call type;
- status;
- API-key alias;
- streaming state;
- prompt tokens;
- generated tokens;
- total tokens;
- time to first token;
- prompt-processing speed;
- generation speed;
- queue time;
- model-load time;
- total duration;
- autoload state;
- client metadata;
- sanitized failures.

LiteLLM-compatible trace/session correlation can group related traffic while individual requests retain their own request identity.

Per-Instance request logging can remain metadata-only or retain request/response payloads when explicitly configured.

### Raw Instance logs

Every managed `llama-server` has captured process output available through the UI and management API.

Request history and worker logs deliberately answer different questions:

```text
Request logs
    └── What happened to an inference request?

Instance logs
    └── What did llama-server print?
```

### Metrics, health and OpenAPI

Operational endpoints include:

```text
GET /health
GET /metrics
GET /openapi.json
GET /docs
```

`/metrics` exposes Prometheus-format manager metrics and can be protected with its own configured authentication token.

The OpenAPI document is generated by the running application rather than maintained as a separate static YAML file.

### Management authentication

Management authentication is independent from inference API keys.

Currently supported:

- first-user/bootstrap local account;
- local username/password login;
- bearer-based management authentication;
- active management sessions;
- password changes;
- session revocation;
- login protection / lockout settings;
- configurable OpenID Connect providers;
- multiple OIDC providers;
- provider discovery and connection testing;
- optional OIDC user provisioning/linking.

Secrets such as Hugging Face tokens and OIDC client secrets remain server-side and are not returned as plaintext after storage.

### llama.cpp configuration

LlamaRack manages llama.cpp without hiding its native configuration model.

Available `llama-server` options are discovered from the installed binary.

Configuration is layered:

```text
Global llama.cpp defaults
          +
Model defaults
          +
Instance overrides
          =
Effective llama-server configuration
```

This allows common defaults to be configured once while retaining per-Instance tuning.

Manager-owned values such as worker bind address, private port and model path remain under manager control.

### Web control plane

The Nuxt management interface provides operational surfaces for:

- Dashboard;
- Models and GGUF metadata;
- Instances and Instance details;
- Instance configuration;
- Hugging Face Discover;
- Downloads;
- API keys and integration information;
- request logs;
- system and worker logs;
- Playground;
- Administration;
- Profile.

The current interface uses a flat control-plane-oriented design with separate dark and light themes. Dark is the default, and the selected theme persists across sessions.

---

## Quick start

### Requirements

- Docker
- Docker Compose
- For NVIDIA GPU acceleration:
  - NVIDIA driver
  - NVIDIA Container Toolkit

Clone LlamaRack:

```bash
git clone https://github.com/brantje/LlamaRack.git
cd LlamaRack
mkdir -p data/config data/models
```

### CPU

```bash
docker compose up -d
```

### NVIDIA / CUDA

```bash
docker compose \
  -f docker-compose.yml \
  -f docker-compose.nvidia.yml \
  up -d
```

Then open:

```text
http://localhost:8888
```

On first launch, create the initial management account.

Published images run as unprivileged UID/GID `1000:1000` by default. Set `PUID` and `PGID` when bind-mounted directories need to match another host user, for example:

```bash
PUID=$(id -u) PGID=$(id -g) docker compose up -d
```

Set `LLAMARACK_IMAGE_TAG` or `LLAMARACK_NVIDIA_IMAGE_TAG` to pin a specific published image instead of `latest` / `latest-cuda`.

---

## Persistent data

The default Compose configuration stores data under:

```text
./data/
├── config/
└── models/
```

Mounted inside the container as:

```text
/config
/models
```

The configuration volume contains manager persistence, application configuration and stored credentials. GGUF artifacts live under the models volume.

### Database backup and upgrades

SQLite state lives at `{dataDir}/manager.db` (default `/config/manager.db` in containers). Before upgrading LlamaRack across releases:

1. stop the manager process;
2. copy `manager.db` and any `manager.db-wal` / `manager.db-shm` sidecars from the configuration volume;
3. restore those files before starting the upgraded build.

Schema upgrades run automatically during startup through embedded Goose migrations. Unsupported legacy databases and databases created by a newer release than the running binary fail startup instead of wiping data.

---

## Common configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `LLAMARACK_LISTEN_ADDR` | `:8000` | Internal HTTP listen address |
| `LLAMARACK_DATA_DIR` | `/config` | Persistent manager data |
| `LLAMARACK_MODELS_DIR` | `/models` | GGUF model storage |
| `LLAMARACK_DATABASE_PATH` | `{dataDir}/manager.db` | SQLite database path |
| `LLAMARACK_LLAMA_SERVER` | `/app/llama-server` | Managed `llama-server` binary |
| `LLAMARACK_HUGGINGFACE_BASE_URL` | `https://huggingface.co` | Hugging Face API base URL |
| `LLAMARACK_WORKER_HOST` | `127.0.0.1` | Bind address for managed workers |
| `LLAMARACK_WORKER_PORT_START` | `10000` | First private worker port |
| `LLAMARACK_STARTUP_TIMEOUT_SECONDS` | `180` | Worker startup timeout |
| `LLAMARACK_ALWAYS_ON_RECONCILE_SECONDS` | `15` | Always-On reconciliation interval |
| `LLAMARACK_ALLOWED_ORIGIN` | `http://localhost:3000` | Allowed browser origin |
| `LLAMARACK_HOST_PROC` | `/host/proc` | Host process information for telemetry |
| `LLAMARACK_FRONTEND_DIR` | `/app/frontend` | Built frontend assets |
| `LLAMARACK_SESSION_LIFETIME_SECONDS` | `86400` | Management JWT lifetime |
| `LLAMARACK_LOGIN_PROTECTION_ENABLED` | `true` | Login lockout protection |
| `LLAMARACK_LOGIN_FAILURE_THRESHOLD` | `5` | Failures before lockout |
| `LLAMARACK_LOGIN_LOCKOUT_SECONDS` | `900` | Login lockout duration |
| `LLAMARACK_TRUSTED_PROXIES` | empty | Trusted reverse-proxy CIDRs |
| `LLAMARACK_EXTERNAL_URL` | empty | Public URL used for OIDC redirects |
| `LLAMARACK_PROMETHEUS_AUTH_TOKEN` | empty | Optional `/metrics` Bearer token |
| `LLAMARACK_IMAGE_TAG` | `latest` | CPU container image tag |
| `LLAMARACK_NVIDIA_IMAGE_TAG` | `latest-cuda` | NVIDIA container image tag |
| `LLAMARACK_HOST_MODELS_DIR` | `./data/models` | Host model directory for Compose |
| `PUID` | `1000` | Container user ID |
| `PGID` | `1000` | Container group ID |

The normal Compose service is exposed on host port `8888`.

### Docker GPU telemetry

The Compose configuration mounts host `/proc` read-only at `/host/proc` and sets `LLAMARACK_HOST_PROC=/host/proc`. This lets telemetry map GPU-tool host PIDs back to managed worker processes without disabling normal container PID isolation.

Replacing the container normally tears down every process in that PID namespace, so startup worker reconciliation finds nothing and is a no-op. A native or process-only manager restart can leave managed `llama-server` children running. On the next start LlamaRack proves ownership from installation/generation identity and process start time, terminates those stale owned workers, refreshes hardware state, then starts replacements. Unrelated user-run `llama-server` processes are never killed from executable name, PID, port, or model path alone. `LLAMARACK_HOST_PROC` is not used as a kill handle.

For NVIDIA deployments, NVIDIA Container Toolkit supplies `nvidia-smi`, compatible driver libraries and GPU devices to the container. The application image does not install a host-specific NVIDIA driver.

To verify the published NVIDIA deployment:

```bash
docker compose \
  -f docker-compose.yml \
  -f docker-compose.nvidia.yml \
  exec llamarack nvidia-smi
```

And verify llama.cpp sees the same devices:

```bash
docker compose \
  -f docker-compose.yml \
  -f docker-compose.nvidia.yml \
  exec llamarack /app/llama-server --list-devices
```

`--list-devices` must contain the devices LlamaRack will pass to `--device`.

---

## Models vs Instances

This distinction is fundamental to LlamaRack.

### Model

A Model is a registered GGUF artifact plus reusable defaults.

It owns information such as:

- name;
- GGUF path;
- artifact size;
- quantization;
- context capability;
- GGUF metadata;
- Model-level llama.cpp defaults.

A Model does **not** own runtime state.

### Instance

An Instance is a durable configuration for one `llama-server` process.

It owns:

- name and slug;
- lifecycle policy;
- Always-On policy;
- autoload policy;
- idle timeout;
- eviction policy;
- scheduling priority;
- GPU placement;
- tensor split;
- Instance-level llama.cpp overrides.

The Instance slug is also the exact OpenAI `model` ID.

One GGUF can therefore be exposed in several configurations without duplicating the model file.

---

## Architecture

```text
┌──────────────────────────────────────────────────────────┐
│                       LlamaRack                          │
│                                                          │
│  ┌─────────────┐  ┌──────────────┐  ┌────────────────┐ │
│  │ Nuxt Web UI │  │ Management   │  │ OpenAI /v1    │ │
│  │             │  │ API          │  │ Gateway        │ │
│  └──────┬──────┘  └──────┬───────┘  └───────┬────────┘ │
│         │                │                  │            │
│         └────────────────┼──────────────────┘            │
│                          ▼                               │
│              ┌────────────────────────┐                  │
│              │ Lifecycle / Scheduler  │                  │
│              └───────────┬────────────┘                  │
│                          │                               │
│          ┌───────────────┼───────────────┐               │
│          ▼               ▼               ▼               │
│      Instance A      Instance B      Instance C           │
│      llama-server    llama-server    llama-server         │
│                                                          │
│ SQLite · Downloads · HF · Telemetry · Request history    │
└──────────────────────────────────────────────────────────┘
```

The production image contains the Go backend, built Nuxt frontend and managed llama.cpp runtime.

Individual `llama-server` processes use private local ports and are not exposed as the public API surface.

---

## Repository layout

```text
.
├── backend/       Go control plane, gateway and runtime services
├── frontend/      Nuxt management interface
├── specs/         Architecture and feature specifications
├── Dockerfile
├── docker-compose.yml
├── docker-compose.nvidia.yml
├── docker-compose.dev.yml
└── docker-compose.dev.nvidia.yml
```

The `specs/` directory contains design contracts for important behaviour such as Models vs Instances, lifecycle, scheduling and OpenAI compatibility.

---

## Development

### Backend

```bash
cd backend
go test ./...
go test -race ./...
go vet ./...
```

### Development containers

CPU:

```bash
mkdir -p data/config data/models
docker compose -f docker-compose.dev.yml up --build
```

Open:

- Frontend: http://localhost:3000
- Backend: http://localhost:8888
- Health: http://localhost:8888/health

For NVIDIA development:

```bash
docker compose \
  -f docker-compose.dev.yml \
  -f docker-compose.dev.nvidia.yml \
  up -d --build
```

Read [`AGENTS.md`](./AGENTS.md) before larger implementation changes. The project keeps implementation, tests and specifications closely aligned.

---

## Project status

The major single-node control-plane foundation is implemented, including:

- Model registry and GGUF inspection;
- durable multi-Instance lifecycle;
- redesigned web control plane with dark and light themes;
- operator Playground;
- OpenAI-compatible gateway with Responses API support;
- inference API keys;
- autoload, idle unload and Always-On reconciliation;
- GPU placement and resource-pressure eviction;
- Hugging Face discovery and managed downloads;
- hardware recommendations;
- local management authentication and OIDC;
- request observability and tracing;
- metrics and runtime OpenAPI documentation.

The project is still moving quickly and is not yet considered API-stable.

---

## Roadmap

Planned and ongoing work includes:

- Controlled `llama-bench` benchmarking and hardware-specific configuration recommendations.
- Persistent prompt/KV cache across Instance restarts once upstream llama.cpp support is reliable.
- Multi-node / clustered operation.
- Faster local GGUF discovery and metadata indexing.
- Continued OpenAI, LiteLLM, multimodal and llama.cpp compatibility testing.

---

## Home Assistant Local OpenAI LLM

Home Assistant's Local OpenAI LLM integration can group Assist requests into one LlamaRack request-log session without a LlamaRack-specific transport.

Enable **Pass conversation session ID to LiteLLM server**. The integration sends the Assist conversation ID as `metadata.session_id`, which LlamaRack uses as the request `session_id` while individual requests retain their own trace identity.

---

## What LlamaRack is not

LlamaRack is not another model format, inference engine or hosted AI platform.

Its current focus is:

- local/self-hosted `llama.cpp`;
- GGUF models;
- durable `llama-server` lifecycle;
- resource-aware local execution;
- one predictable inference API;
- an operational control plane around those workers.

`llama.cpp` does the inference. LlamaRack manages everything around it.

---

## Contributing

Issues, testing reports and pull requests are welcome.

For larger changes, first check:

- [existing issues](https://github.com/brantje/LlamaRack/issues);
- [`specs/`](./specs);
- [`AGENTS.md`](./AGENTS.md);
- [`CONTRIBUTING.md`](./CONTRIBUTING.md).

Many control-plane behaviours have explicit contracts, particularly around lifecycle, scheduling and OpenAI compatibility.

Useful information in bug reports includes:

- LlamaRack version / commit;
- llama.cpp version;
- CPU / GPU configuration;
- relevant Instance configuration;
- manager logs;
- raw Instance logs where applicable;
- request ID, trace ID or session ID for inference problems.

---

<p align="center">
  <strong>llama.cpp is the runtime. LlamaRack is the control plane.</strong>
</p>
