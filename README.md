# llamacpp-manager

Web-based lifecycle manager and OpenAI-compatible gateway for llama.cpp.

## Repository structure

- `frontend/` — Nuxt application
- `backend/` — Go manager service
- `specs/` — product and architecture specifications

The frontend and backend are independent application roots. Run dependency-management and build commands from the corresponding directory.

## Local testing with Docker Compose

Start both applications from the repository root:

```bash
docker compose up
```

Then open:

- Frontend: http://localhost:3000
- Backend: http://localhost:8080
- Backend health check: http://localhost:8080/health

The Compose setup bind-mounts both source directories. Nuxt runs in development mode, so frontend edits are picked up without rebuilding the container. Go build/module caches and frontend `node_modules` are stored in named Docker volumes instead of the working tree.

To stop the stack:

```bash
docker compose down
```

To also discard the development dependency/cache volumes:

```bash
docker compose down -v
```
