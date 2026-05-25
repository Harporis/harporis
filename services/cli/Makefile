.PHONY: build install test test-integration lint clean

PREFIX ?= /usr/local
BIN     := bin/harporis
PKG     := github.com/Harporis/harporis/services/cli

VERSION := $(shell git describe --tags --dirty --always 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w \
	-X '$(PKG)/internal/version.Version=$(VERSION)' \
	-X '$(PKG)/internal/version.Commit=$(COMMIT)' \
	-X '$(PKG)/internal/version.ProtoVersion=v1'

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/harporis

install: build
	install -d $(PREFIX)/bin
	install -m 0755 $(BIN) $(PREFIX)/bin/harporis

test:
	go test ./... -race -timeout 90s

test-integration:
	go test ./... -race -timeout 180s -tags integration

lint:
	go vet ./...

clean:
	rm -rf bin/ dist/
