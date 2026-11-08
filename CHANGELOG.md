# Changelog

## Unreleased

### Added

- **Media access:** `discord_fetch_media` returns attachments, author avatars, or
  any public http(s) URL inline as base64. There is no host allowlist; only
  local and private network addresses are refused. It resizes in pure Go, re-encodes to JPEG or
  PNG, and pages payloads over 1 MB with a stable `sha256`.
  `discord_get_member_avatar` returns a member's account and server avatars.
- **Save media to disk:** `discord_fetch_media` takes `save_to`/`overwrite`.
  New tools: `fetch_url_to_file`, `discord_batch_fetch_media`, and
  `media_list_files`.
  - Files go under `GOCORD_MEDIA_DIR` (default `~/Downloads/gocord`), so other
    local tools (e.g. lkmcp for LegendKeeper) can upload them by path.
  - Paths are confined to the root, including against `..` and symlink tricks.
    Absolute paths outside it need `GOCORD_MEDIA_ALLOW_ABSOLUTE=true`.
  - Identical files are deduplicated by sha256, and writes are atomic.
- **Tupperbox helpers:** message objects carry `is_webhook`, `webhook_id`,
  `author_display_name`, and `author_avatar_url`. Attachments add `id`,
  `proxy_url`, `width`, and `height`. `discord_list_tuppers` lists webhook
  characters with their latest art.
- **Files from personas and webhooks:** `discord_persona_send` and
  `discord_send_webhook_message` take `file_path` and `file_paths`, so images
  and handouts post under a persona's name and avatar instead of the bot's.
  `discord_send` also takes `file_paths`. All three attach up to 10 files
  (up to 100 MiB each on disk) and accept paths relative to the media root, such as
  `Scenes/keep.png`; `content` is optional when an embed or file is given.
- **Automatic image shrinking:** when attachments are over the server's upload
  limit (10 MiB at boost level 0-1, 50 MiB at 2, 100 MiB at 3), the three send
  tools re-encode images as JPEG (PNG if transparent) and scale them down until
  they fit. The files on disk are never changed, and the result lists each change.
  `image_max_dimension` and `image_format` adjust images even when they fit.
  Non-image files over the limit are refused before upload, and a Discord
  "Request entity too large" error explains the limit.
- **Diagnostics:** `discord_check_access` computes the bot's View/Read History
  access per channel and suggests a fix for each blocked one.
- The Desktop extension has an optional "Saved Images Folder" field.

### Removed

- **All allow/block lists.** The media host allowlist
  (`GOCORD_MEDIA_ALLOWED_HOSTS` and the built-in host list) and the Discord
  guild/channel lists (`--allowed-guilds`, `--blocked-guilds`,
  `--allowed-channels`, `--blocked-channels`, their `DISCORD_MCP_*` variables,
  and the profile fields) are gone. Every guild and channel the bot can reach is
  in scope, and media downloads accept any public URL. Local and private network
  addresses are still refused. Old profile fields are ignored.

### Changed

- Search index schema v2. Upgrading clears backfill coverage once, so channels
  are re-read to fill the webhook and avatar columns.
- Tool count: 37 → 44.
- Message reads no longer fail on Discord component types that `discordgo` does
  not know yet (e.g. type 20); components are dropped instead.

### Fixed

- `build-mac-kit.sh` now defaults to the semver tag on HEAD, or to
  `0.<UTC date>.<UTC time>` when HEAD has none. The old default (`0.0.0-<sha>`)
  sorted below already-installed versions, so Claude Desktop kept the old
  extension without saying so.
