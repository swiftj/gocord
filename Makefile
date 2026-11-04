# discord-mcp — build, test, lint, package. See specs/04-release-and-ci.md §3.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

BINARY         := bin/discord-mcp
PLATFORMS      := darwin/arm64 linux/amd64 linux/arm64
EXPECTED_TOOLS ?= 44

# JSON-RPC frames for the stdio smoke test (newline-delimited).
SMOKE_INIT  := {"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"make-smoke","version":"0.0.0"}}}
SMOKE_READY := {"jsonrpc":"2.0","method":"notifications/initialized"}
SMOKE_LIST  := {"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}

.PHONY: all build build-all test test-race vet lint fmt dxt snapshot smoke clean

all: build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY) ./cmd/discord-mcp

build-all:
	@mkdir -p dist
	@set -e; for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags="$(LDFLAGS)" \
			-o dist/discord-mcp_$${os}_$${arch} ./cmd/discord-mcp; \
	done

test:
	CGO_ENABLED=0 go test -v ./...

test-race:
	CGO_ENABLED=1 go test -race ./...

vet:
	CGO_ENABLED=0 go vet ./...

lint:
	CGO_ENABLED=0 go vet ./... && golangci-lint run

fmt:
	gofmt -s -w . && go mod tidy

dxt:
	./ecosystem/dxt/build-dxt.sh $(VERSION)

snapshot:
	goreleaser release --snapshot --clean

# Pipe initialize + notifications/initialized + tools/list into the stdio server
# (no token needed) and assert exactly $(EXPECTED_TOOLS) tools with typed schemas.
smoke: build
	@command -v jq >/dev/null 2>&1 || { echo "smoke: jq is required" >&2; exit 1; }
	@set -e; \
	out=$$( { printf '%s\n' '$(SMOKE_INIT)'; printf '%s\n' '$(SMOKE_READY)'; printf '%s\n' '$(SMOKE_LIST)'; sleep 2; } \
		| ./$(BINARY) --transport stdio 2>/dev/null || true); \
	resp=$$(printf '%s\n' "$$out" | jq -c 'select(type == "object" and .id == 2)' 2>/dev/null | head -n 1); \
	if [ -z "$$resp" ]; then echo "smoke: no tools/list response" >&2; printf '%s\n' "$$out" | head -c 2000 >&2; exit 1; fi; \
	count=$$(printf '%s' "$$resp" | jq '.result.tools | length'); \
	bad=$$(printf '%s' "$$resp" | jq '[.result.tools[] | select((.description // "") == "" or .inputSchema.type != "object")] | length'); \
	echo "smoke: tools/list returned $$count tools (expected $(EXPECTED_TOOLS))"; \
	if [ "$$count" != "$(EXPECTED_TOOLS)" ]; then \
		printf '%s' "$$resp" | jq -r '.result.tools[].name' | sort >&2; exit 1; fi; \
	if [ "$$bad" != "0" ]; then echo "smoke: $$bad tools lack a description or object inputSchema" >&2; exit 1; fi; \
	echo "smoke: OK"

clean:
	rm -rf bin dist dist-dxt
