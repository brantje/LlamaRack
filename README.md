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

Always-On models are reconciled once when the backend starts. Periodic reconciliation defaults to every 15 seconds and can be changed with `LCM_ALWAYS_ON_RECONCILE_SECONDS`. Set it to `0` to keep the startup reconciliation but disable periodic restarts, which allows an Always-On model to remain manually stopped until the manager restarts or it is started explicitly.

The default backend image uses the standard llama.cpp server image. To test a different llama.cpp image variant, set `LLAMA_IMAGE` while building, for example:

```bash
LLAMA_IMAGE=ghcr.io/ggml-org/llama.cpp:server-cuda docker compose up --build
```

The exact upstream image tag can be overridden without changing the manager code.
