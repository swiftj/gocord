# Tool Reference

`discord-mcp` exposes **44 tools** (28 baseline-parity tools compatible with
`barryyip0625/mcp-discord`, 9 differentiating tools, and 7 media, LegendKeeper, and
diagnostic tools), 3 resources, and 3 prompts.

Conventions:

- **Kind:** R = read-only · W = write · D = destructive · DR = supports `dry_run`.
- Any `*_id` for a guild, channel, user, or role also accepts `#name`, `@name`, a mention (`<#id>`, `<@id>`), or a bare name. `guild_id` may be omitted when `DISCORD_MCP_DEFAULT_GUILD_ID` is set or the bot is in exactly one guild.
- Timestamps accept a Snowflake or ISO-8601.
- Responses are compact JSON (shown pretty-printed here). Lists include `count` and, when more results exist, `next_cursor`.
- Errors are returned as tool errors with actionable messages (not found, ambiguous, policy denied, validation).
- Examples use synthetic Snowflakes (`1000000000000000xx`) and `YOUR_BOT_TOKEN`.

## Index

| # | Tool | Kind | Description |
| :-- | :--- | :-- | :--- |
| 1 | [`discord_login`](#discord_login) | W | Authenticate or switch the active bot profile. The token is never echoed. |
| 2 | [`discord_list_servers`](#discord_list_servers) | R | List guilds the bot belongs to with member counts and owners (filtered by allow/block lists). |
| 3 | [`discord_get_server_info`](#discord_get_server_info) | R | Guild metadata, channels, roles, boost tier, and features. |
| 4 | [`discord_send`](#discord_send) | W | Send text, an embed, or a local file to a channel or thread. Content ≤ 2000 chars. |
| 5 | [`discord_read_messages`](#discord_read_messages) | R | Read message history with mentions resolved. At most one of before/after/around. |
| 6 | [`discord_edit_message`](#discord_edit_message) | W | Edit a bot-authored message. |
| 7 | [`discord_delete_message`](#discord_delete_message) | D, DR | Delete a message. |
| 8 | [`discord_bulk_delete_messages`](#discord_bulk_delete_messages) | D, DR | Bulk delete 2–100 messages. All must be younger than 14 days. |
| 9 | [`discord_add_reaction`](#discord_add_reaction) | W | Add an emoji reaction (unicode, `:name:`, or `name:id`). |
| 10 | [`discord_add_multiple_reactions`](#discord_add_multiple_reactions) | W | Add up to 20 reactions in order; reports per-emoji failures. |
| 11 | [`discord_remove_reaction`](#discord_remove_reaction) | W | Remove a reaction. Omit user_id to remove the bot's own. |
| 12 | [`discord_create_text_channel`](#discord_create_text_channel) | W | Create a text or announcement channel. |
| 13 | [`discord_create_voice_channel`](#discord_create_voice_channel) | W | Create a voice or stage channel. |
| 14 | [`discord_create_category`](#discord_create_category) | W | Create a channel category. |
| 15 | [`discord_edit_channel`](#discord_edit_channel) | D, DR | Edit name, topic, category, or archive state; only provided fields change. |
| 16 | [`discord_delete_channel`](#discord_delete_channel) | D, DR | Delete a channel. The preview warns about categories with children. |
| 17 | [`discord_set_channel_permissions`](#discord_set_channel_permissions) | D, DR | Set or remove a role/member permission overwrite. Permissions are names or a decimal bitfield. |
| 18 | [`discord_create_forum_channel`](#discord_create_forum_channel) | W | Create a forum channel with tags and an optional default reaction. |
| 19 | [`discord_create_forum_post`](#discord_create_forum_post) | W | Start a forum post. Unknown tag names return the list of available tags. |
| 20 | [`discord_get_forum_post`](#discord_get_forum_post) | R | Fetch a forum post's starter message and replies. |
| 21 | [`discord_reply_to_forum`](#discord_reply_to_forum) | W | Reply inside a forum post. |
| 22 | [`discord_list_forum_threads`](#discord_list_forum_threads) | R | List active (and optionally archived) posts in a forum. |
| 23 | [`discord_list_members`](#discord_list_members) | R | List members with roles and join dates. Requires the SERVER MEMBERS intent. |
| 24 | [`discord_manage_roles`](#discord_manage_roles) | R/D, DR | List, create, edit, delete, assign, or revoke roles. Roles at or above the bot's highest role are refused. |
| 25 | [`discord_create_webhook`](#discord_create_webhook) | W | Create a channel webhook. The token is never returned. |
| 26 | [`discord_send_webhook_message`](#discord_send_webhook_message) | W | Send a message through a webhook (by ID or URL). |
| 27 | [`discord_edit_webhook`](#discord_edit_webhook) | D, DR | Rename a webhook or move it to another channel. |
| 28 | [`discord_delete_webhook`](#discord_delete_webhook) | D, DR | Delete a webhook (also removes a persona webhook mapping). |
| 29 | [`discord_search_messages`](#discord_search_messages) | R | FTS5 full-text search over indexed messages; backfills unindexed channels via REST first. |
| 30 | [`discord_await_interaction`](#discord_await_interaction) | W | Post buttons, a select menu, or a modal and block until an authorised user responds or the timeout expires. |
| 31 | [`discord_persona_register`](#discord_persona_register) | W | Register or update a reusable persona (stored locally; no Discord call). |
| 32 | [`discord_persona_send`](#discord_persona_send) | W | Send as a registered persona; reuses or creates a webhook on the target (parent) channel. |
| 33 | [`discord_create_poll`](#discord_create_poll) | W | Create a native Discord poll. |
| 34 | [`discord_get_poll_results`](#discord_get_poll_results) | R | Poll tallies, total votes, winners (ties → multiple), and finalized state. |
| 35 | [`discord_manage_threads`](#discord_manage_threads) | W/D, DR | Create threads (standalone or from a message), or archive/unarchive/lock/unlock them. |
| 36 | [`discord_manage_events`](#discord_manage_events) | R/W | List or create guild scheduled events. Start time must be in the future. |
| 37 | [`discord_governance_audit`](#discord_governance_audit) | R/D, DR | Query audit logs, list AutoMod rules, or time out a member. |
| 38 | [`discord_get_member_avatar`](#discord_get_member_avatar) | R | A member's account and server avatar URLs and display name. |
| 39 | [`discord_fetch_media`](#discord_fetch_media) | R | An attachment, author avatar, or allowlisted URL as inline base64, resized and chunked. |
| 40 | [`discord_media_to_legendkeeper`](#discord_media_to_legendkeeper) | W | Copy an attachment or author avatar into a LegendKeeper asset library. |
| 41 | [`fetch_url_to_legendkeeper`](#fetch_url_to_legendkeeper) | W | Copy an allowlisted URL into a LegendKeeper asset library. |
| 42 | [`discord_batch_media_to_legendkeeper`](#discord_batch_media_to_legendkeeper) | W | Up to 25 bridge uploads in one call. |
| 43 | [`discord_list_tuppers`](#discord_list_tuppers) | R | Webhook (Tupperbox/PluralKit) identities with their latest avatar. |
| 44 | [`discord_check_access`](#discord_check_access) | R | Effective View/Read History per channel, with a fix for each blocked one. |

## Baseline parity tools (1–28)

### discord_login

**#1 · Kind:** W  
Authenticate or switch the active bot profile. The token is never echoed.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `token` | string | yes | Bot token. May be omitted when `profile` names a configured profile (its `token_env` is used). |
| `profile` | string | no | Profile name to activate. |

Example request (`tools/call` arguments):

```json
{"name": "discord_login", "arguments": {"token": "YOUR_BOT_TOKEN", "profile": "default"}}
```

Example response (text content):

```json
{
  "profile": "default",
  "bot_user_id": "100000000000000099",
  "bot_username": "discord-mcp",
  "guild_count": 2
}
```

### discord_list_servers

**#2 · Kind:** R  
List guilds the bot belongs to with member counts and owners (filtered by allow/block lists).

No parameters.

Example request (`tools/call` arguments):

```json
{"name": "discord_list_servers", "arguments": {}}
```

Example response (text content):

```json
{
  "count": 1,
  "guilds": [
    {
      "id": "100000000000000001",
      "name": "Test Guild",
      "member_count": 1234,
      "owner_id": "100000000000000050"
    }
  ]
}
```

### discord_get_server_info

**#3 · Kind:** R  
Guild metadata, channels, roles, boost tier, and features.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `guild_id` | string | yes | Guild Snowflake or name. |

Example request (`tools/call` arguments):

```json
{"name": "discord_get_server_info", "arguments": {"guild_id": "100000000000000001"}}
```

Example response (text content):

```json
{
  "id": "100000000000000001",
  "name": "Test Guild",
  "member_count": 1234,
  "owner_id": "100000000000000050",
  "boost_tier": 2,
  "features": [
    "COMMUNITY"
  ],
  "channels": [
    {
      "id": "100000000000000010",
      "guild_id": "100000000000000001",
      "name": "general",
      "type": "text",
      "parent_id": "100000000000000020"
    }
  ],
  "roles": [
    {
      "id": "100000000000000040",
      "name": "Moderator",
      "color": "#3498DB",
      "position": 5
    }
  ]
}
```

### discord_send

**#4 · Kind:** W  
Send text, an embed, or a local file to a channel or thread. Content ≤ 2000 chars.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `channel_id` | string | yes | Channel or thread; accepts `#name`. |
| `content` | string | yes | Message text (≤ 2000 chars). |
| `embed` | object | no | `{title, description, url, color, fields[{name,value,inline}], footer, image_url}`. |
| `file_path` | string | no | Absolute path to a regular file ≤ 25 MiB. |
| `reply_to_message_id` | string | no | Message to reply to. |

Example request (`tools/call` arguments):

```json
{"name": "discord_send", "arguments": {"channel_id": "#general", "content": "Deploy finished ✅"}}
```

Example response (text content):

```json
{
  "id": "100000000000000080",
  "channel_id": "100000000000000010",
  "author_id": "100000000000000099",
  "author": "discord-mcp",
  "content": "Deploy finished ✅",
  "timestamp": "2026-11-01T14:55:00Z"
}
```

### discord_read_messages

**#5 · Kind:** R  
Read message history with mentions resolved. At most one of before/after/around.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `channel_id` | string | yes | Channel or thread. |
| `limit` | number | no | 1–100, default 50. |
| `before` | string | no | Snowflake or ISO-8601. |
| `after` | string | no | Snowflake or ISO-8601. |
| `around` | string | no | Snowflake or ISO-8601. |

Example request (`tools/call` arguments):

```json
{"name": "discord_read_messages", "arguments": {"channel_id": "100000000000000010", "limit": 2, "after": "2026-11-01T00:00:00Z"}}
```

Example response (text content):

```json
{
  "count": 2,
  "messages": [
    {
      "id": "100000000000000080",
      "channel_id": "100000000000000010",
      "author_id": "100000000000000050",
      "author": "alice",
      "content": "Shipping v1.2 tomorrow",
      "timestamp": "2026-11-01T14:55:00Z"
    },
    {
      "id": "100000000000000081",
      "channel_id": "100000000000000010",
      "author_id": "100000000000000051",
      "author": "bob",
      "content": "LGTM",
      "timestamp": "2026-11-01T14:57:00Z",
      "reply_to_id": "100000000000000080"
    }
  ]
}
```

### discord_edit_message

**#6 · Kind:** W  
Edit a bot-authored message.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `channel_id` | string | yes |  |
| `message_id` | string | yes | Must be authored by the bot. |
| `content` | string | yes | New content (≤ 2000 chars). |

Example request (`tools/call` arguments):

```json
{"name": "discord_edit_message", "arguments": {"channel_id": "100000000000000010", "message_id": "100000000000000080", "content": "Deploy finished ✅ (v1.2.0)"}}
```

Example response (text content):

```json
{
  "id": "100000000000000080",
  "channel_id": "100000000000000010",
  "content": "Deploy finished ✅ (v1.2.0)",
  "edited_at": "2026-11-01T15:00:00Z"
}
```

### discord_delete_message

**#7 · Kind:** D, DR  
Delete a message.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `channel_id` | string | yes |  |
| `message_id` | string | yes |  |
| `reason` | string | no | Audit-log reason. |
| `dry_run` | boolean | no | Return a `DryRunPreview` instead of acting (default `false`). |

Example request (`tools/call` arguments):

```json
{"name": "discord_delete_message", "arguments": {"channel_id": "100000000000000010", "message_id": "100000000000000080", "dry_run": true}}
```

Example response (text content):

```json
{
  "dry_run": true,
  "action": "discord_delete_message",
  "target": "#general/100000000000000080",
  "changes": [
    "delete message by @alice: \"Shipping v1.2 tomorrow\""
  ],
  "allowed": true
}
```

### discord_bulk_delete_messages

**#8 · Kind:** D, DR  
Bulk delete 2–100 messages. All must be younger than 14 days.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `channel_id` | string | yes |  |
| `message_ids` | array | yes | 2–100 Snowflakes. |
| `reason` | string | no |  |
| `dry_run` | boolean | no | Return a `DryRunPreview` instead of acting (default `false`). |

Example request (`tools/call` arguments):

```json
{"name": "discord_bulk_delete_messages", "arguments": {"channel_id": "100000000000000010", "message_ids": ["100000000000000080", "100000000000000081"], "reason": "spam"}}
```

Example response (text content):

```json
{
  "deleted": 2,
  "channel_id": "100000000000000010"
}
```

### discord_add_reaction

**#9 · Kind:** W  
Add an emoji reaction (unicode, `:name:`, or `name:id`).

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `channel_id` | string | yes |  |
| `message_id` | string | yes |  |
| `emoji` | string | yes |  |

Example request (`tools/call` arguments):

```json
{"name": "discord_add_reaction", "arguments": {"channel_id": "100000000000000010", "message_id": "100000000000000080", "emoji": "👍"}}
```

Example response (text content):

```json
{
  "ok": true
}
```

### discord_add_multiple_reactions

**#10 · Kind:** W  
Add up to 20 reactions in order; reports per-emoji failures.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `channel_id` | string | yes |  |
| `message_id` | string | yes |  |
| `emojis` | array | yes | ≤ 20 emoji. |

Example request (`tools/call` arguments):

```json
{"name": "discord_add_multiple_reactions", "arguments": {"channel_id": "100000000000000010", "message_id": "100000000000000080", "emojis": ["1️⃣", "2️⃣", "3️⃣"]}}
```

Example response (text content):

```json
{
  "added": 3,
  "failed": []
}
```

### discord_remove_reaction

**#11 · Kind:** W  
Remove a reaction. Omit user_id to remove the bot's own.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `channel_id` | string | yes |  |
| `message_id` | string | yes |  |
| `emoji` | string | yes |  |
| `user_id` | string | no | User whose reaction to remove. |

Example request (`tools/call` arguments):

```json
{"name": "discord_remove_reaction", "arguments": {"channel_id": "100000000000000010", "message_id": "100000000000000080", "emoji": "👍"}}
```

Example response (text content):

```json
{
  "ok": true
}
```

### discord_create_text_channel

**#12 · Kind:** W  
Create a text or announcement channel.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `guild_id` | string | yes |  |
| `name` | string | yes |  |
| `category_id` | string | no | Parent category. |
| `topic` | string | no |  |
| `announcement` | boolean | no | Create an announcement channel. |

Example request (`tools/call` arguments):

```json
{"name": "discord_create_text_channel", "arguments": {"guild_id": "100000000000000001", "name": "release-notes", "category_id": "100000000000000020", "topic": "Changelogs"}}
```

Example response (text content):

```json
{
  "id": "100000000000000011",
  "guild_id": "100000000000000001",
  "name": "release-notes",
  "type": "text",
  "parent_id": "100000000000000020",
  "topic": "Changelogs"
}
```

### discord_create_voice_channel

**#13 · Kind:** W  
Create a voice or stage channel.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `guild_id` | string | yes |  |
| `name` | string | yes |  |
| `category_id` | string | no |  |
| `bitrate` | number | no | bps. |
| `user_limit` | number | no | 0 = unlimited. |
| `stage` | boolean | no | Create a stage channel. |

Example request (`tools/call` arguments):

```json
{"name": "discord_create_voice_channel", "arguments": {"guild_id": "100000000000000001", "name": "Lounge", "user_limit": 10}}
```

Example response (text content):

```json
{
  "id": "100000000000000070",
  "guild_id": "100000000000000001",
  "name": "Lounge",
  "type": "voice"
}
```

### discord_create_category

**#14 · Kind:** W  
Create a channel category.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `guild_id` | string | yes |  |
| `name` | string | yes |  |
| `position` | number | no | Sort position. |

Example request (`tools/call` arguments):

```json
{"name": "discord_create_category", "arguments": {"guild_id": "100000000000000001", "name": "Community"}}
```

Example response (text content):

```json
{
  "id": "100000000000000020",
  "guild_id": "100000000000000001",
  "name": "Community",
  "type": "category"
}
```

### discord_edit_channel

**#15 · Kind:** D, DR  
Edit name, topic, category, or archive state; only provided fields change.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `channel_id` | string | yes |  |
| `name` | string | no |  |
| `topic` | string | no |  |
| `category_id` | string | no |  |
| `archived` | boolean | no | Threads only. |
| `dry_run` | boolean | no | Return a `DryRunPreview` instead of acting (default `false`). |

Example request (`tools/call` arguments):

```json
{"name": "discord_edit_channel", "arguments": {"channel_id": "100000000000000010", "topic": "General chat", "dry_run": true}}
```

Example response (text content):

```json
{
  "dry_run": true,
  "action": "discord_edit_channel",
  "target": "#general",
  "changes": [
    "topic: \"Old\" → \"General chat\""
  ],
  "allowed": true
}
```

### discord_delete_channel

**#16 · Kind:** D, DR  
Delete a channel. The preview warns about categories with children.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `channel_id` | string | yes |  |
| `reason` | string | no |  |
| `dry_run` | boolean | no | Return a `DryRunPreview` instead of acting (default `false`). |

Example request (`tools/call` arguments):

```json
{"name": "discord_delete_channel", "arguments": {"channel_id": "100000000000000020", "dry_run": true}}
```

Example response (text content):

```json
{
  "dry_run": true,
  "action": "discord_delete_channel",
  "target": "📁 Community",
  "changes": [
    "delete category Community"
  ],
  "warnings": [
    "category has 2 child channels"
  ],
  "allowed": true
}
```

### discord_set_channel_permissions

**#17 · Kind:** D, DR  
Set or remove a role/member permission overwrite. Permissions are names or a decimal bitfield.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `channel_id` | string | yes |  |
| `target_id` | string | yes | Role or member. |
| `target_type` | string | yes | `role` or `member`. |
| `allow_permissions` | array or string | no | e.g. `["ViewChannel","SendMessages"]`. |
| `deny_permissions` | array or string | no |  |
| `remove` | boolean | no | Delete the overwrite. |
| `reason` | string | no |  |
| `dry_run` | boolean | no | Return a `DryRunPreview` instead of acting (default `false`). |

Example request (`tools/call` arguments):

```json
{"name": "discord_set_channel_permissions", "arguments": {"channel_id": "100000000000000011", "target_id": "100000000000000001", "target_type": "role", "deny_permissions": ["SendMessages"], "dry_run": true}}
```

Example response (text content):

```json
{
  "dry_run": true,
  "action": "discord_set_channel_permissions",
  "target": "#release-notes",
  "changes": [
    "#release-notes: role @everyone deny +SendMessages"
  ],
  "allowed": true
}
```

### discord_create_forum_channel

**#18 · Kind:** W  
Create a forum channel with tags and an optional default reaction.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `guild_id` | string | yes |  |
| `name` | string | yes |  |
| `topic` | string | no | Guidelines. |
| `tags` | array | no | ≤ 20 tag names. |
| `category_id` | string | no |  |
| `default_reaction` | string | no | Emoji. |

Example request (`tools/call` arguments):

```json
{"name": "discord_create_forum_channel", "arguments": {"guild_id": "100000000000000001", "name": "help-forum", "tags": ["bug", "question"]}}
```

Example response (text content):

```json
{
  "id": "100000000000000030",
  "guild_id": "100000000000000001",
  "name": "help-forum",
  "type": "forum"
}
```

### discord_create_forum_post

**#19 · Kind:** W  
Start a forum post. Unknown tag names return the list of available tags.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `forum_channel_id` | string | yes |  |
| `title` | string | yes |  |
| `content` | string | yes |  |
| `tag_names` | array | no |  |

Example request (`tools/call` arguments):

```json
{"name": "discord_create_forum_post", "arguments": {"forum_channel_id": "100000000000000030", "title": "Login fails on v1.2", "content": "Steps to reproduce…", "tag_names": ["bug"]}}
```

Example response (text content):

```json
{
  "id": "100000000000000060",
  "parent_id": "100000000000000030",
  "name": "Login fails on v1.2",
  "owner_id": "100000000000000099",
  "applied_tags": [
    "bug"
  ],
  "created_at": "2026-11-01T15:10:00Z"
}
```

### discord_get_forum_post

**#20 · Kind:** R  
Fetch a forum post's starter message and replies.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `thread_id` | string | yes |  |
| `limit` | number | no | 1–100, default 50. |

Example request (`tools/call` arguments):

```json
{"name": "discord_get_forum_post", "arguments": {"thread_id": "100000000000000060", "limit": 10}}
```

Example response (text content):

```json
{
  "thread": {
    "id": "100000000000000060",
    "parent_id": "100000000000000030",
    "name": "Login fails on v1.2",
    "message_count": 1
  },
  "starter": {
    "id": "100000000000000080",
    "author": "alice",
    "content": "Steps to reproduce…"
  },
  "replies": [
    {
      "id": "100000000000000081",
      "author": "bob",
      "content": "Fixed in v1.2.1"
    }
  ]
}
```

### discord_reply_to_forum

**#21 · Kind:** W  
Reply inside a forum post.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `thread_id` | string | yes |  |
| `content` | string | yes |  |

Example request (`tools/call` arguments):

```json
{"name": "discord_reply_to_forum", "arguments": {"thread_id": "100000000000000060", "content": "Thanks, confirmed."}}
```

Example response (text content):

```json
{
  "id": "100000000000000081",
  "channel_id": "100000000000000060",
  "author": "discord-mcp",
  "content": "Thanks, confirmed."
}
```

### discord_list_forum_threads

**#22 · Kind:** R  
List active (and optionally archived) posts in a forum.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `forum_channel_id` | string | yes |  |
| `include_archived` | boolean | no | Default `false`. |

Example request (`tools/call` arguments):

```json
{"name": "discord_list_forum_threads", "arguments": {"forum_channel_id": "100000000000000030", "include_archived": true}}
```

Example response (text content):

```json
{
  "count": 1,
  "threads": [
    {
      "id": "100000000000000060",
      "parent_id": "100000000000000030",
      "name": "Login fails on v1.2",
      "archived": false,
      "applied_tags": [
        "bug"
      ]
    }
  ]
}
```

### discord_list_members

**#23 · Kind:** R  
List members with roles and join dates. Requires the SERVER MEMBERS intent.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `guild_id` | string | yes |  |
| `limit` | number | no | 1–1000, default 100. |
| `after` | string | no | Pagination cursor (user ID). |

Example request (`tools/call` arguments):

```json
{"name": "discord_list_members", "arguments": {"guild_id": "100000000000000001", "limit": 1}}
```

Example response (text content):

```json
{
  "count": 1,
  "members": [
    {
      "user_id": "100000000000000050",
      "username": "alice",
      "roles": [
        "Moderator"
      ],
      "joined_at": "2025-03-01T10:00:00Z"
    }
  ],
  "next_cursor": "100000000000000050"
}
```

### discord_manage_roles

**#24 · Kind:** R/D, DR  
List, create, edit, delete, assign, or revoke roles. Roles at or above the bot's highest role are refused.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `action` | string | yes | `list`, `create`, `edit`, `delete`, `assign`, `revoke`. |
| `guild_id` | string | yes |  |
| `role_id` | string | no | edit/delete/assign/revoke. |
| `user_id` | string | no | assign/revoke. |
| `name` | string | no |  |
| `color` | string or number | no | Hex `#RRGGBB` or int. |
| `permissions` | array or string | no | Names or bitfield. |
| `hoist` | boolean | no |  |
| `mentionable` | boolean | no |  |
| `reason` | string | no |  |
| `dry_run` | boolean | no | Return a `DryRunPreview` instead of acting (default `false`). |

Example request (`tools/call` arguments):

```json
{"name": "discord_manage_roles", "arguments": {"action": "create", "guild_id": "100000000000000001", "name": "Helper", "color": "#2ECC71", "permissions": ["ManageThreads"]}}
```

Example response (text content):

```json
{
  "id": "100000000000000041",
  "name": "Helper",
  "color": "#2ECC71",
  "position": 2,
  "permissions": "17179869184",
  "permission_names": [
    "ManageThreads"
  ]
}
```

### discord_create_webhook

**#25 · Kind:** W  
Create a channel webhook. The token is never returned.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `channel_id` | string | yes |  |
| `name` | string | yes |  |

Example request (`tools/call` arguments):

```json
{"name": "discord_create_webhook", "arguments": {"channel_id": "100000000000000010", "name": "CI"}}
```

Example response (text content):

```json
{
  "id": "100000000000000090",
  "channel_id": "100000000000000010",
  "guild_id": "100000000000000001",
  "name": "CI"
}
```

### discord_send_webhook_message

**#26 · Kind:** W  
Send a message through a webhook (by ID or URL).

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `webhook_id` | string | no | One of `webhook_id` / `webhook_url` is required. |
| `webhook_url` | string | no | `https://discord.com/api/webhooks/{id}/{token}`. |
| `content` | string | yes |  |
| `username` | string | no | Override display name. |
| `avatar_url` | string | no |  |
| `thread_id` | string | no |  |
| `embed` | object | no |  |

Example request (`tools/call` arguments):

```json
{"name": "discord_send_webhook_message", "arguments": {"webhook_id": "100000000000000090", "content": "Build #42 passed", "username": "CI"}}
```

Example response (text content):

```json
{
  "id": "100000000000000080",
  "channel_id": "100000000000000010",
  "author": "CI",
  "content": "Build #42 passed"
}
```

### discord_edit_webhook

**#27 · Kind:** D, DR  
Rename a webhook or move it to another channel.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `webhook_id` | string | yes |  |
| `name` | string | no |  |
| `channel_id` | string | no |  |
| `dry_run` | boolean | no | Return a `DryRunPreview` instead of acting (default `false`). |

Example request (`tools/call` arguments):

```json
{"name": "discord_edit_webhook", "arguments": {"webhook_id": "100000000000000090", "name": "CI Bot"}}
```

Example response (text content):

```json
{
  "id": "100000000000000090",
  "channel_id": "100000000000000010",
  "name": "CI Bot"
}
```

### discord_delete_webhook

**#28 · Kind:** D, DR  
Delete a webhook (also removes a persona webhook mapping).

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `webhook_id` | string | yes |  |
| `reason` | string | no |  |
| `dry_run` | boolean | no | Return a `DryRunPreview` instead of acting (default `false`). |

Example request (`tools/call` arguments):

```json
{"name": "discord_delete_webhook", "arguments": {"webhook_id": "100000000000000090", "dry_run": true}}
```

Example response (text content):

```json
{
  "dry_run": true,
  "action": "discord_delete_webhook",
  "target": "webhook CI (100000000000000090)",
  "changes": [
    "delete webhook CI in #general"
  ],
  "allowed": true
}
```

## Differentiating tools (29–37)

### discord_search_messages

**#29 · Kind:** R  
FTS5 full-text search over indexed messages; backfills unindexed channels via REST first.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `guild_id` | string | yes |  |
| `query` | string | yes | FTS5: `"phrase"`, `AND`/`OR`/`NOT`, `prefix*`. |
| `channel_id` | string | no |  |
| `author_id` | string | no |  |
| `before` | string | no | Snowflake or ISO-8601. |
| `after` | string | no | Snowflake or ISO-8601. |
| `has_attachments` | boolean | no |  |
| `limit` | number | no | 1–100, default 25. |
| `auto_backfill_limit` | number | no | 0–5000, default 500. |

Example request (`tools/call` arguments):

```json
{"name": "discord_search_messages", "arguments": {"guild_id": "100000000000000001", "query": "\"release notes\" OR changelog", "channel_id": "#general", "auto_backfill_limit": 500}}
```

Example response (text content):

```json
{
  "count": 1,
  "hits": [
    {
      "id": "100000000000000080",
      "channel_id": "100000000000000010",
      "author": "alice",
      "content": "Release notes are up",
      "timestamp": "2026-11-01T14:55:00Z",
      "snippet": "[Release notes] are up",
      "rank": -4.2
    }
  ],
  "indexed_channels": 1,
  "backfilled": 312
}
```

### discord_await_interaction

**#30 · Kind:** W  
Post buttons, a select menu, or a modal and block until an authorised user responds or the timeout expires.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `channel_id` | string | yes |  |
| `prompt_text` | string | yes | The decision card. |
| `mode` | string | no | `buttons` (default), `select`, `modal`. |
| `options` | array | no | `[{label, value, style, emoji}]`; default Approve/Reject. |
| `allowed_user_ids` | array | no |  |
| `allowed_role_ids` | array | no |  |
| `timeout_seconds` | number | no | 10–900, default 300. |
| `min_values` | number | no | select only. |
| `max_values` | number | no | select only. |

Example request (`tools/call` arguments):

```json
{"name": "discord_await_interaction", "arguments": {"channel_id": "100000000000000010", "prompt_text": "**Deploy v1.2 to prod?**\nRisk: low · Rollback: redeploy v1.1", "allowed_role_ids": ["100000000000000040"], "timeout_seconds": 300}}
```

Example response (text content):

```json
{
  "message_id": "100000000000000080",
  "selected": [
    "approve"
  ],
  "user_id": "100000000000000050",
  "username": "alice",
  "timed_out": false,
  "responded_at": "2026-11-01T15:02:11Z"
}
```

### discord_persona_register

**#31 · Kind:** W  
Register or update a reusable persona (stored locally; no Discord call).

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `persona_name` | string | yes | `[a-z0-9-]{1,32}`. |
| `display_name` | string | yes | Clearly a bot/agent name. |
| `avatar_url` | string | no | https only. |
| `description` | string | no |  |
| `default_channel_id` | string | no |  |

Example request (`tools/call` arguments):

```json
{"name": "discord_persona_register", "arguments": {"persona_name": "release-bot", "display_name": "Release Bot", "avatar_url": "https://example.com/release-bot.png", "default_channel_id": "100000000000000011"}}
```

Example response (text content):

```json
{
  "name": "release-bot",
  "display_name": "Release Bot",
  "avatar_url": "https://example.com/release-bot.png",
  "default_channel_id": "100000000000000011",
  "created_at": "2026-11-01T15:00:00Z"
}
```

### discord_persona_send

**#32 · Kind:** W  
Send as a registered persona; reuses or creates a webhook on the target (parent) channel.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `persona_name` | string | yes |  |
| `content` | string | yes |  |
| `channel_id` | string | no | Defaults to the persona's default channel. |
| `thread_id` | string | no |  |
| `embed` | object | no |  |

Example request (`tools/call` arguments):

```json
{"name": "discord_persona_send", "arguments": {"persona_name": "release-bot", "content": "v1.2.0 is live 🚀", "thread_id": "100000000000000060"}}
```

Example response (text content):

```json
{
  "id": "100000000000000080",
  "channel_id": "100000000000000060",
  "author": "Release Bot",
  "content": "v1.2.0 is live 🚀"
}
```

### discord_create_poll

**#33 · Kind:** W  
Create a native Discord poll.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `channel_id` | string | yes |  |
| `question` | string | yes | ≤ 300 chars. |
| `answers` | array | yes | 2–10 answers, each ≤ 55 chars. |
| `duration_hours` | number | no | 1–768, default 24. |
| `allow_multiselect` | boolean | no | Default `false`. |

Example request (`tools/call` arguments):

```json
{"name": "discord_create_poll", "arguments": {"channel_id": "100000000000000010", "question": "Release day?", "answers": ["Tuesday", "Thursday"], "duration_hours": 24}}
```

Example response (text content):

```json
{
  "id": "100000000000000080",
  "channel_id": "100000000000000010",
  "author": "discord-mcp",
  "timestamp": "2026-11-01T15:00:00Z"
}
```

### discord_get_poll_results

**#34 · Kind:** R  
Poll tallies, total votes, winners (ties → multiple), and finalized state.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `channel_id` | string | yes |  |
| `message_id` | string | yes |  |

Example request (`tools/call` arguments):

```json
{"name": "discord_get_poll_results", "arguments": {"channel_id": "100000000000000010", "message_id": "100000000000000080"}}
```

Example response (text content):

```json
{
  "message_id": "100000000000000080",
  "question": "Release day?",
  "answers": [
    {
      "id": 1,
      "text": "Tuesday",
      "votes": 7
    },
    {
      "id": 2,
      "text": "Thursday",
      "votes": 3
    }
  ],
  "total_votes": 10,
  "winners": [
    "Tuesday"
  ],
  "finalized": false,
  "expires_at": "2026-11-02T15:00:00Z"
}
```

### discord_manage_threads

**#35 · Kind:** W/D, DR  
Create threads (standalone or from a message), or archive/unarchive/lock/unlock them.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `action` | string | yes | `create`, `archive`, `unarchive`, `lock`, `unlock`. |
| `channel_id` | string | no | Parent channel (create). |
| `thread_id` | string | no | Other actions. |
| `message_id` | string | no | Start the thread from this message. |
| `name` | string | no | create. |
| `private` | boolean | no | create. |
| `auto_archive_minutes` | number | no | 60, 1440, 4320, or 10080. |
| `dry_run` | boolean | no | Applies to `archive`/`lock`. |

Example request (`tools/call` arguments):

```json
{"name": "discord_manage_threads", "arguments": {"action": "create", "channel_id": "100000000000000010", "message_id": "100000000000000080", "name": "Release v1.2"}}
```

Example response (text content):

```json
{
  "id": "100000000000000060",
  "parent_id": "100000000000000010",
  "name": "Release v1.2",
  "owner_id": "100000000000000099",
  "created_at": "2026-11-01T15:05:00Z"
}
```

### discord_manage_events

**#36 · Kind:** R/W  
List or create guild scheduled events. Start time must be in the future.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `action` | string | yes | `list` or `create`. |
| `guild_id` | string | yes |  |
| `name` | string | no | create. |
| `description` | string | no |  |
| `start_time_iso` | string | no | create; ISO-8601. |
| `end_time_iso` | string | no | Required for `external`. |
| `location_or_channel_id` | string | no | Location text (`external`) or voice/stage channel. |
| `entity_type` | string | no | `stage`, `voice`, `external`. |

Example request (`tools/call` arguments):

```json
{"name": "discord_manage_events", "arguments": {"action": "create", "guild_id": "100000000000000001", "name": "Community call", "start_time_iso": "2026-11-05T18:00:00Z", "entity_type": "voice", "location_or_channel_id": "100000000000000070"}}
```

Example response (text content):

```json
{
  "id": "100000000000000095",
  "guild_id": "100000000000000001",
  "name": "Community call",
  "start_time": "2026-11-05T18:00:00Z",
  "entity_type": "voice",
  "channel_id": "100000000000000070",
  "status": "scheduled"
}
```

### discord_governance_audit

**#37 · Kind:** R/D, DR  
Query audit logs, list AutoMod rules, or time out a member.

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `action` | string | yes | `audit_logs`, `automod_rules`, `timeout_member`. |
| `guild_id` | string | yes |  |
| `user_id` | string | no | timeout_member; audit filter. |
| `action_type` | number | no | Audit-log action type filter. |
| `limit` | number | no | 1–100, default 50. |
| `duration_seconds` | number | no | 0–2419200; 0 clears the timeout. |
| `reason` | string | no |  |
| `dry_run` | boolean | no | Return a `DryRunPreview` instead of acting (default `false`). |

Example request (`tools/call` arguments):

```json
{"name": "discord_governance_audit", "arguments": {"action": "timeout_member", "guild_id": "100000000000000001", "user_id": "100000000000000050", "duration_seconds": 600, "reason": "spam", "dry_run": true}}
```

Example response (text content):

```json
{
  "dry_run": true,
  "action": "discord_governance_audit:timeout_member",
  "target": "@alice",
  "changes": [
    "timeout @alice for 10m (reason: spam)"
  ],
  "allowed": true
}
```

## Media, LegendKeeper, and diagnostic tools (38–44)

Contract and rules: [specs/05-media-and-legendkeeper.md](../specs/05-media-and-legendkeeper.md).

Message objects returned by every tool now also carry `is_webhook`, `webhook_id`,
`author_display_name`, and `author_avatar_url`. Attachments add `id`,
`proxy_url`, `width`, and `height`. Attachment and avatar URLs are signed and
expire (typically after about 24 hours). Re-resolve them from `channel_id` +
`message_id`; don't cache them.

**Image source** (fetch and bridge tools): `channel_id` + `message_id` plus
exactly one of `attachment_index` (0-based), `attachment_id`, or `avatar`; or
`url` on an allowlisted host.

### discord_get_member_avatar

**#38 · Kind:** R

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `guild_id` | string | no |  |
| `user_id` | string | yes |  |
| `size` | number | no | 128, 256, 512, 1024 (default), 2048, 4096. |

Returns `{user_id, display_name, avatar_url, guild_avatar_url?, animated}`.

### discord_fetch_media

**#39 · Kind:** R

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| image source | — | yes | See above. |
| `max_dimension` | number | no | Default 1600; 0 keeps the original size. Never upscales. |
| `format` | string | no | `jpeg` (default), `png`, `webp` (returned as PNG), `original` (untouched bytes). |
| `quality` | number | no | JPEG quality 1–100, default 85. |
| `offset`, `length` | number | no | Chunked retrieval of the encoded payload; `length` ≤ 1,000,000. |

Returns `{mime, filename, width, height, total_bytes, offset, length, eof, sha256, base64, hint?}`.
Payloads over 1,000,000 bytes return the first chunk with `eof: false` and a
`hint`. Concatenating every chunk reproduces `sha256`.

### discord_media_to_legendkeeper

**#40 · Kind:** W (writes to LegendKeeper; blocked by `--read-only`)

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `channel_id`, `message_id` | string | yes | Plus one of `attachment_index`, `attachment_id`, `avatar`. |
| `lk_project` | string | no | Default `LEGENDKEEPER_PROJECT_ID`. |
| `lk_folder_id` / `lk_folder_path` | string | no | One or the other; a path like `Campaign/Characters` is created if missing. |
| `name` | string | no | ≤ 120 characters; default is the filename stem. |
| `description` | string | no |  |
| `max_dimension`, `format`, `quality` | — | no | Default `0` / `original` (untouched bytes). |

Returns `{asset_id, url, name, width, height, content_type, bytes, folder_id, source_url, project, deduplicated?}`.

```json
{"name": "discord_media_to_legendkeeper", "arguments": {"channel_id": "100000000000000010", "message_id": "100000000000000150", "avatar": true, "lk_folder_path": "Druskenvald SPG/Characters", "name": "Ser Thaddeus Mournvale"}}
```

### fetch_url_to_legendkeeper

**#41 · Kind:** W. Same as #40, but the source is `url` (required, allowlisted host).

### discord_batch_media_to_legendkeeper

**#42 · Kind:** W

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `items` | array | yes | 1–25 objects with the #40 fields. |
| `lk_project`, `lk_folder_path` | string | no | Defaults applied to items that omit them. |
| `continue_on_error` | boolean | no | Default `true`. |

Items run sequentially, 250 ms apart. Returns `{results: [{index, ok, asset_id?, url?, deduplicated?, error?}], succeeded, failed}`.

### discord_list_tuppers

**#43 · Kind:** R

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `guild_id` | string | no |  |
| `channel_id` | string | no | Channel, forum, or category; includes threads. |
| `since` | string | no | Snowflake or ISO-8601. |
| `limit` | number | no | 1–500, default 100. |

Backfills like `discord_search_messages`, then returns
`{tuppers: [{display_name, avatar_url, webhook_id, last_seen_message_id, last_seen_channel_id, last_seen_at, message_count}], backfilled, warning?}`,
newest first. Avatar URLs come from each identity's latest message.

### discord_check_access

**#44 · Kind:** R

| Parameter | Type | Required | Notes |
| :--- | :--- | :-- | :--- |
| `guild_id` | string | no |  |
| `channel_ids` | string[] | no | Default: every text, announcement, and forum channel. |

Returns `{channels: [{id, name, category, can_view, can_read_history, missing, fix?}], summary: {readable, blocked}}`.
Each `fix` names the role and the category (when the channel is synced with it) or channel that needs an overwrite.

## Resources

### `discord://guilds`
MIME `application/json`. The `discord_list_servers` output; an entry point for clients that browse resources.

### `discord://guilds/{guild_id}/map`
MIME `text/markdown`. Roles, categories, channels, forum tags, and active threads with Snowflakes, in one compact document. Supports subscriptions (updated on channel, role, or thread changes).

```markdown
# Test Guild (100000000000000001) · 1,234 members · tier 2
## Roles
- @Moderator (100000000000000040) pos 5 · ManageMessages, KickMembers
## Channels
- 📁 Community (100000000000000020)
  - #general (100000000000000010) text · "Topic…"
    - 🧵 Release planning (100000000000000060) active
  - 🗂 #help-forum (100000000000000030) forum · tags: bug, question
- 🔊 Lounge (100000000000000070) voice
```

### `discord://channels/{channel_id}/transcript`
MIME `text/plain`. The last 100 messages in chronological order (`?limit=` up to 500, `?before=` to page back), times in UTC, with user, role, and channel mentions resolved. Supports subscriptions (updated on new messages).

```text
[2026-11-01 14:55] @alice: Shipping v1.2 tomorrow — @Moderator please review #release-notes
[2026-11-01 14:57] @bob ↪ reply to alice: LGTM 👍  [2 attachments]
```

## Prompts

| Prompt | Arguments (required in **bold**) | What it does |
| :--- | :--- | :--- |
| `discord-channel-digest` | **`channel_id`**, `since` (ISO-8601 or `24h`/`7d`, default `24h`), `audience` | Embeds the channel transcript for the window and asks for **Summary**, **Decisions**, **Open Questions**, **Action Items (owner → task)**, and **Notable Links**, citing timestamps. |
| `discord-server-blueprint` | **`guild_id`**, **`goal`** | Embeds the guild map and asks for a plan of categories, channels, roles, and overwrites, calling every mutating tool with `dry_run: true` first and presenting the combined preview before any change. |
| `discord-hitl-approval` | **`channel_id`**, **`decision`**, `context`, `options` (default `Approve,Reject`), `approver` (`@user` or `@role`), `timeout_seconds` | Formats a decision card (what, why, risk, rollback), calls `discord_await_interaction`, and proceeds only on an explicit approval. |

Example `prompts/get`:

```json
{"name": "discord-channel-digest", "arguments": {"channel_id": "100000000000000010", "since": "24h"}}
```
