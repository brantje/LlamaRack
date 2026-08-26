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

On first load, create the bootstrap administrator account. GGUF files placed under `data/models/` are visible inside the backend as `/models/<file>.gguf` and can then be registered from the Models page.

The default backend image uses the standard llama.cpp server image. To test a different llama.cpp image variant, set `LLAMA_IMAGE` while building, for example:

```bash
LLAMA_IMAGE=ghcr.io/ggml-org/llama.cpp:server-cuda docker compose up --build
```

The exact upstream image tag can be overridden without changing the manager code.
