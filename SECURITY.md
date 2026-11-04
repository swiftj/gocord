# Security Policy

## Supported versions

Only the latest minor release line receives security fixes.

| Version | Supported |
| :--- | :--- |
| latest minor (e.g. 0.x latest) | Yes |
| older releases | No |

## Reporting a vulnerability

Report vulnerabilities **privately** through GitHub Security Advisories
(repository → *Security* → *Report a vulnerability*). **Do not open public
issues** for security problems.

We aim to acknowledge reports within **72 hours** and will keep you updated on
the fix and disclosure timeline. Please include a description, affected
version, reproduction steps, and impact. Never include real bot tokens or
webhook URLs in a report — use placeholders such as `YOUR_BOT_TOKEN`.

## Threat model notes

- **Bot token handling.** The token is read from the environment
  (`DISCORD_BOT_TOKEN`) or from an env var named by a profile's `token_env`.
  It is never logged, never returned by any tool, and profile files containing
  a literal `"token"` key are rejected. Claude Desktop stores it through the
  extension's `sensitive` user config.
- **Webhook tokens.** Webhooks created for personas have their tokens stored
  only in the local index database, which is created with `0600` permissions
  (parent directory `0700`). The default `:memory:` mode keeps them in RAM only.
  Tool output never includes webhook tokens or URLs.
- **HTTP transport.** `--transport http` binds `127.0.0.1:8080` by default and
  has no built-in authentication. If you expose it beyond loopback, put it
  behind an authenticating reverse proxy or a private network.
- **Defence in depth.** Use `--read-only` and `dry_run: true` previews to bound what an LLM can do. Policy
  denials are enforced server-side before any Discord API call.
- **Logs.** Structured logs go to stderr only; stdout carries JSON-RPC frames.
- **Hooks.** The Claude plugin's notification hook posts only to
  `DISCORD_MCP_NOTIFY_WEBHOOK_URL`, never echoes it, and always exits 0.

## Minimum bot permissions

Grant only what you use. The privileged gateway intents `MESSAGE CONTENT` and
`SERVER MEMBERS` must be enabled in the Developer Portal for search indexing and
member tools respectively.

| Feature set | Tools | Permissions |
| :--- | :--- | :--- |
| Read & search | `discord_read_messages`, `discord_search_messages`, forum reads, resources | View Channels, Read Message History |
| Messaging | `discord_send`, `discord_edit_message`, `discord_reply_to_forum`, polls | Send Messages, Send Messages in Threads, Embed Links, Attach Files |
| Reactions | `discord_add_reaction`, `discord_add_multiple_reactions`, `discord_remove_reaction` | Add Reactions (Manage Messages to remove others' reactions) |
| Moderation | `discord_delete_message`, `discord_bulk_delete_messages` | Manage Messages |
| Threads | `discord_manage_threads`, forum posts | Create Public/Private Threads, Manage Threads |
| Channels | create/edit/delete channels, permission overwrites | Manage Channels, Manage Roles (for overwrites) |
| Roles | `discord_manage_roles` | Manage Roles (only for roles below the bot's highest role) |
| Members | `discord_list_members` | `SERVER MEMBERS` intent |
| Timeouts & audit | `discord_governance_audit` | Moderate Members, View Audit Log, Manage Server (AutoMod rules) |
| Webhooks & personas | webhook tools, `discord_persona_send` | Manage Webhooks |
| Events | `discord_manage_events` | Manage Events |
| HITL | `discord_await_interaction` | Send Messages (interactions need no extra permission) |
