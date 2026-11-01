# discord-mcp

A static, CGO-free Go [Model Context Protocol](https://modelcontextprotocol.io)
server for Discord — one binary, no runtime dependencies — plus a complete Claude
ecosystem suite: a Claude Desktop extension (`.dxt`), and a Claude Code / Cowork
plugin with skills, slash commands, sub-agents, and hooks. It offers 100%
baseline parity with [`barryyip0625/mcp-discord`](https://github.com/barryyip0625/mcp-discord)
(28 tools) plus 9 differentiating tools, for **37 tools** in total.

- **Embedded SQLite + FTS5 search** — works around Discord's `403 Forbidden`
  bot-search restriction with real-time Gateway indexing and lazy REST backfill;
  runs on disk or RAM-only (`:memory:`, the default).
- **Human-in-the-loop components** — `discord_await_interaction` posts Buttons,
  Select Menus, or Modals and waits for an authorised human, so Claude can ask
  for approval from your phone.
- **Multi-persona webhooks** — register named agent personas and post as them;
  channel webhooks are created and reused automatically, with thread support.
- **Multi-profile bot routing** — a single `DISCORD_BOT_TOKEN` or several named
  profiles (`--config`) to switch bot identities.
- **Full MCP surface** — Tools, `discord://` Resources with live subscriptions,
  and Prompts; compact DTOs; `#channel` / `@user` names resolve to Snowflakes.
- **Governance & dry-run policy engine** — `--read-only`, guild/channel
  allowlists and blocklists, rate-limit telemetry, and `dry_run: true` previews
  for every destructive operation.

## Discord setup

1. Open the [Discord Developer Portal](https://discord.com/developers/applications)
   → **New Application**.
2. **Bot** → **Reset Token** and copy the token into your environment as
   `DISCORD_BOT_TOKEN` (never commit it).
3. **Bot → Privileged Gateway Intents**: enable **MESSAGE CONTENT** (search
   indexing, transcripts) and **SERVER MEMBERS** (member tools).
4. Invite the bot with an OAuth2 URL (replace `YOUR_CLIENT_ID`):

   ```text
   # Read & search only (View Channels + Read Message History)
   https://discord.com/oauth2/authorize?client_id=YOUR_CLIENT_ID&scope=bot&permissions=66560

   # Full feature set (messaging, moderation, channels, roles, webhooks, threads, events, timeouts, audit log)
   https://discord.com/oauth2/authorize?client_id=YOUR_CLIENT_ID&scope=bot&permissions=1504043986160
   ```

   See [SECURITY.md](SECURITY.md#minimum-bot-permissions) for the per-feature
   permission table. Role management only works for roles below the bot's
   highest role.

## Install on macOS / Linux

Download the archive for your platform from
[Releases](https://github.com/OWNER/discord-mcp/releases) along with
`checksums.txt`, verify, and install:

```sh
VERSION=0.1.0
OS=darwin      # or linux
ARCH=arm64     # or amd64
curl -LO "https://github.com/OWNER/discord-mcp/releases/download/v${VERSION}/discord-mcp_${VERSION}_${OS}_${ARCH}.tar.gz"
curl -LO "https://github.com/OWNER/discord-mcp/releases/download/v${VERSION}/checksums.txt"

# macOS
shasum -a 256 -c checksums.txt --ignore-missing
# Linux
sha256sum -c checksums.txt --ignore-missing

tar -xzf "discord-mcp_${VERSION}_${OS}_${ARCH}.tar.gz"
sudo install -m 0755 discord-mcp /usr/local/bin/
discord-mcp --version
```

macOS: release binaries are not notarised. If Gatekeeper blocks the binary, run
`xattr -d com.apple.quarantine /usr/local/bin/discord-mcp`.

Or build from source (Go 1.26+):

```sh
CGO_ENABLED=0 go install github.com/OWNER/discord-mcp/cmd/discord-mcp@latest
```

## Claude Desktop

1. Download the `.dxt` bundle for your platform from Releases
   (`discord-mcp_<version>_<os>_<arch>.dxt`).
2. Double-click it, or open **Settings → Extensions → Install Extension…** and
   pick the file.
3. In the extension settings, fill in **Discord Bot Token** (stored as a
   sensitive value), and optionally **Default Server ID**, **Search Index
   Path** (`:memory:` or a file path), and **Read-only Mode**.

## Claude Code

Add the server directly:

```sh
claude mcp add discord-stdio -e DISCORD_BOT_TOKEN=YOUR_BOT_TOKEN -- discord-mcp --transport stdio
```

Or install the plugin, which adds the server plus skills, commands
(`/discord:digest`, `/discord:announce`, `/discord:provision`,
`/discord:approve`), the `discord-ops-agent` and `discord-scribe-agent`
sub-agents, and lifecycle hooks:

```text
# from a local clone of this repository
/plugin marketplace add ./discord-mcp/ecosystem
/plugin install discord@discord-mcp
```

The marketplace manifest lives at `ecosystem/.claude-plugin/marketplace.json`.

The plugin reads `DISCORD_BOT_TOKEN` (and optionally `DISCORD_MCP_DB_PATH`,
`DISCORD_MCP_READ_ONLY`) from your environment. It defines two connectors —
`discord-stdio` (spawns `discord-mcp`, which must be on `PATH`) and
`discord-http` (`http://localhost:8080/mcp`, for a shared long-running
instance started with `discord-mcp --transport http`). **Enable only one of
them** to avoid duplicate tools (`/mcp` → disable the other).

Optional: set `DISCORD_MCP_NOTIFY_WEBHOOK_URL` to a Discord webhook URL to get a
message when a session finishes or is waiting for permission. The hook is
silent and non-blocking when unset.

## Claude Cowork

Install the `ecosystem/claude-plugin` plugin (plugin name `discord`) from this
repository's marketplace, and set `DISCORD_BOT_TOKEN` in the environment the
plugin's MCP server runs in. The same skills, commands, and sub-agents are
available as in Claude Code.

## Configuration

Precedence: **flag > env > config file > default**. See [.env.example](.env.example).

| Flag | Env var | Default | Notes |
| :--- | :--- | :--- | :--- |
| `--token` | `DISCORD_BOT_TOKEN` | — | Prefer the env var. Never logged. |
| `--config` | `DISCORD_MCP_CONFIG` | — | JSON file defining named `profiles` |
| `--profile` | `DISCORD_MCP_PROFILE` | `default` | Active profile at startup |
| `--transport` | `DISCORD_MCP_TRANSPORT` | `stdio` | `stdio` or `http` |
| `--http-addr` | `DISCORD_MCP_HTTP_ADDR` | `127.0.0.1:8080` | Loopback by default; endpoint `/mcp` |
| `--db-path` | `DISCORD_MCP_DB_PATH` | `:memory:` | e.g. `~/.config/discord-mcp/index.db` |
| `--read-only` | `DISCORD_MCP_READ_ONLY` | `false` | Blocks write and destructive tools |
| `--allowed-guilds` | `DISCORD_MCP_ALLOWED_GUILDS` | empty (all) | Comma-separated Snowflakes |
| `--blocked-guilds` | `DISCORD_MCP_BLOCKED_GUILDS` | empty | |
| `--allowed-channels` | `DISCORD_MCP_ALLOWED_CHANNELS` | empty (all) | |
| `--blocked-channels` | `DISCORD_MCP_BLOCKED_CHANNELS` | empty | |
| `--default-guild` | `DISCORD_MCP_DEFAULT_GUILD_ID` | — | Used when a tool omits `guild_id` |
| `--log-level` | `DISCORD_MCP_LOG_LEVEL` | `info` | Logs go to stderr only |
| `--version` | — | — | Prints version, commit, and build date |

Profiles file — tokens are referenced by env var name, never inline:

```json
{ "profiles": { "ops": { "token_env": "DISCORD_OPS_TOKEN", "allowed_guilds": ["100000000000000001"], "read_only": false } } }
```

### Safety features

- **`--read-only`** blocks every write and destructive tool; dry runs still
  work so Claude can plan.
- **Allowlists / blocklists** for guilds and channels (threads are checked
  against their parent channel).
- **`dry_run: true`** on destructive tools returns a `DryRunPreview` (what would
  change, warnings) with zero mutating API calls.
- Bulk deletes are limited to 2–100 messages younger than 14 days.

## Tools, resources, and prompts

37 tools — full parameter tables and examples in
[docs/TOOL_REFERENCE.md](docs/TOOL_REFERENCE.md).

| Area | Tools |
| :--- | :--- |
| Session & guilds | `discord_login`, `discord_list_servers`, `discord_get_server_info` |
| Messages | `discord_send`, `discord_read_messages`, `discord_edit_message`, `discord_delete_message`, `discord_bulk_delete_messages` |
| Reactions & polls | `discord_add_reaction`, `discord_add_multiple_reactions`, `discord_remove_reaction`, `discord_create_poll`, `discord_get_poll_results` |
| Channels | `discord_create_text_channel`, `discord_create_voice_channel`, `discord_create_category`, `discord_edit_channel`, `discord_delete_channel`, `discord_set_channel_permissions` |
| Forums & threads | `discord_create_forum_channel`, `discord_create_forum_post`, `discord_get_forum_post`, `discord_reply_to_forum`, `discord_list_forum_threads`, `discord_manage_threads` |
| Members & roles | `discord_list_members`, `discord_manage_roles` |
| Webhooks & personas | `discord_create_webhook`, `discord_send_webhook_message`, `discord_edit_webhook`, `discord_delete_webhook`, `discord_persona_register`, `discord_persona_send` |
| Search | `discord_search_messages` |
| HITL | `discord_await_interaction` |
| Events & governance | `discord_manage_events`, `discord_governance_audit` |

**Resources:** `discord://guilds` (guild list), `discord://guilds/{guild_id}/map`
(Markdown map of roles, categories, channels, threads), and
`discord://channels/{channel_id}/transcript` (chronological transcript with
resolved mentions). Map and transcript support subscriptions.

**Prompts:** `discord-channel-digest`, `discord-server-blueprint`,
`discord-hitl-approval`.

Architecture and data flows: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Development

```sh
make build       # CGO_ENABLED=0 static binary → bin/discord-mcp
make test        # CGO_ENABLED=0 go test -v ./...
make test-race   # CGO_ENABLED=1 go test -race ./...  (race runtime needs cgo on Linux)
make lint        # go vet + golangci-lint
make smoke       # stdio initialize + tools/list, asserts 37 tools
make build-all   # darwin/linux × amd64/arm64 → dist/
make dxt         # Claude Desktop bundles → dist-dxt/
```

Tests are mock-backed and never touch the network. Use synthetic Snowflakes
(`1000000000000000xx`) and `YOUR_BOT_TOKEN` in tests, docs, and issues.

## License & security

MIT — see [LICENSE](LICENSE). Report vulnerabilities privately as described in
[SECURITY.md](SECURITY.md).
