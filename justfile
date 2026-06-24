# List available recipes
default:
    @just --list

# Run unit tests
test:
    go test ./...

# Run tests with the race detector
test-race:
    go test -race ./...

# Run tests and print the coverage summary
cover:
    go test -coverprofile=coverage.out ./...
    go tool cover -func=coverage.out | tail -1

# Run golangci-lint
lint:
    golangci-lint run ./...

# Build the embedded frontend (run after changing frontend/)
web:
    cd frontend && npm install && npm run build

# Build the CLI (assumes frontend/dist exists; run `just web` first if changed)
build:
    go build -o bin/kbengine ./cmd/kbengine

# Run the dashboard server against a catalog
serve catalog:
    go run ./cmd/kbengine serve --catalog {{catalog}}

# Tidy modules
tidy:
    go mod tidy

# Full gate, same as CI
ci: tidy lint test-race cover
