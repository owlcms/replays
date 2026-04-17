#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
FFMPEG_DIR="$REPO_ROOT/video_config/ffmpeg"
FFMPEG_BIN_DIR="$FFMPEG_DIR/ffmpeg-7.1-full_build/bin"

case "${OS:-}" in
  Windows_NT)
    BINARY="$REPO_ROOT/cameras.exe"
    ;;
  *)
    if [[ -x "$REPO_ROOT/cameras.exe" ]]; then
      BINARY="$REPO_ROOT/cameras.exe"
    else
      BINARY="$REPO_ROOT/cameras"
    fi
    ;;
esac

if [[ ! -f "$BINARY" ]]; then
  echo "Built cameras binary not found: $BINARY" >&2
  echo "Build it first, for example: go build -o cameras.exe ./cmd/cameras" >&2
  exit 1
fi

if [[ ! -d "$FFMPEG_DIR" ]]; then
  echo "Local ffmpeg config directory not found: $FFMPEG_DIR" >&2
  exit 1
fi

if [[ ! -f "$FFMPEG_BIN_DIR/ffmpeg.exe" ]]; then
  echo "Local ffmpeg binary not found: $FFMPEG_BIN_DIR/ffmpeg.exe" >&2
  exit 1
fi

export VIDEO_CONFIGDIR="$FFMPEG_DIR"
export VIDEO_FFMPEG_PATH="$FFMPEG_BIN_DIR/ffmpeg.exe"

exec "$BINARY" --configDir "$REPO_ROOT/video_config/cameras" "$@"