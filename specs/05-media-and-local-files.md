# Spec 05 — Media access, Tupperbox avatars, and local media files

Claude often runs in a sandbox that cannot reach `cdn.discordapp.com`,
`media.discordapp.net`, `assets.legendkeeper.com`, or other image hosts. gocord
moves image bytes in two ways: **inline** (base64 inside a tool result) or **to
local disk** under a media root, where another local MCP server (e.g. lkmcp for
LegendKeeper) can pick the file up by path. gocord itself never talks to the
LegendKeeper API. This spec also adds identity fields for webhook
(Tupperbox/PluralKit) messages and a permissions diagnostic. Tool count:
37 → **44**.

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
- URL sources must be http(s) and carry no userinfo. **There is no host
  allowlist** (removed by owner decision): any public host is accepted.
  Connections to loopback, private (RFC 1918 / ULA), link-local, CGNAT,
  multicast, or unspecified addresses are refused. IP literals are checked in
  `CheckURL`; host names are checked at dial time on the resolved IP
  (`net.Dialer.Control`), which also defeats DNS rebinding. Proxies are not
  used for media downloads.
- Downloads are capped at 25 MB (`GOCORD_MEDIA_MAX_BYTES`), time out after 30 s,
  and follow at most 5 redirects. Every redirect hop is re-checked against the
  same rule. No credentials are ever sent, and errors name the host, never the
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
| 39 | `discord_fetch_media` | R / local write | Inline base64 (chunked), or `save_to` a file. See §3.1. |
| 40 | `fetch_url_to_file` | local write | Public URL → file. See §3.2. |
| 41 | `discord_batch_fetch_media` | local write | 1–25 message sources → files, sequential, 250 ms apart. |
| 42 | `media_list_files` | R | Files under the media root. |
| 43 | `discord_list_tuppers` | R | Webhook identities with their latest avatar. |
| 44 | `discord_check_access` | R | Effective View/ReadHistory per channel with a fix string. |

All message-source tools resolve `channel_id` and run a read policy check on it.
Saving writes only the local disk, so `--read-only` (a Discord-write guard) does
not block it. The saving tools are annotated `readOnlyHint: false`.

### 3.1 `discord_fetch_media`

Input:

- Source: `channel_id`, `message_id`, plus one of `attachment_index`,
  `attachment_id`, or `avatar`; or `url` instead.
- `max_dimension`: default 1600; 0 keeps the original size.
- `format`: `jpeg`, `png`, `webp`, or `original`.
- `quality`: default 85.
- `offset`, `length`: for chunked base64.
- `save_to`, `overwrite`: write a file instead (§3.2).

Without `save_to` the output is
`{mime, filename, width, height, total_bytes, offset, length, eof, sha256, base64, hint?}`:

- `format: "original"` returns the untouched bytes and ignores `max_dimension`.
- Without `offset`/`length`, the tool returns the whole payload if it is ≤ 1,000,000
  bytes. Otherwise it returns the first 1,000,000 bytes, `eof:false`, and a `hint`.
- `length` is at most 1,000,000. Every chunk is cut from the same cached encoding,
  so `sha256` is constant and concatenating all chunks reproduces it.

With `save_to` the output is
`{path, mime, filename, width, height, bytes, sha256, overwritten, deduplicated}`
and no base64. `offset`/`length` are rejected.

### 3.2 Saving to disk (`save_to`, `fetch_url_to_file`, `discord_batch_fetch_media`)

- **Media root:** `GOCORD_MEDIA_DIR`, default `~/Downloads/gocord` (`~`
  expanded). Directories are created on demand with mode 0755; files are 0644.
- **Paths:**
  - A relative `save_to` resolves under the root. It is cleaned, and any result
    that escapes the root (`..`) is rejected.
  - An absolute `save_to` inside the root is accepted. Outside the root it needs
    `GOCORD_MEDIA_ALLOW_ABSOLUTE=true` (off by default).
  - For paths confined to the root, the deepest existing ancestor and then the
    created parent are resolved with `filepath.EvalSymlinks`. Both must stay
    inside the (resolved) root, so a symlinked directory cannot redirect a
    write, and nothing is created outside the root.
  - The target itself must not be a symlink or a directory.
  - File names must not start with `.`.
- **Extension:**
  - A `save_to` without an extension gets one from the output type (`.png`,
    `.jpg`, `.gif`, `.webp`). A dotted name such as "Ser T. Mournvale" counts as
    having no extension.
  - An extension that disagrees with the output type is an error. `.jpg` and
    `.jpeg` both mean JPEG.
- **Format default:**
  - Without `format`, an image extension on `save_to` picks it: `.png` → `png`,
    `.jpg`/`.jpeg` → `jpeg`, `.gif`/`.webp` → `original`.
  - Otherwise the default is `jpeg`. So `save_to: "x.png"` writes the untouched
    bytes of a PNG source.
  - Resize and format options apply before saving, e.g. `max_dimension: 2048,
    format: "jpeg"` caps a 4K render on its way to disk.
- **Existing files:**
  - Same sha256 → `deduplicated: true`, and nothing is written.
  - Different sha256 → an error unless `overwrite: true`.
  - New files and overwrites are written atomically: a temp file in the same
    directory is fsynced, renamed into place, and the directory is fsynced.
- **`fetch_url_to_file`:**
  - Input: `{url, save_to, overwrite?, max_dimension?, format?, quality?}`.
  - Output: the saved fields plus `source_url`.
- **`discord_batch_fetch_media`:**
  - Input: `{items: [{channel_id, message_id, attachment_index|attachment_id|avatar, save_to, overwrite?, max_dimension?, format?, quality?}] (1–25), continue_on_error? (default true)}`.
  - `save_to` is required on every item, and `url` is rejected (use `fetch_url_to_file`).
  - Output: `{results: [{index, ok, path?, sha256?, width?, height?, error?}], succeeded, failed}`.
- **`media_list_files`:**
  - Input: `{subdir?, glob?}`. `subdir` is relative and confined to the root.
    `glob` matches the file name or the root-relative path.
  - Output: `{root, files: [{path, bytes, mime, width?, height?, modified_at}], truncated?}`.
  - Hidden files, temp files, and symlinks are skipped; at most 1000 entries.
  - It is read-only.

### 3.3 `discord_list_tuppers`

- Input: `{guild_id?, channel_id?, since? (ISO-8601 or Snowflake), limit? (1–500, default 100)}`.
- First backfills the targets exactly like `discord_search_messages`, using a
  500-message budget.
- Then groups indexed webhook messages by `(webhook_id, display name)`, newest first.
- Each entry reports `avatar_url`, `last_seen_message_id`, `last_seen_channel_id`,
  `last_seen_at`, and `message_count`, all taken from the newest message.
- Avatar URLs reflect the latest message. Fetch them promptly, or save them with
  `discord_fetch_media(avatar: true, save_to: …)`.

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
- Everything still builds with `CGO_ENABLED=0` on Go 1.26+.
