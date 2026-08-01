#!/bin/bash
# Build the video application from source and deploy it over the installed
# version so the control panel launches the development binary.

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

ARCH=$(uname -m)
case "$ARCH" in
    aarch64) SUFFIX="linux_arm64" ;;
    x86_64)  SUFFIX="linux_amd64" ;;
    *)       echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

VIDEO_DIR="$HOME/.local/share/owlcms-video"

# Find the latest installed version
VIDEO_VER=$(find "$VIDEO_DIR" -mindepth 1 -maxdepth 1 -type d -printf '%f\n' 2>/dev/null | sort -V | tail -1)

echo "Building video_${SUFFIX}..."
go build -o "video_${SUFFIX}" ./cmd/video

if [[ -n "$VIDEO_VER" ]]; then
    cp "video_${SUFFIX}" "$VIDEO_DIR/$VIDEO_VER/video_${SUFFIX}"
    echo "Deployed video to $VIDEO_DIR/$VIDEO_VER/"
else
    echo "No video version installed — skipping deploy"
fi

echo "Done."
