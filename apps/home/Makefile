BINARY := kittyhome
PKG    := ./cmd/kittyhome
VERSION ?= $(shell git -C ../.. describe --tags --always 2>/dev/null || echo dev)
COMMIT  ?= $(shell git -C ../.. rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT)

.PHONY: build test lint fmt run smoke clean

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

clean:
	rm -f $(BINARY)
