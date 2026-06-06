# syntax=docker/dockerfile:1
# Coordinator image: the SvelteKit admin dashboard is built and embedded into
# the Go binary (go:embed all:build), so the dashboard, control plane and admin
# plane ship as one static binary on distroless. Config is via env var.
ARG GO_VERSION=1.26
ARG NODE_VERSION=22

# The SPA build emits platform-independent JS, so pin it to the native build
# platform: it builds once on amd64 and is reused by every target arch, never
# running node/npm under arm64 emulation.
FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-bookworm AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY web/ ./
RUN npm run build

# Build on the native platform and let Go cross-compile to the target arch. Go
# cross-compiles in seconds; running the compiler under QEMU for arm64 took
# minutes. The cache mounts persist the module and build caches across runs.
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /web/build ./web/build
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH:-amd64}" \
    go build -ldflags="-s -w" -o /out/coordinator ./cmd/coordinator

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/coordinator /usr/local/bin/coordinator
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/coordinator"]
