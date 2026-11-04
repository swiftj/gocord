# Spec 02 — MCP Surface (Tools, Resources, Prompts)

**Read when:** Editing `internal/mcp/**`. Contracts for `discord.Service` and DTOs live in `specs/01-architecture-and-contracts.md` §4–5.

`tools/list` MUST return exactly **44 tools**: 28 baseline parity tools (compatible with `barryyip0625/mcp-discord`), 9 differentiating tools, and the 7 media, local-file, and diagnostic tools (38–44) specified in `specs/05-media-and-local-files.md`.

## 1. Conventions for Every Tool
- **Naming:** `discord_<verb>_<noun>`, snake_case. Parameter names are snake_case and match the tables below exactly. They are part of the public contract.
- **Schemas:** Build with `mcp.NewTool(name, mcp.WithDescription(...), mcp.WithString/Number/Boolean/Array/Object(...))`. Mark required params with `mcp.Required()`. Use `mcp.Enum(...)` for `action`/`type` params. Add `mcp.Min/Max` where the table gives a range.
- **Annotations:** Set `ReadOnlyHint` on read tools, `DestructiveHint` on destructive tools, and `IdempotentHint` where true.
- **Descriptions:** One or two sentences: what it does, plus the single most important constraint (e.g. "Messages must be < 14 days old.").
- **Reference resolution:** Any `*_id` param for a guild, channel, user, or role also accepts `#name`, `@name`, a mention (`<#id>`, `<@id>`), or a bare name. Resolve through `Service.Resolve*ID` before calling Discord. `guild_id` is optional when `DISCORD_MCP_DEFAULT_GUILD_ID` is set or the bot is in exactly one guild.
- **Policy:** Every handler calls `Guard.Check` before any mutating call. Tools marked **DR** accept `dry_run` (boolean, default `false`) and return a `DryRunPreview` when it is true.
- **Output:** Return `mcp.NewToolResultText(json)` with the compact DTO (no indentation). Lists include a `"count"` field and a `"next_cursor"` (`after`) when more results exist.
- **Errors:** Return `mcp.NewToolResultError(msg)`, not a Go error, for user-correctable problems (not found, ambiguous, forbidden, policy denied, validation). Messages are actionable, e.g. `"ambiguous channel 'general': 100000000000000010 (#general in Test Guild), 100000000000000012 (#general in Other)"`. Reserve Go errors for internal failures.
- **Timestamps** accepted as input: Snowflake or ISO-8601. Convert with `(unixMillis - 1420070400000) << 22`.

## 2. Baseline Parity Tools (1–28)
Legend: **R** read-only · **W** write · **D** destructive · **DR** supports `dry_run`

| # | Tool | Kind | Parameters (required in **bold**) | Behaviour / Service call |
| :-- | :--- | :-- | :--- | :--- |
| 1 | `discord_login` | W | `token`, `profile` (at least one required) | `Login`. Switches the active profile. If `token` is omitted and `profile` names a configured profile, use its `token_env`. Never echo the token. Returns `SessionInfo`. |
| 2 | `discord_list_servers` | R | — | `ListGuilds`. Name, ID, member count, owner. |
| 3 | `discord_get_server_info` | R | **`guild_id`** | `GetGuildInfo`. Metadata, channels, roles, boost tier, features. |
| 4 | `discord_send` | W | **`channel_id`**, **`content`**, `embed` (object), `file_path`, `reply_to_message_id` | `SendMessage`. Works in threads. `file_path` must be absolute, a regular file, and ≤ 25 MiB. Content must be ≤ 2000 chars (validate before calling). |
| 5 | `discord_read_messages` | R | **`channel_id`**, `limit` (1–100, default 50), `before`, `after`, `around` | `ReadMessages`. At most one of `before`/`after`/`around`. Mentions resolved to names. Opportunistically upserts the results into the index. |
| 6 | `discord_edit_message` | W | **`channel_id`**, **`message_id`**, **`content`** | `EditMessage`. Bot-authored messages only (return a clear error otherwise). |
| 7 | `discord_delete_message` | D, DR | **`channel_id`**, **`message_id`**, `reason`, `dry_run` | `DeleteMessage`. |
| 8 | `discord_bulk_delete_messages` | D, DR | **`channel_id`**, **`message_ids`** (array, 2–100), `reason`, `dry_run` | `BulkDeleteMessages`. All messages must be < 14 days old (policy rule 5). |
| 9 | `discord_add_reaction` | W | **`channel_id`**, **`message_id`**, **`emoji`** | `AddReaction`. Accepts unicode, `:name:`, or `name:id` for custom emoji. |
| 10 | `discord_add_multiple_reactions` | W | **`channel_id`**, **`message_id`**, **`emojis`** (array, ≤ 20) | `AddMultipleReactions`. Applied in order. Reports per-emoji failures. |
| 11 | `discord_remove_reaction` | W | **`channel_id`**, **`message_id`**, **`emoji`**, `user_id` | `RemoveReaction`. Omit `user_id` to remove the bot's own reaction. |
| 12 | `discord_create_text_channel` | W | **`guild_id`**, **`name`**, `category_id`, `topic`, `announcement` (bool) | `CreateChannel` (type `text` or `announcement`). |
| 13 | `discord_create_voice_channel` | W | **`guild_id`**, **`name`**, `category_id`, `bitrate`, `user_limit`, `stage` (bool) | `CreateChannel` (type `voice` or `stage`). |
| 14 | `discord_create_category` | W | **`guild_id`**, **`name`**, `position` | `CreateChannel` (type `category`). |
| 15 | `discord_edit_channel` | D, DR | **`channel_id`**, `name`, `topic`, `category_id`, `archived`, `dry_run` | `EditChannel`. Only provided fields change. The preview shows old → new. |
| 16 | `discord_delete_channel` | D, DR | **`channel_id`**, `reason`, `dry_run` | `DeleteChannel`. The preview warns if it is a category with children. |
| 17 | `discord_set_channel_permissions` | D, DR | **`channel_id`**, **`target_id`**, **`target_type`** (`role`/`member`), `allow_permissions`, `deny_permissions`, `remove` (bool), `reason`, `dry_run` | `SetChannelPermissions`. Permissions accept permission names (`["ViewChannel","SendMessages"]`) or a decimal bitfield string. |
| 18 | `discord_create_forum_channel` | W | **`guild_id`**, **`name`**, `topic`, `tags` (array ≤ 20), `category_id`, `default_reaction` | `CreateForumChannel`. |
| 19 | `discord_create_forum_post` | W | **`forum_channel_id`**, **`title`**, **`content`**, `tag_names` (array) | `CreateForumPost`. Unknown tag names → error listing the available tags. |
| 20 | `discord_get_forum_post` | R | **`thread_id`**, `limit` (1–100, default 50) | `GetForumPost`. Starter + replies. |
| 21 | `discord_reply_to_forum` | W | **`thread_id`**, **`content`** | `ReplyToForum`. |
| 22 | `discord_list_forum_threads` | R | **`forum_channel_id`**, `include_archived` (default false) | `ListForumThreads`. |
| 23 | `discord_list_members` | R | **`guild_id`**, `limit` (1–1000, default 100), `after` | `ListMembers`. Requires the `GuildMembers` intent. Error text says so when it is missing. |
| 24 | `discord_manage_roles` | R/D, DR | **`action`** (`list`/`create`/`edit`/`delete`/`assign`/`revoke`), **`guild_id`**, `role_id`, `user_id`, `name`, `color` (hex or int), `permissions` (names or bitfield), `hoist`, `mentionable`, `reason`, `dry_run` | Dispatches to `ListRoles`/`CreateRole`/`EditRole`/`DeleteRole`/`AssignRole`/`RemoveRole`. Validate per-action required params. Refuse edits to roles at or above the bot's highest role, with a clear message. |
| 25 | `discord_create_webhook` | W | **`channel_id`**, **`name`** | `CreateWebhook`. Returns `WebhookInfo` **without the token**. |
| 26 | `discord_send_webhook_message` | W | `webhook_id` *or* `webhook_url` (one required), **`content`**, `username`, `avatar_url`, `thread_id`, `embed` | `SendWebhookMessage`. `webhook_url` must match `https://(discord\|discordapp).com/api/webhooks/{id}/{token}`. |
| 27 | `discord_edit_webhook` | D, DR | **`webhook_id`**, `name`, `channel_id`, `dry_run` | `EditWebhook`. |
| 28 | `discord_delete_webhook` | D, DR | **`webhook_id`**, `reason`, `dry_run` | `DeleteWebhook`. If the webhook is a persona webhook, also remove its `persona_webhooks` row. |

## 3. Differentiating Tools (29–37)
| # | Tool | Kind | Parameters (required in **bold**) | Behaviour |
| :-- | :--- | :-- | :--- | :--- |
| 29 | `discord_search_messages` | R | **`guild_id`**, **`query`**, `channel_id`, `author_id`, `before`, `after`, `has_attachments`, `limit` (1–100, default 25), `auto_backfill_limit` (0–5000, default 500) | See §3.1. Returns `SearchHit[]` plus `indexed_channels` and `backfilled` counts. |
| 30 | `discord_await_interaction` | W | **`channel_id`**, **`prompt_text`**, `mode` (`buttons`/`select`/`modal`, default `buttons`), `options` (array of `{label, value, style, emoji}`; default Approve/Reject), `allowed_user_ids`, `allowed_role_ids`, `timeout_seconds` (10–900, default 300), `min_values`/`max_values` (select) | See §3.2. Returns `InteractionResult`. |
| 31 | `discord_persona_register` | W | **`persona_name`**, **`display_name`**, `avatar_url`, `description`, `default_channel_id` | `Store.UpsertPersona`. `persona_name` is `[a-z0-9-]{1,32}`. `avatar_url` must be `https`. Works offline (no Discord call). |
| 32 | `discord_persona_send` | W | **`persona_name`**, **`content`**, `channel_id` (falls back to the persona default), `thread_id`, `embed` | See §3.3. |
| 33 | `discord_create_poll` | W | **`channel_id`**, **`question`** (≤ 300), **`answers`** (array 2–10, each ≤ 55), `duration_hours` (1–768, default 24), `allow_multiselect` (default false) | `CreatePoll`. |
| 34 | `discord_get_poll_results` | R | **`channel_id`**, **`message_id`** | `GetPollResults`. Tallies, total, winners (ties → multiple), `finalized`. |
| 35 | `discord_manage_threads` | W/D, DR | **`action`** (`create`/`archive`/`unarchive`/`lock`/`unlock`), `channel_id` (parent, for create), `thread_id` (for other actions), `message_id` (thread from a message), `name`, `private` (bool), `auto_archive_minutes` (60/1440/4320/10080), `dry_run` | `CreateThread` / `ModifyThread`. `dry_run` applies to `archive`/`lock`. |
| 36 | `discord_manage_events` | R/W | **`action`** (`list`/`create`), **`guild_id`**, `name`, `description`, `start_time_iso`, `end_time_iso`, `location_or_channel_id`, `entity_type` (`stage`/`voice`/`external`) | `ListScheduledEvents` / `CreateScheduledEvent`. `external` requires `location_or_channel_id` (as location) and `end_time_iso`. Start time must be in the future. |
| 37 | `discord_governance_audit` | R/D, DR | **`action`** (`audit_logs`/`automod_rules`/`timeout_member`), **`guild_id`**, `user_id`, `action_type` (int filter), `limit` (1–100, default 50), `duration_seconds` (0–2419200; 0 clears), `reason`, `dry_run` | `GetAuditLogs` / `ListAutoModRules` / `TimeoutMember`. |

### 3.1 Search + backfill algorithm (`search.go`)
1. Resolve the guild and the optional channel and author. Run `Guard.Check` (read op).
2. Determine target channels: the given `channel_id`, or else every text, announcement, and forum-thread channel in the guild that the bot can read.
3. For each target channel where `Store.Coverage` shows `full == false` and `auto_backfill_limit > 0`, page backwards via `ReadMessages(before=oldest_id, limit=100)`. Upsert each page, then `UpdateCoverage`. Stop at the per-call budget (`auto_backfill_limit` messages total across channels), an empty page (`full = true`), or a `before` bound older than the requested `after` filter. Process channels sequentially and respect rate limits.
4. Run `Store.Search`. If backfill was truncated by budget, include a `"warning": "partial index: N channels not fully backfilled"` field.
5. Never call Discord's `/guilds/{id}/messages/search` endpoint (bots get 403).

### 3.2 HITL broker (`interactions.go`)
- A process-wide `Broker` keyed by a random `custom_id` prefix (`dmcp:<16-hex>:<value>`). It registers one `InteractionCreate` handler at startup through a small hook that `client.go` exposes (`OnInteraction(func(*discordgo.InteractionCreate))`). Because that hook is not on `discord.Service`, the tool layer depends only on `Service.AwaitInteraction`, and the broker lives in `client.go`'s implementation. `interactions.go` owns validation, defaults, and formatting.
- **Buttons:** ≤ 5 per action row, ≤ 25 total. Style is one of `primary`/`secondary`/`success`/`danger` (Approve → `success`, Reject → `danger` by default).
- **Select:** A string select with ≤ 25 options.
- **Modal:** Posts a single "Respond" button. On click, it opens a modal whose text inputs come from `options` (label → input label, value → custom_id). It returns `ModalFields`.
- **Authorisation:** Clicks from users outside `allowed_user_ids` ∪ members of `allowed_role_ids` get an ephemeral "not authorised" reply and the wait continues. If both lists are empty, any non-bot guild member may respond.
- **Completion:** ACK within 3 s (`DeferredMessageUpdate`), then edit the original message to disable all components and append `✅ <label> by @user` or `⌛ timed out`.
- **Timeout/cancel:** Return `TimedOut: true` (not an error). Cancelling the MCP request context also disables the components.

### 3.3 Persona send (`webhooks_persona.go`)
1. Load the persona (`Store.GetPersona`). If unknown, return an error listing the registered personas.
2. Target the channel: `channel_id`, or else the persona's default. If the target is a thread, use its parent for the webhook and pass `thread_id`.
3. Look up `persona_webhooks[parent_channel]`. If it is missing, `CreateWebhook(parent, "MCP Personas")` and store the ID. The token stays inside the live client and is never returned. Discord rejects webhook names and usernames containing "discord" or "clyde", so the tools validate against both. If a send returns `404 Unknown Webhook`, delete the row, recreate once, and retry.
4. `SendWebhookMessage` with `username = display_name`, `avatar_url = persona.avatar_url`.
5. Never expose the webhook token in output or logs.

## 4. Resources (`resources/resources.go`)
Register with `mcp.NewResourceTemplate` and `s.AddResourceTemplate`.

### `discord://guilds/{guild_id}/map`
MIME `text/markdown`. A single compact document:
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
Sort categories by position, then uncategorised channels. Include active threads only. Truncate topics to 80 chars.

### `discord://channels/{channel_id}/transcript{?limit,before}`
MIME `text/plain`. The last 100 messages (query param `?limit=` up to 500; `?before=`), in chronological order:
```text
[2026-11-01 14:55] @alice: Shipping v1.2 tomorrow — @Moderator please review #release-notes
[2026-11-01 14:57] @bob ↪ reply to alice: LGTM 👍  [2 attachments]
```
All `<@id>`, `<@&id>`, and `<#id>` mentions resolve to `@name`, `@role`, and `#channel`. Times are UTC.

### Static resource
`discord://guilds` (MIME `application/json`) is the `ListGuilds` output. It serves as an entry point for clients that browse resources.

### Resource subscriptions
Enable with `server.WithResourceCapabilities(true /*subscribe*/, true /*listChanged*/)`. On a Gateway `MessageCreate` in a channel with an active subscription to its transcript URI, send `notifications/resources/updated`. Do the same for `map` on channel, role, or thread create/update/delete. Debounce to ≤ 1 notification per URI per 2 s. Implemented by `internal/mcp/subscriptions.go` (`Notifier`); notifications go only to sessions subscribed to that URI, including URIs with query strings.

### Webhook policy scope
`discord_edit_webhook`, `discord_delete_webhook`, and `discord_send_webhook_message` (by `webhook_id`) resolve the webhook's channel with `GetWebhook` and check policy against that channel and guild. A move is also checked against the destination channel. Read-only mode is checked first, before any lookup. There are no guild or channel allow/block lists (removed by owner decision); every guild and channel the bot can reach is in scope.

## 5. Prompts (`prompts/prompts.go`)
Register with `mcp.NewPrompt(name, mcp.WithPromptDescription(...), mcp.WithArgument(...))`.

| Prompt | Arguments | Generated messages |
| :--- | :--- | :--- |
| `discord-channel-digest` | **`channel_id`**, `since` (ISO-8601 or `24h`/`7d`, default `24h`), `audience` | Embeds the transcript resource for the window, then instructs Claude to produce **Summary**, **Decisions**, **Open Questions**, **Action Items (owner → task)**, and **Notable Links**, citing message timestamps. |
| `discord-server-blueprint` | **`guild_id`**, **`goal`** (free text, e.g. "open-source project with support forum") | Embeds the guild map resource, then instructs Claude to draft categories, channels, roles, and permission overwrites. It must call each mutating tool with `dry_run: true` first and present the combined preview before any real change. |
| `discord-hitl-approval` | **`channel_id`**, **`decision`**, `context`, `options` (comma-separated, default `Approve,Reject`), `approver` (`@user` or `@role`), `timeout_seconds` | Instructs Claude to format a decision card (what, why, risk, rollback) and call `discord_await_interaction` with the given options and approver. It then proceeds only on an explicit approval value. |

## 6. Test Matrix (per tool file)
For each tool:
- Schema test: the name, required params, and enums exist.
- Happy path against `MockClient`.
- Reference resolution (`#general` → `100000000000000010`).
- Policy denial (read-only mode).
- For **DR** tools, `dry_run: true` produces zero mutating `MockClient.Calls`.

`server_test.go` (Wave 3) asserts that `tools/list` returns exactly the 37 names above plus the 7 from spec 05.
