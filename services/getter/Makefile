.PHONY: build build-getter build-cli test lint run clean

build: build-getter build-cli

build-getter:
	go build -o bin/getter ./cmd/getter

build-cli:
	go build -o bin/getter-cli ./cmd/getter-cli

test:
	go test ./... -race -timeout 90s

lint:
	go vet ./...

run: build-getter
	./bin/getter --config config/getter.yaml

clean:
	rm -rf bin/
