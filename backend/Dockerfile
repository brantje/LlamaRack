# syntax=docker/dockerfile:1.7
FROM golang:1.27-bookworm AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/llamacpp-manager ./cmd/llamacpp-manager

ARG LLAMA_IMAGE=ghcr.io/ggml-org/llama.cpp:server
FROM ${LLAMA_IMAGE}
COPY --from=build /out/llamacpp-manager /usr/local/bin/llamacpp-manager
ENV LCM_LISTEN_ADDR=:8000 \
    LCM_DATA_DIR=/config \
    LCM_MODELS_DIR=/models \
    LCM_LLAMA_SERVER=/app/llama-server
VOLUME ["/config", "/models"]
EXPOSE 8000
ENTRYPOINT ["/usr/local/bin/llamacpp-manager"]
