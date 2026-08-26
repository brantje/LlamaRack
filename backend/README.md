# Backend

Go control plane for llamacpp-manager.

The backend owns persistence, authentication, model configuration, `llama-server` worker processes, lifecycle/autoload, and the unified OpenAI-compatible `/v1` gateway.

Default container paths:

- `/config/manager.db` — SQLite state
- `/models` — GGUF model files
- `/app/llama-server` — managed worker binary from the upstream llama.cpp server image

The HTTP API listens on port `8000` inside the container and is mapped to host port `8888` by the root Compose file.
