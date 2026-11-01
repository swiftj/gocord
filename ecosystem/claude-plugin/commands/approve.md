---
description: Ask a human to approve a decision via Discord buttons and report the result.
argument-hint: "<#channel> <decision…> [@approver]"
allowed-tools: mcp__plugin_discord_discord-stdio__discord_list_servers, mcp__plugin_discord_discord-stdio__discord_list_members, mcp__plugin_discord_discord-stdio__discord_await_interaction, mcp__discord-stdio__discord_list_servers, mcp__discord-stdio__discord_list_members, mcp__discord-stdio__discord_await_interaction, ReadMcpResourceTool
---

Request a human decision through Discord and wait for the answer.

Arguments: `$ARGUMENTS`

Parse the arguments as `<#channel> <decision…> [@approver]`. The approver may be a user (`@alice`) or a role (`@Maintainers`).

Follow the **discord-hitl-persona-ops** skill:

1. Resolve the channel and approver (read `discord://guilds/{guild_id}/map` for roles; `discord_list_members` for users if needed).
2. Write a decision card with **What · Why · Risk · Rollback**, using context from this conversation.
3. Call `discord_await_interaction` with `channel_id`, `prompt_text` (the card), default Approve/Reject buttons, `allowed_user_ids` or `allowed_role_ids` for the approver, and `timeout_seconds: 300` unless the user asked otherwise. If no approver was given, ask the user who may approve before posting.
4. Report the result: who chose what and when. `timed_out: true` means **not approved**.
5. Proceed with the decided action only if the selected value is the approval value.

Tool names: when installed as the `discord` plugin the tools are `mcp__plugin_discord_discord-stdio__<tool>`; when the server was added standalone they are `mcp__discord-stdio__<tool>`. Use whichever exists.
