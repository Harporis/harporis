.PHONY: build build-cli test lint run

build: bin/getter bin/getter-cli

bin/getter:
	go build -o bin/getter ./cmd/getter

bin/getter-cli:
	go build -o bin/getter-cli ./cmd/getter-cli

build-cli: bin/getter-cli

test:
	go test ./... -race -timeout 90s

lint:
	go vet ./...

run: bin/getter
	./bin/getter --config config/getter.yaml

clean:
	rm -rf bin/
