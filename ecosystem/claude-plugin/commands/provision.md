---
description: Plan Discord server provisioning (roles, categories, channels, permissions) as dry-run previews only.
argument-hint: "<guild> <goal…>"
allowed-tools: mcp__plugin_discord_discord-stdio__discord_list_servers, mcp__plugin_discord_discord-stdio__discord_get_server_info, mcp__plugin_discord_discord-stdio__discord_manage_roles, mcp__plugin_discord_discord-stdio__discord_edit_channel, mcp__plugin_discord_discord-stdio__discord_delete_channel, mcp__plugin_discord_discord-stdio__discord_set_channel_permissions, mcp__plugin_discord_discord-stdio__discord_manage_threads, mcp__discord-stdio__discord_list_servers, mcp__discord-stdio__discord_get_server_info, mcp__discord-stdio__discord_manage_roles, mcp__discord-stdio__discord_edit_channel, mcp__discord-stdio__discord_delete_channel, mcp__discord-stdio__discord_set_channel_permissions, mcp__discord-stdio__discord_manage_threads, ReadMcpResourceTool
---

Plan a Discord server change from a goal, ending with dry-run previews only.

Arguments: `$ARGUMENTS`

Parse the arguments as `<guild> <goal…>` (guild name or ID, then free text such as "open-source project with a support forum and private staff area").

Follow the **discord-community-architect** skill:

1. Resolve the guild (`discord_list_servers` if needed) and read `discord://guilds/{guild_id}/map`.
2. Draft the target structure as a table: category → channels (type) → role overwrites, plus new or changed roles.
3. Compute the diff against the map.
4. Run every supported mutating call with `dry_run: true` (`discord_manage_roles`, `discord_edit_channel`, `discord_delete_channel`, `discord_set_channel_permissions`). List channel/category/forum creations as "will create" — do **not** call create tools here.
5. Present one combined preview with all changes and warnings.

**Stop there.** This command never applies changes. Applying requires a follow-up confirmation from the user, after which the changes are made in the order roles → categories → channels → overwrites.

Tool names: when installed as the `discord` plugin the tools are `mcp__plugin_discord_discord-stdio__<tool>`; when the server was added standalone they are `mcp__discord-stdio__<tool>`. Use whichever exists.
