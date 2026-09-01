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
    CGO_ENABLED=0 go build -trimpath -o /out/llamarack ./cmd/llamarack

FROM ${LLAMA_IMAGE}
COPY --from=backend-build /out/llamarack /usr/local/bin/llamarack
COPY --from=frontend-build /src/frontend/.output/public /app/frontend
RUN ln -s /usr/local/bin/llamarack /usr/local/bin/llamacpp-manager && \
    mkdir -p /config /models && \
    chown -R 1000:1000 /config /models
ENV LLAMARACK_LISTEN_ADDR=:8000 \
    LLAMARACK_DATA_DIR=/config \
    LLAMARACK_MODELS_DIR=/models \
    LLAMARACK_LLAMA_SERVER=/app/llama-server \
    LLAMARACK_FRONTEND_DIR=/app/frontend
VOLUME ["/config", "/models"]
EXPOSE 8000
USER 1000:1000
ENTRYPOINT ["/usr/local/bin/llamarack"]
