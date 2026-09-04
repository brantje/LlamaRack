# syntax=docker/dockerfile:1.7
ARG LLAMA_IMAGE=ghcr.io/ggml-org/llama.cpp:server
ARG LLAMARACK_VERSION=development
ARG LLAMARACK_COMMIT=
ARG LLAMARACK_BUILD_TIME=
ARG LLAMARACK_CHANNEL=development
ARG LLAMARACK_VARIANT=unknown
ARG LLAMA_CPP_RELEASE=
ARG LLAMA_CPP_BUILD=
ARG LLAMA_CPP_IMAGE=

FROM node:24-bookworm-slim AS frontend-build
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run generate

FROM golang:1.27-bookworm AS backend-build
ARG LLAMARACK_VERSION
ARG LLAMARACK_COMMIT
ARG LLAMARACK_BUILD_TIME
ARG LLAMARACK_CHANNEL
ARG LLAMARACK_VARIANT
ARG LLAMA_CPP_RELEASE
ARG LLAMA_CPP_BUILD
ARG LLAMA_CPP_IMAGE
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download && go mod verify
COPY backend/ ./
RUN if [ "$LLAMARACK_CHANNEL" = "release" ] && [ "$LLAMARACK_VERSION" = "development" ]; then \
      echo 'release builds must provide an explicit LlamaRack version' >&2; exit 1; \
    fi && \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags="-X github.com/brantje/llamarack/backend/internal/buildinfo.version=${LLAMARACK_VERSION} \
      -X github.com/brantje/llamarack/backend/internal/buildinfo.commit=${LLAMARACK_COMMIT} \
      -X github.com/brantje/llamarack/backend/internal/buildinfo.buildTime=${LLAMARACK_BUILD_TIME} \
      -X github.com/brantje/llamarack/backend/internal/buildinfo.channel=${LLAMARACK_CHANNEL} \
      -X github.com/brantje/llamarack/backend/internal/buildinfo.variant=${LLAMARACK_VARIANT} \
      -X github.com/brantje/llamarack/backend/internal/buildinfo.llamaCppRelease=${LLAMA_CPP_RELEASE} \
      -X github.com/brantje/llamarack/backend/internal/buildinfo.llamaCppBuild=${LLAMA_CPP_BUILD} \
      -X github.com/brantje/llamarack/backend/internal/buildinfo.llamaCppImage=${LLAMA_CPP_IMAGE}" \
      -o /out/llamarack ./cmd/llamarack

FROM ${LLAMA_IMAGE}
ARG LLAMARACK_VERSION
ARG LLAMARACK_COMMIT
ARG LLAMARACK_BUILD_TIME
ARG LLAMARACK_CHANNEL
ARG LLAMARACK_VARIANT
ARG LLAMA_CPP_RELEASE
ARG LLAMA_CPP_BUILD
ARG LLAMA_CPP_IMAGE
LABEL org.opencontainers.image.version="${LLAMARACK_VERSION}" \
      org.opencontainers.image.revision="${LLAMARACK_COMMIT}" \
      org.opencontainers.image.created="${LLAMARACK_BUILD_TIME}" \
      io.llamarack.release.channel="${LLAMARACK_CHANNEL}" \
      io.llamarack.runtime.variant="${LLAMARACK_VARIANT}" \
      io.llamarack.llama.cpp.release="${LLAMA_CPP_RELEASE}" \
      io.llamarack.llama.cpp.build="${LLAMA_CPP_BUILD}" \
      io.llamarack.llama.cpp.image="${LLAMA_CPP_IMAGE}"
COPY --from=backend-build /out/llamarack /usr/local/bin/llamarack
COPY --from=frontend-build /src/frontend/.output/public /app/frontend
RUN mkdir -p /config /models && \
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
