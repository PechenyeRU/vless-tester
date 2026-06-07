# syntax=docker/dockerfile:1
# Single-file worker image: the proxy core (mihomo) is a vendored Go dependency,
# so the worker is one self-contained static binary on distroless/static. All
# config is via env var.
ARG GO_VERSION=1.26

# Build on the native platform and let Go cross-compile to the target arch, so
# the compiler never runs under QEMU.
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
# TARGETOS/TARGETARCH are set automatically by buildx; default to linux/amd64
# for a plain `docker build`.
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH:-amd64}" \
    go build -ldflags="-s -w" -o /out/worker ./cmd/worker

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/worker /usr/local/bin/worker
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/worker"]
