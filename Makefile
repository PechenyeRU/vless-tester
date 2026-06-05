# Development tasks. Postgres connection for integration tests is taken from
# TEST_DATABASE_URL; see docker-compose.yml for a local instance.
.PHONY: build test test-int vet lint up down tidy clean

build:
	go build ./...

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
