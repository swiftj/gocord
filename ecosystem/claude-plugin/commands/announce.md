---
description: Draft and post a Discord announcement, optionally as a persona and with a discussion thread.
argument-hint: "<#channel> <message> [persona=name]"
allowed-tools: mcp__plugin_discord_discord-stdio__discord_list_servers, mcp__plugin_discord_discord-stdio__discord_send, mcp__plugin_discord_discord-stdio__discord_persona_send, mcp__plugin_discord_discord-stdio__discord_manage_threads, mcp__discord-stdio__discord_list_servers, mcp__discord-stdio__discord_send, mcp__discord-stdio__discord_persona_send, mcp__discord-stdio__discord_manage_threads, ReadMcpResourceTool
---

Post an announcement to Discord.

Arguments: `$ARGUMENTS`

Parse the arguments as `<#channel> <message…> [persona=<name>]`.

1. Resolve the channel (read `discord://guilds/{guild_id}/map` if the name is ambiguous).
2. Draft the announcement from the message: a clear first line, short paragraphs, and at most 2000 characters. Do not add `@everyone`/`@here` unless the user wrote it.
3. **Show the draft** and the target channel (and persona, if any) and ask the user to confirm or edit it.
4. After confirmation, send it:
   - With `persona=<name>`: `discord_persona_send` (`persona_name`, `channel_id`, `content`). If the persona is unknown, report the registered personas instead of sending.
   - Otherwise: `discord_send` (`channel_id`, `content`).
5. Offer to create a discussion thread from the announcement with `discord_manage_threads` (`action: create`, `channel_id`, `message_id` of the sent message, `name`).
6. Report the message ID (and thread ID, if created).

Tool names: when installed as the `discord` plugin the tools are `mcp__plugin_discord_discord-stdio__<tool>`; when the server was added standalone they are `mcp__discord-stdio__<tool>`. Use whichever exists.
