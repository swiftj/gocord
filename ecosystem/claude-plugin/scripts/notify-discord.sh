#!/bin/sh
# notify-discord.sh — non-blocking Claude Code / Cowork lifecycle hook.
#
# Usage: notify-discord.sh <event>   (event: stop | permission | any label)
# Reads the hook JSON payload from stdin and posts a short message to the
# Discord webhook in $DISCORD_MCP_NOTIFY_WEBHOOK_URL.
#
# Contract (specs/03-claude-ecosystem.md §6):
#   - never writes to stdout (hook stdout may be interpreted by the host)
#   - never echoes the webhook URL or any token
#   - always exits 0, even when unset/misconfigured/offline

# Silence everything: stdout and stderr go nowhere.
exec >/dev/null 2>&1

event="${1:-event}"

# Nothing to do without a webhook URL or curl.
[ -n "${DISCORD_MCP_NOTIFY_WEBHOOK_URL:-}" ] || exit 0
command -v curl >/dev/null 2>&1 || exit 0

# Only allow Discord webhook URLs over https.
case "$DISCORD_MCP_NOTIFY_WEBHOOK_URL" in
  https://discord.com/api/webhooks/*|https://discordapp.com/api/webhooks/*|https://ptb.discord.com/api/webhooks/*|https://canary.discord.com/api/webhooks/*) ;;
  *) exit 0 ;;
esac

# Read the hook payload (bounded to 64 KiB).
payload="$(head -c 65536 2>/dev/null || true)"

project=""
last=""
if command -v jq >/dev/null 2>&1 && [ -n "$payload" ]; then
  project="$(printf '%s' "$payload" | jq -r '.cwd // empty' 2>/dev/null || true)"
  last="$(printf '%s' "$payload" | jq -r '.message // .last_assistant_message // empty' 2>/dev/null || true)"
fi
[ -n "$project" ] || project="${CLAUDE_PROJECT_DIR:-$(pwd)}"
project="$(basename "$project" 2>/dev/null || echo project)"

case "$event" in
  stop) title="Claude finished a task" ;;
  permission) title="Claude is waiting for your approval" ;;
  *) title="Claude event: $event" ;;
esac

# Collapse whitespace and truncate the last message to 300 characters.
last="$(printf '%s' "$last" | tr '\r\n\t' '   ' | cut -c1-300)"

text="**[$project]** $title"
[ -n "$last" ] && text="$text
> $last"

# JSON-encode safely: prefer jq, else a minimal sed escape (\ " and control chars).
if command -v jq >/dev/null 2>&1; then
  body="$(printf '%s' "$text" | jq -Rs '{content: ., allowed_mentions: {parse: []}}' 2>/dev/null)" || exit 0
else
  esc="$(printf '%s' "$text" | tr -d '\000-\011\013-\037' | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' | awk 'BEGIN{ORS=""} NR>1{print "\\n"} {print}')"
  body="{\"content\":\"$esc\",\"allowed_mentions\":{\"parse\":[]}}"
fi

curl -fsS --max-time 5 -H 'Content-Type: application/json' -X POST \
  --data "$body" "$DISCORD_MCP_NOTIFY_WEBHOOK_URL" >/dev/null 2>&1 || true

exit 0
