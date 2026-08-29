# syntax=docker/dockerfile:1.7
ARG LLAMA_IMAGE=ghcr.io/ggml-org/llama.cpp:server

FROM node:24-bookworm-slim AS frontend-build
WORKDIR /src/frontend
COPY frontend/package.json ./
RUN npm install
COPY frontend/ ./
RUN npm run generate

FROM golang:1.27-bookworm AS backend-build
WORKDIR /src/backend
COPY backend/go.mod ./
RUN go mod download
COPY backend/ ./
RUN go mod tidy && \
    go mod verify && \
    CGO_ENABLED=0 go build -trimpath -o /out/llamacpp-manager ./cmd/llamacpp-manager

FROM ${LLAMA_IMAGE}
COPY --from=backend-build /out/llamacpp-manager /usr/local/bin/llamacpp-manager
COPY --from=frontend-build /src/frontend/.output/public /app/frontend
ENV LCM_LISTEN_ADDR=:8000 \
    LCM_DATA_DIR=/config \
    LCM_MODELS_DIR=/models \
    LCM_LLAMA_SERVER=/app/llama-server \
    LCM_FRONTEND_DIR=/app/frontend
VOLUME ["/config", "/models"]
EXPOSE 8000
ENTRYPOINT ["/usr/local/bin/llamacpp-manager"]
