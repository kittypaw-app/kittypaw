BINARY := kittyhome
PKG    := ./cmd/kittyhome
VERSION ?= $(shell git -C ../.. describe --tags --always 2>/dev/null || echo dev)
COMMIT  ?= $(shell git -C ../.. rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT)

.PHONY: build test lint fmt run smoke smoke-local smoke-cutover clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

test:
	go test ./... -count=1

lint:
	golangci-lint run ./...

fmt:
	gofmt -w cmd internal

run:
	go run $(PKG)

smoke:
	bash deploy/smoke.sh

smoke-local:
	go run ./cmd/kittyhome-smoke

smoke-cutover:
	go run ./cmd/kittyhome-cutover-smoke

clean:
	rm -f $(BINARY)
