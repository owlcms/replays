#!/bin/bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FFMPEG_SCRIPT="$SCRIPT_DIR/install-ffmpeg-nvenc.sh"
PLUGIN_RELEASE_API="https://api.github.com/repos/dimtpap/obs-pipewire-audio-capture/releases/latest"

echo "=== User Media Setup (FFplay + OBS PipeWire Plugin) ==="

if [ ! -x "$FFMPEG_SCRIPT" ]; then
    chmod +x "$FFMPEG_SCRIPT"
fi

echo
echo "Step 1/4: Install OBS and base tools..."
sudo apt-get update
sudo apt-get install -y software-properties-common curl tar obs-studio pulseaudio-utils

echo
echo "Step 2/4: Install FFmpeg/FFplay via project script..."
sudo "$FFMPEG_SCRIPT"

echo
echo "Step 3/4: Download OBS PipeWire plugin from GitHub releases..."
ASSET_URL="$(curl -fsSL "$PLUGIN_RELEASE_API" | grep -oE 'https://[^\"]*linux-pipewire-audio[^\"]*\.tar\.gz' | head -n1)"

if [ -z "$ASSET_URL" ]; then
    echo "Could not determine latest plugin tarball from: $PLUGIN_RELEASE_API"
    echo "Install manually from: https://github.com/dimtpap/obs-pipewire-audio-capture/releases"
    exit 1
fi

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

PLUGIN_TGZ="$TMPDIR/linux-pipewire-audio.tar.gz"
curl -fL "$ASSET_URL" -o "$PLUGIN_TGZ"

mkdir -p "$HOME/.config/obs-studio/plugins"
tar -xzf "$PLUGIN_TGZ" -C "$HOME/.config/obs-studio/plugins"

echo
echo "Step 4/4: Verify installs..."
ffmpeg -version | head -1
ffplay -version | head -1

if ffmpeg -hide_banner -encoders 2>&1 | grep -q h264_nvenc; then
    echo "✓ NVENC encoder is available"
else
    echo "⚠ NVENC encoder not found (check NVIDIA driver/runtime)"
fi

if find "$HOME/.config/obs-studio/plugins" -type f | grep -qi 'pipewire'; then
    echo "✓ OBS PipeWire plugin files detected in ~/.config/obs-studio/plugins"
else
    echo "⚠ Could not confirm plugin files. Restart OBS and check Sources list."
fi

echo
echo "Setup complete. Restart OBS and add 'Application Audio Capture (PipeWire)'."
