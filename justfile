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

# Build the dashboard bundle (once after cloning, then after frontend/ changes)
web:
    cd frontend && npm install && npm run build

# Build the CLI. frontend/dist is a build artifact and is not in the repository:
# run `just web` first, or go:embed fails and nothing in this file compiles.
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
    docker run --rm -p 8080:8080 -v {{catalog}}:/data:ro {{tag}} serve --catalog /data/catalog.json --addr :8080

# Tidy modules
tidy:
    go mod tidy

# Install git hooks (lefthook: 18 gates — see scripts/gates/README.md)
hooks:
    lefthook install

# Architectural gates only (DDD/Clean, fast — no build required)
gates:
    ./scripts/gates/arch.sh

# Everything pre-push runs, without pushing
gates-full:
    ./scripts/gates/push.sh

# Возраст новых зависимостей ветки. В хуки не вешается намеренно: нужна сеть, а
# запрос про каждый пакет стоит секунды — в pre-push это отключили бы за неделю.
dep-age:
    ./scripts/gates/dep-age.sh

# Сверка состава выпуска с историей: какие поведенческие коммиты после тега не
# оставили записи в CHANGELOG. Обязательный шаг перед релизом — pre-push стоит
# на машине разработчика и ветку, ушедшую мимо него, уже не догонит.
#   just release-scope v0.22.0
release-scope tag:
    ./scripts/gates/release-scope.sh {{tag}}

# Full gate, same as CI
ci: tidy lint test-race cover-gate gates
