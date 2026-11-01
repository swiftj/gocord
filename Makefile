# Wave 1 bootstrap Makefile. The ecosystem-and-release teammate extends it to the
# full target set in specs/04-release-and-ci.md §3 (build-all, lint, dxt, snapshot, smoke).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: build test test-race vet fmt clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/discord-mcp ./cmd/discord-mcp

test:
	CGO_ENABLED=0 go test -v ./...

test-race:
	CGO_ENABLED=1 go test -race ./...

vet:
	CGO_ENABLED=0 go vet ./...

fmt:
	gofmt -s -w .

clean:
	rm -rf bin dist dist-dxt
