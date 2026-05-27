.PHONY: build build-getter test lint run clean

build: build-getter

build-getter:
	go build -o bin/getter ./cmd/getter

test:
	go test ./... -race -timeout 90s

lint:
	go vet ./...

run: build-getter
	./bin/getter --config config/getter.yaml

clean:
	rm -rf bin/
