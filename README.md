# LlamaCPP Manager

**A self-hosted control plane and OpenAI-compatible gateway for `llama.cpp`.**

Manage GGUF models, run multiple `llama-server` instances, automatically load and unload models, schedule GPU resources, download models from Hugging Face, and expose everything through one stable OpenAI-compatible API.

[![CI](https://github.com/brantje/llamacpp-manager/actions/workflows/ci.yml/badge.svg)](https://github.com/brantje/llamacpp-manager/actions/workflows/ci.yml)
[![Container](https://img.shields.io/badge/container-GHCR-blue)](https://github.com/brantje/llamacpp-manager/pkgs/container/llamacpp-manager)
[![Go](https://img.shields.io/badge/backend-Go-00ADD8?logo=go&logoColor=white)](./backend)
[![Nuxt](https://img.shields.io/badge/frontend-Nuxt-00DC82?logo=nuxtdotjs&logoColor=white)](./frontend)

> [!NOTE]
> LlamaCPP Manager is under active development. Interfaces, configuration and persistence may still change before a stable release.

---

## Why LlamaCPP Manager?

`llama-server` is excellent at serving a model.

LlamaCPP Manager takes care of everything around it.

Instead of manually starting individual servers, assigning ports, tracking GPU memory and wiring clients to different endpoints, you run one manager:

```text
                    OpenAI SDK
                       LiteLLM
                     other clients
                          │
                          ▼
                ┌──────────────────┐
                │ LlamaCPP Manager │
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

The manager decides which Instance should handle a request, starts it when necessary, waits for it to become ready, protects active requests, tracks hardware usage and proxies the response back through the same stable API.

---

## Features

### 🧠 Model management

- Register local GGUF models.
- Recursively discover GGUF files from your model directory.
- Keep **Models** separate from running **Instances**.
- Read GGUF metadata without loading the model.
- Automatically detect model architecture and context capability where available.
- Browse raw GGUF metadata as generic key / type / value data.
- Support split GGUF artifacts.
- Detect companion artifacts such as vision projectors (`mmproj`) and MTP / draft models.
- Configure reusable llama.cpp defaults per Model.

### 🔎 Hugging Face discovery

Search and download GGUF models without leaving the manager.

- Search Hugging Face repositories.
- Open repositories through URL-addressable routes.
- Filter by author.
- Sort by trending, likes, downloads, creation date or update date.
- Infinite scrolling.
- Inspect available GGUF quantizations and shards.
- Detect incomplete split artifacts.
- Detect companion model files.
- Use authenticated Hugging Face access for gated/private repositories.

LlamaCPP Manager also turns quantization names such as `Q4_K_M` or `Q6_K` into more useful guidance instead of expecting every user to already understand GGUF quantization naming.

For each artifact it can show:

- quality / memory trade-off;
- estimated memory requirements;
- whether it fits on one GPU;
- whether it requires multiple GPUs;
- whether GPU + CPU offload is required;
- CPU-only fallback;
- suggested GPU placement;
- estimated generation-speed range where enough hardware data is available;
- a recommended artifact for the current machine.

Recommendations can be recalculated for different context sizes before downloading the model.

### 📦 Managed downloads

Downloads are handled by the manager rather than the browser.

- Background GGUF downloads.
- Multi-file and split-GGUF jobs.
- Live progress updates.
- Per-file progress.
- Download speed and ETA.
- Cancel jobs.
- Retry interrupted/failed jobs.
- Resume supported transfers.
- Keep partial downloads separate from usable model artifacts.
- Download history.

You can start a model import from Discover and continue using the rest of the UI while the transfer runs.

### ⚙️ Durable llama.cpp Instances

A **Model** describes a GGUF artifact.

An **Instance** describes one durable `llama-server` configuration using that Model.

A single Model can therefore have multiple independently configured Instances.

For example:

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

Each Instance supports lifecycle operations such as:

- Launch
- Stop
- Restart
- Kill
- Duplicate
- Reconfigure
- View logs
- Delete

Stopped Instances remain registered and addressable.

### 🔄 Automatic model loading

Instances do not have to stay in VRAM forever.

An Instance can be configured to automatically start when an inference request targets it:

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

Concurrent requests for the same stopped Instance coordinate around the same startup rather than spawning duplicate workers.

Instances can also be:

- **Always On** — reconcile back to running when resources permit.
- **On demand** — start manually or through autoload.
- **Idle unloaded** — automatically stop after a configurable period without inference traffic.

A manual stop of an Always-On Instance remains respected instead of immediately restarting it.

### 🎛 Resource-aware scheduling

LlamaCPP Manager understands that running a model is ultimately a resource-allocation problem.

The scheduler considers:

- available VRAM;
- host RAM;
- Model requirements;
- configured context size;
- Instance priority;
- GPU placement constraints;
- active inference requests;
- eviction policy;
- currently loaded Instances.

Automatic GPU placement prefers a single GPU when the Instance safely fits there instead of unnecessarily spreading the model across every visible device.

For larger models you can use:

- manual GPU selection;
- multi-GPU placement;
- tensor split;
- GPU/CPU offloading.

When another Instance needs memory, eligible idle Instances may be stopped to make room.

**Always On and eviction protection are intentionally separate settings.**

| Always On | Evictable | Behaviour |
| --- | --- | --- |
| No | Yes | On demand and may be evicted |
| No | No | On demand but protected once loaded |
| Yes | Yes | Keep running when possible, but yield under pressure |
| Yes | No | Keep running and protect from normal pressure eviction |

Active inference requests are not selected as normal eviction victims.

### 📡 One OpenAI-compatible endpoint

Applications talk to the manager, not directly to individual `llama-server` processes.

Supported public endpoints include:

```text
GET  /v1/models
POST /v1/chat/completions
POST /v1/completions
POST /v1/responses
POST /v1/embeddings
```

The `model` value is the Instance slug:

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

That ID maps to one exact Instance. The manager does not silently redirect a request to another Instance just because it happens to use the same underlying Model.

Streaming responses are proxied transparently.

### 🔑 Inference API keys

The public inference API uses manager-generated Bearer keys.

Keys can be:

- created;
- named;
- disabled;
- re-enabled;
- rotated;
- revoked.

Plaintext is only returned when a new secret is created or rotated.

Inference API keys authenticate **clients**, not management users.

### 🔌 OpenAI SDK example

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:8888/v1",
    api_key="your-lcm-api-key",
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

### 📊 Observability

LlamaCPP Manager observes both the control plane and inference traffic.

The Dashboard includes live information about:

- Instance states;
- loaded / loading / failed Instances;
- RAM usage;
- GPU and VRAM usage;
- per-Instance VRAM attribution;
- active and queued requests;
- request volume;
- prompt and generated tokens;
- autoloads;
- load duration;
- request failures;
- recent inference traffic.

Per-Instance runtime telemetry includes data such as:

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

### 🧾 Request logs and tracing

Inference requests are recorded separately from raw `llama-server` process logs.

Request history can include:

- request ID;
- Instance;
- endpoint / call type;
- status;
- API-key alias;
- streaming state;
- prompt tokens;
- generated tokens;
- total tokens;
- time to first token;
- prompt processing speed;
- generation speed;
- queue time;
- model load time;
- total duration;
- whether the Instance had to autoload;
- client metadata;
- sanitized failures.

Requests can also carry LiteLLM-compatible trace/session correlation so related requests can be inspected together.

Per-Instance logging can remain metadata-only or retain request/response payloads when explicitly configured.

### 🖥 Raw Instance logs

Every managed `llama-server` has captured process output available through the UI and management API.

This is intentionally separate from inference request history:

```text
Request logs
    └── What happened to an API request?

Instance logs
    └── What did llama-server print?
```

### 📈 Metrics, health and OpenAPI

The manager exposes operational endpoints for integrations and automation:

```text
GET /health
GET /metrics
GET /openapi.json
GET /docs
```

`/metrics` exposes manager observability in Prometheus format and can be protected with its own configured authentication token.

The OpenAPI document is generated by the running application rather than maintained as a separate static YAML file.

### 🔐 Management authentication

The management UI is separate from inference API-key authentication.

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

Provider secrets such as Hugging Face tokens and OIDC client secrets are kept server-side and are not returned as plaintext after storage.

### 🧩 llama.cpp configuration without hiding llama.cpp

LlamaCPP Manager is intended to manage llama.cpp, not replace its configuration model with an incompatible abstraction.

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

This allows common defaults to be configured once while still exposing Instance-specific tuning.

Manager-owned options such as worker bind address, private port and model path remain under manager control.

---

## Quick start

### Requirements

- Docker
- Docker Compose
- For NVIDIA GPU acceleration:
  - NVIDIA driver
  - NVIDIA Container Toolkit

Clone the repository:

```bash
git clone https://github.com/brantje/llamacpp-manager.git
cd llamacpp-manager
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

Set `LCM_IMAGE_TAG` or `LCM_NVIDIA_IMAGE_TAG` to pin a specific published image instead of `latest` / `latest-cuda`.

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

The manager database, application configuration and credentials live under the configuration volume.

GGUF files live under the models volume.

---

## Common configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `LCM_LISTEN_ADDR` | `:8000` | Internal HTTP listen address |
| `LCM_DATA_DIR` | `/config` | Persistent manager data |
| `LCM_MODELS_DIR` | `/models` | GGUF model storage |
| `LCM_LLAMA_SERVER` | `/app/llama-server` | Managed `llama-server` binary |
| `LCM_HOST_PROC` | `/host/proc` | Host process information used for telemetry |
| `LCM_ALWAYS_ON_RECONCILE_SECONDS` | `15` | Always-On reconciliation interval |
| `PUID` | `1000` | Container user ID |
| `PGID` | `1000` | Container group ID |

The normal Compose service listens on host port `8888`.

### Docker GPU telemetry

The provided Compose files mount host `/proc` read-only at `/host/proc` and set `LCM_HOST_PROC=/host/proc`. This lets the telemetry collector map GPU-tool host PIDs back to managed `llama-server` processes inside Docker without disabling normal PID isolation.

For NVIDIA deployments, `nvidia-smi` and the host-compatible NVML libraries should be injected by NVIDIA Container Toolkit. The project does not install a distribution-specific NVIDIA driver inside the application image.

If GPU telemetry works but `llama-server` rejects a CUDA device, verify both pieces from the same container environment:

```bash
docker compose -f docker-compose.dev.yml -f docker-compose.dev.nvidia.yml exec backend nvidia-smi
docker compose -f docker-compose.dev.yml -f docker-compose.dev.nvidia.yml exec backend /app/llama-server --list-devices
```

`--list-devices` must include the CUDA devices the manager will pass to `--device`.

---

## Models vs Instances

This distinction is fundamental to the project.

### Model

A Model is a registered GGUF artifact plus reusable defaults.

It owns things such as:

- name;
- GGUF path;
- size;
- quantization;
- context capability;
- GGUF metadata;
- Model-level llama.cpp defaults.

A Model does **not** have a running/stopped state.

### Instance

An Instance is a durable configuration for one `llama-server` process.

It owns things such as:

- Instance name and slug;
- lifecycle;
- Always-On policy;
- autoload policy;
- idle timeout;
- eviction policy;
- scheduling priority;
- GPU placement;
- tensor split;
- Instance-level llama.cpp overrides.

The Instance slug is also the exact OpenAI `model` ID.

This means one GGUF can be exposed several ways without duplicating the model file.

---

## Architecture

```text
┌────────────────────────────────────────────────────────┐
│                    LlamaCPP Manager                    │
│                                                        │
│  ┌─────────────┐  ┌──────────────┐  ┌──────────────┐ │
│  │ Nuxt Web UI │  │ Management   │  │ OpenAI /v1  │ │
│  │             │  │ API          │  │ Gateway      │ │
│  └──────┬──────┘  └───────┬──────┘  └──────┬───────┘ │
│         │                 │                │          │
│         └─────────────────┼────────────────┘          │
│                           ▼                           │
│              ┌────────────────────────┐               │
│              │ Lifecycle / Scheduler  │               │
│              └───────────┬────────────┘               │
│                          │                            │
│          ┌───────────────┼───────────────┐            │
│          ▼               ▼               ▼            │
│      Instance A      Instance B      Instance C        │
│      llama-server    llama-server    llama-server      │
│                                                        │
│  SQLite · Downloads · HF · Telemetry · Request logs   │
└────────────────────────────────────────────────────────┘
```

The production container contains the Go backend, built Nuxt frontend and managed llama.cpp runtime.

Individual `llama-server` workers use private local ports and are not the public API surface.

---

## Repository layout

```text
.
├── backend/       Go control plane, gateway and runtime services
├── frontend/      Nuxt management interface
├── specs/         Architecture and feature specifications
├── Dockerfile
├── docker-compose.yml
└── docker-compose.nvidia.yml
```

The `specs/` directory contains the design contracts behind the implementation and is useful if you want to understand why Models, Instances, lifecycle and API identity behave the way they do.

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

The source-build Compose setup is available separately from the published production image:

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

See [`AGENTS.md`](./AGENTS.md) before making larger changes. The project keeps implementation, tests and specifications closely aligned.

---

## Project status

A large part of the control-plane foundation is already implemented:

- Model registry;
- durable multi-Instance lifecycle;
- OpenAI-compatible gateway;
- API keys;
- autoload;
- idle unload;
- Always-On reconciliation;
- hardware-aware placement and scheduling;
- Hugging Face discovery and downloads;
- GGUF metadata inspection;
- hardware recommendations;
- management authentication and OIDC;
- request observability;
- metrics;
- runtime OpenAPI documentation.

The project is still moving quickly and is not yet considered API-stable.

---

## Roadmap

Some notable work being developed or designed:

### UI redesign

A complete management-interface redesign is tracked in [#33](https://github.com/brantje/llamacpp-manager/issues/33), including:

- flat control-plane focused UI;
- dark theme by default;
- full light theme;
- extensible semantic theme system;
- redesigned Dashboard, Models, Instances, API, Downloads, Logs and Administration;
- richer Instance history;
- dedicated operator Playground.

### Playground

[#46](https://github.com/brantje/llamacpp-manager/issues/46) adds an operator console that deliberately sends requests through the real `/v1` gateway so autoload, scheduling, logging and metrics behave exactly like an external client.

The Playground is for exercising the control plane and inspecting real request diagnostics. It is not intended to replace controlled benchmarking.

### Benchmarking and configuration tuning

A dedicated benchmarking facility is planned around llama.cpp's `llama-bench`.

Unlike the Playground, which measures real inference requests passing through the manager, benchmarking will run controlled workloads specifically to measure hardware and configuration performance.

Planned capabilities include:

- benchmark registered Models and Instances on the current hardware;
- measure prompt-processing and token-generation throughput;
- compare CPU, single-GPU and multi-GPU execution;
- test different GPU offload levels;
- compare tensor-split configurations;
- evaluate different context sizes and runtime parameters;
- capture VRAM, RAM and GPU utilization alongside benchmark results;
- retain benchmark results for comparison;
- compare configuration changes before applying them to an Instance;
- use measured results to improve the manager's configuration recommendations.

The longer-term goal is for LlamaCPP Manager to move beyond estimating whether a model will run and help answer:

```text
Which configuration runs this model best on my hardware?
```

For example, the manager could benchmark several viable placements:

```text
Qwen 32B · Q4_K_M

Configuration             Prompt       Generation     VRAM
─────────────────────────────────────────────────────────────
CUDA0                     382 tok/s     31.4 tok/s     14.8 GiB
CUDA1                     371 tok/s     30.9 tok/s     14.8 GiB
CUDA0 + CUDA1 · 50/50     415 tok/s     34.7 tok/s      8.1 + 7.2 GiB
CUDA0 + CPU               188 tok/s     17.2 tok/s     11.6 GiB

Recommended
CUDA0 + CUDA1 · 50/50
```

Benchmark-derived recommendations may eventually suggest settings such as:

- GPU placement;
- GPU layer offload;
- tensor split;
- context size;
- batch-related options;
- other llama.cpp parameters where benchmarking demonstrates a meaningful difference.

Recommendations should remain explainable and based on measurements from the user's own machine rather than assuming that a generic hardware profile is optimal.

Benchmarking is intentionally separate from normal inference traffic and from Playground diagnostics. Real benchmarking belongs in the `llama-bench` facility so production request metrics are not presented as controlled performance measurements.

### Persistent prompt / KV cache

[#62](https://github.com/brantje/llamacpp-manager/issues/62) tracks persistence of llama.cpp prompt/KV state across Instance eviction and restart.

This work is intentionally blocked until upstream llama.cpp provides a reliable persistence mechanism.

### Compatibility hardening

Broader compatibility testing is planned across:

- OpenAI Python/JavaScript SDKs;
- LiteLLM;
- streaming;
- tool calling;
- structured output;
- Responses;
- embeddings;
- supported multimodal flows;
- lifecycle and resource-pressure failure scenarios.

---

## Home Assistant Local OpenAI LLM

Home Assistant's Local OpenAI LLM integration can group Assist requests into one request-log session without a LlamaCPP Manager-specific mode.

Enable **Pass conversation session ID to LiteLLM server**. The integration sends the Assist conversation ID as `metadata.session_id`, which LlamaCPP Manager uses as the request `session_id` while keeping individual `trace_id` values separate.

---

## What this project is not

LlamaCPP Manager does not try to become another model format, inference engine or hosted AI platform.

It currently focuses on:

- local/self-hosted `llama.cpp`;
- GGUF models;
- managing the lifecycle around `llama-server`;
- providing one predictable API to clients.

`llama.cpp` remains the inference engine.

LlamaCPP Manager is the control plane around it.

---

## Contributing

Issues, testing reports and pull requests are welcome.

If you're working on a larger feature, check the existing [issues](https://github.com/brantje/llamacpp-manager/issues) and [`specs/`](./specs) first. Many parts of the control plane have explicit behavioural contracts, particularly around lifecycle, scheduling and OpenAI compatibility.

When reporting a problem, useful information includes:

- LlamaCPP Manager version / commit;
- llama.cpp version;
- CPU / GPU configuration;
- relevant Instance configuration;
- manager logs;
- raw Instance logs when applicable;
- request ID or trace ID for inference problems.

---

<p align="center">
  <strong>llama.cpp is the runtime. LlamaCPP Manager is the control plane.</strong>
</p>
