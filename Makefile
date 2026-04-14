.PHONY: test test-unit test-integration lint coverage build

# Run all tests (unit + integration)
test: test-unit test-integration

# Unit tests only (no database required)
test-unit:
	go test ./internal/...

# Integration tests — containers start automatically via testcontainers-go.
# To reuse an already-running dev cluster (faster iteration), export:
#   NEXUS_TEST_DATABASE_URL=postgres://nexus:nexus@localhost:5432/nexus?sslmode=disable
#   NEXUS_TEST_OPENSEARCH_URL=http://localhost:9200
test-integration:
	go test -tags integration ./internal/...

# Lint
lint:
	golangci-lint run ./...

# Coverage report (excludes testutil)
coverage:
	go test -tags integration $$(go list ./internal/... | grep -v testutil) -coverprofile=coverage.out
	go tool cover -func=coverage.out | tail -1
	@echo "Run 'go tool cover -html=coverage.out' for detailed report"

# Generate swagger docs (requires: go install github.com/swaggo/swag/cmd/swag@v1.8.12)
swagger:
	swag init -g cmd/nexus/main.go -o docs --parseDependency --parseInternal

# Build the binary
build: swagger
	go build -o bin/nexus ./cmd/nexus

# Dev: start Postgres + OpenSearch
dev-db:
	docker compose -f docker-compose.dev.yml up -d

# Dev: run the app locally
dev: dev-db
	NEXUS_DATABASE_URL=postgres://nexus:nexus@localhost:5432/nexus?sslmode=disable \
	NEXUS_OPENSEARCH_URL=http://localhost:9200 \
	NEXUS_FS_ROOT_PATH=./testdata \
	go run ./cmd/nexus
