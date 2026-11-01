# Spec 04 — Release, CI & Repository Hygiene

**Read when:** Editing `.github/**`, `.goreleaser.yaml`, `Makefile`, or the root OSS files (`README.md`, `LICENSE`, `SECURITY.md`, `.gitignore`, `.env.example`).

## 1. Build Matrix
| GOOS | GOARCH |
| :--- | :--- |
| darwin | arm64 |
| darwin | amd64 |
| linux | amd64 |
| linux | arm64 |

Every shipped binary is built with:
```sh
CGO_ENABLED=0 go build -trimpath \
  -ldflags="-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)" \
  -o bin/discord-mcp ./cmd/discord-mcp
```
`cmd/discord-mcp/main.go` declares `var version, commit, date = "dev", "none", "unknown"`.
Windows is out of scope for v1. Do not add it to the matrix.

## 2. Race Detector Caveat (verified)
`go test -race` with `CGO_ENABLED=0` works on **darwin** but fails on **linux** with `go: -race requires cgo`. So:
- **Shipped binaries:** always `CGO_ENABLED=0`.
- **Race tests:** run with `CGO_ENABLED=1` (`make test-race`). This is safe because `modernc.org/sqlite` is pure Go, so cgo only enables the race runtime.
- **Non-race tests:** also run with `CGO_ENABLED=0` (`make test`) to prove the pure-Go build path.

## 3. Makefile
Targets (all `.PHONY`). `VERSION ?= $(shell git describe --tags --always --dirty)`.

| Target | Command |
| :--- | :--- |
| `build` | the §1 command for the host platform → `bin/discord-mcp` |
| `build-all` | loop over the §1 matrix → `dist/discord-mcp_$(os)_$(arch)` |
| `test` | `CGO_ENABLED=0 go test -v ./...` |
| `test-race` | `CGO_ENABLED=1 go test -race ./...` |
| `lint` | `go vet ./... && golangci-lint run` |
| `fmt` | `gofmt -s -w . && go mod tidy` |
| `dxt` | `./ecosystem/dxt/build-dxt.sh $(VERSION)` |
| `snapshot` | `goreleaser release --snapshot --clean` |
| `smoke` | `build`, then pipe an `initialize` + `tools/list` JSON-RPC pair into `bin/discord-mcp --transport stdio` and assert 37 tools with `jq` |
| `clean` | `rm -rf bin dist` |

`.golangci.yml`: enable `govet`, `staticcheck`, `errcheck`, `ineffassign`, `unused`, `gosec`, `forbidigo`. Configure `forbidigo` to forbid `fmt.Print*` and `os.Stdout` in `internal/**` and `cmd/**`, except `cmd/discord-mcp/main.go`'s `--version`/`--help` output.

## 4. GoReleaser (`.goreleaser.yaml`)
```yaml
version: 2
project_name: discord-mcp
before:
  hooks:
    - go mod tidy
    - go test ./...
builds:
  - id: discord-mcp
    main: ./cmd/discord-mcp
    binary: discord-mcp
    env: [CGO_ENABLED=0]
    flags: [-trimpath]
    ldflags: ["-s -w -X main.version={{.Version}} -X main.commit={{.ShortCommit}} -X main.date={{.Date}}"]
    goos: [darwin, linux]
    goarch: [amd64, arm64]
    mod_timestamp: "{{ .CommitTimestamp }}"
archives:
  - formats: [tar.gz]
    name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    files: [LICENSE, README.md]
checksum:
  name_template: "checksums.txt"
  algorithm: sha256
  extra_files:
    - glob: ./dist-dxt/*.dxt
snapshot:
  version_template: "{{ incpatch .Version }}-next"
changelog:
  use: github
  filters:
    exclude: ["^docs:", "^chore:", "^test:"]
release:
  draft: false
  prerelease: auto
  extra_files:
    - glob: ./dist-dxt/*.dxt
```
- Trigger: push of tags matching `v*`.
- `.dxt` bundles: the release workflow runs `build-dxt.sh` **before** GoReleaser. The script writes to `dist-dxt/`, not `dist/`, so GoReleaser's `--clean` does not wipe the bundles. `release.extra_files` uploads them, and `checksum.extra_files` adds them to `checksums.txt`.
- Reproducibility: `-trimpath` plus `mod_timestamp` from the commit.

## 5. GitHub Actions

### `.github/workflows/ci.yml`
```yaml
name: ci
on:
  push: { branches: [main] }
  pull_request:
permissions: { contents: read }
concurrency: { group: "ci-${{ github.ref }}", cancel-in-progress: true }
jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: go.mod }
      - run: test -z "$(gofmt -s -l .)"
      - run: go vet ./...
      - uses: golangci/golangci-lint-action@v8 # v2 config format requires >= v7
  test:
    strategy:
      fail-fast: false
      matrix: { os: [ubuntu-latest, macos-latest] }
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: go.mod }
      - run: CGO_ENABLED=0 go test ./...
      - run: CGO_ENABLED=1 go test -race ./...
  build:
    runs-on: ubuntu-latest
    needs: [lint, test]
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: go.mod }
      - run: make build-all
      - run: make smoke
      - run: ./ecosystem/dxt/build-dxt.sh ci-${{ github.sha }}
  secrets-scan:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - uses: gitleaks/gitleaks-action@v2
        env: { GITHUB_TOKEN: "${{ secrets.GITHUB_TOKEN }}" }
```
Pin third-party actions to a full commit SHA, with the version in a trailing comment, before the first public release.

### `.github/workflows/release.yml`
```yaml
name: release
on:
  push: { tags: ["v*"] }
permissions: { contents: write }
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - uses: actions/setup-go@v5
        with: { go-version-file: go.mod }
      - run: CGO_ENABLED=1 go test -race ./...
      - run: ./ecosystem/dxt/build-dxt.sh "${GITHUB_REF_NAME#v}"
      - uses: goreleaser/goreleaser-action@v6
        with: { distribution: goreleaser, version: "~> v2", args: release --clean }
        env: { GITHUB_TOKEN: "${{ secrets.GITHUB_TOKEN }}" }
```
- Release assets: 4 `tar.gz` archives, 4 `.dxt` bundles, and `checksums.txt` (SHA-256 covering all of them).
- The only secret used is the built-in `GITHUB_TOKEN`. No Discord token is needed in CI because tests are mock-backed.

## 6. Repository Hygiene

### `.gitignore`
```gitignore
/bin/
/dist/
/dist-dxt/
*.dxt
*.db
*.db-shm
*.db-wal
.env
.env.*
!.env.example
*.test
*.out
coverage.*
.DS_Store
.idea/
.vscode/
.claude/settings.local.json
```

### `.env.example`
Lists every variable from spec 01 §6 with placeholder or blank values and a comment for each. Use `DISCORD_BOT_TOKEN=` (empty) and `DISCORD_MCP_NOTIFY_WEBHOOK_URL=`, never a realistic-looking token.

### `SECURITY.md`
- Supported versions: latest minor.
- Report privately via GitHub Security Advisories. Do not open public issues.
- Response target: acknowledge within 72 h.
- Threat model notes: bot-token handling (env only, never logged), webhook tokens stored only in the local index DB (`0600`), the HTTP transport binds `127.0.0.1` by default and must sit behind auth if exposed, and `--read-only`/allowlists as defence in depth.
- Minimum bot permissions table per feature set.

### `LICENSE`
MIT, `Copyright (c) 2026 discord-mcp contributors`.

### `README.md`
Sections, in order:
1. One-paragraph pitch + feature bullets (the 6 differentiators).
2. **Discord setup:** create the application and bot, enable the privileged intents (`MESSAGE CONTENT`, `SERVER MEMBERS`), and give an OAuth2 invite URL template with the minimum permissions.
3. **Install on macOS/Linux:** a tarball download from Releases + `shasum -a 256 -c` / `sha256sum -c` verification + `install -m 0755 discord-mcp /usr/local/bin/`, or `go install …/cmd/discord-mcp@latest` (with `CGO_ENABLED=0`). macOS note: `xattr -d com.apple.quarantine` for unsigned binaries.
4. **Claude Desktop:** download the platform `.dxt` and double-click it (or Settings → Extensions → Install). Fill in the token in the extension settings.
5. **Claude Code:** `claude mcp add discord -e DISCORD_BOT_TOKEN=… -- discord-mcp`, or install the plugin from `ecosystem/claude-plugin` (`/plugin marketplace add …` → `/plugin install discord`).
6. **Claude Cowork:** install the `ecosystem/claude-plugin` plugin. Set `DISCORD_BOT_TOKEN` in the environment.
7. Configuration table (from spec 01 §6), safety features (`--read-only`, allowlists, `dry_run`).
8. Tool overview (37 tools, linking to `docs/TOOL_REFERENCE.md`), resources, prompts.
9. Development: `make build`, `make test`, `make test-race`, `make smoke`.
10. License + security links.

All examples use synthetic Snowflakes and `YOUR_BOT_TOKEN`.

## 7. Quality Gates (Definition of Done)
A build is complete only when all of these pass:
- [ ] `make build` (`CGO_ENABLED=0 … go build`) succeeds with zero warnings, and `make build-all` produces all 4 binaries.
- [ ] `make test` (`CGO_ENABLED=0`) and `make test-race` (`CGO_ENABLED=1 -race`) pass across `internal/index`, `internal/discord`, `internal/policy`, and `internal/mcp/...`.
- [ ] `make lint` is clean.
- [ ] `make smoke`: a stdio `tools/list` returns exactly 37 tools with descriptions and typed JSON schemas.
- [ ] All `ecosystem/claude-plugin` skills, commands, agents, and hooks, plus the `.dxt` manifest, are populated and valid (JSON parses, frontmatter is present, and `dxt validate` passes when available).
- [ ] `gitleaks` finds no secrets.
