# Spec 03 — Claude Ecosystem (`ecosystem/`)

**Read when:** Editing `ecosystem/**` or `docs/**`. Tool names and params referenced here are defined in `specs/02-mcp-surface.md`.

```text
ecosystem/
├── dxt/
│   ├── manifest.json
│   ├── icon.png                    # 512×512
│   └── build-dxt.sh
└── claude-plugin/
    ├── .claude-plugin/plugin.json
    ├── .mcp.json
    ├── skills/
    │   ├── discord-community-architect/SKILL.md
    │   ├── discord-knowledge-synthesizer/SKILL.md
    │   └── discord-hitl-persona-ops/SKILL.md
    ├── commands/{digest,announce,provision,approve}.md
    ├── agents/{discord-ops-agent,discord-scribe-agent}.md
    ├── hooks/hooks.json
    └── scripts/notify-discord.sh
```

## 1. Plugin Manifest (`claude-plugin/.claude-plugin/plugin.json`)
The plugin is named `discord`, so commands namespace as `/discord:<cmd>`.
```json
{
  "name": "discord",
  "version": "0.1.0",
  "description": "Discord MCP server with FTS5 search, HITL approvals, multi-persona webhooks, and governance dry-runs.",
  "author": { "name": "discord-mcp contributors" },
  "homepage": "https://github.com/<owner>/discord-mcp",
  "repository": "https://github.com/<owner>/discord-mcp",
  "license": "MIT",
  "keywords": ["discord", "mcp", "community", "moderation", "hitl"]
}
```
- Components (`skills/`, `commands/`, `agents/`, `hooks/hooks.json`, `.mcp.json`) are discovered from their default locations, so the manifest does not list them.
- `version` is kept in sync with the git tag by the release pipeline (spec 04).
- The manifest must work in both Claude Code (`/plugin install`) and Claude Cowork.
- `ecosystem/.claude-plugin/marketplace.json` is a local marketplace, so the plugin can be installed from a clone (`/plugin marketplace add ./ecosystem`). Before the first public release, add a root-level `.claude-plugin/marketplace.json` so `/plugin marketplace add <owner>/discord-mcp` works.

## 2. Connectors (`claude-plugin/.mcp.json`)
```json
{
  "mcpServers": {
    "discord-stdio": {
      "command": "discord-mcp",
      "args": ["--transport", "stdio"],
      "env": {
        "DISCORD_BOT_TOKEN": "${DISCORD_BOT_TOKEN}",
        "DISCORD_MCP_DB_PATH": "${DISCORD_MCP_DB_PATH:-:memory:}",
        "DISCORD_MCP_READ_ONLY": "${DISCORD_MCP_READ_ONLY:-false}"
      }
    },
    "discord-http": {
      "type": "http",
      "url": "http://localhost:8080/mcp"
    }
  }
}
```
- Tokens are **only** referenced as `${ENV}`. Never write a literal value.
- `discord-mcp` must be on `PATH`. The README documents `brew`/tarball install (spec 04).
- Document that users should enable only one of the two servers to avoid duplicate tools (the HTTP one is for a shared long-running instance).

## 3. Skills (`claude-plugin/skills/*/SKILL.md`)
Each skill has YAML frontmatter with `name` (matching the directory) and a `description` written as trigger guidance ("Use when…"). The body is procedural and ≤ 200 lines. Skills reference tools by their full MCP names.

### 3.1 `discord-community-architect`
- **Description:** Use when designing, provisioning, or restructuring a Discord server: categories, channels, forums, roles, and permission overwrites.
- **Procedure:**
  1. Always read `discord://guilds/{guild_id}/map` first. Never guess IDs.
  2. Draft the target structure as a table (category → channels → role overwrites).
  3. Compute the diff against the map. Run **every** mutating call with `dry_run: true` and batch the previews into one summary for the user.
  4. Apply only after explicit confirmation, or after an approval via `discord_await_interaction` when the user is remote. Apply in dependency order: roles → categories → channels → overwrites.
  5. Re-read the map and report the final state.
- Include a permission-name cheat sheet (ViewChannel, SendMessages, ManageMessages, ManageThreads, …) and the rule "deny @everyone ViewChannel + allow role" for private areas.

### 3.2 `discord-knowledge-synthesizer`
- **Description:** Use when summarising, searching, or extracting knowledge (digests, FAQs, decision logs) from Discord channels, threads, or forums.
- **Procedure:** Read the map to choose channels. Use `discord_search_messages` with FTS5 syntax (`"exact phrase"`, `AND`/`OR`/`NOT`, `prefix*`) and `auto_backfill_limit` sized to the window (≈ 500 per active channel per week). Read `discord://channels/{id}/transcript` for chronological context. Use `discord_get_forum_post` for forum threads.
- **Output templates:** daily digest, FAQ (Q/A with source timestamps), decision log (decision · who · when · link).
- **Caveats:** Explain partial-index warnings, respect `limit`, and quote sparingly.

### 3.3 `discord-hitl-persona-ops`
- **Description:** Use when a task needs a human's approval or input via Discord, or when posting as named agent personas through webhooks.
- **HITL procedure:** Write a decision card (what · why · risk · rollback). Call `discord_await_interaction` with explicit `allowed_user_ids`/`allowed_role_ids` and a sensible `timeout_seconds`. Branch on `selected`. Treat `timed_out` as **not approved**. Use `mode: modal` for structured input.
- **Persona procedure:** Register personas once (`discord_persona_register`), then use `discord_persona_send`. Use a thread per workstream (`discord_manage_threads` `create`, then `thread_id`). Keep one persona per role (e.g. `planner`, `reviewer`, `release-bot`).
- **Safety:** Never impersonate real server members. Persona display names must be clearly bot or agent names.

## 4. Slash Commands (`claude-plugin/commands/*.md`)
Each command has frontmatter `description`, `argument-hint`, and `allowed-tools`. Tool names are listed in **both** forms: `mcp__plugin_discord_discord-stdio__<tool>` (the name when the server comes from the plugin) and `mcp__discord-stdio__<tool>` (the name when the server is added standalone). The body uses `$ARGUMENTS`.

| File | Invocation | Args | Behaviour |
| :--- | :--- | :--- | :--- |
| `digest.md` | `/discord:digest` | `<#channel> [since=24h]` | Invokes the knowledge-synthesizer procedure. Outputs the digest in chat. Asks before posting it back to Discord. |
| `announce.md` | `/discord:announce` | `<#channel> <message> [persona=name]` | Drafts and shows the announcement, then sends via `discord_persona_send` (if a persona is given) or `discord_send`. Optionally creates a thread. |
| `provision.md` | `/discord:provision` | `<guild> <goal…>` | Runs the community-architect procedure. Ends with dry-run previews only. Applying requires a follow-up confirmation. |
| `approve.md` | `/discord:approve` | `<#channel> <decision…> [@approver]` | Posts a decision card through `discord_await_interaction` and reports the result. |

## 5. Sub-Agents (`claude-plugin/agents/*.md`)
Frontmatter: `name`, `description`, `tools` (explicit comma-separated list), and `model` (omit to inherit).

### `discord-ops-agent`
- **Duties:** moderation, audit review, timeouts, and provisioning with dry-run.
- **tools:** `mcp__discord-stdio__discord_get_server_info, discord_list_members, discord_manage_roles, discord_set_channel_permissions, discord_create_text_channel, discord_create_voice_channel, discord_create_category, discord_edit_channel, discord_delete_channel, discord_delete_message, discord_bulk_delete_messages, discord_governance_audit, discord_manage_threads, discord_await_interaction, ReadMcpResourceTool`. Write each tool by its full name in both forms (`mcp__plugin_discord_discord-stdio__<tool>` and `mcp__discord-stdio__<tool>`).
- **Rules:** Dry-run first for every destructive action. Require HITL approval for deletes affecting more than 10 messages or any channel or role deletion. Summarise the actions taken with IDs.

### `discord-scribe-agent`
- **Duties:** read-only indexing, search, transcripts, and executive summaries.
- **tools:** `discord_list_servers, discord_get_server_info, discord_read_messages, discord_search_messages, discord_get_forum_post, discord_list_forum_threads, discord_get_poll_results, ReadMcpResourceTool`, all **read-only**.
- **Output:** a structured summary with citations (`[YYYY-MM-DD HH:MM] #channel`).

## 6. Hooks (`claude-plugin/hooks/hooks.json`)
```json
{
  "hooks": {
    "Stop": [
      { "hooks": [ { "type": "command", "command": "${CLAUDE_PLUGIN_ROOT}/scripts/notify-discord.sh stop", "timeout": 10 } ] }
    ],
    "Notification": [
      { "matcher": "permission_prompt", "hooks": [ { "type": "command", "command": "${CLAUDE_PLUGIN_ROOT}/scripts/notify-discord.sh permission", "timeout": 10 } ] }
    ]
  }
}
```
`scripts/notify-discord.sh` (POSIX `sh`, `chmod +x`):
- Reads hook JSON from stdin and posts a short message (project dir basename, event, and the last message truncated to 300 chars) to `$DISCORD_MCP_NOTIFY_WEBHOOK_URL` with `curl -fsS --max-time 5`.
- **Non-blocking:** If the env var is unset, `curl` is missing, or the post fails, it exits `0` silently. It never prints to stdout (stdout could be interpreted as hook output) and always exits 0.
- No token or webhook URL is ever echoed.

## 7. Claude Desktop Extension (`ecosystem/dxt/`)

### `manifest.json`
```json
{
  "dxt_version": "0.1",
  "name": "discord-mcp",
  "display_name": "Discord",
  "version": "0.1.0",
  "description": "Discord MCP server with FTS5 search, HITL approvals, and multi-persona webhooks.",
  "author": { "name": "discord-mcp contributors" },
  "license": "MIT",
  "icon": "icon.png",
  "server": {
    "type": "binary",
    "entry_point": "bin/discord-mcp",
    "mcp_config": {
      "command": "${__dirname}/bin/discord-mcp",
      "args": ["--transport", "stdio"],
      "env": {
        "DISCORD_BOT_TOKEN": "${user_config.discord_bot_token}",
        "DISCORD_MCP_DEFAULT_GUILD_ID": "${user_config.default_guild_id}",
        "DISCORD_MCP_DB_PATH": "${user_config.db_path}",
        "DISCORD_MCP_READ_ONLY": "${user_config.read_only_mode}"
      }
    }
  },
  "user_config": {
    "discord_bot_token": { "type": "string", "title": "Discord Bot Token", "description": "Bot token from the Discord Developer Portal.", "sensitive": true, "required": true },
    "default_guild_id":  { "type": "string", "title": "Default Server ID", "description": "Optional guild Snowflake used when a tool omits guild_id.", "required": false },
    "db_path":           { "type": "string", "title": "Search Index Path", "description": ":memory: (RAM only) or a file path for a persistent index.", "default": ":memory:", "required": false },
    "read_only_mode":    { "type": "boolean", "title": "Read-only Mode", "description": "Block all write and destructive tools.", "default": false, "required": false }
  },
  "tools": [ { "name": "discord_send", "description": "…" } ],
  "compatibility": { "platforms": ["darwin", "linux"] }
}
```
- The `tools` array lists all 44 tools (name + one-line description). `build-dxt.sh` generates it from `bin/discord-mcp --list-tools-json`, a hidden flag Wave 3 adds to `main.go`, so it never drifts.
- `version` is injected at package time.

### `build-dxt.sh`
- Usage: `build-dxt.sh [version]` (defaults to `git describe --tags --always`).
- For each target in `darwin/arm64 linux/amd64 linux/arm64`: build with `CGO_ENABLED=0 GOOS GOARCH go build -trimpath -ldflags="-s -w -X main.version=…"` into a staging dir `dist-dxt/stage/<os>_<arch>/bin/discord-mcp`, then copy `manifest.json` (with `version` and `compatibility.platforms` set to the single OS via `jq`) and `icon.png`.
- Zip each staging dir to `dist-dxt/discord-mcp_<version>_<os>_<arch>.dxt`, then remove `dist-dxt/stage/`. Write to `dist-dxt/`, never `dist/`, because GoReleaser `--clean` wipes `dist/`. (a `.dxt` is a zip). If `npx @anthropic-ai/dxt` is available, run `dxt validate` first.
- `set -euo pipefail`, no network access other than `go` module download, and it prints the produced paths.

## 8. Docs (`docs/`)
- `ARCHITECTURE.md`: component diagram (Mermaid) of the MCP transport → tools → policy guard → `discord.Service` → discordgo REST/Gateway → FTS5 index, data flows for search backfill, HITL, and persona send, plus the security model.
- `TOOL_REFERENCE.md`: all 44 tools with parameter tables and one example call/response each, plus resources and prompts. Generate it from the `--list-tools-json` output where possible.
- All examples use synthetic Snowflakes (`1000000000000000xx`) and placeholder tokens (`YOUR_BOT_TOKEN`).
