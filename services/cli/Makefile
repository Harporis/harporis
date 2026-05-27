.PHONY: build install uninstall completions deb rpm test test-integration lint clean

PREFIX        ?= /usr/local
BIN           := bin/harporis
PKG           := github.com/Harporis/harporis/services/cli
NFPM_VERSION  ?= 2.40.0
NFPM_BIN      := bin/nfpm

VERSION := $(shell git describe --tags --dirty --always --match 'cli/v*' 2>/dev/null || \
                   git rev-parse --short HEAD 2>/dev/null || \
                   echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w \
	-X '$(PKG)/internal/version.Version=$(VERSION)' \
	-X '$(PKG)/internal/version.Commit=$(COMMIT)' \
	-X '$(PKG)/internal/version.ProtoVersion=v1'

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/harporis

completions: build
	mkdir -p completions
	./$(BIN) completion bash > completions/harporis.bash
	./$(BIN) completion zsh  > completions/_harporis
	./$(BIN) completion fish > completions/harporis.fish

install: build completions
	install -d $(PREFIX)/bin
	install -m 0755 $(BIN) $(PREFIX)/bin/harporis
	install -d $(PREFIX)/share/bash-completion/completions
	install -m 0644 completions/harporis.bash $(PREFIX)/share/bash-completion/completions/harporis

uninstall:
	rm -f $(PREFIX)/bin/harporis $(PREFIX)/share/bash-completion/completions/harporis

$(NFPM_BIN):
	mkdir -p bin
	curl -sSL https://github.com/goreleaser/nfpm/releases/download/v$(NFPM_VERSION)/nfpm_$(NFPM_VERSION)_Linux_x86_64.tar.gz \
		| tar xz -C bin nfpm

deb: build completions $(NFPM_BIN)
	mkdir -p dist
	HARPORIS_VERSION=$(VERSION) $(NFPM_BIN) pkg --packager deb --config packaging/nfpm.yaml --target dist/

rpm: build completions $(NFPM_BIN)
	mkdir -p dist
	HARPORIS_VERSION=$(VERSION) $(NFPM_BIN) pkg --packager rpm --config packaging/nfpm.yaml --target dist/

test:
	go test ./... -race -timeout 90s

test-integration:
	go test ./... -race -timeout 180s -tags integration

lint:
	go vet ./...

clean:
	rm -rf bin/ dist/ completions/
