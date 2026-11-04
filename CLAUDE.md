# CLAUDE.md — `discord-mcp`

Zero-dependency Golang Discord MCP server (`stdio` + `Streamable HTTP`) with embedded SQLite FTS5 search, Human-in-the-Loop (HITL) Discord interactions, Multi-Persona webhook routing, and a complete Claude Desktop (`.dxt`), Cowork, and Code (`.claude-plugin`) suite.

## 1. Locked Technical Stack
- **Language:** Go 1.26+ (minimum required by `mcp-go` v1.5.0). `CGO_ENABLED=0` mandatory on all builds and non-race tests; race tests use `CGO_ENABLED=1` (Linux `-race` requires cgo — see `specs/04-release-and-ci.md` §2).
- **Target Platforms:** `darwin/arm64`, `linux/amd64`, `linux/arm64`. Intel Macs (`darwin/amd64`) are not supported.
- **Core Dependencies:**
  - MCP SDK: `github.com/mark3labs/mcp-go` (`stdio` & `Streamable HTTP` on `/mcp`)
  - Discord: `github.com/bwmarrin/discordgo` (REST + Gateway v10)
  - FTS5 Indexer: `modernc.org/sqlite` (CGO-free SQLite; defaults to `:memory:`, configurable to disk)
  - Images: `golang.org/x/image` (pure-Go resize + WebP decode)
- **Logging:** `log/slog` JSON strictly to `os.Stderr`. **NEVER write logs to `os.Stdout`** (corrupts `stdio` JSON-RPC frames).

## 2. Public Repository & Security Rules
- **Zero Secrets in Git:** Never commit tokens, webhook URLs, real Guild/User Snowflakes, `.env` files, or `.db` files. Use synthetic Snowflakes (`"100000000000000001"`) in all tests and docs.
- **Reproducible Builds:** Always build with `CGO_ENABLED=0 -trimpath -ldflags="-s -w"`.
- **Safe Defaults:** Server starts with `--db-path :memory:` by default and respects `--read-only`, `--allowed-guilds`, and `dry_run: true` on destructive tools.

## 3. Essential Commands
- **Build Local Binary:** `make build` (`CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/discord-mcp ./cmd/discord-mcp`)
- **Cross-Compile Matrix (macOS/Linux):** `make build-all`
- **Test:** `make test` (`CGO_ENABLED=0 go test -v ./...`) · **Race Check:** `make test-race` (`CGO_ENABLED=1 go test -race ./...`)
- **Lint & Vet:** `make lint` (`go vet ./... && golangci-lint run`)
- **Dry-Run Release:** `goreleaser release --snapshot --clean`
- **Package Desktop Extension:** `./ecosystem/dxt/build-dxt.sh`

## 4. Progressive Disclosure Context Map
Do NOT guess interfaces or schemas. Read ONLY the specification file relevant to your active task:

| Spec File | When to Read | Contents |
| :--- | :--- | :--- |
| `specs/01-architecture-and-contracts.md` | Wave 1 or editing `internal/discord/`, `internal/dto/`, `internal/index/` | Monorepo layout, `discord.Service` Go interface, DTO models, SQLite FTS5 schema, Policy guard. |
| `specs/02-mcp-surface.md` | Editing `internal/mcp/**` | The 37 core MCP Tool schemas (28 baseline + 9 next-gen), `discord://` Resources, and Prompts. |
| `specs/05-media-and-local-files.md` | Editing `internal/media/` or the media/file/tupper/access tools | Message identity fields, media resolver and local media root, 7 media/file/diagnostic tools (38–44), index schema v2. |
| `specs/03-claude-ecosystem.md` | Editing `ecosystem/**` or `docs/**` | `.claude-plugin` manifest, 3 `SKILL.md` specs, 4 Slash Commands, 2 Sub-Agents, Hooks, and `.dxt` spec. |
| `specs/04-release-and-ci.md` | Editing `.github/**`, `.goreleaser.yaml`, `Makefile` | GitHub Actions CI/CD, macOS/Linux cross-compilation matrix, GoReleaser, `.gitignore`, OSS files. |

Path-scoped rules in `.claude/rules/*.md` load automatically when you touch matching files.

## 5. Multi-Agent Wave Execution Plan

### Wave 1: Bootstrap & Contract Lock (Lead Agent — Synchronous)
1. Read `specs/01-architecture-and-contracts.md` and `specs/04-release-and-ci.md`.
2. Initialize `git`, `.gitignore`, `go.mod`, `Makefile`, `internal/config/`, `internal/dto/`, `internal/policy/`, `internal/discord/interface.go`, `internal/discord/mock_client.go`, and `internal/mcp/server.go`.
3. Verify `CGO_ENABLED=0 go build ./...` succeeds before spawning Wave 2.

### Wave 2: Parallel Teammates (Strict File Ownership)
Spawn 4 parallel teammates. Each teammate reads ONLY its assigned spec file(s):

1. **`infra-and-index`** (Read `specs/01-architecture-and-contracts.md`)
   - **Owns:** `cmd/discord-mcp/`, `internal/discord/client.go`, `internal/discord/resolver.go` (+ their tests), `go.mod`/`go.sum` (sole owner of dependency changes)
   - **Deliverable:** Live `discordgo` wrapper (incl. HITL interaction broker), `#channel`/`@user` Snowflake resolver, Gateway → `index.Store` ingestion, and CLI (`stdio` + `http`). The FTS5 `internal/index` store was completed in Wave 1 (Lead-owned).
2. **`mcp-baseline-tools`** (Read `specs/02-mcp-surface.md`)
   - **Owns:** `internal/mcp/tools/{guilds,channels,messages,forums_threads,reactions_polls,members_roles}.go`, `internal/mcp/resources/`
   - **Deliverable:** All 28 baseline tools + Polls, Threads, Governance Audit, `discord://` Resources (`map`, `transcript`), and unit tests.
3. **`mcp-nextgen-tools`** (Read `specs/02-mcp-surface.md`)
   - **Owns:** `internal/mcp/tools/{search,webhooks_persona,interactions,events}.go`, `internal/mcp/prompts/`
   - **Deliverable:** FTS5 `discord_search_messages` with REST backfill, HITL `discord_await_interaction`, Multi-Persona webhook engine, Scheduled Events, Prompts, and unit tests.
4. **`ecosystem-and-release`** (Read `specs/03-claude-ecosystem.md` & `specs/04-release-and-ci.md`)
   - **Owns:** `ecosystem/`, `.github/workflows/`, `.goreleaser.yaml`, `Makefile`, `docs/`, `README.md`, `LICENSE`, `SECURITY.md`
   - **Deliverable:** `.claude-plugin`, `.dxt` packager, Skills, Commands, Agents, Hooks, GitHub Actions CI + Release pipelines for `darwin/arm64` and `linux/{amd64,arm64}`.

### Wave 3: Integration & Release Verification (Lead Agent)
1. Wire all registrations in `internal/mcp/server.go` and `cmd/discord-mcp/main.go`.
2. Run `make test` and `make build-all` to verify all 3 macOS/Linux binaries compile cleanly.
3. Smoke-test `stdio` JSON-RPC `initialize` and `tools/list` (verifying all 44 tools).
