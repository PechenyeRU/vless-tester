# Development tasks. Postgres connection for integration tests is taken from
# TEST_DATABASE_URL; see docker-compose.yml for a local instance.
.PHONY: build web coordinator worker-embedded dist embed-singbox \
	docker docker-worker docker-coordinator test test-int vet lint up down \
	stack stack-down tidy clean

SINGBOX_VERSION ?= 1.13.13
VERSION         ?= dev
IMAGE_PREFIX    ?= vless-tester
PLATFORMS       ?= linux/amd64 linux/arm64

build:
	go build ./...

# Fetch the static sing-box release for the host platform into the embed dir.
embed-singbox:
	scripts/fetch-singbox.sh $$(go env GOOS) $$(go env GOARCH) $(SINGBOX_VERSION)

# Single-file worker for the host platform with sing-box embedded.
worker-embedded: embed-singbox
	CGO_ENABLED=0 go build -tags embed_singbox -ldflags="-s -w" -o bin/worker ./cmd/worker

# Multi-arch single-file workers (sing-box embedded) into dist/.
dist:
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "building worker $$os/$$arch"; \
		scripts/fetch-singbox.sh $$os $$arch $(SINGBOX_VERSION) || exit 1; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -tags embed_singbox \
			-ldflags="-s -w" -o dist/worker-$$os-$$arch ./cmd/worker || exit 1; \
	done
	@rm -rf internal/core/embedded

# Container images (worker embeds sing-box; coordinator embeds the SPA).
docker-worker:
	docker build -f deploy/docker/worker.Dockerfile \
		--build-arg SINGBOX_VERSION=$(SINGBOX_VERSION) \
		-t $(IMAGE_PREFIX)-worker:$(VERSION) .

docker-coordinator:
	docker build -f deploy/docker/coordinator.Dockerfile \
		-t $(IMAGE_PREFIX)-coordinator:$(VERSION) .

docker: docker-worker docker-coordinator

# Full local stack: postgres + coordinator + worker, images built from source.
stack:
	docker compose --profile stack up --build -d

stack-down:
	docker compose --profile stack down

# Build the SvelteKit admin SPA; its output is embedded into the coordinator.
# The build wipes web/build, so restore the committed .gitkeep that keeps the
# go:embed target present on a clean checkout.
web:
	cd web && npm ci && npm run build && touch build/.gitkeep

# Coordinator binary with the dashboard embedded (builds the SPA first).
coordinator: web
	go build -o bin/coordinator ./cmd/coordinator

test:
	go test ./...

# Integration tests require a reachable Postgres (TEST_DATABASE_URL).
test-int:
	go test -tags=integration ./...

vet:
	go vet ./...

lint:
	golangci-lint run

up:
	docker compose up -d

down:
	docker compose down

tidy:
	go mod tidy

clean:
	rm -rf bin dist internal/core/embedded
