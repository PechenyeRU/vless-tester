# Development tasks. Postgres connection for integration tests is taken from
# TEST_DATABASE_URL; see docker-compose.yml for a local instance.
.PHONY: build web coordinator test test-int vet lint up down tidy clean

build:
	go build ./...

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
	rm -rf bin dist
