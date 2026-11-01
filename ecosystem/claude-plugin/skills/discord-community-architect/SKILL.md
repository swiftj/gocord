---
name: discord-community-architect
description: Use when designing, provisioning, or restructuring a Discord server — categories, channels, forums, roles, and permission overwrites. Always previews every change with dry_run before applying.
---

# Discord Community Architect

Provision or restructure a Discord server safely: read the live state, draft a
target structure, preview every mutation with `dry_run: true`, and apply only
after explicit approval.

## Tool names

Tools are referenced below by their MCP tool name (e.g. `discord_create_category`).
In Claude Code they appear with a server prefix:

- installed as this plugin: `mcp__plugin_discord_discord-stdio__<tool>`
- added standalone (`claude mcp add discord-stdio …`): `mcp__discord-stdio__<tool>`

Use whichever prefix is available in the session.

## Procedure

### 1. Read the map first — never guess IDs
Read the resource `discord://guilds/{guild_id}/map` (via `ReadMcpResourceTool`).
If the guild ID is unknown, call `discord_list_servers` first. The map lists
roles (with positions and permissions), categories, channels, forum tags, and
active threads with their Snowflakes. All later steps use IDs from the map.

### 2. Draft the target structure as a table
Present the plan before touching anything:

| Category | Channel (type) | Role overwrites |
| :--- | :--- | :--- |
| Community | #general (text) | @everyone: default |
| Community | #announcements (announcement) | @everyone deny SendMessages · @Staff allow SendMessages |
| Staff | #mod-log (text) | @everyone deny ViewChannel · @Moderator allow ViewChannel |
| Support | #help (forum, tags: bug, question) | — |

Also list new or changed roles: name, color, permissions, hoist, position intent.

### 3. Compute the diff and dry-run every mutation
Compare the table with the map and classify each item: **create**, **edit**,
**move**, **delete**, or **unchanged**. Then run every mutating call with
`dry_run: true` where the tool supports it:

- `discord_manage_roles` (`action: create|edit|delete|assign|revoke`, `dry_run: true`)
- `discord_edit_channel` (`dry_run: true`) — the preview shows old → new
- `discord_delete_channel` (`dry_run: true`) — warns about categories with children
- `discord_set_channel_permissions` (`dry_run: true`)

Create tools (`discord_create_category`, `discord_create_text_channel`,
`discord_create_voice_channel`, `discord_create_forum_channel`) do not take
`dry_run`; list them in the preview as "will create" instead of calling them.

Batch all previews into **one** summary for the user:

```
Planned changes (dry run) — Test Guild (100000000000000001)
Roles:      + @Moderator (ManageMessages, KickMembers)
Categories: + Staff
Channels:   + #mod-log in Staff · ~ #general topic "old" → "new"
Overwrites: #mod-log @everyone deny ViewChannel · @Moderator allow ViewChannel
Warnings:   (copy every warning from the DryRunPreview results)
```

If any preview returns `allowed: false`, stop and report `denied_reason`
(read-only mode, allowlist, etc.). Do not try to work around policy.

### 4. Apply only after approval
Apply only after the user explicitly confirms in chat. If the user is remote or
asked for mobile approval, post a decision card with `discord_await_interaction`
(see the `discord-hitl-persona-ops` skill) and treat `timed_out: true` as **not
approved**.

Apply in dependency order so referenced IDs exist:

1. **Roles** — `discord_manage_roles` `create`/`edit` (capture new role IDs)
2. **Categories** — `discord_create_category` (capture category IDs)
3. **Channels** — `discord_create_text_channel`, `discord_create_voice_channel`,
   `discord_create_forum_channel`, `discord_edit_channel` (with `category_id`)
4. **Overwrites** — `discord_set_channel_permissions` (role overwrites first,
   then member overwrites)
5. **Deletions last** — `discord_delete_channel`, `discord_manage_roles delete`,
   each re-confirmed individually

Stop at the first failure, report what was applied and what was not, and do not
retry destructive calls blindly.

### 5. Verify
Re-read `discord://guilds/{guild_id}/map` and report the final state as a
compact table with the new Snowflakes. Call out anything that differs from the
plan.

## Rules for private areas

To make a category or channel private:

1. `discord_set_channel_permissions` — `target_type: role`, `target_id` = the
   `@everyone` role (its ID equals the guild ID), `deny_permissions: ["ViewChannel"]`
2. `discord_set_channel_permissions` — `target_type: role`, `target_id` = the
   allowed role, `allow_permissions: ["ViewChannel", "SendMessages", "ReadMessageHistory"]`

Prefer setting overwrites on the **category** and letting channels inherit;
only add per-channel overwrites for exceptions. Never grant `Administrator` to
solve an access problem.

## Role hierarchy rules

- The bot can only manage roles **below** its own highest role. If a role edit
  is refused, tell the user to move the bot's role higher in Server Settings.
- Keep staff roles above member roles; keep cosmetic roles at the bottom.
- `hoist: true` only for roles that should appear separately in the member list.

## Permission-name cheat sheet

Permissions accept names (`["ViewChannel","SendMessages"]`) or a decimal
bitfield string.

| Name | Typical use |
| :--- | :--- |
| `ViewChannel` | See the channel (deny on @everyone for private areas) |
| `SendMessages` | Post in text channels |
| `SendMessagesInThreads` | Post inside threads and forum posts |
| `CreatePublicThreads` / `CreatePrivateThreads` | Start threads |
| `ReadMessageHistory` | Read past messages |
| `AddReactions` | React to messages |
| `AttachFiles` / `EmbedLinks` | Uploads and link previews |
| `MentionEveryone` | Ping @everyone / @here |
| `ManageMessages` | Delete or pin others' messages (moderators) |
| `ManageThreads` | Archive, lock, or delete threads |
| `ManageChannels` | Edit or delete channels |
| `ManageRoles` | Edit roles and overwrites |
| `ManageWebhooks` | Create webhooks (needed for personas) |
| `ModerateMembers` | Timeouts |
| `KickMembers` / `BanMembers` | Removal |
| `Connect` / `Speak` | Voice channels |
| `Administrator` | Avoid — bypasses all overwrites |

## Safety checklist

- [ ] Map read before planning; no invented IDs
- [ ] Every destructive call previewed with `dry_run: true`
- [ ] One combined preview shown to the user
- [ ] Explicit approval received (chat or HITL) before applying
- [ ] Applied in order roles → categories → channels → overwrites
- [ ] Final map re-read and reported
