# syntax=docker/dockerfile:1.7

FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json ./
RUN npm install
COPY web/ ./
RUN npm run generate

FROM golang:1.24-bookworm AS backend
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/llamacpp-manager ./cmd/llamacpp-manager

ARG LLAMA_IMAGE=ghcr.io/ggml-org/llama.cpp:server
FROM ${LLAMA_IMAGE} AS runtime

COPY --from=backend /out/llamacpp-manager /app/llamacpp-manager
COPY --from=web /src/web/.output/public /app/web

ENV LCM_LISTEN_ADDR=:8080 \
    LCM_DATA_DIR=/config \
    LCM_MODELS_DIR=/models \
    LCM_LLAMA_SERVER=/app/llama-server \
    LCM_WEB_ROOT=/app/web

VOLUME ["/config", "/models"]
EXPOSE 8080
ENTRYPOINT ["/app/llamacpp-manager"]
