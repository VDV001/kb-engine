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

# Build the CLI
build:
    go build -o bin/kbengine ./cmd/kbengine

# Tidy modules
tidy:
    go mod tidy

# Full gate, same as CI
ci: tidy lint test-race cover
