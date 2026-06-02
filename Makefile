BINARY  := wifi-provisioner
PKG     := ./cmd/wifi-provisioner
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)

# Static, dependency-free binary for the target board (ARM64).
GOFLAGS  := -trimpath
LDFLAGS  := -s -w -X main.version=$(VERSION)

.PHONY: all build build-host clean install uninstall test fmt

all: build

## build: cross/native build a static ARM64 binary into bin/
build:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
		go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(PKG)
	@echo "built bin/$(BINARY) ($(VERSION))"

## build-host: build for the current machine (handy for quick checks)
build-host:
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(PKG)

## test: run unit tests
test:
	go test ./...

## fmt: gofmt the tree
fmt:
	gofmt -w .

## install: install the binary + service on this machine (run as root on the board)
install: build
	sudo ./install.sh

## uninstall: remove the installed binary + service
uninstall:
	sudo ./uninstall.sh

clean:
	rm -rf bin
