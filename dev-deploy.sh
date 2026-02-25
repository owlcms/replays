#!/bin/bash
# Build cameras and replays from source and deploy over the installed versions
# so the control panel launches the development binaries.

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

ARCH=$(uname -m)
case "$ARCH" in
    aarch64) SUFFIX="linux_arm64" ;;
    x86_64)  SUFFIX="linux_amd64" ;;
    *)       echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

CAMERAS_DIR="$HOME/.local/share/owlcms-cameras"
REPLAYS_DIR="$HOME/.local/share/owlcms-replays"

# Find the latest installed version for each
CAMERAS_VER=$(find "$CAMERAS_DIR" -mindepth 1 -maxdepth 1 -type d -printf '%f\n' 2>/dev/null | sort -V | tail -1)
REPLAYS_VER=$(find "$REPLAYS_DIR" -mindepth 1 -maxdepth 1 -type d -printf '%f\n' 2>/dev/null | sort -V | tail -1)

echo "Building cameras_${SUFFIX}..."
go build -o "cameras_${SUFFIX}" ./cmd/cameras

echo "Building replays_${SUFFIX}..."
go build -o "replays_${SUFFIX}" ./cmd/replays

if [[ -n "$CAMERAS_VER" ]]; then
    cp "cameras_${SUFFIX}" "$CAMERAS_DIR/$CAMERAS_VER/cameras_${SUFFIX}"
    echo "Deployed cameras to $CAMERAS_DIR/$CAMERAS_VER/"
else
    echo "No cameras version installed — skipping deploy"
fi

if [[ -n "$REPLAYS_VER" ]]; then
    cp "replays_${SUFFIX}" "$REPLAYS_DIR/$REPLAYS_VER/replays_${SUFFIX}"
    echo "Deployed replays to $REPLAYS_DIR/$REPLAYS_VER/"
else
    echo "No replays version installed — skipping deploy"
fi

echo "Done."
