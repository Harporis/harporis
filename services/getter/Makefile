.PHONY: build test lint run

build:
	go build -o bin/getter ./cmd/getter

test:
	go test ./... -race -timeout 60s

lint:
	go vet ./...

run: build
	./bin/getter --config config/getter.yaml
