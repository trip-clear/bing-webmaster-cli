BINARY  := bwt
PKG     := ./cmd/bwt
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)
PREFIX  ?= $(HOME)/.local

.PHONY: build test lint install clean

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(PKG)

test:
	go test ./...

lint:
	gofmt -l .
	go vet ./...

# Installs to ~/.local/bin by default; make sure it is on your PATH.
install:
	go build -ldflags "$(LDFLAGS)" -o $(PREFIX)/bin/$(BINARY) $(PKG)
	@echo "installed: $(PREFIX)/bin/$(BINARY) ($(VERSION))"

clean:
	rm -rf bin
