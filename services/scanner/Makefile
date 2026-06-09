.PHONY: build install uninstall test test-integration lint docker docker-push clean

PREFIX        ?= /usr/local
BIN           := bin/scanner
PKG           := github.com/Harporis/harporis/services/scanner
IMAGE         ?= harporis/scanner
IMAGE_TAG     ?= dev

VERSION := $(shell git describe --tags --dirty --always --match 'scanner/v*' 2>/dev/null || \
                   git rev-parse --short HEAD 2>/dev/null || \
                   echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w \
	-X '$(PKG)/internal/version.Version=$(VERSION)' \
	-X '$(PKG)/internal/version.Commit=$(COMMIT)' \
	-X '$(PKG)/internal/version.ProtoVersion=v1'

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/scanner

install: build
	install -d $(PREFIX)/bin
	install -m 0755 $(BIN) $(PREFIX)/bin/scanner

uninstall:
	rm -f $(PREFIX)/bin/scanner

test:
	go test ./... -race -timeout 90s

test-integration:
	go test ./... -race -timeout 180s -tags integration

lint:
	go vet ./...

docker:
	docker build -t $(IMAGE):$(IMAGE_TAG) -f Dockerfile ../..

docker-push:
	docker push $(IMAGE):$(IMAGE_TAG)

clean:
	rm -rf bin/
