# syntax=docker/dockerfile:1.7
#
# Membuss daemon - multi-stage container build.
#
# Stage 1 builds the web explorer frontend using Node.js.
# Stage 2 compiles statically-linked Linux binaries using Go,
# incorporating the embedded web assets.
# Stage 3 copies only the binaries and small entrypoint into a distroless base image.

# ---------------------------------------------------------------------------
# Stage 1: frontend-builder
# ---------------------------------------------------------------------------
FROM node:20-alpine AS frontend-builder

WORKDIR /src/explorer-web

# Pre-copy package manifests for layer caching
COPY explorer-web/package*.json ./
RUN npm ci

# Copy explorer-web source files and root target directory structure
COPY explorer-web/ ./
WORKDIR /src
COPY gateway/explorer/web /src/gateway/explorer/web

# Build the frontend into gateway/explorer/web/dist
WORKDIR /src/explorer-web
RUN npm run build

# ---------------------------------------------------------------------------
# Stage 2: builder (Go)
# ---------------------------------------------------------------------------
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates binutils

WORKDIR /src

# Pre-copy module manifests so dependency layer is cached
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source
COPY . .

# Copy compiled frontend dist from Stage 1 into gateway/explorer/web/dist
COPY --from=frontend-builder /src/gateway/explorer/web/dist ./gateway/explorer/web/dist

# Build static, stripped binaries with target OS and architecture support
ARG TARGETOS
ARG TARGETARCH
ARG GIT_COMMIT=unknown
ARG BUILD_TIME=unknown

ENV CGO_ENABLED=0

RUN mkdir -p /out \
    && GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags "-s -w -X github.com/nnlgsakib/membuss/core/version.GitCommit=${GIT_COMMIT} -X github.com/nnlgsakib/membuss/core/version.BuildTime=${BUILD_TIME}" -o /out/membuss ./cmd/membuss \
    && GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags "-s -w" -o /out/membuss-entrypoint ./cmd/membuss-entrypoint \
    && strip /out/membuss /out/membuss-entrypoint

# ---------------------------------------------------------------------------
# Stage 3: runtime
# ---------------------------------------------------------------------------
FROM gcr.io/distroless/base-debian12

LABEL org.opencontainers.image.title="membuss" \
      org.opencontainers.image.description="Decentralized, content-addressed storage and delivery network (daemon + CLI)" \
      org.opencontainers.image.source="https://github.com/nnlgsakib/membuss" \
      org.opencontainers.image.licenses="MIT"

COPY --from=builder /out/membuss     /usr/local/bin/membuss
COPY deploy/membuss.yaml /etc/membuss/config.yaml
COPY deploy/GeoLite2-City.mmdb /etc/membuss/GeoLite2-City.mmdb
COPY --from=builder /out/membuss-entrypoint /usr/local/bin/membuss-entrypoint

ENV MEMBUSS_DATA_DIR=/var/lib/membuss
VOLUME ["/var/lib/membuss"]

EXPOSE 4001 4001/udp 4002 5001 8080 50051

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD ["/usr/local/bin/membuss", "--addr", "127.0.0.1:50051", "ping"]

ENTRYPOINT ["/usr/local/bin/membuss-entrypoint"]
CMD ["/usr/local/bin/membuss", "-config", "/etc/membuss/config.yaml"]
