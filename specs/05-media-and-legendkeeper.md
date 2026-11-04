# Spec 05 — Media access, Tupperbox avatars, and the LegendKeeper bridge

Claude often runs in a sandbox that cannot reach `cdn.discordapp.com`,
`media.discordapp.net`, `assets.legendkeeper.com`, or other image hosts. This
spec adds two ways to move image bytes without the model downloading them:
**inline** (base64 inside a tool result) and **server-side** (gocord copies the
image into a LegendKeeper asset library and returns the asset URL). It also adds
identity fields for webhook (Tupperbox/PluralKit) messages and a permissions
diagnostic. Tool count: 37 → **44**.

## 1. Contract additions

`internal/discord.Service` gains two methods (the interface was extended the same
way for `GetWebhook` in Wave 3):

| Method | Purpose |
| :--- | :--- |
| `GetMessage(ctx, channelID, messageID) (*dto.MessageItem, error)` | Re-fetch one message so its signed CDN URLs are fresh. |
| `GetMember(ctx, guildID, userID) (*dto.MemberProfile, error)` | A member's avatar hashes, nick, and role IDs. `userID == ""` means the bot. |

`dto.ChannelInfo.Overwrites []PermissionOverwrite` carries permission overwrites
(`json:"-"`, never serialised) for `discord_check_access`.

### Message objects

Every serialised message (`discord_read_messages`, `discord_search_messages`,
`discord_get_forum_post`, and the message objects other tools return) carries:

| Field | Meaning |
| :--- | :--- |
| `is_webhook` | `webhook_id != ""` (always present). |
| `webhook_id` | Omitted for normal users. |
| `author_display_name` | Webhook messages: the per-message webhook username (the Tupper's name). Users: nick > global name > username. |
| `author_avatar_url` | As displayed. Webhooks: `cdn.discordapp.com/avatars/{author.id}/{hash}`. Users: guild avatar > user avatar > default embed avatar. `.gif` for `a_` hashes, else `.png`; always `?size=1024`. |
| `attachments[]` | Adds `id`, `proxy_url`, `width`, `height` to `filename`, `url`, `size`, `content_type`. |

Attachment and avatar URLs are signed (`?ex=&is=&hm=`) and expire, typically after
about 24 hours. Consumers re-resolve them from `channel_id` + `message_id`.

REST message objects omit `member`, so the live client warms its member cache
(at most 10 lookups per read, skipping webhooks and bots) to resolve nicks and
guild avatars. Gateway messages carry `member` and update the cache directly.

### Index schema v2

`messages` gains `webhook_id`, `author_display_name`, `author_avatar_url`, and
`attachments_json`. Upgrading a v1 database adds the columns and clears
`channel_coverage`, so the next search or tupper scan re-reads channels and fills
the new columns. `Store.ListWebhookAuthors` groups webhook messages by
`(webhook_id, display name)`.

## 2. `internal/media`

`Source` names exactly one image. Set one of `AttachmentIndex` (0-based),
`AttachmentID`, `Avatar`, or `URL`. The first three also need `ChannelID` and
`MessageID`. `Fetcher.Resolve` and `Fetcher.Fetch` work like this:

- Message sources are always re-fetched with `GetMessage`.
- URL sources must be http(s), carry no userinfo, and not be an IP literal. The
  host must match the allowlist: `cdn.discordapp.com`, `media.discordapp.net`,
  `images-ext-*.discordapp.net`, `*.magnific.com`, `cdn.magnific.com`,
  `*.freepik.com`, `assets.legendkeeper.com`, plus `GOCORD_MEDIA_ALLOWED_HOSTS`
  (comma-separated; `*.suffix` and in-label globs). The rejection error lists the
  allowed hosts.
- Downloads are capped at 25 MB (`GOCORD_MEDIA_MAX_BYTES`), time out after 30 s,
  and follow at most 5 redirects. Every redirect hop is re-checked against the
  allowlist. No credentials are ever sent, and errors name the host, never the
  signed URL.
- The type is sniffed from the bytes (`http.DetectContentType`). Only PNG, JPEG,
  GIF, and WebP are accepted. Dimensions come from the stdlib decoders plus
  `golang.org/x/image/webp`.
- `FetchOptions{MaxDimension, Format, Quality, StripGIFToFirstFrame}`:
  - Resizing uses `x/image/draw` CatmullRom, preserves the aspect ratio, and
    never upscales.
  - With an empty `Format` and no resize, the original bytes pass through untouched.
  - There is no pure-Go WebP encoder, so WebP output is PNG.
  - JPEG output flattens transparency onto white.
  - Re-encoding an animated GIF needs `StripGIFToFirstFrame`.
- `FetchCached` caches the encoded result under `sha256(source key + options)`.
  The cache holds entries for 10 minutes, with at most 64 entries and 200 MB (LRU).
  Message sources are keyed by IDs, not by URL.

## 3. Tools

| # | Tool | Kind | Summary |
| :-- | :--- | :-- | :--- |
| 38 | `discord_get_member_avatar` | R | `{guild_id?, user_id, size?∈128…4096}` → `{user_id, display_name, avatar_url, guild_avatar_url?, animated}` |
| 39 | `discord_fetch_media` | R | Inline base64, chunked. See §3.1. |
| 40 | `discord_media_to_legendkeeper` | W (LK) | Message attachment or avatar → LK asset. |
| 41 | `fetch_url_to_legendkeeper` | W (LK) | Allowlisted URL → LK asset. |
| 42 | `discord_batch_media_to_legendkeeper` | W (LK) | 1–25 items, sequential, 250 ms apart. |
| 43 | `discord_list_tuppers` | R | Webhook identities with their latest avatar. |
| 44 | `discord_check_access` | R | Effective View/ReadHistory per channel with a fix string. |

All message-source tools resolve `channel_id` and run a read policy check on it.
The bridge tools also respect `--read-only`: they write to LegendKeeper.

### 3.1 `discord_fetch_media`

Input:

- Source: `channel_id`, `message_id`, plus one of `attachment_index`,
  `attachment_id`, or `avatar`; or `url` instead.
- `max_dimension`: default 1600; 0 keeps the original size.
- `format`: `jpeg` (default), `png`, `webp`, or `original`.
- `quality`: default 85.
- `offset`, `length`: for chunked retrieval.

Output: `{mime, filename, width, height, total_bytes, offset, length, eof, sha256, base64, hint?}`.

- `format: "original"` returns the untouched bytes and ignores `max_dimension`.
- Without `offset`/`length`, the tool returns the whole payload if it is ≤ 1,000,000
  bytes. Otherwise it returns the first 1,000,000 bytes, `eof:false`, and a `hint`
  explaining how to page.
- `length` is at most 1,000,000. Every chunk is cut from the same cached encoding,
  so `sha256` is constant and concatenating all chunks reproduces it.

### 3.2 Bridge tools

- Configuration: `LEGENDKEEPER_API_KEY` (required), `LEGENDKEEPER_PROJECT_ID`
  (the default project), and `LEGENDKEEPER_BASE_URL` (optional).
- Without an API key the tools still register, but return
  `LegendKeeper not configured: …`.
- Uploads go through `github.com/swiftj/golk` `Assets.UploadImage`, which runs
  three steps: create an upload, PUT the bytes to signed storage with only the
  returned headers, then create the image. Folders use `Assets.List` and
  `Assets.CreateFolder`, scoped per call with `lk.ForProject`. **No LegendKeeper
  calls are inferred from the web app.**
- Targets:
  - Pass at most one of `lk_folder_id` and `lk_folder_path`.
  - A path such as `Campaign/Characters` is resolved case-insensitively and
    missing folders are created. Results are cached per process.
- Names:
  - `name` is at most 120 characters. Control characters and `/` `\` are dropped
    and whitespace is collapsed.
  - The default name is the source filename's stem, with `_`/`-` turned into spaces.
- Dedupe:
  - The index file is `$XDG_CACHE_HOME/gocord/lk-assets.json` (or
    `~/.cache/gocord/`), mode 0600, written atomically.
  - It maps `project:sha256(final bytes)` to the asset.
  - A hit returns that asset with `deduplicated: true` and does not upload.
- Output: `{asset_id, url, name, width, height, content_type, bytes, folder_id, source_url, project, deduplicated?}`.
- Batch output: `{results:[{index, ok, asset_id?, url?, deduplicated?, error?}], succeeded, failed}`.
  `continue_on_error` defaults to true.
- Logs contain asset IDs and byte counts only, never API keys or signed URLs.

### 3.3 `discord_list_tuppers`

- Input: `{guild_id?, channel_id?, since? (ISO-8601 or Snowflake), limit? (1–500, default 100)}`.
- First backfills the targets exactly like `discord_search_messages`, using a
  500-message budget.
- Then groups indexed webhook messages by `(webhook_id, display name)`, newest first.
- Each entry reports `avatar_url`, `last_seen_message_id`, `last_seen_channel_id`,
  `last_seen_at`, and `message_count`, all taken from the newest message.
- Avatar URLs reflect the latest message. Fetch them promptly, or bridge them with
  `discord_media_to_legendkeeper(avatar: true)`.

### 3.4 `discord_check_access`

- Input: `{guild_id?, channel_ids?}`. The default checks every text,
  announcement, and forum channel. A thread is checked through its parent.
- Effective permissions follow Discord's algorithm:
  1. Start from @everyone plus the bot's role permissions (Administrator or
     owner grants everything).
  2. Apply the @everyone overwrite.
  3. Apply the bot's role overwrites, aggregated.
  4. Apply the bot's member overwrite.
- Output: `{channels:[{id, name, category, can_view, can_read_history, missing, fix?}], summary:{readable, blocked}}`.
- `fix` names the bot's managed role and points at one of two places:
  - the category, when the channel's overwrites are synced with it;
  - otherwise the channel itself.

## 4. Dependencies

- `golang.org/x/image` (pure Go: `draw`, `webp`).
- `github.com/swiftj/golk` v0.4.2.
- golk requires Go 1.27, so the module's `go` directive is now 1.27.
- Everything still builds with `CGO_ENABLED=0`.
