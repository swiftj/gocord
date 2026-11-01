#!/usr/bin/env bash
# build-mac-kit.sh — package a self-contained Apple Silicon install kit for
# Claude Desktop, Cowork, and Claude Code. Intel Macs are not supported.
#
# Usage: ecosystem/mac-kit/build-mac-kit.sh [--default-guild <snowflake>] [version]
#
#   --default-guild  guild used when a tool omits guild_id (baked into the
#                    Desktop extension's defaults; never commit the result)
#   version          defaults to `git describe --tags --always` (leading "v" stripped)
#
# Output: dist-dxt/gocord-mac-kit_<version>.zip containing
#   install.sh, README.txt, bin/{discord-mcp,discord-mcp-launch},
#   gocord_darwin_arm64.dxt, discord.plugin, ecosystem/ (Claude Code marketplace).
# The kit carries no token: install.sh stores it in the target Mac's Keychain.
set -euo pipefail

GUILD=""
VERSION=""
while [ $# -gt 0 ]; do
  case "$1" in
    --default-guild) GUILD="${2:?--default-guild needs a value}"; shift 2 ;;
    -h|--help) sed -n '2,15p' "$0" >&2; exit 0 ;;
    -*) echo "build-mac-kit: unknown option: $1" >&2; exit 2 ;;
    *) VERSION="$1"; shift ;;
  esac
done

log() { printf 'build-mac-kit: %s\n' "$*" >&2; }
die() { log "error: $*"; exit 1; }

KIT_SRC="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$KIT_SRC/../.." && pwd)"
cd "$ROOT"

for tool in go jq zip; do
  command -v "$tool" >/dev/null 2>&1 || die "'$tool' is required but not installed"
done
if [ -n "$GUILD" ] && ! printf '%s' "$GUILD" | grep -Eq '^[0-9]{17,20}$'; then
  die "--default-guild must be a numeric Snowflake"
fi

[ -n "$VERSION" ] || VERSION="$(git describe --tags --always 2>/dev/null || echo dev)"
VERSION="${VERSION#v}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
if printf '%s' "$VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.+-]+)?$'; then
  MANIFEST_VERSION="$VERSION"
else
  MANIFEST_VERSION="0.0.0-$(printf '%s' "$VERSION" | tr -c '0-9A-Za-z-' '-' | sed 's/-*$//')"
fi

OUT_DIR="$ROOT/dist-dxt"
STAGE="$OUT_DIR/kit-stage"
KIT="$STAGE/gocord-mac-kit"
rm -rf "$STAGE"
mkdir -p "$KIT/bin"
trap 'rm -rf "$STAGE"' EXIT

# --- 1. Binary + launcher ----------------------------------------------------
log "building darwin/arm64"
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath \
  -ldflags="-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.date=$DATE" \
  -o "$KIT/bin/discord-mcp" ./cmd/discord-mcp
install -m 0755 "$KIT_SRC/discord-mcp-launch.sh" "$KIT/bin/discord-mcp-launch"

# --- 2. Desktop extension (Keychain-backed, token optional) ------------------
DXT="$STAGE/dxt"
mkdir -p "$DXT/bin"
cp "$KIT/bin/discord-mcp" "$KIT/bin/discord-mcp-launch" "$DXT/bin/"
cp "$ROOT/ecosystem/dxt/icon.png" "$DXT/icon.png"
TOOLS_JSON="null"
if [ "$(uname -s)/$(uname -m)" = "Darwin/arm64" ]; then
  TOOLS_JSON="$("$KIT/bin/discord-mcp" --list-tools-json </dev/null 2>/dev/null \
    | jq -c '(if type == "object" and has("tools") then .tools else . end)
             | map({name, description: ((.description // "") | split("\n")[0])})' 2>/dev/null || echo null)"
fi
jq --arg v "$MANIFEST_VERSION" --arg g "$GUILD" --argjson t "$TOOLS_JSON" '
    .version = $v
  | .compatibility.platforms = ["darwin"]
  | .server.mcp_config.command = "${__dirname}/bin/discord-mcp-launch"
  | .user_config.discord_bot_token.required = false
  | .user_config.discord_bot_token.description = "Leave empty to read it from the macOS Keychain (service discord-mcp)."
  | .user_config.db_path.default = "${HOME}/.config/discord-mcp/desktop-index.db"
  | (if $g != "" then .user_config.default_guild_id.default = $g else . end)
  | (if $t != null then .tools = $t else . end)
' "$ROOT/ecosystem/dxt/manifest.json" >"$DXT/manifest.json"
(cd "$DXT" && zip -qrX "$KIT/gocord_darwin_arm64.dxt" manifest.json icon.png bin)

# --- 3. Cowork plugin (MCP comes from the Desktop extension) -----------------
CW="$STAGE/cowork"
mkdir -p "$CW"
cp -R "$ROOT/ecosystem/claude-plugin/.claude-plugin" "$ROOT/ecosystem/claude-plugin/skills" \
  "$ROOT/ecosystem/claude-plugin/commands" "$ROOT/ecosystem/claude-plugin/agents" "$CW/"
# Cowork names the extension's tools differently; let agents inherit every tool.
for f in "$CW"/agents/*.md; do
  awk 'BEGIN{fm=0} /^---$/{fm++} !(fm==1 && /^tools:/)' "$f" >"$f.tmp" && mv "$f.tmp" "$f"
done
(cd "$CW" && zip -qrX "$KIT/discord.plugin" .)

# --- 4. Claude Code marketplace ----------------------------------------------
mkdir -p "$KIT/ecosystem"
cp -R "$ROOT/ecosystem/.claude-plugin" "$ROOT/ecosystem/claude-plugin" "$KIT/ecosystem/"

# --- 5. Installer + README, then zip -----------------------------------------
install -m 0755 "$KIT_SRC/install.sh" "$KIT/install.sh"
sed "s/@VERSION@/$VERSION/" "$KIT_SRC/README.txt" >"$KIT/README.txt"

OUT="$OUT_DIR/gocord-mac-kit_$(printf '%s' "$VERSION" | tr '/' '-').zip"
rm -f "$OUT"
(cd "$STAGE" && zip -qrXy "$OUT" gocord-mac-kit)
printf '%s\n' "$OUT"
