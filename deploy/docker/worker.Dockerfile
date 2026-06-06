# syntax=docker/dockerfile:1
# Single-file worker image: the sing-box release is embedded into the Go binary
# (go:embed, -tags embed_singbox) and extracted to the cache dir at first run, so
# the final image is just one binary on distroless. All config is via env var.
#
# The final stage is distroless/base (not static): official sing-box releases are
# dynamically linked against glibc, so the runtime needs the loader and libc that
# base provides. The worker's own Go binary is built static (CGO_ENABLED=0).
ARG GO_VERSION=1.26
ARG SINGBOX_VERSION=1.13.13

# Build on the native platform and let Go cross-compile to the target arch, so
# the compiler never runs under QEMU. fetch-singbox.sh takes the arch as an
# argument (a plain download, not emulated execution), so it still pulls the
# correct sing-box release to embed for the target arch.
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
# TARGETOS/TARGETARCH are set automatically by buildx; default to linux/amd64
# for a plain `docker build`.
ARG TARGETOS
ARG TARGETARCH
ARG SINGBOX_VERSION
RUN bash scripts/fetch-singbox.sh "${TARGETOS:-linux}" "${TARGETARCH:-amd64}" "${SINGBOX_VERSION}"
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH:-amd64}" \
    go build -tags embed_singbox -ldflags="-s -w" -o /out/worker ./cmd/worker

FROM gcr.io/distroless/base-debian12:nonroot
COPY --from=build /out/worker /usr/local/bin/worker
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/worker"]
