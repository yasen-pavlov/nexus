.PHONY: test test-unit test-integration lint coverage build swagger dev dev-db up down logs help

help:
	@echo "Nexus — common targets:"
	@echo "  make up           Start full stack (app + deps) in Docker"
	@echo "  make down         Stop everything"
	@echo "  make dev          Run backend locally against containerized deps (db/opensearch/tika)"
	@echo "  make dev-db       Start just the deps (db/opensearch/tika)"
	@echo "  make test         Run all tests (unit + integration)"
	@echo "  make lint         Run golangci-lint"
	@echo "  make coverage     Run integration tests with coverage report"
	@echo "  make build        Build binary to bin/nexus"

# Run all tests (unit + integration).
test: test-unit test-integration

# Unit tests only (no database required).
test-unit:
	go test ./internal/...

# Integration tests — containers start automatically via testcontainers-go.
# To reuse an already-running dev cluster (faster iteration), export:
#   NEXUS_TEST_DATABASE_URL=postgres://nexus:nexus@localhost:5432/nexus?sslmode=disable
#   NEXUS_TEST_OPENSEARCH_URL=http://localhost:9200
test-integration:
	go test -tags integration ./internal/...

lint:
	golangci-lint run ./...

# Coverage report (excludes testutil).
coverage:
	go test -tags integration $$(go list ./internal/... | grep -v testutil) -coverprofile=coverage.out
	go tool cover -func=coverage.out | tail -1
	@echo "Run 'go tool cover -html=coverage.out' for detailed report"

# Generate swagger docs (requires: go install github.com/swaggo/swag/cmd/swag@v1.8.12)
swagger:
	swag init -g cmd/nexus/main.go -o docs --parseDependency --parseInternal

# Build the binary.
build: swagger
	go build -o bin/nexus ./cmd/nexus

# --- Containers ---------------------------------------------------------------

# Start deps only (Postgres, OpenSearch, Tika). Used by `make dev`.
dev-db:
	docker compose up -d db opensearch tika

# Start the full stack (app + deps). Requires .env — copy from .env.example.
up:
	@test -f .env || { echo "Missing .env — run: cp .env.example .env && edit it"; exit 1; }
	docker compose --profile app up -d

# Stop everything across all profiles.
down:
	docker compose --profile app --profile ollama down

logs:
	docker compose --profile app logs -f

# --- Local dev ----------------------------------------------------------------

# Run the backend locally against containerized deps. Loads NEXUS_ENCRYPTION_KEY /
# NEXUS_JWT_SECRET from .env so sessions + encrypted connector configs stay valid
# across `make dev` and `docker compose --profile app up`.
dev: dev-db
	@test -f .env || { echo "Missing .env — run: cp .env.example .env && edit it"; exit 1; }
	set -a && . ./.env && set +a && \
		NEXUS_DATABASE_URL=postgres://nexus:nexus@localhost:5432/nexus?sslmode=disable \
		NEXUS_OPENSEARCH_URL=http://localhost:9200 \
		NEXUS_TIKA_URL=http://localhost:9998 \
		NEXUS_FS_ROOT_PATH=./testdata \
		go run ./cmd/nexus
