.PHONY: test test-race cover lint build tidy ci

test:
	go test ./...

test-race:
	go test -race ./...

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

lint:
	golangci-lint run ./...

build:
	go build -o bin/kbengine ./cmd/kbengine

tidy:
	go mod tidy

# Full gate, same as CI
ci: tidy lint test-race cover
