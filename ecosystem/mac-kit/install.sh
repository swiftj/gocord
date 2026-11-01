#!/usr/bin/env bash
# install.sh — install gocord (discord-mcp) on this Apple Silicon Mac for
# Claude Desktop, Cowork, and Claude Code. Safe to re-run (it upgrades in place).
#
# Usage: bash install.sh [--no-code] [--no-desktop]
#
# Token: if the Keychain has no "discord-mcp" entry yet, copy the bot token to
# the clipboard first (it is moved into the Keychain and the clipboard is
# cleared), or type it at the hidden prompt. It is never printed or written
# to a file.
set -euo pipefail

DO_CODE=1
DO_DESKTOP=1
for arg in "$@"; do
  case "$arg" in
    --no-code) DO_CODE=0 ;;
    --no-desktop) DO_DESKTOP=0 ;;
    -h|--help) sed -n '2,11p' "$0"; exit 0 ;;
    *) echo "install: unknown option: $arg" >&2; exit 2 ;;
  esac
done

say() { printf '==> %s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

[ "$(uname -s)" = "Darwin" ] || die "this kit is for macOS only"
[ "$(uname -m)" = "arm64" ] || die "this kit supports Apple Silicon Macs only"

KIT="$(cd "$(dirname "$0")" && pwd)"
LIB="$HOME/.local/lib/discord-mcp"
BIN="$HOME/.local/bin"
SHARE="$HOME/.local/share/gocord"
CONF="$HOME/.config/discord-mcp"
KC_SERVICE="discord-mcp"
KC_ACCOUNT="DISCORD_BOT_TOKEN"

# Files copied by AirDrop or a browser are quarantined; Gatekeeper would block
# the unsigned binary.
xattr -dr com.apple.quarantine "$KIT" 2>/dev/null || true

# --- 1. Bot token -> Keychain ------------------------------------------------
looks_like_token() { case "$1" in *.*.*) [ "${#1}" -ge 50 ] ;; *) return 1 ;; esac; }

if /usr/bin/security find-generic-password -s "$KC_SERVICE" -a "$KC_ACCOUNT" >/dev/null 2>&1; then
  say "bot token already in Keychain"
else
  token="$(pbpaste 2>/dev/null || true)"
  if looks_like_token "$token"; then
    say "using the bot token on the clipboard"
    from_clipboard=1
  else
    from_clipboard=0
    [ -t 0 ] || die "no bot token in Keychain or on the clipboard; copy it and re-run"
    printf 'Paste the Discord bot token (input hidden): '
    IFS= read -rs token
    printf '\n'
    looks_like_token "$token" || die "that does not look like a Discord bot token"
  fi
  /usr/bin/security add-generic-password -U -s "$KC_SERVICE" -a "$KC_ACCOUNT" \
    -l "gocord Discord bot token" -w "$token"
  [ "$from_clipboard" -eq 1 ] && pbcopy </dev/null
  unset token
  say "bot token stored in Keychain (service $KC_SERVICE)"
fi

# Verify the token without printing it.
if command -v curl >/dev/null 2>&1; then
  me="$(/usr/bin/security find-generic-password -s "$KC_SERVICE" -a "$KC_ACCOUNT" -w \
    | sed 's/^/Authorization: Bot /' \
    | curl -fsS -H @- https://discord.com/api/v10/users/@me 2>/dev/null || true)"
  case "$me" in
    *'"username"'*) say "token OK: bot $(printf '%s' "$me" | sed -E 's/.*"username": *"([^"]*)".*/\1/')" ;;
    *) warn "could not verify the token with Discord (offline, or the token was reset)" ;;
  esac
fi

# --- 2. Binary + launcher ------------------------------------------------------
mkdir -p "$LIB" "$BIN" "$SHARE"
mkdir -p "$CONF" && chmod 0700 "$CONF"
install -m 0755 "$KIT/bin/discord-mcp" "$LIB/discord-mcp"
install -m 0755 "$KIT/bin/discord-mcp-launch" "$LIB/discord-mcp-launch"
cat >"$BIN/discord-mcp" <<'SHIM'
#!/bin/sh
exec "$HOME/.local/lib/discord-mcp/discord-mcp-launch" "$@"
SHIM
chmod 0755 "$BIN/discord-mcp"
say "installed $("$LIB/discord-mcp" --version 2>&1 | head -n 1) to $LIB"
case ":$PATH:" in
  *":$BIN:"*) ;;
  *) warn "$BIN is not on your PATH; add it in ~/.zprofile so Claude Code can find discord-mcp" ;;
esac

# --- 3. Claude Code plugin -----------------------------------------------------
if [ "$DO_CODE" -eq 1 ]; then
  if command -v claude >/dev/null 2>&1; then
    rm -rf "$SHARE/ecosystem"
    cp -R "$KIT/ecosystem" "$SHARE/ecosystem"
    claude plugin marketplace add "$SHARE/ecosystem" >/dev/null 2>&1 \
      || claude plugin marketplace update discord-mcp >/dev/null 2>&1 || true
    if claude plugin install discord@discord-mcp >/dev/null 2>&1; then
      say "Claude Code plugin discord@discord-mcp installed (start a new session to load it)"
    else
      warn "could not install the Claude Code plugin; run: claude plugin install discord@discord-mcp"
    fi
  else
    warn "Claude Code CLI ('claude') not found; skipping the Code plugin"
  fi
fi

# --- 4. Claude Desktop extension + Cowork plugin -------------------------------
if [ "$DO_DESKTOP" -eq 1 ]; then
  if [ -d /Applications/Claude.app ] || [ -d "$HOME/Applications/Claude.app" ]; then
    open -a Claude "$KIT/gocord_darwin_arm64.dxt"
    say "Claude Desktop: click Install and leave the token field EMPTY (it reads the Keychain)"
  else
    warn "Claude Desktop not found; install it, then open $KIT/gocord_darwin_arm64.dxt"
  fi
  open -R "$KIT/discord.plugin"
  say "Cowork: if the Discord plugin isn't already under Customize > Plugins, upload discord.plugin (shown in Finder)"
fi

say "done"
