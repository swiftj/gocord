---
description: Summarise recent Discord discussion in a channel (decisions, open questions, action items).
argument-hint: "<#channel> [since=24h]"
allowed-tools: mcp__plugin_discord_discord-stdio__discord_list_servers, mcp__plugin_discord_discord-stdio__discord_get_server_info, mcp__plugin_discord_discord-stdio__discord_read_messages, mcp__plugin_discord_discord-stdio__discord_search_messages, mcp__plugin_discord_discord-stdio__discord_get_forum_post, mcp__plugin_discord_discord-stdio__discord_list_forum_threads, mcp__plugin_discord_discord-stdio__discord_send, mcp__plugin_discord_discord-stdio__discord_persona_send, mcp__discord-stdio__discord_list_servers, mcp__discord-stdio__discord_get_server_info, mcp__discord-stdio__discord_read_messages, mcp__discord-stdio__discord_search_messages, mcp__discord-stdio__discord_get_forum_post, mcp__discord-stdio__discord_list_forum_threads, mcp__discord-stdio__discord_send, mcp__discord-stdio__discord_persona_send, ReadMcpResourceTool, ListMcpResourcesTool
---

Produce a digest of Discord activity.

Arguments: `$ARGUMENTS`

Parse the arguments as `<#channel> [since=<window>]`. The window accepts `24h`, `7d`, or an ISO-8601 timestamp; default `24h`.

Follow the **discord-knowledge-synthesizer** skill:

1. Resolve the channel. If the guild is ambiguous, call `discord_list_servers`, then read `discord://guilds/{guild_id}/map` to find the channel ID. Never guess IDs.
2. Read `discord://channels/{channel_id}/transcript` (use `?limit=` up to 500 for longer windows) and/or `discord_read_messages` with `after` set to the start of the window. For a forum, list posts with `discord_list_forum_threads` and read them with `discord_get_forum_post`.
3. If the window is larger than the transcript, use `discord_search_messages` with `channel_id`, `after`, and an `auto_backfill_limit` of roughly 500 per week.
4. Output the digest **in chat** with sections: **Summary**, **Decisions**, **Open Questions**, **Action Items (owner → task)**, **Notable Links**. Cite each item as `[YYYY-MM-DD HH:MM] #channel`.
5. Mention any `partial index` warning.
6. **Ask before posting** the digest back to Discord. Only if the user agrees, send it with `discord_send` (or `discord_persona_send` if they name a persona), split into messages of at most 2000 characters.

Tool names: when installed as the `discord` plugin the tools are `mcp__plugin_discord_discord-stdio__<tool>`; when the server was added standalone they are `mcp__discord-stdio__<tool>`. Use whichever exists.
