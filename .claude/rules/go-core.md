---
paths: ["cmd/**", "internal/**"]
---

# Go Core Rules

- **CGO_ENABLED=0 always** for builds and non-race tests. Never add a dependency that needs cgo. `modernc.org/sqlite` is the only SQLite driver allowed (no `mattn/go-sqlite3`). The race detector is the single exception: `CGO_ENABLED=1 go test -race` (see `specs/04-release-and-ci.md` §2).
- **Logs go to `os.Stderr` only** via `log/slog` (JSON handler). Never use `fmt.Print*`, `log.Print*`, or `os.Stdout` in `internal/**`. Stdout carries `stdio` JSON-RPC frames, and one stray byte corrupts the session. The only stdout writes allowed are `--version`/`--help` in `cmd/discord-mcp/main.go`.
- **Never log secrets:** bot tokens, webhook tokens and URLs, or `Authorization` headers. Redact them to `"***"`.
- **Mock-backed unit tests:** tests use `discord.MockClient` and a `:memory:` `index.Store`. No test opens a network connection or needs `DISCORD_BOT_TOKEN`. Use synthetic Snowflakes (`1000000000000000xx`). Prefer table-driven tests, and assert on `MockClient.Calls` for side effects.
- **Respect the contract:** `internal/discord/interface.go` and `internal/dto/models.go` are locked after Wave 1 (`specs/01-architecture-and-contracts.md`). Tool code depends on `discord.Service`, never on the concrete client.
- Every exported function takes `context.Context` first and honours cancellation. Wrap errors with `%w`. Use the sentinel errors from `internal/discord`.
- Run `gofmt -s`, `go vet ./...`, and `CGO_ENABLED=0 go build ./...` before declaring work done.
