#!/usr/bin/env bash
# rasterize-icon.sh — rebuild all raster + vector icon artifacts from a
# single source SVG. Idempotent; safe to re-run whenever the source changes.
#
# What it touches:
#   1. web/public/favicon.svg              — served as the browser favicon
#   2. web/public/boomtime.svg             — referenced by sidebar/login/register
#   3. extensions/boomtime-watch/BoomtimeWatch/Assets.xcassets/AppIcon.appiconset/AppIcon-1024.png
#   4. extensions/boomtime-watch/BoomtimeWatchWatch/Assets.xcassets/AppIcon.appiconset/AppIcon-1024.png
#
# Requires macOS (uses `qlmanage` for SVG rasterization — no third-party deps).
# On Linux, install `librsvg` (`rsvg-convert`) and this script auto-detects it.
#
# Usage:
#   scripts/rasterize-icon.sh                 # uses ./boomtime.svg
#   scripts/rasterize-icon.sh path/to/logo.svg
#
# Exit codes:
#   0  success
#   1  source SVG missing
#   2  no rasterizer available on PATH

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC="${1:-$REPO_ROOT/boomtime.svg}"
SIZE=1024

WEB_FAVICON="$REPO_ROOT/web/public/favicon.svg"
WEB_BRAND="$REPO_ROOT/web/public/boomtime.svg"
IOS_ICON_DIR="$REPO_ROOT/extensions/boomtime-watch/BoomtimeWatch/Assets.xcassets/AppIcon.appiconset"
WATCH_ICON_DIR="$REPO_ROOT/extensions/boomtime-watch/BoomtimeWatchWatch/Assets.xcassets/AppIcon.appiconset"

if [[ ! -f "$SRC" ]]; then
    echo "error: source SVG not found: $SRC" >&2
    exit 1
fi

echo "==> source:   $SRC"
echo "==> target:   ${SIZE}x${SIZE} PNG for iOS + watchOS App Icons"
echo

# --- Vector: copy SVG straight to the web serving paths -----------------
mkdir -p "$(dirname "$WEB_FAVICON")"
cp -f "$SRC" "$WEB_FAVICON"
cp -f "$SRC" "$WEB_BRAND"
echo "  updated  $WEB_FAVICON"
echo "  updated  $WEB_BRAND"

# --- Raster: pick a rasterizer ------------------------------------------
TMP_PNG="$(mktemp -t boomtime-icon).png"
trap 'rm -f "$TMP_PNG"' EXIT

rasterize() {
    local out="$1"
    if command -v rsvg-convert >/dev/null 2>&1; then
        rsvg-convert -w "$SIZE" -h "$SIZE" "$SRC" -o "$out"
    elif command -v qlmanage >/dev/null 2>&1; then
        # qlmanage writes to <outdir>/<basename>.png; capture and rename.
        local ql_out
        ql_out="$(mktemp -d)"
        qlmanage -t -s "$SIZE" -o "$ql_out" "$SRC" >/dev/null 2>&1
        mv "$ql_out"/*.png "$out"
        rm -rf "$ql_out"
    else
        echo "error: no rasterizer found. Install librsvg (\`brew install librsvg\`)" >&2
        echo "       or run on macOS where \`qlmanage\` is available."          >&2
        exit 2
    fi
}

rasterize "$TMP_PNG"

# Sanity-check the output dimensions.
if command -v sips >/dev/null 2>&1; then
    dims=$(sips -g pixelWidth -g pixelHeight "$TMP_PNG" 2>/dev/null | awk '/pixel(Width|Height)/ {print $2}' | paste -sd x -)
    echo "  rasterized to $dims PNG"
fi

# --- Distribute PNG to iOS + watchOS icon slots -------------------------
mkdir -p "$IOS_ICON_DIR" "$WATCH_ICON_DIR"
cp -f "$TMP_PNG" "$IOS_ICON_DIR/AppIcon-1024.png"
cp -f "$TMP_PNG" "$WATCH_ICON_DIR/AppIcon-1024.png"
echo "  updated  $IOS_ICON_DIR/AppIcon-1024.png"
echo "  updated  $WATCH_ICON_DIR/AppIcon-1024.png"

echo
echo "done. Next time boomtime.svg changes, re-run: task icons"
