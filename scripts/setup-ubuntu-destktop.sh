#!/usr/bin/env bash
set -euo pipefail

SCHEMA="org.gnome.shell.extensions.dash-to-dock"

if ! command -v gsettings >/dev/null 2>&1; then
  echo "Error: gsettings not found. This script must run on a GNOME desktop."
  exit 1
fi

if ! gsettings list-schemas | grep -qx "$SCHEMA"; then
  echo "Error: GNOME Dock schema not found: $SCHEMA"
  echo "Make sure Ubuntu Dock (dash-to-dock) is installed and enabled."
  exit 1
fi

echo "Applying Ubuntu Dock settings..."

# Panel mode on
gsettings set "$SCHEMA" extend-height true

# Show Apps icon at top/start
gsettings set "$SCHEMA" show-apps-at-top true

# Dock at bottom
gsettings set "$SCHEMA" dock-position 'BOTTOM'

# Icons centered
gsettings set "$SCHEMA" dock-alignment 'CENTER'

echo "Done. Current values:"
gsettings get "$SCHEMA" extend-height
gsettings get "$SCHEMA" show-apps-at-top
gsettings get "$SCHEMA" dock-position
gsettings get "$SCHEMA" dock-alignment
