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

# Run tests and fail if total coverage drops below 80% (same gate as CI)
cover-gate: cover
    @total=$(go tool cover -func=coverage.out | awk '/^total:/ {print substr($3, 1, length($3)-1)}'); \
    echo "total coverage: $total%"; \
    awk -v t="$total" 'BEGIN { if (t+0 < 80.0) { print "coverage below 80% threshold"; exit 1 } }'

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

# Build the container image (tag defaults to kbengine:dev)
docker tag="kbengine:dev":
    docker build -t {{tag}} .

# Run the containerized dashboard against a catalog dir mounted at /data
docker-serve catalog tag="kbengine:dev":
    docker run --rm -p 8080:8080 -v {{catalog}}:/data:ro {{tag}} serve --catalog /data/catalog.json

# Tidy modules
tidy:
    go mod tidy

# Install git hooks (lefthook: gitleaks pre-commit + conventional commit-msg)
hooks:
    lefthook install

# Full gate, same as CI
ci: tidy lint test-race cover-gate
