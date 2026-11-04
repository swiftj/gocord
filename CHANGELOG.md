# Changelog

## Unreleased

### Added

- **Media access:** `discord_fetch_media` returns attachments, author avatars, or
  allowlisted URLs inline as base64. It resizes in pure Go, re-encodes to JPEG or
  PNG, and pages payloads over 1 MB with a stable `sha256`.
  `discord_get_member_avatar` returns a member's account and server avatars.
- **LegendKeeper bridge:** `discord_media_to_legendkeeper`,
  `fetch_url_to_legendkeeper`, and `discord_batch_media_to_legendkeeper` copy
  images into a LegendKeeper asset library server-side through the `golk` SDK.
  They create folder paths as needed and deduplicate repeat uploads. Configure
  them with `LEGENDKEEPER_API_KEY` and `LEGENDKEEPER_PROJECT_ID`.
- **Tupperbox helpers:** message objects carry `is_webhook`, `webhook_id`,
  `author_display_name`, and `author_avatar_url`. Attachments add `id`,
  `proxy_url`, `width`, and `height`. `discord_list_tuppers` lists webhook
  characters with their latest art.
- **Diagnostics:** `discord_check_access` computes the bot's View/Read History
  access per channel and suggests a fix for each blocked one.
- The Desktop extension has optional LegendKeeper API key, project, and
  extra-image-host fields. The Mac launcher reads the LegendKeeper key from the
  Keychain (`discord-mcp` / `LEGENDKEEPER_API_KEY`).

### Changed

- Search index schema v2. Upgrading clears backfill coverage once, so channels
  are re-read to fill the webhook and avatar columns.
- Minimum Go version is 1.27, as required by `github.com/swiftj/golk`.
- Tool count: 37 → 44.

### Fixed

- `build-mac-kit.sh` now defaults to the semver tag on HEAD, or to
  `0.<UTC date>.<UTC time>` when HEAD has none. The old default (`0.0.0-<sha>`)
  sorted below already-installed versions, so Claude Desktop kept the old
  extension without saying so.
