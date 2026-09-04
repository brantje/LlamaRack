# syntax=docker/dockerfile:1.7
ARG LLAMA_IMAGE=ghcr.io/ggml-org/llama.cpp:server

FROM golang:1.27-bookworm AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN go mod tidy && \
    go mod verify && \
    CGO_ENABLED=0 go build -trimpath -o /out/llamarack ./cmd/llamarack

FROM ${LLAMA_IMAGE}
COPY --from=build /out/llamarack /usr/local/bin/llamarack
RUN mkdir -p /config /models
ENV LLAMARACK_LISTEN_ADDR=:8000 \
    LLAMARACK_DATA_DIR=/config \
    LLAMARACK_MODELS_DIR=/models \
    LLAMARACK_LLAMA_SERVER=/app/llama-server
VOLUME ["/config", "/models"]
EXPOSE 8000
ENTRYPOINT ["/usr/local/bin/llamarack"]
