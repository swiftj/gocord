#!/usr/bin/env bash
# build-dxt.sh — package discord-mcp as Claude Desktop Extensions (.dxt).
#
# Usage: ecosystem/dxt/build-dxt.sh [version]
#
#   version  defaults to `git describe --tags --always` (leading "v" stripped)
#
# Output: dist-dxt/discord-mcp_<version>_<os>_<arch>.dxt (one per target).
# macOS targets Apple Silicon only; Intel Macs are not supported.
# Writes to dist-dxt/ (never dist/, which `goreleaser --clean` wipes).
# Diagnostics go to stderr; the produced bundle paths are printed to stdout.
# Portable across macOS (BSD) and Linux (GNU) userlands.
set -euo pipefail

TARGETS="darwin/arm64 linux/amd64 linux/arm64"
VERSION=""

for arg in "$@"; do
  case "$arg" in
    -h|--help) sed -n '2,12p' "$0" >&2; exit 0 ;;
    -*) echo "build-dxt: unknown option: $arg" >&2; exit 2 ;;
    *) VERSION="$arg" ;;
  esac
done

log() { printf 'build-dxt: %s\n' "$*" >&2; }
die() { log "error: $*"; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$ROOT"

for tool in go jq zip; do
  command -v "$tool" >/dev/null 2>&1 || die "'$tool' is required but not installed"
done
[ -f "$SCRIPT_DIR/manifest.json" ] || die "missing $SCRIPT_DIR/manifest.json"
[ -f "$SCRIPT_DIR/icon.png" ] || die "missing $SCRIPT_DIR/icon.png"
[ -d "$ROOT/cmd/discord-mcp" ] || die "missing ./cmd/discord-mcp (nothing to build)"

if [ -z "$VERSION" ]; then
  VERSION="$(git describe --tags --always 2>/dev/null || echo dev)"
fi
VERSION="${VERSION#v}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

# The manifest version must be semver; fall back to a prerelease of 0.0.0.
if printf '%s' "$VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.+-]+)?$'; then
  MANIFEST_VERSION="$VERSION"
else
  MANIFEST_VERSION="0.0.0-$(printf '%s' "$VERSION" | tr -c '0-9A-Za-z-' '-' | sed 's/-*$//')"
fi
# File names must not contain path separators.
FILE_VERSION="$(printf '%s' "$VERSION" | tr '/' '-')"

OUT_DIR="$ROOT/dist-dxt"
STAGE_ROOT="$OUT_DIR/stage"
rm -rf "$STAGE_ROOT"
mkdir -p "$STAGE_ROOT"
trap 'rm -rf "$STAGE_ROOT"' EXIT

LDFLAGS="-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.date=$DATE"

# --- 1. Cross-compile -------------------------------------------------------
for target in $TARGETS; do
  os="${target%/*}"
  arch="${target#*/}"
  bin_dir="$STAGE_ROOT/${os}_${arch}/bin"
  mkdir -p "$bin_dir"
  log "building $os/$arch"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags="$LDFLAGS" -o "$bin_dir/discord-mcp" ./cmd/discord-mcp
done

# --- 2. Regenerate the tools array from the host binary ----------------------
TOOLS_JSON=""
host_os="$(go env GOOS)"
host_arch="$(go env GOARCH)"
host_bin="$STAGE_ROOT/${host_os}_${host_arch}/bin/discord-mcp"
if [ -x "$host_bin" ]; then
  raw="$STAGE_ROOT/tools.raw.json"
  # stdin from /dev/null so a binary without the flag can never block on stdio.
  if "$host_bin" --list-tools-json </dev/null >"$raw" 2>/dev/null; then
    if TOOLS_JSON="$(jq -c '
        (if type == "object" and has("tools") then .tools else . end)
        | if type == "array" and length > 0 and all(.[]; (.name | type) == "string")
          then map({name: .name, description: ((.description // "") | split("\n")[0])})
          else error("unexpected shape") end' "$raw" 2>/dev/null)"; then
      log "regenerated tools list: $(printf '%s' "$TOOLS_JSON" | jq 'length') tools"
    else
      TOOLS_JSON=""
      log "warning: --list-tools-json output not understood; keeping manifest tools"
    fi
  else
    log "warning: binary does not support --list-tools-json yet; keeping manifest tools"
  fi
else
  log "warning: host $host_os/$host_arch not in target matrix; keeping manifest tools"
fi

# --- 3. Write per-target manifests -----------------------------------------
write_manifest() { # $1=dest dir, $2=platform
  local filter='.version = $v | .compatibility.platforms = [$p]'
  if [ -n "$TOOLS_JSON" ]; then
    jq --arg v "$MANIFEST_VERSION" --arg p "$2" --argjson t "$TOOLS_JSON" \
      "$filter | .tools = \$t" "$SCRIPT_DIR/manifest.json" >"$1/manifest.json"
  else
    jq --arg v "$MANIFEST_VERSION" --arg p "$2" \
      "$filter" "$SCRIPT_DIR/manifest.json" >"$1/manifest.json"
  fi
  cp "$SCRIPT_DIR/icon.png" "$1/icon.png"
}

for target in $TARGETS; do
  os="${target%/*}"
  arch="${target#*/}"
  write_manifest "$STAGE_ROOT/${os}_${arch}" "$os"
done

# --- 4. Validate (offline only) ---------------------------------------------
DXT_CLI=""
if command -v dxt >/dev/null 2>&1; then
  DXT_CLI="dxt"
elif command -v npx >/dev/null 2>&1 && npx --no -- @anthropic-ai/dxt --version >/dev/null 2>&1; then
  # --no: never download; only use an already-installed/cached package.
  DXT_CLI="npx --no -- @anthropic-ai/dxt"
fi
if [ -n "$DXT_CLI" ]; then
  for target in $TARGETS; do
    dir="$STAGE_ROOT/${target%/*}_${target#*/}"
    log "validating $dir/manifest.json"
    $DXT_CLI validate "$dir/manifest.json" >&2 || die "dxt validate failed for $target"
  done
else
  log "dxt CLI not available offline; skipping 'dxt validate' (jq-checked manifests only)"
  for target in $TARGETS; do
    jq -e '.dxt_version and .server.entry_point and (.tools | length > 0)' \
      "$STAGE_ROOT/${target%/*}_${target#*/}/manifest.json" >/dev/null \
      || die "manifest for $target failed basic checks"
  done
fi

# --- 5. Zip -----------------------------------------------------------------
PRODUCED=""
for target in $TARGETS; do
  os="${target%/*}"
  arch="${target#*/}"
  out="$OUT_DIR/discord-mcp_${FILE_VERSION}_${os}_${arch}.dxt"
  rm -f "$out"
  (cd "$STAGE_ROOT/${os}_${arch}" && zip -qrX "$out" manifest.json icon.png bin)
  PRODUCED="$PRODUCED $out"
done

rm -rf "$STAGE_ROOT"
for p in $PRODUCED; do
  printf '%s\n' "$p"
done
