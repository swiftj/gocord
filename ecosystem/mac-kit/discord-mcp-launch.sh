#!/bin/sh
# discord-mcp-launch — start discord-mcp with its bot token from the macOS
# Keychain (service "discord-mcp", account "DISCORD_BOT_TOKEN") so the token
# never lives in a config file. An explicit DISCORD_BOT_TOKEN still wins.
# Placeholders a host left unsubstituted ("${user_config.x}") count as unset.
# Never writes to stdout: stdio JSON-RPC runs over it.
for v in DISCORD_BOT_TOKEN DISCORD_MCP_DEFAULT_GUILD_ID DISCORD_MCP_DB_PATH DISCORD_MCP_READ_ONLY \
  GOCORD_MEDIA_DIR GOCORD_MEDIA_ALLOWED_HOSTS; do
  eval "val=\${$v:-}"
  case "$val" in ''|'${'*) unset "$v" ;; esac
done
if [ -z "${DISCORD_BOT_TOKEN:-}" ]; then
  DISCORD_BOT_TOKEN="$(/usr/bin/security find-generic-password -s discord-mcp -a DISCORD_BOT_TOKEN -w 2>/dev/null || true)"
  export DISCORD_BOT_TOKEN
fi
: "${DISCORD_MCP_DB_PATH:=$HOME/.config/discord-mcp/index.db}"
export DISCORD_MCP_DB_PATH
exec "$(dirname "$0")/discord-mcp" "$@"
