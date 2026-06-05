# syntax=docker/dockerfile:1
# Coordinator image: the SvelteKit admin dashboard is built and embedded into
# the Go binary (go:embed all:build), so the dashboard, control plane and admin
# plane ship as one static binary on distroless. Config is via env var.
ARG GO_VERSION=1.26
ARG NODE_VERSION=22

FROM node:${NODE_VERSION}-bookworm AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/build ./web/build
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH:-amd64}" \
    go build -ldflags="-s -w" -o /out/coordinator ./cmd/coordinator

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/coordinator /usr/local/bin/coordinator
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/coordinator"]
