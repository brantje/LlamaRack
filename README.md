# llamacpp-manager

Web-based lifecycle manager and OpenAI-compatible gateway for llama.cpp.

## Repository structure

- `frontend/` — Nuxt 4 application
- `backend/` — Go manager service
- `specs/` — product and architecture specifications

## Local testing

Create the local model/config directories and start both applications:

```bash
mkdir -p data/config data/models
docker compose up --build
```

Open:

- Frontend: http://localhost:3000
- Backend: http://localhost:8888
- Health: http://localhost:8888/health

On first load, create the bootstrap administrator account. GGUF files placed anywhere under `data/models/` are visible inside the backend models directory. **Models → Add model** (or `/models/new`) scans that directory recursively and lists only GGUF files that have not already been added. Paths in the UI are relative to the models directory, so the `/models` container prefix is hidden. Model registration and GGUF registration are a single step; there is no separate artifact registry.

### Home Assistant Local OpenAI LLM

Home Assistant's Local OpenAI LLM integration can group Assist requests into one `/logs` session without a llamacpp-manager-specific mode. Enable **Pass conversation session ID to LiteLLM server**; the integration sends the Assist conversation ID as `metadata.session_id`, which llamacpp-manager uses as `session_id` while keeping each request's `trace_id` separate.

Always-On models are reconciled once when the backend starts. Periodic reconciliation defaults to every 15 seconds and can be changed with `LCM_ALWAYS_ON_RECONCILE_SECONDS`; set it to `0` to disable periodic reconciliation after the startup pass. When a user manually stops an Always-On model, periodic Always-On reconciliation will leave it stopped for the rest of the current backend runtime. An explicit Start clears the suppression. If autoload is enabled, an inference request also clears the suppression and starts the model on demand. Restarting the backend clears all manual-stop suppressions so Always-On models are started again.

Non-Always-On models are automatically unloaded after five minutes without inference activity by default. The global idle unload timeout is configured under **Administration → General** and persisted by the manager; set it to `0` to disable the global idle timeout. The Add Model form can override the timeout per model with `idle_unload_seconds`; `0` on the model means inherit the global value. Active inference requests, including streaming responses, keep the model active until the proxied response completes.

The Add Model form also exposes eviction policy. `eviction_enabled=false` removes that model from normal resource-pressure eviction plans, while model priority controls eviction preference among eligible models. Always-On and active models remain protected regardless. Per-GPU VRAM measurements and automatic resource-pressure-triggered eviction are added by the later Phase 7 hardware-integration phase.

## Docker GPU telemetry

The Instances page streams live CPU, RAM, GPU placement, GPU utilization and VRAM telemetry for each running `llama-server` process. NVIDIA/ROCm tooling can report a GPU process by its **host PID**, while the manager sees the same llama-server through Docker's PID namespace. Without a mapping, for example, the manager might see PID `1652` while `nvidia-smi`/NVML reports host PID `2554129`, so exact GPU attribution fails even though CPU and RAM telemetry still work.

The provided `docker-compose.yml` mounts the host `/proc` read-only at `/host/proc` and sets `LCM_HOST_PROC=/host/proc`. The telemetry collector reads the host process `NSpid` chain and maps the GPU tool's host PID back to the manager's container PID. This keeps normal Docker PID isolation while allowing per-Instance GPU placement, utilization and VRAM attribution. Custom Docker deployments should provide the same read-only host `/proc` mount and set `LCM_HOST_PROC` to its mount point if exact process attribution is required.

### NVIDIA containers

`nvidia-smi` is a host-driver utility. The backend image does **not** install a distribution-specific NVIDIA driver package; instead, NVIDIA Container Toolkit should inject the host-compatible utility and NVML libraries into the container.

Use the NVIDIA Compose override for NVIDIA hosts:

```bash
docker compose -f docker-compose.yml -f docker-compose.nvidia.yml up -d --build
```

The override is a complete NVIDIA mode: it explicitly reserves NVIDIA GPUs, requests `NVIDIA_VISIBLE_DEVICES=all` plus `NVIDIA_DRIVER_CAPABILITIES=compute,utility`, and defaults the backend build to `ghcr.io/ggml-org/llama.cpp:server-cuda`. This is important because GPU telemetry and scheduling can see `CUDA0` through `nvidia-smi`, but a CPU-only `llama-server` will reject `--device CUDA0` as an invalid device.

After changing NVIDIA runtime/image settings, recreate the backend container rather than merely restarting it:

```bash
docker compose -f docker-compose.yml -f docker-compose.nvidia.yml up -d --build --force-recreate backend
```

Verify both the NVIDIA runtime and the llama.cpp backend from inside the same backend container:

```bash
docker compose -f docker-compose.yml -f docker-compose.nvidia.yml exec backend nvidia-smi
docker compose -f docker-compose.yml -f docker-compose.nvidia.yml exec backend nvidia-smi pmon -c 1
docker compose -f docker-compose.yml -f docker-compose.nvidia.yml exec backend /app/llama-server --list-devices
```

`--list-devices` should include the CUDA device(s) the manager will pass to `--device`. If `nvidia-smi` works but `llama-server --list-devices` does not show CUDA, the container is using a non-CUDA llama.cpp image. If a custom `LLAMA_IMAGE` is supplied while using the NVIDIA override, it must itself be CUDA-enabled.

For NVIDIA process utilization, the manager samples `nvidia-smi pmon`. It first requests the utilization-only form (`nvidia-smi pmon -c 1 -s u`) and, if that command fails or returns no usable process rows, retries with plain `nvidia-smi pmon -c 1`. This covers driver/tooling combinations where the default `pmon` output works but the filtered invocation does not.

GPU telemetry falls back **per metric**. If placement and VRAM can be attributed to the `llama-server` PID but process-level GPU utilization is unavailable, placement and VRAM remain per-Instance while GPU utilization falls back to the assigned GPU's device-wide utilization. The Instances UI labels only that metric as `GPU usage (global fallback)`. If no GPU placement can be attributed at all, the fallback uses all detected GPUs: utilization is averaged and VRAM usage is summed, and both values are labeled `global fallback`. Global fallback values may include other GPU workloads and must not be interpreted as process-only measurements. CPU and RAM remain process-scoped.

`nvcc --version` reports the installed CUDA compiler toolkit, not whether runtime process-utilization counters are exposed by the active NVIDIA driver/container stack. When troubleshooting telemetry, `nvidia-smi` and `llama-server --list-devices` output from the same environment as the manager are the relevant runtime checks.

The default backend image uses the standard llama.cpp server image. To test a different llama.cpp image variant outside the NVIDIA override, set `LLAMA_IMAGE` while building, for example:

```bash
LLAMA_IMAGE=ghcr.io/ggml-org/llama.cpp:server-cuda docker compose up --build
```

The exact upstream image tag can be overridden without changing the manager code.