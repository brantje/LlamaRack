# syntax=docker/dockerfile:1.7
ARG LLAMA_IMAGE=ghcr.io/ggml-org/llama.cpp:server

FROM golang:1.27-bookworm AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN go mod tidy && \
    go mod verify && \
    CGO_ENABLED=0 go build -trimpath -o /out/llamacpp-manager ./cmd/llamacpp-manager

FROM ${LLAMA_IMAGE}
COPY --from=build /out/llamacpp-manager /usr/local/bin/llamacpp-manager
ENV LCM_LISTEN_ADDR=:8000 \
    LCM_DATA_DIR=/config \
    LCM_MODELS_DIR=/models \
    LCM_LLAMA_SERVER=/app/llama-server \
    NVIDIA_VISIBLE_DEVICES=all \
    NVIDIA_DRIVER_CAPABILITIES=compute,utility
VOLUME ["/config", "/models"]
EXPOSE 8000
ENTRYPOINT ["/usr/local/bin/llamacpp-manager"]
